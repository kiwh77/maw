package dispatch

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// fakeClock 是可推进的测试时钟。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// fakeQueue 是内存版操作队列，语义与 DBQueue 一致。
type fakeQueue struct {
	mu   sync.Mutex
	now  func() time.Time
	ops  map[model.OperationID]Operation
	ord  []model.OperationID
	rslt map[model.OperationID]model.OperationResult

	holder    map[model.OperationID]string
	leaseTill map[model.OperationID]time.Time

	claimCalls    int
	completeCalls int
	rejectCalls   int
	failCompletes int
}

func newFakeQueue(now func() time.Time, ops ...Operation) *fakeQueue {
	q := &fakeQueue{
		now:       now,
		ops:       map[model.OperationID]Operation{},
		rslt:      map[model.OperationID]model.OperationResult{},
		holder:    map[model.OperationID]string{},
		leaseTill: map[model.OperationID]time.Time{},
	}
	for _, op := range ops {
		q.ops[op.OperationID] = op
		q.ord = append(q.ord, op.OperationID)
		q.rslt[op.OperationID] = model.ResultPending
	}
	return q
}

func (q *fakeQueue) Pending(ctx context.Context) ([]Operation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Operation
	for _, id := range q.ord {
		if q.rslt[id] == model.ResultPending {
			out = append(out, q.ops[id])
		}
	}
	return out, nil
}

func (q *fakeQueue) Claim(ctx context.Context, id model.OperationID, coordinator string, ttl time.Duration) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.rslt[id] != model.ResultPending {
		return false, nil
	}
	if holder := q.holder[id]; holder != "" && q.leaseTill[id].After(q.now()) {
		return false, nil
	}
	q.holder[id] = coordinator
	q.leaseTill[id] = q.now().Add(ttl)
	q.claimCalls++
	return true, nil
}

func (q *fakeQueue) Complete(ctx context.Context, id model.OperationID, coordinator string, result model.OperationResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completeCalls++
	if q.failCompletes > 0 {
		q.failCompletes--
		return errors.New("db busy")
	}
	if q.rslt[id] != model.ResultPending {
		return fmt.Errorf("operation %s 已结束", id)
	}
	if q.holder[id] != coordinator {
		return fmt.Errorf("operation %s 由 %q 持有，不是 %q", id, q.holder[id], coordinator)
	}
	q.rslt[id] = result
	delete(q.holder, id)
	delete(q.leaseTill, id)
	return nil
}

func (q *fakeQueue) RejectPending(ctx context.Context, id model.OperationID) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.rejectCalls++
	if q.rslt[id] != model.ResultPending {
		return false, nil
	}
	if q.holder[id] != "" && q.leaseTill[id].After(q.now()) {
		return false, nil
	}
	q.rslt[id] = model.ResultRejected
	delete(q.holder, id)
	delete(q.leaseTill, id)
	return true, nil
}

func (q *fakeQueue) result(id model.OperationID) model.OperationResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.rslt[id]
}

func (q *fakeQueue) claims() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.claimCalls
}

// fakeLauncher 记录启动次数，可注入错误、身份与阻塞点。
type fakeLauncher struct {
	mu       sync.Mutex
	calls    int
	identity ProcessIdentity
	err      error
	before   func(LaunchRequest) error
	entered  chan struct{}
	unblock  chan struct{}
	once     sync.Once
}

func (l *fakeLauncher) Launch(ctx context.Context, req LaunchRequest) (ProcessIdentity, error) {
	if err := ctx.Err(); err != nil {
		return ProcessIdentity{}, err
	}
	l.mu.Lock()
	l.calls++
	before := l.before
	err := l.err
	identity := l.identity
	entered := l.entered
	unblock := l.unblock
	l.mu.Unlock()
	if before != nil {
		if err := before(req); err != nil {
			return ProcessIdentity{}, err
		}
	}
	if entered != nil {
		l.once.Do(func() { close(entered) })
		<-unblock
	}
	if err != nil {
		return ProcessIdentity{}, err
	}
	if identity.PID == 0 {
		identity = ProcessIdentity{PID: 4242, Nonce: req.Nonce, StartedAt: time.Now()}
	}
	identity.Nonce = req.Nonce
	return identity, nil
}

func (l *fakeLauncher) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls
}

type fakePreparer struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (p *fakePreparer) Prepare(context.Context, PrepareRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.err
}

func testOperation() Operation {
	return Operation{
		OperationID:   "op_a001",
		ProjectID:     "0123456789abcdef",
		RunID:         "run_a001",
		Instance:      "ti_abcdef",
		Generation:    1,
		RequestDigest: "digest-1",
	}
}

func newTestCoordinator(t *testing.T, cfg Config, clock *fakeClock) *Coordinator {
	t.Helper()
	if cfg.StateDir == "" {
		cfg.StateDir = t.TempDir()
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Now
	}
	if cfg.Coordinator == "" {
		cfg.Coordinator = "test-coordinator"
	}
	if cfg.LeaseTTL == 0 {
		cfg.LeaseTTL = time.Second
	}
	if cfg.IdleBackoff == 0 {
		cfg.IdleBackoff = time.Millisecond
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestStepPersistsLaunchIntentBeforeCreatingProcess(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	var seenProgress Progress
	var seenNonce string
	launcher := &fakeLauncher{before: func(req LaunchRequest) error {
		intent, err := store.Load(op.OperationID)
		if err != nil {
			return err
		}
		if intent == nil {
			return errors.New("启动前没有落盘意图")
		}
		seenProgress = intent.Progress
		seenNonce = intent.Nonce
		return nil
	}}
	c := newTestCoordinator(t, Config{
		Queue:    q,
		Intents:  store,
		Launcher: launcher,
	}, clock)

	result, err := c.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if result.Action != ActionLaunched || !result.Progressed {
		t.Fatalf("期望 launched，得到 %+v", result)
	}
	if seenProgress != ProgressLaunching {
		t.Fatalf("启动前意图进度应为 launching，实际 %s", seenProgress)
	}
	if seenNonce == "" {
		t.Fatal("启动意图缺少 nonce")
	}
	intent, err := store.Load(op.OperationID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if intent.Progress != ProgressConfirmed || intent.Identity == nil || intent.Identity.PID != 4242 {
		t.Fatalf("确认进度不完整: %+v", intent)
	}
	if lasted := intent.Transitions; len(lasted) != 4 ||
		lasted[0] != ProgressQueued || lasted[1] != ProgressPreparing ||
		lasted[2] != ProgressLaunching || lasted[3] != ProgressConfirmed {
		t.Fatalf("进度历史不符合预期: %v", lasted)
	}
	if got := q.result(op.OperationID); got != model.ResultApplied {
		t.Fatalf("操作结果应为 applied，实际 %s", got)
	}
	if launcher.count() != 1 {
		t.Fatalf("启动次数应为 1，实际 %d", launcher.count())
	}
}

func TestStepResumesConfirmedWithoutRelaunch(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	seed := Intent{
		OperationID: op.OperationID,
		ProjectID:   op.ProjectID,
		RunID:       op.RunID,
		Instance:    op.Instance,
		Generation:  op.Generation,
		Progress:    ProgressConfirmed,
		Nonce:       "nonce-1",
		Identity:    &ProcessIdentity{PID: 5151, Nonce: "nonce-1"},
		CreatedAt:   clock.Now(),
		Transitions: []Progress{ProgressQueued, ProgressPreparing, ProgressLaunching, ProgressConfirmed},
	}
	if err := store.Save(&seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	launcher := &fakeLauncher{}
	c := newTestCoordinator(t, Config{Queue: q, Intents: store, Launcher: launcher}, clock)

	result, err := c.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if result.Action != ActionResumed {
		t.Fatalf("期望 resumed，得到 %+v", result)
	}
	if launcher.count() != 0 {
		t.Fatalf("恢复确认进度时不应再启动进程，实际启动 %d 次", launcher.count())
	}
	if got := q.result(op.OperationID); got != model.ResultApplied {
		t.Fatalf("操作结果应为 applied，实际 %s", got)
	}
}

func TestStepCompletesConfirmedAfterPreviousCompleteFailure(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	q.failCompletes = 1
	store := NewFileStore(t.TempDir())
	launcher1 := &fakeLauncher{}
	c1 := newTestCoordinator(t, Config{
		Queue: q, Intents: store, Launcher: launcher1, Coordinator: "coordinator-1",
	}, clock)
	if _, err := c1.Step(context.Background()); err == nil {
		t.Fatal("期望第一次完成失败暴露写库错误")
	}
	intent, err := store.Load(op.OperationID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if intent.Progress != ProgressConfirmed {
		t.Fatalf("完成失败后应保留 confirmed 进度，实际 %s", intent.Progress)
	}

	// 等到旧协调者租约过期，换一个协调者接管，收尾而不重发。
	clock.Advance(2 * time.Second)
	launcher2 := &fakeLauncher{}
	c2 := newTestCoordinator(t, Config{
		Queue: q, Intents: store, Launcher: launcher2, Coordinator: "coordinator-2",
	}, clock)
	result, err := c2.Step(context.Background())
	if err != nil {
		t.Fatalf("c2 Step: %v", err)
	}
	if result.Action != ActionResumed {
		t.Fatalf("期望 resumed，得到 %+v", result)
	}
	if launcher1.count() != 1 || launcher2.count() != 0 {
		t.Fatalf("启动次数异常: c1=%d c2=%d", launcher1.count(), launcher2.count())
	}
	if got := q.result(op.OperationID); got != model.ResultApplied {
		t.Fatalf("操作结果应为 applied，实际 %s", got)
	}
}

func TestUncertainLaunchIsNeverRetried(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	launcher := &fakeLauncher{err: fmt.Errorf("runner 超时: %w", ErrLaunchUncertain)}
	c1 := newTestCoordinator(t, Config{
		Queue: q, Intents: store, Launcher: launcher, Coordinator: "coordinator-1",
	}, clock)
	result, err := c1.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if result.Action != ActionUncertain {
		t.Fatalf("期望 uncertain，得到 %+v", result)
	}
	intent, err := store.Load(op.OperationID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !intent.Uncertain || intent.Progress != ProgressLaunching {
		t.Fatalf("意图应停在 launching+uncertain，实际 %+v", intent)
	}

	// 租约过期后换协调者：仍然不领、不重发。
	clock.Advance(2 * time.Second)
	launcher2 := &fakeLauncher{}
	c2 := newTestCoordinator(t, Config{
		Queue: q, Intents: store, Launcher: launcher2, Coordinator: "coordinator-2",
	}, clock)
	result, err = c2.Step(context.Background())
	if err != nil {
		t.Fatalf("c2 Step: %v", err)
	}
	if result.Action != ActionUncertain || result.Progressed {
		t.Fatalf("期望跳过且不确定，得到 %+v", result)
	}
	if launcher.count() != 1 || launcher2.count() != 0 {
		t.Fatalf("不确定结果不得重发，实际 c1=%d c2=%d", launcher.count(), launcher2.count())
	}
	if got := q.result(op.OperationID); got != model.ResultPending {
		t.Fatalf("不确定启动应保持 pending，实际 %s", got)
	}
	if q.claims() != 1 {
		t.Fatalf("不确定意图不应被再次领取，实际领取 %d 次", q.claims())
	}
}

func TestCrashBeforeIdentityIsMarkedUncertain(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	seed := Intent{
		OperationID: op.OperationID, ProjectID: op.ProjectID, RunID: op.RunID,
		Instance: op.Instance, Generation: op.Generation,
		Progress: ProgressLaunching, Nonce: "nonce-crash",
		CreatedAt: clock.Now(), Transitions: []Progress{ProgressQueued, ProgressPreparing, ProgressLaunching},
	}
	if err := store.Save(&seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	launcher := &fakeLauncher{}
	c := newTestCoordinator(t, Config{Queue: q, Intents: store, Launcher: launcher}, clock)
	result, err := c.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if result.Action != ActionUncertain {
		t.Fatalf("期望 uncertain，得到 %+v", result)
	}
	if launcher.count() != 0 {
		t.Fatalf("崩溃窗口中的启动结果不得重发，实际 %d", launcher.count())
	}
	if got := q.result(op.OperationID); got != model.ResultPending {
		t.Fatalf("操作应保持 pending，实际 %s", got)
	}
}

func TestWithdrawQueuedIntent(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	seed := Intent{
		OperationID: op.OperationID, ProjectID: op.ProjectID, RunID: op.RunID,
		Instance: op.Instance, Generation: op.Generation,
		Progress: ProgressQueued, CreatedAt: clock.Now(), Transitions: []Progress{ProgressQueued},
	}
	if err := store.Save(&seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	launcher := &fakeLauncher{}
	c := newTestCoordinator(t, Config{Queue: q, Intents: store, Launcher: launcher}, clock)

	intent, err := c.Withdraw(context.Background(), op.OperationID, "用户取消")
	if err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	if intent.Progress != ProgressWithdrawn || intent.Reason != "用户取消" {
		t.Fatalf("撤回意图不符合预期: %+v", intent)
	}
	if got := q.result(op.OperationID); got != model.ResultRejected {
		t.Fatalf("撤回后操作应为 rejected，实际 %s", got)
	}
	if launcher.count() != 0 {
		t.Fatalf("撤回的排队意图不应创建进程，实际 %d", launcher.count())
	}

	// 幂等重试。
	again, err := c.Withdraw(context.Background(), op.OperationID, "用户取消")
	if err != nil {
		t.Fatalf("重复撤回应幂等: %v", err)
	}
	if again.Progress != ProgressWithdrawn {
		t.Fatalf("重复撤回结果异常: %+v", again)
	}
}

func TestWithdrawRefusesAfterLaunch(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	seed := Intent{
		OperationID: op.OperationID, ProjectID: op.ProjectID, RunID: op.RunID,
		Instance: op.Instance, Generation: op.Generation,
		Progress: ProgressConfirmed, Nonce: "nonce-1",
		Identity:  &ProcessIdentity{PID: 7001, Nonce: "nonce-1"},
		CreatedAt: clock.Now(),
	}
	if err := store.Save(&seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := newTestCoordinator(t, Config{Queue: q, Intents: store, Launcher: &fakeLauncher{}}, clock)
	if _, err := c.Withdraw(context.Background(), op.OperationID, "太晚"); !errors.Is(err, ErrTooLate) {
		t.Fatalf("期望 ErrTooLate，实际 %v", err)
	}
	if got := q.result(op.OperationID); got != model.ResultPending {
		t.Fatalf("撤回失败不应改操作结果，实际 %s", got)
	}
}

func TestPrepareFailureRejectsWithoutLaunch(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	store := NewFileStore(t.TempDir())
	launcher := &fakeLauncher{}
	preparer := &fakePreparer{err: errors.New("worktree 绑定失败")}
	c := newTestCoordinator(t, Config{Queue: q, Intents: store, Launcher: launcher, Preparer: preparer}, clock)

	result, err := c.Step(context.Background())
	if err != nil {
		t.Fatalf("Step: %v", err)
	}
	if result.Action != ActionRejected {
		t.Fatalf("期望 rejected，得到 %+v", result)
	}
	if launcher.count() != 0 {
		t.Fatalf("准备失败不得启动进程，实际 %d", launcher.count())
	}
	intent, err := store.Load(op.OperationID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if intent.Progress != ProgressFailed || intent.Failure == "" {
		t.Fatalf("失败意图不完整: %+v", intent)
	}
	if got := q.result(op.OperationID); got != model.ResultRejected {
		t.Fatalf("操作结果应为 rejected，实际 %s", got)
	}
}

func TestSingleActiveCoordinatorPerStateDir(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	stateDir := t.TempDir()
	entered := make(chan struct{})
	unblock := make(chan struct{})
	launcher := &fakeLauncher{entered: entered, unblock: unblock}

	c1, err := New(Config{
		StateDir: stateDir, Coordinator: "coordinator-1", Queue: q,
		Launcher: launcher, Clock: clock.Now, LeaseTTL: time.Second, IdleBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New c1: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan Report, 1)
	go func() {
		report, _ := c1.Run(ctx)
		done <- report
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("c1 未进入启动")
	}

	c2, err := New(Config{
		StateDir: stateDir, Coordinator: "coordinator-2", Queue: q,
		Launcher: &fakeLauncher{}, Clock: clock.Now,
	})
	if err != nil {
		t.Fatalf("New c2: %v", err)
	}
	if err := c2.Acquire(); !errors.Is(err, ErrCoordinatorActive) {
		t.Fatalf("期望 ErrCoordinatorActive，实际 %v", err)
	}
	if _, err := c2.Step(context.Background()); !errors.Is(err, ErrCoordinatorActive) {
		t.Fatalf("Step 也应被排他锁挡住，实际 %v", err)
	}

	close(unblock)
	deadline := time.Now().Add(2 * time.Second)
	for q.result(op.OperationID) != model.ResultApplied && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	report := <-done
	if report.Launched != 1 {
		t.Fatalf("c1 应完成一次启动，实际 %+v", report)
	}
	if err := c2.Acquire(); err != nil {
		t.Fatalf("c1 退出后 c2 应能拿到锁: %v", err)
	}
	_ = c2.Release()
}

func TestRunConsumesAllPendingOperations(t *testing.T) {
	clock := newFakeClock()
	op1 := testOperation()
	op2 := Operation{
		OperationID: "op_b002", ProjectID: op1.ProjectID, RunID: "run_b002",
		Instance: "ti_abcdef", Generation: 1, RequestDigest: "digest-2",
	}
	q := newFakeQueue(clock.Now, op1, op2)
	stateDir := t.TempDir()
	launcher := &fakeLauncher{}
	c, err := New(Config{
		StateDir: stateDir, Coordinator: "coordinator-1", Queue: q,
		Launcher: launcher, Clock: clock.Now, IdleBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// 等到两个操作都写出结果再停循环，避免把「还没跑到第二条」当成漏派。
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if q.result(op1.OperationID) == model.ResultApplied && q.result(op2.OperationID) == model.ResultApplied {
				break
			}
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	report, err := c.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Launched != 2 {
		t.Fatalf("应启动两次，实际 %+v", report)
	}
	if launcher.count() != 2 {
		t.Fatalf("启动次数应为 2，实际 %d", launcher.count())
	}
	for _, id := range []model.OperationID{op1.OperationID, op2.OperationID} {
		if got := q.result(id); got != model.ResultApplied {
			t.Fatalf("%s 结果应为 applied，实际 %s", id, got)
		}
	}
}
