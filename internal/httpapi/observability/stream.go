package observability

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleStream 输出 SSE 失效通知。
//
// 帧约定：
//   - event: ready      —— 连接就绪
//   - event: invalidate —— 某类观测数据可能已变，data 含 watermark／revision／capabilities
//   - : keepalive       —— 注释帧，保活
//
// 只推「变了」，不推「变成什么」：SSE 掉帧、重连、代理缓冲都会让帧丢失，
// 事实必须由查询接口按水位补拉才能恢复（契约 §12.2）。因此这里既不查库，
// 也不在帧里放区间、轮次、提醒正文。
func (a *API) handleStream(w http.ResponseWriter, r *http.Request) {
	if a.Changes == nil {
		// 用 503 而不是 200 空流：让客户端知道「现在没有实时通道」，
		// 可以退回轮询，而不是对着一个永不发帧的连接傻等。
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusServiceUnavailable, "stream_unavailable",
			"未配置失效通知源", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusNotImplemented, "streaming_unavailable",
			"响应不支持流式写出", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ctx := r.Context()
	signals, cancel := a.Changes.Subscribe(ctx)
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
		case sig, ok := <-signals:
			if !ok {
				return
			}
			payload, err := json.Marshal(invalidationPayload(sig))
			if err != nil {
				continue
			}
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

// invalidationPayload 是失效帧的载荷。
//
// 字段白名单是刻意的：只有水位、版本、时间与受影响的数据类别。
// 任何人想往这里塞区间或提醒正文，都必须先改这个函数——它是一道显式的闸。
func invalidationPayload(sig ChangeSignal) map[string]any {
	at := sig.At
	if at.IsZero() {
		at = time.Now()
	}
	out := map[string]any{
		"at":           at.UTC().Format(time.RFC3339),
		"capabilities": sig.Capabilities,
	}
	if sig.Watermark != nil {
		out["watermark"] = sig.Watermark
	}
	if sig.Revision != 0 {
		out["revision"] = sig.Revision
	}
	return out
}
