package db

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/goal"
)

// seedGoalProject 落一个工程与若干任务，返回任务实例编号。
func seedGoalProject(t *testing.T, path string, taskIDs ...string) map[string]string {
	t.Helper()
	instances := map[string]string{}
	if err := WithTx(path, func(tx *sql.Tx) error {
		if err := UpsertProject(tx, &Project{
			ID: "p1", Name: "Demo", Path: "/tmp/demo", MainBranch: "main",
			MaxParallel: 2, CreatedAt: UTCNow(), LastSeen: UTCNow(),
		}); err != nil {
			return err
		}
		for _, id := range taskIDs {
			task := &Task{
				ProjectID: "p1", ID: id, Title: "任务 " + id, Status: "ready",
				Priority: 10, MergeOrder: 10,
				Deps: []string{}, WriteScope: []string{"internal/goal/**"},
				ReadScope: []string{"README.md"}, Acceptance: []string{"可核对"},
			}
			if err := UpsertTask(tx, task); err != nil {
				return err
			}
			instances[id] = task.InstanceID
		}
		return nil
	}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return instances
}

func testGoal() *goal.Goal {
	return &goal.Goal{
		ProjectID: "p1", GoalID: "gl_pi001ax9", IterationID: "",
		Status: goal.StatusDraft, ActiveVersion: 1,
	}
}

func testGoalVersion(version int) *goal.Version {
	return &goal.Version{
		ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: version,
		Title:     "打通目标级 Lead",
		Statement: "现状：Lead 只有一次性拆卡。目标：目标可被批准、入队与验收。",
		Acceptance: []goal.Acceptance{{
			ID: "AC-1", Kind: goal.AcceptanceCommand,
			Check: "go test ./internal/goal -count=1", Statement: "领域逻辑通过",
			EvidenceRequired: true,
		}},
		Budget: goal.DefaultBudget(),
	}
}

func testPlan(rev int, digest string) *goal.PlanRevision {
	cards := []goal.PlanCard{
		{
			CardID: "c01", ID: "GL-001", Title: "文档卡", Goal: "现状：缺文档。目标：补齐。",
			Priority: 10, MergeOrder: 10, WriteScope: []string{"docs/a.md"},
			ReadScope: []string{"README.md"}, Acceptance: []string{"段落齐全"},
			Verify: []string{"manual: 人工核对"}, NotDoing: []string{"不改代码"},
			Origin: goal.Origin{GoalID: "gl_pi001ax9", PlanRev: rev, CardID: "c01", SourceRefs: []string{"docs/plan.md#1"}},
		},
		{
			CardID: "c02", ID: "GL-002", Title: "代码卡", Goal: "现状：缺实现。目标：补齐。",
			Priority: 20, MergeOrder: 20, WriteScope: []string{"internal/goal/**"},
			ReadScope: []string{"README.md"}, Acceptance: []string{"测试通过"},
			Verify: []string{"go test ./internal/goal -count=1"}, NotDoing: []string{"不改主干"},
			Origin: goal.Origin{GoalID: "gl_pi001ax9", PlanRev: rev, CardID: "c02"},
		},
	}
	return &goal.PlanRevision{
		ProjectID: "p1", GoalID: "gl_pi001ax9", GoalVersion: 1,
		PlanRev: rev, Kind: goal.PlanKindInitial, Status: goal.PlanProposed,
		Baseline:   goal.Baseline{BlueprintSHA256: strings.Repeat("a", 64), TasksRevision: 7, MainBranchTip: "deadbee"},
		PlanDigest: digest, Cards: cards,
	}
}

// buildLegacyGoalDB 构造一个「升级前」的库：只应用 maxVersion 及更早的迁移。
//
// 这是为了证明 v15 是纯增量迁移：老库里的任务与实例身份原样保留，
// 而不是靠「同一个空库重跑一遍 DDL」蒙混过关。
func buildLegacyGoalDB(t *testing.T, path string, maxVersion int) {
	t.Helper()
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ledgerDDL); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	for _, m := range sortedMigrations() {
		if m.Version > maxVersion {
			continue
		}
		if _, err := tx.Exec(m.SQL); err != nil {
			_ = tx.Rollback()
			t.Fatalf("legacy migration %d(%s): %v", m.Version, m.Name, err)
		}
		if m.Apply != nil {
			if err := m.Apply(tx); err != nil {
				_ = tx.Rollback()
				t.Fatalf("legacy migration %d apply: %v", m.Version, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES (?,?,?,?)`,
			m.Version, m.Name, migrationChecksum(m.SQL), UTCNow()); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES (?, ?)`,
		metaSchemaVersionKey, strconv.Itoa(maxVersion)); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestGoalMigrationUpgradePreservesTasks 是验收 1 的迁移部分：
// 老库升级后历史任务、实例身份与状态一条不少，新表可读可写。
func TestGoalMigrationUpgradePreservesTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "maw.db")
	buildLegacyGoalDB(t, path, 14)

	// 升级前写入工程与两道题（此时还没有 goals 表）。
	conn, err := openConn(path, writeConnOptions())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := UpsertProject(tx, &Project{
		ID: "p1", Name: "Demo", Path: "/tmp/demo", MainBranch: "main",
		MaxParallel: 2, CreatedAt: UTCNow(), LastSeen: UTCNow(),
	}); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	legacy := []*Task{
		{ProjectID: "p1", ID: "T-OLD-1", Title: "历史题一", Status: "done", Priority: 5, MergeOrder: 5,
			Deps: []string{}, WriteScope: []string{"x"}, ReadScope: []string{}, Acceptance: []string{"ok"}},
		{ProjectID: "p1", ID: "T-OLD-2", Title: "历史题二", Status: "in_progress", Priority: 6, MergeOrder: 6,
			Owner: "someone", Deps: []string{"T-OLD-1"}, WriteScope: []string{"y"}, ReadScope: []string{}, Acceptance: []string{"ok"}},
	}
	for _, task := range legacy {
		if err := UpsertTask(tx, task); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()

	if err := Migrate(path); err != nil {
		t.Fatalf("升级失败：%v", err)
	}

	if err := WithTx(path, func(tx *sql.Tx) error {
		for _, want := range []struct{ id, status, title string }{
			{"T-OLD-1", "done", "历史题一"},
			{"T-OLD-2", "in_progress", "历史题二"},
		} {
			got, err := GetTask(tx, "p1", want.id)
			if err != nil {
				return err
			}
			if got == nil {
				t.Fatalf("迁移后历史任务 %s 丢了", want.id)
			}
			if got.Status != want.status || got.Title != want.title || got.InstanceID == "" {
				t.Fatalf("历史任务 %s 被改动：%+v", want.id, got)
			}
			if got.ID != want.id {
				t.Fatalf("历史任务身份错位：%s", got.ID)
			}
		}
		// 新表可用：建目标 + 读回。
		if _, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "tester"); err != nil {
			return err
		}
		g, err := GetGoal(tx, "p1", "gl_pi001ax9")
		if err != nil {
			return err
		}
		if g == nil || g.ActiveVersion != 1 {
			t.Fatalf("迁移后目标读不回来：%+v", g)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 迁移必须是纯增量：不得出现删表、删数据、改列。
	for _, forbidden := range []string{"DROP ", "DELETE ", "ALTER ", "TRUNCATE"} {
		if strings.Contains(strings.ToUpper(goalDDL), forbidden) {
			t.Fatalf("goal 迁移出现破坏性语句 %q", forbidden)
		}
	}
	// 台账末端推到 v15。
	if err := WithTx(path, func(tx *sql.Tx) error {
		return VerifySchema(tx)
	}); err != nil {
		t.Fatalf("升级后 schema 未到末端：%v", err)
	}
}

// TestGoalSnapshotSurvivesRestart 是验收 1 的读取部分：
// 关掉连接重开，仍能读到当前目标、验收条件、计划版本与所关联任务。
func TestGoalSnapshotSurvivesRestart(t *testing.T) {
	path := openTestDB(t)
	instances := seedGoalProject(t, path, "GL-001", "GL-002")

	if err := WithTx(path, func(tx *sql.Tx) error {
		if _, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "lead"); err != nil {
			return err
		}
		// 目标版本 2：改验收条件必须产生新版本，而不是改历史。
		v2 := testGoalVersion(2)
		v2.Acceptance = append(v2.Acceptance, goal.Acceptance{
			ID: "AC-2", Kind: goal.AcceptanceEvidence,
			Check: "delivery.merged=true", Statement: "交付已合入", EvidenceRequired: true,
		})
		if _, err := AppendGoalVersion(tx, v2, 0, "lead"); err != nil {
			return err
		}
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusActive, "", "lead", 1); err != nil {
			return err
		}
		rev := testPlan(1, "")
		rev.GoalVersion = 2
		saved, err := SavePlanRevision(tx, rev, "lead")
		if err != nil {
			return err
		}
		if _, _, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 1, "alice", goal.PolicyManual, saved.PlanDigest, "lead"); err != nil {
			return err
		}
		if _, _, err := MarkPlanRevisionApplied(tx, "p1", "gl_pi001ax9", 1, "lead"); err != nil {
			return err
		}
		for _, card := range []struct{ cardID, taskID string }{{"c01", "GL-001"}, {"c02", "GL-002"}} {
			key := goal.EnqueueKey("p1", "gl_pi001ax9", 1, saved.PlanDigest, card.cardID)
			if _, _, err := RecordEnqueue(tx, &goal.EnqueueRecord{
				ProjectID: "p1", EnqueueKey: key, GoalID: "gl_pi001ax9",
				PlanRev: 1, CardID: card.cardID, PlanDigest: saved.PlanDigest,
				State: goal.EnqueueAppended,
			}); err != nil {
				return err
			}
			if _, _, err := RecordEnqueue(tx, &goal.EnqueueRecord{
				ProjectID: "p1", EnqueueKey: key, GoalID: "gl_pi001ax9",
				PlanRev: 1, CardID: card.cardID, PlanDigest: saved.PlanDigest,
				State: goal.EnqueueApplied,
			}); err != nil {
				return err
			}
			if _, _, err := RecordTaskLink(tx, &goal.TaskLink{
				ProjectID: "p1", GoalID: "gl_pi001ax9", PlanRev: 1, CardID: card.cardID,
				InstanceID: instances[card.taskID], DisplayID: card.taskID, VersionNo: 1,
				EnqueueKey: key, State: goal.LinkApplied, AppliedBy: "lead",
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("准备快照失败：%v", err)
	}

	// 重新打开（新连接、新事务），模拟重启。
	if err := WithTx(path, func(tx *sql.Tx) error {
		snap, err := LoadGoalSnapshot(tx, "p1", "gl_pi001ax9")
		if err != nil {
			return err
		}
		if snap.Goal.Status != goal.StatusActive || snap.Goal.Revision != 2 {
			t.Fatalf("目标状态／revision 没恢复：%+v", snap.Goal)
		}
		if snap.Active == nil || snap.Active.GoalVersion != 2 {
			t.Fatalf("当前目标版本不对：%+v", snap.Active)
		}
		if len(snap.Active.Acceptance) != 2 || snap.Active.Acceptance[1].ID != "AC-2" {
			t.Fatalf("验收条件没恢复：%+v", snap.Active.Acceptance)
		}
		if len(snap.Versions) != 2 {
			t.Fatalf("历史版本应保留 2 份，得到 %d", len(snap.Versions))
		}
		if len(snap.Plans) != 1 || snap.Plans[0].Status != goal.PlanApplied || snap.Plans[0].AppliedAt == "" {
			t.Fatalf("计划修订没恢复：%+v", snap.Plans)
		}
		if len(snap.TaskLinks) != 2 || len(snap.Enqueues) != 2 {
			t.Fatalf("映射／台账没恢复：links=%d enqueues=%d", len(snap.TaskLinks), len(snap.Enqueues))
		}
		if len(snap.Tasks) != 2 {
			t.Fatalf("所关联任务没恢复：%d", len(snap.Tasks))
		}
		for _, task := range snap.Tasks {
			if task.Status != "ready" || task.ID == "" || task.InstanceID == "" {
				t.Fatalf("关联任务快照不对：%+v", task)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestGoalRevisionCAS 是验收 2 的并发部分：读到旧 revision 的写入必须整体失败。
func TestGoalRevisionCAS(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "lead")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// 两个写者都读到 revision 0。
	var stale error
	if err := WithTx(path, func(tx *sql.Tx) error {
		g, err := GetGoal(tx, "p1", "gl_pi001ax9")
		if err != nil {
			return err
		}
		if g.Revision != 0 {
			t.Fatalf("新建目标 revision 应为 0，得到 %d", g.Revision)
		}
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusActive, "", "lead", 0); err != nil {
			return err
		}
		stale = SetGoalStatusCASExtra(tx)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if stale == nil {
		t.Fatal("拿旧 revision 的第二次写入必须失败")
	}
	if !IsGoalRevisionMismatch(stale) {
		t.Fatalf("应是 revision_mismatch，得到 %v", stale)
	}
	var mm *GoalRevisionMismatch
	if !errors.As(stale, &mm) || mm.Actual != 1 || mm.Expected != 0 {
		t.Fatalf("冲突细节不对：%+v", mm)
	}
	if mm.Code() != "revision_mismatch" || mm.Details()["actual_revision"] != 1 {
		t.Fatalf("错误形状不对：%v %+v", mm.Code(), mm.Details())
	}

	// 用最新 revision 重试成功，且 revision 单调 +1。
	if err := WithTx(path, func(tx *sql.Tx) error {
		g, err := GetGoal(tx, "p1", "gl_pi001ax9")
		if err != nil {
			return err
		}
		updated, changed, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusVerifying, "", "lead", g.Revision)
		if err != nil {
			return err
		}
		if !changed || updated.Revision != g.Revision+1 {
			t.Fatalf("成功后 revision 应 +1：%+v", updated)
		}
		again, changed, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusVerifying, "", "lead", updated.Revision)
		if err != nil {
			return err
		}
		if changed || again.Revision != updated.Revision {
			t.Fatalf("同状态必须幂等且不推高 revision：%+v", again)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// SetGoalStatusCASExtra 是一个「用旧 revision 再写一次」的小工具：
// 在同一个事务里拿 revision 0 去改一个已经变成 1 的目标。
func SetGoalStatusCASExtra(tx *sql.Tx) error {
	_, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusPaused, goal.ReasonHumanPause, "lead", 0)
	return err
}

func TestGoalStatusRules(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "lead")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// draft → verifying 不是合法边。
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusVerifying, "", "lead", 0)
		if !errors.Is(err, goal.ErrGoalTransition) {
			t.Fatalf("draft→verifying 应报 ErrGoalTransition，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := WithTx(path, func(tx *sql.Tx) error {
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusActive, "", "lead", 0); err != nil {
			return err
		}
		// 暂停必须带合法原因。
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusPaused, "", "lead", 1); err == nil {
			t.Fatal("暂停缺原因必须拒绝")
		}
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusPaused, "because", "lead", 1); err == nil {
			t.Fatal("非法原因必须拒绝")
		}
		if _, changed, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusPaused, goal.ReasonBudgetExhaust, "lead", 1); err != nil || !changed {
			t.Fatalf("合法暂停应成功：changed=%v err=%v", changed, err)
		}
		// 放弃必须写 reason，落库后保留。
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusAbandoned, "", "lead", 2); err == nil {
			t.Fatal("放弃缺 reason 必须拒绝")
		}
		g, changed, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusAbandoned, "需求取消", "lead", 2)
		if err != nil || !changed {
			t.Fatalf("放弃应成功：changed=%v err=%v", changed, err)
		}
		if g.Status != goal.StatusAbandoned || g.AbandonReason != "需求取消" {
			t.Fatalf("放弃原因未落库：%+v", g)
		}
		// 终态之后不能再迁移。
		if _, _, err := SetGoalStatusCAS(tx, "p1", "gl_pi001ax9", goal.StatusActive, "", "lead", g.Revision); !errors.Is(err, goal.ErrGoalTransition) {
			t.Fatalf("终态后迁移应拒绝，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestGoalVersionAppendMonotonic(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)
	if err := WithTx(path, func(tx *sql.Tx) error {
		if _, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "lead"); err != nil {
			return err
		}
		// 版本号必须 max+1。
		if _, err := AppendGoalVersion(tx, testGoalVersion(3), 0, "lead"); !errors.Is(err, ErrGoalVersionConflict) {
			t.Fatalf("跳号追加应报 ErrGoalVersionConflict，得到 %v", err)
		}
		g, err := AppendGoalVersion(tx, testGoalVersion(2), 0, "lead")
		if err != nil {
			return err
		}
		if g.ActiveVersion != 2 || g.Revision != 1 {
			t.Fatalf("追加后指针／revision 不对：%+v", g)
		}
		// 拿旧 revision 再追加必须失败。
		if _, err := AppendGoalVersion(tx, testGoalVersion(3), 0, "lead"); !IsGoalRevisionMismatch(err) {
			t.Fatalf("旧 revision 追加应报冲突，得到 %v", err)
		}
		// 同一 goal_id 不可重复创建。
		if _, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "lead"); !errors.Is(err, ErrGoalExists) {
			t.Fatalf("重复创建应报 ErrGoalExists，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLinkIdempotentAndConflict(t *testing.T) {
	path := openTestDB(t)
	instances := seedGoalProject(t, path, "GL-001", "GL-002")
	key := goal.EnqueueKey("p1", "gl_pi001ax9", 1, "digest", "c01")

	if err := WithTx(path, func(tx *sql.Tx) error {
		link := &goal.TaskLink{
			ProjectID: "p1", GoalID: "gl_pi001ax9", PlanRev: 1, CardID: "c01",
			InstanceID: instances["GL-001"], DisplayID: "GL-001", VersionNo: 1,
			EnqueueKey: key, State: goal.LinkApplied, AppliedBy: "lead",
		}
		if _, created, err := RecordTaskLink(tx, link); err != nil || !created {
			t.Fatalf("首次映射应新建：created=%v err=%v", created, err)
		}
		// 完全相同的绑定重放：不新建、不报错。
		again, created, err := RecordTaskLink(tx, &goal.TaskLink{
			ProjectID: "p1", GoalID: "gl_pi001ax9", PlanRev: 1, CardID: "c01",
			InstanceID: instances["GL-001"], DisplayID: "GL-001", VersionNo: 1,
			EnqueueKey: key, State: goal.LinkApplied, AppliedBy: "lead",
		})
		if err != nil || created {
			t.Fatalf("重放应幂等：created=%v err=%v", created, err)
		}
		if again.EnqueueKey != key {
			t.Fatalf("幂等返回应指向原映射：%+v", again)
		}
		// 同一实例换卡：冲突，绝不静默覆盖。
		if _, _, err := RecordTaskLink(tx, &goal.TaskLink{
			ProjectID: "p1", GoalID: "gl_pi001ax9", PlanRev: 1, CardID: "c02",
			InstanceID: instances["GL-001"], DisplayID: "GL-001", VersionNo: 1,
			EnqueueKey: goal.EnqueueKey("p1", "gl_pi001ax9", 1, "digest", "c02"), State: goal.LinkApplied,
		}); !errors.Is(err, goal.ErrTaskLinkConflict) {
			t.Fatalf("同实例换卡应冲突，得到 %v", err)
		}
		// 同一幂等键换实例：冲突。
		if _, _, err := RecordTaskLink(tx, &goal.TaskLink{
			ProjectID: "p1", GoalID: "gl_pi001ax9", PlanRev: 1, CardID: "c01",
			InstanceID: instances["GL-002"], DisplayID: "GL-002", VersionNo: 1,
			EnqueueKey: key, State: goal.LinkApplied,
		}); !errors.Is(err, goal.ErrTaskLinkConflict) {
			t.Fatalf("同键换实例应冲突，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 漂移标记：只改状态、不删行，重复标记幂等。
	if err := WithTx(path, func(tx *sql.Tx) error {
		changed, err := MarkTaskLinkDrifted(tx, "p1", instances["GL-001"], "lead")
		if err != nil || !changed {
			t.Fatalf("首次漂移应变更：changed=%v err=%v", changed, err)
		}
		changed, err = MarkTaskLinkDrifted(tx, "p1", instances["GL-001"], "lead")
		if err != nil || changed {
			t.Fatalf("重复漂移应幂等：changed=%v err=%v", changed, err)
		}
		links, err := ListTaskLinks(tx, "p1", "gl_pi001ax9")
		if err != nil {
			return err
		}
		if len(links) != 1 || links[0].State != goal.LinkDrifted {
			t.Fatalf("漂移状态未落库：%+v", links)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueueLedgerIdempotency(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)
	key := goal.EnqueueKey("p1", "gl_pi001ax9", 1, "digest-a", "c01")

	if err := WithTx(path, func(tx *sql.Tx) error {
		rec := func(digest, state, reason string) *goal.EnqueueRecord {
			return &goal.EnqueueRecord{
				ProjectID: "p1", EnqueueKey: key, GoalID: "gl_pi001ax9",
				PlanRev: 1, CardID: "c01", PlanDigest: digest, State: state, Reason: reason,
			}
		}
		if _, replayed, err := RecordEnqueue(tx, rec("digest-a", goal.EnqueueAppended, "")); err != nil || replayed {
			t.Fatalf("首次登记不算重放：replayed=%v err=%v", replayed, err)
		}
		if _, replayed, err := RecordEnqueue(tx, rec("digest-a", goal.EnqueueAppended, "")); err != nil || !replayed {
			t.Fatalf("同状态重放应为 replayed：replayed=%v err=%v", replayed, err)
		}
		if _, replayed, err := RecordEnqueue(tx, rec("digest-a", goal.EnqueueApplied, "")); err != nil || replayed {
			t.Fatalf("appended→applied 是推进而非重放：replayed=%v err=%v", replayed, err)
		}
		if _, replayed, err := RecordEnqueue(tx, rec("digest-a", goal.EnqueueApplied, "")); err != nil || !replayed {
			t.Fatalf("applied 重放应为 replayed：replayed=%v err=%v", replayed, err)
		}
		// applied 是终态：不得再回到 appended/failed。
		if _, _, err := RecordEnqueue(tx, rec("digest-a", goal.EnqueueAppended, "")); !errors.Is(err, goal.ErrPlanTransition) {
			t.Fatalf("applied→appended 应拒绝，得到 %v", err)
		}
		// 同键异摘要：冲突。
		if _, _, err := RecordEnqueue(tx, rec("digest-b", goal.EnqueueApplied, "")); !errors.Is(err, goal.ErrEnqueueKeyReuse) {
			t.Fatalf("同键异摘要应报 ErrEnqueueKeyReuse，得到 %v", err)
		}
		got, err := GetEnqueueRecord(tx, "p1", key)
		if err != nil {
			return err
		}
		if got == nil || got.State != goal.EnqueueApplied {
			t.Fatalf("台账状态不对：%+v", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 失败重试：appended → failed 可以，failed → applied 直接拒绝。
	failKey := goal.EnqueueKey("p1", "gl_pi001ax9", 1, "digest-a", "c02")
	if err := WithTx(path, func(tx *sql.Tx) error {
		base := &goal.EnqueueRecord{
			ProjectID: "p1", EnqueueKey: failKey, GoalID: "gl_pi001ax9",
			PlanRev: 1, CardID: "c02", PlanDigest: "digest-a", State: goal.EnqueueAppended,
		}
		if _, _, err := RecordEnqueue(tx, base); err != nil {
			return err
		}
		base.State = goal.EnqueueFailed
		base.Reason = "write_failed"
		if _, replayed, err := RecordEnqueue(tx, base); err != nil || replayed {
			t.Fatalf("appended→failed 应推进：replayed=%v err=%v", replayed, err)
		}
		base.State = goal.EnqueueApplied
		if _, _, err := RecordEnqueue(tx, base); err == nil {
			t.Fatal("failed 不能直接改 applied")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanApprovalBindsDigest(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)

	if err := WithTx(path, func(tx *sql.Tx) error {
		rev, err := SavePlanRevision(tx, testPlan(1, ""), "lead")
		if err != nil {
			return err
		}
		if rev.PlanDigest == "" || rev.Status != goal.PlanProposed {
			t.Fatalf("落库应补摘要且保持 proposed：%+v", rev)
		}
		// 批准别人的摘要：拒绝。
		if _, _, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 1, "alice", goal.PolicyManual, strings.Repeat("0", 64), "lead"); !errors.Is(err, goal.ErrApprovalDigest) {
			t.Fatalf("错摘要应报 ErrApprovalDigest，得到 %v", err)
		}
		// 批准缺批准人：拒绝。
		if _, _, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 1, "", goal.PolicyManual, rev.PlanDigest, "lead"); err == nil {
			t.Fatal("缺 approver 必须拒绝")
		}
		// 正常批准。
		if _, changed, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 1, "alice", goal.PolicyManual, rev.PlanDigest, "lead"); err != nil || !changed {
			t.Fatalf("批准应成功：changed=%v err=%v", changed, err)
		}
		// 同摘要同批准人重复批准：幂等。
		if _, changed, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 1, "alice", goal.PolicyManual, rev.PlanDigest, "lead"); err != nil || changed {
			t.Fatalf("重复批准应幂等：changed=%v err=%v", changed, err)
		}
		// 换批准人：拒绝，不能把审批痕迹改掉。
		if _, _, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 1, "bob", goal.PolicyManual, rev.PlanDigest, "lead"); !errors.Is(err, goal.ErrPlanTransition) {
			t.Fatalf("换批准人应拒绝，得到 %v", err)
		}
		// 已批准修订不能失效后再入队。
		if _, changed, err := InvalidatePlanRevision(tx, "p1", "gl_pi001ax9", 1, "baseline_drift", "lead"); err != nil || !changed {
			t.Fatalf("失效应成功：changed=%v err=%v", changed, err)
		}
		if _, _, err := MarkPlanRevisionApplied(tx, "p1", "gl_pi001ax9", 1, "lead"); !errors.Is(err, goal.ErrPlanTransition) {
			t.Fatalf("失效修订不能入队，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 已入队的修订不得失效；重复入队幂等。
	if err := WithTx(path, func(tx *sql.Tx) error {
		rev, err := SavePlanRevision(tx, testPlan(2, ""), "lead")
		if err != nil {
			return err
		}
		if _, _, err := ApprovePlanRevision(tx, "p1", "gl_pi001ax9", 2, "alice", goal.PolicyAutoEnqueue, rev.PlanDigest, "lead"); err != nil {
			return err
		}
		if _, changed, err := MarkPlanRevisionApplied(tx, "p1", "gl_pi001ax9", 2, "lead"); err != nil || !changed {
			t.Fatalf("首次入队应变更：changed=%v err=%v", changed, err)
		}
		if _, changed, err := MarkPlanRevisionApplied(tx, "p1", "gl_pi001ax9", 2, "lead"); err != nil || changed {
			t.Fatalf("重复入队应幂等：changed=%v err=%v", changed, err)
		}
		if _, _, err := InvalidatePlanRevision(tx, "p1", "gl_pi001ax9", 2, "baseline_drift", "lead"); !errors.Is(err, goal.ErrPlanTransition) {
			t.Fatalf("已入队修订不得失效，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPlanRevisionMonotonicAndValidation(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)

	if err := WithTx(path, func(tx *sql.Tx) error {
		// 跳号：先存 rev2。
		if _, err := SavePlanRevision(tx, testPlan(2, ""), "lead"); !errors.Is(err, ErrGoalPlanConflict) {
			t.Fatalf("跳号应报 ErrGoalPlanConflict，得到 %v", err)
		}
		// 非法卡（占位验收）不得落库。
		bad := testPlan(1, "")
		bad.Cards[0].Acceptance = []string{"TODO: 补验收"}
		_ = bad.EnsureDigest()
		if _, err := SavePlanRevision(tx, bad, "lead"); err == nil {
			t.Fatal("占位验收不应落库")
		}
		if _, err := SavePlanRevision(tx, testPlan(1, ""), "lead"); err != nil {
			return err
		}
		// 同一修订号不得重复保存（修订不可变）。
		if _, err := SavePlanRevision(tx, testPlan(1, ""), "lead"); !errors.Is(err, ErrGoalPlanConflict) {
			t.Fatalf("重复修订号应报 ErrGoalPlanConflict，得到 %v", err)
		}
		// 新修订只能以 proposed 落库。
		applied := testPlan(1, "")
		applied.Status = goal.PlanApplied
		if _, err := SavePlanRevision(tx, applied, "lead"); !errors.Is(err, goal.ErrPlanTransition) {
			t.Fatalf("非 proposed 落库应拒绝，得到 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// TestGoalRevisionCASUnderConcurrentWriters 用两个真实连接并发写同一目标：
// 只有一个能落盘，另一个必须拿到 revision_mismatch，而不是后写覆盖先写。
func TestGoalRevisionCASUnderConcurrentWriters(t *testing.T) {
	path := openTestDB(t)
	seedGoalProject(t, path)
	if err := WithTx(path, func(tx *sql.Tx) error {
		_, _, err := CreateGoal(tx, testGoal(), testGoalVersion(1), "lead")
		return err
	}); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	targets := []string{"IT-A", "IT-B"}
	for i := 0; i < 2; i++ {
		go func(idx int) {
			<-start
			errs <- WithTx(path, func(tx *sql.Tx) error {
				g, err := GetGoal(tx, "p1", "gl_pi001ax9")
				if err != nil {
					return err
				}
				g.IterationID = targets[idx]
				_, err = UpdateGoalCAS(tx, g, 0, "writer-"+targets[idx])
				return err
			})
		}(i)
	}
	close(start)

	var ok, mismatched int
	for i := 0; i < 2; i++ {
		err := <-errs
		switch {
		case err == nil:
			ok++
		case IsGoalRevisionMismatch(err):
			mismatched++
		default:
			t.Fatalf("并发写只允许成功或 revision_mismatch，得到 %v", err)
		}
	}
	if ok != 1 || mismatched != 1 {
		t.Fatalf("并发结果应为 1 成功 1 冲突，得到 ok=%d mismatch=%d", ok, mismatched)
	}
	if err := WithTx(path, func(tx *sql.Tx) error {
		g, err := GetGoal(tx, "p1", "gl_pi001ax9")
		if err != nil {
			return err
		}
		if g.Revision != 1 {
			t.Fatalf("并发写后 revision 应为 1，得到 %d", g.Revision)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
