package events

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/query"
)

func writeOK(w http.ResponseWriter, status int, body map[string]any) {
	if body == nil {
		body = map[string]any{}
	}
	body["ok"] = true
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeErr(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	localauth.WriteJSONError(w, status, code, message, details)
}

func mapQueryErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var qe *query.Error
	if errors.As(err, &qe) {
		switch qe.Code {
		case query.CodeInvalidPagination, query.CodeUnknownEnum:
			details := map[string]any{}
			if qe.Field != "" {
				details["field"] = qe.Field
			}
			writeErr(w, http.StatusUnprocessableEntity, qe.Code, qe.Msg, details)
			return
		}
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "查询失败", nil)
}

func rejectLegacyPagination(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query()
	if q.Has("offset") || q.Has("page") {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_pagination",
			"只支持游标分页，禁止 offset／page", nil)
		return true
	}
	return false
}

func parseLimit(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &query.Error{Code: query.CodeInvalidPagination, Field: "limit", Msg: "limit 必须是整数"}
	}
	return n, nil
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_idempotency_key",
			"POST 必须带 Idempotency-Key", map[string]any{"field": "Idempotency-Key"})
		return "", false
	}
	if len(key) < 16 || len(key) > 128 {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_idempotency_key",
			"Idempotency-Key 长度须在 16–128", map[string]any{"field": "Idempotency-Key"})
		return "", false
	}
	for _, c := range key {
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '.' || c == '_' || c == ':' || c == '-'
		if !ok {
			writeErr(w, http.StatusUnprocessableEntity, "invalid_idempotency_key",
				"Idempotency-Key 含非法字符", map[string]any{"field": "Idempotency-Key"})
			return "", false
		}
	}
	return key, true
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "只允许 "+allow, nil)
}
