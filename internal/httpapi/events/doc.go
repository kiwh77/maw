// Package events 提供可恢复的事件补拉与 SSE 失效通知，以及受约束的旁路上报入口。
//
// 契约依据：
//
//	docs/contracts/vnext/http/http-api-contract-v1.md（GET /events、/events/stream、POST /status-reports）
//	docs/ARCHITECTURE_VNEXT.zh-CN.md §12.2（扫描水位、has_more、SSE 不承载事实）
//	docs/contracts/vnext/observability（旁路观测不得改任务状态）
//
// 边界：
//
//  1. 完整事实只走 GET /api/v1/events 游标补拉；SSE 只发失效／水位提示。
//  2. 过滤时 next_cursor 推进到「本窗实际扫描到的最后一条」，允许空页，避免跳过匹配。
//  3. POST /api/v1/status-reports 只摄取观测事实，不占有任务、不写 done。
//  4. 本包不装配根路由；鉴权与 Store／写入依赖由调用方注入。
package events
