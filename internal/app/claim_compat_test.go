package app

import (
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/home"
)

// fixture 是一套真实工程：临时 git 仓 + 临时全局库，走的是 maw init 的生产路径。
//
// 不 mock 数据库：领取的「只能一个赢」全靠 SQLite 的条件写入与事务重试，
// 换成内存假实现就把要验证的东西验证没了。
type fixture struct {
	t    *testing.T
	repo string
	pc   *projCtx
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	t.Setenv("MAW_GLOBAL", t.TempDir())
	repo := t.TempDir()
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
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("# f\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")

	if _, err := Init(repo, false, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	pc, err := resolveProject(repo)
	if err != nil {
		t.Fatalf("resolveProject: %v", err)
	}
	return &fixture{t: t, repo: repo, pc: pc}
}

// seedTask 直接落一条 ready 任务；instance 由 UpsertTask 绑定（与 plan apply 同路径）。
func (f *fixture) seedTask(id, status, owner string, writeScope []string) *db.Task {
	f.t.Helper()
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.UpsertTask(tx, &db.Task{
			ProjectID:  f.pc.ID(),
			ID:         id,
			Title:      "task " + id,
			Status:     status,
			Owner:      owner,
			Priority:   10,
			MergeOrder: 10,
			WriteScope: writeScope,
			Acceptance: []string{"ok"},
		})
	})
	if err != nil {
		f.t.Fatalf("seed %s: %v", id, err)
	}
	task, err := getTask(f.pc, id)
	if err != nil || task == nil {
		f.t.Fatalf("read back %s: %v", id, err)
	}
	return task
}

// task 读回一条任务行。
func (f *fixture) task(id string) *db.Task {
	f.t.Helper()
	t, err := getTask(f.pc, id)
	if err != nil {
		f.t.Fatalf("read %s: %v", id, err)
	}
	if t == nil {
		f.t.Fatalf("任务 %s 不存在", id)
	}
	return t
}

// setMaxParallel 改看板参考值（它不该影响领取决策）。
//
// max_parallel 的权威来源是 config.toml，不是 projects 表——resolveProject 每次都会
// 用配置覆盖库里的值，所以这里改配置文件再重新解析，才是生产里会发生的路径。
func (f *fixture) setMaxParallel(n int) {
	f.t.Helper()
	local, err := home.RequireHome(f.repo)
	if err != nil {
		f.t.Fatalf("require home: %v", err)
	}
	path := home.ConfigPath(local)
	raw, err := os.ReadFile(path)
	if err != nil {
		f.t.Fatalf("read config: %v", err)
	}
	replaced := false
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "max_parallel") {
			lines[i] = "max_parallel = " + strconv.Itoa(n)
			replaced = true
		}
	}
	if !replaced {
		f.t.Fatalf("配置里没有 max_parallel 项：\n%s", raw)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		f.t.Fatalf("write config: %v", err)
	}
	pc, err := resolveProject(f.repo)
	if err != nil {
		f.t.Fatalf("reload project: %v", err)
	}
	f.pc = pc
}

// managedClaim 走受管领取用例，等价于 ClaimExecution 被派发方调用。
func (f *fixture) managedClaim(agent, taskID, workspace string) error {
	f.t.Helper()
	store, err := db.OpenStore(f.pc.GlobalDB)
	if err != nil {
		f.t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	nodeID := "node-lab"
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{
			ID: nodeID, ProjectID: f.pc.ID(), Label: "lab", SlotLimit: 4,
		})
	})
	if err != nil {
		f.t.Fatalf("register node: %v", err)
	}

	svc := claim.New(store, nil)
	_, err = svc.Claim(f.t.Context(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:      model.ProjectID(f.pc.ID()),
			AgentID:        model.AgentID(agent),
			IdempotencyKey: "dispatch:" + taskID + ":" + agent,
		},
		NodeID:    model.NodeID(nodeID),
		Workspace: workspace,
	})
	return err
}

// TestManualClaimLosesRaceInsideWindow 是本卡第一条验收的确定性证据。
//
// 手工领取的「选题」与「写盘」是两步，中间就是真实存在的竞争窗口。这里把那一步
// 固定下来：先选题，再让受管 ClaimExecution 领走同一道题，最后手工写入。
// 手工这一侧必须放弃：既不能覆盖持有者，也不能把状态写回 in_progress——
// 否则两个 Agent 会在各自认为归自己的任务上写盘。
func TestManualClaimLosesRaceInsideWindow(t *testing.T) {
	f := newFixture(t)
	const id = "VN-W1"
	f.seedTask(id, "ready", "", []string{"mod_w1/**"})

	target, err := pickClaimTarget(f.pc, "cursor-loser")
	if err != nil {
		t.Fatalf("选题：%v", err)
	}
	if target.ID != id {
		t.Fatalf("应选中 %s，实际 %s", id, target.ID)
	}

	// 窗口内受管领取先落地。
	if err := f.managedClaim("managed-winner", id, filepath.Join(f.repo, "ws-w1")); err != nil {
		t.Fatalf("受管领取应成功：%v", err)
	}

	got, err := commitClaim(f.pc, "cursor-loser", ".worktrees/cursor-loser", target)
	if err == nil {
		t.Fatalf("窗口内被抢先，条件写入不应命中，却拿到 %v", got.ID)
	}
	if !errors.Is(err, errClaimLost) {
		t.Fatalf("应判为「被别人抢先」，实际 %v", err)
	}
	if detail := err.(*claimLostError); detail.Owner != "managed-winner" {
		t.Fatalf("应报出真正持有者，实际 %q", detail.Owner)
	}

	task := f.task(id)
	if task.Owner != "managed-winner" {
		t.Fatalf("持有者被覆盖成 %q", task.Owner)
	}
	if task.Status != string(model.TaskClaimed) {
		t.Fatalf("状态被改成 %s，受管领取的 claimed 应保持", task.Status)
	}
}

// TestManualNextAndManagedClaimCompete 是并发版本：两个真实 goroutine 同时抢同一道题。
//
// 窗口够窄时它们也可能先后错开（那就各自成功、根本不冲突），所以这里断言的是
// 更强的不变量：**绝不会两个都成功**，且落库的持有者与唯一成功者一致。
func TestManualNextAndManagedClaimCompete(t *testing.T) {
	f := newFixture(t)

	const rounds = 8
	for i := 0; i < rounds; i++ {
		id := "VN-C" + strings.Repeat("0", 2) + string(rune('A'+i))
		f.seedTask(id, "ready", "", []string{"mod_" + id + "/**"})

		manualAgent := "cursor-comp-" + string(rune('a'+i))
		managedAgent := "managed-comp-" + string(rune('a'+i))

		var wg sync.WaitGroup
		var manualErr, managedErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _, manualErr = claimManual(f.pc, manualAgent, filepath.Join(".worktrees", manualAgent))
		}()
		go func() {
			defer wg.Done()
			managedErr = f.managedClaim(managedAgent, id, filepath.Join(f.repo, "ws-"+id))
		}()
		wg.Wait()

		winner := 0
		if manualErr == nil {
			winner++
		}
		if managedErr == nil {
			winner++
		}
		if winner != 1 {
			t.Fatalf("第 %d 轮：期望恰好一个领取者成功，实际 %d（manual=%v managed=%v）",
				i, winner, manualErr, managedErr)
		}

		task := f.task(id)
		switch {
		case manualErr == nil:
			if task.Owner != manualAgent {
				t.Fatalf("第 %d 轮：手工赢了但持有者是 %q", i, task.Owner)
			}
			if task.Status != string(model.TaskClaimed) {
				t.Fatalf("第 %d 轮：手工领取应停在 claimed（目录未就绪），实际 %s", i, task.Status)
			}
		default:
			if task.Owner != managedAgent {
				t.Fatalf("第 %d 轮：受管赢了但持有者是 %q", i, task.Owner)
			}
		}

		// 输的一方必须报「被谁抢了」，不能静默成功或返回空结果。
		if winner == 1 {
			var loserErr error
			if manualErr != nil {
				loserErr = manualErr
			} else {
				loserErr = managedErr
			}
			if loserErr == nil || loserErr.Error() == "" {
				t.Fatalf("第 %d 轮：输的一方没有给出原因", i)
			}
		}
	}
}

// TestManualClaimKeepsScheduleRules 覆盖「原有依赖／scope／亲和策略仍生效」：
// 依赖未完成的任务不会被 claimManual 领走，写盘重叠的在跑任务也会挡住后来者。
func TestManualClaimKeepsScheduleRules(t *testing.T) {
	f := newFixture(t)

	blocker := f.seedTask("VN-B1", "in_progress", "cursor-holder", []string{"internal/core/**"})
	if blocker.Status != "in_progress" {
		t.Fatalf("准备失败：%s", blocker.Status)
	}
	f.seedTask("VN-B2", "ready", "", []string{"internal/core/**"}) // 与在跑任务写盘重叠

	if _, _, err := claimManual(f.pc, "cursor-late", ".worktrees/cursor-late"); err == nil {
		t.Fatal("写盘重叠的任务不应被领取")
	}

	// 依赖未完成：backlog 状态下也不会被选中。
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.UpsertTask(tx, &db.Task{
			ProjectID: f.pc.ID(), ID: "VN-B3", Title: "dep", Status: "ready",
			Priority: 10, MergeOrder: 10, Deps: []string{"VN-NOPE"},
			WriteScope: []string{"internal/free/**"}, Acceptance: []string{"ok"},
		})
	})
	if err != nil {
		t.Fatalf("seed dep: %v", err)
	}
	if _, _, err := claimManual(f.pc, "cursor-late", ".worktrees/cursor-late"); err == nil {
		t.Fatal("依赖未完成的任务不应被领取")
	}
}

// TestExpireStaleAgentsKeepsRunOccupancy 覆盖「心跳过期不能把仍计入占用的任务打回可重派」：
// GAP-004 之后 maw next 也建 Run，因此人工领取与受管领取同规则——Run 还占着目录，
// 就不能退回 ready，否则下一个 Agent 会拿到同一份工作目录。
func TestExpireStaleAgentsKeepsRunOccupancy(t *testing.T) {
	f := newFixture(t)

	managedTask := f.seedTask("VN-O1", "ready", "", []string{"mod_o1/**"})
	manualTask := f.seedTask("VN-O2", "ready", "", []string{"mod_o2/**"})

	// 受管领取：建 Run，任务进入 claimed。
	if err := f.managedClaim("cursor-managed", managedTask.ID, filepath.Join(f.repo, "ws-o1")); err != nil {
		t.Fatalf("managed claim: %v", err)
	}
	// 手工领取：GAP-004 之后同样建 Run（占用语义与受管一致）。
	if _, _, err := claimManual(f.pc, "cursor-manual", ".worktrees/cursor-manual"); err != nil {
		t.Fatalf("manual claim: %v", err)
	}

	// 两个 Agent 的心跳都放到过期之外。
	// 受管领取不登记 Agent 行（派发方从别的通道上报心跳），所以这里显式补上，
	// 否则过期扫描根本没有对象可扫——那会掩盖「有占用时保留」这条规则。
	stale := "2020-01-01T00:00:00Z"
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		for _, a := range []struct{ id, task string }{
			{"cursor-managed", managedTask.ID},
			{"cursor-manual", manualTask.ID},
		} {
			if e := db.UpsertAgent(tx, &db.Agent{
				ProjectID: f.pc.ID(), ID: a.id, Role: "worker", Status: "working",
				TaskID: a.task, LastSeen: stale,
			}); e != nil {
				return e
			}
		}
		// 受管 Run 的核实状态置为 unknown：进程是否还在写盘无人知道。
		_, e := tx.Exec(`UPDATE runs SET reconciliation_state = 'unknown' WHERE project_id = ?`, f.pc.ID())
		return e
	})
	if err != nil {
		t.Fatalf("stale agents: %v", err)
	}

	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return expireStaleAgents(tx, f.pc)
	}); err != nil {
		t.Fatalf("expire: %v", err)
	}

	kept := f.task(managedTask.ID)
	if kept.Status != string(model.TaskClaimed) || kept.Owner != "cursor-managed" {
		t.Fatalf("受管未知占用不得被打回可重派：status=%s owner=%q", kept.Status, kept.Owner)
	}
	if kept.Status == string(model.TaskReady) {
		t.Fatal("任务被退回 ready，下一个 Agent 会拿到同一份工作目录")
	}

	manualKept := f.task(manualTask.ID)
	if manualKept.Status != string(model.TaskClaimed) || manualKept.Owner != "cursor-manual" {
		t.Fatalf("有 Run 的人工领取同样不得被打回可重派：status=%s owner=%q", manualKept.Status, manualKept.Owner)
	}

	// 必须留下可追溯的证据：两条占用都要有「保留了哪条占用、为什么」。
	var keptEvents int
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ? AND type = 'agent_expired_occupancy_kept' AND task_id IN (?, ?)`,
			f.pc.ID(), managedTask.ID, manualTask.ID).Scan(&keptEvents)
	})
	if err != nil {
		t.Fatalf("count events: %v", err)
	}
	if keptEvents != 2 {
		t.Fatalf("应留下两条「保留占用」事件，实际 %d", keptEvents)
	}
}

// TestStatusReportDoesNotStealOwnership 覆盖旧心跳占用语义的收口：
// 一条进度上报不得凭空取得任务所有权，只能续自己已持有的任务。
func TestStatusReportDoesNotStealOwnership(t *testing.T) {
	f := newFixture(t)
	idle := f.seedTask("VN-S1", "ready", "", []string{"mod_s1/**"})

	t.Setenv("MAW_AGENT", "cursor-reporter")
	if _, err := StatusReport(f.repo, StatusReportInput{TaskID: idle.ID, Message: "peek"}); err != nil {
		t.Fatalf("status report: %v", err)
	}
	if got := f.task(idle.ID); got.Owner != "" {
		t.Fatalf("旁路上报取得了所有权：owner=%q", got.Owner)
	}
	// 看板上的「谁在做」也不能被这条上报改写。
	var agentTask string
	err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		a, e := db.GetAgent(tx, f.pc.ID(), "cursor-reporter")
		if e != nil || a == nil {
			return e
		}
		agentTask = a.TaskID
		return nil
	})
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if agentTask != "" {
		t.Fatalf("没有所有权却把自己写成该任务的执行者：%q", agentTask)
	}

	// 自己持有的任务：心跳要续上。
	owned := f.seedTask("VN-S2", "in_progress", "cursor-reporter", []string{"mod_s2/**"})
	before := owned.Heartbeat
	if _, err := StatusReport(f.repo, StatusReportInput{TaskID: owned.ID, Message: "still here"}); err != nil {
		t.Fatalf("status report: %v", err)
	}
	after := f.task(owned.ID)
	if after.Heartbeat == before {
		t.Fatal("持有者自己的上报应刷新心跳")
	}
	if after.Owner != "cursor-reporter" {
		t.Fatalf("持有者被改写：%q", after.Owner)
	}
}

// TestResumeClaimKeepsUncommittedWork 覆盖崩溃续做：任务卡在 claimed、目录里已有
// 未提交的改动时，续做只推进状态，不得 reset——否则 Agent 半天的活会被抹掉。
func TestResumeClaimKeepsUncommittedWork(t *testing.T) {
	f := newFixture(t)
	const id = "VN-R1"
	f.seedTask(id, "ready", "", []string{"mod_r1/**"})

	target, err := pickClaimTarget(f.pc, "cursor-resumer")
	if err != nil {
		t.Fatalf("选题：%v", err)
	}
	claimed, err := commitClaim(f.pc, "cursor-resumer", filepath.Join(".worktrees", "cursor-resumer"), target)
	if err != nil {
		t.Fatalf("领取：%v", err)
	}
	if claimed.Status != string(model.TaskClaimed) {
		t.Fatalf("领取后应停在 claimed，实际 %s", claimed.Status)
	}

	// 模拟「目录已建好、改动未提交」：手工造一个工作目录与一个脏文件。
	wt := filepath.Join(f.repo, claimed.Worktree)
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	dirty := filepath.Join(wt, "wip.txt")
	if err := os.WriteFile(dirty, []byte("未提交的活\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := resumeClaim(f.pc, claimed, "cursor-resumer"); err != nil {
		t.Fatalf("续做：%v", err)
	}
	if got := f.task(id); got.Status != string(model.TaskInProgress) {
		t.Fatalf("续做后应推进到 in_progress，实际 %s", got.Status)
	}
	if _, err := os.Stat(dirty); err != nil {
		t.Fatalf("续做把未提交的改动弄丢了：%v", err)
	}
}

// TestMaxParallelIsReferenceOnly 覆盖「max_parallel 参考值与新增真实上限清楚区分」：
// 参考值设成 1 也不影响领取；受管上限只由 ClaimExecution 的 Capacity 决定。
func TestMaxParallelIsReferenceOnly(t *testing.T) {
	f := newFixture(t)
	f.setMaxParallel(1)

	limits := limitsOf(f.pc)
	if limits["max_parallel_reference"] != 1 {
		t.Fatalf("参考值应如实报出：%v", limits)
	}
	if limits["managed_limit"] != 0 {
		t.Fatalf("手工领取不受受管上限约束，应报 0：%v", limits)
	}

	f.seedTask("VN-M1", "ready", "", []string{"mod_m1/**"})
	f.seedTask("VN-M2", "ready", "", []string{"mod_m2/**"})

	first, _, err := claimManual(f.pc, "cursor-m1", ".worktrees/cursor-m1")
	if err != nil {
		t.Fatalf("第一道领取：%v", err)
	}
	second, _, err := claimManual(f.pc, "cursor-m2", ".worktrees/cursor-m2")
	if err != nil {
		t.Fatalf("第二道领取：%v（max_parallel 不应成为硬上限）", err)
	}
	if first.ID == second.ID {
		t.Fatal("两次领取拿到同一道题")
	}

	// 领取事件要记下参考值，同时标明受管上限不适用。
	var raw string
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT data_json FROM events WHERE project_id = ? AND type = 'claimed' AND task_id = ?`,
			f.pc.ID(), first.ID).Scan(&raw)
	})
	if err != nil {
		t.Fatalf("读领取事件：%v", err)
	}
	for _, want := range []string{`"max_parallel_reference":1`, `"managed_limit_applies":false`} {
		if !strings.Contains(raw, want) {
			t.Fatalf("领取事件缺少 %s：%s", want, raw)
		}
	}
}

// TestNextCreatesRunAndOperation 是本卡第一条验收：maw next 一次成功即产出 Run 与
// Operation（Operation 落 applied，不留在待派发队列里等协调者起 Runner），
// 同时老的 JSON 形状一个字段都不能少。
func TestNextCreatesRunAndOperation(t *testing.T) {
	f := newFixture(t)
	const id = "VN-N1"
	f.seedTask(id, "ready", "", []string{"mod_n1/**"})

	t.Setenv("MAW_AGENT", "cursor-next")
	res, err := Next(f.repo, "cursor-next", false, "")
	if err != nil {
		t.Fatalf("next: %v", err)
	}

	// 老脚本按 task/worktree/branch 取值，字段必须还在。
	for _, k := range []string{
		"ok", "task", "worktree", "branch", "agent", "project_id", "registered", "limits", "env", "next",
	} {
		if _, ok := res[k]; !ok {
			t.Fatalf("兼容字段 %q 丢失：%v", k, res)
		}
	}

	task := f.task(id)
	if task.Status != string(model.TaskInProgress) || task.Owner != "cursor-next" {
		t.Fatalf("next 后应落 in_progress：status=%s owner=%q", task.Status, task.Owner)
	}
	if task.InstanceID == "" {
		t.Fatal("任务缺少 instance，Run 无从建立")
	}

	var run *db.Run
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		run, e = db.ActiveRunForInstance(tx, task.InstanceID)
		return e
	})
	if err != nil {
		t.Fatalf("读 Run：%v", err)
	}
	if run == nil {
		t.Fatal("maw next 成功后必须存在对应 Run")
	}
	if !run.Occupies() {
		t.Fatalf("领取产生的 Run 必须计入占用，实际 phase=%s", run.Phase)
	}
	if got, _ := res["run_id"].(string); got != run.ID {
		t.Fatalf("输出里的 run_id 与库内不一致：res=%q db=%q", got, run.ID)
	}

	// Operation 必须落 applied：CLI 自领不启动 Runner，也不该给协调者留一单待派发。
	var kind, result string
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`
SELECT kind, result FROM operations WHERE project_id = ? AND run_id = ?`,
			f.pc.ID(), run.ID).Scan(&kind, &result)
	})
	if err != nil {
		t.Fatalf("读 Operation：%v", err)
	}
	if kind != "dispatch" || result != "applied" {
		t.Fatalf("Operation 应为 dispatch/applied，实际 %s/%s", kind, result)
	}

	// 节点槽与目录租约随 Run 一起落下，合入时才释放。
	var leases int
	err = db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM resource_leases WHERE run_id = ? AND released_at IS NULL`,
			run.ID).Scan(&leases)
	})
	if err != nil {
		t.Fatalf("读租约：%v", err)
	}
	if leases == 0 {
		t.Fatal("领取没有落下任何租约")
	}
}

// TestConcurrentNextOnlyOneWins 是本卡第二条验收：两个 Agent 同时 next 同一道题，
// 只有一个拿到，另一个看到已失守（等价错误），不会出现双持有者。
func TestConcurrentNextOnlyOneWins(t *testing.T) {
	f := newFixture(t)
	const id = "VN-R1"
	f.seedTask(id, "ready", "", []string{"mod_r1/**"})

	var wg sync.WaitGroup
	var errA, errB error
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, errA = Next(f.repo, "cursor-race-a", false, "")
	}()
	go func() {
		defer wg.Done()
		_, errB = Next(f.repo, "cursor-race-b", false, "")
	}()
	wg.Wait()

	if (errA == nil) == (errB == nil) {
		t.Fatalf("同一道题应恰好一人成功：a=%v b=%v", errA, errB)
	}
	winner := "cursor-race-a"
	if errA != nil {
		winner = "cursor-race-b"
	}
	task := f.task(id)
	if task.Owner != winner {
		t.Fatalf("唯一持有者应为 %s，实际 owner=%q", winner, task.Owner)
	}
	if task.Status != string(model.TaskInProgress) {
		t.Fatalf("赢家应推进到 in_progress，实际 %s", task.Status)
	}
}

// TestNextWithoutClaimOnlyPreviews 是 CQ-002 的第一条验收：裸 next 只查询。
// 任务必须仍是 ready，不能建 Run、不能写 worktree、不能改归属。
func TestNextWithoutClaimOnlyPreviews(t *testing.T) {
	f := newFixture(t)
	const id = "VN-Q1"
	f.seedTask(id, "ready", "", []string{"mod_q1/**"})

	res, err := NextWith(f.repo, "cursor-peek", NextOptions{})
	if err != nil {
		t.Fatalf("next（查询）: %v", err)
	}
	if preview, _ := res["preview"].(bool); !preview {
		t.Fatalf("查询模式必须标记 preview：%v", res)
	}
	if _, ok := res["run_id"]; ok {
		t.Fatalf("查询模式不得产出 run_id：%v", res)
	}
	if _, ok := res["env"]; ok {
		t.Fatalf("查询模式不得给出领取环境变量：%v", res)
	}

	task := f.task(id)
	if task.Status != string(model.TaskReady) {
		t.Fatalf("查询后任务仍应为 ready，实际 %s", task.Status)
	}
	if task.Owner != "" {
		t.Fatalf("查询不得占用归属，实际 owner=%q", task.Owner)
	}
	if n := activeRunCount(t, f); n != 0 {
		t.Fatalf("查询不得建 Run，实际 %d 条", n)
	}
	if _, statErr := os.Stat(filepath.Join(f.repo, ".worktrees", "cursor-peek")); statErr == nil {
		t.Fatal("查询不得创建 worktree")
	}
}

// TestNextClaimMatchesLegacy 是第二条验收：显式 --claim 之后的状态、run_id 与
// worktree 与改造前的正式领取完全一致。
func TestNextClaimMatchesLegacy(t *testing.T) {
	f := newFixture(t)
	const id = "VN-Q2"
	f.seedTask(id, "ready", "", []string{"mod_q2/**"})

	res, err := NextWith(f.repo, "cursor-claim", NextOptions{Claim: true})
	if err != nil {
		t.Fatalf("next --claim: %v", err)
	}
	for _, k := range []string{
		"ok", "task", "worktree", "branch", "agent", "project_id", "registered", "limits", "env", "next",
	} {
		if _, ok := res[k]; !ok {
			t.Fatalf("领取输出缺少字段 %q：%v", k, res)
		}
	}
	task := f.task(id)
	if task.Status != string(model.TaskInProgress) || task.Owner != "cursor-claim" {
		t.Fatalf("领取后应落 in_progress：status=%s owner=%q", task.Status, task.Owner)
	}
	if got, _ := res["run_id"].(string); got == "" {
		t.Fatalf("领取必须产出 run_id：%v", res)
	}
	wt, _ := res["worktree"].(string)
	if wt == "" {
		t.Fatalf("领取必须给出 worktree：%v", res)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("领取后 worktree 应存在：%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(wt, ".maw", "task.json")); statErr != nil {
		t.Fatalf("领取后应写好任务卡：%v", statErr)
	}
}

// TestNextWithoutClaimKeepsHeldTask 是第三条验收：已持有任务时查询不产生副作用，
// 续做仍由 --claim 走原路径。
func TestNextWithoutClaimKeepsHeldTask(t *testing.T) {
	f := newFixture(t)
	const id = "VN-Q3"
	f.seedTask(id, "ready", "", []string{"mod_q3/**"})

	if _, err := NextWith(f.repo, "cursor-hold", NextOptions{Claim: true}); err != nil {
		t.Fatalf("首次领取: %v", err)
	}
	before := f.task(id)

	peek, err := NextWith(f.repo, "cursor-hold", NextOptions{})
	if err != nil {
		t.Fatalf("查询: %v", err)
	}
	if held, _ := peek["held"].(bool); !held {
		t.Fatalf("已持有任务时查询应报 held：%v", peek)
	}
	after := f.task(id)
	if after.Status != before.Status || after.Revision != before.Revision {
		t.Fatalf("查询不得改动任务：before=%s/%d after=%s/%d",
			before.Status, before.Revision, after.Status, after.Revision)
	}

	res, err := NextWith(f.repo, "cursor-hold", NextOptions{Claim: true})
	if err != nil {
		t.Fatalf("续做: %v", err)
	}
	if idem, _ := res["idempotent"].(bool); !idem {
		t.Fatalf("续做应保持幂等语义：%v", res)
	}
}

func activeRunCount(t *testing.T, f *fixture) int {
	t.Helper()
	var n int
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM runs WHERE project_id = ?`, f.pc.ID()).Scan(&n)
	}); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}
