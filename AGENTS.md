# MyProbe repository instructions

These rules apply to the entire repository. They are the default for maintainers,
contributors, and coding agents unless a more specific `AGENTS.md` exists below the
files being changed.

## 模型能力不足时的提醒规则

1. 默认使用当前选择的模型（GPT-6.1 Sol）自主完成项目开发、代码优化、Bug 修复、测试及其他相关任务。
2. Codex 已获得完整操作权限。在正常开发过程中，无须因常规代码修改、文件操作、测试或部署反复请求用户确认。
3. 遇到复杂问题时，应优先自主分析原因、查阅相关代码、尝试合理的解决方案，并验证修复结果，不要因为问题较复杂就立即请求切换模型。
4. 如果经过充分排查和合理尝试后，仍无法解决当前问题，或者发现问题明显超出当前模型的处理能力，应及时停止重复且无效的尝试，避免浪费时间和模型额度。
5. 出现上述情况时，主动提醒用户：

   **⚠️ 当前问题可能超出 GPT-6.1 Sol 的有效处理能力，建议切换至 GPT-6 Astra 继续解决。**

   同时简要说明当前问题、已尝试的方法、失败原因以及推荐切换模型的理由。
6. 提醒后等待用户决定是否切换模型。不得声称已经自动切换，也不要擅自放弃整个开发任务。
7. 如果当前问题不影响其他独立任务的正常开发，可以继续推进其他任务，不必因单个疑难问题中断整个项目。

**核心原则：尽可能自主解决问题；只有在当前模型确实难以有效推进时才提醒切换 Astra，不频繁打断用户，也不进行无意义的反复尝试。**

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
- Keep edited frontend sources in LF form, matching `.gitattributes`, before
  building embedded assets. CRLF working files can produce different Vue scope
  hashes even when Git normalizes the committed source.
- Add regression tests for bug fixes and boundary tests for time, traffic, retention,
  authentication, migrations, and protocol behavior.
- Check browser-facing changes in light and dark themes and at 360, 768, and 1440 px.
- Document any check that cannot run locally and ensure CI covers it.

## Definition of done

A task is complete only when implementation, tests, generated assets, migrations,
documentation, changelog, and deployment implications agree; the worktree is clean; the
branch is pushed; and required pull request checks pass.
