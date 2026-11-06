package app

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/systemagent"
)

// TestLeadHousekeepingExpiresAllProjectsWithoutNext 覆盖主验收：没有任何 worker
// 调 maw next，中枢一轮巡检就把多个工程里超时的 Agent 置 offline 并写事件。
func TestLeadHousekeepingExpiresAllProjectsWithoutNext(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	var seen []string
	var stamps []time.Time
	hook := leadHousekeeping(func() time.Time { return now }, leadHousekeepingDeps{
		projects: func() ([]serveLeaderProject, error) {
			return []serveLeaderProject{
				{ID: f.pc.ID(), Name: "alpha", Path: "/tmp/alpha"},
				{ID: "bbbbbbbbbbbbbbbb", Name: "beta", Path: "/tmp/beta"},
			}, nil
		},
		pathOK: func(string) bool { return true },
		load: func(path string) (*projCtx, config.SystemAgent, error) {
			pc := f.pc
			if path == "/tmp/beta" {
				pc = &projCtx{Project: &db.Project{ID: "bbbbbbbbbbbbbbbb", Name: "beta", Path: path}, Config: f.pc.Config}
			}
			return pc, config.SystemAgent{}, nil
		},
		// 真实写库路径：用临时全局库，验证一轮就完成过期检测。
		expire: func(pc *projCtx, stamp time.Time) error {
			seen = append(seen, pc.ID())
			stamps = append(stamps, stamp)
			return db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
				return expireStaleAgentsAt(tx, pc, stamp)
			})
		},
	})

	staleAgent(t, f, "cursor-stale", "")
	if err := hook(context.Background()); err != nil {
		t.Fatalf("巡检: %v", err)
	}
	if len(seen) != 2 || seen[0] != f.pc.ID() || seen[1] != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("应按工程各跑一次过期检测，实际 %v", seen)
	}
	for _, s := range stamps {
		if !s.Equal(now.UTC()) {
			t.Fatalf("应把注入时钟传给过期检测，实际 %v", s)
		}
	}
	var status string
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT status FROM agents WHERE id = ?`, "cursor-stale").Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "offline" {
		t.Fatalf("一轮巡检后超时应为 offline，实际 %s", status)
	}
	if n := countEventsByType(t, f, "agent_expired"); n != 1 {
		t.Fatalf("应写一条 agent_expired 事件，实际 %d", n)
	}
}

// TestLeadHousekeepingProjectsErrorPropagates 覆盖列举工程失败不静默。
func TestLeadHousekeepingProjectsErrorPropagates(t *testing.T) {
	want := errors.New("db closed")
	hook := leadHousekeeping(nil, leadHousekeepingDeps{
		projects: func() ([]serveLeaderProject, error) { return nil, want },
		pathOK:   func(string) bool { return true },
		load:     func(string) (*projCtx, config.SystemAgent, error) { return nil, config.SystemAgent{}, nil },
		expire:   func(*projCtx, time.Time) error { return nil },
	})
	if err := hook(context.Background()); !errors.Is(err, want) {
		t.Fatalf("列举失败应向上返回，实际 %v", err)
	}
}

// TestLeadHousekeepingExpireErrorPropagates 覆盖写库失败上抛：巡检必须把
// expire 的错误交给 Watcher 的 OnError，而不能吞掉继续下一轮。
func TestLeadHousekeepingExpireErrorPropagates(t *testing.T) {
	want := errors.New("upsert task failed")
	hook := leadHousekeeping(nil, leadHousekeepingDeps{
		projects: func() ([]serveLeaderProject, error) {
			return []serveLeaderProject{{ID: "aaa", Name: "alpha", Path: "/tmp/alpha"}}, nil
		},
		pathOK: func(string) bool { return true },
		load: func(string) (*projCtx, config.SystemAgent, error) {
			return &projCtx{Project: &db.Project{ID: "aaa", Name: "alpha"}}, config.SystemAgent{}, nil
		},
		expire: func(*projCtx, time.Time) error { return want },
	})
	if err := hook(context.Background()); !errors.Is(err, want) {
		t.Fatalf("过期检测失败应向上返回，实际 %v", err)
	}
}

// TestLeadHousekeepingSkipsUnavailableProjects 覆盖路径不可达/配置损坏跳过，
// 且不影响其它工程；取消的 ctx 不再启动任何一轮巡检。
func TestLeadHousekeepingSkipsUnavailableProjects(t *testing.T) {
	calls := 0
	hook := leadHousekeeping(nil, leadHousekeepingDeps{
		projects: func() ([]serveLeaderProject, error) {
			return []serveLeaderProject{
				{ID: "gone", Name: "gone", Path: "missing"},
				{ID: "bad", Name: "bad", Path: "bad-config"},
				{ID: "ok", Name: "ok", Path: "/tmp/ok"},
			}, nil
		},
		pathOK: func(p string) bool { return p == "/tmp/ok" },
		load: func(p string) (*projCtx, config.SystemAgent, error) {
			if p == "bad-config" {
				return nil, config.SystemAgent{}, errors.New("unreadable")
			}
			return &projCtx{Project: &db.Project{ID: "ok"}}, config.SystemAgent{}, nil
		},
		expire: func(*projCtx, time.Time) error { calls++; return nil },
	})
	if err := hook(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("只有可用工程该跑过期检测，实际 %d", calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := hook(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消后应立即返回，实际 %v", err)
	}
}

// TestServeWatcherHousekeepingExpiresStaleAgentInOnePoll 是端到端验收：
// 完全没有 worker 调 maw next，一次 Watcher.PollOnce 就把超时 Agent 置 offline
// 并写 agent_expired 事件，时钟由 Watcher 注入（不依赖真实时间流逝）。
func TestServeWatcherHousekeepingExpiresStaleAgentInOnePoll(t *testing.T) {
	f := newFixture(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	staleAgent(t, f, "cursor-stale", "")

	store, err := db.OpenStore(f.pc.GlobalDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	w := &systemagent.Watcher{
		Store:  store,
		Config: config.SystemAgent{Enabled: true, MaxAttempts: 3},
		Clock:  func() time.Time { return now },
		Housekeeping: leadHousekeeping(func() time.Time { return now }, leadHousekeepingDeps{
			projects: func() ([]serveLeaderProject, error) {
				return []serveLeaderProject{{ID: f.pc.ID(), Name: "alpha", Path: f.repo}}, nil
			},
			pathOK: func(string) bool { return true },
			load: func(string) (*projCtx, config.SystemAgent, error) {
				return f.pc, config.SystemAgent{}, nil
			},
			expire: func(pc *projCtx, stamp time.Time) error {
				return db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
					return expireStaleAgentsAt(tx, pc, stamp)
				})
			},
		}),
	}
	if _, _, err := w.PollOnce(context.Background()); err != nil {
		t.Fatalf("poll: %v", err)
	}
	var status string
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT status FROM agents WHERE id = ?`, "cursor-stale").Scan(&status)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "offline" {
		t.Fatalf("一个 poll 周期内超时应为 offline，实际 %s", status)
	}
	if n := countEventsByType(t, f, "agent_expired"); n != 1 {
		t.Fatalf("一个 poll 周期内应写 agent_expired 事件，实际 %d", n)
	}
}

func countEventsByType(t *testing.T, f *fixture, kind string) int {
	t.Helper()
	var n int
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND type = ?`,
			f.pc.ID(), kind).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}
