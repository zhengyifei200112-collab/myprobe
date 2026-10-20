# 前端依赖更新与验收

2026-10-20 的依赖更新开发后继基于 #119 / afc4cad。目标是消除既有 npm audit 的
5个包提示（对应4个上游公告）；这不是新发现的项目可利用性结论，也不是生产部署。
源码、数据库、Agent协议与正式运行版本没有因此切换。

| 包 | 原锁定版本 | 本次锁定版本 |
| --- | --- | --- |
| vue及同版@vue子包 | 3.5.40 | 3.5.44 |
| postcss | 8.5.20 | 8.5.31 |
| nanoid | 3.3.16 | 3.3.20 |
| source-map-js | 1.2.1 | 1.2.2 |

Vue保持同一3.5补丁系列，其他包使用现有兼容范围解析，不引入强制覆盖或跨主版本升级。
Vue编译器的兼容传递依赖也随锁文件更新。Vite8.1.5、plugin-vue6.0.8及ECharts6.1.0
保持原锁定声明；原有#41/#42/#43各自保留，没有合并/关闭或强推这些分支。

上游公开记录：[Vue补丁](https://github.com/vuejs/core/releases/tag/v3.5.44)、
[Vue SSR](https://github.com/advisories/GHSA-g2v6-rqmx-r4w6)、
[Nano ID](https://github.com/advisories/GHSA-2v37-7h3g-55p8)、
[PostCSS](https://github.com/advisories/GHSA-fxqj-rqcc-2cmp)、
[source-map-js](https://github.com/advisories/GHSA-68fv-2mgg-jv7q)。
锁文件安装和当前注册表审计已报告0项；这只说明本次已知依赖告警，不证明软件没有其他问题。

Node22干净安装、24项前端测试、类型/导航契约与构建已通过。生成的CSS与图表分包保持
原摘要，主包从index-DiF9YZzm.js变为index-DnUBQdB7.js；入口同步更新。仍有既有的
图表分包大小提示。完整Go基线、六主题/宽度浏览器及准确源码CI继续按本分支核对，
结果通过外部实施台账和准确CI留证，不提前宣称待运行检查已通过。

原生SG的0d2d739是原浏览器包，运行保持冻结；本次没有修改或重启其进程。新包影响
浏览器运行，须有自己的联合资源/性能证据，不能使用原源短测/长测为它背书。后续先
完成原轮及对账，再按实际测试资源安排新源5→50及连续24h，不在当前SG长测上并发
叠加负载。真实渠道、使用者复审和所有者合并/发布门槛均保留。
