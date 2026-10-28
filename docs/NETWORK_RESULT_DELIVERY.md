# Bounded network-result acknowledgement

The Agent retains a completed `network_probe.v1` result until it receives the
existing application ACK for that exact sequence, or the original task expires.
A successful WebSocket write alone no longer releases the result.

One worker belongs to `Client.Run`. It keeps at most 32 immutable envelopes,
each with a payload bound of 4096 bytes. Executor concurrency remains eight;
waiting for an ACK does not occupy an executor slot or block the receive loop.
Busy executor responses use the same bounded delivery path.

The worker retries after five seconds without an ACK on the same connection.
A replacement connection can retry immediately once both peers negotiate v2
`network_probe.v1` again. Every attempt reuses the original sequence, payload,
task identity, completion time and envelope timestamp; it does not execute the
probe again. The Server's existing transaction validates the authenticated node,
current connection generation, immutable task, expiry and result fingerprint.
Identical completed-task replay gets an ACK without another latency row.

Only a v2 ACK whose envelope and payload sequences match a pending result sent
on the current negotiated socket removes that result. An unrelated, stale,
unwritten or mismatched ACK does not clear pending work. A higher sequence is
not a cumulative acknowledgement of older pending results.

No attempt starts at or after the original task's receive deadline. Each socket
write, including the wait for the shared writer, has the earlier of that deadline,
the Run context and the existing ten-second write budget. Capability removal,
v1 fallback and a missing socket cannot send a `network_result`; pending work
expires against its original deadline. Expired entries also release capacity
before admitting a new result.

Overflow, expiry and shutdown remain explicit local rejection events in the
private test observation seam. Delivery warnings contain a fixed reason only,
without tokens, addresses, result bodies or task identifiers. Run shutdown
cancels the worker and clears pending memory; late executor completion cannot
reintroduce work. There is no disk queue or recovery across an Agent process
restart. This does not extend task deadlines or change metric sample replacement,
report ACK handling, HTTP fallback, the wire schema or Server admission rules.

## Verification and limits

Real `Client.Run` + Gateway + file-backed Store tests cover a result lost before
storage, an ACK lost after commit, and a lost ACK on a healthy socket. Successful
recovery checks identical sequence/body, one TCP execution, one completed durable
task and one latency row. Further real reconnect cases remove the capability or
fall back to v1: the result is not sent on that channel and expires explicitly.
Unit tests cover capacity, concurrent admission, exact ACK/socket fencing,
deadlines, cancellation while the writer stays held and shutdown.

The first two full-chain tests fail on the previous implementation and pass with
this change. Linux CI also runs the delivery cases with the race detector. These
tests do not certify a new 20-node/24-hour run, history-query performance or every
event in a previous failed load. That failed load and its original evidence remain
failed. No production deployment or release is implied by this branch.

本轮以当前main重新整合。Run在既有三个后台循环上增加一个结果工作线程，等待计数为四；
不引入历史分支的其他后台任务。复用只在启动前安装的私有无敏感字段观察钩子供投递
测试留证，生产默认nil；全类型采样/HTTP envelope观察仍属独立后续工作。
原始红绿及当前完整基线、race、原生平台和兼容检查须按新head验证，历史CI不能代替。
