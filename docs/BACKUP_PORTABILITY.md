# 加密备份跨平台与异机恢复演练

此工作包补齐 SYS-02 的可重复异机恢复步骤，复用生产 `.mpb` 格式、快照、解密、
`StageDatabaseRestore` 和 `ApplyPendingRestore`，不改变公开接口、schema 或部署配置。
它与[静止大库容量](BACKUP_CAPACITY.md)、[在线备份负载](ONLINE_BACKUP_LOAD.md)及
完整 24 小时验收分别记录，不能互相替代。

已有一致SQLite备份的操作者可另用[隔离升级演练](OFFLINE_UPGRADE_REHEARSAL.md)
核对旧库到当前迁移、原始记录保留和旧schema恢复副本。该步骤不启动Server/Provider，
也不替代下述加密归档恢复或完整应用切换。

## 演练内容

`TestEncryptedBackupPortability` 默认跳过。导出默认使用 32 MiB 合法合成报告、一位管理员、
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

## 大库档位与输入绑定

`MYPROBE_TEST_BACKUP_PORTABILITY_MIB` 只接受32、512、1024，默认32。它控制有效
报告JSON的造数目标，不是稀疏文件、空白BLOB或指定的密文字节数；真实数据库及
归档大小分别记录。源端与目的端必须显式选择相同档位，导入在读取大归档前核对
档位、来源平台、完整提交和每个驱动摘要。`producer.v2`记录RequestedMiB和
SourceBytes，并与私有控制文件再次比较；`recovery.v2`保留完整来源描述。v1属于
历史32MiB测试证据，当前同源驱动拒绝它；这不是生产归档或公开API格式变化。

临时卷预检至少需要8倍档位加1GiB；导出还检查包所在卷。哈希与解密保持流式，
密文上限为2倍档位加64MiB，32MiB档位原128MiB上限不变；小描述仍限制64KiB，
不会把可选的大库上限套到描述解析。512/1024MiB建议分别保留10/15分钟独立演练
预算。这些是测试预算，不修改Server请求超时或生产备份策略。

CI的本机roundtrip选择512MiB，保留错误口令/截断/应用密钥拒绝、渠道解密、正式
暂存与激活、完整报告及永久回执摘要/回放、管理员和Agent凭据、原目的库保留及
凭据隔离等11项检查。CI成功仅证明该机本地大库路径；异机结果仍须绑定独立机器、
传输密文摘要、两端实际程序和准确源。Store路径也不冒充HTTP上传或Server重启。

## 独立进程与机器的操作

在同一冻结提交分别构建源/目的原生测试二进制，记录源码归档和两个可执行文件的
SHA-256。Go 测试包工作目录为 `internal/backup`，用于读取实际源码 hash。目的机器
不需要源机器的 Go 编译器或源数据库，但需要同一提交的源文件供证据核对。

以 Windows/amd64 → Linux/arm64 为例，先创建空的、仅当前用户可读的私有导出目录，
以及单独的结果目录。Windows 的 Go 文件模式不能替代 ACL，须使用合适的继承权限。
源端通过环境变量传路径，命令行不包含真实秘密：

```powershell
$env:MYPROBE_TEST_BACKUP_PORTABILITY='export'
$env:MYPROBE_TEST_BACKUP_PORTABILITY_MIB='512' # Optional; default 32. Match import.
$env:MYPROBE_TEST_SOURCE_COMMIT='<full frozen commit>'
$env:MYPROBE_TEST_BACKUP_PORTABILITY_BUNDLE='<existing empty private directory>'
go test ./internal/backup -run '^TestEncryptedBackupPortability$' -count=1 -timeout 10m -v
```

通过已有加密传输把三个文件送入目的端新建的私有目录；核对传输前后密文摘要。不要
把包上传到公共 CI、Git、聊天或公开对象存储。控制文件含一次性合成令牌，只有
`producer.json` 和成功的 `recovery.json` 可作为筛选后的公开证据。测试口令和应用
密钥是已公开的合成常量；此夹具不应用于生产备份。

在目的机器的 `internal/backup` 目录执行原生测试二进制：

```sh
MYPROBE_TEST_BACKUP_PORTABILITY=import \
MYPROBE_TEST_BACKUP_PORTABILITY_MIB=512 \
MYPROBE_TEST_SOURCE_COMMIT='<same full frozen commit>' \
MYPROBE_TEST_BACKUP_PORTABILITY_ORIGIN=windows/amd64 \
MYPROBE_TEST_BACKUP_PORTABILITY_BUNDLE='<absolute private transferred directory>' \
MYPROBE_TEST_BACKUP_PORTABILITY_OUTPUT='<absolute empty result directory>' \
/absolute/backup.test -test.run='^TestEncryptedBackupPortability$' -test.count=1 -test.timeout=10m -test.v
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

## 已执行的原生异机验收（2026-10-16 UTC）

冻结源码 `dc5152ba629ee984a5deba1cbd31277d44c1c93f` 已完成实际双向传输与原生
导入，不再是仅有操作步骤。Windows/amd64 与独立 Linux/arm64 分别使用各自的
Go 1.26.5 原生测试二进制；目的端未依赖源数据库或源编译器。每方向3990条报告，
加密归档均36,369,150 B，均完成以上十一项检查，包括原应用密钥解密渠道、生产
暂存/激活、快照/永久回执、原Agent认证、管理员登录和目的旧库/凭据隔离。

| 方向 | 导出密文 SHA-256 | 目的原生检查 |
| --- | --- | --- |
| Windows/amd64 → Linux/arm64 | `13ec7d050b629389b73545de89ef17a49ceccc695f6d9344b7a096c4ac92c432` | 11项通过 |
| Linux/arm64 → Windows/amd64 | `59112af10fb0e613e10e7592b1713ac5165280a6aba4fc7676b5e962f8069d86` | 11项通过 |

冻结源码归档 SHA-256 为
`375d72534536102b7351ed598b781199e51a8044ad68e0cfad10717ccd92a954`；Windows
二进制为 `7feb402caad70d76340fc7bb6c8b9ad7749de847b79a728303392e6338b5ebc6`，
Linux 二进制为 `3e717bec03929821cdb1af797a58b14ce5c4a639aa1da03609123f90d444bb13`。
筛选后的[验收清单](evidence/backup-portability-dc5152b-20261017.json)保留来源提交、
实际平台、归档/可执行文件摘要、报告数与检查名，不包含控制文件、合成令牌或主机
地址。私有传输包及未筛选日志继续由操作者保管，不上传Git/CI。

该提交的[CI38949952716](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38949952716)
九项通过；CI同机roundtrip不是上述真实跨机结果的替代证明。此文档更正没有重跑
原生演练，也不把后续源码和迁移重新标记成该冻结版本。

实际范围仍为32MiB合成数据的Store级生产恢复路径。跨机大库、真实HTTP上传、
完整Server重启、流量切换和生产failover仍待验收；当前整合源也须重新检查兼容性。
完整 M1—M5、最终整合源24小时、原始容量目标、外部Provider/OAuth与所有者合并/
发布审批继续保留。
