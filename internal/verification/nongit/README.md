# internal/verification/nongit

非代码产物验收策略。与 `internal/verification`（代码／VN-503）对称：代码走机器 gate + 合入；
这里走**产物条件 + 人工结论**，合入步骤对不适用任务直接跳过（契约 E351）。

## 动作

| Action | 行为 |
| --- | --- |
| `evaluate` | 只检查产物条件与旧证据时效，结论 `pending` |
| `accept` | 条件与 checklist 齐备且有人工 `reviewer` + 结果类 `note` → `VerifyManual` + `approved` + **跳过合入** + `done` |
| `reject` | 写独立 `fail` 证据与 `rejected` 原因（E360）；合入仍跳过 |

## 硬规则

- **接受前必须核实**：产物存在、写盘范围内、哈希／条件一致，并绑定冻结任务版本。
- **模型自评不算验收**：`accept` 强制 `reviewer`，空审／无 note 直接拒绝。
- **合入恒跳过**：`Merge.Required=false`、`Merged=false`，拒绝与接受都一样。
- **产物变更留独立证据**：旧验证按当前哈希漂移复制为 `stale`（不改入参）；重新接受写新的 `verification_id`。

## 验证

```sh
CGO_ENABLED=0 go test ./internal/verification/nongit -count=1
```

覆盖：接受完成并跳过合入、缺产物阻塞、哈希漂移→stale→重验、人工拒绝独立证据、清单未齐、越界与 commit 条件拒绝、防模型自评。
