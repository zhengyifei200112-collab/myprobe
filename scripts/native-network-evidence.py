"""Validate actual Go native events; never count skips or missing tests as proof."""
import argparse
import hashlib
import json
import math
import platform
import re
import subprocess
from pathlib import Path

PACKAGE = 'github.com/zhengyifei200112-collab/myprobe/internal/agentclient'
REQUIRED = {
    'TestNetworkProbeRealTCPAddressFamilies/tcp4',
    'TestNetworkProbeRealTCPAddressFamilies/tcp6',
    'TestNetworkProbeNativeICMPEvidence/ipv4',
    'TestNetworkProbeNativeICMPEvidence/ipv6',
    'TestNetworkProbeThroughActualAgentAndGateway/tcp4',
    'TestNetworkProbeThroughActualAgentAndGateway/tcp6',
    'TestNetworkProbeThroughActualAgentAndGateway/bounded_busy',
    'TestNetworkProbeThroughActualAgentAndGateway/icmp4_three_packets',
    'TestNetworkProbeThroughActualAgentAndGateway/icmp6_three_packets',
}
NATIVE = re.compile(r'native=(\w+) family=(ipv[46]) sent=(\d+) received=(\d+) rtt_ms=([0-9.]+)')

def validate_events(events, expected_os):
    passed, bad, packets = set(), set(), {}
    package_passed = False
    for event in events:
        if event.get('Package') != PACKAGE:
            continue
        name, action = event.get('Test'), event.get('Action')
        if action == 'pass' and not name:
            package_passed = True
        if action == 'fail':
            raise ValueError('Native package contains a failed test')
        if name in REQUIRED:
            if action == 'pass': passed.add(name)
            if action == 'skip': bad.add(name)
        match = NATIVE.search(event.get('Output', ''))
        if match:
            os_name, family, sent, received, rtt = match.groups()
            if name != 'TestNetworkProbeNativeICMPEvidence/' + family or os_name != expected_os or family in packets:
                raise ValueError('Native output identity/OS is inconsistent')
            mean = float(rtt)
            if int(sent) != 2 or int(received) != 2 or not math.isfinite(mean) or mean < 0:
                raise ValueError('Actual loopback packet evidence is incomplete')
            packets[family] = {'sent': int(sent), 'received': int(received), 'rtt_ms': mean}
    if bad or REQUIRED - passed or not package_passed or set(packets) != {'ipv4', 'ipv6'}:
        raise ValueError('Missing/skipped native proof: ' + ','.join(sorted((REQUIRED - passed) | bad)))
    return {'passed_tests': sorted(passed), 'loopback_icmp': packets,
            'scheduled_icmp_packets_verified_by_actual_agent_store_tests': 3}

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('events', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--source', required=True)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    args = parser.parse_args()
    if args.events.stat().st_size > 10 * 1024 * 1024:
        raise ValueError('Unexpectedly large event stream')
    raw = args.events.read_bytes()
    proof = validate_events([json.loads(line) for line in raw.splitlines() if line.strip()], 'darwin')
    machine = platform.machine().lower()
    actual_arch = {'arm64': 'arm64', 'aarch64': 'arm64', 'x86_64': 'amd64'}.get(machine)
    if platform.system() != 'Darwin' or actual_arch != args.arch:
        raise ValueError('Actual native runner platform differs from requested platform')
    source = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True, timeout=5).strip()
    if not re.fullmatch('[a-f0-9]{40}', args.source) or source != args.source:
        raise ValueError('Checkout does not match the reviewed source')
    go = subprocess.check_output(['go', 'version'], text=True, timeout=5).strip()
    if not go.endswith('darwin/' + args.arch):
        raise ValueError('Go platform does not match actual native platform')
    proof.update({'format': 'myprobe.darwin-network-native.v1', 'source_commit': source,
                  'os': 'darwin', 'arch': args.arch, 'os_version': subprocess.check_output(['sw_vers', '-productVersion'], text=True, timeout=5).strip(),
                  'go': go, 'events_sha256': hashlib.sha256(raw).hexdigest(),
                  'scope': 'owned IPv4/IPv6 loopback TCP/ICMP and actual Agent/Gateway/Store/ACK; no external hosts or silence/no-route proof',
                  'source_sha256': {str(p): hashlib.sha256(p.read_bytes()).hexdigest() for p in map(Path, [
                      'internal/agentclient/network_probe.go', 'internal/agentclient/network_probe_ping.go',
                      'internal/agentclient/network_probe_test.go', 'internal/agentclient/network_evidence_e2e_test.go'])}})
    args.output.write_text(json.dumps(proof, indent=2) + '\n')
    print(json.dumps({'source_commit': source, 'arch': args.arch, 'required_passes': len(proof['passed_tests'])}))

if __name__ == '__main__':
    main()
