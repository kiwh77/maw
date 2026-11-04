package app_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
)

func TestStatusReportInjectsAgentAndEvent(t *testing.T) {
	global := t.TempDir()
	t.Setenv("MAW_GLOBAL", global)
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")

	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_AGENT", "cursor-demo-1")
	msg := "building feature"
	pct := 40
	res, err := app.StatusReport(repo, app.StatusReportInput{
		Message: msg, Phase: "code", Pct: &pct, Client: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("%v", res)
	}
	gdb := filepath.Join(global, "maw.db")
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		agents, e := db.ListAgents(tx, "")
		if e != nil {
			return e
		}
		if len(agents) == 0 {
			t.Fatal("no agents")
		}
		found := false
		for _, a := range agents {
			if a.ID == "cursor-demo-1" && a.Status == "working" {
				found = true
			}
		}
		if !found {
			t.Fatalf("agents=%v", agents)
		}
		evs, e := db.ListEventsSince(tx, 0, "", "cursor-demo-1", 50)
		if e != nil {
			return e
		}
		if len(evs) == 0 || evs[len(evs)-1].Type != "external_progress" {
			t.Fatalf("events=%v", evs)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := app.LoadEventFeed(gdb, 0, "", "")
	if err != nil || len(feed) == 0 {
		t.Fatalf("feed=%v %v", feed, err)
	}
}

func TestStatusReportLogPathMergesMeta(t *testing.T) {
	global := t.TempDir()
	t.Setenv("MAW_GLOBAL", global)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(home, ".maw-test-status-log")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(logDir) })
	logPath := filepath.Join(logDir, "agent.jsonl")
	if err := os.WriteFile(logPath, []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_AGENT", "cursor-demo-1")

	res, err := app.StatusReport(repo, app.StatusReportInput{
		Message: "with log", LogPath: logPath, Client: "cursor",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res["log_path"] == nil {
		t.Fatalf("expected log_path in result: %v", res)
	}

	gdb := filepath.Join(global, "maw.db")
	var projectID string
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		agents, e := db.ListAgents(tx, "")
		if e != nil {
			return e
		}
		for _, a := range agents {
			if a.ID == "cursor-demo-1" {
				projectID = a.ProjectID
				if a.Meta["log_path"] == nil {
					t.Fatalf("meta missing log_path: %#v", a.Meta)
				}
				// Preserve unrelated keys across a second report without log_path.
				a.Meta["app"] = "cursor"
				return db.UpsertAgent(tx, a)
			}
		}
		t.Fatal("agent missing")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.StatusReport(repo, app.StatusReportInput{Message: "heartbeat"}); err != nil {
		t.Fatal(err)
	}
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		a, e := db.GetAgent(tx, projectID, "cursor-demo-1")
		if e != nil || a == nil {
			return e
		}
		if a.Meta["log_path"] == nil || a.Meta["app"] != "cursor" {
			t.Fatalf("meta not preserved: %#v", a.Meta)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	chunk, err := app.AgentLog(projectID, "cursor-demo-1", 0, true)
	if err != nil || !chunk.OK || chunk.Missing || !strings.Contains(chunk.Chunk, "line1") {
		t.Fatalf("AgentLog=%#v err=%v", chunk, err)
	}
}

// newStatusRepo 建一个最小工程（真 git 仓 + 临时全局库），供 kind 用例复用。
func newStatusRepo(t *testing.T) (repo, gdb string) {
	t.Helper()
	global := t.TempDir()
	t.Setenv("MAW_GLOBAL", global)
	repo = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("x\n"), 0o644)
	run("add", ".")
	run("commit", "-m", "init")
	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAW_AGENT", "cursor-demo-1")
	return repo, filepath.Join(global, "maw.db")
}

// lastEventKind 读回最近一条事件里的 data.kind。
func lastEventKind(t *testing.T, gdb string) (string, bool) {
	t.Helper()
	var kind string
	var ok bool
	if err := db.WithTx(gdb, func(tx *sql.Tx) error {
		evs, e := db.ListEventsSince(tx, 0, "", "cursor-demo-1", 200)
		if e != nil {
			return e
		}
		if len(evs) == 0 {
			return nil
		}
		raw, has := evs[len(evs)-1].Data["kind"]
		if has {
			kind, ok = raw.(string), true
		}
		return nil
	}); err != nil {
		t.Fatalf("read events: %v", err)
	}
	return kind, ok
}

// TestStatusReportKindRecorded 覆盖四种 kind 各写一次且可读回（契约 §2.3）。
func TestStatusReportKindRecorded(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	for _, want := range []string{"progress", "question", "blocker", "final"} {
		res, err := app.StatusReport(repo, app.StatusReportInput{
			Message: "kind=" + want, Kind: want, Client: "test",
		})
		if err != nil {
			t.Fatalf("kind=%s: %v", want, err)
		}
		if got, _ := res["kind"].(string); got != want {
			t.Fatalf("kind=%s 时结果里 kind=%q", want, got)
		}
		if got, ok := lastEventKind(t, gdb); !ok || got != want {
			t.Fatalf("事件里读回的 kind=%q ok=%v，期望 %q", got, ok, want)
		}
	}
}

// TestStatusReportKindDefaultsToProgress 覆盖旧客户端：不传 kind 时按 progress，
// 纯心跳（没 message）不发明分型（契约 §2.2）。
func TestStatusReportKindDefaultsToProgress(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	res, err := app.StatusReport(repo, app.StatusReportInput{Message: "老客户端只说进度"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := res["kind"].(string); got != "progress" {
		t.Fatalf("省略 kind 应回落 progress，得到 %q", got)
	}
	if got, ok := lastEventKind(t, gdb); !ok || got != "progress" {
		t.Fatalf("事件 kind=%q ok=%v", got, ok)
	}

	if _, err := app.StatusReport(repo, app.StatusReportInput{Actor: "cursor-demo-1"}); err != nil {
		t.Fatalf("纯心跳: %v", err)
	}
	if got, ok := lastEventKind(t, gdb); ok {
		t.Fatalf("纯心跳不应写 kind，得到 %q", got)
	}
}

// TestStatusReportRejectsInvalidKind 覆盖非法值：拒绝且不写事件。
func TestStatusReportRejectsInvalidKind(t *testing.T) {
	repo, gdb := newStatusRepo(t)
	before, _ := lastEventKind(t, gdb)
	if _, err := app.StatusReport(repo, app.StatusReportInput{
		Message: "乱填", Kind: "urgent",
	}); err == nil {
		t.Fatal("非法 kind 必须被拒绝")
	} else if !strings.Contains(err.Error(), "kind") {
		t.Fatalf("错误信息应提到 kind: %v", err)
	}
	after, ok := lastEventKind(t, gdb)
	if after != before || ok {
		t.Fatalf("非法 kind 不应写事件：before=%q after=%q ok=%v", before, after, ok)
	}
}
