# Short-lived file history readers

This integration preserves the required 5→20 short profile and same-source
20-node 24-hour run. Frozen source 6d2297d's 20-node 30-day P95 of 2513.201 ms
remains a failure; connection isolation alone is not claimed to solve it.
Current-source native performance/reference and recovery gates remain separate.


Metric, latency and traffic history use a separate read-only SQLite connection
pool for file databases. At most two connections are open concurrently, matching
the API's existing global two-query admission limit. No read connection is kept
idle: closing Rows closes its connection, prepared statements and mappings. The
normal four-connection writer pool remains open for reports, health, retention,
notifications and consistent backups. In-memory Stores retain their original
shared-memory database and connection pool.

Queries, nanosecond bounds, independent per-statement snapshots, visibility,
captured target kinds, weighted statistics, traffic resets and ordering are
unchanged. New views observe committed WAL updates. A queued caller's context
still cancels its wait; errors are propagated without switching to another
database, ignoring missing files, caching a result, or retrying a query.

Read connections use `mode=ro` and `query_only=ON`. SQLite may still coordinate
WAL readers through SHM and write temporary sorting files. This is not a claim
that a history request has no filesystem writes.

## Mapped reads and operational control

`MYPROBE_HISTORY_MMAP_ENABLED` defaults to `true`. On 64-bit Linux only, each file
history connection requests a mapping limit of 2,147,418,112 bytes. SQLite maps
the beginning of the file, up to that limit; larger files continue using buffered
reads beyond the mapped range. This is an address-space limit, not a reservation
of that much RAM or a limit on total process memory. Resident mapped pages,
sorting, Go allocations and the main pool still consume memory. The two-reader
budget and closing mappings between queries bound reader lifetimes; actual
Server RSS, aggregate cgroup memory and long-running trends must still pass
their existing acceptance gates.

Set `MYPROBE_HISTORY_MMAP_ENABLED=false` and restart the Server to keep buffered
reads. The read-only pool and its two-connection limit remain. Compose forwards
this setting from `.env`. Invalid boolean values reject startup. Other operating
systems, 32-bit processes and in-memory databases keep buffered access; SQLite
can also silently decline mapping on an unsupported platform.

SQLite documents a material tradeoff: a mapped-file I/O fault can raise a process
signal instead of an ordinary SQLite query error. Mapped reads may also increase
read-ahead and resident file pages, and do not improve every workload. See
[SQLite's memory-mapped I/O explanation](https://www.sqlite.org/mmap.html) and
[the mmap_size pragma](https://www.sqlite.org/pragma.html#pragma_mmap_size).
Choose buffered reads when this failure behavior or the storage environment is
unsuitable. Do not externally truncate, rewrite or replace a database while the
Server is using it. The existing restore procedure stages data and applies it
at the next startup, before opening the Store; the restore/rollback requirements
and verified backups remain necessary.

There is no schema migration, new index, retention change, Agent protocol change
or new public response field. Downgrading the program does not require a
database conversion for this connection policy.

## First-row cancellation and driver ownership

The driver is upgraded from `modernc.org/sqlite` v1.39.1 to v1.42.2. A query
cancelled after creating its first Rows could previously discard that object
without closing it. Its read transaction survived even when `database/sql`
reported no open connections, preventing a complete WAL checkpoint. The
[upstream fix](https://gitlab.com/cznic/sqlite/-/merge_requests/81) closes the
discarded Rows before returning the context error. Version v1.42.2 also includes
[interrupt completion serialization](https://gitlab.com/cznic/sqlite/-/merge_requests/86),
so a late interrupt cannot reach the next user of a pooled connection.
No cancellation is suppressed,
checkpoint assertion relaxed or query retried. The existing regression performs
80 first-row cancellations with an immediate passive checkpoint after each.

The driver's effective transitive dependency versions are unchanged, and SQLite
remains 3.50.4. Generated engine files differ, including support for driver hooks
added upstream; byte-identical engine code is not claimed for v1.42.2. MyProbe
does not enable those hooks or require a database conversion. A second regression
performs 400 cancellations on retained primary connections and immediately reuses
them without sleep, retry or reopening. See [cancellation and WAL readers](SQLITE_CANCELLATION.md). These checks complement the short-lived history-reader test.

## Validation scope

File-backed differential fixtures compare mapped/buffered read views with the
writer connection across weighted raw/retained data, fractional/equal timestamps,
failed/null latency, counter resets, metadata changes, hidden/public/admin scope
and deletion. Other checks exercise read-only enforcement, two queued readers,
independent writer access, first-row cancellation and passive checkpoint drain,
closure and in-memory fallback. Linux checks inspect actual mappings while Rows
are held and require them to disappear when Rows close. CI runs these under race
instrumentation as well as the normal baseline.

An independently recovered synthetic component is useful for identifying page
read cost and mmap/resource tradeoffs. It is not an HTTP P95 measurement, a
Server memory/steady-state proof or a qualifying capacity run. The same final
source still requires five-to-twenty short checks, uninterrupted twenty-node
24 hours, independent memory review, full aged reference/recovery and all
remaining acceptance gates. Earlier failed native results stay failed.
