# 地址族与 ICMP 包计数：开发中的完整工作包

此分支继续后续开发文档第 7 节，不把协议类型或函数测试当作完整交付。
当前已接通 `network_probe.v1` 双向协商、真实 Agent 执行、连接代次约束和持久回执，
Server 启动时启用协商；旧连接需要重连，新旧 Agent 按实际能力分别派发。
有界执行器、迁移 045、配置 v3、管理设置、NQS2 归档及三种权限上下文的比较页已接入。
完整多节点容量、最终整合源的 24 小时运行和发布验收仍未完成，不能据此声明整项交付。

Agent 已增加[有界结果确认与重试](NETWORK_RESULT_DELIVERY.md)：最多32份结果只在
内存保留至精确ACK或原任务期限，同连接缺ACK五秒重试，重新协商能力的连接可立即
重发同序号/内容。重试不重复执行探测；旧协议或未协商通道不能接收新结果帧。
该修正不增加持久离线队列，也不把原失败长测改为通过。

## 协议与执行口径

- 仅在传输 v2 且 Hello/Welcome 双方明确协商 `network_probe.v1` 时发送
  `network_task` / `network_result`。v1 消息和旧 Agent 的既有路径保留；新字段不塞进 v1。
- 任务冻结 auto/ipv4/ipv6、目标类型及 ping 包数。ping 每任务 1—5 包，间隔至少 250 ms，
  TimeoutMS 是整项工作预算（含解析与包列），不是每包可重复享用的预算；TCP 不接受包数。
  显式地址族不匹配的字面地址拒绝，IPv4 映射地址按 IPv4 处理。
- 结果显式区分 success/failure/unobserved，只有真正执行的结果可提供实际地址族。
  DNS、工具缺失/权限不支持、繁忙、无可用地址、取消和内部异常为未观测，不能作为目标失败。
  实际 TCP 连接的 family 来自 RemoteAddr；ICMP 须解析/约束为一个实际探测的地址族。
- ICMP 包数只保存完整且可信的已发/已收证据。数据缺失或中途打断时为 null，不从
  success、timeout、配置包数或目标字符串反推。TCP 永远没有 ICMP 包数。
- 多包 ICMP 至少一包回应时，RTT 为该次任务成功回包的平均值；任务成功与包丢失率分别
  计算。因此任务可以成功且确有部分丢包。跨任务 P50/P95 为成功任务 RTT 分布，不能命名
  为全部独立回包 RTT 的分位数。精确丢包率只对具有可信计数的已发包求和后相除。
- 结果没有地址、原始工具输出或任意错误文本。严格检查枚举、整数、有限 RTT、计数关系、
  时间、最大消息、未知字段和单个 JSON；结果还必须绑定认证节点、持久任务及实际请求包数。
  `ValidateAgainst` 校验定义/身份，不能替代 Store 的任务状态、epoch、租约和接收期限检查。

Linux iputils 的地址族/包数/间隔语义见
[ping 手册](https://man7.org/linux/man-pages/man8/ping.8.html)，Windows 的地址族和包数见
[Microsoft ping](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/ping)。
各平台工具/权限与真实网络测试必须分开记录；无法可靠解析时明确不可用，不伪造精度。

## 已实现的执行器及验证边界

执行器先在总 deadline 内按所选 family 解析，TCP 只连接一个已解析字面地址并核对真正的
RemoteAddr；不再次隐式解析或退回另一个地址族。ICMP 只调用固定 ping/ping6 与参数，
不经过 shell，捕获输出最多 32 KiB，同时排空 stdout/stderr，终止/清理也有上限。
权限/工具/地址族不支持与 DNS 分别为未观测。没有可信 RTT 时不使用整个程序耗时替代。

只信任完整、唯一、计数关系有效的工具摘要。Windows 的 Received 可能包含 Destination
Unreachable，因此还要求实际带 time/时间 的 Echo Reply 数与摘要一致；否则计数不可用。
Windows 英文、简体中文（含 GBK）有解析回归，其他无法可靠识别的格式明确不可用。
sendmsg/sendto/本地 transmit failed 等发送错误会使工具的 attempted/sent 摘要不再是
可信的实际发包计数。没有回包时记未观测 send_error；有可信 RTT 时可以保留成功结果，
但包数仍为 null，不能把本机没发出去的请求报成目标丢包。
工具平均 RTT 的量化精度保留，例如 Windows 回环平均 0 ms 是工具量化结果，不能解释为
物理零时延；空数据的 null 与这个实际观测值始终不同。

真实静默目标实验发现 Linux iputils 的 `-c` 与 `-w` 组合可能持续发包直到收到指定数量
回应或 deadline，超出请求的发送上限。执行器禁止这个组合：使用严格 `-c` 上限，从
末包回应等待中扣除包列及摘要时间，再由共享 Context deadline 约束整项工作。
隔离的无回包测试必须核对实际 transmitted 正好为请求上限，不能放宽计数验证来通过。

每任务包数是请求上限；总 timeout 或平台发送节奏可能使实际已发包数少于上限。
只报告工具真实完整计数，不补足未发包，也不把未发包计入丢包。中断、截断、矛盾摘要或
无法解析的 RTT 都不能变成成功/失败的虚构精度。收到回应但没有可信 RTT 时为未观测。

当前 Windows 与 Linux 实际自有 IPv4/IPv6 TCP 及 ICMP 各两包都通过，ICMP 均得到
sent=2/received=2。Windows 原生工具 RTT 平均值为 0 ms；Linux 该次约 0.016/0.017 ms。
Linux 自有命名空间禁用 Echo Reply 时实际 sent=3/received=0，为执行失败；另一个没有
路由的合成地址为未观测 send_error、包数不可用，不能报 100% 目标丢包。
原始按源代码区分的日志投影、二进制/源文件 hash、最初超发失败与修复见
[原生证据](evidence/network-probe-native-2026-10-16.json)。准确源 831831a 的独立原生 CI
job 已通过；后继 c0d9fb3 包括真实无路由反例，仍按准确 head 检查完整 CI。
此前 Darwin 只有参数/格式覆盖，未声称实际平台验收。现在增加 macOS 15 原生 ARM64/
Intel CI：真实自有 IPv4/IPv6 TCP、各两包 ICMP、三包任务经过 Agent/Gateway/Store/ACK，
以及忙状态保留未知和 race 回归。原生门槛禁止把 IPv6 跳过计为通过；JSON 事件验证器
要求九个具体子项完成及两族实际计数输出，并绑定实际 OS/架构、Go 版本和准确源码。
实现提交 `a6caea519fedc1328f98ce428fa15b9bbf1f2913` 的
[CI 38947898587](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38947898587)
中，两项 Darwin 原生 job 均已成功，包含上述实际链路和独立 race 执行。Apple Silicon
使用 macOS 15.7.9 / Go 1.26.8，Intel 使用 macOS 15.7.9 / Go 1.26.7；每个平台九项
必要子测试全部通过，IPv4/IPv6 均实际 sent=2/received=2，调度链路断言实际三包。
原始结构化结果见 [ARM64](evidence/network-probe-darwin-arm64-2026-10-17.json) 与
[Intel](evidence/network-probe-darwin-amd64-2026-10-17.json)，事件摘要和源文件 hash
已在下载后复核。该记录绑定实现提交，不代替后继提交的全套 CI；Linux 隔离静默/
无路由实验不能冒充 macOS 同等故障覆盖，仍不据此宣布整项网络或发布验收完成。

核对 [Apple ping6 源码](https://github.com/apple-oss-distributions/network_cmds/blob/main/ping6.tproj/ping6.c)
发现 `-W` 表示旧式 FQDN/节点查询，并非 IPv4 `ping` 的毫秒等待参数。IPv6 命令因此
移除该选项，保留固定包数/间隔和共享总 deadline；IPv4 毫秒响应等待不变。没有完整
摘要或被 deadline 打断时，计数继续为空，不伪造已发/已收或丢包率。此平台参数修复
未进入冻结 63b6528 的甲骨文长测，不能自动继承其最终源验收。

## 必须完成的纵向接入

1. 有界执行器：只执行固定程序与参数，不经过 shell；DNS/TCP/ICMP 共用总 deadline，
   限制输出字节、并发、包数及速率；真实自有 IPv4/IPv6 TCP/ICMP 与不支持场景验证。
2. 前向迁移：目标、不可变 epoch、任务快照冻结 family/包数；重命名不重排计划，定义变化
   原子关闭/新建 epoch；已有历史缺乏证据仍为 unknown，不能追溯修改旧统计。
3. 真实协商与持久接收：只使用本连接的能力和身份；结果原子入库/ACK、重试幂等、冲突拒绝。
   旧 Agent 不支持显式 family/多包任务时记录调度未执行，不发送可能退回另一地址族的任务，
   不伪称 Agent 回报。auto 单包兼容旧路径，并保留“没有实际地址族证据”。
4. 原始/分钟/五分钟统计：保留可信 family/包数与证据覆盖，兼容旧汇总载荷；不得把 unknown
   与观测样本合成一个已知 family，或把有缺口的包数统计冒充全范围精确丢包率。
5. 管理目标、配置导入导出及目标预设支持显式 family；公开/分享仍只有允许的别名和枚举。
   网络比较页显示配置与实际证据范围、计数覆盖及不可用原因，三个权限上下文都须验证。
6. 迁移失败回滚、旧 Agent、跨 epoch/保留层、重复/乱序/过期、混合地址族、真实六视图、
   多节点容量和最终整合源 24 小时均须留证；原已中断的 #70 长稳不能复用或拼接。

## 请求定义与兼容性阶段

迁移 045 为 target/不可变 epoch/task 增加请求 family/包数，保留结果格式和预期证据标记。
旧记录仅被标明原有 auto/单包请求语义，result_format 仍为 v1，旧 JSON/hash 不变，不能据
此获得实际 family 或 sent/received。真实升级失败回滚与旧 epoch phase 保持有专门测试。
旧 schema 的升级 fixture 使用冻结 SQL 造数，不能调用依赖新列的当前生产创建方法。

改名/排序不会重建 epoch；family/包数变化与关闭/新建计划同事务。已领取任务持有旧定义。
旧客户端省略新字段的更新在写事务中保留当前选项，避免并发读取后将 profile 降级。
Ping 旧配置中无效但曾被忽略的 port 仍可保留，生成严格 v2 任务时会去掉这个无效输入。

配置导出升为 v3；v1/v2 输入继续支持，只允许旧的 auto/单包语义。显式 family/多包不允许
伪装成旧格式。dry-run 不写入目标；完整 v3 导出/导入包含请求选项，仍不包含观测证据、
Token、事件、告警设置或运行账本。主机策略和服务告警的独立迁移包不改版。

管理界面在目标创建/编辑中提供“请求地址族”和最多五包，TCP 不传 ICMP 包数；IPv6 目标
端口以方括号表达。旧 Agent 没有协商能力时，调度在任何 pending/传输写入之前跳过任务，
仅保存带 `source=server_dispatch` / `result_format=dispatch.v1` 的兼容计数投影，不插入
延迟样本、不冒充 Agent 回报，也不能凭此为 Agent 重放授权 ACK。比较页使用“确认未执行”
说明这个来源区别。已升级且双向协商的新 Agent 执行高级 profile；未协商的旧连接仍为
已确认未执行，不能静默退回 v1 不受 family 约束的探测。

## 真实传输、归档与公开投影

派发在首次可能的 socket 写入前，将任务绑定为 evidence_required；旧 v1 回执不能满足
这个义务，也不能在不确定发送后降级重试。SQLite 写事务检查当前 node/connection_epoch/
wire=2，接收同时校验冻结定义、节点、状态、任务期限及活动 epoch。新连接注册先推进
数据库代次，再公布 socket，旧连接不能利用中间窗口入库。Gateway 不跨数据库 IO 持有
全局连接锁。任务完成与兼容延迟样本在同一事务提交后才 ACK；相同规范 JSON/hash 的
重复回执可幂等确认，冲突拒绝。首次迟到拒绝；重复回执仍受协议十分钟时间窗口限制。
Agent 任务受本连接 Context 约束，结果只写回捕获的原连接，断线后不写入替代连接。
当前没有新加 Agent 持久重发队列，存储可重放不代表客户端已经保证自动重发。

unobserved 不插入延迟样本，在质量账本中归为 not_executed。DNS、本地发送错误与无包等
固定原因同样保留。NQS2 保存实际 IPv4/IPv6 任务数、具有计数的成功/失败任务数、已发/
已收包总数；读取 NQS1 的旧 11 类原因和零证据。旧写入器生成的冻结二进制 fixture 防止
新编码器自证兼容。混合原始—分钟—五分钟及重新打开数据库后，包数与原因保持一致，
时间点之和与范围总数一致；跨失败/缺测/epoch 的抖动断点及分位数权重继续保留。

公开只增加 family 枚举和聚合计数，不提供任务地址、解析 IP、请求主机、端口或输出。
每节点/点单独显示 family 覆盖和包计数覆盖。ICMP 丢包为 sum(sent-received)/sum(sent)，
不平均任务百分比。已执行任务中只要有旧记录或缺少计数，就标为 partial；不能把这个
值当成全范围精确丢包率。没有观测包数时 sent/received/value 均为 null；TCP 为
not_applicable。顶层仅标 per_node，不把不同节点/实际窗口硬合成一个比例。

新增验证包括真实 WebSocket 协商/版本隔离/失败不 ACK、Store 回滚/重连代次/并发重放、
真实定时 TCP IPv4/IPv6、繁忙未执行及可选原生三包 ICMP IPv4/IPv6。Windows 三包均为
sent=3/received=3；Linux 由原生 CI 检验，不能由 Windows 结果替代。六视图检查同一真实
Gin/Store/嵌入资源，混合新旧记录显示部分观测，旧记录仍未知；公开/管理/密码分享分别
验证范围及字段白名单。原始本地记录见 [传输证据](evidence/network-probe-transport-2026-10-16.json)。
本地完整 Go 仍仅已有 Windows HostRoot #47 失败；CGO 关闭，race 以准确源 CI 为准。
实际实现 `49334ad7ec5cde20079c62a25d7facd8980006bd` 的
[CI 38835498392](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38835498392)
四个 job 已全部成功：完整/race/升级恢复/联合运行/容器/安装检查、原生网络及隔离故障、
年归档容量、真实六视图。完整步骤见 [CI 元数据](evidence/network-probe-transport-ci-2026-10-16.json)。
后继文档或测试提交仍检查自己的准确 head，不继承这份历史通过状态。

冻结同一实现的五节点稠密一年历史联合短测已通过：91.285 秒测量、29/29/29 条报告
发送/ACK/入库，全部封套核对差异为零，通知排空，五个真实页面均收敛到五节点，无页面
错误。Server 实际 CPU 200%/CPU 0—1/2 GiB/swap 0；93 次资源采样，最大间隔 1.006 秒，
不可读取样本 0，峰值 RSS 58,212,352 B、WAL 4,635,032 B，一年历史 P95 1387.39 ms。
完整原始结果见 [联合短测](evidence/network-probe-joint-five-browser-2026-10-16.json)。
这不等于八节点整年网络比较或 24 小时验收。

准备时两次包装器配置失败已保留：TMPDIR 0755 导致浏览器共享内存权限错误，未进入
测量；修正后又因输出并非已存在目录，使资源进度写入失败 4 次，最终 Passed=false，见
[输出失败记录](evidence/network-probe-joint-output-failure-2026-10-16.json)。它们不计入
成功时长。成功运行从零开始，资源落盘零错误，没有放宽断言或改浏览器渲染模式。

2026-10-16 01:45:47 UTC 已启动同一冻结源码的全新 100 节点/一年历史/五个真实页面
24 小时测试，run_id=33f0ac7640b24344884853e74a010a71。启动快照仍在造数阶段，没有
正式测量起点或通过结果。源码归档、二进制、runner、supervisor hash 和限制见
[启动证据](evidence/network-probe-soak-launch-2026-10-16.json)。造数不计时，不自动重启，
不复用旧中断测试；最终仍须核对完整计数、故障阶段、排空、页面及资源结果。
五条原有 npm advisories 及图表体积提示仍单独登记。所有者批准合并/发布、最终容量及
最终整合源 24 小时门槛继续保留，旧中断长稳不能复用。

部署需先完成 Server 的迁移，再让新 Agent 重连协商；旧 Agent 可继续原有自动单包路径。
NQS2 写入后，旧 NQS1-only Server 不能直接读新归档。回退需使用兼容的升级前备份及对应
二进制，不能把原库交给旧二进制或反向改写已经提交的迁移。
