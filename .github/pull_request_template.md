## Summary

<!-- What changed? Keep this concise and user-focused. -->

## Why

<!-- What problem or maintenance need does this solve? -->

## Impact

- User impact:
- Operator/deployment impact:
- Security/privacy impact:
- Compatibility or migration impact:

## Validation

<!-- List exact commands and manual checks. -->

- [ ] `npm --prefix web run build` (when frontend or embedded assets changed)
- [ ] `go test ./... -count=1`
- [ ] `go vet ./...`
- [ ] `go build ./cmd/...`
- [ ] Browser checks completed when UI changed
- [ ] `CHANGELOG.md` and relevant documentation updated

## Review checklist

- [ ] This PR has one reviewable purpose.
- [ ] No secrets, tokens, production data, or unmasked private data are included.
- [ ] Schema changes use a new forward-only migration.
- [ ] Protocol/API compatibility and rollback behavior were considered.
- [ ] Generated frontend assets are committed when `web/` changed.
