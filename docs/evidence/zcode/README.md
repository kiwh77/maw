# ZCode 显式配置入口证据（VN-307）

**结论**：运行时、provider、模型就绪分列报告；路径只接受绝对受控引用；缺模型时不改用户配置；未验证的模型调用／取消／恢复不开放。

验收：`CGO_ENABLED=0 go test ./internal/adapters/zcode -count=1`。夹具 CLI 版本 `1.0.0`。
