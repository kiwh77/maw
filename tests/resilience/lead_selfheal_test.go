package resilience

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/systemagent"
	"github.com/kiwh77/maw/tests/testsupport"
)

// 本文件是 SH-900 的端到端韧性验收：五条场景全部走公开入口（app.Should 的真实装配），
// 不用内部测试夹具替换任何端口，确保「能跑」与「装得上」是同一件事。

// mustTx 在全局库上跑一个写事务，失败立即终止用例。
func mustTx(t *testing.T, gdb string, fn func(*sql.Tx) error) {
	t.Helper()
	if err := db.WithTx(gdb, fn); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
}

// readTx 在全局库上跑一个只读事务，失败立即终止用例。
func readTx(t *testing.T, gdb string, fn func(*sql.Tx) error) {
	t.Helper()
	if err := db.WithTx(gdb, fn); err != nil {
		t.Fatalf("读库失败: %v", err)
	}
}

// setTOMLValue 把仓外 config.toml 里某个段落的键改成指定值，不动其它行。
func setTOMLValue(t *testing.T, repo, section, key, value string) {
	t.Helper()
	local, err := home.MawHome(repo)
	if err != nil {
		t.Fatalf("maw home: %v", err)
	}
	path := home.ConfigPath(local)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读配置: %v", err)
	}
	text := string(raw)
	header := "[" + section + "]"
	start := strings.Index(text, header)
	if start < 0 {
		text = strings.TrimRight(text, "\n") + "\n\n" + header + "\n" + key + " = " + value + "\n"
	} else {
		end := len(text)
		if i := strings.Index(text[start+len(header):], "\n["); i >= 0 {
			end = start + len(header) + i + 1
		}
		block := text[start:end]
		line := key + " = " + value
		re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `\s*=.*$`)
		if re.MatchString(block) {
			block = re.ReplaceAllString(block, line)
		} else {
			block = strings.TrimRight(block, "\n") + "\n" + line + "\n"
		}
		text = text[:start] + block + text[end:]
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatalf("写配置: %v", err)
	}
}

// enableSystemAgent 打开系统代理，让 Watcher.PollOnce 真正推进。
func enableSystemAgent(t *testing.T, repo string) {
	t.Helper()
	setTOMLValue(t, repo, "system_agent", "enabled", "true")
	// 本文件的场景只验证「回收／编排是否按预期走」，不验证队列卡死信号入队。
	// RT-1 之后五类卡死信号会按 auto_kinds 产作业，那会让这里的 enqueued 计数
	// 混入无关来源；因此把 auto_kinds 收窄到队列信号不用的类型。
	setTOMLValue(t, repo, "system_agent", "auto_kinds", `["ops_run"]`)
}

// enableManagedAuto 打开受管自动执行（自动执行开关 + 并发上限）。
func enableManagedAuto(t *testing.T, repo, pid string, limit int) {
	t.Helper()
	yes := true
	if _, err := app.ProjectUpdate(repo, app.ProjectUpdateInput{
		Ref: pid, AutoEnabled: &yes, Limit: &limit,
	}); err != nil {
		t.Fatalf("开启自动执行: %v", err)
	}
}

// seedReadyNode 落一个观测齐全、可被匹配的受管节点。
func seedReadyNode(t *testing.T, gdb, pid, nodeID string) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339)
	mustTx(t, gdb, func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{
			ID: nodeID, ProjectID: pid, Label: "resilience-node",
			SlotLimit: 1, CreatedAt: now, LastSeen: now,
			Meta: map[string]any{
				"entry_state": "ready", "auth_state": "ready",
				"connection_state": "fresh", "capacity_state": "available",
				"observed_at": now, "accept_new": true,
				// 关掉自动探测：测试里没有真实 CLI，探测会把节点改回 missing。
				"auto_probe": false,
			},
		})
	})
}

// waitFor 轮询直到条件成立，超时即失败。
func waitFor(t *testing.T, timeout time.Duration, what string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等待超时：%s", what)
}

// newExtraProject 在同一全局库里登记第二个工程，用于跨工程隔离场景。
func newExtraProject(t *testing.T, h *testsupport.Harness) (repo, pid string) {
	t.Helper()
	repo = t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# extra\n"), 0o644)
	for _, mod := range []string{"mod_a", "mod_b"} {
		_ = os.MkdirAll(filepath.Join(repo, mod), 0o755)
		_ = os.WriteFile(filepath.Join(repo, mod, ".gitkeep"), nil, 0o644)
	}
	git("add", ".")
	git("commit", "-m", "init")
	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatalf("init extra: %v", err)
	}
	bp := filepath.Join(repo, "bp.toml")
	_ = os.WriteFile(bp, []byte(testsupport.DefaultTasks), 0o644)
	if _, err := app.PlanApply(bp, repo, true, false, false); err != nil {
		t.Fatalf("plan apply extra: %v", err)
	}
	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatalf("re-init extra: %v", err)
	}
	var found string
	var listed []string
	want, _ := filepath.EvalSymlinks(repo)
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		ps, e := db.ListProjects(tx)
		if e != nil {
			return e
		}
		for _, p := range ps {
			if p == nil {
				continue
			}
			listed = append(listed, p.ID+"@"+p.Path)
			got, _ := filepath.EvalSymlinks(p.Path)
			if got == want {
				found = p.ID
			}
		}
		return nil
	})
	if found == "" {
		t.Fatalf("找不到第二工程登记：%v（repo=%s）", listed, repo)
	}
	return repo, found
}

// TestSH900CrashedLeaderJobIsReclaimed 场景 1：leader 中途崩溃，超租约作业被下一轮巡检回收。
func TestSH900CrashedLeaderJobIsReclaimed(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	enableSystemAgent(t, h.Repo)

	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	mustTx(t, h.GDB, func(tx *sql.Tx) error {
		// 最后一次尝试期间崩溃：回收后直接落 failed 终态，不会在下一轮被重新执行，
		// reaped_from 证据保留在 result 里。
		_, err := db.InsertSystemJob(tx, &db.SystemJob{
			JobID: "job-crash-1", ProjectID: h.PID, Kind: systemagent.SystemKindUnblock,
			Status: db.SystemJobRunning, Attempts: 1, MaxAttempts: 1,
			LockedBy: "crashed-leader", LockedAt: old,
			CreatedAt: old, UpdatedAt: old,
			Decision: map[string]any{"message": "crashed mid-run"},
		})
		return err
	})

	enqueued, ran := 0, 0
	if res, err := app.SystemWatch(h.Repo, true); err != nil {
		t.Fatalf("system watch: %v", err)
	} else {
		enqueued, _ = res["enqueued"].(int)
		ran, _ = res["ran"].(int)
	}
	if enqueued != 0 || ran != 0 {
		t.Fatalf("本场景不应有 else 动作: enqueued=%d ran=%d", enqueued, ran)
	}

	var job *db.SystemJob
	var all []*db.SystemJob
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		var e error
		job, e = db.GetSystemJob(tx, "job-crash-1")
		if e != nil {
			return e
		}
		all, e = db.ListSystemJobs(tx, "", "", 20)
		return e
	})
	if job == nil {
		t.Fatalf("作业丢失；现有作业: %+v", all)
	}
	if job.Status != db.SystemJobFailed {
		t.Fatalf("用尽次数的崩溃作业应收割为 failed，实际 %s", job.Status)
	}
	if job.LockedBy != "" {
		t.Fatalf("回收后应清空锁持有者，实际 %q", job.LockedBy)
	}
	if fmt.Sprint(job.Result["reaped_from"]) != db.SystemJobRunning {
		t.Fatalf("缺少 reaped_from 证据: %+v", job.Result)
	}
}

// TestSH900TwoProjectsDoNotStealDispatchOps 场景 2：A 工程派发只消费自己的操作队列。
func TestSH900TwoProjectsDoNotStealDispatchOps(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	repoB, pidB := newExtraProject(t, h)
	enableManagedAuto(t, h.Repo, h.PID, 1)
	seedReadyNode(t, h.GDB, h.PID, "node-a")

	// B 工程先放一条更早的 pending 操作：若队列没有按工程隔离，A 的 drain 会先吃掉它。
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	mustTx(t, h.GDB, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO operations(operation_id, project_id, kind, run_id, instance_id,
idempotency_key, request_digest, target_phase, result, coordinator, lease_expires_at, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			"op-b-1", pidB, "dispatch", "", "", "key-b-1", "digest-b-1", "",
			"pending", "", old, old, old)
		return err
	})

	res, err := app.SystemTrigger(h.Repo, systemagent.SystemKindDispatchReady, "", "resilience")
	if err != nil {
		t.Fatalf("trigger dispatch: %v", err)
	}
	jobID, _ := res["job_id"].(string)
	if jobID == "" {
		t.Fatalf("没有拿到作业: %+v", res)
	}
	if _, err := app.SystemRun(h.Repo, jobID); err != nil {
		t.Fatalf("run dispatch job: %v", err)
	}

	var aRuns, bTaskStatus, opBResult int
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE project_id = ?`, h.PID).Scan(&aRuns); err != nil {
			return err
		}
		var st string
		if err := tx.QueryRow(`SELECT status FROM tasks WHERE project_id = ? AND id = 'VN-R1'`, pidB).Scan(&st); err != nil {
			return err
		}
		bTaskStatus = map[string]int{"ready": 1, "claimed": 2, "in_progress": 3}[st]
		return tx.QueryRow(`SELECT CASE WHEN result = 'pending' THEN 1 ELSE 0 END FROM operations WHERE operation_id = 'op-b-1'`).Scan(&opBResult)
	})
	if aRuns == 0 {
		t.Fatal("A 工程没有产生 Run，场景前提不成立")
	}
	if opBResult != 1 {
		t.Fatal("B 工程的 pending 操作被 A 工程消费了")
	}
	if bTaskStatus != 1 {
		t.Fatalf("B 工程任务不应被 A 的派发放动，实际状态码 %d", bTaskStatus)
	}
	if repoB == "" {
		t.Fatal("第二工程未创建")
	}
}

// TestSH900ReconcileReleasesConfirmedKeepsUnknown 场景 3：证据齐全的 Run 被释放，
// 证据不明的 Run 保留占用。
func TestSH900ReconcileReleasesConfirmedKeepsUnknown(t *testing.T) {
	h := testsupport.NewHarness(t, "")

	inst1 := taskInstance(t, h, "VN-R1")
	inst2 := taskInstance(t, h, "VN-R2")
	old := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	ws := filepath.Join(t.TempDir(), "workspaces", "VN-R1")

	mustTx(t, h.GDB, func(tx *sql.Tx) error {
		// 两条 Run：一条从未启动（可释放），一条有启动时间（现场不明，保留）。
		for _, spec := range []struct{ id, inst, started string }{
			{"run-release", inst1, ""},
			{"run-keep", inst2, old},
		} {
			if _, err := tx.Exec(`INSERT INTO runs(
run_id, project_id, instance_id, version_no, generation, phase, reconciliation_state,
created_at, updated_at, started_at, finished_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				spec.id, h.PID, spec.inst, 1, 1, "running", "unknown",
				old, old, spec.started, ""); err != nil {
				return err
			}
		}
		// 释放场景：任务仍由实例持有；节点槽与未冻结的目录各占一条。
		task, e := db.GetTask(tx, h.PID, "VN-R1")
		if e != nil {
			return e
		}
		task.Status, task.Owner = "in_progress", "w1"
		if err := db.UpsertTask(tx, task); err != nil {
			return err
		}
		for _, res := range []db.Resource{
			{Kind: model.LeaseNodeSlot, NodeID: "node-re", Slot: 1, ProjectID: h.PID},
			{Kind: model.LeaseWorkspace, Path: ws, ProjectID: h.PID},
		} {
			if _, err := db.AcquireLease(tx, db.LeaseSpec{
				ProjectID: h.PID, InstanceID: inst1, RunID: "run-release",
				Generation: 1, Holder: "w1",
			}, res); err != nil {
				return err
			}
		}
		// 保留场景：任务与节点槽都还在。
		task, e = db.GetTask(tx, h.PID, "VN-R2")
		if e != nil {
			return e
		}
		task.Status, task.Owner = "in_progress", "w2"
		if err := db.UpsertTask(tx, task); err != nil {
			return err
		}
		if _, err := db.AcquireLease(tx, db.LeaseSpec{
			ProjectID: h.PID, InstanceID: inst2, RunID: "run-keep",
			Generation: 1, Holder: "w2",
		}, db.Resource{Kind: model.LeaseNodeSlot, NodeID: "node-re", Slot: 2, ProjectID: h.PID}); err != nil {
			return err
		}
		now := time.Now().UTC().Format(time.RFC3339)
		for _, spec := range []struct{ id, run, task, inst string }{
			{"job-rc-release", "run-release", "VN-R1", inst1},
			{"job-rc-keep", "run-keep", "VN-R2", inst2},
		} {
			if _, err := db.InsertSystemJob(tx, &db.SystemJob{
				JobID: spec.id, ProjectID: h.PID, Kind: systemagent.SystemKindRunReconcile,
				Status: db.SystemJobQueued, TaskID: spec.task, InstanceID: spec.inst,
				Attempts: 0, MaxAttempts: 3, CreatedAt: now, UpdatedAt: now,
				Decision: map[string]any{"run_id": spec.run},
			}); err != nil {
				return err
			}
		}
		return nil
	})

	for _, jobID := range []string{"job-rc-release", "job-rc-keep"} {
		if _, err := app.SystemRun(h.Repo, jobID); err != nil {
			t.Fatalf("%s: %v", jobID, err)
		}
	}

	var releaseMode, keepMode string
	var releasePhase, releaseRecon, keepPhase string
	var taskReady, taskHeld string
	var nodeReleased, wsReleased bool
	var keptEvent, unknownEvent int
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		job, e := db.GetSystemJob(tx, "job-rc-release")
		if e != nil {
			return e
		}
		releaseMode = fmt.Sprint(job.Result["mode"])
		job, e = db.GetSystemJob(tx, "job-rc-keep")
		if e != nil {
			return e
		}
		keepMode = fmt.Sprint(job.Result["mode"])
		if e := tx.QueryRow(`SELECT phase, reconciliation_state FROM runs WHERE run_id = 'run-release'`).Scan(&releasePhase, &releaseRecon); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT phase FROM runs WHERE run_id = 'run-keep'`).Scan(&keepPhase); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT status FROM tasks WHERE project_id = ? AND id = 'VN-R1'`, h.PID).Scan(&taskReady); e != nil {
			return e
		}
		if e := tx.QueryRow(`SELECT status FROM tasks WHERE project_id = ? AND id = 'VN-R2'`, h.PID).Scan(&taskHeld); e != nil {
			return e
		}
		nodeKey, _ := (db.Resource{Kind: model.LeaseNodeSlot, NodeID: "node-re", Slot: 1, ProjectID: h.PID}).Key()
		wsKey, _ := (db.Resource{Kind: model.LeaseWorkspace, Path: ws, ProjectID: h.PID}).Key()
		l, e := db.ActiveLease(tx, nodeKey)
		if e != nil {
			return e
		}
		nodeReleased = l == nil
		l, e = db.ActiveLease(tx, wsKey)
		if e != nil {
			return e
		}
		wsReleased = l == nil
		e = tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND type = 'run_reconcile_workspace_kept' AND task_id = 'VN-R1'`, h.PID).Scan(&keptEvent)
		if e != nil {
			return e
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM run_events WHERE run_id = 'run-keep' AND kind = 'run.reconcile_unknown'`).Scan(&unknownEvent)
	})

	if releaseMode != string(systemagent.ReconcileRelease) {
		t.Fatalf("证据齐全的 Run 应 release，实际 %q", releaseMode)
	}
	if keepMode != string(systemagent.ReconcileKeep) {
		t.Fatalf("证据不明的 Run 应 keep，实际 %q", keepMode)
	}
	if releasePhase != string(model.PhaseFailed) || releaseRecon != "confirmed" {
		t.Fatalf("release 后 Run 应 failed/confirmed，实际 %s/%s", releasePhase, releaseRecon)
	}
	if keepPhase != string(model.PhaseRunning) {
		t.Fatalf("keep 后 Run 阶段不得改动，实际 %s", keepPhase)
	}
	if taskReady != "ready" || taskHeld != "in_progress" {
		t.Fatalf("任务回退错误: release=%s keep=%s", taskReady, taskHeld)
	}
	if !nodeReleased {
		t.Fatal("release 后节点槽应已释放")
	}
	if wsReleased {
		t.Fatal("未冻结的目录不应被释放")
	}
	if keptEvent != 1 {
		t.Fatalf("应记录一次 workspace_kept 事件，实际 %d", keptEvent)
	}
	if unknownEvent != 1 {
		t.Fatalf("keep 应记录 run.reconcile_unknown 事件，实际 %d", unknownEvent)
	}
}

// TestSH900HousekeepingExpiresOfflineAgent 场景 4：无 worker 调 next 时，
// 中枢自己的巡检也会把过期心跳的 Agent 标成 offline。
func TestSH900HousekeepingExpiresOfflineAgent(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	enableSystemAgent(t, h.Repo)

	old := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339)
	mustTx(t, h.GDB, func(tx *sql.Tx) error {
		return db.UpsertAgent(tx, &db.Agent{
			ProjectID: h.PID, ID: "stale-worker", Role: "worker",
			Status: "working", TaskID: "VN-R1",
			RegisteredAt: old, LastSeen: old,
		})
	})

	// 中枢常驻循环里才装配了巡检；这里用真实 Serve 入口，不偷偷复用内部函数。
	go func() { _, _ = app.Serve(h.Repo, "127.0.0.1:0", app.BoardFilters{}) }()

	var status string
	waitFor(t, 10*time.Second, "过期 Agent 被标记 offline", func() bool {
		readTx(t, h.GDB, func(tx *sql.Tx) error {
			status = ""
			agents, e := db.ListAgents(tx, h.PID)
			if e != nil {
				return e
			}
			for _, a := range agents {
				if a != nil && a.ID == "stale-worker" {
					status = a.Status
				}
			}
			return nil
		})
		return status == "offline"
	})

	var expired int
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND type = 'agent_expired'`, h.PID).Scan(&expired)
	})
	if expired != 1 {
		t.Fatalf("应记录一次 agent_expired 事件，实际 %d", expired)
	}
}

// TestSH900AutoSelectDispatchesOnce 场景 5：打开 auto_select 后，ready 题被自动派发一次，
// 不会因为下一轮生产而重复领取。
func TestSH900AutoSelectDispatchesOnce(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	enableManagedAuto(t, h.Repo, h.PID, 1)
	setTOMLValue(t, h.Repo, "managed", "auto_select", "true")
	seedReadyNode(t, h.GDB, h.PID, "node-auto")

	go func() { _, _ = app.Serve(h.Repo, "127.0.0.1:0", app.BoardFilters{}) }()

	waitFor(t, 10*time.Second, "ready 题被自动派发", func() bool {
		var n int
		readTx(t, h.GDB, func(tx *sql.Tx) error {
			return tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE project_id = ?`, h.PID).Scan(&n)
		})
		return n >= 1
	})

	// 生产者只按空闲周期重跑；这里等一小段确认没有「同一题被二次领取」。
	time.Sleep(500 * time.Millisecond)
	var runs, active int
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		if e := tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE project_id = ?`, h.PID).Scan(&runs); e != nil {
			return e
		}
		return tx.QueryRow(`SELECT COUNT(*) FROM tasks WHERE project_id = ? AND status IN ('claimed','in_progress','self_check','in_review','merging')`, h.PID).Scan(&active)
	})
	if runs != 1 {
		t.Fatalf("ready 题只应被自动派发一次，实际 %d 条 Run", runs)
	}
	if active != 1 {
		t.Fatalf("只应有 1 道题被占用，实际 %d", active)
	}

	var jobCount int
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM system_jobs WHERE project_id = ? AND kind = ?`, h.PID, systemagent.SystemKindDispatchReady).Scan(&jobCount)
	})
	if jobCount != 1 {
		t.Fatalf("应只有一条 dispatch_ready 作业，实际 %d", jobCount)
	}
}

// taskInstance 取一道初始化任务的任务实例身份。
func taskInstance(t *testing.T, h *testsupport.Harness, taskID string) string {
	t.Helper()
	var inst string
	readTx(t, h.GDB, func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, h.PID, taskID)
		if e != nil {
			return e
		}
		if task == nil {
			return fmt.Errorf("任务 %s 不存在", taskID)
		}
		inst = task.InstanceID
		return nil
	})
	return inst
}
