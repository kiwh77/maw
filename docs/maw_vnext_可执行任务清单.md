# maw vNext 可执行任务清单

**主分支：`main`**（已存在本地 `main` 与 `origin/main`；本波合入目标和未来 worktree 基线）。

版本：拆包稿 v1.0，2026-10-23。状态：**仅规划，未执行、未初始化 maw、未生成或 apply 蓝图、未启动 worker**。

依据：[架构 v1.1](ARCHITECTURE_VNEXT.zh-CN.md)、[综合功能清单 F01–F48](maw_integrated_feature_list.zh-CN.md)、[Board 设计规范](BOARD_DESIGN.md)及[中文镜像](zh/BOARD_DESIGN.md)。按用户指定的 [maw-task-list](/Users/wuhao/.agents/skills/maw-task-list/SKILL.md) 编制；[结构化任务草案](planning/maw-vnext-tasks.json)与本清单同源。

## 1. 基线与领取规则

- 检查时 HEAD 为 `dedc9193c8dd5481fcf4e7940c8c89149c8efaef`，但**分析基线包含当前未提交工作区**：国际化、桌面和文档已有变更。后续执行前需将认可的现状纳入可复现基线；仅从当前 HEAD 创建 worktree 会遗漏它们。本轮不提交或覆盖这些既有变更。
- 当前工程没有 `.multiagent`。未来授权执行时再由 `maw-init` 完成接入，由 `maw-plan-push` 映射蓝图并 apply；`[meta] main_branch` 必须为 `main`。这些步骤本轮均不执行。
- 一次领取一题，所有代码、测试及新增文件必须落在该卡 Surface 内；只读参考不构成修改授权。发现缺少写权限或冻结接口不够用时先修订任务边界／契约，不扩写其他模块。
- 实现卡不能改契约字段；契约题只写规范、样本和文档校验。需要修订时回到契约 owner，记录版本与受影响卡，再继续实现。
- 卡内 Verify 是**未来实现者须交付并执行的命令**。新包、测试文件和浏览器基座尚不存在；不能现在跑通，也不能把本轮文档校验算成实现验收。验收证据须记录命令、版本和结果。
- 同盘题通过依赖串行，并在各 Lane 内递增 priority／merge_order。不同 Lane 可同档；数字不是全局唯一排名。Lane 是规划簇，后续蓝图靠 deps、路径亲和与优先级承接。
- 新增文件优先落入列明目录；现有共享文件由 WIRE／FE-SHELL owner 接线。需要增改依赖 `go.mod/go.sum` 等根文件时，须先增加单独依赖接线卡；当前计划不预授权改这些文件。

## 2. 范围与交付边界

| 标记 | 含义 | 放行条件 |
|---|---|---|
| B0 | 领域／接口／UI 契约与最小支撑实现 | 相应依赖合入、单卡 Verify 通过 |
| C1 | 当前 macOS、显式任务与节点、CodeBuddy、Git 任务的受管闭环 | VN-982 主链及 VN-983 故障门槛通过 |
| O2 | 时间轴、会话、用量、提醒、保留与跨页面复盘 | 只展示有证据且口径通过的能力 |
| X3 | 其他来源、非 Git、自动选择、最终支持矩阵及文档 | 每种能力独立验收，开关默认关闭 |

这些标记是范围与门槛，**不是 Sprint 或工期承诺**；依赖图决定先后，不要求所有 B0 再统一开始 C1。完整清单覆盖 F01–F48，首闭环不依赖完整 Token／热图／多工具。

首闭环依赖闭包共 **56 题**（VN-983 及其全部前置）；这体现它是一次完整版本演进。集合：`VN-001`、`VN-002`、`VN-003`、`VN-004`、`VN-005`、`VN-006`、`VN-021`、`VN-022`、`VN-101`、`VN-102`、`VN-103`、`VN-104`、`VN-105`、`VN-106`、`VN-201`、`VN-202`、`VN-203`、`VN-204`、`VN-205`、`VN-206`、`VN-301`、`VN-302`、`VN-303`、`VN-304`、`VN-401`、`VN-402`、`VN-403`、`VN-404`、`VN-405`、`VN-501`、`VN-502`、`VN-503`、`VN-601`、`VN-602`、`VN-603`、`VN-604`、`VN-605`、`VN-701`、`VN-702`、`VN-703`、`VN-704`、`VN-721`、`VN-722`、`VN-723`、`VN-724`、`VN-801`、`VN-802`、`VN-901`、`VN-951`、`VN-952`、`VN-953`、`VN-954`、`VN-955`、`VN-981`、`VN-982`、`VN-983`。其中 VN-006 仅冻结后续计量字段边界，不要求提前实现统计；VN-702／703 先建立共享 UI，VN-704 接入四个控制页面。

首闭环的 UI、CLI 与人工兼容都应成立。CodeBuddy 命令已由用户确认可以运行；VN-301 验证的是版本化协议和能力证据，不重新把“未验证安装”当阻塞。CLI 可运行不能替代退出语义、取消、恢复、轮次与用量的独立证据。

## 3. Lane 与并行度

共 **81 题、15 条 Lane**。规划容量为 3 个 worker，未启动执行；无前置且两两不撞盘的首题为 **VN-001 ‖ VN-021 ‖ VN-301**，分别属于契约、前端规范和来源证据。后续按 ready 集合补位，不为凑并发跳过真实前置。

| Lane | 题数／ID | Surface 族与续作方式 |
|---|---|---|
| CONTRACT | 6：`VN-001`、`VN-002`、`VN-003`、`VN-004`、`VN-005`、`VN-006` | docs/contracts/vnext/{task,execution,delivery,ports,http,observability}；契约续链 |
| FE-DESIGN | 2：`VN-021`、`VN-022` | docs/contracts/vnext/{ui,ui-data}；视觉与页面挂载数据契约 |
| STORE | 7：`VN-101`、`VN-102`、`VN-103`、`VN-104`、`VN-105`、`VN-106`、`VN-107` | internal/db/**；显式串行，VN-106 另拥有 schema 版本接线 |
| ENGINE | 7：`VN-201`、`VN-202`、`VN-203`、`VN-204`、`VN-205`、`VN-206`、`VN-207` | internal/engine/**；领域、领取、派发、对账、任务用例，各自子目录 |
| ADAPTER | 8：`VN-301`、`VN-302`、`VN-303`、`VN-304`、`VN-305`、`VN-306`、`VN-307`、`VN-308` | internal/nodes/**、internal/adapters/**、docs/evidence/**；来源能力族 |
| RUNNER | 5：`VN-401`、`VN-402`、`VN-403`、`VN-404`、`VN-405` | internal/runner/**；journal → process → session → recovery |
| DELIVERY | 5：`VN-501`、`VN-502`、`VN-503`、`VN-504`、`VN-505` | internal/workspace/**、internal/delivery/**、internal/verification/**；工作空间与交付链 |
| HTTP | 6：`VN-601`、`VN-602`、`VN-603`、`VN-604`、`VN-605`、`VN-606` | internal/localauth/**、internal/httpapi/**；不同资源子目录，根路由留给 WIRE |
| FE-SHELL | 5：`VN-701`、`VN-702`、`VN-703`、`VN-704`、`VN-705` | 内嵌壳、全局 styles/components/api/bootstrap/shell/i18n；递增串行 |
| FE-PAGES-CONTROL | 5：`VN-721`、`VN-722`、`VN-723`、`VN-724`、`VN-725` | assets/views/{nodes,tasks,runs,deliveries,projects,settings}；控制页续链 |
| FE-PAGES-OBS | 4：`VN-741`、`VN-742`、`VN-743`、`VN-744` | assets/views/{timeline,activity,sessions,analytics}；回顾页续链 |
| QUERY | 7：`VN-801`、`VN-802`、`VN-803`、`VN-804`、`VN-805`、`VN-806`、`VN-807` | internal/{query,events,projection,observability,analytics,notifications,retention}/**；事实到读模型 |
| CLIENTS | 3：`VN-901`、`VN-902`、`VN-903` | 仓库协议／技能、桌面壳与使用手册；集成客户端族，具体 Surface 分开 |
| WIRE | 6：`VN-951`、`VN-952`、`VN-953`、`VN-954`、`VN-955`、`VN-956` | internal/app 共享命令与 cmd/maw/main.go；951 → 952 → 953 → 954 → 955 → 956 |
| QUALITY | 5：`VN-981`、`VN-982`、`VN-983`、`VN-984`、`VN-985` | tests/browser、tests/managed、tests/resilience、tests/platform 与 CI；门槛续作 |

### 3.1 写盘图与共享热区 owner

```text
docs/contracts/task ‖ docs/contracts/ui ‖ docs/evidence/codebuddy  （首题）
internal/db                 STORE 串行
internal/engine 子模块  ‖  adapters  ‖  runner  ‖  delivery  （端口／库到位后）
httpapi 子模块          ‖  query 子模块  ‖  前端独立视图
internal/app + cmd          WIRE 951 → 956 收口
Board 壳／样式／菜单        FE-SHELL 701 → 705 独占
```

| 共享热区 | 顺序／owner | 其他卡的限制 |
|---|---|---|
| internal/db/** | VN-101 → 107 | 业务实现通过端口调用，不追加表或 SQL |
| worker_cmd.go | VN-952 Next/Register/Progress → VN-953 Submit/Review | 同文件不允许并行改不同函数 |
| project.go／helpers.go／plan_cmd.go | VN-951 | 其他用例实现放新目录，接线由 owner 完成 |
| main.go／runtime.go／config／Go i18n | VN-954；扩展组装 VN-956 依赖 VN-955 | 不从各业务卡随手注册命令或增全局语言键 |
| board.go + serve.go 资源拆分 | VN-701；serve.go 后由 VN-955 接线 | VN-955 显式依赖 VN-701 |
| 页面根文件／全局样式／公共组件／请求层 | VN-701 → 702 → 703 → 704 → 705 | FE-PAGES 禁止改菜单、根布局、路由与全局 token |
| CodeBuddy adapter 与证据 | VN-301 → 303／304 → 308 | 扩展统计卡不改来源解析 |
| 浏览器包、锁文件、基座 | VN-981；全页审计 VN-984 后续 | 页面局部测试跟随自己的视图目录；全页审计不越界修视图 |
| README／安全与运行说明 | VN-903 | 实现卡通过局部交付证据回传，不并发改根文档 |

## 4. 前端一致性门槛 UI-C

现有 Workbench 是视觉依据，截图只提供功能启发。新增页面沿用紧凑工作台结构；不引入蓝紫渐变主题、第二套导航或生产前端框架。

| 范围 | 必须遵守的规则 | 固定 owner／验收 |
|---|---|---|
| 主题 token | 延续 --bg/panel/card/line/text/muted/accent/warn/danger；背景深 #141618／浅 #f7f8f9，信号绿 #9cdbb0／#267044；圆角6px；语义状态同时有文字 | VN-021 冻结，VN-702 实现；页面使用 token，不复制全局色值 |
| 排版密度 | 标题22px、指标18px、任务标题12px、正文13px、辅助10–11px；桌面边距28px、小屏16px、任务间距8px；系统字体与等宽数字／路径 | 共用 CSS 与组件，新增图表继承；窄屏允许表格／时间轴局部滚动 |
| 页面与组件 | 统一筛选条、紧凑指标、状态标签、列表／表格、抽屉、确认动作、错误／空／加载／过期／unknown；保留现有 Board／拓扑 | VN-702 公共组件，721–744 仅自身 views，704／705 挂载 |
| 数据上下文 | 同一工程、来源、节点、状态、日期与时区；切页／下钻／返回保留上下文；拒收过期响应；刷新保留滚动和焦点 | VN-022 冻结 ViewContext，VN-703 单一请求客户端 |
| 语言与能力 | 每个视图有同结构 zh-CN／en 字典，经壳注册；未知、不支持、缺测、估算、已清理和零值明确区分 | 页面 owner 提供局部字典，VN-704／705 注册；无能力不显示可执行承诺 |
| 键盘与动效 | Enter／Space 打开详情，Escape 关闭，抽屉焦点约束／归还；操作有辅助名称；尊重 reduced-motion | 公共组件与每页用例验证，不以颜色作唯一信息 |
| 浏览器／桌面／静态导出 | 浏览器与 Tauri 共用业务视图；静态导出可加载同套资源且只读；桌面不复制状态机 | VN-701／704／705、VN-902；服务端根路由由 VN-955／956 接线 |
| 视觉回归 | 桌面及窄屏 × 深浅主题 × 中英文；补测系统主题切换、长中文、关键异常态；截图前断言真实资源和数据已加载 | VN-981 建基座，每页卡有自己的 grep 场景，VN-984 全页复核；基线变化需逐项解释 |

页面卡共同验收：不修改冻结字段；只能调用公共 API／上下文；写操作有请求中、防重复、冲突与失败反馈；未知资源有证据缺口和下一步。实现开始前先交 VN-021 的现有页＋节点／运行页状态样本，样本沿用既有设计，不构成另开设计系统。

## 5. 任务索引

| ID | Lane | 标记 | 标题 | 直接前置 |
|---|---|---|---|---|
| [VN-001](#vn-001) | CONTRACT | B0 | 冻结任务实例与版本契约 | 无 |
| [VN-002](#vn-002) | CONTRACT | B0 | 冻结运行状态与资源契约 | `VN-001` |
| [VN-003](#vn-003) | CONTRACT | B0 | 冻结交付与验收契约 | `VN-002` |
| [VN-004](#vn-004) | CONTRACT | B0 | 冻结应用端口与接入器契约 | `VN-003` |
| [VN-005](#vn-005) | CONTRACT | B0 | 冻结本机接口与授权契约 | `VN-004` |
| [VN-006](#vn-006) | CONTRACT | B0 | 冻结时间轴与计量契约 | `VN-005` |
| [VN-021](#vn-021) | FE-DESIGN | B0 | 冻结 Workbench 视觉与交互规范 | 无 |
| [VN-022](#vn-022) | FE-DESIGN | B0 | 冻结页面上下文与挂载契约 | `VN-021`、`VN-005`、`VN-006` |
| [VN-101](#vn-101) | STORE | B0 | 建立显式迁移与读写 Store | `VN-002`、`VN-004`、`VN-201` |
| [VN-102](#vn-102) | STORE | B0 | 存储任务实例与冻结版本 | `VN-101` |
| [VN-103](#vn-103) | STORE | B0 | 存储节点槽与工作目录占用 | `VN-102` |
| [VN-104](#vn-104) | STORE | B0 | 存储运行操作与原子事件 | `VN-103` |
| [VN-105](#vn-105) | STORE | B0 | 存储交付清单与验证证据 | `VN-104` |
| [VN-106](#vn-106) | STORE | B0 | 验证升级备份与旧写者边界 | `VN-105`、`VN-005` |
| [VN-107](#vn-107) | STORE | O2 | 存储来源用量与投影元数据 | `VN-106`、`VN-006` |
| [VN-201](#vn-201) | ENGINE | B0 | 实现领域值对象与应用端口 | `VN-004` |
| [VN-202](#vn-202) | ENGINE | B0 | 实现运行与交付状态规则 | `VN-201` |
| [VN-203](#vn-203) | ENGINE | B0 | 实现统一领取与资源绑定 | `VN-202`、`VN-104`、`VN-302`、`VN-501` |
| [VN-204](#vn-204) | ENGINE | B0 | 实现持久派发协调循环 | `VN-203`、`VN-304`、`VN-404` |
| [VN-205](#vn-205) | ENGINE | B0 | 实现取消重试与状态核实 | `VN-202`、`VN-104`、`VN-403` |
| [VN-206](#vn-206) | ENGINE | B0 | 实现工程与任务版本用例 | `VN-201`、`VN-102` |
| [VN-207](#vn-207) | ENGINE | X3 | 实现按能力自动匹配节点 | `VN-203`、`VN-204`、`VN-983` |
| [VN-301](#vn-301) | ADAPTER | B0 | 采集 CodeBuddy 版本化协议证据 | 无 |
| [VN-302](#vn-302) | ADAPTER | B0 | 实现节点登记与就绪模型 | `VN-201`、`VN-103`、`VN-301` |
| [VN-303](#vn-303) | ADAPTER | B0 | 实现 CodeBuddy 探测与启动规格 | `VN-302`、`VN-201`、`VN-004` |
| [VN-304](#vn-304) | ADAPTER | B0 | 实现 CodeBuddy 事件与结束判定 | `VN-303` |
| [VN-305](#vn-305) | ADAPTER | X3 | 接入 WorkBuddy 两类节点 | `VN-304`、`VN-983` |
| [VN-306](#vn-306) | ADAPTER | X3 | 接入 Trae 的能力分级入口 | `VN-305` |
| [VN-307](#vn-307) | ADAPTER | X3 | 接入 ZCode 的显式配置入口 | `VN-306` |
| [VN-308](#vn-308) | ADAPTER | O2 | 解析已验证轮次用量与恢复能力 | `VN-304`、`VN-804`、`VN-983` |
| [VN-401](#vn-401) | RUNNER | B0 | 实现运行日志与补交游标 | `VN-201`、`VN-002`、`VN-004` |
| [VN-402](#vn-402) | RUNNER | B0 | 实现 macOS 受管进程启动身份 | `VN-401`、`VN-201` |
| [VN-403](#vn-403) | RUNNER | B0 | 实现停止证据与取消竞争 | `VN-402` |
| [VN-404](#vn-404) | RUNNER | B0 | 实现单次运行监督循环 | `VN-403`、`VN-104`、`VN-304`、`VN-205` |
| [VN-405](#vn-405) | RUNNER | B0 | 实现运行恢复与现场核实 | `VN-404`、`VN-205` |
| [VN-501](#vn-501) | DELIVERY | B0 | 实现受占用保护的 Git 工作空间 | `VN-201`、`VN-103` |
| [VN-502](#vn-502) | DELIVERY | B0 | 实现交付收取与结果冻结 | `VN-501`、`VN-105`、`VN-304` |
| [VN-503](#vn-503) | DELIVERY | B0 | 实现版本绑定的代码验收 | `VN-502`、`VN-202`、`VN-403` |
| [VN-504](#vn-504) | DELIVERY | X3 | 实现独立非 Git 工作目录 | `VN-501`、`VN-983` |
| [VN-505](#vn-505) | DELIVERY | X3 | 实现非代码产物验收策略 | `VN-503`、`VN-504` |
| [VN-601](#vn-601) | HTTP | B0 | 实现本机令牌与浏览器会话 | `VN-005`、`VN-201` |
| [VN-602](#vn-602) | HTTP | B0 | 实现工程节点运行查询接口 | `VN-601`、`VN-801` |
| [VN-603](#vn-603) | HTTP | B0 | 实现工程任务节点配置接口 | `VN-601`、`VN-206`、`VN-302` |
| [VN-604](#vn-604) | HTTP | B0 | 实现派发控制与交付操作接口 | `VN-601`、`VN-204`、`VN-205`、`VN-502`、`VN-503` |
| [VN-605](#vn-605) | HTTP | B0 | 实现事件补拉与 SSE 兼容传输 | `VN-601`、`VN-801`、`VN-802` |
| [VN-606](#vn-606) | HTTP | O2 | 实现观测与提醒查询接口 | `VN-601`、`VN-803`、`VN-804`、`VN-805`、`VN-806` |
| [VN-701](#vn-701) | FE-SHELL | B0 | 保行为拆分内嵌页面资源 | `VN-021`、`VN-022` |
| [VN-702](#vn-702) | FE-SHELL | B0 | 建立共享主题组件与状态样式 | `VN-701`、`VN-981` |
| [VN-703](#vn-703) | FE-SHELL | B0 | 实现公共请求与页面上下文 | `VN-702`、`VN-005`、`VN-022` |
| [VN-704](#vn-704) | FE-SHELL | C1 | 挂载首个受管闭环页面 | `VN-703`、`VN-721`、`VN-722`、`VN-723`、`VN-724`、`VN-981` |
| [VN-705](#vn-705) | FE-SHELL | O2 | 挂载回顾统计与设置入口 | `VN-704`、`VN-725`、`VN-741`、`VN-742`、`VN-743`、`VN-744` |
| [VN-721](#vn-721) | FE-PAGES-CONTROL | C1 | 实现节点列表与能力详情页 | `VN-703`、`VN-022`、`VN-981` |
| [VN-722](#vn-722) | FE-PAGES-CONTROL | C1 | 实现任务创建与指定派发视图 | `VN-721` |
| [VN-723](#vn-723) | FE-PAGES-CONTROL | C1 | 实现运行详情与待核实面板 | `VN-722` |
| [VN-724](#vn-724) | FE-PAGES-CONTROL | C1 | 实现交付与验收详情页 | `VN-723` |
| [VN-725](#vn-725) | FE-PAGES-CONTROL | X3 | 实现工程策略与保留设置页 | `VN-724`、`VN-956` |
| [VN-741](#vn-741) | FE-PAGES-OBS | O2 | 实现分层执行时间轴视图 | `VN-703`、`VN-803`、`VN-022`、`VN-981` |
| [VN-742](#vn-742) | FE-PAGES-OBS | O2 | 实现全天活动与小时下钻 | `VN-741`、`VN-805` |
| [VN-743](#vn-743) | FE-PAGES-OBS | O2 | 实现会话轮次与来源明细 | `VN-742`、`VN-804`、`VN-308` |
| [VN-744](#vn-744) | FE-PAGES-OBS | O2 | 实现交付与用量统计视图 | `VN-743`、`VN-805` |
| [VN-801](#vn-801) | QUERY | B0 | 实现权威资源查询与历史分页 | `VN-201`、`VN-104`、`VN-105`、`VN-006` |
| [VN-802](#vn-802) | QUERY | B0 | 实现标准观测摄取与去重 | `VN-801` |
| [VN-803](#vn-803) | QUERY | O2 | 实现阶段与执行区间投影 | `VN-802`、`VN-107`、`VN-983` |
| [VN-804](#vn-804) | QUERY | O2 | 实现会话归属与用量计量 | `VN-802`、`VN-107`、`VN-006` |
| [VN-805](#vn-805) | QUERY | O2 | 实现工程与来源统计查询 | `VN-803`、`VN-804`、`VN-503` |
| [VN-806](#vn-806) | QUERY | O2 | 实现可恢复的介入提醒 | `VN-802`、`VN-107`、`VN-983` |
| [VN-807](#vn-807) | QUERY | O2 | 实现按占用与证据约束的数据保留 | `VN-107`、`VN-502`、`VN-401`、`VN-983` |
| [VN-901](#vn-901) | CLIENTS | C1 | 升级人工领取与旁路上报协议 | `VN-952`、`VN-954` |
| [VN-902](#vn-902) | CLIENTS | O2 | 接通桌面提醒与受限页面桥接 | `VN-806`、`VN-704`、`VN-955` |
| [VN-903](#vn-903) | CLIENTS | X3 | 更新操作指南与迁移发布说明 | `VN-985` |
| [VN-951](#vn-951) | WIRE | B0 | 接通增量存储与任务身份入口 | `VN-106`、`VN-206` |
| [VN-952](#vn-952) | WIRE | B0 | 统一人工领取与旧心跳占用语义 | `VN-951`、`VN-203`、`VN-205`、`VN-302`、`VN-802` |
| [VN-953](#vn-953) | WIRE | B0 | 统一旧提交评审与目录清理入口 | `VN-952`、`VN-502`、`VN-503` |
| [VN-954](#vn-954) | WIRE | C1 | 组装运行服务与受管命令入口 | `VN-953`、`VN-204`、`VN-405`、`VN-601` |
| [VN-955](#vn-955) | WIRE | C1 | 接通 HTTP 与旧看板兼容入口 | `VN-954`、`VN-602`、`VN-603`、`VN-604`、`VN-605`、`VN-701`、`VN-901` |
| [VN-956](#vn-956) | WIRE | X3 | 接通扩展来源与观测能力开关 | `VN-955`、`VN-207`、`VN-504`、`VN-505`、`VN-305`、`VN-306`、`VN-307`、`VN-308`、`VN-606`、`VN-807` |
| [VN-981](#vn-981) | QUALITY | B0 | 建立真实页面浏览器验收基座 | `VN-021`、`VN-022` |
| [VN-982](#vn-982) | QUALITY | C1 | 验收首个 CodeBuddy 受管完整闭环 | `VN-955`、`VN-704`、`VN-405`、`VN-106` |
| [VN-983](#vn-983) | QUALITY | C1 | 验收迁移与故障恢复硬门槛 | `VN-982` |
| [VN-984](#vn-984) | QUALITY | O2 | 验收所有页面的视觉与交互一致性 | `VN-705`、`VN-902` |
| [VN-985](#vn-985) | QUALITY | X3 | 固化集成检查与平台支持矩阵 | `VN-983`、`VN-984`、`VN-956`、`VN-902` |

## 6. 可领取任务卡

以下全部为待实施卡。Contract 引用尚待前置任务交付；Read scope 仅允许读取。每卡的验收与 Verify 都需通过后才可提交。UI-C 自动适用于全部前端卡与桌面视觉相关卡。

<a id="vn-001"></a>

### VN-001 冻结任务实例与版本契约

- **Goal**：明确当前任务、稳定实例、定义版本及退役别名的唯一关系。
- **Non-goals**：不迁移真实状态库，不修改应用和现有蓝图。
- **Depends**：无
- **Surface / write_scope**：`docs/contracts/vnext/task/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`internal/db/db.go`、`internal/app/worker_cmd.go`、`internal/app/plan_cmd.go`；现有实现只读，不扩大 Surface。
- **Contract**：本卡产出或现有架构／设计基线；文档／证据按本卡验收冻结。
- **Lane / 范围**：`CONTRACT` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F44`、`F45`；`G02`
- **Acceptance**：
  - [ ] 定义旧显示编号、instance_id、版本、归档与依赖解析，明确旧主键保留。
  - [ ] 正反例覆盖编号重建、活跃定义更新和歧义历史，规范样本可被标准库校验。
- **Steps**：
  1. 对照现有 tasks、plan apply 和架构 v1.1，写不可变字段及状态权威。
  2. 建立 JSON 规范样本、反例和仅依赖标准库的文档校验。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/task -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-002"></a>

### VN-002 冻结运行状态与资源契约

- **Goal**：固定 Run、Operation、占用、心跳与标准事件之间的一致性规则。
- **Non-goals**：不增加数据库表，不调用厂商 CLI。
- **Depends**：`VN-001`
- **Surface / write_scope**：`docs/contracts/vnext/execution/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`internal/db/db.go`、`internal/app/worker_cmd.go`、`internal/app/plan_cmd.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`；`docs/contracts/vnext/task/`。
- **Lane / 范围**：`CONTRACT` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F32`、`F44`、`F45`、`F46`、`F47`；`G01`、`G03`、`G06`
- **Acceptance**：
  - [ ] 覆盖 unknown、取消竞争、代际、节点槽和目录分别释放，并给出非法转移反例。
  - [ ] 定义接收时间／发生时间、事件身份和同事务规则，状态样本不依赖厂商字段。
- **Steps**：
  1. 冻结三类状态、操作完成语义与自动执行默认关闭配置。
  2. 用冲突、迟到结束与启动不确定案例校验不变量。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/execution -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-003"></a>

### VN-003 冻结交付与验收契约

- **Goal**：规定产物候选、冻结结果和任务完成之间的证据关系。
- **Non-goals**：不改 submit／merge，不运行项目验证命令。
- **Depends**：`VN-002`
- **Surface / write_scope**：`docs/contracts/vnext/delivery/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`internal/db/db.go`、`internal/app/worker_cmd.go`、`internal/app/plan_cmd.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`。
- **Lane / 范围**：`CONTRACT` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F07`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`；`G04`
- **Acceptance**：
  - [ ] Manifest 包含任务版本、Run、基线／提交、产物引用与验证结论，定义代码和非代码完成条件。
  - [ ] 反例证明退出码 0、缺失产物或旧提交验证不能产生 done。
- **Steps**：
  1. 固定 Manifest、验证证据与评审合入策略的字段和版本。
  2. 给出重复交付、产物被修改及人工验收的规范样本。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/delivery -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-004"></a>

### VN-004 冻结应用端口与接入器契约

- **Goal**：为领取、存储、进程监督和来源适配定义唯一的应用边界。
- **Non-goals**：不实现端口，不引入微服务或外部消息队列。
- **Depends**：`VN-003`
- **Surface / write_scope**：`docs/contracts/vnext/ports/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`internal/db/db.go`、`internal/app/worker_cmd.go`、`internal/app/plan_cmd.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`。
- **Lane / 范围**：`CONTRACT` / `B0`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F44`；无
- **Acceptance**：
  - [ ] 定义 ClaimExecution、Store 事务、Probe／Launch／Decode／Interpret 与 ProcessController 的输入、结果和错误。
  - [ ] 明确依赖方向及各实现目录，不允许接入器直接写业务任务状态。
- **Steps**：
  1. 以已有 schedule、gitops 和 db 调用路径冻结接口与所有者。
  2. 列出幂等、资源冲突、协议未知和平台不支持的交互样本。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/ports -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-005"></a>

### VN-005 冻结本机接口与授权契约

- **Goal**：定义新旧 HTTP 入口、异步操作和本机授权的统一语义。
- **Non-goals**：不启动 HTTP 服务，不启用任何写功能。
- **Depends**：`VN-004`
- **Surface / write_scope**：`docs/contracts/vnext/http/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/db/db.go`、`internal/app/worker_cmd.go`、`internal/app/plan_cmd.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`CONTRACT` / `B0`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F30`、`F31`、`F32`、`F33`、`F44`、`F47`；`G05`
- **Acceptance**：
  - [ ] 明确 /api/v1 资源、分页、202 操作查询、幂等键、revision、401／409／422 及旧入口适配。
  - [ ] 同源会话、CSRF、CLI 令牌与旧状态归属验证有正反例，明确同用户进程不属于强隔离承诺。
- **Steps**：
  1. 冻结请求／响应示例和 GET 无业务副作用约束。
  2. 覆盖旧匿名状态写入、跨站请求及同键不同请求的拒绝案例。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/http -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-006"></a>

### VN-006 冻结时间轴与计量契约

- **Goal**：明确事件查询、区间投影和已知用量的唯一统计口径。
- **Non-goals**：不实现图表或投影，不承诺未验证的厂商能力。
- **Depends**：`VN-005`
- **Surface / write_scope**：`docs/contracts/vnext/observability/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/db/db.go`、`internal/app/worker_cmd.go`、`internal/app/plan_cmd.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-005`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`CONTRACT` / `B0`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`；`G08`
- **Acceptance**：
  - [ ] 定义 task_phase、cli_process、turn、explicit_wait、游标水位与完整性字段。
  - [ ] 样本覆盖跨日、乱序、累计 usage 重置、共享 Turn 与未知 Token，禁止重复计量。
- **Steps**：
  1. 固定半开时间窗口、来源命名空间、计量 scope 与 nullable 字段。
  2. 发布可供后端与前端共用的数值样本和错误口径反例。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/observability -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-021"></a>

### VN-021 冻结 Workbench 视觉与交互规范

- **Goal**：把现有 Board 设计延续为新增页面必须共同遵守的规范。
- **Non-goals**：不改页面代码，不重做品牌，不直接套用截图配色。
- **Depends**：无
- **Surface / write_scope**：`docs/contracts/vnext/ui/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/page.html`、`internal/board/page.test.cjs`；现有实现只读，不扩大 Surface。
- **Contract**：本卡产出或现有架构／设计基线；文档／证据按本卡验收冻结。
- **Lane / 范围**：`FE-DESIGN` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F30`、`F31`、`F32`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F46`、`F47`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 冻结石墨灰／纸白／信号绿、6px 圆角、22/18/12/13px 字级及 28/16px 边距；不新设页面皮肤。
  - [ ] 统一状态词汇、状态与来源颜色职责、表格／表单／空态／抽屉、焦点、减弱动画、中英与窄屏规则。
- **Steps**：
  1. 对照两份 Board 设计文档和当前 page.html 提取 token、组件及状态基线。
  2. 编制视觉场景矩阵、DOM 测试定位约定与样例，校验 token 和场景覆盖。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/ui -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-022"></a>

### VN-022 冻结页面上下文与挂载契约

- **Goal**：统一页面模块使用数据、语言、筛选和操作状态的方式。
- **Non-goals**：不创建新框架工程，不挂载业务页面。
- **Depends**：`VN-021`、`VN-005`、`VN-006`
- **Surface / write_scope**：`docs/contracts/vnext/ui-data/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/page.html`、`internal/board/page.test.cjs`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`。
- **Lane / 范围**：`FE-DESIGN` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F30`、`F31`、`F32`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`、`F47`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 定义 ViewContext、公共请求客户端、局部双语字典、视图注册及 URL／旧锚点兼容。
  - [ ] 明确 FE-PAGES 不写 shell、路由、全局样式和公共请求层，静态导出保持只读可加载。
- **Steps**：
  1. 为各视图规定数据边界、操作 pending／unknown 交互及错误文案映射。
  2. 冻结 HTTP 与静态快照的资源加载策略、浏览器场景与测试脚本名称。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/contracts/vnext/ui-data -p 'test_*.py'
  ```

- **Deliverables**：冻结的规范、正反样本及标准库文档校验证据；不含业务实现。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-101"></a>

### VN-101 建立显式迁移与读写 Store

- **Goal**：将 schema 初始化和读写事务生命周期从普通查询中分离。
- **Non-goals**：不创建新运行表，不操作用户真实数据库。
- **Depends**：`VN-002`、`VN-004`、`VN-201`
- **Surface / write_scope**：`internal/db/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`STORE` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F32`、`F44`、`F45`；`G03`
- **Acceptance**：
  - [ ] 查询不执行迁移或 BEGIN IMMEDIATE，schema 版本由显式迁移序列维护。
  - [ ] 旧 CRUD 与忙锁测试仍成立，写重试有界且不包含文件／进程副作用。
- **Steps**：
  1. 拆出连接／迁移生命周期，保持已有数据与接口兼容。
  2. 增加只读访问、失败回滚和写锁耗尽用例。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1
  ```

- **Deliverables**：建立显式迁移与读写 Store的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-102"></a>

### VN-102 存储任务实例与冻结版本

- **Goal**：在保留当前复合主键的前提下提供稳定实例与退役归档。
- **Non-goals**：不重建 tasks 物理主键，不修改 plan apply 调用点。
- **Depends**：`VN-101`
- **Surface / write_scope**：`internal/db/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`STORE` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F44`、`F45`；`G02`
- **Acceptance**：
  - [ ] 当前任务有唯一非空实例引用，版本不可变，退役归档与别名释放同事务。
  - [ ] 编号重建不串历史；重复创建且证据不足的旧事件保持 ambiguous。
- **Steps**：
  1. 增加实例、版本、实例依赖的迁移与仓储。
  2. 用历史重复编号和活跃任务定义更新验证迁移及引用。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1
  ```

- **Deliverables**：存储任务实例与冻结版本的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-103"></a>

### VN-103 存储节点槽与工作目录占用

- **Goal**：提供节点、执行槽、工作空间及唯一资源占用的持久化接口。
- **Non-goals**：不做节点探测或真实 worktree 准备。
- **Depends**：`VN-102`
- **Surface / write_scope**：`internal/db/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`STORE` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F11`、`F15`、`F22`、`F23`、`F24`、`F25`；`G01`、`G03`
- **Acceptance**：
  - [ ] 节点槽和规范化目录有唯一有效持有者，旧代际不能释放新占用。
  - [ ] 空闲计算槽与待验收目录可分别保留，平台账号额度不参与自动推断。
- **Steps**：
  1. 按契约增加节点与工作目录仓储及占用条件更新。
  2. 并发争抢、路径别名和错误释放用例检查实际数据库约束。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1
  ```

- **Deliverables**：存储节点槽与工作目录占用的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-104"></a>

### VN-104 存储运行操作与原子事件

- **Goal**：让领取占用、Run、Operation 和业务事件能够原子提交。
- **Non-goals**：不启动 CLI，不以事务重试重发外部任务。
- **Depends**：`VN-103`
- **Surface / write_scope**：`internal/db/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`STORE` / `B0`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F32`、`F44`、`F45`；`G01`、`G03`
- **Acceptance**：
  - [ ] 请求幂等键绑定摘要，状态／占用／事件任一失败时整笔回滚。
  - [ ] Run 风险占用、协调者操作租约、游标分页及补交去重可被真实并发连接验证。
- **Steps**：
  1. 增加运行、操作、语义事件身份与事务端口实现。
  2. 注入事件写入失败、重复领取、超页积压及过期代际写入。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1
  ```

- **Deliverables**：存储运行操作与原子事件的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-105"></a>

### VN-105 存储交付清单与验证证据

- **Goal**：保存绑定任务版本和产物身份的交付及验证结果。
- **Non-goals**：不执行验证命令，不改合入逻辑。
- **Depends**：`VN-104`
- **Surface / write_scope**：`internal/db/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-003`、`VN-004`；`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`STORE` / `B0`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F26`、`F27`、`F28`、`F29`、`F44`；`G04`
- **Acceptance**：
  - [ ] 交付、产物引用、验证对象及人工结论可追到确定 Run 和版本。
  - [ ] 重复提交幂等，产物／提交变化使旧验证不能用于完成新结果。
- **Steps**：
  1. 增加交付与验证仓储，并保留证据与归档引用。
  2. 检查并发交付、旧结果审批与孤立引用拒绝。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1
  ```

- **Deliverables**：存储交付清单与验证证据的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-106"></a>

### VN-106 验证升级备份与旧写者边界

- **Goal**：为身份升级建立隔离演练、维护前提和一致恢复通道。
- **Non-goals**：不执行生产迁移，不自动提交现有工作区。
- **Depends**：`VN-105`、`VN-005`
- **Surface / write_scope**：`internal/db/**`、`internal/version/version.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`、`VN-005`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`STORE` / `B0`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F44`、`F45`、`F47`；`G02`
- **Acceptance**：
  - [ ] 含 WAL 的备份恢复后引用与计数一致，迁移失败保持可恢复状态。
  - [ ] 新写者检查版本；明确旧 0.4 不受新字段保护，存在未排空运行时拒绝破坏性升级。
- **Steps**：
  1. 实现维护与兼容检查、备份清单及恢复验证接口。
  2. 使用临时历史库演练失败中断、旧写者前提和恢复，不接触 ~/.maw。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db ./internal/version -count=1
  ```

- **Deliverables**：验证升级备份与旧写者边界的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-107"></a>

### VN-107 存储来源用量与投影元数据

- **Goal**：为来源会话、计量与可重建读模型提供后续持久化能力。
- **Non-goals**：不绘图，不解析厂商协议，不创建无契约的空表。
- **Depends**：`VN-106`、`VN-006`
- **Surface / write_scope**：`internal/db/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`STORE` / `O2`；**maw**：priority=70 · merge_order=70 · write_scope 同上。
- **需求／门槛**：`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`、`F47`；`G08`
- **Acceptance**：
  - [ ] 会话／轮次按来源分域唯一，usage 保留 scope 与累计／增量语义。
  - [ ] 投影检查点、脏范围与通知去重身份独立于业务状态，空值不变成零。
- **Steps**：
  1. 增加经契约确定的来源与投影存储，不提前生成统计值。
  2. 验证共享轮次、累计重置、检查点重放与通知重复记录。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/db -count=1
  ```

- **Deliverables**：存储来源用量与投影元数据的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-201"></a>

### VN-201 实现领域值对象与应用端口

- **Goal**：把冻结契约转为无业务副作用的 Go 类型和依赖接口。
- **Non-goals**：不写仓储或运行实现，不修改契约字段。
- **Depends**：`VN-004`
- **Surface / write_scope**：`internal/engine/model/**`、`internal/engine/ports/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F44`；无
- **Acceptance**：
  - [ ] 任务／版本／Run／节点／交付身份和错误码只有一套定义。
  - [ ] 端口无 HTTP、SQLite 和厂商进程依赖，非法规格可被验证器拒绝。
- **Steps**：
  1. 实现不可变规格、引用、类型化错误与端口。
  2. 添加不完整规格、标识混用和能力缺失反例。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/model ./internal/engine/ports -count=1
  ```

- **Deliverables**：实现领域值对象与应用端口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-202"></a>

### VN-202 实现运行与交付状态规则

- **Goal**：将任务、运行、交付及风险占用的合法转移独立表达。
- **Non-goals**：不做存储调用，不改旧心跳接线。
- **Depends**：`VN-201`
- **Surface / write_scope**：`internal/engine/state/**`、`internal/machine/machine.go`、`internal/machine/machine_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F17`、`F18`、`F19`、`F20`、`F21`、`F27`、`F28`；`G01`、`G04`
- **Acceptance**：
  - [ ] 取消完成竞争、unknown、停止确认和验收通过分别有规则，运行完成不能直达任务 done。
  - [ ] 既有任务词汇保留；终态、跨版本结论和非法释放被拒绝。
- **Steps**：
  1. 实现三类状态与可复用转移校验。
  2. 用取消竞争、旧验证和未知进程的正反路径覆盖不变量。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/state ./internal/machine -count=1
  ```

- **Deliverables**：实现运行与交付状态规则的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-203"></a>

### VN-203 实现统一领取与资源绑定

- **Goal**：让指定题、人工 next 和后续自动匹配共用原子领取服务。
- **Non-goals**：不启动工具，不将 max_parallel 改为硬容量。
- **Depends**：`VN-202`、`VN-104`、`VN-302`、`VN-501`
- **Surface / write_scope**：`internal/engine/claim/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F12`、`F13`、`F14`、`F15`、`F16`、`F22`、`F23`、`F24`；`G01`、`G03`
- **Acceptance**：
  - [ ] 指定 A 不会再 next 到 B，依赖／写盘／共享文件规则与节点容量一起检查。
  - [ ] 并发领取同题或同目录至多成功一次，失败不遗留部分事件或占用。
- **Steps**：
  1. 复用 schedule 规则并加入节点、目录和预期 revision。
  2. 使用真实临时 Store 复核并发冲突、幂等和事务回滚。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/claim -count=1
  ```

- **Deliverables**：实现统一领取与资源绑定的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-204"></a>

### VN-204 实现持久派发协调循环

- **Goal**：按持久操作推进准备和启动而不依赖 HTTP 请求存活。
- **Non-goals**：不启用自动选题循环，不改公共 CLI 命令。
- **Depends**：`VN-203`、`VN-304`、`VN-404`
- **Surface / write_scope**：`internal/engine/dispatch/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `B0`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F13`、`F17`、`F18`、`F19`、`F20`、`F21`；`G01`、`G03`、`G06`
- **Acceptance**：
  - [ ] 同一状态目录只有一个活动协调者，启动意图和确认进度可恢复。
  - [ ] 启动结果不确定不重复创建工具进程，排队意图可撤回。
- **Steps**：
  1. 实现操作消费、排他协调、步骤提交与 Runner 启动端口。
  2. 模拟准备失败、确认丢失和重复协调者。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/dispatch -count=1
  ```

- **Deliverables**：实现持久派发协调循环的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-205"></a>

### VN-205 实现取消重试与状态核实

- **Goal**：在证据不足时保留占用并提供可解释的恢复决策。
- **Non-goals**：不猜测来源会话，不通过换目录绕过未知执行。
- **Depends**：`VN-202`、`VN-104`、`VN-403`
- **Surface / write_scope**：`internal/engine/reconcile/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `B0`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F18`、`F19`、`F20`、`F21`、`F23`、`F28`；`G01`、`G06`
- **Acceptance**：
  - [ ] 心跳失联只进入核实，取消与正常完成竞争返回确定结果。
  - [ ] 显式停止、任务取消、重试与会话恢复各自校验前驱和占用；人工证据保留来源。
- **Steps**：
  1. 实现读取进程证据、取消意图、恢复资格与有界重试策略。
  2. 覆盖 PID 重用、孤儿不确定、already-finished 与旧代际更新。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/reconcile -count=1
  ```

- **Deliverables**：实现取消重试与状态核实的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-206"></a>

### VN-206 实现工程与任务版本用例

- **Goal**：为创建、增量规划和归档提供唯一的任务应用服务。
- **Non-goals**：不改旧命令调用点，不执行数据库维护。
- **Depends**：`VN-201`、`VN-102`
- **Surface / write_scope**：`internal/engine/tasks/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `B0`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F44`、`F47`；`G02`
- **Acceptance**：
  - [ ] 创建／更新／归档／依赖解析均用稳定实例，活跃 Run 固定旧版本。
  - [ ] 默认增量保留既有交付；reset 不清空活跃占用，不复用旧实例。
- **Steps**：
  1. 实现工程与任务用例及旧编号解析。
  2. 对比增量、重建、歧义历史和运行中改验收的行为。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/tasks -count=1
  ```

- **Deliverables**：实现工程与任务版本用例的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-207"></a>

### VN-207 实现按能力自动匹配节点

- **Goal**：在受管闭环验证后提供显式启用的节点与任务匹配策略。
- **Non-goals**：不实现调度学习或额度推断，不在用户机器启动无人值守任务。
- **Depends**：`VN-203`、`VN-204`、`VN-983`
- **Surface / write_scope**：`internal/engine/matching/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/schedule/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ENGINE` / `X3`；**maw**：priority=70 · merge_order=70 · write_scope 同上。
- **需求／门槛**：`F11`、`F14`、`F15`、`F21`、`F47`；无
- **Acceptance**：
  - [ ] 自动模式默认关闭，按能力、容量、依赖和用户工程策略稳定选择。
  - [ ] 未就绪、未知能力和占用有排队原因，人工 next 不被自动执行配额偷换语义。
- **Steps**：
  1. 实现确定性匹配、策略开关及共享资源组显式配置。
  2. 验证同等候选稳定性、暂停接收和容量冲突。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/engine/matching -count=1
  ```

- **Deliverables**：实现按能力自动匹配节点的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-301"></a>

### VN-301 采集 CodeBuddy 版本化协议证据

- **Goal**：为已可运行的 CodeBuddy 建立脱敏且可追溯的来源样本。
- **Non-goals**：本轮拆包不执行本题；本题实施时也不切换账号、不安装工具、不修改项目。
- **Depends**：无
- **Surface / write_scope**：`docs/evidence/codebuddy/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：本卡产出或现有架构／设计基线；文档／证据按本卡验收冻结。
- **Lane / 范围**：`ADAPTER` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F09`、`F10`、`F17`、`F20`、`F44`、`F46`；`G04`、`G07`
- **Acceptance**：
  - [ ] 成功最小调用的版本、参数、结果和来源标为实测，未观察到的取消／usage 保持 unknown。
  - [ ] 样本包含损坏／缺最终结果等明确标为合成的反例，不记录凭据或项目正文。
- **Steps**：
  1. 在未来执行本题时于临时目录调用已安装 CLI，保存固定文本测试的来源证据。
  2. 建立脱敏清单、样本校验和能力矩阵，不用合成样本冒充来源实测。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/evidence/codebuddy -p 'test_*.py'
  ```

- **Deliverables**：采集 CodeBuddy 版本化协议证据的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-302"></a>

### VN-302 实现节点登记与就绪模型

- **Goal**：统一表达入口、认证、心跳、容量和已验证能力。
- **Non-goals**：不保管厂商秘密，不启动模型任务。
- **Depends**：`VN-201`、`VN-103`、`VN-301`
- **Surface / write_scope**：`internal/nodes/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ADAPTER` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F11`、`F47`；`G07`
- **Acceptance**：
  - [ ] 四维状态与 draining／disabled 分离，Node 与工程 Agent 不混用身份。
  - [ ] 版本或入口变化使对应能力证据失效，未知能力不能被匹配为自动可用。
- **Steps**：
  1. 实现节点配置用例与 Store 端口的能力证据管理。
  2. 验证多实例、容量变更、过期探测与共享资源组。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/nodes -count=1
  ```

- **Deliverables**：实现节点登记与就绪模型的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-303"></a>

### VN-303 实现 CodeBuddy 探测与启动规格

- **Goal**：将冻结的 RunSpec 转为受控 CodeBuddy 调用参数。
- **Non-goals**：不派发进程，不改变认证状态，不扩展来源能力承诺。
- **Depends**：`VN-302`、`VN-201`、`VN-004`
- **Surface / write_scope**：`internal/adapters/codebuddy/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ADAPTER` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F09`、`F10`、`F13`、`F17`、`F24`；`G07`
- **Acceptance**：
  - [ ] 解析真实绝对入口与运行时，版本／帮助探测不会登录或发模型任务。
  - [ ] 输入与固定参数按数组传递，权限／MCP／会话配置受 RunSpec 约束，禁止 shell 拼接。
- **Steps**：
  1. 实现 Probe 和 BuildLaunchSpec 并消费 VN-301 的证据。
  2. 验证路径带空格、环境白名单、非法规格与入口失效。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/adapters/codebuddy -count=1
  ```

- **Deliverables**：实现 CodeBuddy 探测与启动规格的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-304"></a>

### VN-304 实现 CodeBuddy 事件与结束判定

- **Goal**：把来源输出归一化为运行事实而不误判成功。
- **Non-goals**：不做 Token 汇总，不把解析结果直接写任务状态。
- **Depends**：`VN-303`
- **Surface / write_scope**：`internal/adapters/codebuddy/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-003`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ADAPTER` / `B0`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F17`、`F18`、`F19`、`F20`、`F26`、`F28`、`F44`、`F46`；`G04`、`G07`
- **Acceptance**：
  - [ ] 正常结果、应用错误、退出 0 认证失败、缺最终结果和未知版本分别有确定解释。
  - [ ] 稳定记录键支持重放去重，未识别字段只保留证据，不生成 done 或伪造执行区间。
- **Steps**：
  1. 实现 DecodeRecord／InterpretResult，固定版本兼容规则。
  2. 用 VN-301 的来源及反例样本验证边界与脱敏。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/adapters/codebuddy -count=1
  ```

- **Deliverables**：实现 CodeBuddy 事件与结束判定的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-305"></a>

### VN-305 接入 WorkBuddy 两类节点

- **Goal**：为两款 WorkBuddy 复用经验证的协议能力并保留独立配置。
- **Non-goals**：不提取桌面凭据，不自动授权账号，不接管已有聊天。
- **Depends**：`VN-304`、`VN-983`
- **Surface / write_scope**：`internal/adapters/workbuddy/**`、`docs/evidence/workbuddy/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ADAPTER` / `X3`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F13`、`F16`、`F20`；`G07`
- **Acceptance**：
  - [ ] 两包入口、运行时、认证就绪与版本分别登记，不自动继承对方能力。
  - [ ] 只对真实验证过的入口开放受管能力，认证缺失时给出人工接入与处理信息。
- **Steps**：
  1. 实现随包入口探测与 CodeBuddy 解析委托。
  2. 记录两发行包差异、最小调用证据和能力降级用例。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/adapters/workbuddy -count=1
  ```

- **Deliverables**：接入 WorkBuddy 两类节点的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-306"></a>

### VN-306 接入 Trae 的能力分级入口

- **Goal**：按实测结果区分 Trae IDE 人工入口和独立 CLI 能力。
- **Non-goals**：不为凑验收安装 CLI，不默认桌面与 CLI 共享权益。
- **Depends**：`VN-305`
- **Surface / write_scope**：`internal/adapters/trae/**`、`docs/evidence/trae/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ADAPTER` / `X3`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F13`、`F16`、`F20`；`G07`
- **Acceptance**：
  - [ ] 只发现 IDE chat 时保持 manual，不宣称可后台回收结果。
  - [ ] 若具备独立已授权 CLI，Probe／参数／结果通过同一契约用例才开放对应能力。
- **Steps**：
  1. 定位本机已有入口并记录版本与能力证据。
  2. 实现与证据相符的入口适配和缺失／不支持路径。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/adapters/trae -count=1
  ```

- **Deliverables**：接入 Trae 的能力分级入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-307"></a>

### VN-307 接入 ZCode 的显式配置入口

- **Goal**：为 ZCode 建立可解释配置就绪与来源适配。
- **Non-goals**：不复制账号秘密，不改变全局模型选择，不承诺未实测能力。
- **Depends**：`VN-306`
- **Surface / write_scope**：`internal/adapters/zcode/**`、`docs/evidence/zcode/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`ADAPTER` / `X3`；**maw**：priority=70 · merge_order=70 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F13`、`F16`、`F20`；`G07`
- **Acceptance**：
  - [ ] 运行时、provider 配置和模型就绪分开报告，路径补齐使用受控配置引用。
  - [ ] 未验证的模型调用、取消或恢复不开放，缺模型时不自动改用户配置。
- **Steps**：
  1. 基于本机随包布局实现 Probe 与显式 LaunchSpec。
  2. 记录真实可用能力并验证配置缺失、路径空格和未知结果。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/adapters/zcode -count=1
  ```

- **Deliverables**：接入 ZCode 的显式配置入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-308"></a>

### VN-308 解析已验证轮次用量与恢复能力

- **Goal**：按版本把 CodeBuddy 的额外观测和恢复信息映射为标准事实。
- **Non-goals**：不推断空白等待，不实现聚合，不保证所有版本都有完整字段。
- **Depends**：`VN-304`、`VN-804`、`VN-983`
- **Surface / write_scope**：`internal/adapters/codebuddy/**`、`docs/evidence/codebuddy/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`docs/evidence/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`ADAPTER` / `O2`；**maw**：priority=80 · merge_order=80 · write_scope 同上。
- **需求／门槛**：`F10`、`F21`、`F36`、`F37`、`F42`、`F46`；`G07`、`G08`
- **Acceptance**：
  - [ ] 只有真实样本支持时才映射 turn、wait、usage 与明确 session 恢复参数。
  - [ ] 不支持时保留 unknown，累计与增量口径有证据，不使用最近会话作为恢复目标。
- **Steps**：
  1. 扩展经过确认的事件映射及按版本能力证据。
  2. 回放共享轮次、计量重置与显式恢复正反例。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/adapters/codebuddy -count=1
  ```

- **Deliverables**：解析已验证轮次用量与恢复能力的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-401"></a>

### VN-401 实现运行日志与补交游标

- **Goal**：为每个 Run 提供可恢复且有限保留的本机语义日志。
- **Non-goals**：不启动厂商进程，不写业务 done。
- **Depends**：`VN-201`、`VN-002`、`VN-004`
- **Surface / write_scope**：`internal/runner/journal/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`、`internal/adapters/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`RUNNER` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F17`、`F20`、`F26`、`F44`、`F45`、`F46`、`F47`；`G03`
- **Acceptance**：
  - [ ] 冻结规格不含秘密；语义记录有稳定序号和位置，数据库写失败后可去重补交。
  - [ ] 大小／保留限制不会删除未补交记录，裁剪与脱敏造成的不完整显式可见。
- **Steps**：
  1. 实现原子规格落盘、追加记录、检查点和恢复读取。
  2. 覆盖半写记录、磁盘预算、秘密过滤和重复补交。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/runner/journal -count=1
  ```

- **Deliverables**：实现运行日志与补交游标的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-402"></a>

### VN-402 实现 macOS 受管进程启动身份

- **Goal**：为当前平台提供唯一 Run 的启动与进程身份检查。
- **Non-goals**：不重发模型任务，不自动确认未知子进程停止。
- **Depends**：`VN-401`、`VN-201`
- **Surface / write_scope**：`internal/runner/process/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`、`internal/adapters/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`RUNNER` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F13`、`F17`、`F19`、`F23`；`G06`
- **Acceptance**：
  - [ ] 同一 Run 排他锁阻止重复监督实例；PID、启动身份和 nonce 共同校验。
  - [ ] 其他平台构建可返回明确未验证／不支持，不能默认继承 macOS 控制能力。
- **Steps**：
  1. 实现参数数组启动、固定 cwd、运行身份和无 shell 的标准输入。
  2. 使用受控测试子进程验证重复启动、PID 重用及路径空格。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/runner/process -count=1
  ```

- **Deliverables**：实现 macOS 受管进程启动身份的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-403"></a>

### VN-403 实现停止证据与取消竞争

- **Goal**：在当前平台记录真实停止结果并隔离未确认的后代执行。
- **Non-goals**：不杀未知归属进程，不宣称跨平台统一停止保证。
- **Depends**：`VN-402`
- **Surface / write_scope**：`internal/runner/process/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`、`internal/adapters/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`RUNNER` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F19`、`F20`、`F21`、`F23`；`G01`、`G06`
- **Acceptance**：
  - [ ] 取消、超时、已完成和进程失联分别有证据；无法确认后代停止时返回 unknown。
  - [ ] 不会仅因父进程退出或锁释放而宣布目录可复用。
- **Steps**：
  1. 实现有界停止、身份再检查和停止结果分类。
  2. 用正常退出、忽略信号、脱离父进程及取消竞争测试控制边界。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/runner/process -count=1
  ```

- **Deliverables**：实现停止证据与取消竞争的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-404"></a>

### VN-404 实现单次运行监督循环

- **Goal**：把来源进程、语义日志和持久操作连接成一个 Run 生命周期。
- **Non-goals**：不选择下一题，不暴露公共 CLI 命令，不依赖浏览器在线。
- **Depends**：`VN-403`、`VN-104`、`VN-304`、`VN-205`
- **Surface / write_scope**：`internal/runner/session/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`、`internal/adapters/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-003`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`RUNNER` / `B0`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F17`、`F18`、`F19`、`F20`、`F21`、`F26`、`F32`、`F44`；`G03`、`G04`、`G06`
- **Acceptance**：
  - [ ] 启动与结束事实通过应用端口提交，心跳不直接释放业务任务。
  - [ ] 中枢掉线时输出仍持久保存，取消命令可恢复，协议结果不绕过验收。
- **Steps**：
  1. 实现 LaunchSpec 执行、输出摄取、心跳和控制操作消费。
  2. 使用真实受控子进程与临时 Store 验证离线记录、补交和幂等结束。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/runner/session -count=1
  ```

- **Deliverables**：实现单次运行监督循环的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-405"></a>

### VN-405 实现运行恢复与现场核实

- **Goal**：在中枢或 Runner 中断后恢复事实而不重复启动来源任务。
- **Non-goals**：不在真实用户进程上演练故障，不强制释放 unknown。
- **Depends**：`VN-404`、`VN-205`
- **Surface / write_scope**：`internal/runner/recovery/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`、`internal/adapters/codebuddy/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`RUNNER` / `B0`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F20`、`F21`、`F23`、`F44`、`F45`；`G01`、`G06`
- **Acceptance**：
  - [ ] 可区分仍运行、确定结束、尚未启动和启动未知，并恢复日志游标。
  - [ ] Runner 死亡但工具可能活着时保留资源；证据不足不会通过新目录重跑同题。
- **Steps**：
  1. 实现 Run／Operation／日志／进程身份的核实策略。
  2. 对启动前后崩溃、重复核实、主机启动身份变化做故障场景测试。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/runner/recovery -count=1
  ```

- **Deliverables**：实现运行恢复与现场核实的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-501"></a>

### VN-501 实现受占用保护的 Git 工作空间

- **Goal**：在调用现有 worktree 操作前检查目录与执行占用。
- **Non-goals**：不修改现有 gitops 规则，不操作真实仓库或 worktree。
- **Depends**：`VN-201`、`VN-103`
- **Surface / write_scope**：`internal/workspace/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/gitops/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`DELIVERY` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F22`、`F23`、`F24`、`F25`；`G01`
- **Acceptance**：
  - [ ] 规范化路径的独占检查先于 reset／checkout，默认仍按 Agent 复用目录。
  - [ ] 活动／unknown／待验收占用阻止复用，准备失败保留可核实现场。
- **Steps**：
  1. 实现工作空间计划、基线冻结和对 gitops 的受控调用。
  2. 用临时 Git 仓库验证路径别名、重用、脏目录及占用冲突。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/workspace -count=1
  ```

- **Deliverables**：实现受占用保护的 Git 工作空间的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-502"></a>

### VN-502 实现交付收取与结果冻结

- **Goal**：将运行候选结果变成可长期引用的交付清单。
- **Non-goals**：不执行验证命令，不进行评审或合入。
- **Depends**：`VN-501`、`VN-105`、`VN-304`
- **Surface / write_scope**：`internal/delivery/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/gitops/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`DELIVERY` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F24`、`F25`、`F26`、`F29`、`F44`；`G04`
- **Acceptance**：
  - [ ] 验证实际文件、范围、基线／提交和 Run 归属，不能只信 CLI 产物声明。
  - [ ] 归档或可恢复提交引用在目录复用后仍有效，缺失结果不被标记为交付通过。
- **Steps**：
  1. 实现 Manifest 校验、内容快照与交付幂等。
  2. 覆盖越界路径、缺产物、目录复用和重复收取。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/delivery -count=1
  ```

- **Deliverables**：实现交付收取与结果冻结的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-503"></a>

### VN-503 实现版本绑定的代码验收

- **Goal**：把验证证据绑定到冻结规格和确定代码结果。
- **Non-goals**：不从模型输出生成验证命令，不修改 merge 命令接线。
- **Depends**：`VN-502`、`VN-202`、`VN-403`
- **Surface / write_scope**：`internal/verification/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/gitops/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`DELIVERY` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F24`、`F27`、`F28`、`F29`；`G04`、`G06`
- **Acceptance**：
  - [ ] 仅执行用户／工程定义的验证策略，结果保留命令、状态与证据。
  - [ ] 旧提交证据、验证运行失联或范围错误均不能支持 done；明确机器与人工验证。
- **Steps**：
  1. 复用现有 gate 语义并通过受控执行监督收取证据。
  2. 测试环境失败、验证失败、产物变化和人工结论覆盖。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/verification -count=1
  ```

- **Deliverables**：实现版本绑定的代码验收的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-504"></a>

### VN-504 实现独立非 Git 工作目录

- **Goal**：为独立文档和调研任务增加同样受占用约束的目录实现。
- **Non-goals**：不改 Git 实现，不做结果验收。
- **Depends**：`VN-501`、`VN-983`
- **Surface / write_scope**：`internal/workspace/nongit/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/gitops/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`DELIVERY` / `X3`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F07`、`F22`、`F23`、`F24`、`F25`；无
- **Acceptance**：
  - [ ] 非 Git 项目使用明确任务根，仓库内文档仍走 Git 工作空间。
  - [ ] 未知执行与待冻结结果继续占用原目录，不覆盖已有产物。
- **Steps**：
  1. 实现非 Git 路径规划、准备和占用确认。
  2. 验证跨工程路径、保留结果及重复准备。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/workspace/nongit -count=1
  ```

- **Deliverables**：实现独立非 Git 工作目录的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-505"></a>

### VN-505 实现非代码产物验收策略

- **Goal**：让非代码任务在明确产物和验收结论下完成。
- **Non-goals**：不把模型自评当验收，不修改任务 UI。
- **Depends**：`VN-503`、`VN-504`
- **Surface / write_scope**：`internal/verification/nongit/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/gitops/**`、`internal/scope/**`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`DELIVERY` / `X3`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F07`、`F26`、`F27`、`F28`、`F29`；无
- **Acceptance**：
  - [ ] 产物存在、版本和验收条件满足后才能接受，合入步骤仅对不适用任务跳过。
  - [ ] 人工拒绝、产物变更及重新验收都留独立证据。
- **Steps**：
  1. 实现类型化产物条件与人工接受策略。
  2. 验证缺产物、旧快照证据、验收失败和接受完成。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/verification/nongit -count=1
  ```

- **Deliverables**：实现非代码产物验收策略的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-601"></a>

### VN-601 实现本机令牌与浏览器会话

- **Goal**：为新控制接口提供统一的本机授权与同源防护。
- **Non-goals**：不接入现有路由，不建立云端账户。
- **Depends**：`VN-005`、`VN-201`
- **Surface / write_scope**：`internal/localauth/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`internal/engine/ports/**`、`internal/query/**`、`internal/board/serve.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`；`docs/contracts/vnext/http/`。
- **Lane / 范围**：`HTTP` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F13`、`F19`、`F27`、`F47`；`G05`
- **Acceptance**：
  - [ ] CLI 令牌文件权限受限，浏览器／WebView 同源会话与 CSRF 规则可验证。
  - [ ] 拒绝跨站与错误 Host，不在 URL／日志暴露令牌，并明确同用户进程威胁边界。
- **Steps**：
  1. 实现凭据生命周期、会话建立和鉴权中间件。
  2. 以 httptest 覆盖来源、CSRF、Cookie 和泄露反例。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/localauth -count=1
  ```

- **Deliverables**：实现本机令牌与浏览器会话的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-602"></a>

### VN-602 实现工程节点运行查询接口

- **Goal**：将权威查询模型发布为统一版本化读取资源。
- **Non-goals**：不挂接服务根路由，不生成前端数据替身。
- **Depends**：`VN-601`、`VN-801`
- **Surface / write_scope**：`internal/httpapi/read/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`internal/engine/ports/**`、`internal/query/**`、`internal/board/serve.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`HTTP` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F08`、`F09`、`F10`、`F11`、`F17`、`F20`、`F29`、`F30`、`F31`、`F32`、`F44`、`F46`；无
- **Acceptance**：
  - [ ] 读取资源返回实例身份、能力与状态质量，列表有稳定分页。
  - [ ] 旧编号歧义不静默取第一条，查询不会发起探测或业务写操作。
- **Steps**：
  1. 实现项目、任务、节点、Run、Operation 和交付读取。
  2. 验证过滤、错误码、一致水位和不存在资源。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/httpapi/read -count=1
  ```

- **Deliverables**：实现工程节点运行查询接口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-603"></a>

### VN-603 实现工程任务节点配置接口

- **Goal**：对创建、版本更新与节点配置提供带条件的写入口。
- **Non-goals**：不直接写数据库，不做路由接线或派发实现。
- **Depends**：`VN-601`、`VN-206`、`VN-302`
- **Surface / write_scope**：`internal/httpapi/resources/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`internal/engine/ports/**`、`internal/query/**`、`internal/board/serve.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-005`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`HTTP` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F01`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F47`；无
- **Acceptance**：
  - [ ] 创建返回 201，版本冲突／语义错误使用约定状态码，重复请求遵守幂等规则。
  - [ ] 节点配置不允许原始凭据入库，探测使用可查询 Operation。
- **Steps**：
  1. 实现资源输入校验与应用用例调用。
  2. 验证并发编辑、非法范围、重复创建和探测受理。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/httpapi/resources -count=1
  ```

- **Deliverables**：实现工程任务节点配置接口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-604"></a>

### VN-604 实现派发控制与交付操作接口

- **Goal**：把派发、停止和验收请求映射到持久操作用例。
- **Non-goals**：不在 HTTP 请求内等待模型执行，不直接调用厂商 CLI。
- **Depends**：`VN-601`、`VN-204`、`VN-205`、`VN-502`、`VN-503`
- **Surface / write_scope**：`internal/httpapi/commands/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/engine/ports/**`、`internal/query/**`、`internal/board/serve.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-003`、`VN-004`、`VN-005`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`HTTP` / `B0`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F26`、`F27`、`F28`、`F33`；`G04`、`G05`
- **Acceptance**：
  - [ ] 异步操作返回 202 和查询位置，受理、运行结束及验收完成不混用。
  - [ ] 重复取消、同键不同参数、旧 revision 和未授权交付均有确定响应。
- **Steps**：
  1. 实现领取、派发、取消、重试、交付、验证和评审的传输适配。
  2. 验证命令受理与业务结论分离，错误不泄露内部堆栈。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/httpapi/commands -count=1
  ```

- **Deliverables**：实现派发控制与交付操作接口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-605"></a>

### VN-605 实现事件补拉与 SSE 兼容传输

- **Goal**：提供可恢复的变化通知和完整持久事件分页。
- **Non-goals**：不维护另一套状态，不用推送成功代替消费者确认。
- **Depends**：`VN-601`、`VN-801`、`VN-802`
- **Surface / write_scope**：`internal/httpapi/events/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`internal/engine/ports/**`、`internal/query/**`、`internal/board/serve.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-005`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`HTTP` / `B0`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F16`、`F32`、`F33`、`F44`、`F45`、`F46`；`G03`、`G05`
- **Acceptance**：
  - [ ] 超过 200 条积压、过滤空页和断线重连不跳过匹配事件。
  - [ ] 新旧 SSE 帧可适配，旁路上报只写观测事实，不占有任务或直接 done。
- **Steps**：
  1. 实现扫描水位、has_more、变化流及受约束 observation 摄取。
  2. 验证慢客户端、重放去重和旧事件名称兼容。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/httpapi/events -count=1
  ```

- **Deliverables**：实现事件补拉与 SSE 兼容传输的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-606"></a>

### VN-606 实现观测与提醒查询接口

- **Goal**：将投影、计量和提醒用例以统一分页与质量字段提供给页面。
- **Non-goals**：不在 HTTP 层重复聚合，不修改服务根路由。
- **Depends**：`VN-601`、`VN-803`、`VN-804`、`VN-805`、`VN-806`
- **Surface / write_scope**：`internal/httpapi/observability/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`internal/engine/ports/**`、`internal/query/**`、`internal/board/serve.go`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`HTTP` / `O2`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F28`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F46`、`F47`；`G05`、`G08`
- **Acceptance**：
  - [ ] 时间轴、热图、会话、统计与提醒使用统一窗口、来源过滤和权限检查。
  - [ ] 缺测、投影落后和不支持能力是类型化结果；SSE 只做失效通知，查询可恢复事实。
- **Steps**：
  1. 实现观测读接口与提醒确认的薄适配。
  2. 核对契约数值样本、游标和权限错误映射。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/httpapi/observability -count=1
  ```

- **Deliverables**：实现观测与提醒查询接口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-701"></a>

### VN-701 保行为拆分内嵌页面资源

- **Goal**：把现有 Board 资源拆为可维护模块而不改变现有只读行为。
- **Non-goals**：不挂载新业务页面，不换框架，不修改后端业务逻辑。
- **Depends**：`VN-021`、`VN-022`
- **Surface / write_scope**：`internal/board/page.html`、`internal/board/board.go`、`internal/board/serve.go`、`internal/board/page.test.cjs`、`internal/board/assets/legacy/**`、`internal/board/assets/bootstrap/**`、`internal/board/snapshot_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-021`、`VN-022`；`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-SHELL` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F30`、`F31`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 现有看板、拓扑、筛选、抽屉、主题和语言测试保留，测试加载实际发布资源。
  - [ ] HTTP 嵌入资源与静态快照导出均可加载，静态模式不出现可执行写操作。
- **Steps**：
  1. 按契约抽取当前样式和脚本到 legacy 资源并支持嵌入／导出。
  2. 调整回归加载器，证明资产拆分没有重写页面视觉和旧行为。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/page.test.cjs
  CGO_ENABLED=0 go test ./internal/board -count=1
  ```

- **Deliverables**：保行为拆分内嵌页面资源的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-702"></a>

### VN-702 建立共享主题组件与状态样式

- **Goal**：让所有新增视图复用同一套 Workbench 视觉和交互组件。
- **Non-goals**：不写页面导航，不私自增加新的视觉风格。
- **Depends**：`VN-701`、`VN-981`
- **Surface / write_scope**：`internal/board/assets/styles/**`、`internal/board/assets/components/**`、`internal/board/assets/legacy/style.css`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-021`、`VN-022`；`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-SHELL` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F30`、`F31`、`F32`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F46`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] token 成为唯一视觉源，状态徽标、表格、表单、空态和抽屉无页内硬编码配色。
  - [ ] 颜色配文字，焦点、抽屉返回焦点、减弱动画和系统主题沿用旧约定。
- **Steps**：
  1. 抽取 token 和现有可复用组件，定义来源标记与状态色的分工。
  2. 以共享组件场景验证深浅主题、长中文、键盘与窄屏。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/components/components.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-702'
  ```

- **Deliverables**：建立共享主题组件与状态样式的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-703"></a>

### VN-703 实现公共请求与页面上下文

- **Goal**：集中处理授权、幂等操作、筛选和过期请求响应。
- **Non-goals**：不挂路由，不在客户端计算业务统计，不分散保存令牌。
- **Depends**：`VN-702`、`VN-005`、`VN-022`
- **Surface / write_scope**：`internal/board/assets/api/**`、`internal/board/assets/utils/request-context.mjs`、`internal/board/assets/utils/request-context.test.cjs`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-SHELL` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F13`、`F19`、`F28`、`F32`、`F47`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 统一处理 CSRF、revision、202 操作轮询及结构化错误，不把受理当完成。
  - [ ] 切换工程与并发请求不会覆盖新上下文，状态未知和刷新恢复可见。
- **Steps**：
  1. 实现资源请求客户端与注入 ViewContext 的过滤／操作状态。
  2. 覆盖乱序返回、过期请求、相同幂等键和401／409／unknown。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/utils/request-context.test.cjs
  ```

- **Deliverables**：实现公共请求与页面上下文的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-704"></a>

### VN-704 挂载首个受管闭环页面

- **Goal**：在统一布局内接入节点、任务、运行和交付视图。
- **Non-goals**：不重做业务页，不改 API 或补造后端数据。
- **Depends**：`VN-703`、`VN-721`、`VN-722`、`VN-723`、`VN-724`、`VN-981`
- **Surface / write_scope**：`internal/board/page.html`、`internal/board/page.test.cjs`、`internal/board/assets/bootstrap/**`、`internal/board/assets/shell/**`、`internal/board/assets/i18n/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-SHELL` / `C1`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F30`、`F31`、`F32`、`F33`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 导航、旧工程锚点、筛选和详情返回保持一致，根布局与全局语言只由此包收口。
  - [ ] 真实挂载组件通过主题／双语／窄屏检查，pending 与 unknown 不呈现假成功。
- **Steps**：
  1. 实现统一注册、路由与 ViewContext 注入，保留兼容入口。
  2. 接入页面局部字典并验证视图间状态与键盘返回。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/page.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-704'
  ```

- **Deliverables**：挂载首个受管闭环页面的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-705"></a>

### VN-705 挂载回顾统计与设置入口

- **Goal**：在同一工作台中接入后续视图而不再扩出第二套布局。
- **Non-goals**：不修改页面图表或统计口径，不新增平行菜单体系。
- **Depends**：`VN-704`、`VN-725`、`VN-741`、`VN-742`、`VN-743`、`VN-744`
- **Surface / write_scope**：`internal/board/page.html`、`internal/board/page.test.cjs`、`internal/board/assets/bootstrap/**`、`internal/board/assets/shell/**`、`internal/board/assets/i18n/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-SHELL` / `O2`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F31`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`、`F47`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 回顾、统计、工程设置共用导航和筛选，URL 及面包屑能恢复上下文。
  - [ ] 来源能力缺失时降级文案与空态一致，静态导出保持只读。
- **Steps**：
  1. 注册后续视图、局部词典与能力可见性。
  2. 验证跨视图下钻、历史返回和主题／语言切换。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/page.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-705'
  ```

- **Deliverables**：挂载回顾统计与设置入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-721"></a>

### VN-721 实现节点列表与能力详情页

- **Goal**：清楚展示节点能否接任务及需要用户处理的条件。
- **Non-goals**：禁止修改 router、shell、全局 token、公共 API 客户端；不直接启动 App。
- **Depends**：`VN-703`、`VN-022`、`VN-981`
- **Surface / write_scope**：`internal/board/assets/views/nodes/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-CONTROL` / `C1`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F11`、`F47`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 就绪四维、能力未知、版本失效和暂停接收分别展示，不把在线等同可执行。
  - [ ] 表单与详情复用公共组件，深浅／双语／窄屏及未认证状态均有场景。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现节点表格、详情与受控配置交互，使用公共请求客户端。
  2. 新增局部字典与浏览器场景，不自行处理令牌。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/nodes/nodes.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-721'
  ```

- **Deliverables**：实现节点列表与能力详情页的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-722"></a>

### VN-722 实现任务创建与指定派发视图

- **Goal**：将任务定义与指定节点派发接入现有任务工作台。
- **Non-goals**：禁止修改导航／全局布局，不实现前端调度算法。
- **Depends**：`VN-721`
- **Surface / write_scope**：`internal/board/assets/views/tasks/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-CONTROL` / `C1`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F04`、`F05`、`F06`、`F12`、`F13`、`F14`、`F15`、`F16`、`F30`、`F31`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 创建与编辑保留版本语义，依赖／范围／节点不满足时解释阻止原因。
  - [ ] 重用任务卡与拓扑组件，显示实际优先级，派发202后进入操作状态而非标完成。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现任务表单、版本冲突提示和指定派发操作。
  2. 覆盖重复点击、无可用节点、任务范围错误和旧上下文响应。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/tasks/tasks.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-722'
  ```

- **Deliverables**：实现任务创建与指定派发视图的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-723"></a>

### VN-723 实现运行详情与待核实面板

- **Goal**：让用户定位本次执行、证据缺口及保留资源。
- **Non-goals**：禁止修改公共抽屉实现或全局样式，不提供无证据强制释放捷径。
- **Depends**：`VN-722`
- **Surface / write_scope**：`internal/board/assets/views/runs/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-CONTROL` / `C1`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F17`、`F18`、`F19`、`F20`、`F21`、`F23`、`F28`、`F29`、`F30`、`F31`、`F32`、`F46`、`F48`；`G01`、`UI-C`
- **Acceptance**：
  - [ ] 运行、交付、任务状态分开，取消受理不立即显示已停止。
  - [ ] unknown 面板给出最后事实、未知点、占用资源与处理入口，文本安全渲染。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现执行摘要、证据、操作状态与取消／核实交互。
  2. 覆盖运行中切项目、已完成竞争、孤儿未知和长错误文案。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/runs/runs.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-723'
  ```

- **Deliverables**：实现运行详情与待核实面板的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-724"></a>

### VN-724 实现交付与验收详情页

- **Goal**：让用户核对产物和验证证据后处理交付。
- **Non-goals**：禁止修改路由／壳，不在浏览器执行 gate 或打开任意路径。
- **Depends**：`VN-723`
- **Surface / write_scope**：`internal/board/assets/views/deliveries/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-003`、`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-CONTROL` / `C1`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F26`、`F27`、`F28`、`F29`、`F48`；`G04`、`UI-C`
- **Acceptance**：
  - [ ] 展示产物／提交、验证对象和结论，运行退出成功不会自动变成验收通过。
  - [ ] 旧版本结论不可用于新交付，人工结论与合入操作状态可追溯。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现清单、证据与受策略约束的验收动作。
  2. 覆盖缺产物、验证失败、人工拒绝及提交变化。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/deliveries/deliveries.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-724'
  ```

- **Deliverables**：实现交付与验收详情页的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-725"></a>

### VN-725 实现工程策略与保留设置页

- **Goal**：统一配置工程执行策略和已支持的数据保留选项。
- **Non-goals**：禁止修改 shell／公共客户端，不加入平台统一购买额度功能。
- **Depends**：`VN-724`、`VN-956`
- **Surface / write_scope**：`internal/board/assets/views/projects/**`、`internal/board/assets/views/settings/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-CONTROL` / `X3`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F07`、`F11`、`F14`、`F33`、`F47`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 自动模式、计算上限、节点容量和 max_parallel 参考值有不同标签，不默认开启自动执行。
  - [ ] 设置页沿用相同表单、错误与保存状态；无秘密字段直接编辑入口。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现同一业务族的工程策略与全局保留配置表单。
  2. 验证 revision 冲突、非法容量、模式未就绪与实际保存恢复。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/settings/settings.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-725'
  ```

- **Deliverables**：实现工程策略与保留设置页的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-741"></a>

### VN-741 实现分层执行时间轴视图

- **Goal**：按工程和运行展示任务阶段与真实可用的执行区间。
- **Non-goals**：禁止修改路由、全局颜色，不在页面推算缺失执行时长。
- **Depends**：`VN-703`、`VN-803`、`VN-022`、`VN-981`
- **Surface / write_scope**：`internal/board/assets/views/timeline/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-OBS` / `O2`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F31`、`F35`、`F36`、`F38`、`F46`、`F48`；`G08`、`UI-C`
- **Acceptance**：
  - [ ] 阶段驻留、CLI 运行、可用轮次和明确等待图例分开，未知区间不补满。
  - [ ] 支持小时窗口、缩放、键盘聚焦和详情下钻，使用统一 token 与日期上下文。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现服务端区间数据的渲染、缩放与选择交互。
  2. 覆盖跨日、稀疏数据、长标签、多并发及深浅主题。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/timeline/timeline.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-741'
  ```

- **Deliverables**：实现分层执行时间轴视图的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-742"></a>

### VN-742 实现全天活动与小时下钻

- **Goal**：以明确口径的热图帮助定位工程活动时段。
- **Non-goals**：禁止改 shell／公共时间格式，不照搬截图蓝紫视觉。
- **Depends**：`VN-741`、`VN-805`
- **Surface / write_scope**：`internal/board/assets/views/activity/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-OBS` / `O2`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F34`、`F35`、`F38`、`F40`、`F41`、`F46`、`F48`；`G08`、`UI-C`
- **Acceptance**：
  - [ ] 事件活跃度与执行覆盖使用不同单位和文案，缺测不涂成零活动。
  - [ ] 全天点击进入同一小时上下文，非颜色提示及键盘访问完整。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现全天桶与小时选择，不在页面重新聚合来源记录。
  2. 覆盖空日、跨时区、部分覆盖和多工程筛选。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/activity/activity.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-742'
  ```

- **Deliverables**：实现全天活动与小时下钻的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-743"></a>

### VN-743 实现会话轮次与来源明细

- **Goal**：对可观测的来源会话展示轮次、跨度和关联任务。
- **Non-goals**：禁止改公共计量口径，不把来源文本解释为可执行指令。
- **Depends**：`VN-742`、`VN-804`、`VN-308`
- **Surface / write_scope**：`internal/board/assets/views/sessions/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-OBS` / `O2`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F29`、`F37`、`F38`、`F42`、`F44`、`F46`、`F48`；`G08`、`UI-C`
- **Acceptance**：
  - [ ] 显示共享／未归属关联、完整性和已知用量，不强制一会话等于一任务。
  - [ ] 不支持轮次的来源给出一致降级态，输入点只在有真实事件时出现。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现会话轮次表与时间轴之间的跳转。
  2. 验证共享轮次、缺边界、未知 Token 与不支持来源。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/sessions/sessions.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-743'
  ```

- **Deliverables**：实现会话轮次与来源明细的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-744"></a>

### VN-744 实现交付与用量统计视图

- **Goal**：在统一筛选下展示成果、失败和可解释投入。
- **Non-goals**：禁止改导航或全局 token，不推算节省人力倍数或生产力排名。
- **Depends**：`VN-743`、`VN-805`
- **Surface / write_scope**：`internal/board/assets/views/analytics/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`docs/BOARD_DESIGN.md`、`docs/zh/BOARD_DESIGN.md`、`internal/board/assets/components/**`、`internal/board/assets/api/**`、`internal/board/assets/utils/**`、`tests/browser/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`FE-PAGES-OBS` / `O2`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F02`、`F39`、`F40`、`F41`、`F42`、`F43`、`F46`、`F48`；`G08`、`UI-C`
- **Acceptance**：
  - [ ] 成果／失败与投入分开，累计／并集／平均并发各有口径，未知不显示为零。
  - [ ] 来源分布与时间图使用共享色板和图例，所有汇总可下钻到证据。
  - [ ] UI-C 共用主题／布局组件，中英深浅主题与窄屏通过；键盘和加载／空／错误／过期／unknown 状态可核对，未另造公共层。
- **Steps**：
  1. 实现统一指标行、小时分布和来源对比。
  2. 校验覆盖率、未分摊、无分母与筛选／搜索口径提示。
- **Verify**（本卡实现时执行）：

  ```sh
  node --test internal/board/assets/views/analytics/analytics.test.cjs
  npm --prefix tests/browser test -- --grep 'VN-744'
  ```

- **Deliverables**：实现交付与用量统计视图的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-801"></a>

### VN-801 实现权威资源查询与历史分页

- **Goal**：提供一致快照和不会截断后续事件的基础查询。
- **Non-goals**：不使用写事务读取，不实现图表聚合。
- **Depends**：`VN-201`、`VN-104`、`VN-105`、`VN-006`
- **Surface / write_scope**：`internal/query/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`、`VN-005`、`VN-006`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`QUERY` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F08`、`F09`、`F10`、`F11`、`F17`、`F20`、`F29`、`F30`、`F31`、`F32`、`F44`、`F45`、`F46`；`G03`
- **Acceptance**：
  - [ ] 任务、Run、Operation、节点与交付查询保留稳定身份和一致状态水位。
  - [ ] 大于200条、过滤空页和同名历史的游标查询不漏匹配记录。
- **Steps**：
  1. 实现只读端口、筛选与当前／退役实例解析。
  2. 以实际临时库验证并发插入下的分页和状态水位。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/query -count=1
  ```

- **Deliverables**：实现权威资源查询与历史分页的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-802"></a>

### VN-802 实现标准观测摄取与去重

- **Goal**：将来源和旁路记录写成有质量与归属边界的事件。
- **Non-goals**：不做业务调度，不变更原始来源记录。
- **Depends**：`VN-801`
- **Surface / write_scope**：`internal/events/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`QUERY` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F16`、`F32`、`F44`、`F45`、`F46`；`G03`、`G08`
- **Acceptance**：
  - [ ] 来源分域去重，日志补交与晚到事件可保留发生／接收时间。
  - [ ] 旁路上报不取得任务所有权，不把缺字段填成真实执行或 done。
- **Steps**：
  1. 实现类型化事件摄取、幂等与来源质量校验。
  2. 覆盖重复内容独立事件、旧字段、乱序和越权对象。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/events -count=1
  ```

- **Deliverables**：实现标准观测摄取与去重的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-803"></a>

### VN-803 实现阶段与执行区间投影

- **Goal**：从事实生成分层区间并支持跨日重建。
- **Non-goals**：不在推送层计算，不根据无输出推断人工等待。
- **Depends**：`VN-802`、`VN-107`、`VN-983`
- **Surface / write_scope**：`internal/projection/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`QUERY` / `O2`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F34`、`F35`、`F36`、`F37`、`F38`、`F40`、`F43`、`F44`、`F45`、`F46`；`G08`
- **Acceptance**：
  - [ ] 区间类型分开，窗口前边界与半开裁剪正确，晚到记录补算受影响范围。
  - [ ] 投影可重建且幂等，缺失边界不制造完整时长，状态与投影水位分别返回。
- **Steps**：
  1. 实现游标、脏范围与阶段／进程／明确等待区间。
  2. 验证跨时区、时钟倒置、重放与不完整历史。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/projection -count=1
  ```

- **Deliverables**：实现阶段与执行区间投影的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-804"></a>

### VN-804 实现会话归属与用量计量

- **Goal**：统一管理来源轮次关联和可追溯的已知消耗。
- **Non-goals**：不猜测平台费用，不要求所有厂商都有 Token。
- **Depends**：`VN-802`、`VN-107`、`VN-006`
- **Surface / write_scope**：`internal/observability/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`QUERY` / `O2`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F37`、`F42`、`F44`、`F45`、`F46`；`G08`
- **Acceptance**：
  - [ ] 多 Run 关联同一 Turn 不重复计量，无分摊依据时保留未分摊。
  - [ ] 累计快照、重置、缓存包含关系与 null 按契约处理，覆盖率只针对已观测分母。
- **Steps**：
  1. 实现来源身份关联、usage 观测和计量查询。
  2. 用跨来源同ID、累计重置及重复实时／回放样本验证。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/observability -count=1
  ```

- **Deliverables**：实现会话归属与用量计量的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-805"></a>

### VN-805 实现工程与来源统计查询

- **Goal**：为热图和复盘提供统一且可下钻的聚合结果。
- **Non-goals**：不在前端重复实现统计，不给投入量做生产力评分。
- **Depends**：`VN-803`、`VN-804`、`VN-503`
- **Surface / write_scope**：`internal/analytics/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-003`、`VN-005`、`VN-006`；`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`QUERY` / `O2`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F02`、`F34`、`F39`、`F40`、`F41`、`F42`、`F43`、`F46`；`G08`
- **Acceptance**：
  - [ ] 累计、并集、平均并发、小时桶和交付数的单位与去重身份一致。
  - [ ] 零分母、缺数据、共享用量和搜索范围不混算，指标可追到证据。
- **Steps**：
  1. 实现按工程／来源／日期聚合与质量元数据。
  2. 使用固定已知区间和真实临时库核对累计与并集差异。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/analytics -count=1
  ```

- **Deliverables**：实现工程与来源统计查询的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-806"></a>

### VN-806 实现可恢复的介入提醒

- **Goal**：为等待、失败和关键交付生成有身份的提醒记录。
- **Non-goals**：不发送邮件或聊天消息，不接入外部通知服务。
- **Depends**：`VN-802`、`VN-107`、`VN-983`
- **Surface / write_scope**：`internal/notifications/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-005`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`QUERY` / `O2`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F20`、`F28`、`F33`、`F47`；无
- **Acceptance**：
  - [ ] 按业务事件和渠道去重，默认不为无变化状态持续打扰。
  - [ ] 重启后能恢复未处理提醒，OS 弹窗非原子投递的重复边界可见。
- **Steps**：
  1. 实现提醒规则、保留与投递确认端口。
  2. 验证重复事件、状态未变、用户确认及投递中断。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/notifications -count=1
  ```

- **Deliverables**：实现可恢复的介入提醒的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-807"></a>

### VN-807 实现按占用与证据约束的数据保留

- **Goal**：让日志和历史产物按策略清理且保持可恢复执行与证据可追溯。
- **Non-goals**：不清理真实用户目录，不提供绕过 unknown 的强删开关。
- **Depends**：`VN-107`、`VN-502`、`VN-401`、`VN-983`
- **Surface / write_scope**：`internal/retention/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`internal/engine/model/**`、`internal/engine/ports/**`、`internal/db/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-003`、`VN-004`、`VN-005`、`VN-006`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`。
- **Lane / 范围**：`QUERY` / `O2`；**maw**：priority=70 · merge_order=70 · write_scope 同上。
- **需求／门槛**：`F25`、`F29`、`F44`、`F45`、`F46`、`F47`；`G01`、`G04`、`G08`
- **Acceptance**：
  - [ ] 默认保留策略显式，进行中／unknown 的目录、未补交日志和必要交付证据不被回收。
  - [ ] 跨 DB／文件删除有持久意图和核实，失败重试幂等，查询能区分已清理与缺测。
- **Steps**：
  1. 实现清理候选、占用／引用保护与文件删除对账。
  2. 以临时目录注入删除失败、重启、并发新引用与用户缩短保留期。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/retention -count=1
  ```

- **Deliverables**：实现按占用与证据约束的数据保留的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-901"></a>

### VN-901 升级人工领取与旁路上报协议

- **Goal**：让现有人工客户端在同一身份、授权和交付协议下继续协作。
- **Non-goals**：不改用户全局 skills，不自动注册或唤起桌面客户端。
- **Depends**：`VN-952`、`VN-954`
- **Surface / write_scope**：`internal/protocol/**`、`skills/maw-status-attach/**`、`skills/maw-task-pull/**`、`skills/maw/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`internal/protocol/**`、`internal/app/runtime.go`、`internal/board/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-005`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`CLIENTS` / `C1`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F04`、`F05`、`F06`、`F07`、`F12`、`F16`、`F24`、`F25`、`F26`、`F27`、`F32`、`F44`、`F45`、`F47`；`G01`、`G04`、`G05`
- **Acceptance**：
  - [ ] 人工领取仍可用，模板保留实例／版本与来源；令牌不进入提示词、仓库或事件。
  - [ ] 旁路仅能上报自己的观察，无法夺取 managed Run 所有权，协议中英文同步。
- **Steps**：
  1. 更新内嵌协议与仓库技能中的领取、心跳、提交示例。
  2. 用协议生成测试验证版本、授权说明与旧客户端升级提示。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/protocol -count=1
  ```

- **Deliverables**：升级人工领取与旁路上报协议的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-902"></a>

### VN-902 接通桌面提醒与受限页面桥接

- **Goal**：让 Tauri 复用同一业务页面并投递可恢复的本机提醒。
- **Non-goals**：不建第二套前端，不把普通窗口关闭解释成取消执行。
- **Depends**：`VN-806`、`VN-704`、`VN-955`
- **Surface / write_scope**：`clients/desktop/src-tauri/src/**`、`clients/desktop/src-tauri/Cargo.toml`、`clients/desktop/src-tauri/Cargo.lock`、`clients/desktop/src-tauri/capabilities/default.json`、`clients/desktop/src-tauri/tauri.conf.json`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`internal/protocol/**`、`internal/app/runtime.go`、`internal/board/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`CLIENTS` / `O2`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F20`、`F28`、`F33`、`F47`、`F48`；`G05`、`UI-C`
- **Acceptance**：
  - [ ] 启动、重连和关闭窗口不接管 Runner 生命周期，桌面与浏览器显示同一权威状态。
  - [ ] 桥接仅允许已声明通知与导航；权限拒绝时保留应用内提醒，令牌不放 URL 或日志。
- **Steps**：
  1. 复用服务授权引导，接通提醒确认和跳转。
  2. 在实际 macOS WebView 核对深浅主题、CJK、重连和通知拒绝；证据随卡交付。
- **Verify**（本卡实现时执行）：

  ```sh
  cargo test --manifest-path clients/desktop/src-tauri/Cargo.toml
  cargo check --manifest-path clients/desktop/src-tauri/Cargo.toml
  ```

- **Deliverables**：接通桌面提醒与受限页面桥接的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-903"></a>

### VN-903 更新操作指南与迁移发布说明

- **Goal**：为人工兼容、首个受管闭环及后续能力提供可核实的使用与恢复说明。
- **Non-goals**：不发布二进制、不创建 tag、不替用户迁移真实库。
- **Depends**：`VN-985`
- **Surface / write_scope**：`README.md`、`README.zh-CN.md`、`SECURITY.md`、`SECURITY.zh-CN.md`、`clients/desktop/README.md`、`clients/desktop/README.zh-CN.md`、`docs/operations/vnext/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`internal/protocol/**`、`internal/app/runtime.go`、`internal/board/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-005`、`VN-006`、`VN-021`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`。
- **Lane / 范围**：`CLIENTS` / `X3`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F30`、`F31`、`F32`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`、`F47`、`F48`；`G01`、`G02`、`G05`、`G06`、`G07`、`G08`
- **Acceptance**：
  - [ ] 文档区分已支持、需证据与不支持的平台／来源；明确 automatic 默认关闭及 CodeBuddy 可运行不等于全部能力通过。
  - [ ] 升级说明覆盖停旧写端、WAL 一致备份、故障恢复、unknown 解除证据与不可直接降级写新库。
- **Steps**：
  1. 根据已通过的验收证据更新中英文入口与操作手册。
  2. 校验命令示例、支持矩阵、链接与恢复路径，并收录文档样本测试。
- **Verify**（本卡实现时执行）：

  ```sh
  python3 -m unittest discover -s docs/operations/vnext -p 'test_*.py'
  ```

- **Deliverables**：更新操作指南与迁移发布说明的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-951"></a>

### VN-951 接通增量存储与任务身份入口

- **Goal**：让初始化、规划更新和工程解析使用新存储与稳定任务实例。
- **Non-goals**：不顺带改领取、提交或 HTTP 鉴权，不处理当前真实全局库。
- **Depends**：`VN-106`、`VN-206`
- **Surface / write_scope**：`internal/app/project.go`、`internal/app/init_cmd.go`、`internal/app/plan_cmd.go`、`internal/app/helpers.go`、`internal/app/task_services.go`、`internal/app/task_identity_test.go`、`internal/app/plan_apply_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`internal/app/**`、`internal/engine/**`、`internal/runner/**`、`internal/httpapi/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`WIRE` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F44`、`F45`；`G02`、`G03`
- **Acceptance**：
  - [ ] plan apply 保留稳定实例，删除定义转退役；重建同显示号不会串到旧 Run。
  - [ ] 工程只读查询不隐式升级写库；有状态升级必须遵循停写与备份入口。
- **Steps**：
  1. 替换共享初始化和任务定义更新路径。
  2. 以临时 MAW_GLOBAL、旧库样本验证编号重建、增量更新与只读打开。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1
  ```

- **Deliverables**：接通增量存储与任务身份入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-952"></a>

### VN-952 统一人工领取与旧心跳占用语义

- **Goal**：将 next 和人工进度接入同一领取用例且隔离 managed Run 过期处理。
- **Non-goals**：同文件 Submit／Review 留给 VN-953，不实现自动选择节点。
- **Depends**：`VN-951`、`VN-203`、`VN-205`、`VN-302`、`VN-802`
- **Surface / write_scope**：`internal/app/worker_cmd.go`、`internal/app/heartbeat.go`、`internal/app/status_cmd.go`、`internal/app/claim_compat_test.go`、`internal/app/status_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/app/**`、`internal/engine/**`、`internal/runner/**`、`internal/httpapi/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-004`、`VN-005`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`WIRE` / `B0`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F08`、`F09`、`F10`、`F11`、`F12`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F32`、`F44`、`F45`；`G01`、`G03`
- **Acceptance**：
  - [ ] 手工 next 与 managed ClaimExecution 竞争同一任务只有一个成功；原有依赖／scope／亲和策略仍生效。
  - [ ] 旧 expireStaleAgents 无法把 managed unknown 任务打回可重派；max_parallel 参考值与新增真实上限清楚区分。
- **Steps**：
  1. 改接 Next、Register、Progress 与状态归属，保留非受管兼容路径。
  2. 覆盖旧心跳触发、人工／受管竞争和容量配置迁移。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1
  ```

- **Deliverables**：统一人工领取与旧心跳占用语义的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-953"></a>

### VN-953 统一旧提交评审与目录清理入口

- **Goal**：让 submit、review、merge 与清理都遵守交付证据和占用规则。
- **Non-goals**：不改 Next 语义，不扩大无证据强制清理能力。
- **Depends**：`VN-952`、`VN-502`、`VN-503`
- **Surface / write_scope**：`internal/app/worker_cmd.go`、`internal/app/merge_cmd.go`、`internal/app/task_cmd.go`、`internal/app/prune_cmd.go`、`internal/app/delivery_compat_test.go`、`internal/app/e2e_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`internal/app/**`、`internal/engine/**`、`internal/runner/**`、`internal/httpapi/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`。
- **Lane / 范围**：`WIRE` / `B0`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F07`、`F19`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F44`、`F45`；`G01`、`G04`、`G06`
- **Acceptance**：
  - [ ] 原有提交、拒绝、阻塞与合入调用同一交付／验收用例，不存在 CLI 绕过接口。
  - [ ] unknown、残留进程或证据不匹配时无法 prune/reset/merge；人工兼容任务按明确策略完成。
- **Steps**：
  1. 在现有命令参数兼容前提下替换交付与清理实现。
  2. 验证临时 Git 的正常合入、拒绝、提交变更和目录占用拒绝。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1
  ```

- **Deliverables**：统一旧提交评审与目录清理入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-954"></a>

### VN-954 组装运行服务与受管命令入口

- **Goal**：提供默认关闭的受管运行入口和同一二进制独立 Runner。
- **Non-goals**：不改厂商登录或权益，不在首轮开放自动任务／节点匹配。
- **Depends**：`VN-953`、`VN-204`、`VN-405`、`VN-601`
- **Surface / write_scope**：`cmd/maw/main.go`、`internal/app/managed_cmd.go`、`internal/app/runtime.go`、`internal/app/managed_cmd_test.go`、`internal/config/**`、`internal/i18n/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/app/**`、`internal/engine/**`、`internal/runner/**`、`internal/httpapi/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`、`VN-005`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`WIRE` / `C1`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F01`、`F03`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F33`、`F47`；`G01`、`G02`、`G05`、`G06`、`G07`
- **Acceptance**：
  - [ ] 显式任务／节点启动进入同一 Operation 流程，Runner 与中枢分离，窗口退出不杀执行。
  - [ ] 配置版本、受管上限1、自动模式关闭、平台不支持与结构化错误可见，中英文键同步。
- **Steps**：
  1. 增加根命令、运行工厂、配置迁移和受限内部 Runner 启动入口。
  2. 验证引导令牌、参数白名单、重复启动和无 GUI 下运行。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/app ./internal/config ./internal/i18n -count=1
  CGO_ENABLED=0 go build -o /tmp/maw-vnext-verify ./cmd/maw
  ```

- **Deliverables**：组装运行服务与受管命令入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-955"></a>

### VN-955 接通 HTTP 与旧看板兼容入口

- **Goal**：将同一用例、安全边界和事件恢复接入现有看板服务。
- **Non-goals**：不重复实现业务状态机，不直接修改页面布局和 token。
- **Depends**：`VN-954`、`VN-602`、`VN-603`、`VN-604`、`VN-605`、`VN-701`、`VN-901`
- **Surface / write_scope**：`internal/app/board_cmd.go`、`internal/board/serve.go`、`internal/board/events.go`、`internal/board/serve_test.go`、`internal/board/events_test.go`、`internal/board/runtime_integration_test.go`、`internal/board/status_api_test.go`、`internal/app/board_snapshot_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui-data/`、`internal/app/**`、`internal/engine/**`、`internal/runner/**`、`internal/httpapi/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-005`、`VN-022`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`WIRE` / `C1`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F30`、`F31`、`F32`、`F33`、`F44`、`F47`、`F48`；`G03`、`G05`
- **Acceptance**：
  - [ ] 新旧写端一次接入授权，旧 POST /api/status 无匿名写通道，旧只读响应保持兼容或明确版本错误。
  - [ ] 超过200条事件的 SSE 补拉无跳过；自动模式默认关闭，服务退出后 Runner 仍可由证据对账。
- **Steps**：
  1. 在应用组装层注册 API、授权引导和兼容映射。
  2. 核对所有历史写入口与旧 SSE frame，并验证静态导出不能发起受管操作。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/board ./internal/app -count=1
  node --test internal/board/page.test.cjs
  ```

- **Deliverables**：接通 HTTP 与旧看板兼容入口的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-956"></a>

### VN-956 接通扩展来源与观测能力开关

- **Goal**：在首个闭环验收后逐项开放有证据支持的扩展能力。
- **Non-goals**：不修改前端菜单，不替用户自动开启或真实派发任务。
- **Depends**：`VN-955`、`VN-207`、`VN-504`、`VN-505`、`VN-305`、`VN-306`、`VN-307`、`VN-308`、`VN-606`、`VN-807`
- **Surface / write_scope**：`cmd/maw/main.go`、`internal/app/runtime.go`、`internal/app/managed_cmd.go`、`internal/app/board_cmd.go`、`internal/app/extended_runtime_test.go`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui-data/`、`internal/app/**`、`internal/engine/**`、`internal/runner/**`、`internal/httpapi/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-003`、`VN-004`、`VN-005`、`VN-006`、`VN-022`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`WIRE` / `X3`；**maw**：priority=60 · merge_order=60 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F28`、`F29`、`F30`、`F31`、`F32`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`、`F47`；`G01`、`G04`、`G05`、`G07`、`G08`
- **Acceptance**：
  - [ ] 非 Git、自动匹配和其他来源各自受能力／工程策略限制，未验证或不支持能力无法被配置强行开启。
  - [ ] 提醒、观测 API 与投影水位可访问；关闭扩展仍保留人工和首个 CodeBuddy 闭环。
- **Steps**：
  1. 注册扩展实现和按证据分级的开关。
  2. 覆盖未就绪节点、平台拒绝、来源降级以及关闭扩展回归。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./internal/app -count=1
  ```

- **Deliverables**：接通扩展来源与观测能力开关的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-981"></a>

### VN-981 建立真实页面浏览器验收基座

- **Goal**：提供复用生产资源的前端行为和视觉证据环境。
- **Non-goals**：不引入生产框架，不用静态 HTML 图冒充真实页面验收。
- **Depends**：`VN-021`、`VN-022`
- **Surface / write_scope**：`tests/browser/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`internal/**`、`cmd/maw/**`、`clients/desktop/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`QUALITY` / `B0`；**maw**：priority=10 · merge_order=10 · write_scope 同上。
- **需求／门槛**：`F30`、`F31`、`F46`、`F48`；`UI-C`
- **Acceptance**：
  - [ ] 测试目录独立依赖与锁文件，固定浏览器版本，服务真实生产 asset 并通过测试边界注入 API／SSE 数据。
  - [ ] 基座可断言数据已加载、截图、键盘焦点和请求乱序；支持卡号 grep，报告不混入产品资源。
  - [ ] 测试发现规则覆盖各视图及公共组件目录内的 *.browser.spec.*；页面卡可在自身 Surface 内交付 grep 场景。
- **Steps**：
  1. 建立 Playwright 测试服务、统一 page 注册方式与截图输出路径。
  2. 验证基座能发现空白、未加载数据、焦点丢失和过期响应。
- **Verify**（本卡实现时执行）：

  ```sh
  npm ci --prefix tests/browser
  npm --prefix tests/browser exec -- playwright install chromium
  npm --prefix tests/browser test -- --grep 'VN-981'
  ```

- **Deliverables**：建立真实页面浏览器验收基座的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-982"></a>

### VN-982 验收首个 CodeBuddy 受管完整闭环

- **Goal**：用真实中枢二进制和隔离工程证明领取到验收的完整流程。
- **Non-goals**：不触及用户真实库和工作目录，不把模拟器证据写为厂商实测。
- **Depends**：`VN-955`、`VN-704`、`VN-405`、`VN-106`
- **Surface / write_scope**：`tests/managed/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`、`internal/**`、`cmd/maw/**`、`clients/desktop/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`、`VN-005`、`VN-021`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/ui/`。
- **Lane / 范围**：`QUALITY` / `C1`；**maw**：priority=20 · merge_order=20 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F30`、`F31`、`F32`、`F44`、`F45`、`F47`、`F48`；`G01`、`G02`、`G03`、`G04`、`G05`、`G06`、`G07`、`UI-C`
- **Acceptance**：
  - [ ] 临时库／Git／端口下完成 Probe→显式 Claim→Runner→Manifest→验收，退出码0而产物无效不能完成任务。
  - [ ] 窗口或中枢重启后恢复同一 Run；人工 next 仍可用，任务、Run、交付状态可分别核对。
- **Steps**：
  1. 构建真实 maw，使用受控 CLI 子进程与已标注来源的 CodeBuddy 协议样本验证主链和负例。
  2. 保留一次可复核的当前版本 CodeBuddy 最小真实运行证据；真实模型 smoke 不纳入默认 CI，按显式 opt-in 执行。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./tests/managed -count=1
  ```

- **Deliverables**：验收首个 CodeBuddy 受管完整闭环的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-983"></a>

### VN-983 验收迁移与故障恢复硬门槛

- **Goal**：以可重复故障注入证明旧进程、重试和占用不会造成重复执行或丢改动。
- **Non-goals**：不杀真实工具进程，不测试真实全局库，不以纯 mock 代替关键跨进程证据。
- **Depends**：`VN-982`
- **Surface / write_scope**：`tests/resilience/**`、`tests/testsupport/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`internal/**`、`cmd/maw/**`、`clients/desktop/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-001`、`VN-002`、`VN-003`、`VN-004`、`VN-005`；`docs/contracts/vnext/task/`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/delivery/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`。
- **Lane / 范围**：`QUALITY` / `C1`；**maw**：priority=30 · merge_order=30 · write_scope 同上。
- **需求／门槛**：`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F32`、`F44`、`F45`、`F47`；`G01`、`G02`、`G03`、`G04`、`G05`、`G06`、`G07`
- **Acceptance**：
  - [ ] 覆盖启动副作用前后中断、Runner 死亡、工具孤儿、取消竞争、SQLite 写等待、重复请求与迟到事件。
  - [ ] 覆盖 WAL 备份恢复、歧义旧编号、旧心跳、旧匿名写端和未知进程；未证实停止时不重派、不 reset 目录。
- **Steps**：
  1. 建立阶段故障点和受控进程／数据库样本。
  2. 输出 G01–G07 逐项证据及剩余 unknown 的人工恢复指引，首闭环通过后才准扩展依赖进入。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./tests/resilience -count=1
  ```

- **Deliverables**：验收迁移与故障恢复硬门槛的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-984"></a>

### VN-984 验收所有页面的视觉与交互一致性

- **Goal**：用同一矩阵证明新旧页面沿用 Workbench 设计和可靠数据状态。
- **Non-goals**：不通过更新所有截图掩盖回归，不重设计截图式蓝紫主题。
- **Depends**：`VN-705`、`VN-902`
- **Surface / write_scope**：`tests/browser/audit/**`、`docs/verification/vnext/frontend/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`、`internal/**`、`cmd/maw/**`、`clients/desktop/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-005`、`VN-006`、`VN-021`、`VN-022`；`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`docs/contracts/vnext/ui-data/`。
- **Lane / 范围**：`QUALITY` / `O2`；**maw**：priority=40 · merge_order=40 · write_scope 同上。
- **需求／门槛**：`F30`、`F31`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F46`、`F48`；`G05`、`G08`、`UI-C`
- **Acceptance**：
  - [ ] 覆盖桌面／窄屏、深浅／系统主题、zh-CN／en、长中文、空／载入／错误／unknown、断线和乱序响应。
  - [ ] 截图前断言数据与投影状态；验证键盘开关、Escape、焦点恢复、非颜色信息和 reduced-motion，并检查页面没有另造全局样式。
- **Steps**：
  1. 对所有注册页面运行相同场景，输出基线差异与 DOM 证据。
  2. 将生产样式或交互缺陷退回原 owner 修复，不在审计任务越界改页面。
- **Verify**（本卡实现时执行）：

  ```sh
  npm --prefix tests/browser test -- --grep 'VN-984'
  ```

- **Deliverables**：验收所有页面的视觉与交互一致性的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

<a id="vn-985"></a>

### VN-985 固化集成检查与平台支持矩阵

- **Goal**：将已验收闭环转为持续可执行的构建与回归门禁。
- **Non-goals**：不自动发布、不推 tag、不把跨编译成功标记为进程监督已支持。
- **Depends**：`VN-983`、`VN-984`、`VN-956`、`VN-902`
- **Surface / write_scope**：`.github/workflows/ci.yml`、`.github/workflows/release.yml`、`tests/platform/**`、`docs/verification/vnext/release/**`
- **Read scope**：`docs/ARCHITECTURE_VNEXT.zh-CN.md`、`docs/maw_integrated_feature_list.zh-CN.md`、`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`、`internal/**`、`cmd/maw/**`、`clients/desktop/**`；现有实现只读，不扩大 Surface。
- **Contract**：`VN-002`、`VN-004`、`VN-005`、`VN-006`、`VN-021`；`docs/contracts/vnext/execution/`、`docs/contracts/vnext/ports/`、`docs/contracts/vnext/http/`、`docs/contracts/vnext/observability/`、`docs/contracts/vnext/ui/`。
- **Lane / 范围**：`QUALITY` / `X3`；**maw**：priority=50 · merge_order=50 · write_scope 同上。
- **需求／门槛**：`F01`、`F02`、`F03`、`F04`、`F05`、`F06`、`F07`、`F08`、`F09`、`F10`、`F11`、`F12`、`F13`、`F14`、`F15`、`F16`、`F17`、`F18`、`F19`、`F20`、`F21`、`F22`、`F23`、`F24`、`F25`、`F26`、`F27`、`F28`、`F29`、`F30`、`F31`、`F32`、`F33`、`F34`、`F35`、`F36`、`F37`、`F38`、`F39`、`F40`、`F41`、`F42`、`F43`、`F44`、`F45`、`F46`、`F47`、`F48`；`G01`、`G02`、`G03`、`G04`、`G05`、`G06`、`G07`、`G08`、`UI-C`
- **Acceptance**：
  - [ ] CI 覆盖 Go 核心与跨进程集成、前端行为／浏览器、静态导出和桌面构建前置条件；不降低原有覆盖基线掩盖失败。
  - [ ] 跨编译与原生进程停止实测明确区分；macOS 之外未有原生证据的平台保持 managed 不支持，人工功能构建可验证。
- **Steps**：
  1. 按平台能力配置 CI 与构建检查，原生停止证据单独归档。
  2. 汇总 G01–G08／UI-C 门禁、支持矩阵和真实使用限制。
- **Verify**（本卡实现时执行）：

  ```sh
  CGO_ENABLED=0 go test ./...
  node --test internal/board/page.test.cjs
  npm --prefix tests/browser test
  CGO_ENABLED=0 go build -o /tmp/maw-vnext-release-check ./cmd/maw
  CGO_ENABLED=0 go test ./tests/platform -count=1
  ```

- **Deliverables**：固化集成检查与平台支持矩阵的实现／文档及针对验收边界的验证证据。Verify 的测试代码必须在本卡 Surface 内，或由其显式前置基座提供。

## 7. 需求覆盖与门槛证据

下表列出实现／接线 owner；契约、测试与文档覆盖另见 JSON 的 feature_ids。被标记“覆盖”表示规划有归属，不表示功能已经实现。

| 需求 | 功能 | 实现／接线任务 |
|---|---|---|
| F01 | 在统一入口登记、切换本机工程，关联仓库／任务目录、主干和工程配置 | `VN-102`、`VN-201`、`VN-206`、`VN-602`、`VN-603`、`VN-703`、`VN-704`、`VN-705`、`VN-725`、`VN-801`、`VN-951`、`VN-954`、`VN-955`、`VN-956` |
| F02 | 查看单工程的任务、工作者、执行批次、交付结果与执行记录 | `VN-102`、`VN-201`、`VN-206`、`VN-602`、`VN-703`、`VN-704`、`VN-705`、`VN-725`、`VN-744`、`VN-801`、`VN-805`、`VN-951`、`VN-955`、`VN-956` |
| F03 | 跨工程总览，并按工程、工具来源、节点、状态与日期组合筛选；相关页面保留上下文 | `VN-102`、`VN-201`、`VN-206`、`VN-602`、`VN-703`、`VN-704`、`VN-705`、`VN-725`、`VN-801`、`VN-951`、`VN-954`、`VN-955`、`VN-956` |
| F04 | 通过界面、CLI 或蓝图建立任务，统一记录目标、输入、优先级、读写范围、验收条件和验证方式 | `VN-102`、`VN-201`、`VN-206`、`VN-603`、`VN-722`、`VN-901`、`VN-951`、`VN-955` |
| F05 | 批量导入蓝图，校验必需字段、依赖和范围；增量更新时保留执行中及已完成任务 | `VN-102`、`VN-201`、`VN-206`、`VN-603`、`VN-722`、`VN-901`、`VN-951`、`VN-955` |
| F06 | 配置前置依赖与顺序，在依赖拓扑中查看可并行任务和受阻任务 | `VN-102`、`VN-201`、`VN-206`、`VN-603`、`VN-722`、`VN-901`、`VN-951`、`VN-955` |
| F07 | 支持代码任务及有明确产物的文档／调研任务；按任务类型选择工作目录和验收方式 | `VN-102`、`VN-201`、`VN-206`、`VN-504`、`VN-505`、`VN-603`、`VN-725`、`VN-901`、`VN-951`、`VN-953`、`VN-955` |
| F08 | 统一登记 WorkBuddy、WorkBuddy AI、CodeBuddy CN、Trae CN、ZCode 等本机执行节点；同一接入器可管理多个实例 | `VN-103`、`VN-201`、`VN-302`、`VN-305`、`VN-306`、`VN-307`、`VN-602`、`VN-603`、`VN-704`、`VN-721`、`VN-801`、`VN-952`、`VN-954`、`VN-955`、`VN-956` |
| F09 | 检查入口路径、版本、运行时、认证和模型就绪情况，明确提示需安装、需登录、需选择模型或探测失败 | `VN-103`、`VN-201`、`VN-302`、`VN-303`、`VN-305`、`VN-306`、`VN-307`、`VN-602`、`VN-603`、`VN-704`、`VN-721`、`VN-801`、`VN-952`、`VN-954`、`VN-955`、`VN-956` |
| F10 | 展示节点支持的能力：人工接入、自动启动、事件回传、取消、恢复、轮次或用量；不支持与未知分别显示 | `VN-103`、`VN-201`、`VN-302`、`VN-303`、`VN-305`、`VN-306`、`VN-307`、`VN-308`、`VN-602`、`VN-603`、`VN-704`、`VN-721`、`VN-801`、`VN-952`、`VN-954`、`VN-955`、`VN-956` |
| F11 | 分别展示在线情况、工作任务、空闲容量与最近心跳，设置是否接收新任务及并发槽数 | `VN-103`、`VN-201`、`VN-207`、`VN-302`、`VN-602`、`VN-603`、`VN-704`、`VN-721`、`VN-725`、`VN-801`、`VN-952`、`VN-954`、`VN-955`、`VN-956` |
| F12 | 保留客户端主动领取任务的方式，兼容现有 Codex、Cursor 等客户端通过统一协议领取和上报 | `VN-104`、`VN-201`、`VN-203`、`VN-604`、`VN-704`、`VN-722`、`VN-901`、`VN-952`、`VN-954`、`VN-955`、`VN-956` |
| F13 | 选择具体任务与节点派发；当节点已就绪且支持自动启动时，由接入器启动对应 CLI／控制入口 | `VN-104`、`VN-201`、`VN-203`、`VN-204`、`VN-303`、`VN-305`、`VN-306`、`VN-307`、`VN-402`、`VN-601`、`VN-604`、`VN-703`、`VN-704`、`VN-722`、`VN-954`、`VN-955`、`VN-956` |
| F14 | 按任务要求、节点能力、可用容量及用户偏好自动选择执行节点；不满足条件时说明排队原因 | `VN-104`、`VN-201`、`VN-203`、`VN-207`、`VN-604`、`VN-704`、`VN-722`、`VN-725`、`VN-954`、`VN-955`、`VN-956` |
| F15 | 人工领取与自动派发使用同一队列、依赖规则及写盘冲突检查，避免重复领取与跨入口绕过约束 | `VN-103`、`VN-104`、`VN-201`、`VN-203`、`VN-207`、`VN-604`、`VN-704`、`VN-722`、`VN-954`、`VN-955`、`VN-956` |
| F16 | 无稳定自动入口的客户端继续支持人工启动、复制任务上下文、进度上报和统一交付 | `VN-104`、`VN-201`、`VN-203`、`VN-305`、`VN-306`、`VN-307`、`VN-604`、`VN-605`、`VN-704`、`VN-722`、`VN-802`、`VN-901`、`VN-952`、`VN-954`、`VN-955`、`VN-956` |
| F17 | 每次执行有独立批次，记录对应任务实例、节点、工作者、工作目录、启动时间和结束原因 | `VN-104`、`VN-201`、`VN-202`、`VN-204`、`VN-303`、`VN-304`、`VN-401`、`VN-402`、`VN-404`、`VN-602`、`VN-604`、`VN-704`、`VN-723`、`VN-801`、`VN-952`、`VN-954`、`VN-955` |
| F18 | 查看启动中、执行中、等待用户／授权、完成、失败、取消和情况未知等运行状态；业务任务状态独立保留 | `VN-104`、`VN-201`、`VN-202`、`VN-204`、`VN-205`、`VN-304`、`VN-404`、`VN-604`、`VN-704`、`VN-723`、`VN-952`、`VN-954`、`VN-955` |
| F19 | 在支持的节点上取消任务运行、设置超时；确认进程停止和资源释放，再复用目录或执行槽 | `VN-104`、`VN-201`、`VN-202`、`VN-204`、`VN-205`、`VN-304`、`VN-402`、`VN-403`、`VN-404`、`VN-601`、`VN-604`、`VN-703`、`VN-704`、`VN-723`、`VN-952`、`VN-953`、`VN-954`、`VN-955` |
| F20 | 分类展示启动失败、认证问题、限流、执行失败、验收失败、断联等原因，提供对应处理入口 | `VN-104`、`VN-201`、`VN-202`、`VN-204`、`VN-205`、`VN-304`、`VN-305`、`VN-306`、`VN-307`、`VN-401`、`VN-403`、`VN-404`、`VN-405`、`VN-602`、`VN-604`、`VN-704`、`VN-723`、`VN-801`、`VN-806`、`VN-902`、`VN-952`、`VN-954`、`VN-955` |
| F21 | 保留每次失败与重试记录；符合策略时重试，支持的工具可恢复明确会话；中枢重启或派发结果不确定时先核实原执行 | `VN-104`、`VN-201`、`VN-202`、`VN-204`、`VN-205`、`VN-207`、`VN-308`、`VN-403`、`VN-404`、`VN-405`、`VN-604`、`VN-704`、`VN-723`、`VN-952`、`VN-954`、`VN-955` |
| F22 | Git 工程按工作者／执行槽分配并复用独立 worktree，记录任务分支、基线和目录 | `VN-103`、`VN-201`、`VN-203`、`VN-501`、`VN-504`、`VN-704`、`VN-952`、`VN-954`、`VN-955` |
| F23 | 同一目录只允许一个写入执行；文档属于仓库时沿用仓库隔离，独立非 Git 任务使用专用任务目录 | `VN-103`、`VN-201`、`VN-203`、`VN-205`、`VN-402`、`VN-403`、`VN-405`、`VN-501`、`VN-504`、`VN-704`、`VN-723`、`VN-952`、`VN-954`、`VN-955` |
| F24 | 执行前检查输入、目录、能力和验收方式是否齐备；提交时核对实际改动与声明范围，超出范围可追溯 | `VN-103`、`VN-201`、`VN-203`、`VN-303`、`VN-501`、`VN-502`、`VN-503`、`VN-504`、`VN-704`、`VN-901`、`VN-953`、`VN-954`、`VN-955` |
| F25 | 查看与打开工作目录、任务上下文及保留结果；已有 worktree 清理方式继续可用 | `VN-103`、`VN-201`、`VN-501`、`VN-502`、`VN-504`、`VN-704`、`VN-901`、`VN-953`、`VN-954`、`VN-955`、`VN-807` |
| F26 | 统一收取结果摘要、产物位置、代码改动／提交、验证结果、运行输出与失败原因，关联任务和执行批次 | `VN-105`、`VN-201`、`VN-304`、`VN-401`、`VN-404`、`VN-502`、`VN-505`、`VN-604`、`VN-704`、`VN-724`、`VN-901`、`VN-953`、`VN-954`、`VN-955` |
| F27 | 代码任务继续经过范围检查、自检、评审、合入；非代码任务按产物和验收条件确认完成 | `VN-105`、`VN-201`、`VN-202`、`VN-503`、`VN-505`、`VN-601`、`VN-604`、`VN-704`、`VN-724`、`VN-901`、`VN-953`、`VN-954`、`VN-955` |
| F28 | 区分“运行结束”“交付待验收”“验收通过”“任务完成”，显示哪些条件未通过 | `VN-105`、`VN-201`、`VN-202`、`VN-205`、`VN-304`、`VN-503`、`VN-505`、`VN-604`、`VN-703`、`VN-704`、`VN-723`、`VN-724`、`VN-806`、`VN-606`、`VN-902`、`VN-953`、`VN-954`、`VN-955`、`VN-956` |
| F29 | 从任务进入本次及历史批次，打开产物、日志和验证证据，比较失败后重试的结果 | `VN-105`、`VN-201`、`VN-502`、`VN-503`、`VN-505`、`VN-602`、`VN-704`、`VN-723`、`VN-724`、`VN-743`、`VN-801`、`VN-953`、`VN-955`、`VN-956`、`VN-807` |
| F30 | 在一个工作台查看工程、节点、待执行／执行中／待验收任务、异常及下一步 | `VN-602`、`VN-701`、`VN-702`、`VN-704`、`VN-722`、`VN-723`、`VN-801`、`VN-955`、`VN-956` |
| F31 | 在看板、依赖拓扑和执行时间轴之间切换，保留筛选；从任务或执行条进入详情 | `VN-602`、`VN-701`、`VN-702`、`VN-704`、`VN-705`、`VN-722`、`VN-723`、`VN-741`、`VN-801`、`VN-955`、`VN-956` |
| F32 | 通过实时更新与断线后的补拉同步状态；同步失败保留最近有效信息，并区分显示过期状态 | `VN-101`、`VN-104`、`VN-404`、`VN-602`、`VN-605`、`VN-702`、`VN-703`、`VN-704`、`VN-723`、`VN-801`、`VN-802`、`VN-901`、`VN-952`、`VN-955`、`VN-956` |
| F33 | 对需要用户处理的等待、阻塞、失败及关键交付变化给出站内／桌面托盘提醒，可配置类型与去重 | `VN-107`、`VN-604`、`VN-605`、`VN-702`、`VN-704`、`VN-725`、`VN-806`、`VN-606`、`VN-902`、`VN-954`、`VN-955`、`VN-956` |
| F34 | 按日期查看工程活动热图，定位集中执行或事件活跃的时段 | `VN-107`、`VN-702`、`VN-705`、`VN-742`、`VN-803`、`VN-805`、`VN-606`、`VN-956` |
| F35 | 按小时展开工程、任务、工作者与执行批次，查看并行执行条与任务阶段驻留 | `VN-107`、`VN-702`、`VN-705`、`VN-741`、`VN-742`、`VN-803`、`VN-606`、`VN-956` |
| F36 | 同屏区分真实执行、明确等待、业务阻塞、阶段驻留和未知片段；输入节点仅在有观测事件时显示 | `VN-107`、`VN-308`、`VN-702`、`VN-705`、`VN-741`、`VN-803`、`VN-606`、`VN-956` |
| F37 | 下钻到已关联的会话与轮次，查看起止时间、状态、执行累计和整体跨度 | `VN-107`、`VN-308`、`VN-702`、`VN-705`、`VN-743`、`VN-803`、`VN-804`、`VN-606`、`VN-956` |
| F38 | 缩放时间轴、聚焦单次执行与前后切换时间段；所有历史回顾均为只读展示 | `VN-107`、`VN-702`、`VN-705`、`VN-741`、`VN-742`、`VN-743`、`VN-803`、`VN-606`、`VN-956` |
| F39 | 查看各工程任务与执行批次数、已验收／已合入成果、阻塞和失败重试情况 | `VN-107`、`VN-705`、`VN-744`、`VN-805`、`VN-606`、`VN-956` |
| F40 | 展示已观测执行时长累计、活跃时间覆盖和按小时的分布，并与任务阶段驻留分开 | `VN-107`、`VN-705`、`VN-742`、`VN-744`、`VN-803`、`VN-805`、`VN-606`、`VN-956` |
| F41 | 对比工具来源的任务量、执行量与已知消耗，明确每张图的单位和覆盖范围 | `VN-107`、`VN-705`、`VN-742`、`VN-744`、`VN-805`、`VN-606`、`VN-956` |
| F42 | 查看已知输入／输出／缓存 Token、缺失情况与采集覆盖率，下钻到执行批次或轮次 | `VN-107`、`VN-308`、`VN-705`、`VN-743`、`VN-744`、`VN-804`、`VN-805`、`VN-606`、`VN-956` |
| F43 | 展示活跃期间平均并发及同一口径下的工程／日期比较；定位长时间停留、反复失败和依赖积压 | `VN-107`、`VN-705`、`VN-744`、`VN-803`、`VN-805`、`VN-606`、`VN-956` |
| F44 | 每条运行记录可追到来源、任务实例、执行批次、事件时间和验收证据；同一显示编号重建后历史互不混淆 | `VN-101`、`VN-102`、`VN-104`、`VN-105`、`VN-106`、`VN-107`、`VN-201`、`VN-206`、`VN-304`、`VN-401`、`VN-404`、`VN-405`、`VN-502`、`VN-602`、`VN-605`、`VN-705`、`VN-743`、`VN-801`、`VN-802`、`VN-803`、`VN-804`、`VN-901`、`VN-951`、`VN-952`、`VN-953`、`VN-955`、`VN-956`、`VN-807` |
| F45 | 处理重复、迟到与乱序事件，分页补齐历史，支持跨小时／跨日统计；缺失边界显式标记 | `VN-101`、`VN-102`、`VN-104`、`VN-106`、`VN-107`、`VN-401`、`VN-405`、`VN-605`、`VN-705`、`VN-801`、`VN-802`、`VN-803`、`VN-804`、`VN-901`、`VN-951`、`VN-952`、`VN-953`、`VN-956`、`VN-807` |
| F46 | 显示数据为实测、估算或未知，以及支持的来源范围；不完整记录不伪造完整统计 | `VN-107`、`VN-304`、`VN-308`、`VN-401`、`VN-602`、`VN-605`、`VN-702`、`VN-705`、`VN-723`、`VN-741`、`VN-742`、`VN-743`、`VN-744`、`VN-801`、`VN-802`、`VN-803`、`VN-804`、`VN-805`、`VN-606`、`VN-956`、`VN-807` |
| F47 | 配置接入方式、节点容量、超时／重试、通知和数据保留；日志排除凭据，按需开启更详细采集 | `VN-106`、`VN-107`、`VN-206`、`VN-207`、`VN-302`、`VN-401`、`VN-601`、`VN-603`、`VN-703`、`VN-705`、`VN-721`、`VN-725`、`VN-806`、`VN-606`、`VN-901`、`VN-902`、`VN-954`、`VN-955`、`VN-956`、`VN-807` |
| F48 | 保留中英双语、主题、键盘访问及浏览器／桌面共用业务界面，本机存储和运行 | `VN-701`、`VN-702`、`VN-703`、`VN-704`、`VN-705`、`VN-721`、`VN-722`、`VN-723`、`VN-724`、`VN-725`、`VN-741`、`VN-742`、`VN-743`、`VN-744`、`VN-902`、`VN-955` |

| 门槛 | 首要证明任务 | 必须证明 |
|---|---|---|
| G01 | VN-103／203／403／501／952／983 | 旧心跳不释放受管执行，槽与目录占用有唯一身份 |
| G02 | VN-102／106／951／983 | 稳定实例、歧义历史、WAL 一致备份及旧写者停写窗口 |
| G03 | VN-104／203／801／955／983 | 原子状态＋事件、并发领取、写失败与完整分页 |
| G04 | VN-304／502／503／953／982 | 来源结束可识别，交付证据与任务完成分别成立 |
| G05 | VN-601／603／604／955／983 | 新旧写端、同源 CSRF、CLI 令牌与对象归属完整 |
| G06 | VN-402／403／405／983／985 | 原生平台进程身份与停止证据，unknown 不复用 |
| G07 | VN-301／303／304／305–308／982 | 按工具版本逐项验证，不把未支持能力宣称已支持 |
| G08 | VN-006／803–805／807／984 | 区间／用量来源、去重、覆盖率、缺测与保留语义 |
| UI-C | VN-021／022／702／703／981／984 | 公共设计、上下文、双语主题及全页交互一致性 |

## 8. 本轮清单自检

- 共 81 题、15 Lane；任务 ID 唯一，依赖均存在，依赖图无环。
- 检出 37 对 Surface 交叠，均有直接或传递依赖串行；未排序冲突为 0。交叠明细见[审查报告](planning/maw-vnext-task-audit.md)。
- 首波 3 条不同 Lane、3 题均无前置且写盘不重叠，匹配规划容量3；不把未来人工开工等同于本轮已启动。
- 所有卡具备 Goal、Non-goals、Deps、Surface、Contract、Steps、Acceptance、Verify、Deliverables、Lane 与 maw 映射。F01–F48 均有实现／接线归属，G01–G08 与 UI-C 均有验证归属。
- 首闭环依赖闭包没有 O2／X3 卡；来源能力和平台停止证据未通过时保持相应能力关闭。此静态检查不证明实现质量、真实工期或厂商能力。
- 本轮仅生成规划文档并检查其字段、DAG、写盘冲突和覆盖关系；未运行任何业务测试、厂商模型任务、迁移、安装、领取或派发。

后续进入执行时，先按已批准的工作区基线接入 maw，再使用 `maw-plan-push` 将本清单映射到蓝图；不会因为本清单已存在而自动执行。
