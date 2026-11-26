// Package nongit 对非代码交付执行产物条件与人工验收策略。
//
// 与 internal/verification（代码验收 / VN-503）对称：代码走机器 gate + 合入；
// 非代码走「产物存在／版本／条件」+ 人工结论，合入步骤对不适用任务直接跳过（E351）。
//
// 契约落点（maw.delivery.manifest/v1 §5、E350／E351／E334／E360）：
//
//  1. 接受前必须核实产物存在、落在写盘范围、哈希与条件一致，并绑定冻结任务版本。
//  2. 人工 Accept 产出 VerifyManual + review.approved；Reject 产出独立失败证据与 rejected 原因。
//  3. Merge 恒为 required=false／merged=false——非代码不得记合入。
//  4. 产物哈希漂移时，旧验证降为 stale 并保留；重新验收写一条新的 verification_id。
//  5. 不把模型自评当验收：Accept 必须有明确人工 reviewer，不得空审。
//
// 本包不改 UI、不执行 shell、不触碰 Git 工作空间或合入实现。
package nongit
