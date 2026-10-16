# 在线加密备份与实际负载（SYS-02 / PERF-01）

本夹具补齐 [静止大库容量](BACKUP_CAPACITY.md) 不覆盖的在线场景。它使用正式 main
Server、真实 Client.Run、探测/HTTP 服务/历史查询、已认证管理 API，以及可选真实页面。
只有主机指标为合成数据；临时数据库和密钥均为本次测试所有，不使用生产数据。
尚不代表百节点在线备份、异机恢复、生产切换或完整 M1—M5 已验收。

## 工作流与证据

在原 [联合运行夹具](JOINT_RUNTIME_LOAD.md) 增加显式 `MYPROBE_TEST_LOAD_BACKUP=1`。
必须同时选择 year 稠密历史、报告核对以及 3—15 分钟测量；默认原有模式保持关闭。
节点仍按真实 15 秒采集/上报，每节点三项 60 秒 TCP、全节点 30 秒 HTTP 检查持续执行。

1. Server 通过独立 0600 文件取得备份口令。使用实际登录 Cookie/CSRF 保存备份计划，
   选择未来的每日时间，避免短测中额外自动任务。不是直接调用内部备份函数代替 API。
2. 开始测量十秒后，POST 实际异步手动备份，浏览器请求只需接受 202。采集、查询、
   服务评估及订阅保持运行；额外经正式 Client 发送一组报告作为操作重叠探针。
3. 完成后经实际 API 请求隔离恢复，再发送一组报告。观察器仅统计已转发且收到真实
   Server ACK 的报告，发送和 ACK 均须落在操作区间内；旧报告、迟到 ACK、未转发或
   无 ACK 不计入。至少两项操作各有一个可信提交 ACK，不能用最终文件存在代替重叠。
4. 计划中的 Server 重启等到备份与恢复完成后执行，再次确认同一 archive ID/hash、
   恢复状态及恢复人口数仍被持久保存。这里验证完成后的重启，不声称在备份中途杀进程。
5. 保留原有断连/重连、实际发送器拒收/恢复、通知排空、全部报告/封套核对及最终五页
   DOM 收敛，不因备份启用而取消这些路径。

备份所含原始报告数不能直接等于造数前的计数：正式 Server 启动即可能正常清理过期
前缀。夹具按已验证的连续 15 秒种子网格、实际归档完成时刻和正式七天保留策略推导
保证仍须存在的种子行下界；新报告可以增加人口数。边界包含精确 cutoff，纳秒之后会
移除该网格行，独立回归覆盖这些关系。加密生成自身还进行完整 plaintext hash 及
SQLite 验证。不是把应该保留的记录作为“正常清理”任意容忍。

输出只增加聚合的 LiveBackup：时间、归档字节、保留行下界、恢复节点/用户/报告/渠道
计数、两段操作中的提交 ACK 数/节点数和重启保留状态。不会复制管理响应中的目录、
文件名、口令、Cookie、CSRF 或令牌。原始测试材料继续只在私有临时目录。

## 有界 Linux CI

独立 online-backup-load job 固定 Ubuntu 24.04 并绑定准确 head。五节点/一年稠密历史/三分钟/五真实 Chromium
页面，正式 Server 受 CPUQuota=200%、CPU 0—1、MemoryMax=2 GiB、swap=0 约束；Agent、
观察器、浏览器、接收端在组外，不能声称宿主整体只有 2 GiB。测试保持正常 SSRF 防护，
接收端只在自有网络命名空间配置合成地址，不能访问其他基础设施。

CI 除检查 Go 的功能结论，还核验实际 cgroup 数值、页面收敛、报告差异、归档至少
1 GiB、采样 RSS <700 MiB、概览 p95 <500 ms、30 天历史 p95 <2 s。采样可能漏过瞬时
峰值，完整长稳仍独立。只复制聚合结果给 artifact 上传者，不复制临时数据库/加密文件。

```sh
export MYPROBE_TEST_LOAD_DURATION=3m MYPROBE_TEST_LOAD_NODES=5
export MYPROBE_TEST_LOAD_HISTORY=year MYPROBE_TEST_LOAD_SYSTEMD=1
export MYPROBE_TEST_LOAD_RECONCILE=1 MYPROBE_TEST_LOAD_BROWSER=1 MYPROBE_TEST_LOAD_BACKUP=1
export MYPROBE_TEST_LOAD_TIMEOUT=12m MYPROBE_TEST_LOAD_OUTPUT=/absolute/existing/private-directory
# 先构建 web 和正式 Server/test binary，配置 Playwright；只在隔离 Linux 环境以 root 执行。
sh scripts/run-joint-load.sh /absolute/agentclient.test /absolute/server
```

## Windows 功能复核（2026-10-16）

使用真实 main/Agent/API、五个 Go WebSocket 消费者、一年稠密历史和三分钟；没有 cgroup、
没有渲染页面，接收端使用明确的 loopback-policy-denied 模式。因此不冒充 Linux 有界
验收、真实 Provider 故障恢复或通知排空，原始 RecoveryDrained=false 保留。

最终严格窗口复核通过：归档 1,376,978,666 B，恢复五节点/一用户/一渠道/201,629 条
报告，保证保留种子行下界 201,590。备份/恢复分别观察到 10/9 条提交 ACK，均涉及
五节点；窗口从 API 接受之后计起，备份还不早于实际归档开始，不能计入请求前的报告。
重启后完成状态保留。报告发送/ACK/入库 67/67/67，核对差异为零；概览 p95 2.262 ms、
30 天历史 p95 760.381 ms。探针额外产生两组真实报告，不假装恰好只有定时量。

首轮因错误比较造数前行数而失败：正式保留已经删除过期前缀；并非恢复丢数据。该轮
Passed=false 保留，早返回导致 ACK 计数未被计算，不能把原始零值解释为没有 ACK。
修正精确边界规格并增加回归后，新的完整三分钟运行通过，不拼接失败时长。源码 hash、
正反两次原始结果见 [本地记录](evidence/online-backup-windows-2026-10-16.json)。

## Linux 有界场景结果

实现 c4d3555 的 CI 38868668040，online-backup-load job 及其独立验收步骤已通过；
下载时整个 workflow 仍在进行，不能由单 job 推断全部检查通过。Go 1.26.9，实际
CPU 200%/CPU 0—1/2 GiB/swap 0。181.880 秒测量，五个真实页面各收敛到五节点，
页面/API 错误零；报告发送/ACK/入库 66/66/66，全部核对差异为零，通知排空。

归档 1,376,978,666 B，恢复 201,620 条报告，保证保留下界 201,580。备份/恢复有
18/10 条区间内提交 ACK，均涉及五节点；重启保留同一已验证归档。183 次资源采样，
进度写入错误零，最大间隔 1.0011 秒，RSS 峰值 79,388,672 B、WAL 6,027,592 B。
计划重启时有一次 Server 不可读取样本，不能描述为零不可用。

概览 p95 6.206 ms、30 天历史 p95 1808.897 ms，通过现行门槛。一年历史 p95
5793.023 ms、最大 ACK 延迟 2169.582 ms 仍如实记录；这里没有将一年查询设为两秒
门槛，也不据此声称所有请求都低延迟。完整原始结果见
[Linux 记录](evidence/online-backup-linux-2026-10-16.json)。后继固定 runner 和文档提交
仍需检查各自准确 head。无合并、发布或百节点在线备份结论。

随后已核对实现 `c4d355547a3f81bcb4fb77027b3ba9dc4c9538f1` 的
[CI 38868668040](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38868668040)
六个 job 全成功：受限在线备份、静止大库、完整/race/升级恢复/联合运行/容器/安装、
原生网络及隔离故障、年归档、六视图。见 [完整步骤](evidence/online-backup-ci-2026-10-16.json)。
后继仅将新在线 job 固定 Ubuntu 24.04，避免即将变动的 ubuntu-latest 改写平台基线；
负载和验收条件不降低，后继 head 仍独立检查。
