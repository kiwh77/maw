package dispatch

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/process"
)

// ErrCoordinatorActive 表示同一状态目录已有另一个活动协调者。
var ErrCoordinatorActive = errors.New("dispatch: 该状态目录已有活动协调者")

// LockHandle 是排他锁句柄。
type LockHandle interface {
	Release() error
}

// LockFunc 打开并尝试非阻塞排他锁；已被占用时返回 process.ErrLocked。
type LockFunc func(path string) (LockHandle, error)

// Config 是协调者的构造参数。
type Config struct {
	// StateDir 是本机状态目录（例如 ~/.maw）。同一目录只允许一个活动协调者。
	StateDir string
	// Coordinator 是协调者身份，写入 Operation 租约；为空时用 hostname-pid。
	Coordinator string
	// Queue 是持久操作队列，必填。
	Queue Queue
	// Intents 是意图存储，为空时使用 <StateDir>/dispatch/intents 下的文件存储。
	Intents IntentStore
	// Launcher 是 Runner 启动端口，必填。
	Launcher Launcher
	// Preparer 是幂等准备端口，可为空。
	Preparer Preparer
	// LockFunc 可注入测试锁，为空时使用进程锁。
	LockFunc LockFunc
	// Clock 可注入测试时钟。
	Clock func() time.Time
	// NewNonce 可注入确定性 nonce 生成器。
	NewNonce func() (string, error)
	// LeaseTTL 是操作租约时长，默认 30s。
	LeaseTTL time.Duration
	// IdleBackoff 是无待办时的轮询间隔，默认 500ms。
	IdleBackoff time.Duration
}

// Coordinator 按持久操作推进准备与启动。
type Coordinator struct {
	identity    string
	stateDir    string
	queue       Queue
	intents     IntentStore
	launcher    Launcher
	preparer    Preparer
	lockFn      LockFunc
	clock       func() time.Time
	newNonce    func() (string, error)
	leaseTTL    time.Duration
	idleBackoff time.Duration

	mu   sync.Mutex
	lock LockHandle
}

// New 校验配置并构造协调者。
func New(cfg Config) (*Coordinator, error) {
	stateDir := strings.TrimSpace(cfg.StateDir)
	if stateDir == "" {
		return nil, errors.New("dispatch: 缺少状态目录")
	}
	abs, err := filepath.Abs(stateDir)
	if err != nil {
		return nil, fmt.Errorf("dispatch: 解析状态目录 %q: %w", stateDir, err)
	}
	if cfg.Queue == nil {
		return nil, errors.New("dispatch: 缺少操作队列")
	}
	if cfg.Launcher == nil {
		return nil, errors.New("dispatch: 缺少 Runner 启动端口")
	}
	identity := strings.TrimSpace(cfg.Coordinator)
	if identity == "" {
		identity = defaultIdentity()
	}
	intents := cfg.Intents
	if intents == nil {
		intents = NewFileStore(abs)
	}
	lockFn := cfg.LockFunc
	if lockFn == nil {
		lockFn = defaultLock
	}
	clock := cfg.Clock
	if clock == nil {
		clock = time.Now
	}
	nonce := cfg.NewNonce
	if nonce == nil {
		nonce = randomNonce
	}
	ttl := cfg.LeaseTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	backoff := cfg.IdleBackoff
	if backoff <= 0 {
		backoff = 500 * time.Millisecond
	}
	return &Coordinator{
		identity:    identity,
		stateDir:    abs,
		queue:       cfg.Queue,
		intents:     intents,
		launcher:    cfg.Launcher,
		preparer:    cfg.Preparer,
		lockFn:      lockFn,
		clock:       clock,
		newNonce:    nonce,
		leaseTTL:    ttl,
		idleBackoff: backoff,
	}, nil
}

// Identity 返回协调者身份。
func (c *Coordinator) Identity() string { return c.identity }

// StateDir 返回规范化后的状态目录。
func (c *Coordinator) StateDir() string { return c.stateDir }

// LockPath 返回排他锁路径。
func (c *Coordinator) LockPath() string {
	return filepath.Join(c.stateDir, "dispatch", "coordinator.lock")
}

// Acquire 获取状态目录排他锁；已被占用时返回 ErrCoordinatorActive。
func (c *Coordinator) Acquire() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lock != nil {
		return ErrCoordinatorActive
	}
	handle, err := c.lockFn(c.LockPath())
	if err != nil {
		if errors.Is(err, process.ErrLocked) {
			return ErrCoordinatorActive
		}
		return fmt.Errorf("dispatch: 获取协调者锁: %w", err)
	}
	c.lock = handle
	return nil
}

// Release 释放排他锁；未持有时为空操作。
func (c *Coordinator) Release() error {
	c.mu.Lock()
	handle := c.lock
	c.lock = nil
	c.mu.Unlock()
	if handle == nil {
		return nil
	}
	return handle.Release()
}

// Run 持锁执行协调循环，直到 ctx 取消或队列端口报错。
//
// 循环不依赖 HTTP 请求存活：没有待办时按 IdleBackoff 等待，有新操作就继续推进。
func (c *Coordinator) Run(ctx context.Context) (Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	report := Report{Coordinator: c.identity}
	if err := c.Acquire(); err != nil {
		return report, err
	}
	defer func() { _ = c.Release() }()
	for {
		if err := ctx.Err(); err != nil {
			return report, nil
		}
		result, err := c.step(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return report, nil
			}
			return report, err
		}
		report.absorb(result)
		if result.Progressed {
			continue
		}
		if err := sleepCtx(ctx, c.idleBackoff); err != nil {
			return report, nil
		}
	}
}

// Action 描述一次 Step 实际做了什么。
type Action string

const (
	// ActionNone 表示没有可推进的操作。
	ActionNone Action = ""
	// ActionLaunched 表示本次启动了工具进程并写了确认进度。
	ActionLaunched Action = "launched"
	// ActionResumed 表示按已落盘的确认进度收尾，没有重复启动。
	ActionResumed Action = "resumed"
	// ActionRejected 表示准备/启动确定性失败，Operation 已标 rejected。
	ActionRejected Action = "rejected"
	// ActionWithdrawn 表示撤回了排队意图。
	ActionWithdrawn Action = "withdrawn"
	// ActionUncertain 表示启动结果不确定，保持占用且不重发。
	ActionUncertain Action = "uncertain"
)

// StepResult 是一次协调步骤的结果。
type StepResult struct {
	OperationID model.OperationID
	Action      Action
	Progressed  bool
	Note        string
}

// Report 是一次协调循环的汇总。
type Report struct {
	Coordinator string
	Steps       int
	Launched    int
	Resumed     int
	Rejected    int
	Withdrawn   int
	Uncertain   []model.OperationID
}

func (r *Report) absorb(result StepResult) {
	if result.OperationID == "" && result.Action == ActionNone {
		return
	}
	r.Steps++
	switch result.Action {
	case ActionLaunched:
		r.Launched++
	case ActionResumed:
		r.Resumed++
	case ActionRejected:
		r.Rejected++
	case ActionWithdrawn:
		r.Withdrawn++
	case ActionUncertain:
		r.Uncertain = append(r.Uncertain, result.OperationID)
	}
}

// Step 最多推进一个操作；未持锁时先取状态目录排他锁，结束后释放。
//
// 顺序是：列待办 → 读意图（不确定的跳过且不领取）→ 领取租约 → 按意图前进。
// 跳过不确定的操作而不是先领取，是为了避免「领了却永远不写结果」的占用僵死。
func (c *Coordinator) Step(ctx context.Context) (StepResult, error) {
	owned, err := c.ensureLock()
	if err != nil {
		return StepResult{}, err
	}
	if owned {
		defer func() { _ = c.Release() }()
	}
	return c.step(ctx)
}

// ensureLock 在未持锁时取锁；返回是否由本次调用新取得。
func (c *Coordinator) ensureLock() (bool, error) {
	c.mu.Lock()
	held := c.lock != nil
	c.mu.Unlock()
	if held {
		return false, nil
	}
	if err := c.Acquire(); err != nil {
		return false, err
	}
	return true, nil
}

func (c *Coordinator) step(ctx context.Context) (StepResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ops, err := c.queue.Pending(ctx)
	if err != nil {
		return StepResult{}, err
	}
	var blocked *Operation
	for i := range ops {
		if err := ctx.Err(); err != nil {
			return StepResult{}, err
		}
		op := ops[i]
		intent, err := c.intents.Load(op.OperationID)
		if err != nil {
			return StepResult{}, err
		}
		if isUncertain(intent) {
			if blocked == nil {
				copied := op
				blocked = &copied
			}
			continue
		}
		claimed, err := c.queue.Claim(ctx, op.OperationID, c.identity, c.leaseTTL)
		if err != nil {
			return StepResult{}, err
		}
		if !claimed {
			continue
		}
		return c.advance(ctx, op, intent)
	}
	if blocked != nil {
		return StepResult{
			OperationID: blocked.OperationID,
			Action:      ActionUncertain,
			Note:        "启动结果不确定：保持占用，等待人工核对，不重发工具进程",
		}, nil
	}
	return StepResult{Action: ActionNone}, nil
}

// Withdraw 撤回一个尚未启动的排队意图。
//
// 返回值是落盘后的意图；已进入 launching/confirmed 时返回 ErrTooLate。
// 撤回不做工作树清理：准备阶段必须幂等，留给资源端口按占用事实补偿。
func (c *Coordinator) Withdraw(ctx context.Context, operationID model.OperationID, reason string) (Intent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !operationID.Valid() {
		return Intent{}, fmt.Errorf("dispatch: operation_id %q 形态非法", operationID)
	}
	intent, err := c.intents.Load(operationID)
	if err != nil {
		return Intent{}, err
	}
	if intent == nil {
		intent = &Intent{
			OperationID: operationID,
			Progress:    ProgressQueued,
			CreatedAt:   c.clock(),
			Transitions: []Progress{ProgressQueued},
		}
	}
	if intent.Progress == ProgressLaunching || intent.Progress == ProgressConfirmed {
		return Intent{}, ErrTooLate
	}
	alreadyWithdrawn := intent.Progress == ProgressWithdrawn
	if intent.Progress == ProgressFailed {
		return intent.Clone(), nil
	}
	if !alreadyWithdrawn {
		intent.Progress = ProgressWithdrawn
		intent.Reason = reason
		intent.Uncertain = false
		intent.Transitions = append(intent.Transitions, ProgressWithdrawn)
		if err := c.saveIntent(intent); err != nil {
			return Intent{}, err
		}
	}
	ok, err := c.queue.RejectPending(ctx, operationID)
	if err != nil {
		return Intent{}, err
	}
	if !ok && !alreadyWithdrawn {
		return Intent{}, ErrTooLate
	}
	return intent.Clone(), nil
}

// UncertainIntent 便于调用方核对：返回某操作是否停在「启动结果不确定」。
func (c *Coordinator) UncertainIntent(operationID model.OperationID) (bool, error) {
	intent, err := c.intents.Load(operationID)
	if err != nil {
		return false, err
	}
	return isUncertain(intent), nil
}

func (c *Coordinator) advance(ctx context.Context, op Operation, prior *Intent) (StepResult, error) {
	if err := op.Validate(); err != nil {
		// 无法恢复的出队记录：直接拒绝，避免每次轮询都卡在同一条脏数据上。
		if cerr := c.queue.Complete(ctx, op.OperationID, c.identity, model.ResultRejected); cerr != nil {
			return StepResult{}, cerr
		}
		return StepResult{OperationID: op.OperationID, Action: ActionRejected, Progressed: true, Note: err.Error()}, nil
	}
	intent := c.intentFor(op, prior)
	if prior == nil {
		if err := c.saveIntent(intent); err != nil {
			return StepResult{}, err
		}
	} else if prior.RunID != op.RunID || prior.Generation != op.Generation {
		// 旧代际／旧运行的意图不能拿来启动当前操作（E180 语义）。
		if err := c.failIntent(intent, fmt.Errorf("意图对应 run=%s gen=%d，当前操作是 run=%s gen=%d",
			prior.RunID, prior.Generation, op.RunID, op.Generation)); err != nil {
			return StepResult{}, err
		}
		if err := c.queue.Complete(ctx, op.OperationID, c.identity, model.ResultRejected); err != nil {
			return StepResult{}, err
		}
		return StepResult{OperationID: op.OperationID, Action: ActionRejected, Progressed: true, Note: "意图代际不匹配"}, nil
	}

	switch intent.Progress {
	case ProgressWithdrawn, ProgressFailed:
		if err := c.queue.Complete(ctx, op.OperationID, c.identity, model.ResultRejected); err != nil {
			return StepResult{}, err
		}
		return StepResult{OperationID: op.OperationID, Action: ActionRejected, Progressed: true, Note: "按已落盘的终态收尾"}, nil
	case ProgressConfirmed:
		if err := c.queue.Complete(ctx, op.OperationID, c.identity, model.ResultApplied); err != nil {
			return StepResult{}, err
		}
		return StepResult{OperationID: op.OperationID, Action: ActionResumed, Progressed: true, Note: "按已确认的启动事实收尾"}, nil
	case ProgressLaunching:
		if intent.Identity != nil && intent.Identity.Valid() {
			if err := c.confirm(intent, *intent.Identity); err != nil {
				return StepResult{}, err
			}
			if err := c.queue.Complete(ctx, op.OperationID, c.identity, model.ResultApplied); err != nil {
				return StepResult{}, err
			}
			return StepResult{OperationID: op.OperationID, Action: ActionResumed, Progressed: true, Note: "补齐确认进度"}, nil
		}
		// 启动意图已落盘但没拿到身份：可能已经起了进程，绝不重发。
		intent.Uncertain = true
		if err := c.saveIntent(intent); err != nil {
			return StepResult{}, err
		}
		return StepResult{OperationID: op.OperationID, Action: ActionUncertain, Progressed: true,
			Note: "启动结果不确定：保持占用，等待人工核对，不重发工具进程"}, nil
	}

	// queued / preparing：准备阶段可重放。
	if intent.Progress == ProgressQueued {
		if err := c.transition(intent, ProgressPreparing); err != nil {
			return StepResult{}, err
		}
	}
	if c.preparer != nil {
		if err := c.preparer.Prepare(ctx, PrepareRequest{Operation: op, Intent: intent.Clone()}); err != nil {
			return c.reject(ctx, intent, fmt.Errorf("准备失败: %w", err))
		}
	}
	nonce := intent.Nonce
	if nonce == "" {
		generated, err := c.newNonce()
		if err != nil {
			return c.reject(ctx, intent, fmt.Errorf("生成启动 nonce: %w", err))
		}
		nonce = generated
	}
	intent.Nonce = nonce
	intent.SpecDigest = op.RequestDigest
	if err := c.transition(intent, ProgressLaunching); err != nil {
		return StepResult{}, err
	}
	return c.launch(ctx, op, intent)
}

func (c *Coordinator) launch(ctx context.Context, op Operation, intent *Intent) (StepResult, error) {
	identity, err := c.launcher.Launch(ctx, LaunchRequest{
		Operation:  op,
		Nonce:      intent.Nonce,
		SpecDigest: intent.SpecDigest,
		PreparedAt: intent.UpdatedAt,
	})
	if err != nil {
		if errors.Is(err, ErrLaunchUncertain) {
			intent.Uncertain = true
			if saveErr := c.saveIntent(intent); saveErr != nil {
				return StepResult{}, saveErr
			}
			return StepResult{OperationID: op.OperationID, Action: ActionUncertain, Progressed: true,
				Note: "启动结果不确定：保持占用，不重发工具进程: " + err.Error()}, nil
		}
		return c.reject(ctx, intent, fmt.Errorf("启动失败: %w", err))
	}
	if !identity.Valid() {
		// 返回成功但没有身份：按不确定处理，因为进程可能已经存在。
		intent.Uncertain = true
		if err := c.saveIntent(intent); err != nil {
			return StepResult{}, err
		}
		return StepResult{OperationID: op.OperationID, Action: ActionUncertain, Progressed: true,
			Note: "启动返回不可核对的身份：保持占用，不重发工具进程"}, nil
	}
	if err := c.confirm(intent, identity); err != nil {
		return StepResult{}, err
	}
	if err := c.queue.Complete(ctx, op.OperationID, c.identity, model.ResultApplied); err != nil {
		return StepResult{}, err
	}
	return StepResult{OperationID: op.OperationID, Action: ActionLaunched, Progressed: true}, nil
}

// reject 把意图与操作一起收束为确定性失败。
func (c *Coordinator) reject(ctx context.Context, intent *Intent, cause error) (StepResult, error) {
	if err := c.failIntent(intent, cause); err != nil {
		return StepResult{}, err
	}
	if err := c.queue.Complete(ctx, intent.OperationID, c.identity, model.ResultRejected); err != nil {
		return StepResult{}, err
	}
	return StepResult{OperationID: intent.OperationID, Action: ActionRejected, Progressed: true, Note: cause.Error()}, nil
}

func (c *Coordinator) failIntent(intent *Intent, cause error) error {
	intent.Progress = ProgressFailed
	intent.Failure = cause.Error()
	intent.Uncertain = false
	intent.Transitions = append(intent.Transitions, ProgressFailed)
	return c.saveIntent(intent)
}

func (c *Coordinator) confirm(intent *Intent, identity ProcessIdentity) error {
	intent.Identity = &identity
	intent.Uncertain = false
	intent.Progress = ProgressConfirmed
	intent.Transitions = append(intent.Transitions, ProgressConfirmed)
	return c.saveIntent(intent)
}

func (c *Coordinator) transition(intent *Intent, next Progress) error {
	if intent.Progress == next {
		return nil
	}
	if !CanTransition(intent.Progress, next) {
		return fmt.Errorf("dispatch: 操作 %s 不能从 %s 前进到 %s", intent.OperationID, intent.Progress, next)
	}
	intent.Progress = next
	intent.Transitions = append(intent.Transitions, next)
	return c.saveIntent(intent)
}

func (c *Coordinator) saveIntent(intent *Intent) error {
	intent.UpdatedAt = c.clock()
	return c.intents.Save(intent)
}

func (c *Coordinator) intentFor(op Operation, prior *Intent) *Intent {
	if prior != nil {
		cloned := prior.Clone()
		return &cloned
	}
	now := c.clock()
	return &Intent{
		OperationID:   op.OperationID,
		ProjectID:     op.ProjectID,
		RunID:         op.RunID,
		Instance:      op.Instance,
		Generation:    op.Generation,
		RequestDigest: op.RequestDigest,
		Progress:      ProgressQueued,
		CreatedAt:     now,
		UpdatedAt:     now,
		Transitions:   []Progress{ProgressQueued},
	}
}

func isUncertain(intent *Intent) bool {
	return intent != nil && intent.Progress == ProgressLaunching && intent.Uncertain
}

func defaultIdentity() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func defaultLock(path string) (LockHandle, error) {
	return process.AcquireLock(path)
}

func randomNonce() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
