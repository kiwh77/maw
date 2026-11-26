package db

import (
	"database/sql"
	"errors"
	"testing"
)

// seedRemovableProject 造两个工程：p1 带完整关联数据、p2 只有登记行，
// 用来验证「移除只影响目标工程」。
func seedRemovableProject(t *testing.T, tx *sql.Tx) {
	t.Helper()
	now := UTCNow()
	for _, p := range []*Project{
		{ID: "p1", Name: "Removable", Path: "/tmp/removable", MainBranch: "main", MaxParallel: 2,
			CreatedAt: now, LastSeen: now},
		{ID: "p2", Name: "Keeper", Path: "/tmp/keeper", MainBranch: "main", MaxParallel: 2,
			CreatedAt: now, LastSeen: now},
	} {
		if err := UpsertProject(tx, p); err != nil {
			t.Fatal(err)
		}
	}

	tasks := []*Task{
		{ProjectID: "p1", ID: "T-1", Title: "done one", Status: "done", Priority: 10,
			Deps: []string{}, WriteScope: []string{}, ReadScope: []string{}, Acceptance: []string{}},
		{ProjectID: "p1", ID: "T-2", Title: "in flight", Status: "in_progress", Priority: 20,
			Owner: "cursor-demo-1", Deps: []string{}, WriteScope: []string{}, ReadScope: []string{},
			Acceptance: []string{}, Heartbeat: now},
		{ProjectID: "p2", ID: "T-1", Title: "other project", Status: "ready", Priority: 10,
			Deps: []string{}, WriteScope: []string{}, ReadScope: []string{}, Acceptance: []string{}},
	}
	for _, task := range tasks {
		if err := UpsertTask(tx, task); err != nil {
			t.Fatal(err)
		}
	}

	agents := []*Agent{
		{ProjectID: "p1", ID: "cursor-demo-1", Role: "worker", Status: "working",
			TaskID: "T-2", Worktree: "/wt", Branch: "b", RegisteredAt: now, LastSeen: now},
		{ProjectID: "p1", ID: "cursor-demo-2", Role: "worker", Status: "offline",
			RegisteredAt: now, LastSeen: now},
	}
	for _, a := range agents {
		if err := UpsertAgent(tx, a); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := tx.Exec(
		`INSERT INTO locks(project_id, path, owner, task_id, expires_at) VALUES (?,?,?,?,?)`,
		"p1", "internal/a", "cursor-demo-1", "T-2", now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages(ts, project_id, sender, recipient, type, task_id, body_json)
		 VALUES (?,?,?,?,?,?,?)`,
		now, "p1", "cursor-demo-1", "integrator", "note", "T-2", "{}",
	); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSharedFile(tx, "p1", "shared/a.txt", "T-2"); err != nil {
		t.Fatal(err)
	}
	if err := AppendEvent(tx, "p1", "cursor-demo-1", "progress", "T-2", map[string]any{"m": "halfway"}); err != nil {
		t.Fatal(err)
	}
}

func TestProjectRemovalEvidenceListsOnlyLiveWork(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		seedRemovableProject(t, tx)

		tasks, agents, err := ProjectRemovalEvidence(tx, "p1", "")
		if err != nil {
			return err
		}
		if len(tasks) != 1 || tasks[0].ID != "T-2" || tasks[0].Status != "in_progress" {
			t.Fatalf("活跃任务应为 T-2（done 不算）：%+v", tasks)
		}
		if tasks[0].Owner != "cursor-demo-1" {
			t.Fatalf("活跃任务应带上持有者：%+v", tasks[0])
		}
		// offline 的 Agent 是历史行，不该拦住移除。
		if len(agents) != 1 || agents[0].ID != "cursor-demo-1" {
			t.Fatalf("在线 Agent 应只有 cursor-demo-1：%+v", agents)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectRemovalEvidenceHonoursHeartbeatWindow(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		seedRemovableProject(t, tx)
		// 心跳窗口起点晚于最后一次心跳：没有 Agent 可算在线，但状态非 offline。
		tasks, agents, err := ProjectRemovalEvidence(tx, "p1", "2999-01-01T00:00:00Z")
		if err != nil {
			return err
		}
		if len(agents) != 0 {
			t.Fatalf("窗口外的心跳不该判为在线：%+v", agents)
		}
		// 任务判定不受心跳窗口影响：状态是活的就必须拦住。
		if len(tasks) != 1 {
			t.Fatalf("任务判定不该被心跳窗口影响：%+v", tasks)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProjectRemovalEvidenceMissingProject(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := ProjectRemovalEvidence(tx, "nope", "")
		if !errors.Is(err, ErrProjectNotFound) {
			t.Fatalf("未登记工程应回 ErrProjectNotFound，得到 %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPurgeProjectRemovesOnlyTargetAndKeepsLedger(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		seedRemovableProject(t, tx)

		// 先记下目标工程与旁观工程的行数基线。
		instancesBefore, versionsBefore, eventsBefore, usageBefore, err := RetainedLedgerCounts(tx, "p1")
		if err != nil {
			return err
		}
		if instancesBefore == 0 {
			t.Fatal("前置数据应有任务实例，否则本用例证明不了台账保留")
		}

		counts, err := PurgeProject(tx, "p1")
		if err != nil {
			return err
		}
		if counts.Tasks != 2 {
			t.Fatalf("应删掉 p1 的 2 条任务，得到 %d", counts.Tasks)
		}
		if counts.Agents != 2 || counts.Locks != 1 || counts.Messages != 1 || counts.SharedFiles != 1 {
			t.Fatalf("关联行删除计数不符：%+v", counts)
		}
		if counts.Projects != 1 {
			t.Fatalf("工程登记应删掉 1 行，得到 %d", counts.Projects)
		}

		// 目标工程读不到，旁观工程完好。
		gone, err := GetProject(tx, "p1")
		if err != nil {
			return err
		}
		if gone != nil {
			t.Fatalf("p1 应已移除：%+v", gone)
		}
		keeper, err := GetProject(tx, "p2")
		if err != nil {
			return err
		}
		if keeper == nil || keeper.Name != "Keeper" {
			t.Fatalf("p2 不该被牵连：%+v", keeper)
		}
		keptTasks, err := ListTasks(tx, "p2")
		if err != nil {
			return err
		}
		if len(keptTasks) != 1 {
			t.Fatalf("p2 的任务应保留：%+v", keptTasks)
		}

		// 台账保留：实例／版本／事件条数不变。
		instancesAfter, versionsAfter, eventsAfter, usageAfter, err := RetainedLedgerCounts(tx, "p1")
		if err != nil {
			return err
		}
		if instancesAfter != instancesBefore || versionsAfter != versionsBefore {
			t.Fatalf("实例／版本台账不该被移除：前 %d/%d 后 %d/%d",
				instancesBefore, versionsBefore, instancesAfter, versionsAfter)
		}
		if eventsAfter != eventsBefore || usageAfter != usageBefore {
			t.Fatalf("事件／用量台账不该被移除：前 %d/%d 后 %d/%d",
				eventsBefore, usageBefore, eventsAfter, usageAfter)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPurgeProjectRetiresActiveInstances(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		seedRemovableProject(t, tx)

		var instanceID string
		if err := tx.QueryRow(
			`SELECT instance_id FROM tasks WHERE project_id = 'p1' AND id = 'T-2'`,
		).Scan(&instanceID); err != nil {
			return err
		}

		if _, err := PurgeProject(tx, "p1"); err != nil {
			return err
		}

		inst, err := GetInstance(tx, instanceID)
		if err != nil {
			return err
		}
		if inst == nil {
			t.Fatalf("实例台账应保留：%s", instanceID)
		}
		if inst.State != "retired" || inst.RetiredReason == nil || *inst.RetiredReason != RetireProjectRemoved {
			t.Fatalf("活跃实例应被退役并写明原因：%+v", inst)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPurgeProjectMissingProject(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		if _, err := PurgeProject(tx, "nope"); !errors.Is(err, ErrProjectNotFound) {
			t.Fatalf("未登记工程应回 ErrProjectNotFound，得到 %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
