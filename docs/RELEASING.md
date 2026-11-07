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
Linux, Windows, and macOS, and the offline rollback preparer for Linux and Windows.
It requires an annotated tag whose exact checkout is on `main`. Tag jobs are
serialized without cancelling an in-flight publication.

`scripts/publish-release.py plan` verifies the source and any retained candidate.
A matching complete candidate or published release can skip rebuilding; otherwise
the workflow builds all eleven programs and `SHA256SUMS`. Publication then:

1. Validates the full local inventory and creates or reuses a draft.
2. Builds and pushes both versioned Server/Agent images, pulls them back and checks
   image content IDs. Nothing is pushed as `latest` during this preparation.
3. Records `release-manifest.json` with the exact source, Go toolchain, asset hashes
   and sizes, image content IDs and content-addressed registry references.
4. Uploads only missing assets. Existing bytes must match; no existing release
   asset is deleted or overwritten. Every payload is downloaded and hashed before
   the candidate is made public.
5. Publishes the complete candidate, then promotes `latest` images only if this is
   the current stable latest release. Prereleases and older stable releases do not
   move the stable latest pointers.

The release contains thirteen files: eleven programs, `SHA256SUMS`, and
`release-manifest.json`. These checks bind the distribution bytes and image
content to the candidate; they do not replace platform/runtime acceptance.

## Interrupted publication and retries

Rerun the same workflow/tag for a transient failure. A matching partial draft
retains existing assets and uploads missing ones; different rebuilt bytes,
unexpected assets, incompatible manifests and unknown API outcomes stop the job
without replacing original assets. A published release is verified and reused,
so retrying a failed latest promotion does not rebuild or re-upload its payloads.
An older published release without this manifest cannot be rewritten by this path.

Do not delete a retained draft or reuse a tag merely to make a failing job green.
Inspect its original artifacts and source first. If the original build cannot be
reproduced, keep its evidence and prepare a separately approved new version.
The workflow does not automatically delete failed drafts or versioned image tags.

GitHub and the registry do not share an atomic transaction. Failed preparation
can leave a private draft and public staging versioned image tags; these are not
an announced complete release. A failure while promoting the two `latest` image
tags can leave them temporarily different even though the versioned release is
complete. Retry promotion only while GitHub still selects this stable release;
deployments should pin an approved version or registry digest.

Verify the checksums, Server health check, absence of a Server health check on the Agent
image, Agent Ping support, Linux host-metric Compose path, one-click clean install, and
upgrade from the previous release before announcing it. The installer and new container
images must not be advertised as available until the corresponding GitHub Release has
completed successfully.

## Urgent fixes

Create `hotfix/<description>` from the affected supported branch, add a regression test,
and use the normal pull request and review process. Never move or reuse a published tag.
