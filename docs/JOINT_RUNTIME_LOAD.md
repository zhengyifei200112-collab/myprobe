# 联合运行验收

夹具运行独立构建的正式 Server 和真实 `Client.Run`。合成数据只用于主机指标和历史语料。每个节点按 15 秒采集和上报，三个 TCP 目标按 60 秒执行。HTTP 观测先失败后恢复，默认通知发送器在隔离网络中经历拒收和恢复。验收还要求五个实际渲染的页面、Agent 重连、同库重启、队列排空，以及逐条核对报告、封套、回执与入库结果。

自用验收先跑 5 节点短测，再跑 20 节点短测。两次通过后，用同一冻结源码跑 20 节点连续 24 小时。源版本、运行时、目标、数据密度、准备与测量窗口、资源和故障时间须一并保存。参考环境为 2 vCPU、2 GiB、SSD。概览 P95 须低于 500 ms，稠密 30 天历史 P95 须低于 2 s，Server 采样峰值 RSS 须低于 700 MiB。记录全年历史查询供诊断，保持原有 30 天和一年保留层覆盖。

## 运行准备

1. 确认当前测试机、挂载点、可用内存和磁盘。稠密数据在实际夹具卷检查每节点 500 MiB、每节点每小时积压 1 MiB，加 8 GiB 空闲下限。备份、老化和失败样本保留须按实测高水位另计。一次只启动一轮。
2. 在冻结检出编译 `go build -o /absolute/joint-server ./cmd/server` 和 `go test -c -o /absolute/agentclient.test ./internal/agentclient`。保留源码摘要、可执行文件摘要及相同检出的脚本。Linux 需 `unshare`、`ip`、systemd/cgroup v2。五页面模式需要已安装的 Playwright 与浏览器。
3. 用本轮专用绝对路径设置 `MYPROBE_TEST_LOAD_OUTPUT`、`MYPROBE_TEST_LOAD_PRIVATE_FIXTURE_DIR`、`MYPROBE_TEST_LOAD_PROCESS_LOG_DIR`。私有目录先以 0700 创建。记录所属单元、进程和 cgroup，便于中断后检查。
4. 设置 `MYPROBE_TEST_LOAD_NODES=5`、`MYPROBE_TEST_LOAD_DURATION=5m`、`MYPROBE_TEST_LOAD_HISTORY=year`、`MYPROBE_TEST_LOAD_RECONCILE=1`、`MYPROBE_TEST_LOAD_BROWSER=1`、`MYPROBE_TEST_LOAD_SYSTEMD=1`，并显式给足 `MYPROBE_TEST_LOAD_TIMEOUT` 和外层监督器预算。准备窗口、实际测量窗口和排空分开计时。随后执行 `sh scripts/run-joint-load.sh /absolute/agentclient.test /absolute/joint-server`。脚本用 root 创建无外网路由的新网络命名空间，内部执行真实启动失败取证和负载测试。
5. 第二轮节点数设为 20，使用新输出目录。长测时指定 `24h` 测量时间，外层预算覆盖最长两小时准备、24 小时测量及收尾，例如 27 小时。独立运行分别记录条件，保留每轮终态。

拒收接收端和 Go WebSocket 订阅模式用于有限组件检查。拒收模式关闭自动版本查询，使本机检查只接触自有测试端点。正式验收使用隔离通知接收端与五个渲染页面。在线备份重叠是单独的有界检查，设置 `MYPROBE_TEST_LOAD_BACKUP=1`，要求稠密年历史、回执核账和 3 至 15 分钟测量窗口。

## 逐请求取证

`joint-api-requests.jsonl` 每行对应测量循环的一次请求。`endpoint` 标明概览、健康、30 天或一年历史。历史请求的 `node` 是本轮从 1 开始的合成节点编号。`started_ns` 是相对测量开始的单调偏移，`headers_ns` 记录收到响应头时的耗时，`elapsed_ns` 包含响应读取。失败样本保存状态及有限错误分类。文件只写这些诊断字段。

JSON 汇总中的 `APIRequestTrace` 绑定记录数、字节数、上限、SHA-256 和同步关闭状态。逐请求耗时使用计算原有分位数的同一计时值。按节点编号和查询范围查看尾部样本，结合资源时间线、封套证据和已知故障窗口分析。写入出错或证据不完整时验收失败。健康排空和周期服务状态查询另记，分位数仍对应原测量请求集。

结束时核对本轮 Server、Agent、浏览器、控制进程与 cgroup 都已退出。保留摘要、日志和经过隐私检查的跟踪文件。按工作区规则检查成功夹具路径及文件归属后释放；失败原始 DB、WAL、SHM 按字节取证与归档。分别记录磁盘和可用 RAM。
