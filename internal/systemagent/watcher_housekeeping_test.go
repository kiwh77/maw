package systemagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// TestPollOnceRunsHousekeepingEachRound 覆盖主动巡检的接线：每一轮 PollOnce
// 都执行 Housekeeping，且不需要任何 worker 调 maw next。
func TestPollOnceRunsHousekeepingEachRound(t *testing.T) {
	st := newSignalStore(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	calls := 0
	w := &Watcher{
		Store:  st,
		Config: config.SystemAgent{Enabled: true, MaxAttempts: 3},
		Clock:  func() time.Time { return now },
		Housekeeping: func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			calls++
			return nil
		},
	}
	for i := 1; i <= 2; i++ {
		if _, _, err := w.PollOnce(context.Background()); err != nil {
			t.Fatalf("第 %d 轮: %v", i, err)
		}
		if calls != i {
			t.Fatalf("第 %d 轮后 Housekeeping 应执行 %d 次，实际 %d", i, i, calls)
		}
	}
}

// TestPollOnceHousekeepingErrorPropagates 覆盖「巡检错误不再静默」：
// 钩子失败时 PollOnce 直接返回该错误，且不继续入队/编排。
func TestPollOnceHousekeepingErrorPropagates(t *testing.T) {
	st := newSignalStore(t)
	want := errors.New("expire stale agents: write failed")
	w := &Watcher{
		Store:  st,
		Config: config.SystemAgent{Enabled: true, MaxAttempts: 3},
		Housekeeping: func(context.Context) error {
			return want
		},
	}
	_, _, err := w.PollOnce(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("巡检错误应向上返回，实际 %v", err)
	}
}

// TestRunReportsHousekeepingError 覆盖常驻循环：巡检失败经 OnError 上报但循环不退出。
func TestRunReportsHousekeepingError(t *testing.T) {
	st := newSignalStore(t)
	got := make(chan error, 1)
	w := &Watcher{
		Store:  st,
		Config: config.SystemAgent{Enabled: true, PollIntervalSec: 1, MaxAttempts: 3},
		Housekeeping: func(context.Context) error {
			return errors.New("housekeeping: upsert failed")
		},
		OnError: func(err error) {
			select {
			case got <- err:
			default:
			}
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := w.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run 应只在 ctx 取消时退出，实际 %v", err)
	}
	select {
	case err := <-got:
		if err == nil {
			t.Fatal("应上报巡检错误")
		}
	default:
		t.Fatal("巡检错误未被 OnError 上报")
	}
}

// TestPollOnceWithoutHousekeepingKeepsBehavior 守住兼容边界：未注入钩子时
// PollOnce 行为与引入前一致（仍会回收超租约作业）。
func TestPollOnceWithoutHousekeepingKeepsBehavior(t *testing.T) {
	st := newSignalStore(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	locked := now.Add(-11 * time.Minute).Format(time.RFC3339)
	seedAgentJob(t, st, &db.SystemJob{
		JobID: "sjob_no_housekeeping", Kind: db.SystemKindUnblock, Status: db.SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "dead", LockedAt: locked,
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), UpdatedAt: locked,
	})
	w := &Watcher{
		Store: st, Config: config.SystemAgent{Enabled: true, MaxAttempts: 3},
		Clock: func() time.Time { return now },
	}
	if _, _, err := w.PollOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := agentJob(t, st, "sjob_no_housekeeping"); j.Status != db.SystemJobQueued {
		t.Fatalf("未注入钩子时作业回收仍应生效，实际 %s", j.Status)
	}
}
