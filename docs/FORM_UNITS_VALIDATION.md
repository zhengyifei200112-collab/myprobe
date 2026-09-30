# Form quantity validation (M1 / UX-03)

This change implements monetary amounts, billing-cycle presets, alert quantity
units and expiry timezone guidance. Node reordering remains outstanding; this is
not completion of M1 or of the full development roadmap (PR #44).

## Reproduction

- Run `npm --prefix web ci`, `npm --prefix web test`, and
  `npm --prefix web run build`. Build from the configured npm script, with LF
  source files, so Vue scope hashes match CI.
- Start a fresh local Server with a temporary database, explicit test password,
  encryption key, and `MYPROBE_LISTEN=127.0.0.1:25776`.
- With Playwright installed, set `MYPROBE_QA_PASSWORD` to that test password and
  run `node scripts/form_units_browser_test.cjs`. `PLAYWRIGHT_MODULE` may point
  to an existing Playwright module; `BROWSER_CHANNEL` defaults to `msedge`.
- Use only an isolated disposable local database. The script creates a fixture
  node and removes it on success. A failed run may leave its fixture behind.
- Screenshots go to `QA_OUTPUT_DIR` or the OS temporary directory.

## Evidence and limits

Unit tests cover decimal precision, JPY/USD/KWD, bit/byte multipliers, safe integer
boundaries, and non-terminating time conversions. Browser checks exercise a real
Server for amount/custom-cycle/expiry round trips, empty tags, Mbps conversion and
unsafe input validity, with screenshots at 360/768/1440 px in light/dark themes.

On Windows, `go vet ./...` and `go build ./cmd/...` passed. `go test ./... -count=1`
passed all packages except the existing collector test
`TestDiskUsagePathUsesHostRootForAbsoluteMounts`, whose Linux absolute-path
assumption does not hold on Windows. The unchanged collector code is covered by
the Linux CI job. Check current PR results before treating validation as complete.

No API, database or Agent changes are introduced. Existing prices retain their
integer minor-unit values; public display now respects currency fraction digits.
The prior frontend displayed every currency using a divisor of 100, so JPY/KWD
display may change even though stored integers do not.
