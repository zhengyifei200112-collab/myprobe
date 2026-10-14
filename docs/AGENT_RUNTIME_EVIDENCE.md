# Agent 配置确认与上报通道证据（SYS-01）

此文档描述开发分支行为。实现、评审合并与正式发布分别记录，不能把本模块视为整个 SYS-01 或 M1—M5 的验收完成。

## 用户可见结果

管理员在“系统健康 → 节点诊断”可以看到期望的采集/上报周期、配置版本、Agent 确认版本和接收时间，以及最后一次成功入库上报的实际通道、协议版本、Server 观察时间和样本采集时间。

下发成功与 Agent 确认分开。旧 Agent 不支持确认时明确显示能力不可用；历史确认不会因为 Server 重启或 Agent 离线被当作当前连接证据。页面不按 Server/Agent 软件版本大小猜测兼容性或最新版本，不把缺少一个可选能力解释成整个 Agent 不兼容。

## 配置范围与实际采样

此扩展只管理 `collection_seconds` 和 `report_seconds`，各为 1—3600 的整数。网卡、挂载点、HostRoot、HTTP 私网策略及任务权限仍由已有逻辑管理。周期消息省略网卡/挂载点时保留本地选择。

新 Agent 启动独立采集循环和上报循环。采集立即开始，此后以采集开始时间计算下次间隔，串行执行；单次上下文限时为周期与 15 秒的较小值。采集超时/过慢时不并发启动额外采集，也不回放错过的时刻。操作系统调用仍可能受设备或库的行为影响，配置确认不是设备采集成功的证明。

缓存只保留最新一次成功采集，没有完整本地样本队列：

- 上报周期长于采集周期时，上报最新样本，中间样本不进入 Server 历史库。
- 上报周期短于采集周期时，不重复上报同一个缓存样本制造新鲜数据。
- 上传失败时可以重试尚未成功发送的最新样本；新的成功采集可替换缓存。
- 周期变更原子应用两个值、唤醒循环并清除旧缓存；变更前开始的采集不能进入新配置代际。
- 采集期间的累计网络计数仍用于既有流量记账；瞬时 CPU/速率等只代表采集时的观察，不承诺保存每次峰值。

上报发送受网络耗时影响。WebSocket 写成功不等于 Server 入库成功，已有报告回执语义不改变；本模块的通道证据只在 Server 实际提交报告后更新。配置确认是在 Agent 应用设置后发出，不证明后续采集、传输、数据库或监控目标正常。

CLI 的 `--collection-interval` / `--report-interval` 是连接配置应用前及旧 Server 未提供周期时的本地值。旧消息缺少周期时保留原值，部分缺少时精确保留另一周期（包括本地小数秒）；任何提供的非法周期都拒绝，不能部分应用。

## 协议与旧 Agent 兼容

新增能力 `config_apply.v1` 是现有 v2 WebSocket 的明确协商扩展，不改变 v1 信封、HTTP/v1 上报或旧任务负载。只有 Agent hello 与 Server welcome 都允许该能力时才发送扩展。HTTP 检查执行开关不影响采样配置能力。

| 连接 | 欢迎/后续配置 | 应用确认 |
| --- | --- | --- |
| v1，包括正式 v0.2.1 Agent | 保留原 welcome，未添加扩展字段/新消息 | 不支持，不推断已应用 |
| v2，未协商 `config_apply.v1` | 不发送 `runtime_config` 或扩展 config | 不支持 |
| v2，双方明确协商 | welcome 的 `runtime_config`；周期变化后的 `config` | 应用后发送 `config_applied` |
| 只有 HTTP/v1 | 上报与 hello 保持原接口 | 没有连接配置确认 |

扩展负载如下，nonce 是内部随机栅栏示例，不是令牌：

```json
{
  "scope": "sampling_periods",
  "revision": 2,
  "nonce": "00000000000000000000000000000000",
  "collection_seconds": 5,
  "report_seconds": 10
}
```

`config_applied` 回传同一 scope/revision/nonce 和 Agent 实际应用的两个值。新 Agent 先验证整个协商负载再变更设置；welcome 中 managed 值是权威值，不会先应用可能较旧的 legacy 副本。未协商、错误 scope/nonce/周期、旧版本或已替换连接的回执均不能确认当前配置。拒绝响应只给固定错误，不返回原始负载或内部栅栏。

每个有能力连接每 3 秒读取期望版本；发生变更后使用有 5 秒上下文的准备/发送操作。单节点编辑、批量和导入写入都通过数据库周期变更触发器被观察，不依赖某个页面回调。繁忙、数据库错误、网络时延可能延迟确认；这不是 3 秒送达保证，也不是“已保存就立即生效”。不为旧通道发送未协商的新帧。

## 持久身份与状态

迁移 `040_agent_runtime_evidence.sql` 为已有节点设初始采样版本 1，只在采集或上报周期实际变化时递增；改名称/标签等不会递增。A → B → A 仍有新版本，不能用相同数值复用旧确认。版本溢出导致事务拒绝。

每次 WebSocket 注册先持久化新的随机连接代际，再按同一顺序发布活动连接。新连接清空旧确认，准备独立随机 nonce。处理回执时事务锁定期望版本并同时核对当前代际、nonce、已发版本与精确周期。更早连接的回执无法覆盖新连接；重复有效回执保留首次接收时间。

| 状态 | 含义 |
| --- | --- |
| `no_connection_evidence` | 没有连接配置证据，包括只有 HTTP hello/report |
| `acknowledgement_unsupported` | 已观察通道不支持采样确认 |
| `prepared` | 已准备负载，尚无发送完成证据 |
| `awaiting_dispatch` | 期望版本更新，等待下发 |
| `awaiting_agent_ack` | 写出完成，尚无有效应用回执 |
| `dispatch_failed` | 发送失败，不能认定应用 |
| `agent_confirmed` | 活动连接对应的当前版本已收到有效回执 |
| `confirmed_no_active_connection` | 保存的当前版本曾确认，目前没有该活动连接 |
| `connection_evidence_not_current` | 存储与活动连接代际不同，不能投影成当前确认 |
| `connection_observation_unavailable` | 活动连接观察锁繁忙，不能判断当前连接 |
| `unavailable` | 查询失败，保留未知 |

Store 的 `agent_confirmed` 是持久回执事实；管理员 API 额外结合进程内活动连接判断当前/历史范围。数据库与各运行观察不是全系统原子快照，不能把多个响应拼成同一时刻的全局状态。

## 实际入库通道与权限

报告入口把 `http/v1` 或 `websocket/v1|v2` 与服务端观察时间、样本时间写入同一报告事务。报告验证、认证或事务失败都不更新证据。内部 `SaveReport` 未提供来源时明确标记未知，避免继承之前的 WebSocket/HTTP。旧库迁移不倒推历史通道。

最后通道表示最新成功提交的报告来源，不代表当前持久连接、TLS、节点在线或服务健康。HTTP 上报不计入 WebSocket 长连接数。

`GET /api/v1/admin/nodes/:nodeID/diagnostics` 仍需要管理员会话，所有成功和拒绝响应为 `private, no-store`。新增 `configuration_acknowledgement` 与 `last_report_ingress`，旧 `last_report_transport` 保留 `unknown/http/websocket` 兼容值。内部 epoch/nonce、Agent 令牌、会话、主机名和地址不进入这些 DTO；没有新增公开节点字段。诊断导出 v1 仍采用原封闭投影，不加入逐节点身份或内部栅栏。

管理页显示两个周期的**期望值**与已确认版本，避免把期望值标成未确认 Agent 的实际设置。刷新失败清除当前节点值并允许重试；401/403 清除私有诊断并返回登录。切换节点、刷新、Back 和卸载使用已有取消/代际保护。

## 升级与恢复

先备份 SQLite 和原应用密钥。040 是前向迁移，没有回退脚本；降级应恢复升级前完整备份及匹配版本。定时备份继续包含这些表，隔离恢复读取不启动任何 Agent 连接。恢复/重启后的持久确认不证明原连接仍活跃，新连接必须重新确认。

旧 Agent 可以继续使用现有上报。需要采样确认时安装支持双方协商扩展的版本；不要把尚未合并的开发二进制称作正式版本。无需新增 Agent 入站端口，保持主动连接且没有远程 shell/任意命令。

## 可重复验证与剩余范围

普通回归涵盖 Store 版本/nonce/代际、报告与来源事务、v1/v2 协商边界、独立采样/旧样本丢弃、真实 Agent welcome/live config 和 HTTP 回退、私有 API 与内部身份过滤。

正式 v0.2.1 原始 Agent 二进制使用固定 SHA-256，测试真实 WebSocket/v1 与 HTTP/v1 入库及确认不可用；普通离线测试跳过，需要显式提供 `MYPROBE_TEST_RELEASED_AGENT`。CI 下载固定发布资产、校验哈希后运行，Linux/Windows amd64 fixture 各自校验对应哈希，不用重新编译旧源码冒充原始发布文件。

管理页验收命令（需要本地 Node/Playwright 与 Edge）：

```powershell
$env:MYPROBE_TEST_SYSTEM_HEALTH='1'
$env:MYPROBE_TEST_AGENT_RUNTIME_BROWSER='1'
go test ./cmd/server -run '^TestLiveSystemHealthMainBrowser$' -count=1 -v
```

真实 main Server 与真实 CLI Agent 使用隔离库和合成凭据，覆盖 welcome、周期修改/回执、实际 WS/v2 入库、断连历史、重连重新确认、刷新/reload、键盘、两主题 360/768/1440 px 和会话撤销。受控组件脚本额外覆盖发送/未知/旧连接/失败状态及 HTTP/v1、503 重试和 401/403 清除；它不是实际吞吐证据。

当前模块不承担自动 Agent 升级、上游版本查询、完整本地离线缓存或每次采集历史保留。500/1000 节点真实负载、全系统实际 24 小时、其他历史发布/平台、公网 Provider/OAuth、依赖分支合并与正式发布仍遵循原路线，不能以此处单节点和界面验证替代。

2026-10-15 本地核验：npm ci/build、导航契约、gofmt、全仓 Go、vet、命令构建和差异检查完成。生成资源为 `index-DM6HnYPo.js / index-qPqymloK.css`。全仓实际启用健康+CLI Agent、定时备份、诊断导出、正式库升级与 v0.2.1 原始二进制套件；仅既有 Windows 磁盘路径用例失败（独立 PR #47），其余包通过。CGO 关闭，不声称本地 race；既有 npm 五项提示与图表包体积提示不变。Linux、容器和准确提交 CI 另行记录，不能用依赖分支 CI 替代本模块。

实现提交 `3d87bd27016511b6acca6259e67376ac8f753179` 的准确 CI [38610349157](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38610349157) 全部成功：前端确定性构建、Linux 全仓 Go/vet/build、正式库升级、真实受限 tmpfs ENOSPC、固定原始 v0.2.1 Linux Agent 的 WebSocket/v1 与 HTTP/v1 兼容、Server/Agent 容器与 Compose、安装脚本。草稿 PR [#63](https://github.com/zhengyifei200112-collab/myprobe/pull/63) 基于 #62，仍未合并/发布；此条引用绑定实现提交，不冒充后续文档提交的 CI。
