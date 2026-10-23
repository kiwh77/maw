// Package codebuddy 把本机 CodeBuddy CLI 翻译成引擎的 Probe／Launch 端口语义。
//
// 本包只做两件事，都在契约 `maw.app.ports/v1` §2.3–2.4 的边界内：
//
//   - Probe：用 `--version`／`--help` 做只读探测，拿到真实入口、真实运行时、版本与参数清单。
//     不登录、不发模型任务、不改配置。
//   - Launch：把冻结的 RunSpec 翻成 argv 数组（绝不拼 shell 字符串）。
//     入口与版本在构造时绑定，版本能力在 Probe 时确认；未经探测不放行。
//
// 依赖方向（P120）：只 import `internal/engine/model`；不 import app／db／gitops／schedule／machine，
// 也不 import `internal/runner`——本包不认识任务状态，更不落库。
//
// # 证据来源
//
// 全部厂商事实来自 `docs/evidence/codebuddy/`（VN-301，CLI 2.173.0／darwin 实测）。
// 本包不写「推测出来」的参数：每个 argv 元素要么有实测样本背书，要么有实测帮助清单背书，
// 见下表。版本一变，`capabilities.go` 的 observed 行整体降级为 unobserved，
// Launch 随之拒绝启动——这是 G07「能力必须按版本验证」在代码里的落点。
//
//	argv 元素                     依据
//	-p                             C03（S03/S04/S05 三档输出都跑通）
//	<输入>（单元素）               C03（S03 把固定提示作为一个 argv 元素传入）
//	--output-format stream-json    C04（S03）
//	--include-partial-messages     C09（S03；S08 证明不带也能跑，本包固定取带的那种形状）
//	--port 0                       C15（S06：默认端口被占用时直接退出 1，必须显式传端口）
//	--permission-mode <mode>       C02（S02 帮助清单给出的取值枚举）
//	--strict-mcp-config            C02（S02 帮助清单）
//	--mcp-config <file|json>       C02（S02 帮助清单）
//
// # 明确拒绝的东西
//
// 「拒绝」和「支持」一样是接口承诺的一部分。以下情况一律返回错误而不是降级放行：
//
//   - allow_write=false／allow_network=false：帮助清单里没有可验证的「禁写」「禁网」开关，
//     权限拒绝路径（C27）也未观测。承诺不了就不假装承诺，直接拒绝。
//   - 非空 Sandbox：沙箱执行未观测，不接受静默改派执行环境。
//   - 恢复会话（--resume）：C24 是 unknown——恢复语义、会话是否存在都未验证，
//     按未验证语义启动会把「新会话」当成「续跑」，属于最坏的一类误判。
//   - 文件输入：LaunchSpec 没有 stdin 文件路径字段，不接受「读文件代替 stdin」的隐式行为。
//   - 多份 MCP 配置：合并语义未观测。
//   - 环境变量：只传 EnvAllowlist 里的名字（证据里 7 个），继承宿主私有环境会改变认证结果。
//
// # 探测的边界
//
// 认证与模型就绪**在只读探测里无法判定**：证据 C14 显示唯一稳定信号是 result.errors 的文本，
// 而那要发一次模型任务。所以 Probe 把 auth／connection／capacity 一律报 unknown，
// 不报假的 ready。要拿到「需登录／需选模型」，只能靠一次真实运行的可观测结果（VN-304 及之后）。
package codebuddy
