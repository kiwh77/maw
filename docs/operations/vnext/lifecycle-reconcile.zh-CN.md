# 生命周期变更与对账

调度侧（Lane、deps、write_scope、`next` 先决）是稳的；薄弱点在**生命周期变更**：中途改定义、作废题号、merge 后主检出、plan 片段、双配置。不要手改 `~/.maw/maw.db`——用本页命令。

## 五条纪律

1. **定义冻结**：`claim` 之后不改卡。要改 → `interrupt` → 交付／cancel → **开新题号**（勿指望升 `active_version` 能续交旧 Run）。
2. **作废即清理**：`maw task cancel <id>` 会级联收尾 Run／租约并删 worktree／题分支；若旧版本留下残留，再跑 `maw reconcile --apply`。
3. **merge 后先查索引**：合入前主检出干净时，`maw merge` 会自动 `reset --hard` 对齐新 tip；若合入前已有本地改动，看结果里的 `checkout_warning`，再 `git status --porcelain`。
4. **手动提交前核对**：`git diff --cached --stat` 必须与预期一致。「只 add 了一个文件」在非空索引上不成立。
5. **plan apply 只走完整集**：蓝图 `[[task]]` 是**工程级任务全集**；压片段会误退役蓝图外 idle。用 `maw plan append`／完整蓝图；确认退役 ≥2 道 idle 时加 `--allow-retire`。

## 对账出口

```bash
maw reconcile --json                 # 预览：version_drift / terminal_leak / orphan_lease / checkout_drift
maw reconcile --apply --json         # lead／system：释放残留租约、收尾幽灵 Run
maw reconcile --apply --sync-checkout # 另：主检出 tip 漂移可解释时 reset --hard
```

| finding | 含义 | `--apply` |
|---|---|---|
| `version_drift` | 占用中 Run 的 `version_no` ≠ `active_version`（E330 风险） | **不自动改**；开新题号 |
| `terminal_leak` | cancelled/done 仍占 Run／lease | 级联释放 |
| `orphan_lease` | 无任务行的有效租约 | 释放 |
| `checkout_drift` | 主检出在主干但与 HEAD 不一致 | 需 `--sync-checkout` |

## 配置路径（leads）

CLI **只读**仓外：

```bash
maw config path --json    # 打印生效的 ~/.maw/projects/<id>/config.toml
maw doctor --json         # issues 会提示仓内 .multiagent/config.toml 影子文件
```

改 `[identity] leads` 必须改上述路径；改仓内 `.multiagent/config.toml` **不生效**。

## 与旧手改库对照（已退役）

此前运维曾用 SQL 修：Run 版本、残留租约、`task_instances` 退役。现已由 `task cancel` 级联 + `reconcile --apply` 覆盖；**禁止**再直接改库。若 `version_drift` 仍在，唯一合法路径是新题号，不是改 `runs.version_no`。

## 相关

- [operations-guide.zh-CN.md](./operations-guide.zh-CN.md)
- [lead-self-heal.zh-CN.md](./lead-self-heal.zh-CN.md)（受管 Run 的 `run_reconcile`，与本页 CLI 对账互补）
