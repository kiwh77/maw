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
