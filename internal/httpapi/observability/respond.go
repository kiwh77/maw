package observability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/notifications"
	"github.com/kiwh77/maw/internal/query"
)

// writeJSON 输出成功响应。响应体自带 ok:true（见 meta），失败一律走 writeErr。
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 输出契约 §6 的固定错误体。
func writeErr(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	localauth.WriteJSONError(w, status, code, message, details)
}

// mapErr 把语义错误映射成契约状态码。
//
// 映射规则只在这里出现一次：五个读接口＋确认接口的错误体形状必须一致，
// 否则同一类「窗口非法」在不同接口会得到不同状态码。
func mapErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	// 客户端已断开：不再写响应，也不把它记成 500。
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}

	var qe *query.Error
	if errors.As(err, &qe) {
		details := map[string]any{}
		if qe.Field != "" {
			details["field"] = qe.Field
		}
		writeErr(w, http.StatusUnprocessableEntity, qe.Code, qe.Msg, details)
		return
	}

	var inv *ErrInvalid
	if errors.As(err, &inv) {
		details := map[string]any{}
		if inv.Field != "" {
			details["field"] = inv.Field
		}
		writeErr(w, http.StatusUnprocessableEntity, inv.Code, inv.Message, details)
		return
	}

	var nf *ErrNotFound
	if errors.As(err, &nf) {
		writeErr(w, http.StatusNotFound, "not_found", nf.Message,
			map[string]any{"resource": nf.Resource})
		return
	}
	if errors.Is(err, notifications.ErrUnknownNotification) {
		writeErr(w, http.StatusNotFound, "not_found", "提醒不存在",
			map[string]any{"resource": "notification"})
		return
	}

	var fb *ErrForbidden
	if errors.As(err, &fb) {
		writeErr(w, http.StatusForbidden, fb.Code, fb.Message, nil)
		return
	}

	// 兜底：不回显 err.Error()，避免把 SQL、路径或凭据写进响应。
	writeErr(w, http.StatusInternalServerError, "internal_error", "查询失败", nil)
}

// methodNotAllowed 输出 405 并带 Allow（契约 §6）。
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "只允许 "+allow, nil)
}

// requireIdempotencyKey 校验 POST 的幂等键（契约 §4）：形态 [A-Za-z0-9._:-]{16,128}。
//
// 确认提醒虽然幂等，仍要求带键：客户端超时重试时，服务端需要能区分
// 「同一请求重放」与「用户又点了一次」。
func requireIdempotencyKey(r *http.Request) (string, error) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		return "", invalidField("invalid_idempotency_key", "Idempotency-Key",
			"POST 必须带 Idempotency-Key")
	}
	if len(key) < 16 || len(key) > 128 {
		return "", invalidField("invalid_idempotency_key", "Idempotency-Key",
			"Idempotency-Key 长度须在 16–128")
	}
	for _, c := range key {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-'
		if !ok {
			return "", invalidField("invalid_idempotency_key", "Idempotency-Key",
				"Idempotency-Key 含非法字符")
		}
	}
	return key, nil
}
