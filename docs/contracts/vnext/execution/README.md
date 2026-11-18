# docs/contracts/vnext/execution

`docs/contracts/vnext/` 的**运行状态与资源契约**子目录（VN-002 交付）。

| 文件 | 作用 |
| --- | --- |
| `runtime-state-contract-v1.md` | 冻结正文：三类状态、阶段转移、核实状态、占用与释放、取消竞争、代际、事件信封 |
| `pi-execution-contract-v1.md` | 冻结正文（`maw.pi.execution/v1`）：Pi 协议版本、worker/lead/diagnose 授权、事件边界、终态、逐次与运行级用量、缓存归一、重放键 |
| `runtime_contract.py` | 纯标准库校验器（`validate(doc) -> list[Violation]`） |
| `test_runtime_contract.py` | 正反例 + 转移表 + 文档/代码对齐的单测 |
| `samples/valid/*.json` | 规范正例（7 个现场） |
| `samples/invalid/*.json` | 反例（27 个），`expect_error` 声明应触发的错误码 |

上游契约：`../task/task-instance-contract-v1.md`（`maw.task.instance/v1`）、`../observability/timeline-metering-contract-v1.md`（`maw.observability.timeline/v1`）。

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/execution -p 'test_*.py'
```

下游引用：VN-003（交付与验收）以 `runtime-state-contract-v1.md` 为基线；改含义只能出 v2。
