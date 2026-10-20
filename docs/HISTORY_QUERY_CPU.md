# Historical query CPU work

The owner's self-use profile requires about 50 nodes. Runtime 2257a2e completed
its native 50-node short workload with 999 matching report/ACK/store records,
all five rendered subscriptions converged, no API/page errors and drained
recovery. Overview P95 was 159.33 ms and sampled Server RSS was 92,200,960 bytes.
However, 30-day history P95 was 2283.63 ms, above the unchanged 2000 ms gate.
The short run failed and did not start the 24-hour follow-up.

Separate CPU profiles on a byte-verified copy of that synthetic failure corpus
show SQLite execution, record comparisons and sorting dominating the metric and
latency reads. This diagnostic has no live Agents/browser workload and is not
another capacity result. Original failure files are never opened with SQLite.

## Query changes

Every raw/retained source already constrains node_id to one bound argument.
Use an uncorrelated EXISTS against that same node for visibility instead of
repeatedly joining its metadata for each historical row. The node must still
exist for both public and administrator reads; administrators may include a
hidden node. All checks stay inside each statement's read snapshot.

Latency aggregation uses the targets table's integer row identity only as a
statement-local sort/group key. The final metadata join restores the public
target ID and name. No row identity is persisted, returned, used as a foreign
key or assumed stable across statements. Public ID/kind tie ordering is explicit.
Raw and retained kinds, target enumeration, successful-without-duration cases
and weighted sums/counts retain their existing semantics.

Existing node/target/kind/tier/time index seeks, exact raw nanosecond bounds and
complete-retention-bucket policy stay in place. The shared four connections,
global two-reader admission, cancellation/join, request timeouts, API, protocol,
schema, retention, browser updates and original SLOs are unchanged.

## Validation boundaries

Existing history/traffic/visibility/differential and query-plan regressions pass.
An additional regression changes internal target row identities, including
negative values and values greater than 2^53, while checking identical public IDs,
ordering, retained kinds and weighted points against the earlier reference.

Current-candidate component comparisons and a new full 50-node short run remain
required. A component speedup or green CI cannot certify the strict 50-node
capacity gate, continuous 24-hour stability, physical 2 GiB SSD hardware, real
provider delivery or release. See [scope](SELF_USE_CAPACITY.md) and
[previous indexed reads](SCOPED_HISTORY_READS.md).
