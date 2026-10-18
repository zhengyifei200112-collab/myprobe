# 完整应用的大库跨平台恢复

`Store` 能打开解密后的数据库，不足以证明用户能通过站点完成恢复。本验收启动
实际 `cmd/server`，通过 HTTP 流式上传暂存归档，再重启实际 Server 激活，
验证登录、源 Agent 凭据与目的库隔离。它不操作正在提供服务的站点。

## 独立双向结果

运行源为 `75cf0d35b7d17e7b999cf069977d25c833b05f2c`。Windows/amd64 与独立
原生 Linux/amd64 两个方向均使用实际 HTTP 导出的 567,773,850 字节加密归档。
每个方向核对 63,830 条业务报告的完整有序字节及永久回执摘要，均证明：

- HTTP 暂存返回 202，待恢复文件在实际重启时激活并被消费；
- 原管理员重新登录成功；目的库原会话、原管理员密码被拒绝；
- 目的库原标记节点不在恢复库中，保留的旧数据库仍有该标记；
- 原 Agent Token 经实际 `Client.Run`、Gateway 新增 1 条落库报告；
- 原始报告及永久回执保持一致，输入归档前后摘要一致。

Linux 一向运行 76.61 秒，返回 Windows 一向 11.64 秒，均不含传输。
Linux 整个单元限 CPU25%、1 GiB、swap0，独立网络命名空间、只读系统视图；
退出后自有 cgroup 无进程，5 个生产容器的身份摘要未变。记录了运行中驱动的
摘要/命名空间/限制，未捕获 Server 子进程的运行中摘要；Server 文件在运行前后
与构建摘要一致。Windows 使用测试进程终止完成重启，不声称验证了优雅退出。

初始 Windows 种子来自较早 `2d7180f` 的合成 Store 归档；实际跨机的两份归档
均为 `75cf0d3` HTTP 导出。复核全部 824 个跟踪源文件，程序/夹具/归档/结果
分别绑定。[脱敏记录](evidence/application-portability-native-2026-10-18.json)保存
方向、摘要及范围。原凭据、数据库和归档仅留在私有目录，不进入公共附件。

## 可重复工具

`scripts/fixtures/application-portability` 仅接受新建的空结果目录及
`TestEncryptedBackupPortability` 生成的私有 512 MiB 合成夹具，不删除输入或旧结果。
构建前先完成前端基线，确认嵌入资源与 Git 一致。Linux 示例：

```sh
export MYPROBE_TEST_SOURCE_COMMIT="$(git rev-parse HEAD)"
fixture_root="$(mktemp -d)"
mkdir -m 700 "$fixture_root/bundle" "$fixture_root/run" "$fixture_root/tmp"
MYPROBE_TEST_BACKUP_PORTABILITY=export MYPROBE_TEST_BACKUP_PORTABILITY_MIB=512 \
  MYPROBE_TEST_BACKUP_PORTABILITY_BUNDLE="$fixture_root/bundle" \
  go test ./internal/backup -run '^TestEncryptedBackupPortability$' -count=1 -timeout 10m
go build -o "$fixture_root/server" ./cmd/server
go build -o "$fixture_root/driver" ./scripts/fixtures/application-portability
sudo env -i HOME=/root PATH=/usr/sbin:/usr/bin:/sbin:/bin TMPDIR="$fixture_root/tmp" \
  MYPROBE_TEST_SOURCE_COMMIT="$MYPROBE_TEST_SOURCE_COMMIT" \
  unshare --net sh -eu -c '
    ip link set lo up
    exec "$1" "$2" "$3" "$4" linux/amd64 "$5" export
  ' application-fixture "$fixture_root/driver" "$fixture_root/run" \
    "$fixture_root/server" "$fixture_root/bundle" "$MYPROBE_TEST_SOURCE_COMMIT"
```

身份参数必须对应输入生产者的系统/架构及完整源提交；执行文件需独立绑定源码和
摘要。`export` 在 Agent 写入之前通过实际 HTTP 导出归档及私有控制文件，因此
后续真实主机采集报告不进入传输归档。只传私有的 `archive.mpb`、`control.json`、
`producer.json` 到另一机器的新私有输入目录，校验完整摘要，再执行：

```text
driver NEW_EMPTY_RESULT_DIRECTORY SERVER_BINARY PRIVATE_INPUT_DIRECTORY SOURCE_OS/ARCH SOURCE_COMMIT export
```

返回源机器时，最后使用 `verify`，避免额外生成归档。Windows 原生程序使用相同
位置参数及环境变量；私有目录仅允许操作账户访问，所有路径必须绝对化。
预留至少 8 GiB 临时空间。工具使用专供合成容量夹具的已知测试口令/密钥，
不适用于真实生产备份。保留失败结果，排查后使用新的空目录。

CI `encrypted-backup-capacity` 新增单机原生 Linux 实际应用回归，只发布
`application-result.json`，不上传包含 Token 的控制文件、原始日志和归档。
该 CI 不是跨机/跨平台证据，本文 `75cf0d3` 结果也不冒充后继提交执行。

为逐字比较快照，此独立夹具三层保留期均为 3650 天，不改变生产默认值。
默认保留策略、100 节点积压、联合 24 小时、旧应用回退及生产 failover 分别验收。
