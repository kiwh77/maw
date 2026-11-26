package db

import (
	"database/sql"
	"errors"
	"testing"
)

// seedCASTask 落一条归属/元数据可改的任务行。
func seedCASTask(t *testing.T, path, id, owner, title string) {
	t.Helper()
	if err := WithTx(path, func(tx *sql.Tx) error {
		if p, err := GetProject(tx, "p1"); err != nil {
			return err
		} else if p == nil {
			if err := UpsertProject(tx, &Project{
				ID: "p1", Name: "Demo", Path: "/tmp/demo", MainBranch: "main",
				MaxParallel: 2, CreatedAt: UTCNow(), LastSeen: UTCNow(),
			}); err != nil {
				return err
			}
		}
		return UpsertTask(tx, &Task{
			ProjectID: "p1", ID: id, Title: title, Status: "ready", Owner: owner,
			Priority: 10, MergeOrder: 10,
			Deps: []string{}, WriteScope: []string{}, ReadScope: []string{}, Acceptance: []string{"ok"},
		})
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestTaskCASRevisionMonotonic 覆盖成功路径：每写一次 revision 恰好 +1。
func TestTaskCASRevisionMonotonic(t *testing.T) {
	path := openTestDB(t)
	seedCASTask(t, path, "T-1", "alice", "初始标题")

	for want := 1; want <= 3; want++ {
		prev, err := taskRevisionForTest(path, "T-1")
		if err != nil {
			t.Fatalf("read revision: %v", err)
		}
		if prev != want-1 {
			t.Fatalf("第 %d 次写入前 revision=%d，期望 %d", want, prev, want-1)
		}
		err = WithTx(path, func(tx *sql.Tx) error {
			_, e := UpdateTaskCAS(tx, "p1", "T-1", prev, func(task *Task) error {
				task.Title = "标题 v" + string(rune('0'+want))
				return nil
			})
			return e
		})
		if err != nil {
			t.Fatalf("第 %d 次 CAS 写入失败: %v", want, err)
		}
		got, err := taskRevisionForTest(path, "T-1")
		if err != nil {
			t.Fatalf("read revision: %v", err)
		}
		if got != want {
			t.Fatalf("写入后 revision=%d，期望 %d（必须单调 +1，不跳号）", got, want)
		}
	}
}

// TestTaskCASRevisionMismatch 覆盖冲突路径：期望版本过期时整体回滚，owner/title 不变。
func TestTaskCASRevisionMismatch(t *testing.T) {
	path := openTestDB(t)
	seedCASTask(t, path, "T-2", "alice", "原始标题")

	// 先被别人合法改派一次：revision 0 → 1，owner 变成 bob。
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, e := UpdateTaskCAS(tx, "p1", "T-2", 0, func(task *Task) error {
			task.Owner = "bob"
			return nil
		})
		return e
	}); err != nil {
		t.Fatalf("第一次改派失败: %v", err)
	}

	// 拿着旧快照（revision=0）再改派：必须失败且不落盘。
	err := WithTx(path, func(tx *sql.Tx) error {
		_, e := UpdateTaskCAS(tx, "p1", "T-2", 0, func(task *Task) error {
			task.Owner = "carol"
			task.Title = "偷偷覆盖"
			return nil
		})
		return e
	})
	if err == nil {
		t.Fatal("过期 revision 必须被拒绝")
	}
	if !IsRevisionMismatch(err) {
		t.Fatalf("错误应是 revision_mismatch，得到 %v", err)
	}
	var mm *RevisionMismatch
	if !errors.As(err, &mm) {
		t.Fatalf("无法取出 RevisionMismatch：%v", err)
	}
	if mm.Expected != 0 || mm.Actual != 1 || mm.Code() != "revision_mismatch" {
		t.Fatalf("冲突细节不对：expected=%d actual=%d code=%s", mm.Expected, mm.Actual, mm.Code())
	}
	if mm.Details()["actual_revision"] != 1 {
		t.Fatalf("details.actual_revision 应为 1，得到 %v", mm.Details()["actual_revision"])
	}

	got, err := readTaskForTest(path, "T-2")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Owner != "bob" || got.Title != "原始标题" {
		t.Fatalf("冲突写入不应落盘：owner=%q title=%q", got.Owner, got.Title)
	}
	if got.Revision != 1 {
		t.Fatalf("冲突后 revision 不应变化：%d", got.Revision)
	}
}

// TestUpsertTaskCASRejectsStaleSnapshot 覆盖「持有快照直接整行写回」的路径。
func TestUpsertTaskCASRejectsStaleSnapshot(t *testing.T) {
	path := openTestDB(t)
	seedCASTask(t, path, "T-3", "alice", "原始标题")

	stale, err := readTaskForTest(path, "T-3")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// 期间有人把 owner 改走：revision 0 → 1。
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, e := UpdateTaskCAS(tx, "p1", "T-3", 0, func(task *Task) error {
			task.Owner = "bob"
			return nil
		})
		return e
	}); err != nil {
		t.Fatalf("并发改派失败: %v", err)
	}

	stale.Owner = "carol"
	err = WithTx(path, func(tx *sql.Tx) error {
		return UpsertTaskCAS(tx, stale, 0)
	})
	if !IsRevisionMismatch(err) {
		t.Fatalf("过期快照必须得到 revision_mismatch，得到 %v", err)
	}
	got, err := readTaskForTest(path, "T-3")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Owner != "bob" {
		t.Fatalf("过期快照覆盖了 owner：%q", got.Owner)
	}

	// 带上正确期望值即可写入，并前进 1。
	fresh, err := readTaskForTest(path, "T-3")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	fresh.Owner = "carol"
	if err := WithTx(path, func(tx *sql.Tx) error {
		return UpsertTaskCAS(tx, fresh, 1)
	}); err != nil {
		t.Fatalf("正确 revision 应写入成功: %v", err)
	}
	got, err = readTaskForTest(path, "T-3")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Owner != "carol" || got.Revision != 2 {
		t.Fatalf("成功写入后应为 carol/rev2，得到 %q/rev%d", got.Owner, got.Revision)
	}
}

// taskRevisionForTest 在独立事务里读 revision，避免测试用长事务遮住并发。
func taskRevisionForTest(path, id string) (int, error) {
	var rev int
	err := WithTx(path, func(tx *sql.Tx) error {
		v, e := TaskRevision(tx, "p1", id)
		rev = v
		return e
	})
	return rev, err
}

func readTaskForTest(path, id string) (*Task, error) {
	var out *Task
	err := WithTx(path, func(tx *sql.Tx) error {
		t, e := GetTask(tx, "p1", id)
		out = t
		return e
	})
	return out, err
}
