# MyProbe

[English](README.md) | [简体中文](README.zh-CN.md)

MyProbe 是一套开源、自托管的 VPS/服务器监控平台。项目采用独立实现的 Go
Server 与轻量级 Go Agent，配套 Vue 3 响应式管理界面；公共仪表盘的信息架构和
视觉细节参考了 ZJM 探针，但不复制其源码、资源、品牌或实现。

项目功能范围与验收证据记录在
[`docs/PRODUCT_SPEC.md`](docs/PRODUCT_SPEC.md)，Agent 与 Server 之间的版本化协议
记录在 [`docs/PROTOCOL.md`](docs/PROTOCOL.md)。

## 主要功能

- 响应式公共仪表盘，支持桌面、平板和手机布局
- 明暗主题与系统主题跟随
- 节点在线状态、CPU、内存、Swap、磁盘、负载、进程数、温度和运行时间
- 实时上下行速率、累计流量和按月重置的计费周期流量
- 服务端推导并脱敏显示 Agent 公网 IP，避免信任客户端伪造值
- Ping/TCPing 目标、分组、节点分配、定时探测与历史趋势
- WebSocket 实时上报、断线重连、心跳及 HTTP 降级上报
- 1 小时、12 小时、1/3/7/30 天及 1 年历史图表
- 七天原始数据、30 天一分钟汇总和一年五分钟汇总
- 节点注册、编辑、排序、隐藏、删除及 Agent Token 轮换
- Telegram 与通用 Webhook 通知渠道
- 离线、CPU、带宽、周期流量和到期提醒
- 带密码和节点范围限制的只读图表分享
- 结构化徽章、外部链接及经过服务端净化的自定义 HTML
- 版本化配置导入/导出，支持预览和合并
- 使用口令加密的完整 SQLite 备份与可恢复的分阶段还原
- 登录限流、验证码、CSRF、会话撤销、可信代理白名单及管理审计日志

出于安全考虑，MyProbe 不提供远程 Shell、任意命令执行或终端代理功能。

## 技术架构

- Server：Go，发布时为包含前端资源的单一二进制文件
- Agent：Go，单一二进制文件，只主动向 Server 建立出站连接
- 前端：Vue 3、TypeScript、Vite、ECharts
- 数据库：SQLite WAL、显式版本迁移、分层历史汇总
- 通信：带身份认证的 WebSocket，HTTP 作为降级通道
- 部署：一键安装脚本、Docker Compose、systemd 或 GitHub Release 二进制

```text
cmd/server                 Server 入口
cmd/agent                  Agent 入口
internal/protocol          版本化通信协议
internal/store             SQLite Schema、迁移与数据访问
internal/agentgateway      Agent WebSocket/HTTP 接入
internal/httpapi           公共和管理 API
internal/collector         主机指标采集
internal/webui             嵌入 Server 的前端产物
web                        Vue 前端源码
docs                       产品、协议、安全、治理和发布文档
deploy                     Docker 与 systemd 部署资料
```

## 快速部署

Linux + systemd 环境推荐使用一键安装脚本。脚本会自动识别 amd64/arm64、下载最新
GitHub Release、校验 SHA-256、写入安全权限的配置并启用 systemd 服务。首次正式
Release 发布后可使用：

```bash
curl -fsSL https://raw.githubusercontent.com/zhengyifei200112-collab/myprobe/main/install.sh -o install.sh
chmod +x install.sh
sudo ./install.sh server
```

在管理后台创建节点并取得一次性 Agent Token 后，在被监控机器运行：

```bash
sudo ./install.sh agent
```

脚本会隐藏输入管理员密码或 Agent Token。重复执行安装命令可升级二进制且默认保留
配置；也支持 `update`、`status`、`uninstall` 和显式 `--purge`。执行
`./install.sh --help` 查看全部参数。生产环境仍应通过支持 WebSocket 的 HTTPS
反向代理对外提供 Server。

除一键安装外，还支持以下方式：

- Docker Compose：既可在本机从源码构建，也可使用 GHCR 预构建镜像。
- 二进制部署：从 GitHub Releases 下载并对照 `SHA256SUMS` 校验。
- 源码构建：适合开发、审计和定制。

完整步骤、安全边界、升级与卸载说明参见
[`docs/INSTALLATION.zh-CN.md`](docs/INSTALLATION.zh-CN.md)。

## 使用 Docker Compose 部署

需要 Docker Engine 和 Docker Compose v2。

```bash
git clone https://github.com/zhengyifei200112-collab/myprobe.git
cd myprobe
cp .env.example .env
```

编辑 `.env`，至少替换以下两项，禁止直接使用示例值：

```dotenv
MYPROBE_ADMIN_PASSWORD=一个足够长且唯一的管理员密码
MYPROBE_ENCRYPTION_KEY=至少32个随机字符的稳定密钥
```

然后构建并启动：

```bash
docker compose up -d --build
docker compose ps
```

正式 Release 发布后，也可以在 `.env` 中设置
`MYPROBE_IMAGE=ghcr.io/zhengyifei200112-collab/myprobe:latest`，然后运行：

```bash
docker compose pull
docker compose up -d --no-build
```

默认监听宿主机 `0.0.0.0:25775`。主机防火墙放行后，可以直接打开
`http://服务器IP:25775`。直接 HTTP 不会加密登录密码和会话，必须使用足够强且唯一的
管理员密码，不建议长期暴露在不可信网络。

需要域名和 HTTPS 时，使用支持 WebSocket Upgrade 的反向代理，并设置：

```dotenv
MYPROBE_COOKIE_SECURE=true
MYPROBE_PUBLIC_HTTP_ACKNOWLEDGED=true
MYPROBE_BIND_ADDRESS=127.0.0.1
MYPROBE_TRUSTED_PROXIES=反向代理实际连接到MyProbe时使用的IP或CIDR
```

不要为了方便填写过宽的可信代理网段；未列入 `MYPROBE_TRUSTED_PROXIES` 的来源所
提供的转发头会被忽略。SQLite 数据保存在 `myprobe-data` Volume 中，必须纳入宿主机
备份。`MYPROBE_ENCRYPTION_KEY` 应与数据库分开备份，否则通知渠道中的加密凭据无法
恢复。

一键安装脚本同样默认提供 `http://服务器IP:25775`。如果从安装时就准备使用域名反代，
请执行 `sudo ./install.sh server --reverse-proxy`。

### 使用 Docker 监控 Linux 宿主机

Agent 使用独立镜像 `ghcr.io/zhengyifei200112-collab/myprobe-agent`，不会继承 Server
的 `/healthz` 健康检查。模板通过只读挂载宿主机根目录，并使用宿主机网络与 UTS
命名空间，使 CPU、进程、网卡和磁盘指标来自宿主机，而不是 Agent 容器。

先在管理后台创建节点并复制只显示一次的 Token，然后执行：

```bash
cp deploy/agent.env.example .env.agent
chmod 600 .env.agent
# 编辑 .env.agent，设置 MYPROBE_SERVER、MYPROBE_TOKEN，并建议固定镜像版本标签
docker compose --env-file .env.agent -f compose.agent.yaml pull
docker compose --env-file .env.agent -f compose.agent.yaml up -d --no-build
docker compose --env-file .env.agent -f compose.agent.yaml ps
```

该模板仅适用于 Linux 宿主机。它保持 Agent 仅主动向 Server 建立出站连接，不开放
入站端口；宿主机根目录以只读方式挂载，容器丢弃全部能力后只加回 Ping 探测需要的
`NET_RAW`。Server 与 Agent 应固定同一个 Release 标签，便于审计、升级和回滚。

完整部署和 systemd 示例参见 [`deploy/README.md`](deploy/README.md)。

## 本地开发

开发环境要求：

- Go 1.26 或更高版本
- Node.js 22 或更高版本
- npm

安装依赖、构建前端并运行测试：

```bash
npm --prefix web ci
npm --prefix web run build
go test ./...
go vet ./...
go run ./cmd/server
```

生产 Server 会嵌入 `internal/webui/dist` 中的前端产物。开发前端时可以另开终端：

```bash
npm --prefix web run dev
```

Vite 开发服务器会把 API 与 WebSocket 请求代理到 `25775` 端口。

## 添加监控节点

1. 登录管理后台。
2. 创建节点并复制只显示一次的 Agent Token。
3. 在需要监控的服务器上安装或编译 `myprobe-agent`。
4. 使用 HTTPS Server 地址和 Token 启动 Agent：

```bash
myprobe-agent \
  --server https://status.example.com \
  --token '<agent-token>'
```

使用源码运行时：

```bash
go run ./cmd/agent \
  --server http://127.0.0.1:25775 \
  --token '<agent-token>'
```

Agent 会自动采集可用的物理磁盘和非回环网络接口，也可以通过
`--mounts`、`--interfaces`、`--collection-interval` 和 `--report-interval` 调整。
生产环境必须使用 HTTPS/WSS。

## Server 配置

Server 通过 `MYPROBE_*` 环境变量配置。常用项目如下：

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `MYPROBE_LISTEN` | `:25775` | HTTP 监听地址 |
| `MYPROBE_DATABASE` | `data/myprobe.db` | SQLite 数据库路径 |
| `MYPROBE_ADMIN_USERNAME` | `admin` | 初始管理员用户名 |
| `MYPROBE_ADMIN_PASSWORD` | 随机生成 | 首次启动管理员密码，生产环境应显式设置 |
| `MYPROBE_ENCRYPTION_KEY` | 空 | 通知渠道加密密钥，至少 32 个字符 |
| `MYPROBE_SESSION_HOURS` | `24` | 管理会话有效小时数 |
| `MYPROBE_COOKIE_SECURE` | `false` | 仅通过 HTTPS 发送 Cookie |
| `MYPROBE_TRUSTED_PROXIES` | 空 | 允许提供转发客户端 IP 的明确 IP/CIDR 列表 |
| `MYPROBE_PUBLIC_HTTP_ACKNOWLEDGED` | `false` | 确认已部署 HTTPS 代理或接受直接 HTTP 风险 |
| `MYPROBE_RAW_RETENTION_DAYS` | `7` | 原始指标保留天数 |
| `MYPROBE_MINUTE_RETENTION_DAYS` | `30` | 一分钟汇总保留天数 |
| `MYPROBE_FIVE_MINUTE_RETENTION_DAYS` | `365` | 五分钟汇总保留天数 |
| `MYPROBE_RETENTION_INTERVAL_HOURS` | `1` | 数据汇总与清理周期 |

三个历史保留周期必须满足：原始数据 ≤ 一分钟汇总 ≤ 五分钟汇总。

如果没有设置 `MYPROBE_ADMIN_PASSWORD`，Server 会在首次启动日志中输出一次随机初始
密码。部署完成后应立即修改密码，并避免把日志发送到公共位置。

## 安全与隐私

- Agent Token 只在节点创建或轮换时显示一次。
- 转发客户端 IP 默认不受信任，必须显式配置可信代理。
- 公共 API 只返回经过服务端脱敏的公网 IP。
- 登录、分享密码和验证码失败记录持久化到 SQLite，重启不会绕过限流。
- 自定义 HTML 在服务端净化，并配合严格的 Content Security Policy。
- 配置导出不包含密码、Agent Token、通知凭据、Session 或历史数据。
- 完整数据库备份使用 scrypt 与分块 AES-256-GCM 加密。
- 安全漏洞请使用仓库 Security 页面私密报告，不要创建公开 Issue。

安全设计与部署假设参见 [`docs/SECURITY.md`](docs/SECURITY.md) 和
[`.github/SECURITY.md`](.github/SECURITY.md)。

## 项目维护与贡献

项目采用 GitHub Flow、Conventional Commits、受保护的 `main`、Pull Request、自动
测试、CODEOWNERS、Dependabot、语义化版本和只向前的数据库迁移。

开始开发前请阅读：

- [`AGENTS.md`](AGENTS.md)：对维护者和编码 Agent 生效的仓库规则
- [`CONTRIBUTING.md`](CONTRIBUTING.md)：贡献流程与检查要求
- [`docs/GOVERNANCE.md`](docs/GOVERNANCE.md)：角色、分支保护和维护周期
- [`docs/RELEASING.md`](docs/RELEASING.md)：版本和发布流程

禁止直接向 `main` 推送。每项任务都应从最新 `main` 创建短期分支，完成后提交 Draft
PR，在自动检查通过并获得仓库所有者确认后 Squash Merge，随后删除来源分支。

## 许可证

[MIT](LICENSE)
