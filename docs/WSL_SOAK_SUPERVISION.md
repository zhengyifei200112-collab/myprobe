# Windows / WSL 长稳测试托管

原 ecfed96 的 24 小时测试在 WSL 有序关机时中断，只有约 2 小时 45 分钟的部分资源记录，
不能继续累计时长，也没有最终通过结果。关机触发者未证实，见
[中断事实](evidence/joint-soak-interruption-2026-10-15.json)。

`scripts/start-wsl-soak.ps1` 为一次可信、本地、合成数据测试建立独立 Windows PowerShell
进程，并持有前台 `wsl.exe` 直到 Linux 脚本退出。使用 Windows PowerShell 5.1 执行 worker；
启动者可以使用 5.1 或 PowerShell 7。所有辅助窗口隐藏，不修改全局电源策略、WSL 配置，
不注册后台服务或计划任务，也不自动重启失败测试。

[微软 Start-Process 文档](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.management/start-process)
说明 Windows 启动的进程独立于启动 shell；
[WSL systemd 文档](https://learn.microsoft.com/en-us/windows/wsl/systemd) 说明 systemd 服务
本身不能保持 WSL 存活。因此需要实际运行生命周期实验，不能仅从 Server systemd 单元推断。

## 启动

先准备一个使用 LF 的可信 Linux shell 文件，固定源码提交、Server/测试二进制 hash、
完整参数和独立输出目录。脚本必须等待实际测试结束，并用 `exec` 或 `set -e` 保留退出码。
不能让测试在 Linux 后台启动后立即退出。不要在脚本、环境、命令行或日志使用生产凭据。
本 launcher 以 WSL root 执行脚本，适用于既有网络命名空间/限额夹具，不用于运行外部未知脚本。

```powershell
./scripts/start-wsl-soak.ps1 `
  -Distribution Ubuntu-24.04 `
  -LinuxScript /absolute/path/to/frozen-joint-soak.sh `
  -OutputDirectory 'D:/test-results/joint-soak-new-run'
```

OutputDirectory 必须是绝对路径且尚不存在。每次运行使用新目录，旧中断目录绝不复用。
启动时复制 supervisor 到该目录，保存 run ID、启动 PID、时间、发行版、脚本路径和脚本 hash。
这些文件属于本机运行记录，含本机路径；发布证据时使用经过筛选的投影。

## 观察与结果

目录中包含 `manifest.json`、冻结的 `supervisor.ps1`、原子替换的 `status.json`，以及
supervisor 与 WSL 的独立 stdout/stderr 日志。worker 每约五秒更新 observed_at，保存
supervisor/WSL PID、started_at、ended_at 和真实 WSL 退出码。

```powershell
Get-Content -LiteralPath 'D:/test-results/joint-soak-new-run/status.json' -Raw | ConvertFrom-Json
```

- launching / starting：托管启动中，没有实际测试就绪的证据。
- running：Windows WSL 客户端尚未退出；不意味着 Server、页面或测试断言通过。
- exited：客户端退出，exit_code 为其实际退出码。非零就是失败，不重试。
- supervisor_failed：托管自身失败；Linux 任务结果可能未知，必须检查独立进程及日志。
- 长时间未更新：观察失联，不能把旧 running 解释为当前存活。

`test_acceptance` 始终为 not_evaluated，包括退出码 0。完整验收仍须检查测试最终 JSON、
请求时长、报告/任务计数、故障阶段、排空、页面收敛和资源记录。窗口关闭、宿主睡眠/关机、
用户主动终止 WSL 等仍可能中断运行；launcher 不提供抗宿主重启或持久服务承诺。

Linux job 应设置自己的 timeout 和清理 trap。需要停止时只终止本次经过身份核对的任务，
不能执行全局 `wsl --shutdown` 或按进程名称批量停止。不要单独终止 supervisor 后假定
Linux 任务已经停止；这里不提供含糊的 PID 复用式自动停止命令。

## 验证要求

先运行约 90 秒合成心跳任务，确认启动 shell 已退出后 worker/WSL 和 Linux 心跳继续，
最终保存 0 退出码；另运行已知非零退出任务，确认准确保存失败且没有重试。
同时检查含空格、中文、单引号的路径、重复输出目录拒绝和 PowerShell 解析。
这些只证明启动会话独立性；没有覆盖应用退出、宿主睡眠/重启或完整 24 小时稳定性。

2026-10-15 实现 `847cc4f11dda32371f42e3699e04ab3188cf6b23` 从 PowerShell 7 启动时已通过上述
生命周期检查：启动 shell PID 26352 退出后，40 秒时独立 worker、WSL 客户端和 Linux
心跳仍在运行；90.33 秒后记录退出 0，完整 90 条心跳和完成标记。快速失败任务在约
0.22 秒后准确记录退出 31，stdout/stderr 各 2097152 字节，无 pipe 阻塞；同目录再次启动
被拒绝且原 manifest hash 不变。中文、空格和单引号路径均实际使用。
原始状态投影、源码 hash、失败修复和边界见
[验证证据](evidence/wsl-soak-supervision-2026-10-15.json)。此前调试失败没有用于累计时长。

验证时修复了 Windows PowerShell 5.1 将 .NET string 的 $null 转为空字符串、WSL 自行
解析带引号的选项，以及 Start-Process 极快退出后 ExitCode 为空的问题。worker 使用明确
NullString、保留无空白选项、从启动起拥有 .NET 进程句柄，并并发复制两路管道；终态前
完成日志写入。没有通过放宽 Linux 退出码或自动重试来掩盖这些问题。

后续增加 `scripts/test-wsl-soak.ps1` 实际从另一个 Windows PowerShell 5.1 启动 shell 执行
launcher，确认该 shell 确实退出后再检查 Linux 心跳和两个真实进程。这个回归发现
5.1 的 Start-Process -Redirect* 会使启动者持有异步流处理，直到 worker 结束；现在把
supervisor 日志重定向放在 worker 内，启动进程保持独立。使用当前源码 hash 的回归结果
单独保存，不把早期 PowerShell 7 的通过当作 5.1 的证明。
最终自动回归已通过 90 条心跳、真实启动 shell 退出、准确 0/31 退出码、双路各 2 MiB
日志完整及旧目录拒绝；终态前日志已可读取。准确脚本 SHA-256 为
`0e1cfe654432f8b56972e6512184d1f8bd18cb2ea4b0762ce82711fdc1b611d8`，
见 [自动回归原始结果](evidence/wsl-soak-regression-2026-10-15.json)。

```powershell
./scripts/test-wsl-soak.ps1 -Distribution Ubuntu-24.04 `
  -OutputDirectory 'D:/test-results/soak-regression-new' `
  -LinuxOutputDirectory /mnt/d/test-results/soak-regression-new
```

两个目录参数须指向同一个新建本地目录（Windows / WSL 各自的路径）。默认 90 秒；
12—180 秒可明确选择。生成心跳/双管道合成脚本，检查真实终态、完整输出、目录碰撞拒绝，
并把源码 hash 与 result.json 写入本地目录；不需要凭据、不访问公网目标。

同一运行源码和冻结二进制也在托管下通过五节点稠密一年历史的两次正式联合短测：

| 模式 | 测量秒 | 库字节 | 发送 / ACK / 入库 | 资源采样 | Server 不可读取样本 |
| --- | --- | --- | --- | --- | --- |
| Go WebSocket 消费者 | 91.53 | 1475313664 | 34 / 34 / 34 | 93 | 1 |
| 五个真实 Chromium 页面 | 90.63 | 1483223040 | 26 / 26 / 26 | 92 | 0 |

两次均使用 CPU 200%/CPU 0—1/2 GiB/swap 0，报告/封套核对差异为零、通知排空通过。
Go 模式在受测重启阶段观察到一次 Server 不可读取，不能写成零不可用。真实页面错误为零，
五页各收敛到全部五节点；峰值采样 RSS 60559360 B、WAL 5203592 B，一年历史 API
P95 1404.59 ms。原始 [Go 结果](evidence/wsl-supervised-joint-five-go-2026-10-15.json)
及 [页面结果](evidence/wsl-supervised-joint-five-browser-2026-10-15.json) 各自保留源码与二进制
hash。这是当前网络质量源码后的短时真实联合检查，不是网络质量八节点一年 HTTP 容量，
也不是完整 24 小时。

后续重新运行最终整合源的五节点短测，再从零开始 100 节点完整 24 小时测量。
不重启旧的未完成测量来拼接时间；不把旧 #70 源的部分结果用于后继网络质量代码验收。
