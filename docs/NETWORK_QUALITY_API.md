# 网络质量汇总与有界时序 API（NET-02）

本阶段提供一个请求比较最多八个节点的范围汇总及有界时序。
原路径返回 `aggregation=range_summary`；后缀 `/series` 返回同范围汇总及时间点。
公开/管理/密码分享的多节点比较页面已接入，参见 [界面与验收](NETWORK_QUALITY_UI.md)。
统计及保留口径见 [网络质量](NETWORK_QUALITY.md)，一年合成台账证据见
[归档容量实验](NETWORK_ARCHIVE_CAPACITY.md)。无协议变更或新迁移。

## 路由与权限

| GET 路由 | 访问范围 |
| --- | --- |
| `/api/v1/public/network-quality` | 当前公开节点；隐藏、未知节点均拒绝整个批次 |
| `/api/v1/share/:shareID/network-quality` | 有效密码分享会话；仅分享明确列出的节点 |
| `/api/v1/admin/network-quality` | 有效管理会话；允许查询隐藏节点 |

密码分享沿用已有图表分享语义：所有者明确选入的隐藏节点可在这个私有分享中查询。
它不会进入匿名公开查询，也不授权访问分享外的节点。管理 Cookie 不代替分享 Cookie，
分享 Cookie 不代替管理会话。分享修改撤销旧会话，重新登录后仍按新范围授权。
Store 在读取统计的同一 SQLite 快照中再次确认分享启用和实际节点成员。

整个批次先校验授权，再读目标别名及结果。一项越界拒绝整个请求，错误不回显授权部分的
节点名、目标名或统计。名称、别名、历史边界和所有节点统计来自一个读事务，避免查询
中途配置/保留变化混合不同快照。所有成功和失败响应均为 `private, no-store`；GET 无写入。

## 请求参数

示例（均为合成标识）：

```text
/api/v1/public/network-quality?node_id=observer-a&node_id=observer-b&target_id=target-a&kind=ping&range=1d&timezone=Asia%2FShanghai
```

- `node_id`：重复参数，1—8 个不同 ID，每项最多 128 字符。重复、空值和控制字符拒绝。
- `target_id`：一个目标 ID；只能查询至少一个已授权请求节点曾有的保留计划，不能枚举
  全局未分配目标。目标只返回公开显示别名，不返回 host、port、冻结定义或解析地址。
- `kind`：`ping` 或 `tcping`，历史 kind 独立统计，不从当前显示偏好推算。
- `range`：默认 `1h`，支持 `1h/12h/1d/3d/7d/30d/1y`。
- 或同时提供 `start/end`：带时区偏移的 RFC3339/RFC3339Nano 绝对时刻，使用半开范围
  `[start,end)`，最长 365 天，不能倒置或请求未来。不得与 `range` 同用。
- `timezone`：默认 UTC，接受有效时区名称，拒绝含糊的 `Local`。它指定显示上下文，
  不重新解释已经带偏移的绝对时刻；跨天或夏令时不能改变实际区间。

拒绝未知参数、重复的标量参数、超过 4096 字符的 query。客户端不能提供 AsOf、隐藏
开关或管理权限。一次请求由 Server 固定相同 UTC AsOf，最多使用 20 秒读取时间。
每节点仍受 4096 个 epoch、350000 条历史记录的内部上限，超限不会返回截断结果。

## 响应与统计

顶层返回 version、aggregation、`direction=observer_to_target`、目标 id/name/kind、
requested_start/end、AsOf、显示时区及有序 nodes。每个节点包含：

- 节点 id/name、availability、AvailableFrom、实际 EffectiveStart/End 和统计粒度秒数。
  旧计划或可信结果起点以前是 unknown；触及汇总层时向内对齐，并返回实际范围。
- counts：成熟 planned、pending、执行 success/failure、missing、Agent 回报
  not_executed、以及有任务但没有可信结果的 unobserved_tasks。最后两个是 missing
  的子集；未创建任务的缺测通过差额解释，不重复加入分母。
- coverage_pct、execution_failure_pct，以及成功 RTT mean/min/max/P50/P95（毫秒）。
  探测失败不是精确 ICMP 丢包，TCP failure 也不能命名为 packet loss。
- quantile_method：`exact_raw` 或 `histogram_v1`，无成功样本为 unavailable；汇总同时
  返回分位数 lower_ms/upper_ms 和区间中点，不能把中点当成原始精确值。
- jitter_ms/jitter_pairs：同一 epoch 计划相邻且均成功的绝对 RTT 差值平均；失败、
  缺测、未执行和计划变化断开序列，不足配对时 value 为 null。
- 固定枚举的失败、未执行和未观测任务原因计数。没有任意错误文本或原始任务内容。

未追踪的历史、没有保留计划的节点分别标记 `untracked_history` / `no_plan_history`，
计数和相应指标为 null，不把未知表现为零次或 100% 健康。已知范围中真实零分母允许
计数为 0，但百分比仍为 null。客户端必须展示实际可用起点和对齐后的范围。

当前任务结果没有协商地址族或 sent/received 包计数，因此无论目标字符串长什么样，
`address_family.status=unknown`，`packet_loss.value_pct=null` 且说明未上报包计数。
不按主机名、字面地址或错误分类猜测这些证据。

响应使用独立 DTO，包括独立分位数边界投影。不会直接序列化 Store/Target/任务对象，
不包含目标地址/端口、epoch/task ID、hash、原始 payload、租约、备注、凭据或数据库路径。

## 错误与当前验证

400 表示参数不合法；401 表示管理/分享会话无效；404 表示整个授权批次或目标历史不可见；
503 表示历史损坏、超量、取消或读取超时。其余存储错误返回通用 500，不回显 SQL/原始错误。

回归验证真实 Gin 路由与 Store：八节点一次请求、混合隐藏拒绝、管理隐藏访问、真实分享
登录/越界/成员变更撤销/停用、DTO 完整字段白名单和地址/任务信息排除、原始及汇总
分位数、零/未知口径、批次损坏拒绝、读快照、参数边界和 DST/跨天绝对范围。
CI 增加对应 HTTP 合约 race 检查，完整基线与一年独立归档继续运行。

目标发现、最多八节点页面、晚高峰预设及三种宽度明暗主题已实现并验证。
后续仍需 IPv4/IPv6 的可协商证据、实际网络执行和最终多节点运行容量验证。
本 API 的实现不代表 NET-02 或完整路线已经交付。

## 同一快照的时间序列

三个范围查询路由分别增加 `/series` 后缀，参数及权限完全相同。新响应使用
`aggregation=time_series`，额外返回 `bucket_seconds`、`point_limit=2000`；节点在既有
汇总字段之外增加 points。原汇总响应保持原字段集合，不自动追加时间点。

Server 根据请求绝对跨度和保留层，选择 60、300、600、900、1800、3600、7200、10800、
21600、43200 或 86400 秒的 UTC 网格。跨节点共用同一网格，每条序列不超过 2000 点，
不会按比例切分一个汇总直方图。第一、末点使用请求实际边界裁切，不能向外扩大区间；
原始细节不具备时仍遵守实际 EffectiveStart/End。没有保留计划的节点 points 为空数组，
状态仍为 no_plan_history，不伪造整条零值曲线。

每个点只返回 time/end、实际有效起止、availability、成熟/待成熟/执行/缺测计数、
覆盖率/失败率、RTT mean/min/max/P50/P95、精度方法和分位数边界。未知历史使用
`untracked_history`；已有历史因保留粒度向内对齐而排除的片段使用 `resolution_clipped`。
这两类点的计数、百分比、延迟和有效边界为 null。已知的全缺测点允许计划数与缺测数
为正、覆盖率为 0，但 RTT 仍为 null；不能画成 0 ms。

范围汇总和点使用同一批原始/汇总记录，一次历史读取即可生成；不会为每点发起数据库
查询。成熟计划用 epoch 区间算术分别分配到点，所有已知点的计数之和等于范围汇总。
点分位数使用该点完整成功分布，不平均各桶 P95。全范围抖动保留真实计划相邻关系，
不会被图表边界切断；点响应不提供抖动，避免把仅桶内配对误当成全范围抖动。

新增回归覆盖原始/分钟/五分钟混合的独立槽计数与精确排名、跨点和桶内 epoch 断开、
未知/待成熟/粒度裁切、半开范围和 UnixNano 上界、八节点同一网格、最多 2000 点、
嵌套字段白名单以及两个路由模式的真实分享登录/撤销/禁用。独立一年容量 fixture
进一步测量时序查询耗时、点数和与整年汇总完全一致的计数。准确源 `74f36d5` 的本地
一年 fixture 通过：1461 点、574.65 ms；原生 CI 38745384552 的完整基线与独立年归档
也通过。这是单个合成节点/目标的 Store 验证，不能替代八节点 HTTP/页面容量或受限硬件验收。

范围接口实现 `fefd5f0845a04cb08924d16e689243e02cfeedc9` 的准确
[CI 38716023618](https://github.com/zhengyifei200112-collab/myprobe/actions/runs/38716023618)
已通过完整基线及独立年归档两个 job，包括 HTTP 合约 race；
[元数据](evidence/network-quality-api-ci-2026-10-15.json) 保留实际步骤。
它不代替后继时序实现的准确源检查，也不替代页面及完整运行容量验收。

## 授权目标发现

以上三个路由均增加 `/targets`，参数为重复 `node_id`（1—8 个不同节点）和可选 cursor。
每页 128 条，以 kind/ID 稳定翻页；cursor 绑定节点集合与公开/管理/分享上下文，每页仍须
重新授权，不能用 cursor 代替会话。query 最多 4096 字符，cursor 最多 512 字符。
响应只有 `targets` 的 id/name/kind、next_cursor、page_size 和 observed_at，不返回地址/端口。
当前分配及保留计划均可发现；撤销分配不隐藏仍可查询的历史别名。节点范围无一项可越界。
