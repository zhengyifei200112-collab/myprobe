# 稠密历史下的联合运行验收

该夹具在真实 Server、Client.Run、TCP/HTTP 执行器、通知接收端和五个页面同时运行前，
为每个节点写入完整的保留期数据。用于检验长历史查询对实时功能的影响。

## 数据与执行范围

- 每节点最近七天每 15 秒一份指标；每个节点三个实际 TCP 目标每 60 秒一个历史结果。
- 第 8—30 天使用一分钟桶；更早至一年使用五分钟桶，指标、流量和三个目标均有数据。
- 保留正式索引。生成完整合成 report JSON，并使用统一时间原点生成递增网络计数。
- 补充造数耗时对应的尾部，通过真实 SaveReport 初始化最新状态和流量缓存。
- 正式 Server 启动和重启均正常执行保留任务。SQL 造数时间单独记录，不属于实时运行时长。
- 造数结束记录指标、延迟表的最大 ID；实时报告对账和 TCP 成功计数仅读取之后的记录，
  防止历史数据掩盖实时采集或任务缺失。

启动后先对所有节点的 30 天和一年接口逐一校验，再开始联合测量。测量中轮换节点，
连续请求概览、30 天历史、系统健康和一年历史；历史耗时分别记录。
每个历史响应验证完整内部桶的连续性、固定 CPU/内存值、三个 TCP 目标的成功率/延迟，
以及相邻累计流量的精确差值。请求范围的部分起始桶及实时尾部不应用固定历史值断言。
另有自动化反例测试，要求缺层、缺目标、缺桶、重复桶和错误数值被拒绝。

## 复现

按 [联合运行环境](JOINT_RUNTIME_LOAD.md) 准备 Linux 测试二进制和真实 Server，
使用 [报告对账与页面收敛](RUNTIME_RECONCILIATION.md) 的相同选项，再增加：

```sh
export MYPROBE_TEST_LOAD_HISTORY=year
export MYPROBE_TEST_LOAD_NODES=5 # 随后单独运行 50、100
export MYPROBE_TEST_LOAD_TIMEOUT=70m
export MYPROBE_TEST_LOAD_RECONCILE=1
export MYPROBE_TEST_LOAD_BROWSER=1
export MYPROBE_TEST_LOAD_SYSTEMD=1
sudo -E sh scripts/run-joint-load.sh /absolute/agentclient.test /absolute/server
```

测试要求夹具所在卷至少每节点 500 MiB 加额外 8 GiB 可用空间；实际库大小记入结果。
100 节点需预留数十 GB，同时确认 WSL 虚拟磁盘的宿主卷容量。
所有目标均在新建网络命名空间内，正式通知访问策略保持生效。

## 证据边界

这是 SQL 生成的历史加真实联合负载，不是一年的真实采集，也不自动证明 24 小时稳定性。
每次运行需保留源码/Server 二进制 hash、实际资源限额、造数耗时与行数、API P95、
RSS/WAL 采样、报告发送/ACK/入库核对、任务结果、通知排空和每页全部节点最终收敛。
共享宿主上的 WSL/cgroup 结果不能替代独立物理低配 SSD 环境验收。

当前运行结果及准确提交检查在后续证据文件和本节补充；未完成规模不得推断为通过。
