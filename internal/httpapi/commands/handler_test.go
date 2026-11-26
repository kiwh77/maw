package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

const (
	testProject  = "0123456789abcdef"
	testInstance = "ti_abcdef"
	testRun      = "run_a001"
	testDelivery = "dlv_a001"
	testKey      = "idem-key-0000001"
	testOp       = "op_7f3c9a"
)

type fakeCommands struct {
	mu sync.Mutex

	claimFn     func(context.Context, ClaimCommand) (Receipt, error)
	dispatchFn  func(context.Context, DispatchCommand) (Receipt, error)
	cancelFn    func(context.Context, CancelCommand) (Receipt, error)
	retryFn     func(context.Context, RetryCommand) (Receipt, error)
	cancelRunFn func(context.Context, CancelRunCommand) (Receipt, error)
	deliverFn   func(context.Context, DeliveryCommand) (Receipt, error)
	verifyFn    func(context.Context, VerificationCommand) (Receipt, error)
	reviewFn    func(context.Context, ReviewCommand) (Receipt, error)
	mergeFn     func(context.Context, MergeCommand) (Receipt, error)
}

func (f *fakeCommands) Claim(ctx context.Context, cmd ClaimCommand) (Receipt, error) {
	if f.claimFn == nil {
		return Receipt{}, errors.New("claim 未注入")
	}
	return f.claimFn(ctx, cmd)
}

func (f *fakeCommands) Dispatch(ctx context.Context, cmd DispatchCommand) (Receipt, error) {
	if f.dispatchFn == nil {
		return Receipt{}, errors.New("dispatch 未注入")
	}
	return f.dispatchFn(ctx, cmd)
}

func (f *fakeCommands) Cancel(ctx context.Context, cmd CancelCommand) (Receipt, error) {
	if f.cancelFn == nil {
		return Receipt{}, errors.New("cancel 未注入")
	}
	return f.cancelFn(ctx, cmd)
}

func (f *fakeCommands) Retry(ctx context.Context, cmd RetryCommand) (Receipt, error) {
	if f.retryFn == nil {
		return Receipt{}, errors.New("retry 未注入")
	}
	return f.retryFn(ctx, cmd)
}

func (f *fakeCommands) CancelRun(ctx context.Context, cmd CancelRunCommand) (Receipt, error) {
	if f.cancelRunFn == nil {
		return Receipt{}, errors.New("cancel run 未注入")
	}
	return f.cancelRunFn(ctx, cmd)
}

func (f *fakeCommands) Deliver(ctx context.Context, cmd DeliveryCommand) (Receipt, error) {
	if f.deliverFn == nil {
		return Receipt{}, errors.New("deliver 未注入")
	}
	return f.deliverFn(ctx, cmd)
}

func (f *fakeCommands) Verify(ctx context.Context, cmd VerificationCommand) (Receipt, error) {
	if f.verifyFn == nil {
		return Receipt{}, errors.New("verify 未注入")
	}
	return f.verifyFn(ctx, cmd)
}

func (f *fakeCommands) Review(ctx context.Context, cmd ReviewCommand) (Receipt, error) {
	if f.reviewFn == nil {
		return Receipt{}, errors.New("review 未注入")
	}
	return f.reviewFn(ctx, cmd)
}

func (f *fakeCommands) Merge(ctx context.Context, cmd MergeCommand) (Receipt, error) {
	if f.mergeFn == nil {
		return Receipt{}, errors.New("merge 未注入")
	}
	return f.mergeFn(ctx, cmd)
}

func newAPI(f *fakeCommands) http.Handler {
	return (&API{
		Dispatcher:    f,
		Claims:        f,
		Tasks:         f,
		Runs:          f,
		Deliveries:    f,
		Verifications: f,
		Reviews:       f,
		Merges:        f,
	}).Handler()
}

func doReq(t *testing.T, h http.Handler, method, target, key, ifMatch string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, reader)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应不是 JSON: %v\n%s", err, rec.Body.String())
	}
	return out
}

func revisionPtr(v int64) *model.Revision {
	r := model.Revision(v)
	return &r
}

func queuedReceipt() Receipt {
	return Receipt{
		OperationID: testOp,
		Status:      StatusQueued,
		CreatedAt:   time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
		Revision:    revisionPtr(43),
		RetryAfter:  750 * time.Millisecond,
	}
}

func TestClaimAcceptedAsync(t *testing.T) {
	var got ClaimCommand
	fake := &fakeCommands{claimFn: func(_ context.Context, cmd ClaimCommand) (Receipt, error) {
		got = cmd
		return queuedReceipt(), nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`,
		map[string]any{"capacity": map[string]any{"node_slot_limit": 2, "managed_limit": 1}})

	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/api/v1/operations/"+testOp {
		t.Fatalf("Location 不正确: %q", loc)
	}
	if tag := rec.Header().Get("ETag"); tag != `"rev-43"` {
		t.Fatalf("ETag 不正确: %q", tag)
	}
	body := decode(t, rec)
	if body["ok"] != true || body["idempotent_replay"] != false {
		t.Fatalf("响应外壳不正确: %+v", body)
	}
	if body["revision"] != float64(43) {
		t.Fatalf("revision 不正确: %+v", body["revision"])
	}
	if body["retry_after_ms"] != float64(750) {
		t.Fatalf("retry_after_ms 不正确: %+v", body["retry_after_ms"])
	}
	op := body["operation"].(map[string]any)
	if op["status"] != "queued" || op["operation_id"] != testOp {
		t.Fatalf("操作体不正确: %+v", op)
	}
	if _, exists := op["result"]; exists {
		t.Fatalf("202 不得宣称结果: %+v", op)
	}
	if got.ProjectID != testProject || got.ExpectedRevision != 42 {
		t.Fatalf("用例输入不正确: %+v", got)
	}
	if got.IdempotencyKey != testKey || got.Fingerprint == "" {
		t.Fatalf("幂等键/指纹未传到用例: %+v", got)
	}
	if got.Capacity.NodeSlotLimit != 2 || got.Capacity.ManagedLimit != 1 {
		t.Fatalf("容量未解析: %+v", got.Capacity)
	}
}

func TestBodyRevisionMayReplaceIfMatch(t *testing.T) {
	var got ClaimCommand
	fake := &fakeCommands{claimFn: func(_ context.Context, cmd ClaimCommand) (Receipt, error) {
		got = cmd
		return queuedReceipt(), nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, "",
		map[string]any{"expected_revision": 7})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if got.ExpectedRevision != 7 {
		t.Fatalf("expected_revision 未生效: %+v", got)
	}
}

func TestMissingRevisionRejected(t *testing.T) {
	called := false
	fake := &fakeCommands{claimFn: func(context.Context, ClaimCommand) (Receipt, error) {
		called = true
		return queuedReceipt(), nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, "", nil)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状态码应为 422，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "revision_required" {
		t.Fatalf("错误码不正确: %v", code)
	}
	if called {
		t.Fatal("缺 revision 时不应调用用例")
	}
}

func TestHeaderAndBodyRevisionMustAgree(t *testing.T) {
	fake := &fakeCommands{}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`,
		map[string]any{"expected_revision": 43})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状态码应为 422，实际 %d", rec.Code)
	}
}

func TestStaleRevisionReturnsActual(t *testing.T) {
	fake := &fakeCommands{claimFn: func(context.Context, ClaimCommand) (Receipt, error) {
		return Receipt{}, ErrRevisionMismatch{Actual: 44}
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码应为 409，实际 %d", rec.Code)
	}
	errBody := decode(t, rec)["error"].(map[string]any)
	if errBody["code"] != "revision_mismatch" {
		t.Fatalf("错误码不正确: %+v", errBody)
	}
	details := errBody["details"].(map[string]any)
	if details["actual_revision"] != float64(44) {
		t.Fatalf("actual_revision 不正确: %+v", details)
	}
}

func TestIdempotentReplayTerminalReturns200(t *testing.T) {
	fake := &fakeCommands{claimFn: func(context.Context, ClaimCommand) (Receipt, error) {
		rc := queuedReceipt()
		rc.Status = StatusSucceeded
		rc.Replay = true
		rc.RetryAfter = 0
		rc.Result = map[string]any{"delivery_id": testDelivery}
		return rc, nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("重放终止态应为 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["idempotent_replay"] != true {
		t.Fatalf("应标记重放: %+v", body)
	}
	if _, exists := body["retry_after_ms"]; exists {
		t.Fatalf("终止态不应带 retry_after_ms: %+v", body)
	}
	op := body["operation"].(map[string]any)
	if op["status"] != "succeeded" {
		t.Fatalf("终止状态不正确: %+v", op)
	}
	result := op["result"].(map[string]any)
	if result["delivery_id"] != testDelivery {
		t.Fatalf("结果不正确: %+v", result)
	}
}

func TestNonReplayTerminalIsRejected(t *testing.T) {
	fake := &fakeCommands{claimFn: func(context.Context, ClaimCommand) (Receipt, error) {
		rc := queuedReceipt()
		rc.Status = StatusSucceeded
		return rc, nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("非重放同步成功应视为接线错误，实际 %d: %s", rec.Code, rec.Body.String())
	}
}

func TestIdempotencyKeyReuse(t *testing.T) {
	var fingerprints []string
	fake := &fakeCommands{claimFn: func(_ context.Context, cmd ClaimCommand) (Receipt, error) {
		fingerprints = append(fingerprints, cmd.Fingerprint)
		if len(fingerprints) == 1 {
			return queuedReceipt(), nil
		}
		return Receipt{}, ErrConflict{Code: "idempotency_key_reuse", Message: "同键不同请求"}
	}}
	h := newAPI(fake)
	first := doReq(t, h, http.MethodPost, "/api/v1/projects/"+testProject+"/claims",
		testKey, `"rev-42"`, map[string]any{"node_id": "node-a"})
	if first.Code != http.StatusAccepted {
		t.Fatalf("首次应为 202，实际 %d", first.Code)
	}
	second := doReq(t, h, http.MethodPost, "/api/v1/projects/"+testProject+"/claims",
		testKey, `"rev-42"`, map[string]any{"node_id": "node-b"})
	if second.Code != http.StatusConflict {
		t.Fatalf("同键不同请求应为 409，实际 %d", second.Code)
	}
	if code := decode(t, second)["error"].(map[string]any)["code"]; code != "idempotency_key_reuse" {
		t.Fatalf("错误码不正确: %v", code)
	}
	if len(fingerprints) != 2 || fingerprints[0] == fingerprints[1] {
		t.Fatalf("不同请求必须得到不同指纹: %v", fingerprints)
	}
}

func TestInvalidIdempotencyKey(t *testing.T) {
	fake := &fakeCommands{}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", "short", `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状态码应为 422，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "invalid_idempotency_key" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	fake := &fakeCommands{}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`,
		map[string]any{"surprise": true})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("未知字段应为 400，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "malformed_json" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestNoReadyMapsToConflict(t *testing.T) {
	fake := &fakeCommands{claimFn: func(context.Context, ClaimCommand) (Receipt, error) {
		return Receipt{}, &model.Error{Port: model.PortErrNoReady, Class: model.ClassTerminal, Msg: "没有可领取任务"}
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusConflict {
		t.Fatalf("状态码应为 409，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "no_ready" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestCancelTaskAccepted(t *testing.T) {
	var got CancelCommand
	fake := &fakeCommands{cancelFn: func(_ context.Context, cmd CancelCommand) (Receipt, error) {
		got = cmd
		rc := queuedReceipt()
		rc.Action = ""
		return rc, nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/tasks/"+testInstance+"/cancellations", testKey, `"rev-9"`,
		map[string]any{"reason": "需求撤回"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
	op := decode(t, rec)["operation"].(map[string]any)
	if op["action"] != "cancel" || op["resource"] != "/api/v1/tasks/"+testInstance {
		t.Fatalf("动作/资源不正确: %+v", op)
	}
	if got.Instance != testInstance || got.Reason != "需求撤回" || got.ExpectedRevision != 9 {
		t.Fatalf("用例输入不正确: %+v", got)
	}
}

func TestRepeatedCancelDeterministic(t *testing.T) {
	fake := &fakeCommands{cancelFn: func(context.Context, CancelCommand) (Receipt, error) {
		return Receipt{}, ErrInvalid{Code: "invalid_transition", Message: "任务已终止，不能再次取消"}
	}}
	h := newAPI(fake)
	for i := 0; i < 2; i++ {
		rec := doReq(t, h, http.MethodPost,
			"/api/v1/tasks/"+testInstance+"/cancellations", testKey, `"rev-9"`,
			map[string]any{"reason": "重复取消"})
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("第 %d 次重复取消应为 422，实际 %d", i+1, rec.Code)
		}
		if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "invalid_transition" {
			t.Fatalf("第 %d 次错误码不正确: %v", i+1, code)
		}
	}
}

func TestDeliveryAccepted(t *testing.T) {
	var got DeliveryCommand
	fake := &fakeCommands{deliverFn: func(_ context.Context, cmd DeliveryCommand) (Receipt, error) {
		got = cmd
		rc := queuedReceipt()
		rc.Action = ""
		return rc, nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/runs/"+testRun+"/deliveries", testKey, `"rev-5"`,
		map[string]any{"generation": 2, "commit": "abcdef1", "note": "完成"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if got.Generation != 2 || got.Commit != "abcdef1" || got.ExpectedRevision != 5 || got.RunID != testRun {
		t.Fatalf("交付输入不正确: %+v", got)
	}
	op := decode(t, rec)["operation"].(map[string]any)
	if op["action"] != "deliver" {
		t.Fatalf("动作不正确: %+v", op)
	}
}

func TestDeliveryRequiresGeneration(t *testing.T) {
	fake := &fakeCommands{}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/runs/"+testRun+"/deliveries", testKey, `"rev-5"`,
		map[string]any{"commit": "abcdef1"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺 generation 应为 422，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "generation_required" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestDeliveryNotOwner(t *testing.T) {
	fake := &fakeCommands{deliverFn: func(context.Context, DeliveryCommand) (Receipt, error) {
		return Receipt{}, ErrConflict{Code: "not_owner", Message: "该运行不属于当前工作者的任务"}
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/runs/"+testRun+"/deliveries", testKey, `"rev-5"`,
		map[string]any{"generation": 2, "commit": "abcdef1"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("未授权交付应为 409，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "not_owner" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestBusyMapsTo503(t *testing.T) {
	fake := &fakeCommands{claimFn: func(context.Context, ClaimCommand) (Receipt, error) {
		return Receipt{}, ErrBusy{RetryAfter: 2 * time.Second}
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("状态码应为 503，实际 %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "2" {
		t.Fatalf("Retry-After 不正确: %q", rec.Header().Get("Retry-After"))
	}
	errBody := decode(t, rec)["error"].(map[string]any)
	if errBody["code"] != "busy" || errBody["retryable"] != true {
		t.Fatalf("错误体不正确: %+v", errBody)
	}
}

func TestReviewVerificationMergeAccepted(t *testing.T) {
	fake := &fakeCommands{
		verifyFn: func(_ context.Context, cmd VerificationCommand) (Receipt, error) {
			if cmd.DeliveryID != testDelivery || cmd.Mode != "all" || cmd.ExpectedRevision != 3 {
				t.Fatalf("验证输入不正确: %+v", cmd)
			}
			return queuedReceipt(), nil
		},
		reviewFn: func(_ context.Context, cmd ReviewCommand) (Receipt, error) {
			if cmd.Decision != "accept" || cmd.DeliveryID != testDelivery {
				t.Fatalf("评审输入不正确: %+v", cmd)
			}
			return queuedReceipt(), nil
		},
		mergeFn: func(_ context.Context, cmd MergeCommand) (Receipt, error) {
			if cmd.Strategy != "squash" || cmd.DeliveryID != testDelivery {
				t.Fatalf("合入输入不正确: %+v", cmd)
			}
			return queuedReceipt(), nil
		},
	}
	h := newAPI(fake)

	verify := doReq(t, h, http.MethodPost,
		"/api/v1/deliveries/"+testDelivery+"/verifications", testKey, `"rev-3"`,
		map[string]any{"mode": "all"})
	if verify.Code != http.StatusAccepted {
		t.Fatalf("验证应为 202，实际 %d: %s", verify.Code, verify.Body.String())
	}

	review := doReq(t, h, http.MethodPost,
		"/api/v1/tasks/"+testInstance+"/reviews", testKey, `"rev-3"`,
		map[string]any{"delivery_id": testDelivery, "decision": "accept"})
	if review.Code != http.StatusAccepted {
		t.Fatalf("评审应为 202，实际 %d: %s", review.Code, review.Body.String())
	}

	merge := doReq(t, h, http.MethodPost,
		"/api/v1/tasks/"+testInstance+"/merges", testKey, `"rev-3"`,
		map[string]any{"delivery_id": testDelivery})
	if merge.Code != http.StatusAccepted {
		t.Fatalf("合入应为 202，实际 %d: %s", merge.Code, merge.Body.String())
	}
}

func TestInvalidReviewDecision(t *testing.T) {
	fake := &fakeCommands{}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/tasks/"+testInstance+"/reviews", testKey, `"rev-3"`,
		map[string]any{"delivery_id": testDelivery, "decision": "maybe"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法结论应为 422，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "invalid_transition" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestCancelRunAcceptsGenerationOnly(t *testing.T) {
	var got CancelRunCommand
	fake := &fakeCommands{cancelRunFn: func(_ context.Context, cmd CancelRunCommand) (Receipt, error) {
		got = cmd
		return queuedReceipt(), nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/runs/"+testRun+"/cancellations", testKey, "",
		map[string]any{"generation": 4, "reason": "超时"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if got.Generation != 4 || got.ExpectedRevision != nil || got.Reason != "超时" {
		t.Fatalf("运行停止输入不正确: %+v", got)
	}
}

func TestCancelRunRequiresSomeCondition(t *testing.T) {
	fake := &fakeCommands{}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/runs/"+testRun+"/cancellations", testKey, "", map[string]any{"reason": "超时"})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺条件应为 422，实际 %d", rec.Code)
	}
}

func TestNilPortReturns501(t *testing.T) {
	rec := doReq(t, (&API{}).Handler(), http.MethodPost,
		"/api/v1/projects/"+testProject+"/claims", testKey, `"rev-42"`, map[string]any{})
	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("未接线端口应为 501，实际 %d", rec.Code)
	}
	if code := decode(t, rec)["error"].(map[string]any)["code"]; code != "unsupported" {
		t.Fatalf("错误码不正确: %v", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	rec := doReq(t, newAPI(&fakeCommands{}), http.MethodGet,
		"/api/v1/projects/"+testProject+"/claims", "", "", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("非 POST 应为 405，实际 %d", rec.Code)
	}
}

func TestDispatchAccepted(t *testing.T) {
	var got DispatchCommand
	fake := &fakeCommands{dispatchFn: func(_ context.Context, cmd DispatchCommand) (Receipt, error) {
		got = cmd
		return queuedReceipt(), nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/projects/"+testProject+"/dispatches", testKey, `"rev-1"`,
		map[string]any{"task_instance": testInstance, "node_id": "node-a"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
	if got.ProjectID != testProject || got.RequestedTask == nil || *got.RequestedTask != testInstance || got.NodeID != "node-a" {
		t.Fatalf("派发输入不正确: %+v", got)
	}
	if got.ExpectedRevision != 1 {
		t.Fatalf("派发 revision 不正确: %+v", got)
	}
}

func TestRetryLegacyFormats(t *testing.T) {
	fake := &fakeCommands{retryFn: func(_ context.Context, cmd RetryCommand) (Receipt, error) {
		if cmd.Instance != testInstance || cmd.ExpectedRevision != 11 {
			t.Fatalf("重试输入不正确: %+v", cmd)
		}
		return queuedReceipt(), nil
	}}
	rec := doReq(t, newAPI(fake), http.MethodPost,
		"/api/v1/tasks/"+testInstance+"/retries", testKey, "",
		map[string]any{"expected_revision": 11, "reason": "重新执行"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("状态码应为 202，实际 %d: %s", rec.Code, rec.Body.String())
	}
}
