package commands

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/localauth"
)

var idempotencyKeyRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{16,128}$`)

const maxBodyBytes = 1 << 20

// meta 是传输层从请求里提炼出的条件与身份。
type meta struct {
	key         string
	fingerprint string
	revision    *model.Revision
	generation  *model.Generation
	actor       string
	action      string
	resource    string
}

// revisionCarrier 让各请求体用同一套逻辑解析与核对 revision。
type revisionCarrier interface {
	revisionValue() *int64
	generationValue() *int64
}

// begin 完成幂等键、JSON、revision 与指纹校验；失败时已写响应。
func begin(w http.ResponseWriter, r *http.Request, action, resource string, body revisionCarrier) (meta, bool) {
	key, ok := requireIdempotencyKey(w, r)
	if !ok {
		return meta{}, false
	}
	raw, ok := readBody(w, r)
	if !ok {
		return meta{}, false
	}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(body); err != nil {
			writeErr(w, http.StatusBadRequest, "malformed_json", "请求体不是合法 JSON 或含未知字段", nil)
			return meta{}, false
		}
	}
	rev, gen, ok := resolveConditions(w, r, body.revisionValue(), body.generationValue())
	if !ok {
		return meta{}, false
	}
	m := meta{
		key:         key,
		fingerprint: fingerprint(r, rev, gen, raw),
		revision:    rev,
		generation:  gen,
		action:      action,
		resource:    resource,
	}
	if id, ok := localauth.IdentityFrom(r.Context()); ok {
		m.actor = id.Actor
	}
	return m, true
}

// requireRevision 要求写动作必须带版本条件。
func requireRevision(m meta, w http.ResponseWriter) bool {
	if m.revision != nil {
		return true
	}
	writeErr(w, http.StatusUnprocessableEntity, "revision_required", "写动作必须带 If-Match 或 expected_revision", nil)
	return false
}

func readBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "malformed_json", "无法读取请求体", nil)
		return nil, false
	}
	if len(raw) > maxBodyBytes {
		writeErr(w, http.StatusBadRequest, "malformed_json", "请求体过大", nil)
		return nil, false
	}
	return raw, true
}

// resolveConditions 合并 If-Match 与 body 里的条件。
//
// 规则（http-api-contract-v1 §5）：两者都有时必须相等；都没有时 422
// revision_required；允许只有 generation 的「代际条件」仅用于运行级动作。
func resolveConditions(w http.ResponseWriter, r *http.Request, bodyRev, bodyGen *int64) (*model.Revision, *model.Generation, bool) {
	headerRev, hasHeader, ok := parseIfMatchHeader(w, r)
	if !ok {
		return nil, nil, false
	}
	if bodyRev != nil && *bodyRev < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "revision_required", "expected_revision 不得为负", nil)
		return nil, nil, false
	}
	if bodyGen != nil && *bodyGen < 1 {
		writeErr(w, http.StatusUnprocessableEntity, "generation_required", "generation 从 1 开始", nil)
		return nil, nil, false
	}
	if hasHeader && bodyRev != nil && headerRev != *bodyRev {
		writeErr(w, http.StatusUnprocessableEntity, "revision_required", "If-Match 与 expected_revision 必须一致", nil)
		return nil, nil, false
	}
	var rev *model.Revision
	switch {
	case hasHeader && bodyRev != nil:
		v := model.Revision(headerRev)
		rev = &v
	case hasHeader:
		v := model.Revision(headerRev)
		rev = &v
	case bodyRev != nil:
		v := model.Revision(*bodyRev)
		rev = &v
	}
	var gen *model.Generation
	if bodyGen != nil {
		v := model.Generation(*bodyGen)
		gen = &v
	}
	return rev, gen, true
}

func parseIfMatchHeader(w http.ResponseWriter, r *http.Request) (int64, bool, bool) {
	raw := strings.TrimSpace(r.Header.Get("If-Match"))
	if raw == "" {
		return 0, false, true
	}
	raw = strings.Trim(raw, `"`)
	raw = strings.TrimPrefix(raw, "rev-")
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		writeErr(w, http.StatusUnprocessableEntity, "revision_required", "If-Match 必须是 rev-<n>", nil)
		return 0, false, false
	}
	return n, true, true
}

// fingerprint 固定「同一请求」的判定基础：方法、路径、条件与原始 body。
// 同键但 body／revision 不同的请求会得到不同指纹，交给用例判 409。
func fingerprint(r *http.Request, rev *model.Revision, gen *model.Generation, raw []byte) string {
	h := sha256.New()
	h.Write([]byte(r.Method))
	h.Write([]byte{'\n'})
	h.Write([]byte(r.URL.Path))
	h.Write([]byte{'\n'})
	if rev != nil {
		h.Write([]byte(strconv.FormatInt(int64(*rev), 10)))
	}
	h.Write([]byte{'\n'})
	if gen != nil {
		h.Write([]byte(strconv.FormatInt(int64(*gen), 10)))
	}
	h.Write([]byte{'\n'})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
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

// writeBusy 是唯一需要 retryable=true 与 Retry-After 的映射。
func writeBusy(w http.ResponseWriter, retryAfter time.Duration) {
	seconds := int(retryAfter / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok": false,
		"error": map[string]any{
			"code":      "busy",
			"message":   "数据库忙或依赖暂不可用，可稍后重试",
			"retryable": true,
		},
	})
}

// writeReceipt 统一输出 202／200，并保证 202 不宣称成功、不缺少 Location。
func writeReceipt(w http.ResponseWriter, m meta, rc Receipt) {
	if !rc.OperationID.Valid() {
		writeErr(w, http.StatusInternalServerError, "internal_error", "用例未返回合法 operation_id", nil)
		return
	}
	status := rc.Status
	if status == "" {
		status = StatusQueued
	}
	if !status.Valid() {
		writeErr(w, http.StatusInternalServerError, "internal_error", "用例返回了未知操作状态", nil)
		return
	}
	if !rc.Replay && status.Terminal() {
		// 长动作必须在 202 之后完成；非重放却同步终止是接线错误。
		writeErr(w, http.StatusInternalServerError, "internal_error", "异步动作不得同步返回终止态", nil)
		return
	}
	action := rc.Action
	if action == "" {
		action = m.action
	}
	resource := rc.Resource
	if resource == "" {
		resource = m.resource
	}
	created := rc.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	operation := map[string]any{
		"operation_id": rc.OperationID,
		"status":       status,
		"action":       action,
		"resource":     resource,
		"created_at":   created.UTC().Format(time.RFC3339),
	}
	body := map[string]any{
		"operation":         operation,
		"idempotent_replay": rc.Replay,
	}
	location := "/api/v1/operations/" + string(rc.OperationID)
	w.Header().Set("Location", location)
	if rc.Revision != nil {
		w.Header().Set("ETag", etag(int64(*rc.Revision)))
		body["revision"] = *rc.Revision
	}
	if status.Terminal() {
		if status == StatusSucceeded {
			result := rc.Result
			if result == nil {
				result = map[string]any{}
			}
			operation["result"] = result
		} else {
			failure := rc.Failure
			if failure == nil {
				failure = &Failure{Code: "internal_error", Message: "操作失败"}
			}
			operation["error"] = map[string]any{
				"code":      failure.Code,
				"message":   failure.Message,
				"retryable": failure.Retryable,
				"details":   failure.Details,
			}
		}
		writeOK(w, http.StatusOK, body)
		return
	}
	retry := rc.RetryAfter
	if retry <= 0 {
		retry = 500 * time.Millisecond
	}
	if retry < 200*time.Millisecond {
		retry = 200 * time.Millisecond
	}
	if retry > 5*time.Second {
		retry = 5 * time.Second
	}
	body["retry_after_ms"] = int(retry / time.Millisecond)
	writeOK(w, http.StatusAccepted, body)
}

func etag(rev int64) string {
	return `"rev-` + strconv.FormatInt(rev, 10) + `"`
}

// mapUseCaseErr 把用例错误映射到 HTTP 契约的固定状态与错误码。
func mapUseCaseErr(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var mismatch ErrRevisionMismatch
	if errors.As(err, &mismatch) {
		writeErr(w, http.StatusConflict, "revision_mismatch", "revision 已变更", map[string]any{"actual_revision": mismatch.Actual})
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
	var unauthorized ErrUnauthorized
	if errors.As(err, &unauthorized) {
		code := unauthorized.Code
		if code == "" {
			code = "insufficient_scope"
		}
		writeErr(w, http.StatusForbidden, code, unauthorized.Message, nil)
		return
	}
	var busy ErrBusy
	if errors.As(err, &busy) {
		writeBusy(w, busy.RetryAfter)
		return
	}
	var modelErr *model.Error
	if errors.As(err, &modelErr) {
		status, code := mapModelError(modelErr)
		if status == http.StatusServiceUnavailable {
			writeBusy(w, 0)
			return
		}
		details := map[string]any(nil)
		if modelErr.Path != "" {
			details = map[string]any{"field": modelErr.Path}
		}
		writeErr(w, status, code, modelErr.Error(), details)
		return
	}
	writeErr(w, http.StatusInternalServerError, "internal_error", "写入失败", nil)
}

// mapModelError 按端口错误类别映射状态码（契约 §4 四类）。
func mapModelError(e *model.Error) (int, string) {
	code := string(e.Port)
	if code == "" {
		code = string(e.Code)
	}
	switch e.Port {
	case model.PortErrBusy, model.PortErrProbeTimeout:
		return http.StatusServiceUnavailable, code
	case model.PortErrRevisionMismatch, model.PortErrConflict, model.PortErrGenerationMismatch,
		model.PortErrIdempotentReplay, model.PortErrCapacityExhausted, model.PortErrNoReady,
		model.PortErrAlreadyStopped:
		return http.StatusConflict, code
	case model.PortErrUnknownIdentity, model.PortErrInvalidTransition, model.PortErrAuthRequired,
		model.PortErrInvalidSpec, model.PortErrCapabilityMissing, model.PortErrPlatformUnsupported,
		model.PortErrEntryMissing, model.PortErrEntryUnavailable, model.PortErrNotStarted:
		return http.StatusUnprocessableEntity, code
	}
	if e.Class == model.ClassRetryable {
		return http.StatusServiceUnavailable, code
	}
	return http.StatusUnprocessableEntity, code
}
