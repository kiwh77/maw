# 页面上下文与挂载契约（vnext / ui-data）

- 正文：`view-context-contract-v1.md`（契约标识 `maw.ui.data/v1`，已冻结）
- 机检：`ui_data_contract.py`，与正文 §7 的不变量登记表逐条对应
- 样本：`samples/valid/` 正例、`samples/invalid/` 反例（每个反例用 `expect.codes` 声明应触发的码）

验证：

```sh
python3 -m unittest discover -s docs/contracts/vnext/ui-data -p 'test_*.py'
```

本目录只冻结页面装配与挂载形态，不含视图框架或页面实现。上游定义见：

- `../http/http-api-contract-v1.md`（`/api/v1`、幂等键、条件写、202 操作）
- `../observability/timeline-metering-contract-v1.md`（时间轴与游标）
- `../task/task-instance-contract-v1.md`（实例 ID 与 revision）
- `../ui/WORKBENCH_UI_CONTRACT.md`（视觉与布局基线）
