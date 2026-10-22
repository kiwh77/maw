package app_test

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
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
