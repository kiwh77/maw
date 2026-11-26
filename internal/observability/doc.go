// Package observability 管理来源会话归属与可追溯用量计量。
//
// 契约：docs/contracts/vnext/observability/timeline-metering-contract-v1.md §4–§5
// 存储：internal/db（VN-107）提供会话／轮次／usage_records；本包不建表、不猜费用。
//
// 边界：
//
//  1. 计量主键是 turn_id（及 epoch）；多 Run 挂同一 Turn 只计一次。
//  2. 无分摊依据时保留「未分摊」总量，不伪造按任务拆分。
//  3. 累计重置升 epoch；cache_* 不并入 input；unknown 的 Token 保持 null。
//  4. 覆盖率分母只含已观测样本，缺测不填 0。
package observability
