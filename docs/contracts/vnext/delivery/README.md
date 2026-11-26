# docs/contracts/vnext/delivery

`docs/contracts/vnext/` 的**交付与验收契约**子目录（VN-003 交付）。

| 文件 | 作用 |
| --- | --- |
| `delivery-manifest-contract-v1.md` | 冻结正文：Manifest 字段、产物核实、验证证据等级、完成条件、重复交付、评审合入 |
| `delivery_contract.py` | 纯标准库校验器（`validate(doc) -> list[Violation]`） |
| `test_delivery_contract.py` | 正反例 + 验收条款 + 文档/代码对齐的单测 |
| `samples/valid/*.json` | 规范正例（7 个现场） |
| `samples/invalid/*.json` | 反例（25 个），`expect_error` 声明应触发的错误码 |

上游契约：`../task/task-instance-contract-v1.md`、`../execution/runtime-state-contract-v1.md`。

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/delivery -p 'test_*.py'
```

下游引用：VN-004（应用端口与接入器）以本文件为基线；改含义只能出 v2。
