package db

import (
	"database/sql"
	"errors"
	"testing"
)

func TestIterationCRUDAndTaskBinding(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		if err := UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo", MainBranch: "main",
			MaxParallel: 2, CreatedAt: UTCNow(), LastSeen: UTCNow(),
		}); err != nil {
			return err
		}

		it := &Iteration{
			ProjectID: "p1", ID: "IT-001", Name: "第一波",
			Goal: "交付迭代容器", TargetBranch: "main",
		}
		if err := UpsertIteration(tx, it); err != nil {
			return err
		}
		if it.Status != IterationPlanning || it.CreatedAt == "" {
			t.Fatalf("默认状态／创建时间未补齐：%+v", it)
		}

		task := &Task{
			ProjectID: "p1", ID: "T-1", Title: "绑定迭代", Status: "ready",
			Priority: 10, IterationID: "IT-001",
			Deps: []string{}, WriteScope: []string{}, ReadScope: []string{}, Acceptance: []string{},
		}
		if err := UpsertTask(tx, task); err != nil {
			return err
		}
		gotTask, err := GetTask(tx, "p1", "T-1")
		if err != nil {
			return err
		}
		if gotTask == nil || gotTask.IterationID != "IT-001" {
			t.Fatalf("任务未绑定迭代：%+v", gotTask)
		}

		filtered, err := ListTasksFiltered(tx, "p1", "", "")
		if err != nil {
			return err
		}
		if len(filtered) != 1 || filtered[0].IterationID != "IT-001" {
			t.Fatalf("筛选列表丢了迭代归属：%+v", filtered)
		}

		n, err := CountTasksByIteration(tx, "p1", "IT-001")
		if err != nil {
			return err
		}
		if n != 1 {
			t.Fatalf("迭代任务计数应为 1，得到 %d", n)
		}
		counts, err := IterationTaskCounts(tx, "p1")
		if err != nil {
			return err
		}
		if counts["IT-001"] != 1 {
			t.Fatalf("按迭代分组计数不符：%+v", counts)
		}

		created := it.CreatedAt
		it.Status = IterationRunning
		it.UpdatedAt = ""
		if err := UpsertIteration(tx, it); err != nil {
			return err
		}
		got, err := GetIteration(tx, "p1", "IT-001")
		if err != nil {
			return err
		}
		if got.Status != IterationRunning || got.CreatedAt != created {
			t.Fatalf("更新状态不应改写创建时间：%+v", got)
		}

		if err := DeleteIteration(tx, "p1", "IT-001"); !errors.Is(err, ErrIterationNotEmpty) {
			t.Fatalf("仍有任务时应拒绝删除，得到 %v", err)
		}
		task.IterationID = ""
		if err := UpsertTask(tx, task); err != nil {
			return err
		}
		if err := DeleteIteration(tx, "p1", "IT-001"); err != nil {
			return err
		}
		gone, err := GetIteration(tx, "p1", "IT-001")
		if err != nil {
			return err
		}
		if gone != nil {
			t.Fatalf("迭代应已删除：%+v", gone)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIterationValidation(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		if err := UpsertIteration(tx, &Iteration{
			ProjectID: "p1", ID: "IT-001", Name: "坏状态", Status: "bogus",
		}); err == nil {
			t.Fatal("非法状态应被拒绝")
		}
		if err := UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo", MainBranch: "main",
			MaxParallel: 2, CreatedAt: UTCNow(), LastSeen: UTCNow(),
		}); err != nil {
			return err
		}
		if err := UpsertTask(tx, &Task{
			ProjectID: "p1", ID: "T-1", Title: "未知迭代", Status: "ready", Priority: 10,
			IterationID: "IT-999",
		}); !errors.Is(err, ErrIterationNotFound) {
			t.Fatalf("未知迭代应回 ErrIterationNotFound，得到 %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestIterationMigrationUpgradesLegacyDB(t *testing.T) {
	path := freshPath(t)
	seedLegacyDB(t, path)
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}

	var name string
	conn := mustOpenRead(t, path)
	if err := conn.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'iterations'`,
	).Scan(&name); err != nil {
		t.Fatalf("旧库升级后应出现 iterations 表：%v", err)
	}
	var iterationID string
	if err := conn.QueryRow(
		`SELECT iteration_id FROM tasks WHERE project_id = ? AND id = 'VN-001'`, testProject,
	).Scan(&iterationID); err != nil {
		t.Fatalf("旧任务应补上 iteration_id 列：%v", err)
	}
	if iterationID != "" {
		t.Fatalf("旧任务默认不属于任何迭代，得到 %q", iterationID)
	}
}

func TestPurgeProjectRemovesIterations(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		seedRemovableProject(t, tx)
		if err := UpsertIteration(tx, &Iteration{
			ProjectID: "p1", ID: "IT-001", Name: "待移除",
		}); err != nil {
			return err
		}
		if err := UpsertIteration(tx, &Iteration{
			ProjectID: "p2", ID: "IT-001", Name: "保留",
		}); err != nil {
			return err
		}

		counts, err := PurgeProject(tx, "p1")
		if err != nil {
			return err
		}
		if counts.Iterations != 1 {
			t.Fatalf("应删掉 p1 的 1 条迭代，得到 %d", counts.Iterations)
		}
		gone, err := ListIterations(tx, "p1")
		if err != nil {
			return err
		}
		if len(gone) != 0 {
			t.Fatalf("p1 迭代应清空：%+v", gone)
		}
		kept, err := ListIterations(tx, "p2")
		if err != nil {
			return err
		}
		if len(kept) != 1 || kept[0].Name != "保留" {
			t.Fatalf("p2 迭代不该被牵连：%+v", kept)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
