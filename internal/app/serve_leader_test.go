package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/dispatch"
	"github.com/kiwh77/maw/internal/systemagent"
)

// fakeServeLeaderRunner 记录 Run 是否被调用，并可模拟抢锁失败。
type fakeServeLeaderRunner struct {
	err  error
	runs int
}

func (r *fakeServeLeaderRunner) Run(_ context.Context) error {
	r.runs++
	return r.err
}

type recordedEvent struct {
	projectID string
	typ       string
	data      map[string]any
}

// serveLeaderTestEnv 把 startServeLeaders 的外部依赖全部换成内存假件，
// 不真起 goroutine、不真写库，就能断言多工程覆盖与事件节流。
type serveLeaderTestEnv struct {
	deps     serveLeaderDeps
	now      time.Time
	events   []recordedEvent
	launches []string
	runners  []*fakeServeLeaderRunner
	onError  func(error)
}

func newServeLeaderTestEnv(projects []serveLeaderProject, build func(*projCtx, config.SystemAgent, func(error)) (*serveLeaderBundle, error)) *serveLeaderTestEnv {
	e := &serveLeaderTestEnv{now: time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)}
	e.deps = serveLeaderDeps{
		now: func() time.Time { return e.now },
		projects: func() ([]serveLeaderProject, error) {
			return projects, nil
		},
		pathOK: func(p string) bool { return p != "missing" },
		load: func(path string) (*projCtx, config.SystemAgent, error) {
			if path == "bad-config" {
				return nil, config.SystemAgent{}, errors.New("config unreadable")
			}
			id := filepath.Base(path)
			return &projCtx{
				Repo: path, GlobalDB: "",
				Project: &db.Project{ID: id, Name: id, Path: path},
				Config:  &config.Config{},
			}, config.SystemAgent{Enabled: true}, nil
		},
		build: func(pc *projCtx, cfg config.SystemAgent, onError func(error)) (*serveLeaderBundle, error) {
			e.onError = onError
			return build(pc, cfg, onError)
		},
		launch: func(pc *projCtx, b *serveLeaderBundle, onExit func(error)) {
			e.launches = append(e.launches, pc.ID())
			onExit(b.runner.Run(context.Background()))
		},
		write: func(projectID, actor, typ, taskID string, data any) error {
			m, _ := data.(map[string]any)
			e.events = append(e.events, recordedEvent{projectID: projectID, typ: typ, data: m})
			return nil
		},
		logf: func(string, ...any) {},
	}
	return e
}

func (e *serveLeaderTestEnv) countEvents(typ string) int {
	n := 0
	for _, ev := range e.events {
		if ev.typ == typ {
			n++
		}
	}
	return n
}

func activeBundle(_ *projCtx, _ config.SystemAgent, _ func(error)) (*serveLeaderBundle, error) {
	return &serveLeaderBundle{
		runner: &fakeServeLeaderRunner{}, dispatch: true, system: true,
	}, nil
}

func TestServeLeaderCoversAllRegisteredProjects(t *testing.T) {
	projects := []serveLeaderProject{
		{ID: "aaa", Name: "alpha", Path: "/tmp/alpha"},
		{ID: "bbb", Name: "beta", Path: "/tmp/beta"},
		{ID: "ccc", Name: "gamma", Path: "/tmp/gamma"},
	}
	e := newServeLeaderTestEnv(projects, activeBundle)
	out := startServeLeaders(context.Background(), e.deps)

	if len(out) != len(projects) {
		t.Fatalf("期望 %d 个工程各被尝试，实际 %d: %+v", len(projects), len(out), out)
	}
	for i, o := range out {
		if !o.Attempted || !o.Started || o.Reason != "started" {
			t.Fatalf("工程 %s 未被启动: %+v", projects[i].ID, o)
		}
		if o.ProjectID != projects[i].ID {
			t.Fatalf("第 %d 条工程 ID 错位: %+v", i, o)
		}
	}
	if len(e.launches) != len(projects) {
		t.Fatalf("期望 %d 次起循环，实际 %v", len(projects), e.launches)
	}
	if n := e.countEvents(leadLoopAttemptEvent); n != len(projects) {
		t.Fatalf("期望每个工程各留一条尝试记录，实际 %d", n)
	}
}

func TestServeLeaderSkipsLockedProjectAndRecords(t *testing.T) {
	projects := []serveLeaderProject{
		{ID: "aaa", Name: "alpha", Path: "/tmp/alpha"},
		{ID: "bbb", Name: "beta", Path: "/tmp/beta"},
	}
	var runners []*fakeServeLeaderRunner
	e := newServeLeaderTestEnv(projects, func(_ *projCtx, _ config.SystemAgent, _ func(error)) (*serveLeaderBundle, error) {
		r := &fakeServeLeaderRunner{err: dispatch.ErrCoordinatorActive}
		runners = append(runners, r)
		return &serveLeaderBundle{runner: r, dispatch: true}, nil
	})
	out := startServeLeaders(context.Background(), e.deps)

	if len(out) != 2 || !out[0].Started || !out[1].Started {
		t.Fatalf("两个工程都应尝试起 leader: %+v", out)
	}
	for _, r := range runners {
		if r.runs != 1 {
			t.Fatalf("每个工程应各自 Run 一次，实际 %d", r.runs)
		}
	}
	if n := e.countEvents(leadLoopLockSkippedEvent); n != 2 {
		t.Fatalf("抢不到锁应各留一条跳过记录，实际 %d", n)
	}
	if n := e.countEvents(leadLoopErrorEvent); n != 0 {
		t.Fatalf("抢不到锁不是循环错误，不该写 lead_loop_error，实际 %d", n)
	}
}

func TestServeWatcherSignalFactsInjected(t *testing.T) {
	pc := &projCtx{
		GlobalDB: "", Project: &db.Project{ID: "0123456789abcdef", Name: "alpha", Path: "/tmp/alpha"},
	}
	cfg := config.SystemAgent{Enabled: true, PollIntervalSec: 5}
	orch := &systemagent.Orchestrator{}
	w := buildServeWatcher(pc, cfg, orch)

	if w.SignalFacts == nil {
		t.Fatal("常驻 Watcher 必须注入 SignalFacts，否则系统级信号检测被完全跳过")
	}
	if w.LeaderLock != nil {
		t.Fatal("leader 已持同一把锁，Watcher 不应再自己抢锁")
	}
	if w.ProjectID != pc.ID() || w.Orchestrator != orch || !w.Config.Enabled {
		t.Fatalf("Watcher 装配错位: %+v", w)
	}
}

func TestServeLeaderOnErrorWritesThrottledEvent(t *testing.T) {
	projects := []serveLeaderProject{{ID: "aaa", Name: "alpha", Path: "/tmp/alpha"}}
	e := newServeLeaderTestEnv(projects, activeBundle)
	startServeLeaders(context.Background(), e.deps)
	if e.onError == nil {
		t.Fatal("build 未拿到 OnError 回调")
	}

	boom := errors.New("dispatch leader: 瞬时写库失败")
	e.onError(boom)
	e.onError(errors.New("dispatch leader: 另一条同类瞬时错误"))
	if n := e.countEvents(leadLoopErrorEvent); n != 1 {
		t.Fatalf("同类错误 60 秒内只应写一次，实际 %d", n)
	}

	// 仍在窗口内：不同消息但同一类，也不重复写。
	e.now = e.now.Add(59 * time.Second)
	e.onError(boom)
	if n := e.countEvents(leadLoopErrorEvent); n != 1 {
		t.Fatalf("59 秒内的同类错误应被节流，实际 %d", n)
	}

	// 越过窗口后允许再写一次。
	e.now = e.now.Add(2 * time.Second)
	e.onError(boom)
	if n := e.countEvents(leadLoopErrorEvent); n != 2 {
		t.Fatalf("超过 60 秒后应再写一条，实际 %d", n)
	}
	for _, ev := range e.events {
		if ev.typ == leadLoopErrorEvent && ev.projectID != "aaa" {
			t.Fatalf("事件未绑定工程: %+v", ev)
		}
	}
}

func TestServeLeaderBothSwitchesOffDoesNotLaunch(t *testing.T) {
	projects := []serveLeaderProject{
		{ID: "off", Name: "off", Path: "/tmp/off"},
		{ID: "on", Name: "on", Path: "/tmp/on"},
	}
	e := newServeLeaderTestEnv(projects, func(pc *projCtx, _ config.SystemAgent, _ func(error)) (*serveLeaderBundle, error) {
		if pc.ID() == "off" {
			return &serveLeaderBundle{runner: &fakeServeLeaderRunner{}}, nil // 两个开关全关
		}
		return &serveLeaderBundle{runner: &fakeServeLeaderRunner{}, dispatch: true}, nil
	})
	out := startServeLeaders(context.Background(), e.deps)

	if len(e.launches) != 1 || e.launches[0] != "on" {
		t.Fatalf("两个开关全关的工程不该起 goroutine，实际启动 %v", e.launches)
	}
	if out[0].Started || out[0].Reason != "no_switches" {
		t.Fatalf("全关工程处置不对: %+v", out[0])
	}
	if !out[1].Started {
		t.Fatalf("有开关的工程应正常启动: %+v", out[1])
	}
}

func TestServeLeaderSkipsUnavailableProjects(t *testing.T) {
	projects := []serveLeaderProject{
		{ID: "gone", Name: "gone", Path: "missing"},
		{ID: "bad", Name: "bad", Path: "bad-config"},
		{ID: "ok", Name: "ok", Path: "/tmp/ok"},
	}
	e := newServeLeaderTestEnv(projects, activeBundle)
	out := startServeLeaders(context.Background(), e.deps)

	if out[0].Reason != "path_missing" || out[1].Reason != "config_error" || out[2].Reason != "started" {
		t.Fatalf("不可用工程应跳过且不影响其它工程: %+v", out)
	}
	if len(e.launches) != 1 {
		t.Fatalf("只有可用工程该起循环，实际 %v", e.launches)
	}
	if n := e.countEvents(leadLoopAttemptEvent); n != 3 {
		t.Fatalf("每个工程（含跳过）都该留记录，实际 %d", n)
	}
}

func TestServeLeaderDefaultDepsListsRegisteredProjects(t *testing.T) {
	gdb := filepath.Join(t.TempDir(), "global.db")
	store, err := db.OpenStore(gdb)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(func(tx *sql.Tx) error {
		for _, p := range []*db.Project{
			{ID: "aaaaaaaaaaaaaaaa", Name: "alpha", Path: "/tmp/alpha", MainBranch: "main"},
			{ID: "bbbbbbbbbbbbbbbb", Name: "beta", Path: "/tmp/beta", MainBranch: "main"},
		} {
			if err := db.UpsertProject(tx, p); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	projects, err := defaultServeLeaderDeps(gdb).projects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 2 {
		t.Fatalf("启动器应看到全部已登记工程，实际 %d: %+v", len(projects), projects)
	}
	if projects[0].Name != "alpha" || projects[1].Name != "beta" {
		t.Fatalf("工程清单顺序/内容不对: %+v", projects)
	}
}

func TestServeLeaderRecordExitWritesEvent(t *testing.T) {
	projects := []serveLeaderProject{{ID: "aaa", Name: "alpha", Path: "/tmp/alpha"}}
	e := newServeLeaderTestEnv(projects, activeBundle)
	rec := &leadLoopErrorRecorder{
		now: func() time.Time { return e.now }, window: leadLoopErrorWindow,
		write: e.deps.write, last: map[string]time.Time{},
	}
	recordServeLeaderExit(e.deps, rec, "aaa", fmt.Errorf("wrapped: %w", dispatch.ErrCoordinatorActive))
	if n := e.countEvents(leadLoopLockSkippedEvent); n != 1 {
		t.Fatalf("包装过的锁错误也应识别为抢锁失败，实际 %d", n)
	}
	recordServeLeaderExit(e.deps, rec, "aaa", errors.New("cycle failed"))
	if n := e.countEvents(leadLoopErrorEvent); n != 1 {
		t.Fatalf("非锁错误应写 lead_loop_error，实际 %d", n)
	}
}
