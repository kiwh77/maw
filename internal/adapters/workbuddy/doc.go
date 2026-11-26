// Package workbuddy 把本机两款 WorkBuddy 的随包 CLI 翻译成引擎的 Probe／Launch／Decode／Interpret 端口语义。
//
// 两款发行包（`WorkBuddy` 5.8.3 / `WorkBuddy AI` 5.8.4）各自随包携带一份 CodeBuddy CLI 2.161.0。
// 它们共用同一族协议外观，但**不是同一个接入对象**：入口、运行时的实际位置、App 版本、
// 登录上下文与模型枚举都各自成立。所以本包把它们登记成两个发行包（见 distribution.go），
// 每个包的事实各带各的证据，能力不互相继承。
//
// # 与 CodeBuddy 适配器的关系：委托在语义层，不在 import
//
// 分区规则（P120）不允许接入器之间互相 import，`docs/contracts/vnext/ports` 的多接入器样本
// 也只允许 `internal/engine/{ports,model}`。所以「复用经验证的协议能力」落在**协议语义**上：
// 同一套端口语义、同名的观测类事件、同一套 result.subtype 判定纪律；
// 而「哪些语义在这个包上成立」由该包自己的能力矩阵决定。两件事分开以后，
// 复用不会变成「顺手把 CodeBuddy 的 observed 抄过来」。
//
// # 证据（docs/evidence/workbuddy）
//
//	事实                                    证据
//	两包入口位置（.app 内 app.asar.unpacked）VN305-CN-S02／VN305-INTL-S02
//	入口 shebang `#!/usr/bin/env node`       同上（入口首行）
//	--version 输出单行 2.161.0                VN305-{CN,INTL}-S01
//	--help 104 行，两包仅 --model 一行不同     VN305-{CN,INTL}-S02
//	认证缺失 = 退出码 0 + stdout 空 + stderr    VN305-X01（记录型，provenance 见 meta.json）
//
// 两包的随包 CLI 版本相同（2.161.0），所以**版本号不能用来区分来源**：
// 区分只能靠发行包登记与各自的证据目录。这是本包与 codebuddy 适配器最大的结构差别。
//
// # 能力降级（本包与 codebuddy 的实质差别）
//
// CodeBuddy 2.173.0 上观测到的记录形状，不能搬到 2.161.0 的两款随包 CLI 上：
//
//	能力                          本包状态    原因
//	print.headless_mode           observed   帮助清单 + 最小调用都确认得起来
//	output_format.json            observed   最小调用实际使用过该取值
//	output_format.stream_json     unobserved 两包从未产出过一条 stream-json 记录
//	result.record_present         unobserved 两包均未完成回合，单条 json 结果无样本
//	launch.port_auto_assign       observed   帮助写明 --port 默认 auto-assign
//	model.list_interchangeable    unsupported 两包 --model 枚举不同，不可跨包复制
//
// 于是 Launch 只发 `--output-format json`，不发 `--port 0`（那是 2.173.0 的经验，
// 不继承）；Decode 与 Interpret 的「形状解释」被 result.record_present 这条能力闸住，
// 在补到样本之前只保留原始引用、不产生事件、不判完成。
//
// # 认证：只读探测判不出来，但处理路径是确定的
//
// Probe 把 auth／connection／capacity 一律报 unknown——只读探测没发过模型任务，
// 报 ready 等于编造。认证缺失的唯一稳定信号出现在**真实运行**的 stderr
// （`Authentication required. Please use /login command …`，W10／W11），
// 调用方把它作为应用错误交给 Interpret，本包判 failed + auth_required，
// 并给出该包的人工处理指引（AuthHint）：报哪个包、在哪里登录、maw 不会做什么。
//
// # 明确拒绝的东西
//
//   - 非 `.app` 内登记的随包入口：任意路径上的 codebuddy 不是被证据覆盖的入口；
//   - 非 node 运行时（自包含二进制、别的解释器）：版本口径与 bundle 选择都会变；
//   - 缺实测启动条件（CODEBUDDY_FORCE_LITE_WB_BUNDLE／CODEBUDDY_DISABLE_COMPILE_CACHE）：
//     跑起来的不是被证据覆盖的那份实现；
//   - allow_write=false／allow_network=false／非空 Sandbox：没有可验证的开关，承诺不了就不承诺；
//   - 恢复会话：恢复语义未验证；文件输入：LaunchSpec 没有该字段；多份 MCP 配置：合并语义未观测；
//   - 环境白名单之外的宿主变量：继承私有环境会改变认证结果与 bundle 选择。
package workbuddy
