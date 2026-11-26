package events

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleEventsStream 输出 SSE 失效通知。
//
// 帧约定（新旧兼容）：
//   - event: ready —— 连接就绪
//   - event: board-changed —— 旧看板帧名，data 为空对象
//   - event: invalidate —— 新帧，data 含 watermark／revision
//
// 不推送事件正文；客户端必须以 GET /events 补拉事实。
func (a *API) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "streaming_unavailable", "响应不支持流式写出", nil)
		return
	}
	if a.Changes == nil {
		writeErr(w, http.StatusServiceUnavailable, "stream_unavailable", "未注入变化源", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	updates, cancel := a.Changes.Subscribe(ctx)
	defer cancel()

	write := func(frame string) bool {
		if _, err := fmt.Fprint(w, frame); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !write("retry: 3000\nevent: ready\ndata: {}\n\n") {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case sig, ok := <-updates:
			if !ok {
				return
			}
			// 旧帧：与 /api/events 同名，方便已有客户端。
			if !write("event: board-changed\ndata: {}\n\n") {
				return
			}
			payload, _ := json.Marshal(map[string]any{
				"watermark": sig.Watermark,
				"revision":  sig.Revision,
				"at":        sig.At.UTC().Format(time.RFC3339),
			})
			if !write("event: invalidate\ndata: " + string(payload) + "\n\n") {
				return
			}
		case <-heartbeat.C:
			if !write(": keepalive\n\n") {
				return
			}
		}
	}
}
