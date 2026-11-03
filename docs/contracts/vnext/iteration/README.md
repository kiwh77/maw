# docs/contracts/vnext/iteration

`docs/contracts/vnext/` 的**迭代（批次）蓝图契约**子目录（IT-002 交付）。

| 文件 | 作用 |
| --- | --- |
| `iteration-contract-v1.md` | 冻结正文：`[[iteration]]` 字段、状态、默认值、任务归属与不变量 |
| `samples/valid/*.toml` | 合法蓝图样例（可被 `internal/blueprint` 解析） |
| `samples/invalid/*.toml` | 反例：引用不存在的迭代，`plan check` 必须失败 |

验证：

```sh
CGO_ENABLED=0 go test ./internal/blueprint -count=1
```

契约只描述蓝图输入与解析后的模型；写库、迭代分支与合回由后续卡（IT-003 起）负责。
上游语义：`../maw_integrated_feature_list.zh-CN.md` 第 12 节 F49–F55。
