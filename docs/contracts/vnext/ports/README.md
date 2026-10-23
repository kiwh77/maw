# docs/contracts/vnext/ports

`docs/contracts/vnext/` 的**应用端口与接入器契约**子目录（VN-004 交付）。

| 文件 | 作用 |
| --- | --- |
| `app-ports-contract-v1.md` | 冻结正文：四层依赖方向、七个端口的输入/输出/错误、事务边界、错误模型、能力版本绑定 |
| `ports_contract.py` | 纯标准库校验器（`validate(doc) -> list[Violation]`），机检装配快照 |
| `test_ports_contract.py` | 正反例 + 文档/端口表/错误码对齐的单测 |
| `samples/valid/*.json` | 规范装配快照（接线、事务边界、多适配器、未知保留） |
| `samples/invalid/*.json` | 反例，`expect_error` 声明应触发的 P 码 |

三条先看的硬规则（正文 §3）：**事务内不碰外部世界**、**业务状态只在事务内写**、**接入器不得直接写业务任务状态**。
另外：**退出码 0 不能作为成功依据**（P141，依据 VN-301 实测）、**未知协议结果必须保留为 unknown**（P142）。

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/ports -p 'test_*.py'
```

下游引用：VN-005（HTTP 与本机授权）以本契约为接口边界；VN-201／VN-202（领取、派发）实现 `ClaimExecution`；
VN-302（节点登记）实现 `Probe`；VN-303／VN-304 实现 `Launch`／`Decode`／`Interpret`；`internal/runner` 实现 `ProcessController`。
上游基线：`../task/task-instance-contract-v1.md`、`../execution/runtime-state-contract-v1.md`、`../delivery/delivery-manifest-contract-v1.md`；改含义只能出 v2。
