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
//   - Reload 每轮开头执行一次，允许宿主把配置热变更就地写回本结构，因此
//     「工作台打开自动执行」不需要重启 serve。
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
	// Producer 是自动选题生产者回调，每轮先跑一次；默认关闭时由调用方不注入。
	// 与 Dispatch / System 两个开关互不推导：即使两者全关，只要生产者被注入
	// 就仍按 auto_select 自己的开关工作，且错误不会让 leader 退出。
	Producer func(ctx context.Context) error
	// Reload 可选：每轮循环开头调用一次，把配置热变更写回 Dispatch／System／
	// Producer／SystemInterval 等字段。
	//
	// 它**只在 Run 的同一 goroutine 内执行**，因此随后的读取与这里的写入
	// 不构成数据竞争；宿主不得从别的 goroutine 改这些字段。
	// Reload 返回错误不致命：记一次错误、保留上一轮开关继续跑。
	Reload func() error
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
		// 热开关：本轮先问一次宿主配置有没有变。它只改动本结构上那几个
		// 开关字段，且与本循环同 goroutine，因此不需要加锁。
		if l.Reload != nil {
			if err := l.Reload(); err != nil {
				l.report(fmt.Errorf("reload: %w", err))
			}
		}
		systemEvery := l.SystemInterval
		if systemEvery <= 0 {
			systemEvery = 5 * time.Second
		}
		// 先生产再消费：本轮入队并被执行的 dispatch_ready 产生的新操作，
		// 能在同一轮的 drain 里被推进，不必等下一次轮询。
		if l.Producer != nil {
			if err := l.Producer(ctx); err != nil && !isCtxErr(err) {
				l.report(err)
			}
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
