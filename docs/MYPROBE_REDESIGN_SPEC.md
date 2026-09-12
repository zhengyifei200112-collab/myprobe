# MyProbe 产品升级与 Apple-inspired 重构规格

> 本文档是 MyProbe 产品升级的唯一主规格（Source of Truth）。
>
> 执行者必须先完整阅读本文档，再开始任何操作。若本文档与临时聊天指令冲突，以用户最新明确指令为准；如发现重大风险、关键信息缺失或无法安全回滚，应暂停并汇报，不得自行冒险推进。

## 文档信息

- 项目：MyProbe Server Monitor
- 正式域名目标：`https://probe.20011008.xyz`
- 推荐工作分支：`feature/myprobe-product-redesign`
- 备选工作分支：`ui/apple-redesign`
- 当前执行入口：仅执行 **Phase 1：项目审计**
- 当前阶段禁止事项：未完成审计前，不得进行大规模修改、数据库迁移、部署切换或关键配置覆盖

---

## 一、角色与任务

你将作为该项目的产品设计、前端、后端、数据库、安全和部署协作工程师，对当前已经实际部署、已有节点与历史数据的 MyProbe 项目进行渐进式升级。

本次任务不是单纯换皮，而是把 MyProbe 从简单服务器探针逐步升级为完整、自托管、现代化、可维护的服务器监控 Web 产品。

必须遵循以下工作方式：

1. 先审计真实项目，再决定实现方式。
2. 复用现有技术栈、组件、配置体系和部署方式。
3. 不假设目录、框架、数据库、接口或服务器配置。
4. 所有结论必须以当前仓库、运行环境和服务器实际情况为依据。
5. 优先渐进式重构，避免为了 UI 大规模重写后端。
6. 每个阶段均需可验证、可提交、可回滚。
7. 对任何可能导致数据丢失、节点离线或服务中断的操作，先选择安全方案并明确风险。

## 二、总体工作原则

### 2.1 先理解，再修改

在修改代码前，必须检查：

- 前后端技术栈及版本
- 项目目录结构
- 构建与启动命令
- API 与实时通信方式
- 数据库类型、表结构、迁移机制和持久化目录
- 当前认证与会话机制
- Docker、Compose、systemd、Nginx 或 Caddy 配置
- Agent 上报路径、端口和协议依赖
- 当前 Git 状态、分支、远端和未提交修改
- 已有 UI 组件、样式组织和主题实现
- 环境变量、Secret 和日志处理方式

不要仅凭文件名或界面截图推断实现。

### 2.2 保持兼容

除非需求明确要求，否则不得改变：

- Agent 协议
- 已有 API 的外部行为
- 节点 Token 语义
- 已有数据格式
- 部署用户的升级路径
- 现有节点的持续上报

新增配置必须有完整默认值。旧版本升级后应能直接运行，已有用户不需要重新配置。

### 2.3 渐进交付

严格按照本文档的 Phase 顺序推进。当前仅执行 Phase 1，完成审计报告并等待用户确认后，再进入后续阶段。

## 三、安全、Git 与备份

### 3.1 Git 保护

进入开发阶段前：

1. 检查当前 Git 状态、所在分支、远端、最近提交与未跟踪文件。
2. 保存当前稳定状态。
3. 如果存在未提交的重要修改，不得直接覆盖、丢弃或擅自合并；先分析其来源和影响并保护。
4. 在确认当前状态可运行后创建安全提交。
5. 创建独立工作分支：`feature/myprobe-product-redesign`；如项目命名规则更适合 UI 分支，可使用 `ui/apple-redesign`。
6. 不直接在主分支进行大规模开发。

### 3.2 数据库与服务器配置备份

如果即将发生 migration，必须先备份数据库并验证备份文件真实存在、非空且可用于恢复。

修改 Nginx、Caddy、Docker Compose、systemd、环境变量或其他关键部署配置前：

- 先记录当前状态。
- 创建带时间标识的备份。
- 确认恢复方法。
- 先执行语法检查或 dry run（若工具支持）。
- 不覆盖或删除未知配置。

### 3.3 分阶段提交

整个任务按照阶段提交 Git，不要等全部完成后产生一个巨型 commit。建议提交划分：

1. `Design System`
2. `Public Dashboard`
3. `Admin UI`
4. `Site & Theme Settings`
5. `Notification System`
6. `Authentication`
7. `Domain / Reverse Proxy`
8. `Final fixes & tests`

实际提交可根据仓库结构细分，但每个提交必须主题明确、可单独验证、可安全回滚。不要把无关格式化或用户已有修改混入提交。

## 四、整体设计目标

MyProbe 当前整体偏传统 SaaS Dashboard。目标是升级为简洁、高级、克制、现代、易读、信息层级清晰的服务器监控产品。

设计语言可参考：

- Apple
- macOS
- iOS
- Apple Developer
- Apple Health
- Apple Settings
- visionOS

但必须遵守：

- 不复制 Apple 官网。
- 不使用 Apple Logo。
- 不复制 Apple 源码。
- 不下载或分发 Apple 私有字体。
- 不为了“像 Apple”而牺牲服务器监控工具的信息效率。

核心理念：

- Minimal
- Clean
- Premium
- Calm
- Spacious
- Soft
- Precise
- Content-first
- Progressive Disclosure

用户第一眼应该觉得：“这是一个精致的现代服务器监控产品。”而不是“普通 Bootstrap / SaaS 后台换了个颜色。”

## 五、统一 Design System

在重构具体页面前，先建立统一 Design System。不要让每个页面维护一套独立样式。

### 5.1 Design Tokens

统一定义：

- 颜色
- 字体与字号
- 字重与行高
- 圆角
- 阴影
- 间距
- 边框
- 层级
- 动画时长与缓动
- 响应式断点

建议 CSS Variables：

```css
--background:
--surface:
--surface-secondary:
--text-primary:
--text-secondary:
--text-tertiary:
--accent:
--success:
--warning:
--danger:
--border:
--shadow:
```

根据现有技术栈补充 hover、focus、disabled、overlay、backdrop 等状态变量，不要在组件里散落 magic color。

### 5.2 基础组件

建立或统一以下可复用组件：

- Button
- Input / Select / Textarea
- Badge
- Card
- Modal / Dialog
- Dropdown / Context Menu
- Sheet / Drawer
- Toast
- Stat
- NodeCard
- Empty State
- Loading / Skeleton
- Confirm Dialog

组件应支持主题、状态、无障碍和响应式，不应绑定单一页面业务。

## 六、基础视觉规范

### 6.1 浅色模式

建议基础色：

| Token | 建议值 |
| --- | --- |
| Background | `#F5F5F7` 或 `#F7F8FA` |
| Card / Surface | `#FFFFFF` 或 `rgba(255,255,255,0.88)` |
| Primary Text | `#1D1D1F` |
| Secondary Text | `#6E6E73` |
| Tertiary Text | `#86868B` |
| Accent | `#007AFF` 或 `#0A84FF` |
| Success | `#34C759` |
| Warning | `#FF9F0A` |
| Danger | `#FF3B30` |
| Border | `rgba(0,0,0,0.06)` |

可基于对比度和现有品牌色微调，但必须保持克制、清晰和一致。

## 七、字体与排版

优先使用系统字体栈：

```css
-apple-system,
BlinkMacSystemFont,
"SF Pro Display",
"SF Pro Text",
"PingFang SC",
"Microsoft YaHei",
sans-serif
```

不要下载或分发 Apple 私有字体。数字应比标签更加突出，并为服务器指标选择适合表格数字的排版方式。

建议字号：

| 用途 | 字号 |
| --- | --- |
| 页面主标题 | `36px–48px` |
| Section 标题 | `24px–30px` |
| Card Title | `16px–20px` |
| 正文 | `14px–16px` |
| 辅助文字 | `12px–14px` |
| 关键数据 | `24px–32px` |

移动端应使用响应式字号，不得简单照搬桌面尺寸。

## 八、圆角

统一建议：

| 元素 | 圆角 |
| --- | --- |
| Badge | `8px–10px` |
| Input | `12px` |
| Button | `12px–14px` |
| Card | `18px–22px` |
| Modal | `22px–26px` |

不要将所有元素都做成胶囊，不要过度圆角。

## 九、阴影

Apple-inspired 风格应使用低对比、轻阴影和轻边框。例如：

```css
box-shadow:
  0 1px 2px rgba(0, 0, 0, 0.02),
  0 8px 30px rgba(0, 0, 0, 0.05);
```

禁止重阴影、明显黑色投影和强烈立体效果。暗色模式需要单独校准，不能机械复用浅色阴影。

## 十、页面布局

桌面主内容建议：

- `max-width: 1400px–1600px`
- 居中布局
- 左右边距：`32px–64px`
- Section Gap：`32px–56px`
- Card Padding：`20px–28px`

增加留白，不要因为屏幕很大就把所有内容铺满。超宽屏上要控制行长和信息密度。

## 十一、公开面板重新设计

### 11.1 首屏概览

公开首页第一部分：

- Eyebrow：`INFRASTRUCTURE OVERVIEW`
- 标题：`服务器运行概览`
- 副标题：`节点状态、资源占用、实时速率与网络延迟集中展示。`
- 右侧状态：`● 实时监控中`

### 11.2 核心摘要

建议使用四张摘要卡片：

1. 当前时间
2. 服务器概况
3. 累计流量
4. 实时速率

一张卡只表达一个主题。例如累计流量只突出：

```text
累计流量
↑ 39.8 GB
↓ 59.4 GB
```

不要堆叠大量标签。关键数据优先，解释文字次之。

## 十二、节点列表重新设计

节点卡片必须明显降低视觉噪声。推荐信息层级：

```text
HostDZire-SFO                     ● 在线
运行正常
已稳定运行 96 天

公网 IP       在线时长       本月流量

CPU           内存           磁盘
9%            31%            34%

网络实时速率
```

可以使用轻量 Progress Bar 和 Sparkline，但不要在每张卡中塞大型图表。卡片应优先回答：节点是否健康、当前资源负载如何、网络是否正常。

## 十三、在线与离线视觉逻辑

### 在线

- 绿色圆点。
- 非常淡的绿色氛围。
- 不要让整张卡片变成绿色。

### 离线

- 红色圆点。
- 浅红状态提示。
- 适当降低非关键数据的视觉权重，但不能降低文字可读性。
- 最重要的信息是：`节点离线`、`最后上报时间`、`离线持续时间`。

颜色不能成为唯一状态载体，应同时提供文字或图标说明。

## 十四、Hover 与动画

建议：

- Card：`translateY(-2px)`，`150–250ms`，`ease-out`
- Modal：fade + slight scale
- Dropdown：fade + translateY

禁止：

- 弹跳
- 大幅缩放
- 旋转
- 持续动画
- 花哨背景运动

尊重 `prefers-reduced-motion`。动画只服务于层级和反馈，不抢夺注意力。

## 十五、后台导航重构

一级导航建议调整为：

- 节点
- 探测目标
- 告警
- 通知
- 分享
- 设置

将原有“站点设置”“维护”“安全”逐步整合进“设置”，避免一级导航不断增长。若现有权限或路由结构不适合，应在审计报告中给出兼容迁移方案。

## 十六、节点后台

节点管理页面顶部：

- 标题：`节点管理`
- 简洁副标题
- 右侧主操作：`+ 添加节点`

不要将完整“创建节点表单”永久铺在首页。点击“+ 添加节点”后，根据当前技术栈使用 Modal、Sheet 或 Drawer。

字段至少包括：

- 名称
- 标签
- 国家 / 地区
- 上报间隔
- 创建节点

保留现有必要字段和校验逻辑，不得为了界面简化而丢失业务能力。

## 十七、后台节点卡片

节点卡片仅保留核心信息，例如：

- 节点名称：`HostDZire-SFO`
- 公开状态
- 国家 / 地区：`US`
- 探测类型：`TCPING`
- 采集间隔：`2s`
- 上报间隔：`2s`
- 延迟监测目标数量
- 创建时间
- 最后上报时间

直接操作：

- 编辑
- 查看配置
- `•••`

以下操作放入 `•••` 菜单：

- 自定义展示
- 轮换 Token
- 复制 Token
- 删除

删除必须放在菜单底部并使用 Danger Style。Token 相关操作应有清晰确认、权限控制和安全提示。

## 十八、设置中心

新增统一 Settings Center，采用类似 macOS 设置中心的信息架构，但不复制其 UI。

左侧分类：

- 站点
- 外观
- 通知
- 登录与安全
- 系统
- 维护

右侧显示对应配置。不要把几十项设置堆在一个页面。移动端应改为列表进入详情或其他适合窄屏的导航方式。

## 十九、站点设置

后台允许修改：

- 站点名称
- 站点描述
- 浏览器标题
- 公开面板标题
- 公开面板描述
- Logo URL
- Favicon URL
- Footer
- 版权文字
- GitHub 链接
- 博客链接
- 联系方式
- 其他自定义链接

这些内容不得硬编码。必须进行输入校验和安全输出处理，URL 应限制为合理协议，避免 XSS 或危险跳转。

## 二十、主题与外观

支持：

- Light
- Dark
- System

默认强调色：Apple Blue `#007AFF`。

可以提供有限预设：

- Blue
- Purple
- Green
- Orange
- Pink

不要实现无限复杂的主题编辑器。主题切换应避免闪烁，并在服务端设置加载失败时安全回退到默认主题。

## 二十一、自定义背景

支持分别设置：

- 公开面板背景
- 后台背景
- 登录页背景

管理员可填写图片 URL，并支持：

- 清除图片
- 显示方式：Cover / Contain / Original
- 背景位置：Center / Top / Bottom
- 适度 Blur
- Overlay
- Opacity

必须保证背景图片不会导致文字无法阅读。没有自定义背景时使用默认简洁 Apple-inspired 背景。对远程图片加载失败、超大图片和不安全 URL 提供合理回退。

## 二十二、设置持久化

站点设置与主题设置不能只存于 `localStorage`，必须在服务器端持久化。

优先复用现有配置系统。若必须增加数据库结构，允许通过 migration 增加设置表，例如 `site_settings`，但具体命名和表结构必须符合当前项目风格。

要求：

- 默认值完整。
- Migration 非破坏性、可回滚。
- 旧版本升级后不报错。
- 已有用户无需重新配置即可正常运行。
- 浏览器本地仅可保存非敏感偏好或作为缓存，不得成为权威数据源。

## 二十三、通知系统

新增完整通知系统。设计思想可参考 Komari，但不得直接复制其代码或 UI。

后台新增一级“通知”，或在“告警 → 通知”下建立清晰入口。告警条件、事件状态、发送渠道和发送记录应职责分离。

## 二十四、Notification Provider

设计可扩展的 Notification Provider 抽象。

第一阶段优先实现：

- Webhook
- Telegram Bot
- Discord Webhook
- Email / SMTP

未来可扩展：

- Bark
- Gotify
- ntfy
- 企业微信
- 钉钉
- 飞书

第一阶段不要一次性实现过多渠道。Provider 应有统一的配置验证、测试发送、发送结果和错误归一化接口。

## 二十五、通知渠道

支持：

- 添加
- 编辑
- 启用
- 禁用
- 删除
- 测试

每个渠道显示：

- 名称
- 类型
- 状态
- 最近测试结果

Secret 禁止明文展示，包括 Bot Token、SMTP Password、Webhook Secret。读取后台配置时必须脱敏，日志不得打印完整 Secret。

如果项目已有加密能力，应复用；若没有，应在审计报告中说明 Secret 的静态存储方案、密钥来源、轮换与备份影响，再实施。

## 二十六、Telegram

配置项：

- Bot Token
- Chat ID
- Thread / Topic ID
- Parse Mode

支持 HTML 与 Markdown，并提供“发送测试通知”。应正确处理 Telegram API 错误、超时和速率限制，日志中不得泄露 Bot Token。

## 二十七、SMTP

支持：

- SMTP Host
- Port
- Username
- Password
- Encryption
- From Name
- From Address
- STARTTLS
- TLS
- 测试邮件

校验端口、加密方式和发件地址组合；错误反馈应有帮助但不得泄露凭证。

## 二十八、Webhook

支持：

- URL
- Method
- Header
- Content-Type
- Body Template
- Authorization

敏感 Header 必须脱敏。需要防范 SSRF：限制危险协议和本地元数据地址，并根据自托管产品场景设计可配置但安全的访问策略。设置合理超时、响应体大小限制和重试策略。

## 二十九、通知事件

至少包括：

- 节点上线
- 节点离线
- 节点恢复
- CPU 超阈值
- 内存超阈值
- 磁盘超阈值
- 流量超阈值
- 延迟超阈值
- 丢包异常
- 探测异常
- 探测恢复
- 证书过期预警（仅当项目已经支持证书监控）
- 系统错误

事件命名、状态迁移和数据载荷应统一，避免 Provider 直接依赖内部数据库模型。

## 三十、通知规则

支持配置：

- 通知事件
- 通知渠道
- 阈值
- 持续时间
- 冷却时间
- 重复通知间隔
- 恢复通知

示例：

```text
CPU > 90%
持续 5 分钟
发送到 Telegram + Email
Cooldown 30 分钟
恢复后发送恢复通知
```

必须实现防抖、持续窗口、冷却和去重，避免通知轰炸。规则判断需要在进程重启后保持合理状态，具体持久化方式根据项目架构设计。

## 三十一、通知模板

管理员可自定义模板。示例：

```text
【{{site.name}} 节点告警】

节点：{{node.name}}
IP：{{node.ip}}
状态：{{event.status}}
时间：{{event.time}}
持续：{{event.duration}}
CPU：{{metric.cpu}}
内存：{{metric.memory}}
延迟：{{metric.latency}}
```

候选变量：

- `{{site.name}}`
- `{{node.name}}`
- `{{node.ip}}`
- `{{node.country}}`
- `{{event.type}}`
- `{{event.status}}`
- `{{event.time}}`
- `{{event.duration}}`
- `{{metric.cpu}}`
- `{{metric.memory}}`
- `{{metric.disk}}`
- `{{metric.latency}}`
- `{{metric.upload}}`
- `{{metric.download}}`

最终变量必须根据真实数据模型设计。模板页面应显示可用变量、示例值和缺失变量处理方式。模板引擎必须限制能力，避免执行任意代码。

## 三十二、模板管理

支持：

- 创建
- 编辑
- 复制
- 恢复默认
- 模板预览
- 测试发送

可以共享通用模板，或针对特定 Provider 单独设置。恢复默认属于覆盖操作，必须明确确认。

## 三十三、通知历史

新增通知历史，保存：

- 发送时间
- 事件类型
- 节点
- Provider
- 发送结果
- 失败原因

禁止记录完整 Token、Password 或 Secret。支持成功/失败筛选，并考虑历史记录保留周期、分页和清理策略。

## 三十四、登录系统

保留密码登录，增加 GitHub OAuth。

认证架构尽量抽象为 Auth Provider，为未来扩展以下能力留出空间：

- Google
- Microsoft
- GitLab
- OIDC

第一阶段只需完整实现 Password 与 GitHub OAuth，不要过度设计。

## 三十五、密码登录安全

密码禁止明文存储。如果已有安全密码机制则保留；如果没有，使用成熟算法：

- Argon2id，或
- bcrypt

禁止自行实现密码加密算法。需要兼容已有密码数据；若升级哈希，应采用登录时渐进迁移等安全方式，不得让现有管理员失去访问权限。

## 三十六、GitHub OAuth

登录页面结构：

```text
密码登录
────────
使用 GitHub 登录
```

OAuth Callback 建议：

```text
https://probe.20011008.xyz/auth/github/callback
```

具体路径根据实际路由设计。配置 GitHub OAuth App 时应使用最终确认的准确 Callback URL。

## 三十七、GitHub OAuth 设置

后台路径：`设置 → 登录与安全`

支持：

- 启用 GitHub 登录
- Client ID
- Client Secret
- Callback URL
- GitHub Username 白名单

Client Secret 写入后前端不得再次获取完整值。未修改 Secret 时保存其他设置不能将其清空。

## 三十八、GitHub 登录权限

不是任何拥有 GitHub 账号的人都能进入后台。

默认采用 GitHub Username Allowlist，例如：

```text
usernameA
usernameB
```

仅白名单用户可登录。比较规则应明确处理大小写和用户名变更。未来可扩展 GitHub Organization / Team，但第一阶段不要求。

## 三十九、防止管理员锁死

系统必须保证至少一种登录方式始终可用。

禁止出现：密码登录关闭 + GitHub 登录关闭，导致无人能够登录。

GitHub OAuth 配置错误时，必须仍可使用密码登录。OAuth 上线初期默认保留密码登录。任何“关闭密码登录”功能都必须在 GitHub OAuth 已通过实际验证且存在允许用户时才可开放，并提供明确警告与安全回退。

## 四十、OAuth 与会话安全

GitHub OAuth 必须：

- 使用不可预测且一次性的 `state` 防 CSRF。
- 校验 Callback 的状态、错误和授权码。
- Secret 不返回完整值给前端。
- Secret 不写入日志。
- 防止开放重定向。

生产环境的 Session / Cookie 应合理使用：

- `HttpOnly`
- `Secure`
- `SameSite`

同时检查 Session 固定攻击、过期、注销和代理后的 HTTPS 判断。

## 四十一、登录限速

如果当前项目没有，为密码登录增加基础 Rate Limit，防止简单暴力破解。

不要引入复杂到影响正常使用的机制。限速键、可信代理、失败计数、窗口、锁定提示和 IPv6 处理方式应与实际部署结构匹配。

## 四十二、正式域名

当前项目主要通过 `IP:PORT` 访问。最终主要访问地址为：

```text
https://probe.20011008.xyz
```

切换必须渐进验证，不得在域名刚配置后立即关闭原入口。

## 四十三、Cloudflare DNS

域名 `20011008.xyz` 使用 Cloudflare 管理。

目标 DNS：

| 字段 | 值 |
| --- | --- |
| Type | `A` |
| Name | `probe` |
| Content | 当前 MyProbe 公网 IPv4 |
| Proxy | 开启（橙色云） |

如果服务器存在稳定 IPv6，可评估增加 `AAAA`，但不是强制要求。

## 四十四、Cloudflare 权限原则

如果当前环境已存在安全可用、权限足够的 Cloudflare API Token，可以帮助配置 `probe.20011008.xyz`，但：

- 仅修改目标 DNS 记录。
- 不影响 `20011008.xyz` 的其他 DNS 记录。
- 不修改无关设置。
- 不在命令、输出、日志或 Git 中泄露 Token。

如果没有 Cloudflare 授权，不得声称已经配置成功。应完成可安全完成的服务器端准备，并明确告诉用户：

- 需要创建的 DNS 记录
- 应填写的公网 IP
- Proxy 是否开启
- SSL 模式如何选择

然后等待用户配置或授权。

## 四十五、反向代理

先检查服务器：

- 已有 Nginx，则继续使用 Nginx。
- 已有 Caddy，则继续使用 Caddy。
- 不要为了该项目同时安装多个反向代理。

目标架构：

```text
Internet
  ↓
Cloudflare
  ↓
probe.20011008.xyz
  ↓
Nginx / Caddy
  ↓
MyProbe
```

在修改现有站点配置前先备份，避免影响服务器上的其他域名和服务。

## 四十六、应用端口

域名反代成功后，优先考虑让 MyProbe Web 服务仅监听 `127.0.0.1`，减少公网直接暴露应用端口。

但修改前必须确认不会影响：

- Agent 上报
- API
- Docker 网络
- 内部服务通信

如果 Agent 也通过同一端口公网通信，必须先分析通信结构，禁止直接关闭导致 Agent 全部离线。必要时可继续监听公网端口，并先通过防火墙或拆分入口制定后续方案。

## 四十七、HTTPS

最终要求：

- `https://probe.20011008.xyz` 正常工作。
- HTTP 自动跳转 HTTPS。
- Cloudflare SSL 优先使用 `Full (strict)`。
- 禁止使用 `Flexible`。

Origin 证书根据实际环境选择：

- Let's Encrypt，或
- Cloudflare Origin Certificate

不得在未验证证书链、续期方式和反代配置前切换生产流量。

## 四十八、WebSocket / SSE

MyProbe 属于实时监控系统。如果使用 WebSocket、SSE 或 Long Polling，反向代理必须正确支持。

检查：

- `Upgrade`
- `Connection`
- `Host`
- `X-Real-IP`
- `X-Forwarded-For`
- `X-Forwarded-Proto`
- 合理的读取超时与缓冲设置

确保改用域名后实时数据仍正常。

## 四十九、Cloudflare 真实 IP

如果项目记录访客 IP，应使用 `CF-Connecting-IP` 或正确的 Trusted Proxy 机制。

不要无条件信任公网用户自行提供的 `X-Forwarded-For`。仅信任来自 Cloudflare 或明确受控反向代理的头部，并考虑 Cloudflare IP 段更新机制。

## 五十、原 IP 访问

不要在域名配置完成后立即封掉 `IP:PORT`。

先验证：

- 域名
- HTTPS
- Agent
- 后台
- API
- 实时监控
- 登录
- OAuth
- 通知

全部稳定后再决定：

- 保留 IP 作为应急入口，或
- 仅允许 localhost / 内网访问应用端口。

任何收紧动作都必须有回滚或应急登录方案。

## 五十一、暗色模式

支持 Light、Dark、System。

Dark 推荐：

| Token | 建议值 |
| --- | --- |
| Background | `#000000` 或 `#0B0B0C` |
| Card | `#1C1C1E` |
| Secondary Surface | `#2C2C2E` |
| Primary Text | `#F5F5F7` |
| Secondary Text | `#A1A1A6` |
| Border | `rgba(255,255,255,0.08)` |

不要简单将白色全部反转为纯黑。图表、状态色、阴影、边框、输入框和背景图遮罩都需要单独适配并保证对比度。

## 五十二、响应式

支持 Desktop、Tablet、Mobile。

- Desktop：节点卡片优先两列。
- Tablet：根据宽度为一至两列。
- Mobile：单列。

移动端不能只是缩小桌面页面，应重新调整：

- 导航
- 卡片信息层级
- 操作按钮
- 设置页
- Modal / Sheet
- 表格与长文本
- 触控目标大小

公开面板和后台都应在常见窄屏宽度下可用，不出现关键操作溢出或横向滚动。

## 五十三、图标

统一 Icon Style。优先使用当前项目已有的统一图标库；若没有，可选择 Lucide 或 Heroicons。

不要混用 Emoji、Filled、Outline 等多个完全不同风格的图标。国旗可以继续使用，但需要合适的文本回退。

## 五十四、表单

统一：

- 高度：`44px–48px`
- 圆角：`12px`
- Focus：Accent border + 轻微 Glow

建议：

```css
box-shadow: 0 0 0 3px rgba(0, 122, 255, 0.12);
```

不要使用强烈蓝色外圈。必须包含 label、错误状态、帮助文字、disabled、loading 和键盘焦点，不能只依赖 placeholder。

## 五十五、按钮

系统按钮仅保留少数语义类型：

| 类型 | 典型用途 |
| --- | --- |
| Primary | 创建、保存、确认 |
| Secondary | 编辑、查看 |
| Ghost | 取消、关闭、低优先级操作 |
| Danger | 删除、停用 |

不要在同一页面出现大量不同颜色按钮。Loading、disabled、icon-only 和确认状态必须一致。

## 五十六、Secret 管理

以下内容禁止写入 Git：

- Cloudflare API Token
- GitHub Client Secret
- Telegram Bot Token
- SMTP Password
- Webhook Secret
- Database Password

如需新增 `.env`，同时更新 `.env.example`，但 `.env.example` 只能包含变量名和示例占位符，绝不能包含真实 Secret。

同时检查：

- `.gitignore` 是否覆盖本地 Secret 文件。
- 前端构建变量是否会被打入公开产物。
- 日志、错误堆栈、测试快照和数据库导出是否包含 Secret。
- 配置读取接口是否始终脱敏。

## 五十七、数据库新增功能

UI 改造原则上不改变数据库。

站点配置、主题、通知、OAuth 等新增能力若确实需要结构变更，允许新增 migration，但必须：

- 非破坏性
- 可回滚
- 有默认值
- 兼容现有数据库
- 不删除已有表
- 不删除已有数据
- 修改前备份

可能涉及的概念表：

- `site_settings`
- `notification_channels`
- `notification_rules`
- `notification_templates`
- `notification_history`
- auth / oauth related settings

不要机械采用这些名称，必须先按照现有项目数据库风格、ORM 和 migration 机制设计。Secret 字段需要单独说明保护方式。

## 五十八、Docker 与部署

检查：

- `Dockerfile`
- `docker-compose.yml` / `compose.yml`
- Volumes
- `.env`
- Nginx / Caddy
- systemd
- 数据库文件或外部数据库连接

确保服务器重启、容器重启和版本升级不会导致：

- 站点配置丢失
- 主题丢失
- 通知配置丢失
- 用户数据丢失
- 数据库丢失

不得将数据库或上传目录错误地留在容器临时层。部署变更必须保留现有服务的启动策略和健康检查。

## 五十九、实施阶段

必须严格按照以下顺序实施，不要一次性大改。

### Phase 1：完整项目审计

只读检查并输出：

- 架构
- 部署
- 数据库
- 安全风险
- UI 问题
- 实施方案

当前请求只执行本阶段。未经用户确认，不进入 Phase 2。

### Phase 2：Git 保护与备份

- 创建安全 commit。
- 创建 feature branch。
- 数据库备份。
- 关键部署配置备份。
- 验证恢复路径。

### Phase 3：Design System

建立 Design Tokens 和基础组件：

- Card
- Button
- Input
- Badge
- Modal
- Dropdown
- Sheet
- Toast

### Phase 4：公开面板

重构公开首页、摘要卡片、节点列表和状态表现，保持实时数据与所有已有能力正常。

### Phase 5：后台 UI

重构节点卡片、后台节点管理及其他后台视觉，整合导航和常用交互。

### Phase 6：Settings Center

建立设置中心并完成：

- 站点设置
- 外观
- 背景
- 主题
- 服务端持久化

### Phase 7：Notification System

开发：

- Provider
- Channels
- Rules
- Templates
- History
- 防抖、冷却与恢复通知

### Phase 8：Authentication

完善：

- Password Authentication
- GitHub OAuth
- Allowlist
- Rate Limit
- Session / Cookie Security
- 管理员防锁死机制

### Phase 9：域名与反向代理

配置和验证：

- `probe.20011008.xyz`
- Cloudflare
- Nginx / Caddy
- HTTPS
- 反向代理
- WebSocket / SSE
- 真实 IP

### Phase 10：体验完善

完成：

- 响应式
- Dark Mode
- 细节动画
- UI 一致性
- 无障碍基础检查

### Phase 11：完整测试与收尾

- 执行完整测试。
- 修复问题。
- 验证部署与重启。
- 整理最终 Git commits。
- 输出最终交付报告。

## 六十、测试清单

至少验证以下项目：

### 访问与部署

- [ ] `https://probe.20011008.xyz` 正常访问
- [ ] HTTP 自动跳转 HTTPS
- [ ] Cloudflare Proxy 正常
- [ ] SSL Mode 为 Full (strict)
- [ ] Origin 证书有效且续期方案明确
- [ ] 原应急入口按预期保留或收紧

### 页面与主题

- [ ] 公开面板正常
- [ ] 后台正常
- [ ] 站点名称修改
- [ ] 站点描述修改
- [ ] Logo
- [ ] Favicon
- [ ] Footer 与自定义链接
- [ ] 背景图片
- [ ] Light Mode
- [ ] Dark Mode
- [ ] System Mode
- [ ] Desktop
- [ ] Tablet
- [ ] Mobile

### 登录与安全

- [ ] 密码登录
- [ ] GitHub 登录
- [ ] GitHub 白名单
- [ ] 非白名单用户被拒绝
- [ ] OAuth `state` 校验
- [ ] OAuth 失败后密码仍可登录
- [ ] 不会关闭全部登录方式
- [ ] 登录限速
- [ ] Cookie 安全属性
- [ ] 前端、接口和日志不泄露 Secret

### 节点与实时监控

- [ ] Agent 正常上报
- [ ] 节点正常在线
- [ ] 离线与恢复状态正常
- [ ] 实时数据正常
- [ ] WebSocket / SSE 正常
- [ ] CPU 数据正常
- [ ] RAM 数据正常
- [ ] Disk 数据正常
- [ ] Traffic 数据正常
- [ ] Latency 正常
- [ ] 节点创建
- [ ] 节点编辑
- [ ] 节点删除
- [ ] Token 复制与轮换
- [ ] 探测
- [ ] 告警

### 通知

- [ ] 各 Provider 配置保存
- [ ] Secret 脱敏
- [ ] 通知测试
- [ ] 真实告警通知
- [ ] 恢复通知
- [ ] 防抖与冷却
- [ ] 通知模板
- [ ] 通知变量
- [ ] 模板预览
- [ ] 通知成功记录
- [ ] 通知失败记录
- [ ] 日志不含完整凭证

### 数据与稳定性

- [ ] 数据库数据无丢失
- [ ] Migration 可从旧版本执行
- [ ] Migration 回滚或恢复方案有效
- [ ] Docker 重启正常
- [ ] 服务器重启正常
- [ ] 持久化配置不丢失
- [ ] 浏览器 Console 无明显错误
- [ ] 后端无持续报错
- [ ] 构建、lint、typecheck、tests 通过

## 六十一、禁止事项

禁止：

- 为了重构 UI 大规模重写后端。
- 改变 Agent 协议。
- 擅自清空数据库。
- 删除已有表、已有数据或未知配置。
- 覆盖现有 Nginx / Caddy 配置前不备份。
- 直接删除原 IP 访问。
- GitHub OAuth 上线后立即关闭密码登录。
- Secret 明文存储或展示。
- Secret 写入 Git。
- Webhook Secret、Token 或 Password 输出日志。
- 使用 Cloudflare Flexible SSL。
- 未获授权却声称已完成 Cloudflare 配置。
- 将所有内容做成毛玻璃。
- 大量使用 Gradient。
- 大量或持续 Animation。
- 所有卡片都使用重 Shadow。
- 为了 Apple 风隐藏关键服务器监控数据。
- 使用大量不可维护的 absolute positioning、固定宽高或 magic numbers 匹配截图。
- 绕过失败的测试、类型检查或 migration 错误继续上线。
- 擅自丢弃用户已有未提交修改。

## 六十二、设计参考图

用户可能同时提供四类图片：

1. 当前公开页面截图
2. 当前后台截图
3. Apple-inspired 公开面板参考图
4. Apple-inspired 后台节点管理参考图

已提供的 Apple-inspired 参考图表达了以下方向：

- 大面积浅灰背景与居中的宽内容区
- 清晰的标题、摘要和节点信息层级
- 白色轻边框卡片、柔和阴影和克制圆角
- 蓝色作为主要强调色，绿色/红色仅用于状态
- 公开页包含运行概览、四项摘要和双列节点卡片
- 后台包含简洁顶栏、节点管理标题、节点数量和节点卡片

图片只用于表达视觉方向，不要求机械复制。最终实现必须是真实响应式、真实组件化、真实可维护，并以现有项目数据与交互为准。

若参考图片文件已放入项目，可在审计报告中列出其实际路径；不要在产品代码中依赖聊天临时附件路径。

## 六十三、每个阶段完成后的要求

每个 Phase 完成后，先运行项目适用的：

- 编译 / 构建
- lint
- typecheck
- tests
- 浏览器 Console 检查
- Server Log 检查

然后向用户汇报：

1. 修改了什么
2. 涉及哪些文件
3. 是否影响 API
4. 是否影响数据库
5. 执行了哪些验证及结果
6. 是否存在待处理问题
7. 对下一阶段的建议

发现重大问题时先停止，不要为了继续完成任务强行绕过错误。

## 六十四、最终交付报告模板

全部完成后按以下格式提交最终报告：

```text
【访问】

正式网址：
https://probe.20011008.xyz

应用内部监听：
反向代理：
Cloudflare：
SSL Mode：
HTTPS：

------------------------------

【UI】

新增 Design System：
新增组件：
修改页面：
Dark Mode：
Responsive：

------------------------------

【站点设置】

支持：
- 站点名
- 描述
- Logo
- Favicon
- Footer
- 背景
- Theme
- Accent

保存位置：

------------------------------

【通知】

Provider：
- Webhook
- Telegram
- Discord
- SMTP

规则：
模板：
通知历史：

------------------------------

【登录】

Password：
GitHub OAuth：
Allowlist：
Callback：

------------------------------

【数据库】

新增 Table：
新增 Field：
Migration：
Backup：

------------------------------

【安全】

新增环境变量：
Secret 处理：
Rate Limit：
OAuth State：
Cookie：

------------------------------

【Git】

Branch：
Commits：
Changed Files：

------------------------------

【测试】

通过：
失败：
待处理：
```

报告中的“通过”必须基于实际执行结果，不得将未测试项目写成已通过。

## 六十五、最终产品目标

本次改造目标不是“把探针弄漂亮一点”，而是把 MyProbe 从简单服务器探针逐步升级为完整、自托管、现代化的服务器监控 Web 产品。

最终应具备：

- Apple-inspired UI
- 服务器运行概览
- 节点管理
- 探测
- 告警
- 通知渠道
- 通知规则
- 自定义通知模板
- 通知历史
- 站点自定义
- 主题与背景图片
- Light / Dark / System
- 密码登录
- GitHub OAuth
- GitHub 用户白名单
- Cloudflare 域名
- HTTPS
- 反向代理
- 设置中心
- 响应式
- 安全 Secret 管理
- 可靠数据库迁移
- Git 回滚能力

同时必须保持：

- 部署简单
- 性能稳定
- 监控信息直观
- 数据安全
- 维护方便
- 代码可维护
- 未来功能容易扩展

## 六十六、开始执行

现在不要直接进行大规模修改。

首先只完成 **Phase 1：项目审计**，并向用户报告：

1. 当前前后端技术栈
2. 当前项目目录结构
3. 当前部署架构
4. 当前数据库情况
5. 当前认证方式
6. 当前 IP / Port 与反向代理情况
7. 当前 UI 组件体系
8. 实现上述需求最合适的方案
9. 存在哪些风险及如何规避
10. 准备如何分阶段修改

Phase 1 必须以只读审计为主。完成审计报告后暂停，等待用户确认，再按照本文档的 Phase 顺序实施。

任何涉及数据删除、数据库破坏性变更、覆盖服务器关键配置、修改防火墙、关闭公网端口、切换域名流量、重启关键服务或可能导致现有服务不可用的操作，都必须优先选择安全方案，并在执行前确认目标、备份与回滚路径。

---

## 给 Codex 的首次执行指令

将本文档放入项目后，在 Codex 中发送以下短指令即可：

```text
请先完整阅读项目中的 MYPROBE_REDESIGN_SPEC.md。

该文件是本次 MyProbe 产品升级的完整需求规格和执行边界。严格按照文档中的 Phase 顺序执行。

现在只执行 Phase 1：项目审计。
先不要修改任何代码、数据库或服务器配置。

完成审计后，按文档要求向我提交审计报告并暂停，等待我确认后再进入下一阶段。
```
