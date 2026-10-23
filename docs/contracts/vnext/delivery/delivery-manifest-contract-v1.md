# 交付与验收契约 v1（冻结）

契约标识：`maw.delivery.manifest/v1`
上游契约：`maw.task.instance/v1`（`../task/`，任务身份与版本）、`maw.runtime.state/v1`（`../execution/`，运行与占用）——本文件只谈「这次执行交出来的东西算不算完成」。
状态：**冻结**。改含义只能出 v2。

范围：产物候选 → 冻结结果 → 验证结论 → 评审与合入 → 完成判定，以及它们之间的证据关系。**不改 `maw submit` / `maw merge`，不运行任何项目验证命令。**

## 0. 为什么需要这份契约

现状的完成判定靠三样东西拼起来，每样都不足以支撑 `done`：

1. **退出码**。`internal/app/worker_cmd.go` 的提交只关心「有没有 git 改动」，进程退出码 0 就被当作正常结束。但本机历史测试里 CodeBuddy 认证失败也可能退出 0——退出码不是结果证据。
2. **worktree 路径**。`tasks.worktree` 记的是目录，不是产物；下一题一 reset，待验收的东西就没了。
3. **人的记忆**。「我验过了」没有留下对哪一版定义、哪一个提交、哪一份产物的绑定。

契约把「交付」变成一个带身份的物件：Manifest 绑定任务版本与 Run，产物带哈希与范围，验证结论绑定被测对象，完成判定只能从这些证据推导。

## 1. 三条硬规则（先看这里）

- **退出码 0 不能产生 done。** 它只能产生 `pending`（E333）。
- **产物查不到实体不能产生 done。** `exists=false` 的引用不算数（E320、E341）。
- **旧提交上的验证不能给新提交背书。** 验证目标提交必须等于候选提交（E331）；产物哈希变了，旧验证对当前产物无效（E334）。

这三条正是本卡验收里的三个反例。

## 2. Manifest 字段

```jsonc
{
  "contract": "maw.delivery.manifest/v1",
  "project_id": "716c30878ba1b95f",
  "deliveries": [
    {
      "delivery_id": "dlv_001",
      "task_instance": "ti_vn003c",   // 绑定实例（VN-001）
      "task_version": 1,              // 绑定定义版本（VN-001）
      "run_id": "run_a001",           // 绑定产生它的运行（VN-002）
      "mode": "code",                 // code | non_code
      "baseline_commit": "dedc919",   // 基线
      "candidate_commit": "abc1234",  // 候选提交（non_code 为 null）
      "delivered_at": "2026-10-23T07:09:00Z",
      "artifacts": [
        { "artifact_id": "art_001", "kind": "commit", "ref": "abc1234",
          "declared_by": "manual", "exists": true, "in_write_scope": true },
        { "artifact_id": "art_002", "kind": "file", "ref": "docs/x.md",
          "declared_by": "cli", "exists": true, "in_write_scope": true,
          "sha256": "<64 hex>" }
      ],
      "verification": [
        { "verification_id": "vfy_001", "command": "go test ./...",
          "target_task_version": 1, "target_commit": "abc1234",
          "target_artifact_id": null, "target_artifact_sha256": null,
          "result": "pass", "evidence": ["exit_code:0", "go test: 12 packages ok"],
          "ran_at": "2026-10-23T07:10:00Z" }
      ],
      "review": { "required": true, "verdict": "approved", "reviewer": "r1", "reviewed_at": "..." },
      "merge":  { "required": true, "merged": true, "merged_commit": "def5678", "merged_at": "..." },
      "completion": { "decision": "done", "decided_at": "...", "reason": "" },
      "supersedes": null,
      "superseded_by": null
    }
  ]
}
```

必含且不可省：`task_instance`、`task_version`、`run_id`、`baseline_commit`、`mode`、`completion`。少任何一项都不构成可判定交付（E310–E313）。

## 3. 产物：候选不是事实

- `declared_by` 区分来源：`cli` 表示「工具自己说产出了」，`manual` 表示「人确认过」。**CLI 声明只是候选**。
- 候选要变成交付的一部分，必须由 maw 侧核实：`exists`（实体真的在）、`in_write_scope`（没越出本卡写盘范围）、`sha256`（文件类产物的内容哈希）。
- `exists=false` → 该产物不计入（E320）；`in_write_scope=false` → 越界（E321）；两者都不允许出现在 `done` 的交付里。
- 只记一个会被下一题重置的 worktree 路径不算产物引用。

## 4. 验证证据：分等级，不做模糊判定

| `result` | 含义 | 能否支撑 done |
| --- | --- | --- |
| `pass` | 机器验证通过，且带结果类证据 | 是 |
| `manual` | 人工/机器验收结论 | 仅非代码任务（E350 路径） |
| `fail` | 验证失败 | 否（且 done 时不允存在，E361） |
| `pending` | 跑了但结论未定 | 否 |
| `stale` | 曾通过，但产物已变，对当前产物失效 | 否 |

「结果类证据」= `evidence` 里至少有一条不是 `exit_code:` 前缀的记录（例如测试汇总、断言输出、产物清单、评审意见）。**只有 `exit_code:0` 一律视为无证据**（E333）。

每条验证必须绑定：`target_task_version`（等于交付的 `task_version`，否则 E330）、`target_commit`（代码任务等于候选提交，否则 E331）、必要时 `target_artifact_id` + `target_artifact_sha256`（与当前产物哈希一致，否则 E334）。

## 5. 完成条件：代码与非代码分开定义

| | 代码任务 | 非代码任务 |
| --- | --- | --- |
| 产物 | 至少一个（提交或文件，E341） | 至少一个（文件/哈希） |
| 验证 | 至少一条有效 `pass`（E340） | 至少一条 `manual` 且评审 `approved`（E350） |
| 评审 | `required=true` 时必须 `approved`（E343） | 必须有验收结论（人工或机器） |
| 合入 | 必须 `merged=true`（E342），不得声明「不需要合入」（E351） | 不得记录合入（E351） |
| 结论文档 | 代码任务以「验收 + 合入」为完成 | 代码外交付以「明确验收通过」为完成，并记录完成方式 |

- `done` 需要上述条件同时成立；任一缺失，结论只能是 `pending`。
- `rejected` 必须写 `reason`（E360），否则返工方无据可依。
- `done` 时不允许同时存在 `fail` 验证（E361）——失败项必须先修复或显式降级。

## 6. 重复交付与产物变更

**重复交付**：同一任务实例可以多次交付。新交付用 `supersedes` 指向被取代的那次，旧交付写 `superseded_by`。

- 被取代的交付不得保留 `done`（E370）——否则同一任务会有两个完成结论。它应退为 `pending`（等待被取代）或 `rejected`。
- `supersedes` 必须指向快照内存在的其它交付，不得自指（E371）。
- 新交付重新走完整链路，不继承旧交付的验证结论。

**产物变更**：

- 产物内容变了，针对旧哈希的验证立即失效；把它的 `result` 改为 `stale`，并针对新哈希重新验证（正例 `04`）。
- 不允许把旧 `pass` 原样留在新交付里充数（反例 `10`，E334）。

## 7. 评审与合入策略

- `review.required=false` 表示这类交付不需要评审（例如纯文档）；`verdict=not_required` 与之配套。
- 合入策略沿用现有串行合入，本契约只记录结论（`merged`、`merged_commit`、`merged_at`），不定义合入算法。
- **派发不等于授权自动合入**：自动执行与自动合入分离，后者沿用显式配置。

## 8. 不变量登记表

机检主体是 `delivery_contract.py`；本表由 `test_delivery_contract.py` 强制对齐，且**每条都必须有反例触发**。

| 码 | 判定对象 | 不变量 |
| --- | --- | --- |
| E300 | 全文档 | 必填字段存在且类型正确；时间可解析为 ISO8601 |
| E301 | 根节点 | `contract` 必须是 `maw.delivery.manifest/v1` |
| E310 | 交付 | 必须绑定任务实例 |
| E311 | 交付 | 必须绑定任务定义版本 |
| E312 | 交付 | 必须绑定产生它的运行 |
| E313 | 提交引用 | 基线与候选提交必须是合法的提交哈希（7–40 位十六进制） |
| E320 | 产物 | 声明了产物就必须能查到实体 |
| E321 | 产物 | 不得越出本卡写盘范围 |
| E330 | 验证 | 目标任务版本必须等于交付版本 |
| E331 | 验证 | 代码任务的通过结论必须针对候选提交，不能是旧提交 |
| E332 | 验证 | 判为通过必须带证据 |
| E333 | 验证 | 只有退出码不算通过 |
| E334 | 验证 | 产物已变更时旧验证对当前产物无效 |
| E340 | 完成 | `done` 必须有一条有效通过的验证 |
| E341 | 完成 | `done` 必须至少有一个产物 |
| E342 | 完成 | 代码任务未合入不得 `done` |
| E343 | 完成 | 代码任务评审未通过不得 `done` |
| E350 | 完成 | 非代码任务缺人工/机器验收不得 `done` |
| E351 | 完成 | 完成方式必须与任务模式匹配（代码必须合入、非代码不得记合入） |
| E360 | 完成 | `rejected` 必须写明原因 |
| E361 | 完成 | `done` 时不得存在未处理的 `fail` 验证 |
| E370 | 重复交付 | 已被取代的交付不得保留 `done` |
| E371 | 重复交付 | `supersedes` 不得指向不存在或自身 |

## 9. 正反例

正例（`samples/valid/`）：

| 文件 | 覆盖现场 |
| --- | --- |
| `01-code-delivery-done.json` | 代码交付：提交 + 文件产物、结果类证据、评审通过、已合入 → done |
| `02-non-code-manual-acceptance.json` | 非代码交付：人工验收通过、不走合入 → done |
| `03-redelivery-supersedes.json` | 重复交付：旧交付退为 pending 并被取代，新交付重新验证 → done |
| `04-artifact-modified-reverify.json` | 产物被修改：旧验证降级 stale，针对新哈希重验 → done |
| `05-verification-failed-pending.json` | 验证失败 → 只能 pending |
| `06-exit-code-only-pending.json` | 只有退出码 0 → 只能 pending，不判完成 |
| `07-merge-conflict-rejected.json` | 合入冲突 → rejected 并写明原因 |

反例（`samples/invalid/`，各用 `expect_error` 声明应触发的码）：缺实例/版本/运行绑定、提交写成别名、产物不存在、产物越界、验证版本错位、旧提交验证、无证据通过、仅退出码通过、产物变更仍用旧结论、无通过验证就 done、无产物就 done、未合入就 done、评审未过就 done、非代码无验收、非代码记合入、代码声明不需合入、拒绝无原因、done 仍有失败验证、被取代仍 done、取代指向不存在/自身、错误契约版本。

验证命令（本卡 Verify）：

```sh
python3 -m unittest discover -s docs/contracts/vnext/delivery -p 'test_*.py'
```

## 10. 本契约不做的事

- 不改 `maw submit` / `maw merge` 的实现，不运行项目验证命令。
- 不定义接入器如何解析厂商结果（VN-301 起）。
- 不定义应用端口与事务边界（VN-004）。
- 不规定评审人产生方式与合入算法。
