package resources

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/nodes"
)

var idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

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

func mapUseCaseErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var conflict ErrConflict
	if errors.As(err, &conflict) {
		writeErr(w, http.StatusConflict, conflict.Code, conflict.Message, conflict.Details)
		return
	}
	var missing ErrNotFound
	if errors.As(err, &missing) {
		writeErr(w, http.StatusNotFound, "not_found", missing.Message, nil)
		return
	}
	var invalid ErrInvalid
	if errors.As(err, &invalid) {
		code := invalid.Code
		if code == "" {
			code = "invalid_request"
		}
		writeErr(w, http.StatusUnprocessableEntity, code, invalid.Message, invalid.Details)
		return
	}
	if errors.Is(err, nodes.ErrRevisionMismatch) {
		writeErr(w, http.StatusConflict, "revision_mismatch", "节点 revision 已变更", nil)
		return
	}
	if errors.Is(err, nodes.ErrExists) {
		writeErr(w, http.StatusConflict, "already_exists", "节点已存在", nil)
		return
	}
	if errors.Is(err, nodes.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "节点不存在", nil)
		return
	}
	var viol nodes.Violations
	if errors.As(err, &viol) && len(viol) > 0 {
		writeErr(w, http.StatusUnprocessableEntity, string(viol[0].Code), viol[0].Msg, map[string]any{"field": viol[0].Path})
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "写入失败", nil)
}

func requireIdempotencyKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_idempotency_key", "POST 必须带 Idempotency-Key", nil)
		return "", false
	}
	if !idempotencyKeyRe.MatchString(key) {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_idempotency_key", "Idempotency-Key 形态不合法", nil)
		return "", false
	}
	return key, true
}

func parseIfMatch(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		writeErr(w, http.StatusUnprocessableEntity, "revision_required", "写动作必须带 If-Match", nil)
		return 0, false
	}
	raw = strings.Trim(raw, `"`)
	raw = strings.TrimPrefix(raw, "rev-")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "revision_required", "If-Match 必须是 rev-<n>", nil)
		return 0, false
	}
	return n, true
}

func etag(rev int64) string {
	return `"rev-` + strconv.FormatInt(rev, 10) + `"`
}

// credentialKeys 是禁止入库的原始凭据字段名（大小写不敏感）。
var credentialKeys = map[string]bool{
	"password": true, "passwd": true, "secret": true, "token": true,
	"api_key": true, "apikey": true, "access_token": true, "refresh_token": true,
	"credential": true, "credentials": true, "auth_token": true, "private_key": true,
}

func rejectCredentials(raw map[string]any) error {
	return walkCredentials("", raw)
}

func walkCredentials(prefix string, v any) error {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			key := strings.ToLower(strings.TrimSpace(k))
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if credentialKeys[key] {
				return ErrInvalid{
					Code:    "credentials_not_allowed",
					Message: "节点配置不得包含原始凭据字段",
					Details: map[string]any{"field": path},
				}
			}
			if err := walkCredentials(path, child); err != nil {
				return err
			}
		}
	case []any:
		for i, child := range x {
			path := prefix + "[" + strconv.Itoa(i) + "]"
			if err := walkCredentials(path, child); err != nil {
				return err
			}
		}
	}
	return nil
}
