package commands

import (
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/localauth"
)

// API 汇总命令端点用到的写用例端口。任一端口为空时对应端点返回 500。
type API struct {
	Dispatcher    Dispatcher
	Claims        Claimer
	Tasks         TaskCommands
	Runs          RunCommands
	Deliveries    Deliveries
	Verifications Verifications
	Reviews       Reviews
	Merges        Merges
	// Auth 为空时按「仅本机调用」处理，不挂鉴权中间件；服务装配必须显式注入。
	Auth *localauth.Authenticator
}

// Handler 返回挂好路由与写作用域鉴权的 http.Handler。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/projects/{project_id}/dispatches", a.handleDispatch)
	mux.HandleFunc("POST /api/v1/projects/{project_id}/claims", a.handleClaim)
	mux.HandleFunc("POST /api/v1/tasks/{instance_id}/cancellations", a.handleCancelTask)
	mux.HandleFunc("POST /api/v1/tasks/{instance_id}/retries", a.handleRetry)
	mux.HandleFunc("POST /api/v1/runs/{run_id}/cancellations", a.handleCancelRun)
	mux.HandleFunc("POST /api/v1/runs/{run_id}/deliveries", a.handleDelivery)
	mux.HandleFunc("POST /api/v1/deliveries/{delivery_id}/verifications", a.handleVerification)
	mux.HandleFunc("POST /api/v1/tasks/{instance_id}/reviews", a.handleReview)
	mux.HandleFunc("POST /api/v1/tasks/{instance_id}/merges", a.handleMerge)
	if a.Auth != nil {
		return a.Auth.Middleware(a.Auth.RequireScope(localauth.ScopeWrite, mux))
	}
	return mux
}

// ---- 请求体 ----

type capacityBody struct {
	NodeSlotLimit int `json:"node_slot_limit"`
	ManagedLimit  int `json:"managed_limit"`
}

type dispatchBody struct {
	TaskInstance     string        `json:"task_instance,omitempty"`
	NodeID           string        `json:"node_id,omitempty"`
	PredecessorRunID string        `json:"predecessor_run_id,omitempty"`
	Capacity         *capacityBody `json:"capacity,omitempty"`
	ExpectedRevision *int64        `json:"expected_revision,omitempty"`
}

func (b *dispatchBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *dispatchBody) generationValue() *int64 { return nil }

type claimBody struct {
	TaskInstance     string        `json:"task_instance,omitempty"`
	NodeID           string        `json:"node_id,omitempty"`
	Workspace        string        `json:"workspace,omitempty"`
	Capacity         *capacityBody `json:"capacity,omitempty"`
	ExpectedRevision *int64        `json:"expected_revision,omitempty"`
}

func (b *claimBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *claimBody) generationValue() *int64 { return nil }

type taskActionBody struct {
	Reason           string `json:"reason,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func (b *taskActionBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *taskActionBody) generationValue() *int64 { return nil }

type cancelRunBody struct {
	Reason           string `json:"reason,omitempty"`
	Generation       *int64 `json:"generation,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func (b *cancelRunBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *cancelRunBody) generationValue() *int64 { return b.Generation }

type deliveryBody struct {
	Generation       *int64 `json:"generation,omitempty"`
	Commit           string `json:"commit,omitempty"`
	Note             string `json:"note,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func (b *deliveryBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *deliveryBody) generationValue() *int64 { return b.Generation }

type verificationBody struct {
	Mode             string `json:"mode,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func (b *verificationBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *verificationBody) generationValue() *int64 { return nil }

type reviewBody struct {
	DeliveryID       string `json:"delivery_id"`
	Decision         string `json:"decision"`
	Reason           string `json:"reason,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func (b *reviewBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *reviewBody) generationValue() *int64 { return nil }

type mergeBody struct {
	DeliveryID       string `json:"delivery_id"`
	Strategy         string `json:"strategy,omitempty"`
	ExpectedRevision *int64 `json:"expected_revision,omitempty"`
}

func (b *mergeBody) revisionValue() *int64   { return b.ExpectedRevision }
func (b *mergeBody) generationValue() *int64 { return nil }

// ---- 处理器 ----

func (a *API) handleDispatch(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathProject(w, r)
	if !ok {
		return
	}
	var body dispatchBody
	m, ok := begin(w, r, "dispatch", projectPath(projectID), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	instance, ok := optionalInstance(w, body.TaskInstance)
	if !ok {
		return
	}
	node, ok := optionalNode(w, body.NodeID)
	if !ok {
		return
	}
	var predecessor *model.RunID
	if raw := strings.TrimSpace(body.PredecessorRunID); raw != "" {
		id := model.RunID(raw)
		if !id.Valid() {
			writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "predecessor_run_id 形态非法", nil)
			return
		}
		predecessor = &id
	}
	if a.Dispatcher == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "派发端口未接线", nil)
		return
	}
	rc, err := a.Dispatcher.Dispatch(r.Context(), DispatchCommand{
		ProjectID:        projectID,
		Actor:            m.actor,
		RequestedTask:    instance,
		NodeID:           node,
		PredecessorRunID: predecessor,
		Capacity:         capacity(body.Capacity),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleClaim(w http.ResponseWriter, r *http.Request) {
	projectID, ok := pathProject(w, r)
	if !ok {
		return
	}
	var body claimBody
	m, ok := begin(w, r, "claim", projectPath(projectID), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	instance, ok := optionalInstance(w, body.TaskInstance)
	if !ok {
		return
	}
	node, ok := optionalNode(w, body.NodeID)
	if !ok {
		return
	}
	if a.Claims == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "领取端口未接线", nil)
		return
	}
	rc, err := a.Claims.Claim(r.Context(), ClaimCommand{
		ProjectID:        projectID,
		Actor:            m.actor,
		RequestedTask:    instance,
		NodeID:           node,
		Workspace:        strings.TrimSpace(body.Workspace),
		Capacity:         capacity(body.Capacity),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	instance, ok := pathInstance(w, r)
	if !ok {
		return
	}
	var body taskActionBody
	m, ok := begin(w, r, "cancel", taskPath(instance), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	if a.Tasks == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "任务命令端口未接线", nil)
		return
	}
	rc, err := a.Tasks.Cancel(r.Context(), CancelCommand{
		Instance:         instance,
		Actor:            m.actor,
		Reason:           strings.TrimSpace(body.Reason),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleRetry(w http.ResponseWriter, r *http.Request) {
	instance, ok := pathInstance(w, r)
	if !ok {
		return
	}
	var body taskActionBody
	m, ok := begin(w, r, "retry", taskPath(instance), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	if a.Tasks == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "任务命令端口未接线", nil)
		return
	}
	rc, err := a.Tasks.Retry(r.Context(), RetryCommand{
		Instance:         instance,
		Actor:            m.actor,
		Reason:           strings.TrimSpace(body.Reason),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleCancelRun(w http.ResponseWriter, r *http.Request) {
	runID := model.RunID(strings.TrimSpace(r.PathValue("run_id")))
	if !runID.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "run_id 形态非法", nil)
		return
	}
	var body cancelRunBody
	m, ok := begin(w, r, "cancel_run", runPath(runID), &body)
	if !ok {
		return
	}
	// 运行级停止允许只用 generation 作为条件；两者都没有才是缺条件。
	if m.revision == nil && m.generation == nil {
		writeErr(w, http.StatusUnprocessableEntity, "revision_required", "运行停止必须带 generation 或 expected_revision", nil)
		return
	}
	if a.Runs == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "运行命令端口未接线", nil)
		return
	}
	cmd := CancelRunCommand{
		RunID:          runID,
		Actor:          m.actor,
		Reason:         strings.TrimSpace(body.Reason),
		IdempotencyKey: m.key,
		Fingerprint:    m.fingerprint,
	}
	if m.revision != nil {
		cmd.ExpectedRevision = m.revision
	}
	if m.generation != nil {
		cmd.Generation = *m.generation
	}
	rc, err := a.Runs.CancelRun(r.Context(), cmd)
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleDelivery(w http.ResponseWriter, r *http.Request) {
	runID := model.RunID(strings.TrimSpace(r.PathValue("run_id")))
	if !runID.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "run_id 形态非法", nil)
		return
	}
	var body deliveryBody
	m, ok := begin(w, r, "deliver", runPath(runID), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	if m.generation == nil {
		writeErr(w, http.StatusUnprocessableEntity, "generation_required", "交付必须带 generation", nil)
		return
	}
	commit := model.Commit(strings.TrimSpace(body.Commit))
	if commit != "" && !commit.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "commit 形态非法", nil)
		return
	}
	if a.Deliveries == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "交付端口未接线", nil)
		return
	}
	rc, err := a.Deliveries.Deliver(r.Context(), DeliveryCommand{
		RunID:            runID,
		Actor:            m.actor,
		Generation:       *m.generation,
		Commit:           commit,
		Note:             strings.TrimSpace(body.Note),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleVerification(w http.ResponseWriter, r *http.Request) {
	deliveryID := model.DeliveryID(strings.TrimSpace(r.PathValue("delivery_id")))
	if !deliveryID.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "delivery_id 形态非法", nil)
		return
	}
	var body verificationBody
	m, ok := begin(w, r, "verify", deliveryPath(deliveryID), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	if a.Verifications == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "验证端口未接线", nil)
		return
	}
	rc, err := a.Verifications.Verify(r.Context(), VerificationCommand{
		DeliveryID:       deliveryID,
		Actor:            m.actor,
		Mode:             strings.TrimSpace(body.Mode),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleReview(w http.ResponseWriter, r *http.Request) {
	instance, ok := pathInstance(w, r)
	if !ok {
		return
	}
	var body reviewBody
	m, ok := begin(w, r, "review", taskPath(instance), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	deliveryID := model.DeliveryID(strings.TrimSpace(body.DeliveryID))
	if !deliveryID.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "delivery_id 形态非法", nil)
		return
	}
	decision := strings.TrimSpace(body.Decision)
	if decision != "accept" && decision != "reject" {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_transition", "decision 只能是 accept 或 reject", nil)
		return
	}
	if a.Reviews == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "评审端口未接线", nil)
		return
	}
	rc, err := a.Reviews.Review(r.Context(), ReviewCommand{
		Instance:         instance,
		DeliveryID:       deliveryID,
		Actor:            m.actor,
		Decision:         decision,
		Reason:           strings.TrimSpace(body.Reason),
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

func (a *API) handleMerge(w http.ResponseWriter, r *http.Request) {
	instance, ok := pathInstance(w, r)
	if !ok {
		return
	}
	var body mergeBody
	m, ok := begin(w, r, "merge", taskPath(instance), &body)
	if !ok {
		return
	}
	if !requireRevision(m, w) {
		return
	}
	deliveryID := model.DeliveryID(strings.TrimSpace(body.DeliveryID))
	if !deliveryID.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "delivery_id 形态非法", nil)
		return
	}
	strategy := strings.TrimSpace(body.Strategy)
	if strategy == "" {
		strategy = "squash"
	}
	if a.Merges == nil {
		writeErr(w, http.StatusInternalServerError, "internal_error", "合入端口未接线", nil)
		return
	}
	rc, err := a.Merges.Merge(r.Context(), MergeCommand{
		Instance:         instance,
		Actor:            m.actor,
		DeliveryID:       deliveryID,
		Strategy:         strategy,
		ExpectedRevision: *m.revision,
		IdempotencyKey:   m.key,
		Fingerprint:      m.fingerprint,
	})
	if err != nil {
		mapUseCaseErr(w, err)
		return
	}
	writeReceipt(w, m, rc)
}

// ---- 路径与字段辅助 ----

func pathProject(w http.ResponseWriter, r *http.Request) (model.ProjectID, bool) {
	id := model.ProjectID(strings.TrimSpace(r.PathValue("project_id")))
	if !id.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "project_id 形态非法", nil)
		return "", false
	}
	return id, true
}

func pathInstance(w http.ResponseWriter, r *http.Request) (model.InstanceID, bool) {
	id := model.InstanceID(strings.TrimSpace(r.PathValue("instance_id")))
	if !id.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "instance_id 形态非法", nil)
		return "", false
	}
	return id, true
}

func optionalInstance(w http.ResponseWriter, raw string) (*model.InstanceID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	id := model.InstanceID(raw)
	if !id.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "task_instance 形态非法", nil)
		return nil, false
	}
	return &id, true
}

func optionalNode(w http.ResponseWriter, raw string) (model.NodeID, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", true
	}
	id := model.NodeID(raw)
	if !id.Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_request", "node_id 形态非法", nil)
		return "", false
	}
	return id, true
}

func capacity(raw *capacityBody) ports.Capacity {
	if raw == nil {
		return ports.Capacity{}
	}
	return ports.Capacity{NodeSlotLimit: raw.NodeSlotLimit, ManagedLimit: raw.ManagedLimit}
}

func projectPath(id model.ProjectID) string { return "/api/v1/projects/" + string(id) }
func taskPath(id model.InstanceID) string   { return "/api/v1/tasks/" + string(id) }
func runPath(id model.RunID) string         { return "/api/v1/runs/" + string(id) }
func deliveryPath(id model.DeliveryID) string {
	return "/api/v1/deliveries/" + string(id)
}
