# Contributing

MyProbe is built as small, independently testable vertical slices. Changes should keep
the public API, agent protocol, storage migration, and user-facing behavior in sync.

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
