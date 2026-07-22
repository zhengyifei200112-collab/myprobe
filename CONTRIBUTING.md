# Contributing

MyProbe is built as small, independently testable vertical slices. Changes should keep
the public API, agent protocol, storage migration, and user-facing behavior in sync.

Read [`AGENTS.md`](AGENTS.md) before starting work. It defines the repository-wide GitHub
Flow, Conventional Commits, security rules, validation baseline, and definition of done.
Repository roles, protection settings, and maintenance cadence are documented in
[`docs/GOVERNANCE.md`](docs/GOVERNANCE.md).

## Workflow

1. Update local `main` with `git pull --ff-only origin main`.
2. Create a short-lived `feature/*`, `fix/*`, `docs/*`, `chore/*`, or `refactor/*`
   branch. Never commit or push directly to `main`.
3. Make focused Conventional Commits and keep documentation and tests synchronized.
4. Push the branch and open a draft pull request using the repository template.
5. Resolve review feedback and make the pull request ready after all checks pass.
6. After owner approval, squash-merge and delete the source branch.

## Before opening a pull request

```bash
npm --prefix web ci
npm --prefix web run build
go fmt ./...
go test ./...
go vet ./...
```

- Add a forward-only numbered migration for every schema change.
- Do not expose plaintext session tokens, agent tokens, or notification secrets.
- Breaking agent protocol changes require a new protocol version.
- New public fields need an explicit privacy review and API contract test.
- Browser-facing changes must be checked in light and dark themes and at a mobile width.
- Generated frontend assets under `internal/webui/dist` are committed so a Go build is
  self-contained; regenerate them whenever `web/` changes.

User-visible and operational changes belong in [`CHANGELOG.md`](CHANGELOG.md). Release
owners must also follow [`docs/RELEASING.md`](docs/RELEASING.md).
