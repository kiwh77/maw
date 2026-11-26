# 时间轴与计量契约（vnext / observability）

- 正文：`timeline-metering-contract-v1.md`（契约标识 `maw.observability.timeline/v1`，已冻结）
- 机检：`observability_contract.py`，与正文 §7 的不变量登记表逐条对应
- 样本：`samples/valid/` 完整快照正例、`samples/invalid/` 反例；反例期望码登记在测试里

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/observability -p 'test_*.py'
```

本目录只冻结时间与用量的语义，不含采集器与存储实现。上游定义见：

- `../task/task-instance-contract-v1.md`
- `../execution/runtime-state-contract-v1.md`
- `../ports/app-ports-contract-v1.md`
