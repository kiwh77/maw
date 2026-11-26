# Trae 能力分级证据（VN-306）

**一句话结论**：只发现 Trae IDE chat 时保持 `manual_client`，**不宣称**可后台回收结果、不开放受管启动；独立 CLI 必须单独 Probe，版本命中证据矩阵且认证就绪后，才经同一契约端口开放对应能力。IDE 与 CLI **不共享**登录／会话／权益。

采集范围：只读分级与契约门禁（本卡不为凑验收安装 CLI）。平台：darwin。验收：`CGO_ENABLED=0 go test ./internal/adapters/trae -count=1`。

## 分级

| 入口 | 模式 | managed.launch | background_result_recovery |
| --- | --- | --- | --- |
| IDE chat | `manual_client` | unsupported | unsupported |
| 独立 CLI（版本=1.0.0 且已授权） | `managed_cli` | observed | observed |
| 独立 CLI（未登录／版本不符） | `managed_cli` | unobserved | unobserved |

## 样本元数据

见 `capability-matrix.json` 与 `manifest.json`。合成夹具版本 `1.0.0` 仅用于适配器门禁单测；真实本机 CLI 需重新采集后改矩阵版本绑定。
