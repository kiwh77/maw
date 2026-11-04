# tests/managed — first CodeBuddy managed loop (VN-982)

## Default CI

```sh
CGO_ENABLED=0 go test ./tests/managed -count=1
```

Uses:

- Temporary `MAW_GLOBAL` + isolated git repo
- Controlled CLI: [`testdata/fake-codebuddy.sh`](testdata/fake-codebuddy.sh) (**synthetic fixture**, not a vendor field measurement)
- Real maw application APIs: Probe → explicit Claim → ManagedStart / Runner → Manifest negative gate → recovery

## Opt-in real CLI smoke (not default CI)

```sh
MAW_MANAGED_SMOKE=1 CGO_ENABLED=0 go test ./tests/managed -count=1 -run Smoke
```

Requires a real CodeBuddy CLI on `PATH` that the operator has already verified locally. Evidence must be filed under `docs/evidence/codebuddy/` with an explicit source label; do not treat the fake script as field proof.

## 受管 Run 端到端验收（GAP-010）

```sh
CGO_ENABLED=0 go test ./tests/managed -count=1 -run TestManagedRunEndToEndAudit
```

一条受控 Run 走真实 Claim + `__runner` 入口，断言：

- 库里 `runs` 有起止时刻，`run_events` 有 `reserved → running → 终态` 的阶段推进；
- op 目录里有 `journal/journal.jsonl` 与执行后写出的 `report.json`；
- `system_jobs` 能用 `run_id`／`operation_id`／`journal_dir` 反查这次执行，且日志目录在盘上存在。

非 macOS 环境按跳过处理（`--- SKIP`，不是失败）：受管 Run 依赖 macOS 的进程组与文件锁语义。
`TestDarwinGuardClassifiesSkipNotFailure` 在任意平台校验这条口径。

把本次事实写成 Markdown 证据（相对路径按仓库根解析）：

```sh
MAW_E2E_REPORT=docs/verification/vnext/managed-run-e2e.zh-CN.md \
  CGO_ENABLED=0 go test ./tests/managed -count=1 -run TestManagedRunEndToEndAudit
```

