# 跨模块浏览器生命周期验收

当前整合前端已将“聚合等待”改为带单位的文本输入。早期聚合与策略浏览器夹具仍按
`spinbutton` 定位，默认 Go 测试又跳过显式启用的浏览器用例，因此普通全仓测试成功
不能证明这些用户流程仍可操作。定位现按实际可访问名称和 `textbox` 角色匹配，
原有数值、事件、投递、权限、重试和页面断言保留。

`scripts/run-cross-module-browser-qa.cjs` 运行六项管理生命周期用例：范围策略、
维护（未发/在途响应）、知悉、静默、聚合（接收后丢响应、重试）和单事件通知链接。
它启动合成 SQLite/API/评估器/工作线程/本地接收端及真实嵌入页面；不是全部正式
`main` 进程联合容量测试。亮暗主题、360/768/1440 px 由原有脚本分别验证。

Linux 完整配置另外运行两个真实 `Client.Run` 用例：HTTP 维护统计经过实际126秒
成熟期；服务故障/恢复、缺测、诊断和事件链接经过真实调度及本地目标。
`scripts/run-cross-module-browser-qa.sh` 只在新网络命名空间内创建 loopback 和
测试私网地址。运行器拒绝宿主网络或额外网卡；不会调整宿主网卡、放宽正式访问
策略、访问真实 Provider 或改变生产服务。浏览器默认在 Windows 使用 Edge，其他
平台使用已安装的 Playwright Chromium；`BROWSER_CHANNEL` 可显式指定其他通道。
Linux Chromium的共享内存句柄使用标准`/tmp`；Go数据库/日志和Playwright配置目录
仍在私有目录。把浏览器进程的`TMPDIR`也指向0700日志父目录会导致渲染器权限拒绝
并崩溃；原始失败与最小复现保留，不通过放宽私有目录权限修复。

## 复现

从冻结源码构建本机原生测试可执行文件，创建全新空输出目录。以下路径均为占位：

```sh
go test -c -o /absolute/httpapi.test ./internal/httpapi
go test -c -o /absolute/agentclient.test ./internal/agentclient
mkdir -m 700 /absolute/empty-output
sudo env PATH="$PATH" \
  PLAYWRIGHT_MODULE=/absolute/node_modules/playwright \
  PLAYWRIGHT_BROWSERS_PATH=/absolute/browser-cache \
  sh scripts/run-cross-module-browser-qa.sh \
  /absolute/empty-output /absolute/httpapi.test /absolute/agentclient.test
```

Windows 只运行无需私网目标的六项管理流程：

```powershell
go test -c -o C:/owned/httpapi.test.exe ./internal/httpapi
New-Item -ItemType Directory C:/owned/empty-output
$env:PLAYWRIGHT_MODULE='C:/absolute/node_modules/playwright'
node scripts/run-cross-module-browser-qa.cjs C:/owned/empty-output C:/owned/httpapi.test.exe
```

输出的 `private/` 包含合成测试日志和临时文件，保持私密。CI只收集筛选后的
`result.json` 与 PNG截图，不上传整个临时目录、数据库或浏览器状态。
结果记录准确提交、干净状态、源文件/产物摘要、实际两个测试程序摘要、浏览器与
Node版本、逐项用例/退出码和截图摘要。跳过任何必需用例都会失败；脚本不会把
只有顶层 `PASS` 的空运行当作完成。源码和二进制运行前后也须一致。

原a5f8577上策略/聚合两个旧定位超时的失败仍保留；修正脚本后的结果须另存，
不能改写成原轮次通过。CI独立job不延长既有主测试的时限。

这些合成结果不能替代真实GitHub授权、Telegram客户端及发送、完整生产恢复、
整合源码的容量/24小时和所有者合并发布验收。
