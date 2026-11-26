package reconcile

import (
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/state"
)

// EvidenceSource 说明一条证据来自哪里：受管进程观测、中枢运行时事实或人工核对。
type EvidenceSource string

// 三种来源；人工来源必须记录处理人。
const (
	SourceProcess EvidenceSource = "process"
	SourceRuntime EvidenceSource = "runtime"
	SourceManual  EvidenceSource = "manual"
)

// Valid 报告来源是否属于封闭集合。
func (s EvidenceSource) Valid() bool {
	return s == SourceProcess || s == SourceRuntime || s == SourceManual
}

// EvidenceRef 是一条可追溯的证据引用。人工证据缺少处理人就不能顶替自动观测。
type EvidenceRef struct {
	Source EvidenceSource `json:"source"`
	// Ref 是稳定引用：日志位置、身份记录或人工处置单号，必须能回到原始事实。
	Ref   string    `json:"ref"`
	Actor string    `json:"actor,omitempty"`
	At    time.Time `json:"at"`
	Note  string    `json:"note,omitempty"`
}

// Validate 拒绝无法追溯或缺少处理人的人工证据。
func (e EvidenceRef) Validate(path string) []*model.Error {
	var out []*model.Error
	if !e.Source.Valid() {
		out = append(out, model.Violationf(model.CodeRuntimeFieldInvalid, path+".source",
			"只能是 process/runtime/manual，收到 %q", e.Source))
	}
	if strings.TrimSpace(e.Ref) == "" {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, path+".ref",
			"证据必须能回到原始事实，不能只写结论"))
	}
	if e.Source == SourceManual && strings.TrimSpace(e.Actor) == "" {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, path+".actor",
			"人工证据必须记录处理人"))
	}
	if e.At.IsZero() {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, path+".at", "必须记录证据时间"))
	}
	return out
}

// Observation 是上层读到的进程事实（来自 VN-403 的停止证据或人工核对）。
// 本包不自己探测进程，也不杀进程；它只依据这些事实决定能否解除占用。
type Observation struct {
	Source EvidenceSource
	Ref    string
	Actor  string
	At     time.Time

	// Found 表示按 PID 与启动时间确认原进程仍在运行。
	Found bool
	// PIDReused 表示该 PID 已被别的进程占用（启动时间不一致）。
	PIDReused bool
	// Lost 表示按身份记录找不到原进程。
	Lost bool
	// GroupEmpty 表示记录的进程组已确认没有存活成员。
	GroupEmpty bool
	// DescendantsConfirmed 只有进程组清空或人工明确核对才为真。
	DescendantsConfirmed bool
	// ExitCode 仅在父进程已退出且已知时有效；未知用 -1。
	ExitCode int
	Note     string
}

func (o Observation) validate(path string) []*model.Error {
	ref := EvidenceRef{Source: o.Source, Ref: o.Ref, Actor: o.Actor, At: o.At, Note: o.Note}
	out := ref.Validate(path)
	if o.Found && o.DescendantsConfirmed {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, path+".descendants_confirmed",
			"进程仍在运行时不能宣布后代已确认停止"))
	}
	if o.Found && o.PIDReused {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, path+".pid_reused",
			"同一 PID 不能既找到原进程又被判定复用"))
	}
	return out
}

func (o Observation) evidence(at time.Time) EvidenceRef {
	if at.IsZero() {
		at = o.At
	}
	note := o.Note
	if note == "" {
		switch {
		case o.Found:
			note = "进程仍在运行"
		case o.DescendantsConfirmed:
			note = "进程组已确认清空"
		case o.PIDReused:
			note = "PID 已复用，后代未确认"
		default:
			note = "后代停止未确认"
		}
	}
	return EvidenceRef{Source: o.Source, Ref: o.Ref, Actor: o.Actor, At: at, Note: note}
}

// Verdict 是一次核实建议。Phase 为空串表示保持当前阶段；KeepOccupancy 为真时
// 节点槽与目录都不得重新分配。
type Verdict struct {
	RunID          model.RunID               `json:"run_id"`
	Phase          model.RunPhase            `json:"phase,omitempty"`
	Reconciliation model.ReconciliationState `json:"reconciliation_state"`
	StopConfirmed  bool                      `json:"process_stop_confirmed"`
	KeepOccupancy  bool                      `json:"keep_occupancy"`
	Evidence       []EvidenceRef             `json:"evidence,omitempty"`
	Notes          []string                  `json:"notes,omitempty"`
}

// Request 是核实输入。Outcome 为空表示结果尚未判定（VN-206 之前），
// 此时只能确认停止，不能补造终态。
type Request struct {
	Run         *model.Run
	Observation Observation
	Outcome     *model.Outcome
	Now         time.Time
}

// Heartbeat 把「心跳失联」转成核实结论。
//
// 心跳丢失本身不是停止证据：默认进入 unknown 并保留占用。只有观测到后代
// 确认停止、且结果已判定时，才允许补录终态；否则继续保持占用等待核实。
func Heartbeat(req Request) (Verdict, error) {
	if req.Run == nil {
		return Verdict{}, model.Violation(model.CodeRuntimeFieldInvalid, "run", "缺少运行")
	}
	if errs := req.Observation.validate("observation"); len(errs) > 0 {
		return Verdict{}, errs[0]
	}
	now := req.Now
	if now.IsZero() {
		now = req.Observation.At
	}
	v := Verdict{
		RunID:          req.Run.RunID,
		Phase:          req.Run.Phase,
		Reconciliation: req.Run.Reconciliation,
		StopConfirmed:  req.Run.ProcessStopConfirmed,
		KeepOccupancy:  req.Run.Occupies(),
		Evidence:       []EvidenceRef{req.Observation.evidence(now)},
	}

	if req.Run.Phase.Terminal() {
		v.Notes = append(v.Notes, "运行已是终态，心跳失联不改写结果")
		return v, nil
	}

	switch {
	case req.Observation.Found:
		v.Reconciliation = model.ReconciliationUnknown
		v.KeepOccupancy = true
		v.Notes = append(v.Notes, "进程仍在运行：心跳丢失只进入核实，不落终态")
		return v, nil
	case !req.Observation.DescendantsConfirmed:
		v.Reconciliation = model.ReconciliationUnknown
		v.KeepOccupancy = true
		if req.Observation.PIDReused {
			v.Notes = append(v.Notes, "PID 已被复用：原进程与后代都无法确认，保留占用")
		} else {
			v.Notes = append(v.Notes, "后代停止未确认：保留占用并等待核实")
		}
		return v, nil
	}

	// 后代已确认停止：可以记停止事实，但是否落终态取决于结果是否已判定。
	v.StopConfirmed = true
	if req.Outcome == nil || *req.Outcome == model.OutcomeUnknown {
		v.Reconciliation = model.ReconciliationUnknown
		v.KeepOccupancy = true
		v.Notes = append(v.Notes, "停止已确认，但结果未判定：保留占用，不补造结果")
		return v, nil
	}
	target, err := phaseForOutcome(*req.Outcome)
	if err != nil {
		return Verdict{}, err
	}
	next := *req.Run
	next.Reconciliation = model.ReconciliationConfirmed
	next.ProcessStopConfirmed = true
	next.Evidence = append(append([]string{}, req.Run.Evidence...), v.Evidence[0].Ref)
	if err := state.CanEnterPhase(&next, target); err != nil {
		return Verdict{}, err
	}
	next.Phase = target
	v.Phase = target
	v.Reconciliation = model.ReconciliationConfirmed
	v.KeepOccupancy = next.Occupies()
	v.Notes = append(v.Notes, "后代停止与结果均已确认，可补录终态")
	return v, nil
}

func phaseForOutcome(o model.Outcome) (model.RunPhase, error) {
	switch o {
	case model.OutcomeCompleted:
		return model.PhaseCompleted, nil
	case model.OutcomeFailed:
		return model.PhaseFailed, nil
	case model.OutcomeCancelled:
		return model.PhaseCancelled, nil
	case model.OutcomeTimedOut:
		return model.PhaseTimedOut, nil
	}
	return "", model.Violationf(model.CodeRuntimeFieldInvalid, "outcome", "无法补录未知结果 %q", o)
}

// CancelRequest 是一次取消操作：代际与幂等键必填。
type CancelRequest struct {
	Run        *model.Run
	Generation model.Generation
	Key        string
}

// CancelPlan 是取消请求的确定结论。already_finished 时必须保留原结果。
type CancelPlan struct {
	Result        model.OperationResult `json:"result"`
	Phase         model.RunPhase        `json:"phase,omitempty"`
	DesiredAction model.DesiredAction   `json:"desired_action"`
	KeepOccupancy bool                  `json:"keep_occupancy"`
	Note          string                `json:"note,omitempty"`
}

// Cancel 裁定取消与正常完成的竞争。
//
// 顺序按持久事实（阶段与停止确认）判定，不按请求到达顺序：结束事实先到，
// 取消只能 already_finished 并保留原结果；取消只是意图，cancelled 是事实，
// 中间必须隔着停止确认。
func Cancel(req CancelRequest) (CancelPlan, error) {
	if req.Run == nil {
		return CancelPlan{}, model.Violation(model.CodeRuntimeFieldInvalid, "run", "缺少运行")
	}
	if strings.TrimSpace(req.Key) == "" {
		return CancelPlan{}, model.Violation(model.CodeClaimWithoutGuards, "operation.idempotency_key",
			"取消是持久操作，必须带幂等键")
	}
	if !req.Generation.Valid() || req.Generation != req.Run.Generation {
		return CancelPlan{}, model.Violationf(model.CodeReleaseGeneration, "operation.generation",
			"必须等于运行当前代际 %d，收到 %d", req.Run.Generation, req.Generation)
	}

	decision, err := state.ResolveCancel(req.Run)
	if err != nil {
		return CancelPlan{}, err
	}
	switch decision {
	case state.CancelAlreadyFinished:
		return CancelPlan{
			Result:        model.ResultAlreadyFinished,
			Phase:         req.Run.Phase,
			DesiredAction: model.ActionNone,
			KeepOccupancy: req.Run.Occupies(),
			Note:          "结束事实先到：保留原结果，不得追改成 cancelled",
		}, nil
	case state.CancelApplied:
		return CancelPlan{
			Result:        model.ResultApplied,
			Phase:         model.PhaseCancelled,
			DesiredAction: model.ActionNone,
			KeepOccupancy: false,
			Note:          "停止已确认，可以落 cancelled",
		}, nil
	}

	// 意图先落盘：进入 stopping（已在 stopping 则保持），由 Runner 停止并留证据。
	target := req.Run.Phase
	if req.Run.Phase != model.PhaseStopping {
		target = model.PhaseStopping
		if !model.CanTransition(req.Run.Phase, target) {
			return CancelPlan{}, model.Violationf(model.CodePhaseTransitionIllegal, "run.phase",
				"%s 无法进入 stopping", req.Run.Phase)
		}
	}
	return CancelPlan{
		Result:        model.ResultPending,
		Phase:         target,
		DesiredAction: model.ActionStop,
		KeepOccupancy: true,
		Note:          "取消是意图：等停止确认后再落 cancelled",
	}, nil
}

// StopPlan 是显式「停止本次运行」的建议；它保留任务与已有结果。
type StopPlan struct {
	Allowed       bool                `json:"allowed"`
	Phase         model.RunPhase      `json:"phase,omitempty"`
	DesiredAction model.DesiredAction `json:"desired_action"`
	KeepOccupancy bool                `json:"keep_occupancy"`
	Note          string              `json:"note,omitempty"`
}

// PlanStop 校验显式停止：非终态、代际一致。停止不取消业务任务。
func PlanStop(run *model.Run, gen model.Generation) (StopPlan, error) {
	if run == nil {
		return StopPlan{}, model.Violation(model.CodeRuntimeFieldInvalid, "run", "缺少运行")
	}
	if !gen.Valid() || gen != run.Generation {
		return StopPlan{}, model.Violationf(model.CodeReleaseGeneration, "run.generation",
			"必须等于运行当前代际 %d，收到 %d", run.Generation, gen)
	}
	if run.Phase.Terminal() {
		return StopPlan{
			Allowed:       false,
			Phase:         run.Phase,
			DesiredAction: model.ActionNone,
			KeepOccupancy: run.Occupies(),
			Note:          "运行已结束，停止只对进行中的运行有意义",
		}, nil
	}
	phase := run.Phase
	if phase != model.PhaseStopping {
		phase = model.PhaseStopping
		if !model.CanTransition(run.Phase, phase) {
			return StopPlan{}, model.Violationf(model.CodePhaseTransitionIllegal, "run.phase",
				"%s 无法进入 stopping", run.Phase)
		}
	}
	return StopPlan{
		Allowed:       true,
		Phase:         phase,
		DesiredAction: model.ActionStop,
		KeepOccupancy: true,
		Note:          "停止运行保留任务与已有结果",
	}, nil
}

// TaskCancelPlan 是业务任务能否落 cancelled 的结论。
type TaskCancelPlan struct {
	Allowed bool             `json:"allowed"`
	Status  model.TaskStatus `json:"status,omitempty"`
	Note    string           `json:"note,omitempty"`
}

// PlanTaskCancel 校验任务取消：必须先让运行停稳、后代确认停止且不再占用。
func PlanTaskCancel(run *model.Run) TaskCancelPlan {
	if run == nil {
		return TaskCancelPlan{Allowed: true, Status: model.TaskCancelled,
			Note: "没有进行中的运行，可直接取消任务"}
	}
	if !run.Phase.Terminal() || !run.ProcessStopConfirmed || run.Occupies() {
		return TaskCancelPlan{Allowed: false,
			Note: "运行未停稳或仍计入占用：先停止并确认后代停止，再取消任务"}
	}
	return TaskCancelPlan{Allowed: true, Status: model.TaskCancelled,
		Note: "运行已结束且占用已解除，可以取消任务"}
}

// RetryPolicy 是有界重试策略：次数与延迟都有上限，不无限重发。
type RetryPolicy struct {
	MaxAttempts int           `json:"max_attempts"`
	BaseDelay   time.Duration `json:"base_delay"`
	MaxDelay    time.Duration `json:"max_delay"`
}

// DefaultRetryPolicy 给出保守默认：最多 3 次，延迟 1s 起、封顶 1m。
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Minute}
}

func (p RetryPolicy) validate() []*model.Error {
	var out []*model.Error
	if p.MaxAttempts < 1 {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "policy.max_attempts",
			"重试次数必须为正，否则重试没有边界"))
	}
	if p.BaseDelay <= 0 || p.MaxDelay < p.BaseDelay {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "policy.delay",
			"基础延迟必须为正，且封顶不得小于基础延迟"))
	}
	return out
}

func (p RetryPolicy) delay(attempt int) time.Duration {
	if attempt <= 1 {
		return 0
	}
	d := p.BaseDelay
	for i := 2; i < attempt; i++ {
		if d >= p.MaxDelay/2 {
			return p.MaxDelay
		}
		d *= 2
	}
	if d > p.MaxDelay {
		return p.MaxDelay
	}
	return d
}

// RetryRequest 是一次重试资格判定；Attempts 是已经用掉的尝试次数。
type RetryRequest struct {
	Run        *model.Run
	Generation model.Generation
	Attempts   int
	Policy     RetryPolicy
}

// RetryPlan 是重试结论；Allowed 为假时保留占用，不重发。
type RetryPlan struct {
	Allowed    bool             `json:"allowed"`
	Generation model.Generation `json:"generation,omitempty"`
	Attempt    int              `json:"attempt,omitempty"`
	Delay      time.Duration    `json:"delay,omitempty"`
	Note       string           `json:"note,omitempty"`
}

// PlanRetry 校验重试的前驱与占用：前驱必须已结束、后代已确认停止且不再占用；
// 次数达上限或代际过期时拒绝。重试一律开新代际，不复活旧 Run。
func PlanRetry(req RetryRequest) (RetryPlan, error) {
	if errs := req.Policy.validate(); len(errs) > 0 {
		return RetryPlan{}, errs[0]
	}
	if req.Attempts < 0 {
		return RetryPlan{}, model.Violation(model.CodeRuntimeFieldInvalid, "attempts", "已用次数不得为负")
	}
	if req.Run == nil {
		if req.Generation.Valid() && req.Generation != 1 {
			return RetryPlan{}, model.Violationf(model.CodeReleaseGeneration, "retry.generation",
				"没有前驱运行时新一代必须从 1 开始，收到 %d", req.Generation)
		}
		if req.Attempts >= req.Policy.MaxAttempts {
			return RetryPlan{Allowed: false, Note: "重试次数已用尽"}, nil
		}
		return RetryPlan{Allowed: true, Generation: 1, Attempt: req.Attempts + 1}, nil
	}
	if !req.Generation.Valid() || req.Generation != req.Run.Generation {
		return RetryPlan{}, model.Violationf(model.CodeReleaseGeneration, "retry.generation",
			"必须等于前驱运行代际 %d，收到 %d", req.Run.Generation, req.Generation)
	}
	if !req.Run.Phase.Terminal() || !req.Run.ProcessStopConfirmed || req.Run.Occupies() {
		return RetryPlan{Allowed: false,
			Note: "前驱运行未确认结束：保留占用，不得重发"}, nil
	}
	if req.Attempts >= req.Policy.MaxAttempts {
		return RetryPlan{Allowed: false, Note: "重试次数已用尽，等待人工处置"}, nil
	}
	attempt := req.Attempts + 1
	return RetryPlan{
		Allowed:    true,
		Generation: req.Run.Generation + 1,
		Attempt:    attempt,
		Delay:      req.Policy.delay(attempt),
		Note:       "新一代际重试；旧代际写入一律拒绝",
	}, nil
}

// ResumeRequest 是一次会话恢复判定。SessionRef 必须由来源记录明确给出。
type ResumeRequest struct {
	Run            *model.Run
	Generation     model.Generation
	SessionRef     string
	SessionQuality model.Quality
	Now            time.Time
}

// ResumePlan 是恢复结论；来源会话无法确认时一律不允许。
type ResumePlan struct {
	Allowed    bool             `json:"allowed"`
	Generation model.Generation `json:"generation,omitempty"`
	Note       string           `json:"note,omitempty"`
	Evidence   []EvidenceRef    `json:"evidence,omitempty"`
}

// PlanResume 校验会话恢复：来源会话必须明确且质量可确认；前驱必须已停稳并解除占用。
// 不猜测来源会话，也不通过换目录绕过未知执行。
func PlanResume(req ResumeRequest) (ResumePlan, error) {
	if strings.TrimSpace(req.SessionRef) == "" {
		return ResumePlan{Allowed: false, Note: "来源会话未确认：不得猜测恢复"}, nil
	}
	if !req.SessionQuality.Valid() || req.SessionQuality == model.QualityUnknown {
		return ResumePlan{Allowed: false, Note: "会话归属质量不足：不得恢复"}, nil
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	ev := EvidenceRef{
		Source: SourceRuntime,
		Ref:    req.SessionRef,
		At:     now,
		Note:   "来源会话（质量 " + string(req.SessionQuality) + "）",
	}
	if reqs := ev.Validate("resume.evidence"); len(reqs) > 0 {
		return ResumePlan{}, reqs[0]
	}
	if req.Run == nil {
		if req.Generation.Valid() && req.Generation != 1 {
			return ResumePlan{}, model.Violationf(model.CodeReleaseGeneration, "resume.generation",
				"没有前驱运行时新一代必须从 1 开始，收到 %d", req.Generation)
		}
		return ResumePlan{Allowed: true, Generation: 1, Evidence: []EvidenceRef{ev},
			Note: "首次运行可按来源会话恢复"}, nil
	}
	if !req.Generation.Valid() || req.Generation != req.Run.Generation {
		return ResumePlan{}, model.Violationf(model.CodeReleaseGeneration, "resume.generation",
			"必须等于前驱运行代际 %d，收到 %d", req.Run.Generation, req.Generation)
	}
	if !req.Run.Phase.Terminal() || !req.Run.ProcessStopConfirmed || req.Run.Occupies() {
		return ResumePlan{Allowed: false, Evidence: []EvidenceRef{ev},
			Note: "前驱运行未停稳或仍计入占用：不得恢复"}, nil
	}
	return ResumePlan{
		Allowed:    true,
		Generation: req.Run.Generation + 1,
		Evidence:   []EvidenceRef{ev},
		Note:       "前驱已停稳，按明确来源会话恢复为新代际",
	}, nil
}

// ManualEvidence 把人工核对结果转成证据引用；缺少处理人时拒绝。
func ManualEvidence(ref, actor string, at time.Time, note string) (EvidenceRef, error) {
	ev := EvidenceRef{Source: SourceManual, Ref: ref, Actor: actor, At: at, Note: note}
	if errs := ev.Validate("evidence"); len(errs) > 0 {
		return EvidenceRef{}, fmt.Errorf("reconcile: %s", errs[0].Msg)
	}
	return ev, nil
}
