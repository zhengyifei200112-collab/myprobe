#!/usr/bin/env python3
"""Prepare/complete a release without replacing any existing release asset.

Only the tag workflow invokes this script. GitHub/GHCR cannot be published in one
transaction: versioned images and verified assets precede publication; latest is
an idempotent, separately retryable promotion of a complete published release.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile


class ReleaseError(Exception):
    pass


def require(value, message):
    if not value:
        raise ReleaseError(message)


def digest(path):
    with path.open("rb") as handle:
        return hashlib.file_digest(handle, "sha256").hexdigest()


def program_names():
    names = []
    for os_name, arch in (("linux", "amd64"), ("linux", "arm64"), ("windows", "amd64")):
        for role in ("server", "agent", "rollback-prepare"):
            suffix = ".exe" if os_name == "windows" else ""
            names.append(f"myprobe-{role}-{os_name}-{arch}{suffix}")
    return sorted(names + [f"myprobe-agent-darwin-{arch}" for arch in ("amd64", "arm64")])


PROGRAMS = program_names()
PAYLOADS = PROGRAMS + ["SHA256SUMS"]
MANIFEST = "release-manifest.json"
SHA = re.compile(r"[0-9a-f]{64}\Z")
IMAGE_ID = re.compile(r"sha256:[0-9a-f]{64}\Z")
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?\Z")


def version(tag):
    match = TAG.fullmatch(tag)
    require(match is not None and len(tag) <= 128, "A Docker-compatible SemVer vX.Y.Z[-prerelease] tag is required")
    pre = match.group(4)
    require(not pre or all(not part.isdigit() or part == "0" or not part.startswith("0") for part in pre.split(".")), "Invalid numeric prerelease identifier")
    return tuple(int(match.group(n)) for n in (1, 2, 3)), bool(pre)


def run(args, *, operation, input_text=None, allow_failure=False):
    try:
        result = subprocess.run(args, input=input_text, capture_output=True, text=True, timeout=900)
    except (OSError, subprocess.TimeoutExpired) as error:
        raise ReleaseError(f"{operation}: {type(error).__name__}; inspect the existing remote state before retrying") from error
    if result.returncode and not allow_failure:
        detail = result.stderr[-4000:]
        for key in ("GH_TOKEN", "GITHUB_TOKEN"):
            secret = os.environ.get(key)
            if secret:
                detail = detail.replace(secret, "[REDACTED]")
        raise ReleaseError(f"{operation} failed (exit {result.returncode}); existing assets are retained. {detail.strip()}")
    return result


class Publisher:
    def __init__(self):
        self.repo = os.environ.get("GITHUB_REPOSITORY", "")
        self.tag = os.environ.get("GITHUB_REF_NAME", "")
        self.commit = os.environ.get("GITHUB_SHA", "")
        require(re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", self.repo), "GITHUB_REPOSITORY is required")
        require(re.fullmatch(r"[0-9a-f]{40}", self.commit), "Exact GITHUB_SHA is required")
        require(os.environ.get("GITHUB_REF_TYPE") == "tag", "Only an existing tag may publish a release")
        self.version, self.prerelease = version(self.tag)
        self.image_base = "ghcr.io/" + self.repo.lower()
        self.dist = Path("dist")
        self.logged_in = False
        self.check_source()

    def check_source(self):
        require(run(["git", "cat-file", "-t", self.tag], operation="Check annotated tag").stdout.strip() == "tag", "An annotated release tag is required")
        for ref in ("HEAD", self.tag + "^{commit}"):
            actual = run(["git", "rev-parse", "--verify", ref], operation="Check release source").stdout.strip()
            require(actual == self.commit, "Tag, checkout and source commit must match")
        run(["git", "merge-base", "--is-ancestor", self.commit, "origin/main"], operation="Check main ancestry")
        require(not run(["git", "status", "--porcelain", "--untracked-files=no"], operation="Check tracked source").stdout.strip(), "Release source has tracked changes")

    def api(self, endpoint):
        result = run(["gh", "api", "--include", f"repos/{self.repo}/{endpoint}"], operation="Read release state", allow_failure=True)
        lines = result.stdout.splitlines()
        statuses = [(n, re.fullmatch(r"HTTP/\S+\s+(\d{3})(?:\s+.*)?", line)) for n, line in enumerate(lines)]
        statuses = [(n, m) for n, m in statuses if m]
        require(statuses, "Release lookup did not return an HTTP status; publication stopped")
        start, match = statuses[-1]
        code = int(match.group(1))
        if code == 404 and result.returncode:
            return None
        require(code == 200 and result.returncode == 0, f"Release lookup failed with HTTP {code}; publication stopped")
        separator = next((n for n in range(start + 1, len(lines)) if not lines[n].strip()), None)
        require(separator is not None, "Malformed release response")
        try:
            value = json.loads("\n".join(lines[separator + 1:]))
        except (ValueError, TypeError) as error:
            raise ReleaseError("Malformed release JSON") from error
        require(isinstance(value, dict), "Malformed release object")
        return value

    def lookup(self):
        info = self.api("releases/tags/" + self.tag)
        if info is not None:
            require(info.get("tag_name") == self.tag and isinstance(info.get("draft"), bool), "Unexpected release identity/state")
            require(isinstance(info.get("assets"), list), "Missing release asset list")
            require(isinstance(info.get("prerelease"), bool), "Missing prerelease state")
            if not info["draft"]:
                require(info["prerelease"] is self.prerelease, "Published prerelease state conflicts with the tag")
        return info

    def asset_map(self, info):
        assets = info["assets"]
        require(all(isinstance(v, dict) and isinstance(v.get("name"), str) for v in assets), "Malformed release assets")
        names = [v["name"] for v in assets]
        require(len(names) == len(set(names)), "Duplicate remote asset names")
        require(set(names) <= set(PAYLOADS + [MANIFEST]), "Unexpected assets retained; operator review is required")
        return {v["name"]: v for v in assets}

    def download(self, name, directory):
        require(name in PAYLOADS + [MANIFEST], "Unexpected download name")
        run(["gh", "release", "download", self.tag, "--repo", self.repo, "--pattern", name, "--dir", str(directory)], operation="Download existing asset")
        target = directory / name
        require(target.is_file() and not target.is_symlink(), "Downloaded asset is not a regular file")
        require(0 < target.stat().st_size <= (131072 if name == MANIFEST else 256 * 1024 * 1024), "Unexpected asset size")
        return target

    def validate_manifest(self, value):
        require(isinstance(value, dict) and value.get("format") == "myprobe.release.v1", "Unknown release manifest")
        require(value.get("repository") == self.repo and value.get("tag") == self.tag and value.get("source_commit") == self.commit, "Manifest source does not match the tag")
        require(value.get("prerelease") is self.prerelease, "Manifest prerelease mismatch")
        require(isinstance(value.get("go_toolchain"), str) and 0 < len(value["go_toolchain"]) <= 256, "Missing build toolchain")
        assets = value.get("assets")
        require(isinstance(assets, list) and len(assets) == len(PAYLOADS), "Incomplete manifest inventory")
        require(all(isinstance(v, dict) for v in assets), "Malformed manifest inventory")
        require(sorted(v.get("name", "") for v in assets) == sorted(PAYLOADS), "Unexpected manifest asset names")
        for item in assets:
            require(isinstance(item.get("sha256"), str) and SHA.fullmatch(item["sha256"]), "Invalid asset digest")
            require(type(item.get("bytes")) is int and 0 < item["bytes"] <= 256 * 1024 * 1024, "Invalid asset size")
        images = value.get("images")
        require(isinstance(images, list) and len(images) == 2 and all(isinstance(v, dict) for v in images), "Incomplete image inventory")
        require(sorted(v.get("role", "") for v in images) == ["agent", "server"], "Unexpected image roles")
        for image in images:
            expected = self.image_base + ("-agent" if image["role"] == "agent" else "")
            require(image.get("image") == expected, "Unexpected image repository")
            require(isinstance(image.get("image_id"), str) and IMAGE_ID.fullmatch(image["image_id"]), "Invalid image content ID")
            require(isinstance(image.get("pinned_ref"), str) and re.fullmatch(re.escape(expected) + r"@sha256:[0-9a-f]{64}", image["pinned_ref"]), "Invalid pinned image reference")
        return value

    def remote_manifest(self, info):
        if MANIFEST not in self.asset_map(info):
            require(info["draft"], "Published release has no supported manifest; existing assets will not be changed")
            return None
        with tempfile.TemporaryDirectory(prefix="myprobe-release-") as folder:
            try:
                value = json.loads(self.download(MANIFEST, Path(folder)).read_text())
            except (ValueError, UnicodeError) as error:
                raise ReleaseError("Invalid remote manifest; original asset retained") from error
        return self.validate_manifest(value)

    def verify_remote_assets(self, info, manifest, *, complete):
        assets = self.asset_map(info)
        if complete:
            require(set(assets) == set(PAYLOADS + [MANIFEST]), "Release assets are incomplete")
        expected = {v["name"]: v for v in manifest["assets"]}
        for name in sorted(set(assets) & set(PAYLOADS)):
            with tempfile.TemporaryDirectory(prefix="myprobe-release-") as folder:
                target = self.download(name, Path(folder))
                require(target.stat().st_size == expected[name]["bytes"] and digest(target) == expected[name]["sha256"], "Existing asset bytes conflict; original asset retained")

    def local_assets(self):
        require(self.dist.is_dir() and not self.dist.is_symlink(), "Release dist must be an ordinary directory")
        checksums = self.dist / "SHA256SUMS"
        require(checksums.is_file() and not checksums.is_symlink() and checksums.stat().st_size <= 65536, "Missing or invalid SHA256SUMS")
        declared = {}
        for line in checksums.read_text().splitlines():
            match = re.fullmatch(r"([0-9a-f]{64})  (myprobe-[A-Za-z0-9_.-]+)", line)
            require(match is not None and match.group(2) not in declared, "Invalid or duplicate checksum entry")
            declared[match.group(2)] = match.group(1)
        require(sorted(declared) == PROGRAMS, "Release program inventory must contain all eleven expected programs")
        values = []
        for name in PAYLOADS:
            file = self.dist / name
            require(file.is_file() and not file.is_symlink() and 0 < file.stat().st_size <= 256 * 1024 * 1024, "Missing or invalid local asset")
            sha = digest(file)
            require(name == "SHA256SUMS" or sha == declared[name], "Local asset does not match SHA256SUMS")
            values.append({"name": name, "sha256": sha, "bytes": file.stat().st_size})
        return values

    def login(self):
        if self.logged_in:
            return
        actor, token = os.environ.get("GITHUB_ACTOR", ""), os.environ.get("GH_TOKEN", "")
        require(actor and token, "Registry actor and GH_TOKEN are required")
        run(["docker", "login", "ghcr.io", "--username", actor, "--password-stdin"], operation="Registry login", input_text=token)
        self.logged_in = True

    def image_id(self, ref):
        value = run(["docker", "image", "inspect", ref, "--format", "{{.Id}}"], operation="Inspect image content").stdout.strip()
        require(IMAGE_ID.fullmatch(value), "Invalid image content ID")
        return value

    def build_images(self):
        self.login()
        images = []
        for role in ("server", "agent"):
            image = self.image_base + ("-agent" if role == "agent" else "")
            refs = [image + ":" + self.tag, image + ":" + self.tag[1:]]
            run(["docker", "build", "--target", role, "--build-arg", "VERSION=" + self.tag, "--tag", refs[0], "--tag", refs[1], "."], operation=f"Build {role} image")
            expected = self.image_id(refs[0])
            for ref in refs:
                run(["docker", "push", ref], operation=f"Push {role} versioned image")
                run(["docker", "pull", ref], operation=f"Verify {role} registry image")
                require(self.image_id(ref) == expected, "Registry image content differs from the build")
            candidates = json.loads(run(["docker", "image", "inspect", refs[0], "--format", "{{json .RepoDigests}}"], operation="Read pinned image reference").stdout)
            require(isinstance(candidates, list), "Missing registry digest")
            matching = sorted(set(v for v in candidates if isinstance(v, str) and re.fullmatch(re.escape(image) + r"@sha256:[0-9a-f]{64}", v)))
            require(len(matching) == 1, "Ambiguous pinned registry reference")
            images.append({"role": role, "image": image, "image_id": expected, "pinned_ref": matching[0]})
        return images

    def verify_images(self, manifest):
        self.login()
        for item in manifest["images"]:
            refs = [item["pinned_ref"], item["image"] + ":" + self.tag, item["image"] + ":" + self.tag[1:]]
            for ref in refs:
                run(["docker", "pull", ref], operation="Verify published image")
                require(self.image_id(ref) == item["image_id"], "Published image content differs from the manifest")

    def plan(self, output=None):
        info = self.lookup()
        manifest = self.remote_manifest(info) if info else None
        if manifest:
            self.verify_remote_assets(info, manifest, complete=not info["draft"])
        needs_build = manifest is None or set(self.asset_map(info)) != set(PAYLOADS + [MANIFEST])
        state = "new" if info is None else "draft" if info["draft"] else "published"
        if output:
            with Path(output).open("a") as handle:
                handle.write(f"needs_build={'true' if needs_build else 'false'}\nrelease_state={state}\n")
        print(json.dumps({"release_state": state, "needs_build": needs_build, "source_commit": self.commit}))

    def ensure_draft(self, info):
        if info is None:
            args = ["gh", "release", "create", self.tag, "--repo", self.repo, "--draft", "--verify-tag", "--target", self.commit, "--title", self.tag, "--generate-notes"]
            if self.prerelease:
                args.extend(["--prerelease", "--latest=false"])
            run(args, operation="Create candidate draft")
            info = self.lookup()
        require(info is not None and info["draft"], "Candidate is no longer a draft; publication stopped")
        return info

    def should_mark_latest(self):
        if self.prerelease:
            return False
        latest = self.api("releases/latest")
        if latest is None:
            return True
        require(latest.get("draft") is False and latest.get("prerelease") is False, "Unexpected latest release state")
        previous, prerelease = version(latest.get("tag_name", ""))
        require(not prerelease, "Unexpected latest release version")
        return self.version >= previous

    def promote_latest(self, manifest):
        if self.prerelease:
            return
        latest = self.api("releases/latest")
        if latest is None or latest.get("tag_name") != self.tag:
            return
        for item in manifest["images"]:
            target = item["image"] + ":latest"
            run(["docker", "tag", item["pinned_ref"], target], operation="Prepare latest image")
            run(["docker", "push", target], operation="Promote latest image")

    def publish(self):
        info = self.lookup()
        manifest = self.remote_manifest(info) if info else None
        if info is not None and not info["draft"]:
            self.verify_remote_assets(info, manifest, complete=True)
            self.verify_images(manifest)
            self.promote_latest(manifest)
            print(json.dumps({"published_release_reused": True, "assets_replaced": False, "source_commit": self.commit}))
            return
        complete = manifest is not None and set(self.asset_map(info)) == set(PAYLOADS + [MANIFEST])
        if complete:
            self.verify_remote_assets(info, manifest, complete=True)
            self.verify_images(manifest)
        else:
            local = self.local_assets()
            if manifest:
                require(sorted(local, key=lambda v: v["name"]) == sorted(manifest["assets"], key=lambda v: v["name"]), "Rebuilt assets differ from the candidate; original assets retained")
                self.verify_remote_assets(info, manifest, complete=False)
                self.verify_images(manifest)
            info = self.ensure_draft(info)
            if manifest is None:
                # Existing draft payloads must match before any image publication.
                provisional = {"assets": local}
                self.verify_remote_assets(info, provisional, complete=False)
                images = self.build_images()
                toolchain = run(["go", "version"], operation="Record build toolchain").stdout.strip()
                manifest = self.validate_manifest({"format": "myprobe.release.v1", "repository": self.repo, "tag": self.tag, "source_commit": self.commit, "prerelease": self.prerelease, "go_toolchain": toolchain, "assets": local, "images": images})
            self.dist.mkdir(exist_ok=True)
            manifest_file = self.dist / MANIFEST
            data = json.dumps(manifest, sort_keys=True, indent=2) + "\n"
            if manifest_file.exists():
                require(manifest_file.is_file() and not manifest_file.is_symlink() and manifest_file.read_text() == data, "Local manifest conflict")
            else:
                with manifest_file.open("x") as handle:
                    handle.write(data)
            for name in [MANIFEST] + PAYLOADS:
                current = self.ensure_draft(self.lookup())
                if name in self.asset_map(current):
                    if name == MANIFEST:
                        require(self.remote_manifest(current) == manifest, "Candidate manifest conflict")
                    else:
                        self.verify_remote_assets({**current, "assets": [self.asset_map(current)[name]]}, manifest, complete=False)
                    continue
                run(["gh", "release", "upload", self.tag, str(self.dist / name), "--repo", self.repo], operation="Upload missing candidate asset")
            info = self.ensure_draft(self.lookup())
            require(self.remote_manifest(info) == manifest, "Candidate manifest changed")
            self.verify_remote_assets(info, manifest, complete=True)
            self.verify_images(manifest)
        current = self.ensure_draft(self.lookup())
        require(self.remote_manifest(current) == manifest, "Candidate manifest changed before publication")
        latest_flag = "--latest" if self.should_mark_latest() else "--latest=false"
        run(["gh", "release", "edit", self.tag, "--repo", self.repo, "--draft=false", f"--prerelease={'true' if self.prerelease else 'false'}", latest_flag], operation="Publish complete candidate")
        published = self.lookup()
        require(published is not None and not published["draft"], "Publication outcome is not yet confirmed")
        self.promote_latest(manifest)
        print(json.dumps({"published": True, "assets_replaced": False, "source_commit": self.commit, "prerelease": self.prerelease}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("plan", "publish"))
    parser.add_argument("--output", help="GitHub output file for plan")
    args = parser.parse_args()
    try:
        publisher = Publisher()
        if args.phase == "plan":
            publisher.plan(args.output)
        else:
            publisher.publish()
    except (ReleaseError, OSError, ValueError, KeyError, TypeError) as error:
        print(f"Release stopped: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
