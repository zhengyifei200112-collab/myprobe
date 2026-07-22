# Release process

MyProbe uses Semantic Versioning and immutable Git tags. Releases are built from `main`;
release artifacts are never produced from an unmerged feature branch.

## Prepare

1. Confirm every intended change is merged and required checks on `main` pass.
2. Review `CHANGELOG.md`, move relevant entries from `Unreleased` into a dated version
   section, and add comparison links.
3. Confirm migrations are forward-only and document backup, compatibility, and rollback
   implications.
4. Run the full validation commands from `AGENTS.md` on the release commit.
5. Open a `release/vX.Y.Z` pull request for release-only documentation or version changes.

## Publish

After the release pull request is approved and squash-merged, create and push an
annotated tag from the exact `main` commit:

```bash
git switch main
git pull --ff-only origin main
git tag -a vX.Y.Z -m "MyProbe vX.Y.Z"
git push origin vX.Y.Z
```

The Release workflow builds Server binaries for Linux and Windows, Agent binaries for
Linux, Windows, and macOS, generates SHA-256 checksums, and publishes a GitHub Release.
Verify the checksums and perform a clean install plus an upgrade from the previous
release before announcing it.

## Urgent fixes

Create `hotfix/<description>` from the affected supported branch, add a regression test,
and use the normal pull request and review process. Never move or reuse a published tag.
