# Incident action history growth acceptance

This workload is under implementation. It will validate retained acknowledgement
and silence request identities at 1,000 / 10,000 / 100,000 finished incidents, and
maintenance request replay at the existing 1,000-window admission limit.

The test uses synthetic, completed Store operations. It must preserve original
results through metric retention, close/reopen and a consistent SQLite backup,
reject changed-content retries, and keep new actions usable with old history.
Concurrent report writes and component read timings will be reported separately.
Maintenance remains bounded; this does not authorize a larger window limit.

No real notification provider, production database or credential is involved.
This component evidence cannot replace the independent 50-node continuous 24-hour
run, process memory trend, real Telegram/OAuth or owner release approval.

Stacked on PR #117. Runtime source remains the 0d2d739 candidate under native soak.
Results and exact source checks will be added after execution; no pass is claimed.
