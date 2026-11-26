// Package notifications 把「需要用户介入的业务事实」变成有身份、可去重、可恢复的提醒记录。
//
// # 它解决的三件事
//
//   - 去重：同一业务事件在同一渠道只提醒一次；同一对象停在同一个状态时不再重复打扰。
//   - 恢复：提醒先落库再投递。进程在投递途中消失会留下「说不清有没有弹过」的记录，
//     重启后由 Recover 标成 ambiguous，而不是当作没发过。
//   - 保留：只有用户确认过的记录才按保留策略清理；未处理的提醒不因时间流逝消失。
//
// # 三个边界
//
// 事务边界：所有状态写入都发生在调用方给的事务里（与 internal/observability 一致），
// 本包不持有连接、不开事务、不做重试。Dispatch 是唯一的例外，因为投递必须在事务外：
// 它用 TxRunner 把「投递意图」与「投递结果」分成两次短事务落下。
//
// 投递边界：Deliverer 是端口，实现方在接入层（桌面托盘／OS 弹窗，VN-902）。
// 本包不 import 任何 UI、OS 或网络包——「要不要提醒、提醒哪一条」与「怎么弹」无关。
//
// 存储边界：表 notification_deliveries 属 internal/db 的迁移，本包只读写这一张表，
// 不建表、不改 schema；额外状态（指纹、尝试次数、中断次数）放在 meta_json 里。
// internal/db 目前只提供插入与按键读取、没有更新入口，因此状态流转用本包的 SQL 完成。
//
// # 三个容易搞错的地方
//
//   - delivered 不等于处理完。投递返回成功只说明「弹窗调用没报错」，用户看没看见、
//     处理没处理只有 Ack 能回答。所以未处理列表包含 delivered 的记录。
//   - 同一事件重放不是新提醒。业务事件会重放（重试、重连、投影重算），
//     按 (event_id, channel) 幂等；同一对象同一状态的重复事件按指纹再压一次。
//   - 中断的投递不能假装没发生。OS 弹窗没有事务：投递前记意图、投递后记结果，
//     两者之间的崩溃只能由 Recover 显式标成「可能重复」，
//     所以重复边界是可见的字段（Notification.DuplicateRisk / AmbiguousAttempts），
//     而不是一句「理论上不会重复」。
//
// # 验收边界与落点
//
// 「按业务事件和渠道去重」：同 (event_id, channel) 幂等 → Raise + SkipDuplicate；
// 「默认不为无变化状态持续打扰」：同 (kind, subject, state) 指纹压制，
// 未确认的无条件压制、已确认的只在去重窗口内压制 → SkipUnchanged / SkipSettled。
// 对应测试：TestRaiseDedupesByEventAndChannel、TestRaiseSuppressesUnchangedState、
// TestRaiseRenotifiesAfterAckOnlyOutsideWindow。
//
// 「重启后能恢复未处理提醒」：Recover 把未处理记录分成 ambiguous／resumable／
// awaiting_ack 三桶，Dispatch 只重投前两桶里还没投成功的。
// 「OS 弹窗非原子投递的重复边界可见」：投递意图与投递结果分两次事务落，
// 中间消失的尝试由 Recover 记成 AmbiguousAttempts，之后每次重投都带 DuplicateRisk；
// 投递实现自己判不清时用 AmbiguousDeliveryError 表达，而不是谎报成功或失败。
// 对应测试：TestRecoverMarksInterruptedAttemptAmbiguous、
// TestDispatchTreatsUnjudgeableDeliveryAsAmbiguous、TestBeginAttemptCountsInterruptionWithoutRecover。
//
// 「保留」：只有已确认且确认时间早于保留期的记录会被 Prune 删除；
// 未确认（含 ambiguous）永不因保留策略消失。
// 对应测试：TestPruneRemovesOnlyAckedBeyondRetention、TestPruneKeepsAmbiguousForever。
package notifications
