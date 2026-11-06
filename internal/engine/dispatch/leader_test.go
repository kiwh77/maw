package dispatch

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/process"
)

// sharedLock 是测试用共享排他锁：同状态目录第二个持有者拿不到。
type sharedLock struct {
	mu   sync.Mutex
	held bool
}

func (s *sharedLock) lock(string) (LockHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held {
		return nil, process.ErrLocked
	}
	s.held = true
	return &sharedHandle{s: s}, nil
}

func (s *sharedLock) isHeld() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

type sharedHandle struct {
	s    *sharedLock
	once sync.Once
}

func (h *sharedHandle) Release() error {
	h.once.Do(func() {
		h.s.mu.Lock()
		h.s.held = false
		h.s.mu.Unlock()
	})
	return nil
}

func waitResult(t *testing.T, q *fakeQueue, id model.OperationID, want model.OperationResult) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if q.result(id) == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待操作 %s 变为 %s 超时，当前 %s", id, want, q.result(id))
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// TestLeaderAutoDispatchAdvancesQueuedOperation 钉住 serve 内 auto 模式：
// leader 持锁推进已排队的 dispatch 操作，直到操作离开 pending。
func TestLeaderAutoDispatchAdvancesQueuedOperation(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	launcher := &fakeLauncher{}
	lock := &sharedLock{}
	c := newTestCoordinator(t, Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: launcher,
		LockFunc: lock.lock,
	}, clock)

	leader := &Leader{Coord: c, Dispatch: true, Idle: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- leader.Run(ctx) }()

	waitResult(t, q, op.OperationID, model.ResultApplied)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("leader 应正常退出，实际 %v", err)
	}
	if launcher.count() != 1 {
		t.Fatalf("runner 应只启动一次，实际 %d", launcher.count())
	}
	if q.result(op.OperationID) != model.ResultApplied {
		t.Fatal("操作结果未写回队列")
	}
}

// TestLeaderSecondServeLockFailsWithoutDisturbingFirst 钉住「第二个 serve 不抢 leader」：
// 抢锁失败立即返回 ErrCoordinatorActive，不消费任何操作；第一个 leader 仍可推进。
func TestLeaderSecondServeLockFailsWithoutDisturbingFirst(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	lock := &sharedLock{}
	cfg := Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: &fakeLauncher{},
		LockFunc: lock.lock,
	}
	first := newTestCoordinator(t, cfg, clock)
	second := newTestCoordinator(t, cfg, clock)

	if err := first.Acquire(); err != nil {
		t.Fatalf("第一个协调者应拿到锁: %v", err)
	}
	defer func() { _ = first.Release() }()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	leader := &Leader{Coord: second, Dispatch: true, Idle: time.Millisecond}
	err := leader.Run(ctx)
	if !errors.Is(err, ErrCoordinatorActive) {
		t.Fatalf("第二个 serve 应拿到 ErrCoordinatorActive，实际 %v", err)
	}
	if q.claims() != 0 {
		t.Fatalf("第二个 serve 不得消费操作，实际 claim %d 次", q.claims())
	}
	if got := q.result(op.OperationID); got != model.ResultPending {
		t.Fatalf("第二个 serve 改动了操作状态: %s", got)
	}
	if !lock.isHeld() {
		t.Fatal("第二个 serve 把第一个 leader 的锁放掉了")
	}

	// 第一个 leader 不受影响，仍能继续推进。
	result, err := first.Step(context.Background())
	if err != nil {
		t.Fatalf("第一个 leader 步进失败: %v", err)
	}
	if !result.Progressed {
		t.Fatalf("第一个 leader 应推进操作，得到 %+v", result)
	}
	if got := q.result(op.OperationID); got != model.ResultApplied {
		t.Fatalf("操作结果应为 applied，实际 %s", got)
	}
}

// TestLeaderPollsSystemAgentWithSharedLock 钉住与系统 Agent 共用同一 leader：
// 轮询回调在持锁状态下按间隔执行，期间第二个 serve 依然抢不到锁。
func TestLeaderPollsSystemAgentWithSharedLock(t *testing.T) {
	clock := newFakeClock()
	q := newFakeQueue(clock.Now)
	lock := &sharedLock{}
	c := newTestCoordinator(t, Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: &fakeLauncher{},
		LockFunc: lock.lock,
	}, clock)

	var polls int32
	var unlocked int32
	leader := &Leader{
		Coord:          c,
		Dispatch:       false,
		SystemInterval: 5 * time.Millisecond,
		Idle:           2 * time.Millisecond,
		System: func(context.Context) error {
			if !lock.isHeld() {
				atomic.AddInt32(&unlocked, 1)
			}
			atomic.AddInt32(&polls, 1)
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- leader.Run(ctx) }()

	deadline := time.Now().Add(time.Second)
	for atomic.LoadInt32(&polls) < 2 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if atomic.LoadInt32(&polls) < 2 {
		t.Fatalf("系统 Agent 未按间隔轮询，实际 %d 次", atomic.LoadInt32(&polls))
	}
	if atomic.LoadInt32(&unlocked) != 0 {
		t.Fatal("系统 Agent 轮询发生在未持锁状态")
	}

	// 同一状态目录的第二个 serve 抢锁失败，且不影响正在轮询的第一个。
	second := newTestCoordinator(t, Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: &fakeLauncher{},
		LockFunc: lock.lock,
	}, clock)
	if err := (&Leader{Coord: second, Dispatch: true, Idle: time.Millisecond}).Run(ctx); !errors.Is(err, ErrCoordinatorActive) {
		t.Fatalf("第二个 serve 应被 leader 锁挡住，实际 %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("leader 应正常退出，实际 %v", err)
	}
}

// TestLeaderAutoOffDoesNotStepQueue 钉住开关边界：auto_enabled 关闭时不消费队列。
func TestLeaderAutoOffDoesNotStepQueue(t *testing.T) {
	clock := newFakeClock()
	op := testOperation()
	q := newFakeQueue(clock.Now, op)
	lock := &sharedLock{}
	c := newTestCoordinator(t, Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: &fakeLauncher{},
		LockFunc: lock.lock,
	}, clock)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := (&Leader{Coord: c, Dispatch: false, Idle: time.Millisecond}).Run(ctx)
	if err != nil {
		t.Fatalf("leader 应随 ctx 超时正常退出，实际 %v", err)
	}
	if q.claims() != 0 {
		t.Fatalf("auto 关闭时不得消费操作，实际 claim %d 次", q.claims())
	}
	if got := q.result(op.OperationID); got != model.ResultPending {
		t.Fatalf("auto 关闭时操作应保持 pending，实际 %s", got)
	}
}

// TestLeaderRunsProducerEveryRound 钉住 SH-401 的接线：生产者回调每轮都跑，
// 且与 Dispatch/System 两个开关互不推导——两者全关也不影响生产者。
func TestLeaderRunsProducerEveryRound(t *testing.T) {
	clock := newFakeClock()
	q := newFakeQueue(clock.Now)
	c := newTestCoordinator(t, Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: &fakeLauncher{},
		LockFunc: (&sharedLock{}).lock,
	}, clock)
	var calls int32
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := (&Leader{
		Coord: c, Dispatch: false, Idle: time.Millisecond,
		Producer: func(context.Context) error {
			atomic.AddInt32(&calls, 1)
			return nil
		},
	}).Run(ctx)
	if err != nil {
		t.Fatalf("leader 应随 ctx 超时正常退出，实际 %v", err)
	}
	if atomic.LoadInt32(&calls) < 1 {
		t.Fatal("生产者回调应在每轮被调用")
	}
}

// TestLeaderProducerErrorReportedWithoutExit 覆盖生产者错误：经 OnError 上报，
// 但循环不退出（错误不是进程停下的证据）。
func TestLeaderProducerErrorReportedWithoutExit(t *testing.T) {
	clock := newFakeClock()
	q := newFakeQueue(clock.Now)
	c := newTestCoordinator(t, Config{
		StateDir: t.TempDir(),
		Queue:    q,
		Intents:  NewFileStore(t.TempDir()),
		Launcher: &fakeLauncher{},
		LockFunc: (&sharedLock{}).lock,
	}, clock)
	var reported int32
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := (&Leader{
		Coord: c, Dispatch: false, Idle: time.Millisecond,
		Producer: func(context.Context) error {
			return errors.New("auto select: enqueue failed")
		},
		OnError: func(error) { atomic.AddInt32(&reported, 1) },
	}).Run(ctx)
	if err != nil {
		t.Fatalf("生产者错误不应让 leader 退出，实际 %v", err)
	}
	if atomic.LoadInt32(&reported) < 1 {
		t.Fatal("生产者错误应经 OnError 上报")
	}
}
