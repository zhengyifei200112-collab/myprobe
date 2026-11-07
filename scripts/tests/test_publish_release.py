"""Exercise the real publisher against owned Git fixtures and fake CLI transports."""
import base64
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest


SCRIPT = Path(__file__).resolve().parents[1] / "publish-release.py"
PROGRAMS = [
    "myprobe-agent-darwin-amd64", "myprobe-agent-darwin-arm64",
    "myprobe-agent-linux-amd64", "myprobe-agent-linux-arm64", "myprobe-agent-windows-amd64.exe",
    "myprobe-rollback-prepare-linux-amd64", "myprobe-rollback-prepare-linux-arm64", "myprobe-rollback-prepare-windows-amd64.exe",
    "myprobe-server-linux-amd64", "myprobe-server-linux-arm64", "myprobe-server-windows-amd64.exe",
]
EXPECTED = set(PROGRAMS + ["SHA256SUMS", "release-manifest.json"])
FAKE = r'''#!/usr/bin/env python3
import base64,copy,hashlib,json,os,sys
from pathlib import Path
p=Path(os.environ['MYPROBE_RELEASE_FIXTURE_STATE']);s=json.loads(p.read_text());cmd=Path(sys.argv[0]).name;a=sys.argv[1:]
def save():
 t=p.with_suffix('.new');t.write_text(json.dumps(s));t.replace(p)
def event(kind,**values):s['events'].append({'kind':kind,**values})
def fail(kind,name=''):
 f=s.get('fail')
 if f and f['kind']==kind and (not f.get('name') or f['name']==name):
  s['fail']=None;event('failure',operation=kind,name=name);save();print('fixture failure '+os.environ['GH_TOKEN'],file=sys.stderr);raise SystemExit(1)
def info():
 if s['release'] is None:return None
 v=copy.deepcopy(s['release']);v['assets']=[{'name':n,'size':len(base64.b64decode(b)),'digest':'sha256:'+hashlib.sha256(base64.b64decode(b)).hexdigest()} for n,b in s['assets'].items()];return v
if cmd=='go':
 assert a==['version'];event('toolchain');print('go version go1.26.5 linux/amd64')
elif cmd=='gh':
 if a[0]=='api':
  endpoint=a[-1];event('api',endpoint=endpoint)
  status=s.get('lookup_status')
  if status:code=status;value={'message':'fixture API failure'}
  else:
   value=s['latest'] if endpoint.endswith('/releases/latest') else info();code=200 if value else 404
  save();print('HTTP/2.0 '+str(code)+' Fixture\r\nContent-Type: application/json\r\n\r\n'+json.dumps(value or {'message':'Not Found'}));raise SystemExit(0 if code==200 else 1)
 assert a[0]=='release'
 operation=a[1]
 if operation=='view':
  save();print(json.dumps(info()));raise SystemExit(0 if s['release'] is not None else 1)
 elif operation=='create':
  event('create',args=a);fail('create');assert s['release'] is None
  s['release']={'tag_name':a[2],'draft':'--draft' in a,'prerelease':'--prerelease' in a,'target_commitish':a[a.index('--target')+1] if '--target' in a else 'main'}
 elif operation=='upload':
  sources=[]
  for value in a[3:]:
   if value.startswith('--'):break
   sources.append(Path(value))
  for source in sources:
   name=source.name;event('upload',name=name,args=a)
   if '--clobber' in a and name in s['assets']:event('delete-asset',name=name);del s['assets'][name]
   f=s.get('fail')
   if f and f['kind']=='upload' and f.get('name')==name and f.get('after_apply'):
    assert name not in s['assets'];s['assets'][name]=base64.b64encode(source.read_bytes()).decode();fail('upload',name)
   fail('upload',name);assert name not in s['assets'];s['assets'][name]=base64.b64encode(source.read_bytes()).decode()
 elif operation=='download':
  name=a[a.index('--pattern')+1];event('download',name=name);fail('download',name)
  assert name in s['assets'];target=Path(a[a.index('--dir')+1])/name;assert not target.exists();target.write_bytes(base64.b64decode(s['assets'][name]))
 elif operation=='edit':
  event('publish',assets=sorted(s['assets']),registry=sorted(s['registry']),args=a)
  f=s.get('fail');after=f and f['kind']=='publish' and f.get('after_apply')
  if not after:fail('publish')
  assert '--draft=false' in a;s['release']['draft']=False;s['release']['prerelease']='--prerelease=true' in a
  if '--latest' in a:s['latest']=copy.deepcopy(s['release'])
  if after:fail('publish')
 else:raise AssertionError('Unexpected gh operation '+str(a))
elif cmd=='docker':
 operation=a[0]
 if operation=='login':assert sys.stdin.read();event('login')
 elif operation=='build':
  role=a[a.index('--target')+1];event('build',role=role);fail('build',role)
  label=a[a.index('--build-arg')+1];assert label=='VERSION='+s['tag']
  image_id='sha256:'+hashlib.sha256((role+':'+s['tag']).encode()).hexdigest()
  for n,v in enumerate(a):
   if v=='--tag':s['local'][a[n+1]]=image_id
 elif operation=='image':
  assert a[1]=='inspect';ref=a[2];assert ref in s['local'];value=s['local'][ref];event('inspect',ref=ref)
  if a[-1]=='{{.Id}}':print(value)
  elif a[-1]=='{{json .RepoDigests}}':print(json.dumps(s['digests'][value]))
  else:raise AssertionError('Unexpected inspect format')
 elif operation=='push':
  ref=a[1];event('push',ref=ref);fail('push',ref);assert ref in s['local'];image_id=s['local'][ref];s['registry'][ref]=image_id
  image=ref.rsplit(':',1)[0];pin=image+'@sha256:'+hashlib.sha256(('registry:'+image_id).encode()).hexdigest();s['registry'][pin]=image_id;s['digests'][image_id]=[pin]
 elif operation=='pull':
  ref=a[1];event('pull',ref=ref);fail('pull',ref);assert ref in s['registry'];s['local'][ref]=s['registry'][ref]
 elif operation=='tag':
  event('tag',source=a[1],target=a[2]);assert a[1] in s['local'];s['local'][a[2]]=s['local'][a[1]]
 else:raise AssertionError('Unexpected docker operation '+str(a))
else:raise AssertionError('Unexpected transport '+cmd)
save()
'''


@unittest.skipUnless(os.name == "posix", "The tag publisher runs on the Linux release runner")
class ReleasePublicationTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="myprobe-release-test-")
        self.root = Path(self.temp.name).resolve()
        self.work = self.root / "work"
        self.work.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for command in ("gh", "docker", "go"):
            file = self.bin / command
            file.write_text(FAKE)
            file.chmod(0o700)
        self.env = {**os.environ, "PATH": str(self.bin) + os.pathsep + os.environ["PATH"], "GH_TOKEN": "fixture-secret-never-use-real-credentials", "GITHUB_TOKEN": "fixture-only", "GITHUB_REPOSITORY": "example/myprobe", "GITHUB_ACTOR": "fixture", "GITHUB_REF_TYPE": "tag", "GITHUB_REF_NAME": "v0.3.0", "PYTHONDONTWRITEBYTECODE": "1"}
        self.git("init", "-b", "main")
        self.git("config", "user.name", "release fixture")
        self.git("config", "user.email", "fixture@example.invalid")
        (self.work / "tracked.txt").write_text("owned release fixture\n")
        self.git("add", "tracked.txt")
        self.git("commit", "-m", "fixture")
        self.commit = self.git("rev-parse", "HEAD")
        self.git("update-ref", "refs/remotes/origin/main", self.commit)
        self.git("tag", "-a", self.env["GITHUB_REF_NAME"], "-m", "fixture annotated tag")
        self.env["GITHUB_SHA"] = self.commit
        self.state_file = self.root / "state.json"
        self.env["MYPROBE_RELEASE_FIXTURE_STATE"] = str(self.state_file)
        self.put_state({"tag": self.env["GITHUB_REF_NAME"], "release": None, "latest": {"tag_name": "v0.2.1", "draft": False, "prerelease": False}, "assets": {}, "local": {}, "registry": {}, "digests": {}, "events": [], "fail": None})
        self.write_assets()

    def tearDown(self):
        # Only this newly-created, known test root is recursively retired.
        self.assertTrue(self.root.name.startswith("myprobe-release-test-"))
        self.assertEqual(self.root.parent, Path(tempfile.gettempdir()).resolve())
        self.temp.cleanup()

    def git(self, *args):
        return subprocess.check_output(["git", *args], cwd=self.work, env=getattr(self, "env", None), text=True, stderr=subprocess.DEVNULL).strip()

    def state(self):
        return json.loads(self.state_file.read_text())

    def put_state(self, value):
        self.state_file.write_text(json.dumps(value))

    def write_assets(self):
        dist = self.work / "dist"
        dist.mkdir(exist_ok=True)
        lines = []
        for name in PROGRAMS:
            data = ("owned fixture payload for " + name + "\n").encode()
            (dist / name).write_bytes(data)
            lines.append(hashlib.sha256(data).hexdigest() + "  " + name)
        (dist / "SHA256SUMS").write_text("\n".join(sorted(lines)) + "\n")

    def call(self, phase="publish", success=True, extra=()):
        result = subprocess.run([sys.executable, str(SCRIPT), phase, *extra], cwd=self.work, env=self.env, capture_output=True, text=True, timeout=120)
        self.assertEqual(result.returncode == 0, success, result.stdout + result.stderr)
        self.assertNotIn(self.env["GH_TOKEN"], result.stdout + result.stderr)
        return result

    def fail_once(self, kind, name=None, after_apply=False):
        state = self.state()
        state["fail"] = {"kind": kind, "name": name, "after_apply": after_apply}
        self.put_state(state)

    def complete(self):
        self.call()
        state = self.state()
        self.assertFalse(state["release"]["draft"])
        self.assertEqual(set(state["assets"]), EXPECTED)
        return state

    def test_all_artifacts_and_versioned_images_precede_publication(self):
        state = self.complete()
        events = state["events"]
        index = next(n for n, v in enumerate(events) if v["kind"] == "publish")
        self.assertEqual(set(events[index]["assets"]), EXPECTED)
        for role in ("server", "agent"):
            image = "ghcr.io/example/myprobe" + ("-agent" if role == "agent" else "")
            for tag in ("v0.3.0", "0.3.0"):
                self.assertIn(image + ":" + tag, events[index]["registry"])
            self.assertTrue(any(v["kind"] == "pull" and v["ref"] == image + ":v0.3.0" for v in events[:index]))
            latest = next(n for n, v in enumerate(events) if v["kind"] == "push" and v["ref"] == image + ":latest")
            self.assertGreater(latest, index)
        self.assertTrue(all("--clobber" not in v.get("args", []) for v in events))
        created = next(v for v in events if v["kind"] == "create")
        self.assertIn("--draft", created["args"])
        self.assertIn("--verify-tag", created["args"])

    def test_image_push_failure_keeps_draft_unpublished(self):
        self.fail_once("push", "ghcr.io/example/myprobe-agent:v0.3.0")
        self.call(success=False)
        state = self.state()
        self.assertTrue(state["release"]["draft"])
        self.assertFalse(any(v["kind"] == "publish" or (v["kind"] == "push" and v["ref"].endswith(":latest")) for v in state["events"]))

    def test_lost_upload_response_resumes_without_replacing_original_assets(self):
        self.fail_once("upload", PROGRAMS[2], after_apply=True)
        self.call(success=False)
        partial = self.state()
        self.assertTrue(partial["release"]["draft"])
        self.assertIn("release-manifest.json", partial["assets"])
        built = sum(v["kind"] == "build" for v in partial["events"])
        self.call("plan")
        final = self.complete()
        self.assertEqual(sum(v["kind"] == "build" for v in final["events"]), built)
        for name, data in partial["assets"].items():
            self.assertEqual(final["assets"][name], data)
            self.assertEqual(sum(v["kind"] == "upload" and v["name"] == name for v in final["events"]), 1)

    def test_lost_publication_response_reuses_published_assets(self):
        self.fail_once("publish", after_apply=True)
        self.call(success=False)
        first = self.state()
        self.assertFalse(first["release"]["draft"])
        self.assertFalse(any(v["kind"] == "push" and v["ref"].endswith(":latest") for v in first["events"]))
        planned = json.loads(self.call("plan").stdout)
        self.assertFalse(planned["needs_build"])
        dist = self.work / "dist"
        self.assertEqual(dist.resolve().parent, self.work)
        shutil.rmtree(dist)
        final = self.complete()
        self.assertEqual(final["assets"], first["assets"])
        new_events = final["events"][len(first["events"]):]
        self.assertFalse(any(v["kind"] in ("upload", "build", "publish", "toolchain") for v in new_events))

    def test_failed_latest_promotion_can_retry_without_republishing(self):
        self.fail_once("push", "ghcr.io/example/myprobe-agent:latest")
        self.call(success=False)
        first = self.state()
        self.assertFalse(first["release"]["draft"])
        self.assertEqual(set(first["assets"]), EXPECTED)
        final = self.complete()
        self.assertEqual(final["assets"], first["assets"])
        for image in ("ghcr.io/example/myprobe", "ghcr.io/example/myprobe-agent"):
            self.assertEqual(final["registry"][image + ":latest"], final["registry"][image + ":v0.3.0"])
        self.assertFalse(any(v["kind"] in ("upload", "build", "publish") for v in final["events"][len(first["events"]):]))

    def test_published_asset_tampering_stops_without_repairing_it(self):
        state = self.complete()
        state["assets"][PROGRAMS[0]] = base64.b64encode(b"changed remote asset").decode()
        state["events"] = []
        self.put_state(state)
        self.call(success=False)
        after = self.state()
        self.assertEqual(after["assets"], state["assets"])
        self.assertFalse(any(v["kind"] in ("upload", "build", "publish", "push") for v in after["events"]))

    def test_unknown_published_legacy_release_is_not_overwritten(self):
        state = self.state()
        state["release"] = {"tag_name": "v0.3.0", "draft": False, "prerelease": False}
        state["assets"][PROGRAMS[0]] = base64.b64encode(b"original published bytes").decode()
        self.put_state(state)
        self.call(success=False)
        self.assertEqual(self.state()["assets"], state["assets"])

    def test_existing_draft_payload_conflict_is_retained_before_image_build(self):
        state = self.state()
        state["release"] = {"tag_name": "v0.3.0", "draft": True, "prerelease": False}
        state["assets"][PROGRAMS[0]] = base64.b64encode(b"existing different draft bytes").decode()
        self.put_state(state)
        self.call(success=False)
        after = self.state()
        self.assertEqual(after["assets"], state["assets"])
        self.assertFalse(any(v["kind"] in ("build", "upload", "publish") for v in after["events"]))

    def test_lookup_server_failure_is_not_treated_as_absence(self):
        state = self.state()
        state["lookup_status"] = 503
        self.put_state(state)
        self.call(success=False)
        self.assertIsNone(self.state()["release"])
        self.assertFalse(any(v["kind"] in ("create", "build", "upload", "publish") for v in self.state()["events"]))

    def test_wrong_source_commit_stops_before_remote_actions(self):
        self.env["GITHUB_SHA"] = "0" * 40
        self.call(success=False)
        self.assertEqual(self.state()["events"], [])

    def test_unmerged_tag_stops_before_remote_actions(self):
        self.git("switch", "-c", "feature/unmerged")
        (self.work / "tracked.txt").write_text("unmerged source\n")
        self.git("add", "tracked.txt")
        self.git("commit", "-m", "unmerged fixture")
        tag = "v0.3.1"
        self.git("tag", "-a", tag, "-m", "unmerged annotated tag")
        self.env["GITHUB_REF_NAME"] = tag
        self.env["GITHUB_SHA"] = self.git("rev-parse", "HEAD")
        self.call(success=False)
        self.assertEqual(self.state()["events"], [])

    def test_lightweight_tag_stops_before_remote_actions(self):
        self.git("tag", "v0.3.1")
        self.env["GITHUB_REF_NAME"] = "v0.3.1"
        self.call(success=False)
        self.assertEqual(self.state()["events"], [])

    def test_workflow_outputs_select_build_or_published_resume(self):
        before = self.root / "new-output.txt"
        self.call("plan", extra=("--output", str(before)))
        self.assertEqual(before.read_text().splitlines(), ["needs_build=true", "release_state=new"])
        self.assertIsNone(self.state()["release"])
        self.complete()
        after = self.root / "published-output.txt"
        self.call("plan", extra=("--output", str(after)))
        self.assertEqual(after.read_text().splitlines(), ["needs_build=false", "release_state=published"])

    def test_incomplete_program_inventory_stops_before_draft_creation(self):
        (self.work / "dist" / PROGRAMS[-1]).unlink()
        self.call(success=False)
        self.assertIsNone(self.state()["release"])
        self.assertFalse(any(v["kind"] in ("create", "build", "upload") for v in self.state()["events"]))

    def test_prerelease_never_promotes_stable_latest(self):
        tag = "v0.3.0-rc.1"
        self.git("tag", "-a", tag, "-m", "fixture prerelease")
        self.env["GITHUB_REF_NAME"] = tag
        state = self.state()
        state["tag"] = tag
        self.put_state(state)
        final = self.complete()
        self.assertTrue(final["release"]["prerelease"])
        self.assertEqual(final["latest"]["tag_name"], "v0.2.1")
        self.assertFalse(any(v["kind"] == "push" and v["ref"].endswith(":latest") for v in final["events"]))

    def test_older_stable_release_does_not_move_latest_backwards(self):
        state = self.state()
        state["latest"]["tag_name"] = "v1.0.0"
        self.put_state(state)
        final = self.complete()
        self.assertEqual(final["latest"]["tag_name"], "v1.0.0")
        self.assertFalse(any(v["kind"] == "push" and v["ref"].endswith(":latest") for v in final["events"]))

    def test_changed_versioned_image_blocks_ready_draft_publication(self):
        state = self.complete()
        state["release"]["draft"] = True
        state["latest"] = {"tag_name": "v0.2.1", "draft": False, "prerelease": False}
        state["registry"]["ghcr.io/example/myprobe-agent:v0.3.0"] = "sha256:" + "f" * 64
        state["events"] = []
        self.put_state(state)
        self.call(success=False)
        self.assertTrue(self.state()["release"]["draft"])
        self.assertFalse(any(v["kind"] in ("upload", "build", "publish", "push") for v in self.state()["events"]))

    def test_rebuilt_partial_candidate_cannot_replace_previous_bytes(self):
        self.fail_once("upload", PROGRAMS[2])
        self.call(success=False)
        first = self.state()
        (self.work / "dist" / PROGRAMS[-1]).write_bytes(b"different rebuilt bytes")
        lines = [hashlib.sha256((self.work / "dist" / name).read_bytes()).hexdigest() + "  " + name for name in PROGRAMS]
        (self.work / "dist" / "SHA256SUMS").write_text("\n".join(sorted(lines)) + "\n")
        self.call(success=False)
        final = self.state()
        self.assertEqual(final["assets"], first["assets"])
        self.assertFalse(any(v["kind"] in ("upload", "build", "publish", "push") for v in final["events"][len(first["events"]):]))

    def test_readback_failure_keeps_candidate_unpublished(self):
        self.fail_once("download", PROGRAMS[-1])
        self.call(success=False)
        first = self.state()
        self.assertTrue(first["release"]["draft"])
        self.assertEqual(set(first["assets"]), EXPECTED)
        self.assertFalse(any(v["kind"] == "publish" for v in first["events"]))
        final = self.complete()
        self.assertEqual(final["assets"], first["assets"])


if __name__ == "__main__":
    unittest.main()
