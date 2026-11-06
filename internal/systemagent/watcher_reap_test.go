package systemagent

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

func seedAgentJob(t *testing.T, st *db.Store, j *db.SystemJob) {
	t.Helper()
	err := st.Write(func(tx *sql.Tx) error {
		_, e := db.InsertSystemJob(tx, j)
		return e
	})
	if err != nil {
		t.Fatalf("seed %s: %v", j.JobID, err)
	}
}

func agentJob(t *testing.T, st *db.Store, id string) *db.SystemJob {
	t.Helper()
	var job *db.SystemJob
	err := st.Read(func(tx *sql.Tx) error {
		var e error
		job, e = db.GetSystemJob(tx, id)
		return e
	})
	if err != nil || job == nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return job
}

// TestPollOnceReapsStaleJobs 覆盖「Watcher 每轮先回收」：一轮 PollOnce 之后，
// 超租约的 running 作业已经回到 queued，未超租约的保持原样。
func TestPollOnceReapsStaleJobs(t *testing.T) {
	st := newSignalStore(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	seedAgentJob(t, st, &db.SystemJob{
		JobID: "sjob_stale", Kind: db.SystemKindUnblock, Status: db.SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "dead",
		LockedAt:  now.Add(-11 * time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-11 * time.Minute).Format(time.RFC3339),
	})
	seedAgentJob(t, st, &db.SystemJob{
		JobID: "sjob_live", Kind: db.SystemKindUnblock, Status: db.SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "live",
		LockedAt:  now.Add(-time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-time.Minute).Format(time.RFC3339),
	})

	w := &Watcher{
		Store:  st,
		Config: config.SystemAgent{Enabled: true, MaxAttempts: 3},
		Clock:  func() time.Time { return now },
	}
	if _, _, err := w.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}

	reaped := agentJob(t, st, "sjob_stale")
	if reaped.Status != db.SystemJobQueued {
		t.Fatalf("超租约作业应回 queued，实际 %s", reaped.Status)
	}
	if reaped.LockedBy != "" || reaped.LockedAt != "" {
		t.Fatalf("回收后应清空锁：by=%q at=%q", reaped.LockedBy, reaped.LockedAt)
	}
	if reaped.Result["reaped_from"] != db.SystemJobRunning {
		t.Fatalf("result 缺 reaped_from=running：%v", reaped.Result)
	}

	if live := agentJob(t, st, "sjob_live"); live.Status != db.SystemJobRunning {
		t.Fatalf("租约内作业被误回收：%s", live.Status)
	}
}

// TestPollOnceUsesInjectedLease 覆盖「租约可注入」：把租约缩到 1 分钟后，
// 2 分钟前的 running 会被回收，而默认 10 分钟租约下不会。
func TestPollOnceUsesInjectedLease(t *testing.T) {
	st := newSignalStore(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	locked := now.Add(-2 * time.Minute).Format(time.RFC3339)

	seedAgentJob(t, st, &db.SystemJob{
		JobID: "sjob_short_lease", Kind: db.SystemKindUnblock, Status: db.SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "dead", LockedAt: locked,
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339), UpdatedAt: locked,
	})

	w := &Watcher{
		Store:    st,
		Config:   config.SystemAgent{Enabled: true, MaxAttempts: 3},
		Clock:    func() time.Time { return now },
		JobLease: time.Minute,
	}
	if _, _, err := w.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if j := agentJob(t, st, "sjob_short_lease"); j.Status != db.SystemJobQueued {
		t.Fatalf("1 分钟租约下 2 分钟前的作业应被回收，实际 %s", j.Status)
	}
}

// TestRunReportsNonLockErrors 覆盖第四条验收：Run 遇非锁错误调用 OnError，
// 但循环本身不退出（只有 ctx 取消才结束）。
func TestRunReportsNonLockErrors(t *testing.T) {
	st := newSignalStore(t)
	got := make(chan error, 1)
	w := &Watcher{
		Store:  st,
		Config: config.SystemAgent{Enabled: true, PollIntervalSec: 1, MaxAttempts: 3},
		LeaderLock: func() (func(), error) {
			return nil, errors.New("boom: leader lock backend down")
		},
		OnError: func(err error) {
			select {
			case got <- err:
			default:
			}
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case err := <-got:
		if !strings.Contains(err.Error(), "boom") {
			t.Fatalf("OnError 收到 %v，期望原始非锁错误", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError 未收到非锁错误")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消后 Run 未退出")
	}
}
