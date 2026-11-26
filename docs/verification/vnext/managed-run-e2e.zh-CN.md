# 受管真实 Run 端到端验收（GAP-010）

由 `tests/managed/e2e_audit_test.go` 生成；样本来源是受控 CLI 脚本，只证明装配与留痕，不代表厂商现场实测。

## 命令

```sh
CGO_ENABLED=0 go test ./tests/managed -count=1 -run TestManagedRunEndToEndAudit
CGO_ENABLED=0 go test ./tests/managed -count=1   # 默认 CI 全量
```

## 环境

- 平台：darwin/arm64
- Go：go1.26.1
- 隔离：临时 `MAW_GLOBAL` + 临时 Git 仓库（不触碰真实状态库）
- 来源：`tests/managed/testdata/fake-codebuddy.sh（synthetic fixture，非厂商实测）`
- Run：`run_ki938nzcq4ph`；Operation：`op_2z6f7bmgy5jr`

## 库行数前后差

| 表 | 执行前 | 执行后 | 差 |
|---|---:|---:|---:|
| operations | 0 | 1 | +1 |
| run_events | 0 | 5 | +5 |
| runs | 0 | 1 | +1 |
| system_jobs | 0 | 1 | +1 |

## op 目录文件列表

```
bootstrap.token
journal/cursor.json
journal/journal.jsonl
launch_spec.json
process.json
report.json
run_meta.json
runner.lock
```

report.json 路径（本次运行在临时目录，测试结束即清理）：`/var/folders/1t/_t97xpgj6z78vj3zv6pqw6480000gn/T/TestManagedRunEndToEndAudit1335412332/001/runtime/69c8b0bbf99fb669/ops/op_2z6f7bmgy5jr/report.json`

阶段推进（run_events）：`reserved` → `running` → `completed`

## Darwin 守卫

非 macOS 环境下本用例调用 `t.Skip`，`go test` 输出 `--- SKIP` 而不是 `--- FAIL`；
`TestDarwinGuardClassifiesSkipNotFailure` 在任意平台校验这条判定口径。
