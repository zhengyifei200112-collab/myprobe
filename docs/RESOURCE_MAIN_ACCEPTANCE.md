# CPU波动、投递超时与真实主进程重启联验

`TestResourceIncidentThroughMainRestart` 补齐开发文档16.1的一条连续路径：同一CPU
事件从持续异常、短暂回落、再次异常、通知超时、实际Server进程重启，直到持续
恢复。此前的Store时间边界、expiry API重试与HTTP服务联合负载分别有证据，不能
单独当作这条CPU路径已经走完。

## 执行与断言

- 启动真实`cmd/server`及`Client.Run`，仅CPU源与私网接收端响应受夹具控制。
  使用默认15秒评估周期、默认10秒HTTP发送超时、正常重试退避及Agent重连；
  不直接Tick/DeliverOne、不缩短时钟、不手工插入事件、报告或确认。
- CPU95%、阈值80%、持续3秒，观察真实pending→firing及持续时间。短暂CPU10%
  必须先被评估器记为recovery_since，再回到95%，确认原事件保持firing且恢复
  候选被清除；持续恢复条件30秒，重复提醒显式设为一小时，避免把重复提醒当
  重试或新事件。每次变化以实际持久报告/评估状态为屏障，不只等待固定sleep。
- 接收端实际读到第一封firing请求后，等待默认发送器超时关闭连接。API必须
  显示unknown尝试、transport_error和未来待重试时间；已收请求不表示渠道确认。
- 同一数据库停止/重启真实Server，要求进程身份变化，原会话、事件及待重试
  投递身份保留。允许响应后，原firing第二次尝试确认成功，幂等键保持不变。
- 最后持续CPU10%，要求满足30秒真实恢复观察后，同一事件resolved且原因
  recovered，只有一个已确认恢复投递。核对一事件、二投递、三尝试以及接收端
  三次请求（最初结果未知、firing重试确认、恢复确认），不承诺外部恰好一次。

默认全仓跳过这个显式启用的原生进程测试。Linux完整跨模块runner提供Server
参数时执行该用例，绑定实际Server摘要、子结果摘要、源码/测试程序与PNG；
CI要求全部预期用例通过，不能跳过。Windows管理配置仍是原有六项。

## 复现

从冻结的干净源码构建原生程序。以下路径均为占位；完整runner已有浏览器环境
要求，见[跨模块验收](CROSS_MODULE_BROWSER_ACCEPTANCE.md)。

```sh
go test -c -o /absolute/httpapi.test ./internal/httpapi
go test -c -o /absolute/agentclient.test ./internal/agentclient
go build -ldflags '-X main.version=resource-main-acceptance' \
  -o /absolute/resource-server ./cmd/server
mkdir -m 700 /absolute/empty-output
sudo env PATH="$PATH" PLAYWRIGHT_MODULE=/absolute/node_modules/playwright \
  PLAYWRIGHT_BROWSERS_PATH=/absolute/browser-cache \
  sh scripts/run-cross-module-browser-qa.sh /absolute/empty-output \
  /absolute/httpapi.test /absolute/agentclient.test /absolute/resource-server
```

只运行资源路径时，在全新Linux网络命名空间内启用lo并添加192.0.2.1/32，设置
`MYPROBE_TEST_RESOURCE_MAIN=1`、`MYPROBE_TEST_RESOURCE_SERVER`为上述Server
绝对路径；可选`MYPROBE_TEST_RESOURCE_OUTPUT`为自有绝对输出目录。测试限时六
分钟，并要求非宿主命名空间、仅lo、正确版本和运行前后相同二进制。绝不修改
宿主网卡、实际服务、全局设置或已有目录内容。

数据/密钥/密码/Token均为本次临时生成，Server不继承操作者MYPROBE配置。数据库
和日志保持私有，CI只上传汇总JSON、绑定结果及合成截图。结果不含节点/事件
标识、口令、会话、幂等键或请求体；只公布数量、阶段时间、身份一致布尔值和摘要。

这是单节点功能联验，不是CPU实际占用压测、浏览器资源告警交互、真实Provider、
目标硬件容量或24小时结论。当前运行证据按准确提交/干净状态记录，失败必须保留。
