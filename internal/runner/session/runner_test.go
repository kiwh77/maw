package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/runner/journal"
)

// 采样用的来源记录：一行一条，形状与 CodeBuddy stream-json 一致。
const (
	lineInit    = `{"type":"system","subtype":"init","uuid":"u1","__timestamp":"2026-10-23T03:05:05.523Z"}`
	lineAsst    = `{"type":"assistant","uuid":"u2","__timestamp":"2026-10-23T03:05:10.747Z"}`
	lineResult  = `{"type":"result","subtype":"success","is_error":false,"result":"MAW-OK","uuid":"u3","__timestamp":"2026-10-23T03:05:11.002Z"}`
	lineGarbage = `{"type":"result","subtype":"succ`
)

// TestSessionHelper 是「假来源进程」本体：被真正的子进程执行，按场景吐记录。
//
// 用测试二进制当子进程，测试就不依赖外部安装的 CLI；GO_WANT_SESSION_HELPER
// 通过 LaunchSpec.EnvAllowlist 传进来（那就是生产里传环境的同一条通道）。
func TestSessionHelper(t *testing.T) {
	if os.Getenv("GO_WANT_SESSION_HELPER") != "1" {
		return
	}
	scenario := os.Getenv("SESSION_SCENARIO")
	out := os.Stdout
	emit := func(line string) { fmt.Fprintln(out, line) }

	switch scenario {
	case "cancel":
		// 一直输出，直到被杀：用来验证停止路径。
		emit(lineInit)
		for i := 0; i < 500; i++ {
			emit(lineAsst)
			time.Sleep(10 * time.Millisecond)
		}
	case "no-result":
		emit(lineInit)
		emit(lineAsst)
	case "garbage-then-result":
		emit(lineInit)
		emit(lineGarbage)
		emit(lineResult)
	default: // "ok"
		emit(lineInit)
		emit(lineAsst)
		emit(lineResult)
	}
	os.Exit(0)
}

// stubSource 是可控的来源端口实现。
type stubSource struct {
	mu        sync.Mutex
	decodeErr error
	outcome   model.Outcome
	decoded   int
}

func (s *stubSource) Decode(ctx context.Context, req ports.DecodeRequest) (ports.DecodeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.decodeErr != nil {
		return ports.DecodeResult{}, s.decodeErr
	}
	// 与真实适配器同一口径：记录必须是带 type 的 JSON 对象。
	// 坏行被包成 JSON 字符串后仍「是合法 JSON」，但不是记录，必须解不出来。
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(req.Payload, &probe); err != nil || probe.Type == "" {
		return ports.DecodeResult{}, fmt.Errorf("unparseable")
	}
	s.decoded++
	kind := "turn.message"
	if strings.Contains(string(req.Payload), `"type":"result"`) {
		kind = "turn.finished"
	}
	ev := model.Event{
		EventID:       model.EventID(fmt.Sprintf("evt_%06d", s.decoded)),
		Cursor:        req.Cursor,
		SchemaVersion: 1,
		Kind:          kind,
		OccurredAt:    time.Date(2026, 10, 23, 3, 5, 0, 0, time.UTC),
		ReceivedAt:    time.Date(2026, 10, 23, 3, 6, 0, 0, time.UTC),
		Quality:       model.QualityObserved,
	}
	return ports.DecodeResult{Events: []model.Event{ev}, RawRef: req.RecordRef, Quality: model.QualityObserved}, nil
}

func (s *stubSource) Interpret(ctx context.Context, req ports.InterpretRequest) (model.InterpretResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(req.FinalResult) == 0 {
		return model.InterpretResult{Outcome: model.OutcomeUnknown, Quality: model.QualityUnknown}, nil
	}
	if s.outcome != "" {
		return model.InterpretResult{Outcome: s.outcome, ResultText: "MAW-OK", Quality: model.QualityObserved}, nil
	}
	return model.InterpretResult{Outcome: model.OutcomeCompleted, ResultText: "MAW-OK", Quality: model.QualityObserved}, nil
}

// recordingStore 记录所有提交的操作；offline 时一律失败。
type recordingStore struct {
	mu      sync.Mutex
	ops     []ports.StoreOp
	offline bool
	failErr error
}

func (s *recordingStore) Apply(ctx context.Context, req ports.StoreRequest) (ports.StoreResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offline {
		if s.failErr != nil {
			return ports.StoreResult{}, s.failErr
		}
		return ports.StoreResult{}, fmt.Errorf("hub down")
	}
	s.ops = append(s.ops, req.Ops...)
	return ports.StoreResult{Cursor: model.Cursor(len(s.ops)), Revision: 1}, nil
}

func (s *recordingStore) goOnline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline = false
}

func (s *recordingStore) setOffline() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline = true
}

func (s *recordingStore) kinds() map[ports.StoreOpKind]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[ports.StoreOpKind]int{}
	for _, op := range s.ops {
		out[op.Kind]++
	}
	return out
}

func (s *recordingStore) phases() []model.RunPhase {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.RunPhase
	for _, op := range s.ops {
		if op.Kind == ports.StoreRunPhase && op.RunPhase != nil {
			out = append(out, op.RunPhase.Phase)
		}
	}
	return out
}

// newJournal 在临时目录打开一份真实日志（与生产同一实现）。
func newJournal(t *testing.T, runID string) *journal.Journal {
	t.Helper()
	jr, err := journal.Open(t.TempDir(), journal.Options{RunID: runID, Sync: true})
	if err != nil {
		t.Fatalf("打开 journal 失败：%v", err)
	}
	t.Cleanup(func() { _ = jr.Close() })
	return jr
}

// helperLaunchSpec 指回测试二进制本身，用参数与环境选场景。
func helperLaunchSpec(t *testing.T, scenario string) model.LaunchSpec {
	t.Helper()
	t.Setenv("GO_WANT_SESSION_HELPER", "1")
	t.Setenv("SESSION_SCENARIO", scenario)
	return model.LaunchSpec{
		Executable:   os.Args[0],
		Argv:         []string{"-test.run=TestSessionHelper"},
		Cwd:          t.TempDir(),
		StdinMode:    model.StdinNone,
		EnvAllowlist: []string{"GO_WANT_SESSION_HELPER", "SESSION_SCENARIO", "PATH", "HOME", "TMPDIR"},
		ExpectedProtocol: model.Protocol{
			Format:  "stream-json",
			Version: "1",
		},
	}
}

func testConfig(t *testing.T, scenario string) Config {
	return Config{
		Instance:   "ti_vn404a",
		RunID:      "run_a404",
		Generation: 1,
		Spec:       helperLaunchSpec(t, scenario),
		Protocol:   model.Protocol{Format: "stream-json", Version: "1"},
		Heartbeat:  20 * time.Millisecond,
	}
}

func newRunner(t *testing.T, store Store, src Source, jr *journal.Journal, opts ...Option) *Runner {
	t.Helper()
	r, err := New(store, src, jr, opts...)
	if err != nil {
		t.Fatalf("创建 supervisor 失败：%v", err)
	}
	return r
}

// TestRunLifecycle 覆盖主路径：真子进程 → 逐行摄取 → 提交阶段与事件 → completed。
func TestRunLifecycle(t *testing.T) {
	cfg := testConfig(t, "ok")
	store := &recordingStore{}
	jr := newJournal(t, string(cfg.RunID))
	src := &stubSource{}
	r := newRunner(t, store, src, jr)

	rep, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if rep.Outcome != model.OutcomeCompleted {
		t.Fatalf("应判 completed，实际 %s（%s）", rep.Outcome, rep.InterpretError)
	}
	if !rep.Finished || rep.Phase != model.PhaseCompleted {
		t.Fatalf("应落终态 completed，实际 finished=%v phase=%s", rep.Finished, rep.Phase)
	}
	if rep.Appended != 3 {
		t.Fatalf("三行记录都应落盘，实际 %d", rep.Appended)
	}
	// 两条阶段记录 + 三行来源记录都入库了。
	if rep.Submitted != 5 {
		t.Fatalf("五条记录都应入库，实际 %d", rep.Submitted)
	}
	if rep.Pending != 0 {
		t.Fatalf("在线时不应有滞留记录，实际 %d", rep.Pending)
	}

	phases := store.phases()
	if len(phases) != 2 || phases[0] != model.PhaseRunning || phases[1] != model.PhaseCompleted {
		t.Fatalf("阶段序列应为 running→completed，实际 %v", phases)
	}
	// 每个阶段都必须带同批 run.phase 事件（E260）。
	events := store.kinds()[ports.StoreEventAppend]
	if events != 5 { // 2 个阶段事件 + 3 条来源事件
		t.Fatalf("事件条数应为 5，实际 %d", events)
	}
}

// TestHeartbeatNeverTouchesBusinessState 是验收第一条的机检落点：
// 全程只允许出现 run_phase 与 event_append 两种操作。
//
// 心跳若顺手释放了占用或改了任务状态，这里会立刻红。
func TestHeartbeatNeverTouchesBusinessState(t *testing.T) {
	cfg := testConfig(t, "ok")
	cfg.Heartbeat = 5 * time.Millisecond // 让心跳在运行期间确实触发多次
	store := &recordingStore{}
	r := newRunner(t, store, &stubSource{}, newJournal(t, string(cfg.RunID)))

	if _, err := r.Run(context.Background(), cfg); err != nil {
		t.Fatalf("运行失败：%v", err)
	}

	allowed := map[ports.StoreOpKind]bool{ports.StoreRunPhase: true, ports.StoreEventAppend: true}
	for kind, n := range store.kinds() {
		if !allowed[kind] {
			t.Fatalf("出现了业务状态写入 %q（%d 次）：心跳与监督循环都不该碰占用或任务状态", kind, n)
		}
	}
}

// TestOfflineThenResubmit 覆盖「中枢掉线 → 输出仍持久保存 → 恢复后补交」。
func TestOfflineThenResubmit(t *testing.T) {
	cfg := testConfig(t, "ok")
	store := &recordingStore{}
	store.setOffline()
	jr := newJournal(t, string(cfg.RunID))
	r := newRunner(t, store, &stubSource{}, jr)

	rep, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("掉线时运行本身不应失败：%v", err)
	}
	if rep.Appended != 3 {
		t.Fatalf("掉线时输出仍必须落盘，实际 %d", rep.Appended)
	}
	if rep.Submitted != 0 {
		t.Fatalf("掉线时不应有成功提交，实际 %d", rep.Submitted)
	}
	// 三条来源记录 + 两条阶段事实（running／completed）都在 pending 里。
	if rep.Pending != 5 {
		t.Fatalf("掉线时五条事实都应在 pending，实际 %d", rep.Pending)
	}

	// 中枢恢复：补交把事实补齐，且不重复。
	store.goOnline()
	report, err := r.Recover(context.Background(), cfg)
	if err != nil {
		t.Fatalf("补交失败：%v", err)
	}
	if report.Delivered != 5 {
		t.Fatalf("应补交 5 条（3 条来源 + 2 条阶段），实际 %d", report.Delivered)
	}
	if jr.Status().PendingRecords != 0 {
		t.Fatalf("补交后不应再有 pending，实际 %d", jr.Status().PendingRecords)
	}
	if got := store.kinds()[ports.StoreEventAppend]; got != 5 {
		t.Fatalf("补交后事件数应为 5（阶段事件 2 + 来源事件 3），实际 %d", got)
	}
	if got := store.kinds()[ports.StoreRunPhase]; got != 2 {
		t.Fatalf("补交后阶段变更应为 2 条，实际 %d", got)
	}
}

// TestCancelStopsRun 覆盖取消：停止请求 → 进程终止 → 终态 cancelled。
func TestCancelStopsRun(t *testing.T) {
	cfg := testConfig(t, "cancel")
	store := &recordingStore{}
	stopCh := make(chan model.StopRequest, 1)
	r := newRunner(t, store, &stubSource{}, newJournal(t, string(cfg.RunID)), WithStopRequests(stopCh))

	go func() {
		time.Sleep(120 * time.Millisecond)
		stopCh <- model.StopRequest{RunID: cfg.RunID, Generation: 1, Mode: model.StopForce}
	}()

	rep, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("取消路径不应报错：%v", err)
	}
	if rep.Outcome != model.OutcomeCancelled {
		t.Fatalf("应判 cancelled，实际 %s", rep.Outcome)
	}
	if rep.Stop == nil || !rep.Stop.StopConfirmed {
		t.Fatalf("取消必须有停止确认证据，实际 %+v", rep.Stop)
	}
	if rep.Phase != model.PhaseCancelled {
		t.Fatalf("终态应为 cancelled，实际 %s", rep.Phase)
	}
}

// TestUnknownOutcomeLeavesNoTerminal 覆盖协议结果不完整：不落终态（E232）。
func TestUnknownOutcomeLeavesNoTerminal(t *testing.T) {
	cfg := testConfig(t, "no-result")
	store := &recordingStore{}
	r := newRunner(t, store, &stubSource{}, newJournal(t, string(cfg.RunID)))

	rep, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	if rep.Outcome != model.OutcomeUnknown {
		t.Fatalf("缺结果记录应判 unknown，实际 %s", rep.Outcome)
	}
	if rep.Finished {
		t.Fatalf("unknown 不得落终态，实际落了 %s", rep.Phase)
	}
	for _, p := range store.phases() {
		if p.Terminal() {
			t.Fatalf("unknown 时不应出现终态阶段，实际 %v", store.phases())
		}
	}
}

// TestBadRecordDoesNotBlockRun 覆盖损坏行：只留证据，不产事件，也不中断。
func TestBadRecordDoesNotBlockRun(t *testing.T) {
	cfg := testConfig(t, "garbage-then-result")
	store := &recordingStore{}
	jr := newJournal(t, string(cfg.RunID))
	r := newRunner(t, store, &stubSource{}, jr)

	rep, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("损坏行不应让运行失败：%v", err)
	}
	if rep.Appended != 3 {
		t.Fatalf("三行都应落盘（含坏行），实际 %d", rep.Appended)
	}
	if rep.Outcome != model.OutcomeCompleted {
		t.Fatalf("坏行之后的 result 仍应生效，实际 %s", rep.Outcome)
	}
	// 坏行留在 journal 里可回溯，但没有对应事件。
	if got := store.kinds()[ports.StoreEventAppend]; got != 4 { // 2 阶段 + 2 条可解记录
		t.Fatalf("坏行不应产出事件，事件数应 4，实际 %d", got)
	}
}

// TestTimeoutStopsForcefully 覆盖墙钟超时：终态 timed_out。
func TestTimeoutStopsForcefully(t *testing.T) {
	cfg := testConfig(t, "cancel")
	cfg.Timeout = 150 * time.Millisecond
	store := &recordingStore{}
	r := newRunner(t, store, &stubSource{}, newJournal(t, string(cfg.RunID)))

	rep, err := r.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("超时是确定结论，不应报错：%v", err)
	}
	if rep.Outcome != model.OutcomeTimedOut {
		t.Fatalf("应判 timed_out，实际 %s", rep.Outcome)
	}
	if rep.Phase != model.PhaseTimedOut {
		t.Fatalf("终态应为 timed_out，实际 %s", rep.Phase)
	}
}

// TestTerminalSubmittedOnce 覆盖幂等结束：重复提交终态只有一次生效。
func TestTerminalSubmittedOnce(t *testing.T) {
	cfg := testConfig(t, "ok")
	store := &recordingStore{}
	r := newRunner(t, store, &stubSource{}, newJournal(t, string(cfg.RunID)))

	if _, err := r.Run(context.Background(), cfg); err != nil {
		t.Fatalf("运行失败：%v", err)
	}
	st := &runState{finished: true}
	if err := r.submitPhase(context.Background(), cfg, model.PhaseCompleted, st); err != nil {
		t.Fatalf("重复提交终态应被幂等吞掉：%v", err)
	}
	completed := 0
	for _, p := range store.phases() {
		if p == model.PhaseCompleted {
			completed++
		}
	}
	if completed != 1 {
		t.Fatalf("终态只应提交一次，实际 %d 次", completed)
	}
}

// TestConfigGuards 覆盖非法输入。
func TestConfigGuards(t *testing.T) {
	store := &recordingStore{}
	r := newRunner(t, store, &stubSource{}, newJournal(t, "run_a404"))

	if _, err := r.Run(context.Background(), Config{}); err == nil {
		t.Fatal("空配置必须被拒绝")
	}
	if _, err := New(nil, &stubSource{}, newJournal(t, "run_x404")); err == nil {
		t.Fatal("缺 Store 必须被拒绝")
	}
}
