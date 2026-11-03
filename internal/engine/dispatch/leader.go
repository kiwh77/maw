package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultIdleBackoff 是 leader 空转时的默认等待间隔。
const DefaultIdleBackoff = 500 * time.Millisecond

// Leader 是服务进程内常驻的唯一协调者循环。
//
// 与 Coordinator.Run 的差别只有一条：Leader 在同一把状态目录排他锁下
// 同时驱动派发队列与系统 Agent 轮询，而不是让两个循环各自抢锁。
//
// 语义：
//   - 抢不到锁（ErrCoordinatorActive）立刻返回，不消费任何操作、不打断已持锁的 leader；
//     第二个 `maw serve` 因此只是「不是 leader」，而不是把第一个挤掉。
//   - Dispatch=true 时持续推进派发队列，空转按 Idle 等待，不依赖 HTTP 请求存活。
//   - System 回调按 SystemInterval 执行，且恒在持锁状态下进行——系统 Agent 不再自己抢锁，
//     两套循环不会互相饿死。
type Leader struct {
	// Coord 必填：负责持锁与派发步进。
	Coord *Coordinator
	// Dispatch 为 true 时才推进派发队列（auto_enabled 的落点）。
	Dispatch bool
	// System 是系统 Agent 的单步轮询回调，可为空。
	System func(ctx context.Context) error
	// SystemInterval 是系统 Agent 回调间隔，默认 5s。
	SystemInterval time.Duration
	// Idle 是空转等待间隔，默认 500ms。
	Idle time.Duration
	// Clock 可注入测试时钟，只用于判断系统 Agent 的到期时间。
	Clock func() time.Time
	// OnError 观测单步错误（不会因此退出循环）。
	OnError func(error)
}

// Run 持锁循环直到 ctx 取消；抢锁失败时返回 ErrCoordinatorActive。
func (l *Leader) Run(ctx context.Context) error {
	if l == nil || l.Coord == nil {
		return errors.New("dispatch: leader 缺少协调者")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := l.Coord.Acquire(); err != nil {
		return err
	}
	defer func() { _ = l.Coord.Release() }()

	idle := l.Idle
	if idle <= 0 {
		idle = DefaultIdleBackoff
	}
	systemEvery := l.SystemInterval
	if systemEvery <= 0 {
		systemEvery = 5 * time.Second
	}
	clock := l.Clock
	if clock == nil {
		clock = time.Now
	}
	ticker := time.NewTicker(idle)
	defer ticker.Stop()

	var lastSystem time.Time
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		if l.Dispatch {
			if err := l.drain(ctx); err != nil {
				return nil
			}
		}
		if l.System != nil {
			now := clock()
			if lastSystem.IsZero() || now.Sub(lastSystem) >= systemEvery {
				lastSystem = now
				if err := l.System(ctx); err != nil && !isCtxErr(err) {
					l.report(err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// drain 连续步进直到没有可推进的操作；错误不致命，交给下一轮重试。
func (l *Leader) drain(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := l.Coord.Step(ctx)
		if err != nil {
			if isCtxErr(err) {
				return err
			}
			// 落库/端口瞬时错误：留在循环里等下一轮，而不是让 leader 退出。
			l.report(err)
			return nil
		}
		if !result.Progressed {
			return nil
		}
	}
}

func (l *Leader) report(err error) {
	if l.OnError != nil {
		l.OnError(fmt.Errorf("dispatch leader: %w", err))
	}
}

func isCtxErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}
