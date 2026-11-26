# WorkBuddy 两包随包入口证据（VN-305）

结论：两款 WorkBuddy 各自随包携带 CodeBuddy CLI **2.161.0**，入口位置、运行时形态完全一致，
`--model` 取值枚举是两份帮助清单**唯一**的差异行。两包的入口可只读探测到「版本 + 帮助」，
但**都没有跑出过一次成功回合**：认证缺失，形态是退出码 0 + stdout 空 + stderr 有错误。
因此本卡只对「已真实验证过的入口能力」开放受管：探测、启动参数、认证缺失识别；
记录形状、完成判定、用量解析一律保持 unobserved，等样本到位再放开。

采集时间 2026-10-24，平台 darwin。范围：只读 `--version`／`--help`；未登录、未发模型任务、未改 App 文件。

## 两个发行包（分别登记，不互相代表）

| 项 | `cn` | `intl` |
| --- | --- | --- |
| App | WorkBuddy 5.8.3 | WorkBuddy AI 5.8.4 |
| bundle id | `com.tencent.workbuddy.mac` | `com.workbuddy.workbuddy-ai` |
| 随包 CLI | codebuddy 2.161.0 | codebuddy 2.161.0 |
| 入口 | `<App>.app/Contents/Resources/app.asar.unpacked/cli/bin/codebuddy` | 同 |
| shebang | `#!/usr/bin/env node` | 同 |
| `--version` | 退出 0，`2.161.0`，8 字节，70ms | 退出 0，`2.161.0`，8 字节，65ms |
| `--help` | 退出 0，104 行 / 12706 字节 | 退出 0，104 行 / 12654 字节 |
| `--model` 枚举 | 9 个具体模型 ID | 4 个占位 ID |

两包随包 CLI 版本**相同**，这是本卡最容易踩的坑：版本号相同不等于能力相同，
所以能力归属按「发行包 + 入口 + 运行时 + 平台」四元组成立，每条能力各挂各的证据目录（见 `capability-matrix.json`）。

## 实测样本

| 样本 | 内容 |
| --- | --- |
| `samples/VN305-CN-S01-version-probe` | CN 包 `--version` 原始输出与耗时 |
| `samples/VN305-CN-S02-help-probe` | CN 包 `--help` 全文（12706 字节） |
| `samples/VN305-INTL-S01-version-probe` | intl 包 `--version` |
| `samples/VN305-INTL-S02-help-probe` | intl 包 `--help` 全文（12654 字节） |
| `samples/VN305-X01-auth-required` | **记录型**反例：认证缺失形态，provenance 指向 `docs/CLI_SMOKE_TEST.zh-CN.md:15,16,48` |

`X01` 明确标记 `kind: recorded`、`measured_this_round: false`：本轮只做只读探测，
固定文本调用会发模型任务，属于 Probe 边界之外，所以不复测、也不把旧结论伪装成本轮实测。

## 两包差异（逐行比对结论）

两份 `--help` 各 104 行，Python `difflib` 比对只有 **1 处**不同：

```
CN   : --model <model>  … Currently supported: (auto, glm-5v-turbo, glm-5.1, glm-5.0-turbo, glm-5.0, glm-4.7, kimi-k2.5, minimax-m2.7, deepseek-v3-2-volc)
INTL : --model <model>  … Currently supported: (fast-model, balanced-model, primary-model, deep-model)
```

结论：任何「按模型名匹配节点」的实现必须按包查表（`Distribution.SupportsModel`），
CN 的 `glm-5.1` 在 intl 包里不成立，反之 `fast-model` 在 CN 包里也不成立。对应能力 `W15`（observed）与 `W16`（unsupported）。

## 能力降级用例（本卡的核心判据）

CodeBuddy 2.173.0（独立 CLI）上观测到的记录形状，**不能**搬到 2.161.0 的两款随包 CLI 上：

| 能力 | 状态 | 依据 |
| --- | --- | --- |
| `print.headless_mode` | observed | 帮助清单列出 `-p/--print`，最小调用能跑到认证检查 |
| `output_format.json` | observed | 帮助清单给出该取值，最小调用实际使用 |
| `output_format.stream_json` | unobserved | 帮助声明了，但两包从未产出过一条记录 |
| `event_stream.partial_messages` | unobserved | 无 stream-json 样本可验证增量语义 |
| `result.record_present` | unobserved | 两包均未完成回合，单条 json 结果无样本 |
| `launch.port_auto_assign` | observed | 帮助写明 `--port` 默认 auto-assign（故**不**继承「必须显式 `--port 0`」的经验） |
| `model.list_interchangeable` | unsupported | 两包唯一差异行 |

代码里的落点：

- Launch 只发 `--output-format json`，不发 `stream-json`、不发 `--port 0`；
- Decode 在 `result.record_present` 未 observed 时**只保留原始引用**、不产生事件；
- Interpret 在同一条件下**拒绝判完成**，把运行留在待核实，而不是按未验证语义判 success。

补证据的路径是「采样本 → 改矩阵 → 状态变 observed」，不是「改代码里的常量」。

## 认证缺失：给出人工接入与处理信息

两款包的登录态是各自 App 内的账号，maw 既不能读桌面凭据也不代登录（Non-goals）。
只读探测判不出登录态，所以 Probe 把 `auth` 报 `unknown`，从不报假的 ready。

唯一稳定信号在真实运行的 stderr：

```
Authentication required. Please use /login command to sign in to your account
```

调用方把它作为应用错误交给 Interpret，判定为 `failed` + `auth_required`，并附该包的人工处理指引：

> 请在 WorkBuddy 5.8.3（cn，bundle com.tencent.workbuddy.mac）里用该包自己的账号执行 /login 完成登录，
> 然后用同一入口重新探测再派发；maw 不代登录、不读取桌面凭据、不接管已有会话。

注意这与 CodeBuddy 的路径不同：那边认证缺失出现在 `result.errors`，这边在 **stderr**。
两条路都认，否则「需登录」会被当成普通应用错误被反复重试。

## 启动条件（缺了就不是被证据覆盖的那份实现）

随包实现由 `CODEBUDDY_FORCE_LITE_WB_BUNDLE=1` 选中（工作目录只随包携带 `codebuddy-lite-wb.mjs`），
`CODEBUDDY_DISABLE_COMPILE_CACHE=1` 是同一轮最小调用里一起设置的编译缓存开关。
这两个变量由 App 自己的启动脚本提供，直接 exec 随包 CLI 时不会自动带上；
缺失时适配器直接拒绝并说明要导出什么，而不是「悄悄用默认实现跑一遍」。

## 复现

```sh
# 重新采集两包的最小调用证据
python3 docs/evidence/workbuddy/collect/collect_probe.py --dist all

# 适配器验收
CGO_ENABLED=0 go test ./internal/adapters/workbuddy -count=1
```

采集脚本只传白名单环境（`HOME/LANG/NO_COLOR/PATH/SHELL/TERM/TMPDIR`）加两个启动条件，
工作目录固定 `/tmp`、stdin 关闭、超时 60s，输出经 R01–R03 脱敏后落盘。

## 未做的事

- 未做固定文本模型调用（会发模型任务），因此**没有**任何成功回合样本；
- 未验证取消传播、会话恢复、完整用量字段；
- 未登录／未登出、未改账号、未改 App 文件、未接管桌面已有会话；
- 未把「桌面包版本」当作「随包 CLI 版本」使用，两者分别登记。
