package read

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
		case query.CodeInvalidPagination:
			writeErr(w, http.StatusUnprocessableEntity, qe.Code, qe.Msg, fieldDetails(qe))
			return
		case query.CodeUnknownEnum:
			writeErr(w, http.StatusUnprocessableEntity, qe.Code, qe.Msg, fieldDetails(qe))
			return
		}
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "查询失败", nil)
}

func fieldDetails(qe *query.Error) map[string]any {
	if qe == nil || qe.Field == "" {
		return nil
	}
	return map[string]any{"field": qe.Field}
}

// rejectLegacyPagination 拒绝 offset／page（契约 §2）。
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

func pageBody[T any](page query.Page[T], revision int64) map[string]any {
	return pageBodyWith(page, revision, page.Items)
}

// pageBodyWith 与 pageBody 同形，但允许换掉 items：任务列表需要在行上补迭代归属，
// 又不能把分页信封（next_cursor／revision／watermark）各写一遍。
func pageBodyWith[T any](page query.Page[T], revision int64, items any) map[string]any {
	var next any
	if page.Next == "" {
		next = nil
	} else {
		next = page.Next
	}
	return map[string]any{
		"items":       items,
		"next_cursor": next,
		"revision":    revision,
		"watermark":   page.Watermark,
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "只允许 "+allow, nil)
}
