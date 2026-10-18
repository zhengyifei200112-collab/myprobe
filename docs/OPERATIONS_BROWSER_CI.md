# Actual-main operations browser regression

The backup and diagnostic-export Go browser tests are opt-in. An ordinary
`go test ./...` skips them. The `operations-main-browser-windows` CI job enables
both against actual main Servers built from the exact PR head and its rebuilt
embedded frontend. It uses Node 22, Go 1.26.x, Playwright 1.62.1 and Windows Edge.
The separate PowerShell observation job keeps its original five-minute budget.

`scripts/run-operations-browser-qa.cjs` requires a clean committed checkout and an
empty output directory outside it. Each test gets its own temporary directory;
the fixed screenshot names in the existing scripts cannot pick up an older run.
The runner requires exactly one Go JSON run/pass and zero skips/failures for each
named test, plus all 24 expected light/dark, 360/768/1440 px state screenshots.
It hashes all tracked source files before/after and copies only PNGs and
`result.json` to `screened/`. CI uploads that directory, including partial results
on failure. Go events, downloads, databases and fixture secrets stay in `private/`.

On Windows with Go, Node, Edge and Playwright installed, build the frontend first,
commit the candidate, then run (the parent directory must already exist):

```powershell
$env:PLAYWRIGHT_MODULE='<absolute path to node_modules/playwright>'
$evidence = Join-Path $env:TEMP ('myprobe-operations-' + [guid]::NewGuid())
New-Item -ItemType Directory -Path $evidence | Out-Null
node scripts/run-operations-browser-qa.cjs $evidence
if ($LASTEXITCODE -ne 0) { throw 'Inspect the private fixture output' }
```

The existing assertions cover asynchronous backup, isolated recovery, retention,
failed task reminders, frozen diagnostic download bytes/hash, privacy, audit,
reload/Back and session revocation. The loopback notification is rejected by the
production sender policy; this does not send a real external notification.

The helpers build/start disposable main processes but do not retain live Server
executable hashes or prove graceful shutdown. This job does not establish real
OAuth/Telegram delivery, large-database performance, cross-machine recovery,
24-hour stability, production release approval, or full M5 acceptance. Those
remain separate acceptance work.
