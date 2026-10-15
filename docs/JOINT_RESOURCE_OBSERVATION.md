# 联合运行的独立资源采样

稠密历史查询超过一秒后，原请求循环中的 RSS/WAL 采样会同步变慢。
一秒 ticker 并不等于实际每秒采样，因此该实现将资源读取放到独立 goroutine，
继续覆盖测量与通知排空阶段，目标周期一秒，不依赖 HTTP 请求完成。

Server 重启时发布只含 PID、cgroup 路径、数据库路径及累计 CPU 偏移的观察目标。
采样线程不访问正在修改的进程控制器；重启窗口保留 WAL 观察，Server 不可读取的次数
单独统计，不把零 RSS 当作服务器健康或内存下降。

结果保留采样数、不可读取次数、目标周期、实际最大采样间隔和采样最大 RSS/WAL，
每约 30 秒及结束时保存趋势点。CPU 统计沿用两个 Server 生命周期累计口径。
线程调度和文件读取仍可能产生延迟；这些仍是采样最大值，不是绝对瞬时峰值。

指定 `MYPROBE_TEST_LOAD_OUTPUT` 后，开始、每约 30 秒和采样结束时会同步写入并原子替换
`joint-resource-<节点数>-<订阅模式>.json`。文件始终标记 `observation-only`，包括采样计数、
最大间隔及已有趋势，不能作为整个测试通过的凭据。写入失败累计计数，并使最终测试失败；
输出目录应为已存在的绝对路径，正常最终联合运行 JSON 仍单独生成。

自动化测试让一个真实 HTTP 请求持续阻塞，验证资源采样仍能捕获中途 RSS/WAL 峰值；
另验证重启观察目标、CPU 偏移以及并发目标更新。Linux CI 对这些测试启用 race detector。
进度文件的覆盖替换和存储失败计数也有回归测试。
历史 API 查询节奏仍为串行一秒 ticker，采样线程的频率不能用于推断请求频率。

本工作依赖稠密历史联合夹具，仍需真实联合运行及 24 小时趋势证据；源码测试通过
不代表长时间稳定性或完整容量验收已完成。

## 2026-10-15 短时验证

实现 `ecfed969bd444a58286006a3a032e2cadadc6602` 的五节点稠密历史、五个真实页面、
Server CPU 200%/CPU 0—1/2 GiB/swap 0 联合运行通过。约 90 秒测量和恢复阶段获得
93 次资源采样，最大间隔 1.004 秒，进度写入失败 0；每类 API 完成 52 次请求。
RSS/WAL 采样最大值 55246848/5582632 B。报告尝试/ACK/入库均 31，126 个生成封套
全部进入观察层，差异为零；通知排空和全部节点在五页最终收敛通过。

源代码、二进制 hash、原始结果和最终进度快照见
[证据文件](evidence/joint-resource-observation-2026-10-15.json)。本地完整 Go 测试仅
既有 Windows HostRoot #47 失败，其余包及 vet/build/gofmt/差异检查通过；CGO 关闭。
准确实现 CI [38696911014](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38696911014)
全部成功，包含资源测试的 Linux race 检查、正式库/Agent、ENOSPC、稠密联合运行、容器与脚本。
CI 原始资源结果亦保存于证据文件；Go 订阅者和托管硬件的范围与本地真实页面/限额分开。

100 节点 24 小时测试使用同一组最终二进制，附加
`MYPROBE_TEST_LOAD_NODES=100 MYPROBE_TEST_LOAD_DURATION=24h MYPROBE_TEST_LOAD_TIMEOUT=26h`，
保留 `year` 历史、对账、真实页面及 systemd 限额。结果须等真实运行结束并核对，
不能用短时通过或运行中的进度文件替代。

## 2026-10-15：24 小时运行已中断，未通过验收

原 ecfed96 / 100 节点运行在 WSL 有序关机时中断。最后资源样本为
2026-10-15 10:32:10 UTC（9895 条），旧 WSL boot 的最后 journal 为 10:32:38 UTC；
其中记录 `The system will power off now!` / `System is powering down.`。
13:50 UTC 检查时原 PID 384 和 exec 63409 均不存在，WSL 已是新 boot；没有最终结果 JSON。
Windows 未重启，但当前进程无权读取 Windows System 事件，具体关机发起者/触发原因未证实。

这是约 2 小时 45 分钟的部分观测，不能声明 24 小时、最终序列核对/排空或 8/12/16 小时
故障阶段通过。保留 [原始资源片段](evidence/joint-resource-100-24h-interrupted-2026-10-15.json)
及 [中断事实](evidence/joint-soak-interruption-2026-10-15.json)。停止前采样未观察到 Server
不可用，不等同于整段运行无数据丢失。

后续完整长运行必须使用独立于短暂工具会话的托管并保持宿主 WSL 会话，先验证生命周期，
再从零测量完整 24 小时。不能拼接旧时长或依赖 systemd 服务自动保持 WSL 存活；
[微软说明](https://learn.microsoft.com/en-us/windows/wsl/systemd) 明确后者不成立。
不修改本机全局电源设置，也不把测试环境中断当成已定位的 Server 运行故障。
