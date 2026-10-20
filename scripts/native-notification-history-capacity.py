"""Run the optional Store history workload in its own bounded Linux unit."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import sys
import time


def digest(path):
    with path.open('rb') as source:
        return hashlib.file_digest(source, 'sha256').hexdigest()


def save(path, value):
    with path.open('x') as target:
        json.dump(value, target, indent=2)
        target.write('\n')
        target.flush()
        os.fsync(target.fileno())
    path.chmod(0o600)


def state(unit):
    output = subprocess.check_output([
        'systemctl', 'show', unit,
        '--property=LoadState,MainPID,Result,ExecMainStatus,SubState,ControlGroup',
    ], text=True)
    return dict(line.split('=', 1) for line in output.splitlines())


def main():
    assert platform.system() == 'Linux' and len(sys.argv) == 3
    repo = Path(__file__).resolve().parents[1]
    private, evidence = (Path(value).resolve() for value in sys.argv[1:])
    assert private.is_absolute() and evidence.is_absolute()
    private.mkdir(mode=0o700)
    evidence.mkdir(mode=0o700)
    assert os.statvfs(private).f_bavail * os.statvfs(private).f_frsize > 4 * (1 << 30)
    head = os.environ['EXPECTED_SOURCE']
    assert re.fullmatch('[0-9a-f]{40}', head)
    assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=repo, text=True).strip() == head
    assert not subprocess.check_output(['git', 'status', '--porcelain'], cwd=repo).strip()
    names = subprocess.check_output(['git', 'ls-files', '-z'], cwd=repo).decode().rstrip('\0').split('\0')
    # Check actual build inputs against the exact commit, including the fixture.
    blobs = subprocess.check_output(['git', 'cat-file', '--batch'], cwd=repo,
                                    input=''.join(head + ':' + name + '\n' for name in names).encode())
    offset, hashes = 0, {}
    for name in names:
        newline = blobs.index(b'\n', offset)
        header = blobs[offset:newline].decode().split()
        assert header[1] == 'blob'
        size = int(header[2])
        expected = hashlib.sha256(blobs[newline + 1:newline + 1 + size]).hexdigest()
        path = repo / name
        assert not path.is_symlink() and digest(path) == expected, name
        hashes[name] = expected
        offset = newline + size + 2
    assert offset == len(blobs)
    save(evidence / 'source-tree-sha256.json', hashes)
    binary = private / 'notification-history.test'
    env = {**os.environ, 'CGO_ENABLED': '0'}
    subprocess.run(['go', 'test', '-c', '-o', str(binary), './internal/store'], cwd=repo, env=env, check=True)
    executable_sha = digest(binary)
    save(evidence / 'build-manifest.json', {
        'source_commit': head, 'tracked_files_checked': len(hashes),
        'source_map_sha256': digest(evidence / 'source-tree-sha256.json'),
        'test_executable_sha256': executable_sha,
        'go_version': subprocess.check_output(['go', 'version'], text=True).strip(),
        'physical_memory_bytes': os.sysconf('SC_PHYS_PAGES') * os.sysconf('SC_PAGE_SIZE'),
        'physical_two_gib_host_proven': False,
    })
    tmp = private / 'tmp'
    tmp.mkdir(mode=0o700)
    unit = 'myprobe-notification-history-' + head[:12] + '.service'
    assert state(unit)['LoadState'] == 'not-found'
    subprocess.run([
        'sudo', '-n', 'systemd-run', '--quiet', '--unit=' + unit,
        '--property=Type=exec', '--property=RemainAfterExit=yes',
        '--property=User=' + str(os.getuid()), '--property=Group=' + str(os.getgid()),
        '--property=CPUQuota=200%', '--property=AllowedCPUs=0,1',
        '--property=MemoryMax=2G', '--property=MemorySwapMax=0',
        '--property=PrivateNetwork=yes', '--property=ProtectSystem=strict',
        '--property=ProtectHome=read-only', '--property=ReadWritePaths=' + str(private),
        '--property=ReadOnlyPaths=' + str(binary),
        '--property=WorkingDirectory=' + str(repo / 'internal/store'),
        '--property=RuntimeMaxSec=18m',
        '--property=StandardOutput=file:' + str(private / 'test.log'),
        '--property=StandardError=inherit',
        '--setenv=TMPDIR=' + str(tmp),
        '--setenv=MYPROBE_TEST_NOTIFICATION_HISTORY_CAPACITY=1',
        '--setenv=MYPROBE_TEST_NOTIFICATION_HISTORY_OUTPUT=' + str(private),
        str(binary), '-test.run=^TestNotificationPermanentHistoryCapacity$',
        '-test.count=1', '-test.timeout=17m', '-test.v',
    ], check=True)
    samples, identity = [], None
    deadline = time.monotonic() + 19 * 60
    while time.monotonic() < deadline:
        current = state(unit)
        if current['MainPID'] == '0':
            break
        group = Path('/sys/fs/cgroup') / current['ControlGroup'].lstrip('/')
        process = Path('/proc') / current['MainPID']
        try:
            limits = {name: (group / name).read_text().strip() for name in (
                'cpu.max', 'memory.max', 'memory.swap.max', 'cpuset.cpus.effective')}
            assert limits == {'cpu.max': '200000 100000', 'memory.max': '2147483648',
                              'memory.swap.max': '0', 'cpuset.cpus.effective': '0-1'}
            executable = digest(process / 'exe')
            assert executable == executable_sha
            stat = (process / 'stat').read_text()
            observed = {'pid': int(current['MainPID']), 'start_ticks': int(stat[stat.rfind(')') + 2:].split()[19]),
                        'binary_sha256': executable, 'namespace': os.readlink(process / 'ns/net')}
            assert observed['namespace'] != os.readlink('/proc/self/ns/net')
            if identity is None:
                identity = observed
            else:
                assert identity == observed
            sample = {'at': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'limits': limits}
            for name in ('memory.current', 'memory.peak', 'memory.events', 'memory.swap.current', 'cpu.stat'):
                sample[name] = (group / name).read_text().strip()
            events = dict(line.split() for line in sample['memory.events'].splitlines())
            assert events['oom'] == events['oom_kill'] == '0' and sample['memory.swap.current'] == '0'
            samples.append(sample)
        except (FileNotFoundError, ProcessLookupError):
            # A normal exit can remove /proc and cgroup files between reads.
            if state(unit)['MainPID'] == '0':
                break
            time.sleep(0.1)
            continue
        time.sleep(2)
    else:
        raise TimeoutError('native observation deadline; inspect the original unit, never retry')
    terminal = state(unit)
    assert terminal['LoadState'] == 'loaded' and terminal['SubState'] == 'exited'
    assert terminal['MainPID'] == '0' and terminal['Result'] == 'success' and terminal['ExecMainStatus'] == '0'
    assert identity is not None and samples
    if terminal['ControlGroup']:
        group = Path('/sys/fs/cgroup') / terminal['ControlGroup'].lstrip('/')
        assert all(not item.read_text().strip() for item in group.rglob('cgroup.procs'))
    assert digest(binary) == executable_sha
    assert all(digest(repo / name) == sha for name, sha in hashes.items())
    result = json.loads((private / 'notification-history-capacity.json').read_text())
    assert result['passed'] and result['maximum_historical_incidents'] == 100000
    assert result['backup_run_pruning_exercised'] and result['backup_run_history_limit'] == 1000
    assert result['protected_pending_reminder_preserved_and_retried_after_restore']
    assert [level['historical_incidents'] for level in result['levels']] == [1000, 10000, 100000]
    for level in result['levels']:
        observations = level['reads_with_concurrent_reports']
        assert observations['reports']['successful'] > 0 and not observations['reports']['error']
        assert all(value['samples'] >= 100 for name, value in observations.items() if name != 'reports')
    save(evidence / 'result.json', result)
    save(evidence / 'native-proof.json', {
        'passed': True, 'source_commit': head, 'terminal_unit': terminal,
        'live_test_identity': identity, 'samples': samples,
        'result_sha256': digest(evidence / 'result.json'),
        'owning_processes_exited': True, 'source_and_binary_rechecked': True,
        'scope': 'Bounded Store component and synthetic finished-history growth. No real Provider, Agent/browser, full50,24h or physical2GiB VM proof.',
    })
    print(json.dumps({'passed': True, 'source': head, 'historical_incidents': 100000,
                      'tables': 10, 'actual_nodes': 4, 'resource_samples': len(samples)}))


if __name__ == '__main__':
    main()
