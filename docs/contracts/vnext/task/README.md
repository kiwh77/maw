# docs/contracts/vnext/task

`docs/contracts/vnext/` 的**任务身份契约**子目录（VN-001 交付）。

| 文件 | 作用 |
| --- | --- |
| `task-instance-contract-v1.md` | 冻结正文：身份分层、状态权威、版本规则、依赖解析、不变量表 |
| `task_contract.py` | 纯标准库校验器（`validate(doc) -> list[Violation]`） |
| `test_task_contract.py` | 正反例 + 文档/代码对齐的单测 |
| `samples/valid/*.json` | 规范正例 |
| `samples/invalid/*.json` | 反例，`expect_error` 声明应触发的错误码 |

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/task -p 'test_*.py'
```

下游引用：VN-002（运行状态）、VN-003（交付验收）以 `task-instance-contract-v1.md` 为基线；改含义只能出 v2。
