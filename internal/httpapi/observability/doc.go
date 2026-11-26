// Package observability 提供观测与提醒的只读查询接口（HTTP 传输层）。
//
// 契约：
//
//	docs/contracts/vnext/observability/timeline-metering-contract-v1.md（区间、水位、计量）
//	docs/contracts/vnext/http/http-api-contract-v1.md（分页、错误体、授权）
//	docs/ARCHITECTURE_VNEXT.zh-CN.md §11–§12（区间分层、SSE 只做失效通知）
//
// 边界：
//
//  1. 本包只做传输与薄适配：区间来自 internal/projection，统计来自 internal/analytics，
//     轮次计量来自 internal/observability，提醒来自 internal/notifications。
//     不在 HTTP 层重算并集、去重或分摊；同一口径只保留一处实现。
//  2. 时间轴、热图、会话、统计、提醒共用同一套窗口解析（parseWindow）、来源过滤
//     （scope.source_ns）与权限检查（读接口要 read，确认要 write）。
//  3. 缺测、投影落后、不支持能力都是**类型化结果**：status／missing／projection_lag／
//     unsupported 四个字段直接说明，既不是 500，也不用 0 冒充未知。
//  4. SSE 只投递失效信号（水位＋版本＋哪类数据变了），不承载事实；事实一律由查询接口
//     给出，断线后按水位补拉（契约 §12.2）。
//  5. 不装配根路由（根路由由 WIRE 任务挂载），不建表、不改 internal/db。
package observability
