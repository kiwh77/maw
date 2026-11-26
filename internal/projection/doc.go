// Package projection 从标准事实生成分层时间区间，并支持半开窗口查询与跨日重建。
//
// 契约：docs/contracts/vnext/observability/timeline-metering-contract-v1.md §1–§3
// 架构：docs/ARCHITECTURE_VNEXT.zh-CN.md §11.2
//
// 边界：
//
//  1. 四类区间（task_phase／cli_process／turn／explicit_wait）并行存在，不得互相替代。
//  2. 查询窗口为半开区间 [start, end)；窗口前有效状态作边界上下文，再裁剪。
//  3. 投影可全量重建且幂等；缺失 end 不伪造完整时长。
//  4. 业务状态水位（state_cursor）与投影水位（projection_cursor）分开返回；
//     迟到事实可标脏并补算受影响范围，但不得回退已消费游标。
//  5. 本包不推送、不根据无输出推断人工等待；可选把检查点写入 db.projection_state。
package projection
