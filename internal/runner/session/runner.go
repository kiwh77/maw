package session

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/observe"
)

// kindStreamRecord 是来源记录的 journal 类型。它是「一行原始记录」，
// 不是标准事件：解释发生在提交时（Decode），失败也不丢原始字节。
const kindStreamRecord = "stream.record"

// kindPhaseRecord 是阶段事实的 journal 类型。
//
// 阶段也要落盘：中枢掉线时启动／终态事实同样不能丢，恢复后按序号补交，
// 时间轴上不会凭空缺一段。
const kindPhaseRecord = "phase.record"

// kindCorruptRecord 是解析不了的原始行：只留证据，不产事件。
const kindCorruptRecord = "stream.record.corrupt"

// maxRecordBytes 是单条记录的上限；stream-json 的记录可能带整段消息。
const maxRecordBytes = 8 << 20

// Store 是应用侧事务端口。
type Store interface {
	Apply(ctx context.Context, req ports.StoreRequest) (ports.StoreResult, error)
}

// Source 是来源协议端口：解码一条记录、判定一次结果。
type Source interface {
	Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error)
	Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error)
}

// Runner 跑一次 Run 的生命周期。它可以反复使用（每次 Run 一份独立 cfg）。
type Runner struct {
	store   Store
	source  Source
	journal *journal.Journal
	starter starter
	clock   func() time.Time
	stopCh  <-chan model.StopRequest
	// observer 是可选的观测落库端口：没有它就不落观测（不伪造 0 Token）。
	observer observe.Writer
	// observationFailures 统计观测落库失败次数（只报告，不阻塞运行）。
	observationFailures atomic.Int64

	// deliverMu 串行化「取待交付记录 + 提交 + 推进游标」这一整段。
	// 摄取与心跳会同时想交付，不串行就会把同一条记录提交两次。
	deliverMu sync.Mutex
	// delivered 是本次监督循环成功入库的记录总数（跨路径累计）。
	delivered atomic.Int64
}

// Option 是构造选项。
type Option func(*Runner)

// WithClock 替换时间源。
func WithClock(now func() time.Time) Option {
	return func(r *Runner) {
		if now != nil {
			r.clock = now
		}
	}
}

// WithStarter 替换进程启动器（测试注入假实现时用）。
func WithStarter(s starter) Option {
	return func(r *Runner) {
		if s != nil {
			r.starter = s
		}
	}
}

// WithStopRequests 接入控制操作：从这个通道收到的停止请求会终止当前 Run。
func WithStopRequests(ch <-chan model.StopRequest) Option {
	return func(r *Runner) { r.stopCh = ch }
}

// New 创建监督器。store／source／journal 缺一不可：事实要提交、记录要落盘。
func New(store Store, source Source, jr *journal.Journal, opts ...Option) (*Runner, error) {
	if store == nil {
		return nil, fmt.Errorf("session: 缺少 Store 端口：事实无处提交")
	}
	if source == nil {
		return nil, fmt.Errorf("session: 缺少来源端口：无法解码与判定")
	}
	if jr == nil {
		return nil, fmt.Errorf("session: 缺少 journal：掉线时输出会丢")
	}
	r := &Runner{store: store, source: source, journal: jr, starter: execStarter{}, clock: time.Now}
	for _, opt := range opts {
		opt(r)
	}
	return r, nil
}

// Config 是一次 Run 的输入。
type Config struct {
	Instance   model.InstanceID
	RunID      model.RunID
	Generation model.Generation
	Spec       model.LaunchSpec
	Protocol   model.Protocol
	// Timeout 为 0 表示不设墙钟上限（由 StopRequest 或外部 ctx 终止）。
	Timeout time.Duration
	// Heartbeat 是补交与巡检间隔；0 用默认值。
	Heartbeat time.Duration
}

func (c Config) validate() []*model.Error {
	var out []*model.Error
	if !c.Instance.Valid() {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "config.task_instance", "必须绑定任务实例"))
	}
	if !c.RunID.Valid() {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "config.run_id", "必须绑定运行身份"))
	}
	if !c.Generation.Valid() {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "config.generation", "代际必须从 1 开始"))
	}
	out = append(out, c.Spec.Validate()...)
	if c.Protocol.Format == "" || c.Protocol.Version == "" {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "config.protocol", "必须写明预期协议"))
	}
	if c.Timeout < 0 {
		out = append(out, model.Violation(model.CodeRuntimeFieldInvalid, "config.timeout", "超时不得为负"))
	}
	return out
}

func (c Config) heartbeat() time.Duration {
	if c.Heartbeat > 0 {
		return c.Heartbeat
	}
	return 2 * time.Second
}

// Report 是一次监督循环的产出。
//
// 它描述「事实」而不是「验收结论」：Outcome 是运行结果，任务的 done 由交付链路决定。
type Report struct {
	Outcome   model.Outcome
	Finished  bool // 是否落了终态阶段
	Phase     model.RunPhase
	ExitCode  *int
	Appended  int // 落进 journal 的记录数
	Decoded   int // 产出标准事件的记录数
	Submitted int // 成功入库的记录数（含阶段记录与补交）
	Pending   int // 结束时仍滞留待补交的记录数
	Stop      *model.StopEvidence
	Stderr    string
	// InterpretError 是判定侧的诊断（例如未登录）。它不改变事实，只解释结论。
	InterpretError string
	// ObservationFailures 是观测落库失败的次数。观测失败不影响运行结论，
	// 但必须报出来：否则「没记上」会被当成「没有」。
	ObservationFailures int64
}

// runState 是单次 Run 的幂等标记：终态只能提交一次。
type runState struct {
	mu        sync.Mutex
	finished  bool
	submitErr error
}

// Run 跑完整条生命周期。返回的 Report 总是描述已发生的事实。
func (r *Runner) Run(ctx context.Context, cfg Config) (Report, error) {
	if vs := cfg.validate(); len(vs) > 0 {
		return Report{Outcome: model.OutcomeUnknown}, model.Err(vs)
	}

	rep := Report{Outcome: model.OutcomeUnknown}
	st := &runState{}

	// 1. 先提交启动事实，再起进程：进程崩了也不能留下「没有任何记录的运行」。
	if err := r.submitPhase(ctx, cfg, model.PhaseRunning, st); err != nil {
		return rep, err
	}

	ch, err := r.starter.start(cfg.Spec, cfg.RunID, cfg.Generation)
	if err != nil {
		rep.Outcome = model.OutcomeFailed
		rep.Stderr = "启动失败：" + err.Error()
		_ = r.submitPhase(ctx, cfg, model.PhaseFailed, st)
		rep.Finished = st.terminated()
		return rep, err
	}

	// 2. 摄取：先读完输出再等进程退出。
	//
	// 不能一边 cmd.Wait() 一边读 StdoutPipe——Wait 会在进程退出后关掉管道，
	// 把还没读到的行截断。所以「读完」永远先于「等退出」。
	ingestCh := make(chan ingestResult, 1)
	go func() { ingestCh <- r.ingest(ctx, cfg, ch) }()

	waitCtx := ctx
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		waitCtx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	var stopEvidence *model.StopEvidence
	timedOut := false
	ingestDone := false
	var ing ingestResult
	ticker := time.NewTicker(cfg.heartbeat())
	defer ticker.Stop()

loop:
	for {
		select {
		case ing = <-ingestCh:
			ingestDone = true
			break loop
		case <-ticker.C:
			r.resubmit(waitCtx, cfg)
		case req := <-r.stopCh:
			ev, serr := ch.stop(req.Mode)
			stopEvidence = &ev
			if serr != nil {
				return rep, serr
			}
			break loop
		case <-waitCtx.Done():
			if ctx.Err() == nil {
				timedOut = true // 外部 ctx 之前先到的是墙钟超时
			}
			ev, _ := ch.stop(model.StopForce)
			stopEvidence = &ev
			break loop
		}
	}

	if !ingestDone {
		select {
		case ing = <-ingestCh:
		case <-time.After(stopGrace + time.Second):
			// 停止后仍读不到 EOF：保留已摄取的事实，不假装读全了。
		}
	}
	_ = ch.wait()

	rep.Appended, rep.Decoded = ing.appended, ing.decoded
	rep.ExitCode = ch.exitCode()
	rep.Stderr = ch.stderr()

	// 3. 判定：超时与取消是「我们做过的事」，不是来源说的；其余交给 Interpret。
	switch {
	case timedOut:
		rep.Outcome = model.OutcomeTimedOut
	case stopEvidence != nil:
		// 停止未确认就不能声称已取消：保持 unknown，等核实。
		if stopEvidence.StopConfirmed {
			rep.Outcome = model.OutcomeCancelled
		} else {
			rep.Outcome = model.OutcomeUnknown
		}
		rep.Stop = stopEvidence
	default:
		fact, ierr := r.source.Interpret(ctx, ports.InterpretRequest{
			FinalResult: ing.lastResult,
			ExitCode:    rep.ExitCode,
		})
		rep.Outcome = fact.Outcome
		if ierr != nil {
			rep.InterpretError = ierr.Error()
		}
	}

	// 4. 终态：unknown 不落终态（E232），保持待核实。
	if phase, ok := terminalPhase(rep.Outcome); ok {
		if err := r.submitPhase(ctx, cfg, phase, st); err != nil {
			return rep, err
		}
		rep.Finished = st.terminated()
		rep.Phase = phase
	}

	// 5. 收尾补交：把还在 pending 的记录再推一次。
	r.resubmit(ctx, cfg)
	rep.Submitted = int(r.delivered.Load())
	rep.Pending = r.journal.Status().PendingRecords
	rep.ObservationFailures = r.observationFailures.Load()
	return rep, nil
}

// Recover 补交上次运行遗留的待交付记录。
//
// 取消、崩溃、中枢掉线都会留下 pending；重跑第一步调它，事实就补齐了，
// 不需要「重新跑一遍来源进程」。
func (r *Runner) Recover(ctx context.Context, cfg Config) (journal.ResubmitReport, error) {
	r.deliverMu.Lock()
	defer r.deliverMu.Unlock()
	return r.journal.Resubmit(ctx, journal.SubmitterFunc(func(ctx context.Context, rec journal.Record) error {
		return r.submitRecord(ctx, cfg, rec, &deliverStats{})
	}))
}

// deliverStats 是一次交付尝试的计数。
type deliverStats struct {
	delivered int // 成功入库的记录数
	decoded   int // 产出标准事件的记录数
}

// deliver 把待交付记录按序推给后端；这是唯一的交付通道。
//
// 摄取、心跳、阶段提交、收尾都走它，所以「谁在什么时候提交了哪条」只有一个答案。
func (r *Runner) deliver(ctx context.Context, cfg Config, st *deliverStats) error {
	r.deliverMu.Lock()
	defer r.deliverMu.Unlock()
	rep, err := r.journal.Resubmit(ctx, journal.SubmitterFunc(func(ctx context.Context, rec journal.Record) error {
		return r.submitRecord(ctx, cfg, rec, st)
	}))
	if st != nil {
		st.delivered += rep.Delivered
	}
	r.delivered.Add(int64(rep.Delivered))
	return err
}

// ingestResult 是摄取协程的产出（不与主协程共享可变状态）。
type ingestResult struct {
	appended   int
	decoded    int
	lastResult []byte
}

// ingest 逐行读来源输出：先落 journal，再尝试提交。
//
// 「先落盘再提交」是掉线可恢复的关键：提交失败不影响摄取继续，
// 记录留在 pending，由心跳或下一次 Recover 补交。
func (r *Runner) ingest(ctx context.Context, cfg Config, ch child) ingestResult {
	var out ingestResult
	sc := bufio.NewScanner(ch.stdout())
	sc.Buffer(make([]byte, 0, 64<<10), maxRecordBytes)

	index := 0
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		index++
		kind := kindStreamRecord
		payload := line
		incomplete := false
		if !json.Valid(line) {
			// 坏行照样留证据：包成 JSON 字符串进日志，原始字节可回溯；
			// 解码时它拿不到事件，但也不会把整次摄取打断。
			if quoted, err := json.Marshal(string(line)); err == nil {
				payload = quoted
				kind = kindCorruptRecord
				incomplete = true
			}
		}
		if _, err := r.journal.Append(journal.Record{
			Kind: kind,
			Source: journal.Source{
				Namespace:    "codebuddy",
				ParseVersion: cfg.Protocol.Version,
				Path:         cfg.Spec.Executable,
				RecordIndex:  index,
			},
			Payload:    payload,
			Incomplete: incomplete,
		}); err != nil {
			// 落盘都失败说明本机存储不可用，继续跑只会丢事实：停下来。
			return out
		}
		out.appended++
		if isResultRecord(line) {
			out.lastResult = line
		}
		// 一线一交：顺手把之前滞留的记录一起交付，顺序由 journal 序号保证。
		// 失败（掉线）不影响继续摄取——事实已经落盘。
		st := &deliverStats{}
		_ = r.deliver(ctx, cfg, st)
		out.decoded += st.decoded
	}
	return out
}

// submitRecord 把一条 journal 记录翻成标准事件并提交。
//
// 解码失败（损坏行、未识别类型）不算提交失败：证据已在 journal 里，
// 原始字节可回溯，但没有事件可提交——游标照常推进。
func (r *Runner) submitRecord(ctx context.Context, cfg Config, rec journal.Record, st *deliverStats) error {
	if rec.Kind == kindPhaseRecord {
		return r.submitPhaseRecord(ctx, cfg, rec)
	}
	ref := fmt.Sprintf("journal#seq=%d&line=%d", rec.Seq, rec.Line)
	// 观测先落：来源给得出会话身份与用量就记一份，失败不阻塞这次提交。
	r.observeFacts(ctx, cfg, rec)
	decoded, err := r.source.Decode(ctx, ports.DecodeRequest{
		RecordRef:  ref,
		RecordType: rec.Kind,
		Payload:    rec.Payload,
		Cursor:     model.Cursor(maxInt(1, int(rec.Seq))),
		Protocol:   cfg.Protocol,
	})
	if err != nil {
		return nil
	}
	if len(decoded.Events) == 0 {
		return nil
	}
	if st != nil {
		st.decoded++
	}
	ops := make([]ports.StoreOp, 0, len(decoded.Events))
	for i := range decoded.Events {
		ev := decoded.Events[i]
		ops = append(ops, ports.StoreOp{Kind: ports.StoreEventAppend, Event: &ev})
	}
	if err := r.apply(ctx, cfg, ops); err != nil {
		return err
	}
	return nil
}

// submitPhase 提交阶段变更与同批的 run.phase 事件（E260）。
//
// 幂等：终态只提交一次；重复提交落到 Store 侧仍会因事件身份重复被拒，
// 那种情况按「已入库」处理（不是失败）。
func (r *Runner) submitPhase(ctx context.Context, cfg Config, phase model.RunPhase, st *runState) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	if phase.Terminal() && st.finished {
		return nil
	}

	now := r.clock().UTC()
	payload, err := json.Marshal(phaseFact{Phase: phase, At: now})
	if err != nil {
		return err
	}
	if _, err := r.journal.Append(journal.Record{Kind: kindPhaseRecord, Payload: payload}); err != nil {
		return err
	}
	if phase.Terminal() {
		st.finished = true
	}
	// 提交失败不致命：事实已经落盘，靠补交补齐（这就是掉线可恢复的含义）。
	_ = r.deliver(ctx, cfg, nil)
	return nil
}

// phaseFact 是阶段记录的载荷。
type phaseFact struct {
	Phase model.RunPhase `json:"phase"`
	At    time.Time      `json:"at"`
}

// submitPhaseRecord 把一条阶段记录提交成「阶段变更 + 同批 run.phase 事件」（E260）。
func (r *Runner) submitPhaseRecord(ctx context.Context, cfg Config, rec journal.Record) error {
	var fact phaseFact
	if err := json.Unmarshal(rec.Payload, &fact); err != nil {
		return nil // 读不懂的阶段记录只留证据
	}
	if !fact.Phase.Valid() {
		return nil
	}
	phase := fact.Phase
	at := fact.At
	if at.IsZero() {
		at = rec.ReceivedAt
	}
	ev := model.Event{
		EventID:       phaseEventID(cfg, phase),
		SchemaVersion: 1,
		Kind:          "run.phase",
		RunID:         cfg.RunID,
		Phase:         &phase,
		OccurredAt:    at,
		ReceivedAt:    at,
		Quality:       model.QualityObserved,
	}
	ops := []ports.StoreOp{
		{Kind: ports.StoreRunPhase, RunPhase: &ports.RunPhaseWrite{
			RunID: cfg.RunID, Phase: phase, At: at, Event: ev.EventID,
		}},
		{Kind: ports.StoreEventAppend, Event: &ev},
	}
	return r.apply(ctx, cfg, ops)
}

// apply 用同一代际提交一批操作。
func (r *Runner) apply(ctx context.Context, cfg Config, ops []ports.StoreOp) error {
	gen := cfg.Generation
	for i := range ops {
		g := gen
		ops[i].Generation = &g
	}
	if _, err := r.store.Apply(ctx, ports.StoreRequest{Ops: ops}); err != nil {
		// 同一条事实重复入库 = 幂等重放，不是失败（补交路径会走到这里）。
		if e, ok := model.AsError(err); ok && e.Code == model.CodeEventDuplicate {
			return journal.ErrAlreadyStored
		}
		return err
	}
	return nil
}

// resubmit 补交 pending。失败不致命：下一轮心跳或 Recover 再来。
func (r *Runner) resubmit(ctx context.Context, cfg Config) {
	_ = r.deliver(ctx, cfg, nil)
}

func (s *runState) terminated() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finished
}

// terminalPhase 把运行结果映射成终态阶段；unknown 没有终态（E232）。
func terminalPhase(o model.Outcome) (model.RunPhase, bool) {
	switch o {
	case model.OutcomeCompleted:
		return model.PhaseCompleted, true
	case model.OutcomeFailed:
		return model.PhaseFailed, true
	case model.OutcomeCancelled:
		return model.PhaseCancelled, true
	case model.OutcomeTimedOut:
		return model.PhaseTimedOut, true
	default:
		return "", false
	}
}

// phaseEventID 由「运行 + 阶段 + 代际」确定性地推导事件身份。
//
// 重放同一次阶段变更必须得到同一个 event_id，这样重复提交会被 Store 判重放，
// 而不是在时间轴上留下两条「running」。
func phaseEventID(cfg Config, phase model.RunPhase) model.EventID {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d", cfg.RunID, phase, cfg.Generation)))
	return model.EventID("evt_" + hex.EncodeToString(sum[:])[:16])
}

// isResultRecord 只做最小判定：这条记录是不是最终结果记录。
// 真正的解释（subtype／is_error／证据等级）在 Interpret 里。
func isResultRecord(line []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &probe) == nil && probe.Type == "result"
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
