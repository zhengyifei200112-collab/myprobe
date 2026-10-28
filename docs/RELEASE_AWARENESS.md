# Official release awareness

The private System Health page compares the Server build version and the selected
Agent's last advertised version with the public upstream's latest stable release.
It reports update available, equal, ahead of stable, unknown current version, and
unavailable release information separately. Development and incomplete version
strings remain unknown. Semantic versions, including prerelease ordering and
build metadata, are compared using the already pinned `golang.org/x/mod/semver`.
No software version comparison asserts protocol incompatibility or grants an
Agent capability. Existing actual transport/handshake diagnostics remain separate.

## Operator behavior

The real Server checks once on startup and every 24 hours by default. Set
`MYPROBE_RELEASE_CHECKS_AUTO=false` in its process environment and restart to
disable automatic checks. Manual authenticated checks remain available. Invalid
boolean configuration prevents startup. Container operators must pass this
environment variable into the Server container.

All administrators share an in-memory cache and a single concurrent operation.
Manual checks have a 15-minute minimum interval, also after errors. Bounded
GitHub rate-limit reset/Retry-After values can extend it to at most 24 hours.
Successful conditional requests revalidate the cache. Restart clears the cache
and cooldown; there is no database migration. A failed check, cache older than
48 hours, or clock regression makes comparison unavailable. The browser also
ages displayed cached information while the page stays open. It does not poll
the upstream or automatically refresh the selected node.
The separate “刷新版本状态” action rereads only the local cache even during
cooldown, including when a startup check or another administrator's check has
completed since the last read.

Agent evidence is the most recently persisted hello and its receipt time, which
may predate the current connection. No handshake/version remains unknown. The
page clears another node's evidence immediately when selection changes.

Links open fixed official release notes and the
[installation and upgrade guide](INSTALLATION.zh-CN.md). For Linux Server builds,
an update command is shown only for a validated installer-compatible stable tag
and an actual update comparison. It applies only to script installations with an
already downloaded/reviewed `install.sh`. Agent upgrades require checking the
actual host and instance name in the guide; the panel does not guess them.
This feature never downloads binaries, modifies services or executes commands.
Read release notes and verify a recoverable backup before upgrading.

## Private API and trust boundaries

- `GET /api/v1/admin/system/releases[?node_id=ID]` reads local cached evidence;
  it never initiates an upstream request.
- `POST /api/v1/admin/system/releases/check[?node_id=ID]` joins or starts a
  bounded check and returns the same response. A successful HTTP response means
  the evidence was read, not that the upstream check succeeded: inspect status.
- Both require an administrator session and return `Cache-Control: private,
  no-store`; POST additionally requires the session CSRF token. Share sessions
  and anonymous callers receive no diagnostic data.
- Only one nonempty `node_id` of at most 128 bytes is accepted; unknown nodes
  return 404 before consuming a check. Unknown/duplicate/malformed query keys are
  rejected. Client versions, URLs, force flags and credentials are never inputs.
- Response whitelist: observation time, upstream snapshot, Server comparison,
  optional Agent comparison/ID/hello time and the fixed documentation URL. Node
  names, hostnames, addresses, capabilities, tokens and raw upstream bodies are
  excluded. No public API, Agent wire contract or SQLite schema changes.

The only upstream request is an unauthenticated GET to
`https://api.github.com/repos/zhengyifei200112-collab/myprobe/releases/latest`.
It sends constant API/User-Agent headers and an optional bounded ETag; it sends
no local version, node information or application credentials. The hosting
network's normal HTTP proxy configuration still applies. Requests have a
five-second lifetime/timeout, no redirects, no retries and a 1 MiB response cap.
Draft/prerelease/malformed/future releases are rejected; raw upstream URLs,
release bodies and error messages are never used. Release links are constructed
from the fixed repository and validated tag. A cancelled browser waiter does
not cancel other administrators' shared operation; Server shutdown cancels it.

Sources: [GitHub latest release endpoint](https://docs.github.com/en/rest/releases/releases#get-the-latest-release),
[Go semantic version package](https://pkg.go.dev/golang.org/x/mod/semver).

## Verification

Unit/API tests cover semantic ordering, unknown versions, cache boundaries,
revalidation, errors, response limits, untrusted tags/URLs, rate limits,
concurrency, cancellation, disabled automatic checks, startup checks, private
response fields, query validation and session/CSRF/cooldown enforcement.

The opt-in browser uses actual Cookie/CSRF, Gin, Store, checker and embedded
assets with an owned synthetic upstream transport. It covers light/dark at
360/768/1440 px, keyboard manual check, cooldown across all six views, node
selection, missing hello and no overflow/page errors. Explicit intercepted
response fault cases additionally verify stale display, failed-read retry, a12-second browser request limit with retry reenabled, and
unauthorized clearing; these are labelled separately from actual backend proof.

```sh
go test ./internal/releasecheck ./internal/config -count=1
go test ./internal/httpapi -run '^TestReleaseStatusAdminContract$' -count=1
MYPROBE_TEST_RELEASE_BROWSER=1 go test ./internal/httpapi \
  -run '^TestReleaseAwarenessLiveBrowser$' -timeout 4m -count=1 -v
```

Browser checks require `node` and `PLAYWRIGHT_MODULE`; Windows can set
`PLAYWRIGHT_CHANNEL=msedge`. `QA_ARTIFACT_DIR` receives synthetic screenshots and
aggregate results only. CI runs the browser fixture alongside existing comparison
gates. Historical soak evidence does not establish current-source20-node24-hour acceptance. This does not close all SYS-01 or M1–M5
requirements, merge the draft stack or constitute a release.

Current integration must pass complete Go/vet/build, LF frontend generation, current-source privacy/cache/concurrency tests, actual six-view browser and CI/race. Historical Windows HostRoot failures, old npm advisories and prior capacity runs are not current acceptance exceptions. The required native scope remains5→20 and same-source20×24h, tracked separately.

The fixed API header uses version `2026-03-10`, verified against [GitHub API versions](https://docs.github.com/en/rest/about-the-rest-api/api-versions) on2026-10-28.
