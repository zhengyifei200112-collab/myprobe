# 加密备份跨平台与异机恢复演练

此工作包补齐 SYS-02 的可重复异机恢复步骤，复用生产 `.mpb` 格式、快照、解密、
`StageDatabaseRestore` 和 `ApplyPendingRestore`，不改变公开接口、schema 或部署配置。
它与[静止大库容量](BACKUP_CAPACITY.md)、[在线备份负载](ONLINE_BACKUP_LOAD.md)及
完整 24 小时验收分别记录，不能互相替代。

## 演练内容

`TestEncryptedBackupPortability` 默认跳过。导出使用 32 MiB 合法合成报告、一位管理员、
一个节点、加密通知渠道、HTTP 服务与真实告警导入回执，通过生产 Repository 生成
加密快照。备份后新增节点不会出现在还原中。导出仅保留 `archive.mpb`、私有
`control.json` 和 `producer.json`，不传源数据库、WAL 或绑定原路径的仓库归属文件。

导入先核对密文大小/摘要、控制文件摘要、声明源码、实际源文件 hash 和明确指定的
来源 OS/架构；拒绝错误口令、截断归档及错误应用密钥。用单独的原应用密钥检查渠道
可解密后，在全新临时目录创建目的数据库，通过生产暂存与激活路径替换它，并核对：

- 完整报告字节和永久回执摘要、节点/用户/渠道/报告数；
- 原 Agent 凭据可认证、管理员可登录、原导入请求仍幂等回放；
- 目的旧库保留，源与目的原有令牌没有混入另一份数据库。

无外部通知请求或生产凭据。失败不写成功结果，不覆盖既有导出或 `recovery.json`。
恢复数据库由测试临时目录清理；导出包留给操作者安全转移及演练后清理。

## 独立进程与机器的操作

在同一冻结提交分别构建源/目的原生测试二进制，记录源码归档和两个可执行文件的
SHA-256。Go 测试包工作目录为 `internal/backup`，用于读取实际源码 hash。目的机器
不需要源机器的 Go 编译器或源数据库，但需要同一提交的源文件供证据核对。

以 Windows/amd64 → Linux/arm64 为例，先创建空的、仅当前用户可读的私有导出目录，
以及单独的结果目录。Windows 的 Go 文件模式不能替代 ACL，须使用合适的继承权限。
源端通过环境变量传路径，命令行不包含真实秘密：

```powershell
$env:MYPROBE_TEST_BACKUP_PORTABILITY='export'
$env:MYPROBE_TEST_SOURCE_COMMIT='<full frozen commit>'
$env:MYPROBE_TEST_BACKUP_PORTABILITY_BUNDLE='<existing empty private directory>'
go test ./internal/backup -run '^TestEncryptedBackupPortability$' -count=1 -timeout 3m -v
```

通过已有加密传输把三个文件送入目的端新建的私有目录；核对传输前后密文摘要。不要
把包上传到公共 CI、Git、聊天或公开对象存储。控制文件含一次性合成令牌，只有
`producer.json` 和成功的 `recovery.json` 可作为筛选后的公开证据。测试口令和应用
密钥是已公开的合成常量；此夹具不应用于生产备份。

在目的机器的 `internal/backup` 目录执行原生测试二进制：

```sh
MYPROBE_TEST_BACKUP_PORTABILITY=import \
MYPROBE_TEST_SOURCE_COMMIT='<same full frozen commit>' \
MYPROBE_TEST_BACKUP_PORTABILITY_ORIGIN=windows/amd64 \
MYPROBE_TEST_BACKUP_PORTABILITY_BUNDLE='<absolute private transferred directory>' \
MYPROBE_TEST_BACKUP_PORTABILITY_OUTPUT='<absolute empty result directory>' \
/absolute/backup.test -test.run='^TestEncryptedBackupPortability$' -test.count=1 -test.timeout=3m -test.v
```

反向测试使用 Linux 原生导出和 Windows 原生导入，来源改为 `linux/arm64`；每次用新
目录，不能复用成功结果。`roundtrip` 模式在一个进程创建临时包，仅作本地链路回归，
需要指定同样的完整源码值与结果目录。CI 运行该模式并仅保留结果 JSON；它不是
异机或异架构通过声明。

## 证据边界

结果分别记录源/目的真实 OS、架构、Go、源码文件摘要、密文摘要、报告数和完成的
检查。源码提交由运行者声明，须另用冻结 manifest 将源码归档、实际二进制、构建
参数、机器与传输观察绑定；提交字符串或 `CrossPlatform=true` 本身不能证明异机。

当前实现只验证 Store 的生产还原路径，不包括真实 HTTP 上传、完整 Server 重启、
DNS/业务流量切换或生产 failover。32 MiB 跨机成功不扩大已验证的大库上限。应用
密钥与归档口令分别恢复，不能从“归档可解密”推断通知凭据可用。

实际跨机结果尚待执行；实现和 CI 中加入步骤不计为通过。完整 M1—M5、最终整合源
24 小时、原始容量目标、外部 Provider/OAuth 和所有者合并/发布审批继续保留。
