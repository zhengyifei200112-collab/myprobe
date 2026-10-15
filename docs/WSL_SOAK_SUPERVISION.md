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

后续重新运行最终整合源的五节点短测，再从零开始 100 节点完整 24 小时测量。
不重启旧的未完成测量来拼接时间；不把旧 #70 源的部分结果用于后继网络质量代码验收。
