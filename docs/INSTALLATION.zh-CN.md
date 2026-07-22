# MyProbe 安装与升级

MyProbe 提供一键安装、Docker、Release 二进制和源码构建四种受维护的部署方式。
生产环境建议固定版本，并始终把 Server 放在支持 WebSocket Upgrade 的 HTTPS 反向
代理之后。

## 方式选择

| 方式 | 适用场景 | 升级方式 |
| --- | --- | --- |
| 一键安装脚本 | 使用 systemd 的 Linux amd64/arm64 VPS | 重新运行脚本或使用 `update` |
| Docker Compose | 已使用容器运维的主机 | 拉取新的固定版本镜像 |
| Release 二进制 | 非 systemd、Windows、macOS Agent 或自定义服务管理 | 校验后替换二进制 |
| 源码构建 | 开发、代码审计和定制 | 从已审查的提交重新构建 |

一键脚本、预构建镜像和二进制都依赖 GitHub Release。仓库发布首个版本标签之前，
请使用 Docker 本地构建或源码构建。

## 一键安装 Server

先下载脚本，审查后再以 root 权限执行：

```bash
curl -fsSL https://raw.githubusercontent.com/zhengyifei200112-collab/myprobe/main/install.sh -o install.sh
chmod +x install.sh
less install.sh
sudo ./install.sh server
```

脚本会：

1. 检查 Linux、systemd 和必要命令。
2. 识别 amd64 或 arm64 架构。
3. 下载最新 Release 的 Server 二进制和 `SHA256SUMS`。
4. 在校验通过后安装到 `/usr/local/bin/myprobe-server`。
5. 创建权限为 `0600` 的 `/etc/myprobe/server.env`。
6. 安装并启动加固后的 `myprobe.service`。

默认监听 `127.0.0.1:25775`。首次安装会隐藏输入管理员密码，并自动生成至少
32 字符的稳定加密密钥。该密钥必须与 SQLite 数据库分开备份。

## 一键安装 Agent

先在管理后台创建节点并复制仅显示一次的 Token，然后在被监控主机执行：

```bash
sudo ./install.sh agent
```

按提示输入 Server 的 HTTPS 地址和 Token。默认实例名是 `default`；同一主机需要
多个实例时可使用 `--name`：

```bash
sudo ./install.sh agent --name secondary
```

配置保存在 `/etc/myprobe/agents/<实例名>.env`，权限为 `0600`。

## 固定版本与无人值守安装

生产环境可固定语义化版本：

```bash
sudo ./install.sh server --version v1.2.0
```

无人值守环境不要把密码或 Token 直接写进命令行参数。使用权限受限的临时密钥文件：

```bash
sudo ./install.sh server \
  --admin-password-file /run/secrets/myprobe-admin-password \
  --encryption-key-file /run/secrets/myprobe-encryption-key

sudo ./install.sh agent \
  --server-url https://status.example.com \
  --token-file /run/secrets/myprobe-agent-token
```

## 升级、状态与卸载

```bash
sudo ./install.sh update server
sudo ./install.sh update agent --name default
./install.sh status server
./install.sh status agent --name default
```

安装或升级默认保留已有配置。只有明确需要替换配置时才使用 `--force-config`。

```bash
sudo ./install.sh uninstall server
sudo ./install.sh uninstall agent --name default
```

普通卸载会保留配置，Server 数据库也会保留。只有确认不再需要恢复时才使用
`--purge`；该参数会永久删除相应配置，卸载 Server 时还会删除
`/var/lib/myprobe`。

## Docker Compose

本地从源码构建：

```bash
git clone https://github.com/zhengyifei200112-collab/myprobe.git
cd myprobe
cp .env.example .env
# 修改 .env 中的管理员密码与加密密钥
docker compose up -d --build
```

使用正式发布的 GHCR 镜像时，在 `.env` 中设置：

```dotenv
MYPROBE_IMAGE=ghcr.io/zhengyifei200112-collab/myprobe:v1.2.0
```

然后运行：

```bash
docker compose pull
docker compose up -d --no-build
```

固定版本标签便于审计和回滚；`latest` 更方便，但会随新版本变化。

## 直接使用二进制

从同一个 GitHub Release 下载目标平台二进制与 `SHA256SUMS`，先验证再安装：

```bash
sha256sum --check --ignore-missing SHA256SUMS
install -m 0755 myprobe-server-linux-amd64 /usr/local/bin/myprobe-server
```

Linux Server 和 Agent 发布 amd64/arm64；Windows 发布 Server 和 Agent；macOS 发布
Agent。非 systemd 环境的进程守护、日志轮转和开机启动由使用者配置。

## 源码构建

```bash
npm --prefix web ci
npm --prefix web run build
go test ./...
go build -o myprobe-server ./cmd/server
go build -o myprobe-agent ./cmd/agent
```

生产构建必须包含最新的 `internal/webui/dist` 前端产物。完整开发门禁见
[`AGENTS.md`](../AGENTS.md)。
