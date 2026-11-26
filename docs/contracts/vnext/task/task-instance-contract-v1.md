# 任务实例与版本契约 v1（冻结）

契约标识：`maw.task.instance/v1`
状态：**冻结**。本文件是 VN-001 的交付物；后续 CONTRACT 题（VN-002 起）以它为引用基线，不得改写本文含义，只能新增下一版契约。
范围：任务身份、定义版本、退役归档、依赖解析的**唯一关系**。

## 0. 为什么需要这份契约

现状有三处身份混用（`internal/db/db.go:33`、`internal/app/plan_cmd.go:134`、`internal/schedule/schedule.go:41`）：

1. `tasks` 的主键是 `(project_id, id)`，`id` 同时承担「数据库主键」和「给人看的显示编号」两种用途。显示编号一旦被重新建题就会撞车，历史无法区分。
2. `deps_json` 里存的是显示编号字符串，`maw next` 用 `status == "done"` 做字符串匹配。前置被 `plan apply` 软删成 `cancelled` 后，依赖**永久不可满足**，界面上只显示「等前置」，看不出是死锁。
3. 蓝图改一道题的定义时，`mergePlanFields` 直接覆盖同一行。已经按旧定义启动的执行，拿不到自己当时冻结的是什么。

本契约把这三件事拆成三种身份，并规定各自的唯一性与不可变性。

## 1. 四种身份，各管一件事

| 身份 | 载体 | 唯一性 | 可变性 | 谁在用 |
| --- | --- | --- | --- | --- |
| `instance_id` | `ti_` 前缀 + 小写字母数字 | 全局唯一，**永不复用** | 不可变 | 新 Run、版本、依赖、事件 |
| `display_id` | 如 `VN-001` | 同一工程内**至多一个活跃实例**持有 | 随退役/重建转移 | 人读、CLI 参数、看板 |
| 旧物理主键 | `(project_id, display_id)` | 由当前 `tasks` 行持有 | 保留不动 | 现有 SQL、旧只读 API |
| `version_no` | 整数，从 1 连续递增 | 实例内唯一 | 已冻结版本不可改 | Run 冻结输入、验收口径 |

一条硬规则：**`instance_id` 与 `display_id` 属于两个命名空间**。`instance_id` 不能写成 `VN-001` 的样子（E111），否则两套标识会在日志和 API 里重新糊在一起。

## 2. 快照结构

校验对象是一份「任务身份快照」JSON（正反例见 `samples/`）。它描述某工程在某一时刻的实例、版本与退役状态，不描述运行态。

```jsonc
{
  "contract": "maw.task.instance/v1",
  "project_id": "716c30878ba1b95f",
  "generated_at": "2026-10-23T02:55:42Z",
  "instances": [
    {
      "instance_id": "ti_vn001a",
      "display_id": "VN-001",
      "state": "active",                 // active | retired
      "created_at": "2026-10-23T02:50:00Z",
      "retired_at": null,                // state=retired 时必填
      "retired_reason": null,            // state=retired 时必填
      "active_version": 1,               // active 必填，retired 必须为 null
      "versions": [
        {
          "version_no": 1,
          "frozen_at": "2026-10-23T02:50:00Z",
          "title": "冻结任务实例与版本契约",
          "deps": [{ "kind": "instance", "target": "ti_xxx" }]   // 或 kind=legacy, target=VN-005
        }
      ],
      "legacy": {
        "project_id": "716c30878ba1b95f",
        "display_id": "VN-001",
        "primary_key": "716c30878ba1b95f/VN-001",
        "ambiguous": false,
        "ambiguity_note": null           // ambiguous=true 时必填
      }
    }
  ]
}
```

`legacy` 块是**强制**的：每个实例都必须能摊回 `tasks` 的物理主键，否则重建对不上号（E160）。

## 3. 状态权威

- **当前状态只由 `tasks` 持有**。`task_instances`（未来表）只存身份与退役快照，不是第二份可变状态表。
- 活跃实例与 `tasks` 行**一一对应**：`legacy.primary_key` 即那行的主键。实例创建、别名建立、退役快照、别名移除必须在同一事务完成。
- 退役后该实例的 `versions` 立即变为只读快照：`retired_at` 之后不得再出现新版本（E142）。
- 同一 `display_id` 允许「1 个活跃实例 + N 个退役锚点」。物理主键由活跃实例持有；退役锚点只保存快照，不与 `tasks` 争主键。

### 3.1 与现有 `plan apply` 的映射

现状的三种去向，在本契约里的落点：

| 现实现（`internal/app/plan_cmd.go`） | 契约语义 |
| --- | --- |
| 新题插入（`:158`） | 创建**新** `instance_id`，`version_no=1` |
| 已有 idle 题被蓝图覆盖（`:164`） | 同实例**冻结新版本**，`active_version` 前进；旧版本保留 |
| 保留态题只刷计划字段（`:147`） | 同上，但状态/领取字段不动 |
| 蓝图外的 idle 题软删为 `cancelled` + `removed_from_plan`（`:183`） | 实例**退役**，`retired_reason=removed_from_plan` |
| `--reset` 清空重建（`:120`） | 全部旧实例退役为快照，新实例一律新 `instance_id` |

**取消 ≠ 退役**：`cancelled` 是业务取消（人决定不做了），`retired` 是身份归档（这道题从计划里退场）。两者都不可再被依赖满足（见 §5），但退役必须保留快照。

## 4. 版本规则

- `version_no` 从 1 连续递增，不允许跳号或重复（E131）。跳号意味着历史被静默删除过，界面上会出现「v1、v3」却不说明 v2 去哪了。
- `frozen_at` 严格递增（E133）。
- 活跃实例的 `active_version` **必须等于已冻结的最大版本号**（E132）。允许回退指针会让同一个任务在不同时刻读两套验收，已经启动的 Run 与后来的 Run 无法比较。
- 已启动的 Run 冻结具体 `version_no`；蓝图后续更新只产生新版本，不改旧版本内容。

## 5. 依赖解析

解析顺序固定，不得跳步：

1. `kind = "instance"`：按 `instance_id` 精确匹配。找不到 → **E150**；命中已退役实例 → **E151**；命中自身 → **E152**。
2. `kind = "legacy"`：按 `display_id` 在当前工程内匹配。零个 → **E150**；多个 → **E153**（必须改用 instance 形式消歧，**不允许取第一条**）；唯一命中但已退役 → **E151**。
3. 新写入的依赖一律用 `instance` 形式；`legacy` 只用于读取存量 `deps_json`。

这条规则直接对上现状缺陷：软删前置后 `deps` 永远不会被满足，而调度只看 `status == "done"`。契约要求把「前置已退役」从「还没做完」里分出来——前者是**结构性死锁**，必须在 `plan apply` 时就报出来并要求改依赖，而不是让 `next` 永远返回「no ready tasks」。

## 6. 不变量登记表

机检主体是 `task_contract.py`；本表由 `test_task_contract.py` 强制与代码对齐（多一条、少一条都失败）。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| E100 | 全文档 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| E101 | 根节点 | `contract` 必须是 `maw.task.instance/v1` |
| E110 | `instance_id` | 同一快照内不得重复 |
| E111 | `instance_id` | 必须形如 `ti_<小写字母数字，至少 6 位>`，不得占用显示编号形态 |
| E112 | `display_id` | 必须形如 `VN-001`：大写前缀 + 连字符 + 数字 |
| E120 | `display_id` | 同一工程内至多一个 `active` 实例持有该编号 |
| E130 | `active_version` | active 实例必须指向一个已冻结的 `version_no` |
| E131 | `version_no` | 必须从 1 开始连续递增，不跳号不重复 |
| E132 | `active_version` | 不得回退，必须等于已冻结的最大版本号 |
| E133 | `frozen_at` | 版本冻结时间必须严格晚于上一版本 |
| E140 | retired 实例 | 必须有 `retired_at` 与 `retired_reason`，且 `active_version` 为 null |
| E142 | retired 实例 | 退役时间之后不得出现新版本 |
| E150 | `deps` | `target` 在本工程快照内必须存在 |
| E151 | `deps` | 不得依赖已退役实例（不可满足） |
| E152 | `deps` | 不得依赖自身实例 |
| E153 | `deps` | legacy 形式命中多个实例时必须消歧 |
| E160 | `legacy` | 必须存在，且 `primary_key == project_id + "/" + display_id` |
| E161 | `legacy` | `ambiguous=true` 的实例不得为 active，且必须写明 `ambiguity_note` |

## 7. 正反例与校验

正例（`samples/valid/`）：

| 文件 | 覆盖场景 |
| --- | --- |
| `01-minimal.json` | 单实例单版本的最小合法快照 |
| `02-version-history.json` | 活跃定义更新：追加 v2、保留 v1、指针前进 |
| `03-display-id-rebuild.json` | 编号重建：旧实例退役后 `VN-005` 由**新** `instance_id` 占用 |
| `04-ambiguous-legacy.json` | 歧义历史：只作退役锚点，不并入当前活跃实例 |
| `05-deps-legacy-unique.json` | 存量 `deps_json` 编号在唯一命中时按 legacy 解析 |

反例（`samples/invalid/`，每个文件用 `expect_error` 声明应触发的码）：别名冲突、退役仍留活跃指针、版本跳号、悬空依赖、依赖退役前置、旧主键错位、`instance_id` 复用、活跃指针悬空、歧义历史充当活跃别名、版本回退、legacy 依赖歧义、退役后再冻结、自依赖。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/task -p 'test_*.py'
```

## 8. 本契约不做的事

- 不迁移真实状态库，不建表，不改 `internal/db`、`internal/app`、`internal/schedule`。
- 不定义 Run / Operation / 资源占用（VN-002）、交付与验收（VN-003）。
- 不规定 `instance_id` 的具体生成算法，只规定它的形态与不可复用性。
- 不承诺 `tasks` 物理主键会被重建；`tasks` 继续以 `(project_id, id)` 承载当前行。
