// Package codebuddy 把本机 CodeBuddy CLI 翻译成引擎的 Probe／Launch／Decode／Interpret 端口语义。
//
// 本包只做四件事，都在契约 `maw.app.ports/v1` §2.3–2.6 的边界内：
//
//   - Probe：用 `--version`／`--help` 做只读探测，拿到真实入口、真实运行时、版本与参数清单。
//     不登录、不发模型任务、不改配置。
//   - Launch：把冻结的 RunSpec 翻成 argv 数组（绝不拼 shell 字符串）。
//     入口与版本在构造时绑定，版本能力在 Probe 时确认；未经探测不放行。
//   - Decode：把一条 stream-json 记录翻成零到一条**观测类**标准事件。
//     未识别／损坏的记录只留证据引用，不产生事件、不映射成状态迁移。
//   - Interpret：按 `result` 记录判定运行结果。退出码 0 不是成功依据。
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
// 不报假的 ready。要拿到「需登录／需选模型」，只能靠一次真实运行的可观测结果——
// 也就是本包的 Interpret：S08 那种「退出码 0 + Authentication required」由它判成
// failed + auth_required，而不是假 done。
//
// # 事件与结束判定（VN-304）
//
// 记录到事件的映射是封闭的（来源记录带 `__timestamp` 与 `uuid`，均为实测字段）：
//
//	来源记录                   标准事件 kind      说明
//	system/init                turn.started       会话开始
//	assistant                  turn.message       模型回合消息（同回合 thinking/text 两条）
//	result                     turn.finished      最终结果记录出现（≠ 成功）
//	system/status              （无事件）         状态快照，status 字段实测为 null
//	stream_event               （无事件）         SSE 原样透传（C09），是 delta 不是事件
//	其他（如 system/hook）      （无事件 + 错误）  未识别类型，只留证据引用
//
// 三条纪律：
//
//   - **不产出控制类事件**：run.phase／task.status／lease.*／claim.* 只能由 app 层在事务里写。
//     接入器说「回合结束了」，不说「任务完成了」。
//   - **不伪造执行区间**：发生时间只取来源的 `__timestamp`；来源没给就取接收时刻并标
//     estimated，发生晚于接收则标 unknown。事件里没有 duration 字段，本包也不计算时长。
//   - **重放去重靠确定性身份**：event_id = hash(来源身份 + 类型 + 内容指纹)，
//     同一条字节重放必然同 id，store 按 E212 判重放而不是记两条；不同记录不会撞同一个 id。
//
// 结束判定只有 `result` 记录算数：subtype=success 且 is_error=false 且正文非空 → completed；
// error_during_execution／is_error=true → failed（其中认证缺失单独归 auth_required）；
// 缺记录、空正文、未识别 subtype、退出码与记录冲突 → unknown（保持占用，待核实）。
package codebuddy
