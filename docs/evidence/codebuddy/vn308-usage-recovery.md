# VN-308：已验证轮次用量与恢复能力的映射决策

状态：基于 VN-301 实测证据（CLI 2.173.0 / darwin）的映射决策记录。
版本绑定：与 `capability-matrix.json` 一致——CLI 版本、入口或平台变化后，
下列「映射」结论全部作废，需重新采集。

## 1. 用量映射（只从 result 记录）

| 项 | 决策 | 证据 |
| --- | --- | --- |
| result.usage 的 token 字段 | **映射**：input / output / cache_creation→cache_write / cache_read→cache_read，quality=observed | C17（S03/S04 实测字段） |
| input 与缓存的关系 | **不合并**：实测 input_tokens = cache_creation + cache_read（23,945 = 23,433 + 512）；cache_* 单独记账，total = input + output | C18、计量契约 T140 |
| 口径 | **delta**：一次 headless 运行恰好一条 result（C08），usage 描述本次运行；来源没有跨运行累计计数，也未观测到重置 | C03/C08/C17 |
| assistant 记录上的 usage | **不解析**：thinking 那条为 0、text 那条为全量，聚合口径未验证；求和即伪造总量 | C19 unknown |
| num_turns | **不映射**：单轮文本回复 num_turns=3、认证失败 num_turns=2，语义未验证 | C20 unknown |
| total_cost_usd | **不映射**：成功与失败均为 0，口径未验证 | C21 unknown |
| modelUsage | **不解析**：json 模式为 null、stream_json 为映射，形状不稳定 | C22 |
| usage 字段缺失 | 返回 nil（未观测），**不返回全 0 冒充实测**（T142） | 计量契约 §4.3 |
| 负数 token | 拒绝（T140：非负整数） | 计量契约 §4 |

代码落点：`internal/adapters/codebuddy/usage.go`（ParseUsage）。

## 2. 恢复能力（会话续跑）

| 项 | 决策 | 证据 |
| --- | --- | --- |
| `-r/--resume`、`-c/--continue` | **unknown，不得使用**：恢复语义、会话存在性、跨进程可用性均未实测 | C24 unknown |
| `--session-id` 复用 | **unobserved**：仅在帮助清单出现，指定后是否复用未实测 | C25 unobserved |
| Launch 行为 | 带 `ResumeSessionID` 的规格一律拒绝（capability_unsupported） | `launch.go`（VN-304 已建，本卡补回归测试） |
| 最近会话兜底 | **明确禁止**：`ResumeTarget` 对任何请求（含空与显式会话）都返回错误；「最近用过」不等于「可以续跑」 | C24/C25 |

代码落点：`internal/adapters/codebuddy/usage.go`（Recovery／ResumeTarget）。

## 3. 等待与共享轮次（不发明）

- `explicit_wait`：CodeBuddy 样本没有任何等待区间证据（无阻塞声明字段），
  解码层永远不产出等待事实；测试 `TestDecodeNeverEmitsWaitEvents` 用 S03 全量记录回归。
- 共享轮次（`shared_with[]`）：样本没有该字段，用量观测不发明共享分摊；
  一轮只服务一个运行，计量侧按 turn 身份去重即可，无需分摊。
- 累计重置（计量契约 §4.1）：delta 口径下不存在跨代相减；重放同一 result 记录
  得到同一身份的同一观测，由计量侧去重（`TestParseUsageReplayStable`）。

## 4. 正反例回放清单（本卡 Verify 覆盖）

| 用例 | 期望 | 测试 |
| --- | --- | --- |
| S03 真实 result 记录 | 字段一字不差映射，total=input+output | TestParseUsageFromRealSample |
| 同一记录重放 | 同身份同数值，不相加 | TestParseUsageReplayStable |
| assistant 记录带 usage | 拒绝解析（C19） | TestParseUsageRefusesAssistantRecord |
| result 无 usage 字段 | nil，保持 unknown | TestParseUsageMissingKeepsUnknown |
| 负数 token | 拒绝 | TestParseUsageRejectsNegative |
| num_turns / cost 注入 | 不出现在观测 | TestParseUsageIgnoresUnverifiedFields |
| 共享轮次注入 | 不发明 shared 字段 | TestParseUsageNeverInventsSharedTurns |
| 全样本解码 | 永不产出 wait 事件 | TestDecodeNeverEmitsWaitEvents |
| 恢复目标请求 | 一律拒绝并点名最近会话禁区 | TestRecoveryCapabilityUnknownAndResumeRefused |
| 规格带 ResumeSessionID | 拒绝且 argv 无恢复参数 | TestLaunchNeverBuildsResumeArgv |

验证命令：

```sh
CGO_ENABLED=0 go test ./internal/adapters/codebuddy -count=1
```
