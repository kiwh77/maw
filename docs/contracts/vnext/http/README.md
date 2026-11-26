# 本机接口与授权契约（vnext / http）

- 正文：`http-api-contract-v1.md`（契约标识 `maw.http.api/v1`，已冻结）
- 机检：`http_contract.py`，与正文 §8 的不变量登记表逐条对应
- 样本：`samples/valid/` 正例、`samples/invalid/` 反例（每个反例用 `expect.codes` 声明应触发的码）

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/http -p 'test_*.py'
```

本目录只冻结接口形态与授权边界，不含服务端实现。上游定义见：

- `../task/task-instance-contract-v1.md`（实例与版本）
- `../execution/runtime-state-contract-v1.md`（运行阶段与代际）
- `../delivery/delivery-manifest-contract-v1.md`（交付清单）
- `../ports/app-ports-contract-v1.md`（端口、事务与错误模型）
