# MyProbe repository instructions

These rules apply to the entire repository. They are the default for maintainers,
contributors, and coding agents unless a more specific `AGENTS.md` exists below the
files being changed.

## GitHub Flow

- Never commit or push directly to `main`.
- Start every task from an up-to-date `main` on a short-lived branch:
  `feature/*`, `fix/*`, `docs/*`, `chore/*`, `refactor/*`, `hotfix/*`, or `release/*`.
  Automation may use `agent/*` or `dependabot/*` when its platform requires that prefix.
- Keep one independently reviewable concern per branch and pull request.
- Push the branch, open a draft pull request early, and make it ready only after the
  required checks and documentation are complete.
- Do not merge a pull request without the repository owner's explicit approval.
- Squash-merge approved pull requests and delete the source branch after merging.

## Commits and pull requests

- Use Conventional Commits: `type(optional-scope): imperative summary`.
- Allowed types are `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `build`,
  `ci`, `chore`, and `revert`.
- Explain the problem, approach, user impact, security/privacy impact, migrations, and
  validation in the pull request. Use the repository pull request template.
- Update `CHANGELOG.md` for user-visible behavior, security changes, migrations, and
  operational changes. Pure tests or internal refactors normally do not need an entry.
- Never rewrite shared branch history or force-push unless the owner explicitly asks.

## Architecture and compatibility

- Keep the public API, Agent protocol, SQLite schema, generated web assets, product
  specification, and user documentation synchronized.
- Breaking Agent protocol changes require a new protocol version and compatibility
  notes. Servers should remain compatible with the previous released Agent whenever
  practical.
- Every schema change must use a new, forward-only numbered migration. Never edit an
  already released migration.
- Treat traffic accounting, retention, authentication, backup/restore, notification
  delivery, and privacy filtering as correctness-critical code.
- Preserve the outbound-only Agent design. Do not add remote shell or arbitrary command
  execution.

## Security and privacy

- Never commit credentials, tokens, production databases, private keys, personal data,
  or real infrastructure addresses.
- Do not log or return plaintext passwords, sessions, Agent tokens, notification
  credentials, backup passphrases, or unmasked public IP addresses.
- New public fields require a privacy review and an API contract test.
- Validate all input at a trust boundary and keep proxy trust explicit.
- Follow `.github/SECURITY.md` for vulnerability handling; do not disclose an unpatched
  vulnerability in a public issue.

## Required validation

Run the checks relevant to the change before requesting review. The complete baseline is:

```bash
npm --prefix web ci
npm --prefix web run build
gofmt -w <changed-go-files>
go test ./... -count=1
go vet ./...
go build ./cmd/...
git diff --check
```

- Commit regenerated files under `internal/webui/dist` whenever `web/` changes.
- Add regression tests for bug fixes and boundary tests for time, traffic, retention,
  authentication, migrations, and protocol behavior.
- Check browser-facing changes in light and dark themes and at 360, 768, and 1440 px.
- Document any check that cannot run locally and ensure CI covers it.

## Definition of done

A task is complete only when implementation, tests, generated assets, migrations,
documentation, changelog, and deployment implications agree; the worktree is clean; the
branch is pushed; and required pull request checks pass.
