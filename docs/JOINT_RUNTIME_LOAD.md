# 联合运行负载夹具与证据

本夹具推进 PERF-01。它运行单独构建的正式 `cmd/server`，以及正式
`Client.Run` 的连接、采集缓存、上报、配置确认和 TCP/HTTP 执行流程。
仅主机指标来源是测试生成器；不会启动远程命令、访问生产节点或借用生产数据库。
所有数据和凭据均在临时目录生成，正常结束或断言失败时关闭进程和监听器。

## 工作负载

- 1—100 个节点，默认 5 个，采集/上报均为真实 15 秒。
- 每节点三个 60 秒 TCP 目标，连接本夹具拥有的监听器。
- 一个 30 秒 HTTP 服务，所有节点为观测点；先返回 503，后返回 200。
- 一个真实默认通知发送器的接收端：先拒收 503，再接受 204。
- 五个公共实时订阅；默认是 Go WebSocket 消费者，可明确切换为五个
  Playwright 独立上下文中的真实页面，两种模式在结果中分别标记。
- 每秒依次请求概览、轮换节点的 `range=30d` 历史和管理员健康接口。
  当前夹具只包含本轮历史，不能把范围参数当成实际稠密 30 天数据。
- 关闭所有 Agent socket，随后重启同一 Server/数据库，保留正常重连退避。
  正式通知模式等待拒收/成功路径出现后才注入相应故障，防止短测试跳过投递。

默认运行 90 秒，可指定 90 秒至 48 小时。时钟不加速；最终 JSON 保存请求时长、
实际开始/结束和经过时长。90 秒通过不代表 24 小时通过。

## 通知隔离与资源控制

正式通知发送器禁止回环/私网目的地。`run-joint-load.sh` 使用新的 Linux 网络
命名空间，在其中的 loopback 添加 RFC 5737 测试地址和一个自有 RFC 1918 地址。
该命名空间没有外部网卡或默认路由；不会修改宿主接口、DNS 或路由。接收端仅在
隔离命名空间绑定测试地址，保留正式默认发送器的地址验证与真实 TCP/HTTP 发送。
`MYPROBE_TEST_LOAD_DENIED_RECEIVER=1` 是另一个有限场景：断言正式策略拒绝本地
接收端，不能据它宣称 Provider 故障/恢复已验证。

`MYPROBE_TEST_LOAD_SYSTEMD=1` 为每次 Server 启动建立独立临时 systemd 单元：
`CPUQuota=200%`、`AllowedCPUs=0,1`、`MemoryMax=2G`、`MemorySwapMax=0`。
读取真正的 `cpu.max`、`cpuset.cpus.effective`、`memory.max`、`memory.swap.max`
验证限制生效；Server 加入夹具网络命名空间。Agent、浏览器、接收端与采样器在
Server 资源组外。停止时关闭/收集临时单元，不修改永久 service 文件。

Linux 从正式 Server PID 的 `/proc` 采样 VmRSS，从其资源组采样累计 CPU；每秒
记录分配 WAL 的大小，保留采样最大值和每 30 秒时间序列。采样会漏掉瞬时峰值，
不冒充绝对峰值。没有资源观测的平台明确记录 unavailable，数值 0 不代表实测零。
共享 WSL/宿主与真正独立的 2 vCPU SSD 部署仍需分别报告。

## 验证与输出

测试检查所有节点至少三个真实入库报告、三个成功 TCP 结果、一个 HTTP 结果，以及
重连后的当前配置确认。逐条保存报告须来自该节点生成器，去掉 Server 补入的观测
IP 后与原始指标 hash 一致，且没有重复时间戳。采集次数减入库次数包括最新缓存
跳过的采集和没有回执证明的发送，不能直接解释为丢包，也不能当成零数据丢失验收。

概览/历史/健康延迟包含实际请求与完整响应读取，保存样本数、p50/p95/max（毫秒）。
健康使用每次新解码对象，防止省略字段沿用上次旧值。结果记录 worker、队列与连接
计数、HTTP 结果分类、通知尝试、失败接收与成功接收、浏览器事件及页面错误。
保存夹具/关键运行源码和实际 Server 二进制 SHA-256。输出不含 token、密码、
URL、节点 ID、nonce、宿主名称或实际接口地址。

Linux 独立构建并运行短测试：

```sh
go build -o /tmp/myprobe-joint-server ./cmd/server
go test -c ./internal/agentclient -o /tmp/myprobe-joint-agentclient.test
sudo sh scripts/run-joint-load.sh /tmp/myprobe-joint-agentclient.test /tmp/myprobe-joint-server
```

开启资源限制、100 节点和五个真实页面（Node、Playwright、相配的 Chromium 及其系统
依赖须预先准备；输出目录须已经存在）：

```sh
sudo env MYPROBE_TEST_LOAD_SYSTEMD=1 MYPROBE_TEST_LOAD_NODES=100 \
  MYPROBE_TEST_LOAD_BROWSER=1 MYPROBE_TEST_LOAD_OUTPUT=/tmp/myprobe-load-results \
  PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
  PLAYWRIGHT_BROWSERS_PATH=/absolute/path/to/browser-cache \
  sh scripts/run-joint-load.sh /tmp/myprobe-joint-agentclient.test /tmp/myprobe-joint-server
```

Node 必须在传给夹具的 PATH 中。脚本自动切换到该 checkout 的 Agent 测试目录，
确保预构建测试也能读取相同源码和浏览器脚本。CI 运行五节点/Go 订阅模式的短测试，
覆盖正式 main、通知拒收/恢复和重启；它不伪装成浏览器或低配/24 小时结果。
Windows 可单独运行拒收策略及渲染模式，不能冒充 Linux 隔离接收端验收。

## 当前状态与后续门槛

夹具已完成真实隔离接收端的原型验证。准确最终提交、5/50/100 节点的真实浏览器
记录和 CI 必须单独核对，最终原始记录将保存在 `docs/evidence`。
前端资源未改动；没有新的生产配置、协议、API 或迁移。

继续保留稠密 30 天/一年历史同时查询、报告发送/ACK/入库对账及可解释缺测、长时间
队列与内存趋势、实际 24 小时、任意积压保留追赶、加密大库/跨机器恢复、公网
Provider/OAuth、依赖整合、所有者合并/发布与完整 M1—M5 的原始范围。
