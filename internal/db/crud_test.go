package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func openTestDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "maw.db")
	if err := Init(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestProjectCRUD(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		p := &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo",
			MainBranch: "main", MaxParallel: 4,
			CreatedAt: UTCNow(), LastSeen: UTCNow(),
		}
		if err := UpsertProject(tx, p); err != nil {
			return err
		}
		got, err := GetProject(tx, "p1")
		if err != nil {
			return err
		}
		if got.Name != "Demo" || got.Path != "/tmp/demo" {
			t.Fatalf("got %+v", got)
		}
		byPath, err := GetProjectByPath(tx, "/tmp/demo")
		if err != nil || byPath.ID != "p1" {
			t.Fatalf("by path: %+v %v", byPath, err)
		}
		list, err := ListProjects(tx)
		if err != nil || len(list) != 1 {
			t.Fatalf("list=%v err=%v", list, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTaskCRUDAndFilter(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		if err := UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo",
			MainBranch: "main", MaxParallel: 2,
			CreatedAt: UTCNow(), LastSeen: UTCNow(),
		}); err != nil {
			return err
		}
		t1 := &Task{
			ProjectID: "p1", ID: "T-1", Title: "one", Status: "ready",
			Priority: 10, MergeOrder: 1, Owner: "",
			Deps: []string{}, WriteScope: []string{"a/"}, ReadScope: []string{},
			Acceptance: []string{"ok"}, Extra: map[string]any{},
		}
		t2 := &Task{
			ProjectID: "p1", ID: "T-2", Title: "two", Status: "in_progress",
			Priority: 20, MergeOrder: 2, Owner: "cursor-demo-1",
			Deps: []string{"T-1"}, WriteScope: []string{"b/"},
			Acceptance: []string{}, Extra: map[string]any{"k": "v"},
			Heartbeat: UTCNow(),
		}
		if err := UpsertTask(tx, t1); err != nil {
			return err
		}
		if err := UpsertTask(tx, t2); err != nil {
			return err
		}
		got, err := GetTask(tx, "p1", "T-2")
		if err != nil {
			return err
		}
		if got.Owner != "cursor-demo-1" || len(got.Deps) != 1 || got.Deps[0] != "T-1" {
			t.Fatalf("task %+v", got)
		}
		if got.Extra["k"] != "v" {
			t.Fatalf("extra=%v", got.Extra)
		}
		all, err := ListTasks(tx, "p1")
		if err != nil || len(all) != 2 {
			t.Fatalf("list=%d err=%v", len(all), err)
		}
		ready, err := ListTasksFiltered(tx, "p1", "", "ready")
		if err != nil || len(ready) != 1 || ready[0].ID != "T-1" {
			t.Fatalf("ready filter: %+v %v", ready, err)
		}
		owned, err := ListTasksFiltered(tx, "p1", "cursor-demo-1", "")
		if err != nil || len(owned) != 1 || owned[0].ID != "T-2" {
			t.Fatalf("agent filter: %+v %v", owned, err)
		}
		found, err := FindTask(tx, "p1", "T-1")
		if err != nil || found.Title != "one" {
			t.Fatalf("find: %+v %v", found, err)
		}
		if err := DeleteTask(tx, "p1", "T-1"); err != nil {
			return err
		}
		missing, err := FindTask(tx, "p1", "T-1")
		if err != nil {
			return err
		}
		if missing != nil {
			t.Fatal("expected missing after delete")
		}
		if err := DeleteTasks(tx, "p1"); err != nil {
			return err
		}
		left, _ := ListTasks(tx, "p1")
		if len(left) != 0 {
			t.Fatalf("left=%d", len(left))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAgentEventsSharedFilesMeta(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		_ = UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo",
			MainBranch: "main", MaxParallel: 2,
			CreatedAt: UTCNow(), LastSeen: UTCNow(),
		})
		a := &Agent{
			ProjectID: "p1", ID: "cursor-demo-1", Role: "worker",
			Status: "working", TaskID: "T-1", Worktree: "/wt", Branch: "b",
			RegisteredAt: UTCNow(), LastSeen: UTCNow(),
			Meta: map[string]any{"app": "cursor"},
		}
		if err := UpsertAgent(tx, a); err != nil {
			return err
		}
		got, err := GetAgent(tx, "p1", "cursor-demo-1")
		if err != nil || got.Status != "working" || got.Meta["app"] != "cursor" {
			t.Fatalf("agent %+v %v", got, err)
		}
		list, err := ListAgentsFiltered(tx, "p1", "cursor-demo-1")
		if err != nil || len(list) != 1 {
			t.Fatalf("agents filter %v %v", list, err)
		}
		if err := AppendEvent(tx, "p1", "cursor-demo-1", "progress", "T-1", map[string]any{"message": "hi"}); err != nil {
			return err
		}
		evs, err := ListEventsByTask(tx, "p1", "T-1", 10)
		if err != nil || len(evs) != 1 || evs[0].Type != "progress" {
			t.Fatalf("events %+v %v", evs, err)
		}
		if msg, _ := evs[0].Data["message"].(string); msg != "hi" {
			t.Fatalf("data=%v", evs[0].Data)
		}
		if err := UpsertSharedFile(tx, "p1", "lock.json", "T-1"); err != nil {
			return err
		}
		sf, err := ListSharedFiles(tx, "p1")
		if err != nil || len(sf) != 1 || sf[0].Path != "lock.json" {
			t.Fatalf("shared %+v %v", sf, err)
		}
		if err := DeleteSharedFiles(tx, "p1"); err != nil {
			return err
		}
		if err := ClearAgentTask(tx, "p1", "cursor-demo-1"); err != nil {
			return err
		}
		cleared, _ := GetAgent(tx, "p1", "cursor-demo-1")
		if cleared.TaskID != "" || cleared.Status != "idle" {
			t.Fatalf("cleared %+v", cleared)
		}
		if err := SetMeta(tx, "schema_version", "3"); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	v, err := GetMeta(raw, "schema_version")
	if err != nil || v != "3" {
		t.Fatalf("meta=%q %v", v, err)
	}
}

func TestWithTxRollback(t *testing.T) {
	path := openTestDB(t)
	_ = WithTx(path, func(tx *sql.Tx) error {
		return UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo",
			MainBranch: "main", MaxParallel: 1,
			CreatedAt: UTCNow(), LastSeen: UTCNow(),
		})
	})
	err := WithTx(path, func(tx *sql.Tx) error {
		_ = UpsertProject(tx, &Project{
			ID: "p2", Name: "Other", Path: "/tmp/other",
			MainBranch: "main", MaxParallel: 1,
			CreatedAt: UTCNow(), LastSeen: UTCNow(),
		})
		return errString("force rollback")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	_ = WithTx(path, func(tx *sql.Tx) error {
		list, e := ListProjects(tx)
		if e != nil {
			return e
		}
		if len(list) != 1 || list[0].ID != "p1" {
			t.Fatalf("rollback failed: %+v", list)
		}
		return nil
	})
}
