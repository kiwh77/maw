package systemagent

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/recovery"
)

// fakeReconcilePort 记录调用并返回预置结论。
type fakeReconcilePort struct {
	out  ReconcileOutcome
	err  error
	seen int
	req  ReconcileRequest
}

func (p *fakeReconcilePort) Reconcile(_ context.Context, req ReconcileRequest) (ReconcileOutcome, error) {
	p.seen++
	p.req = req
	return p.out, p.err
}

// newReconcileTestJob 建库并插入一条带 unknown 预分类决策的 run_reconcile 作业。
// 预置 action=escalate_human 正是为了证明编排层先走核实、不被预分类截断。
func newReconcileTestJob(t *testing.T) (*db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.OpenStore(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	const pid = "0123456789abcdef"
	if err := store.Write(func(tx *sql.Tx) error {
		if err := db.UpsertProject(tx, &db.Project{
			ID: pid, Name: "demo", Path: dir, MainBranch: "main",
			CreatedAt: db.UTCNow(), LastSeen: db.UTCNow(),
		}); err != nil {
			return err
		}
		_, err := db.InsertSystemJob(tx, &db.SystemJob{
			JobID: "sjob_reconcile_1", ProjectID: pid, Kind: SystemKindRunReconcile,
			Status: db.SystemJobQueued, TaskID: "T-1", InstanceID: "ti_1",
			Reason: "Run 处于 unknown 且未核实",
			Decision: map[string]any{
				"run_id":          "run_1",
				"signal_key":      "run_unverified:" + pid + ":run_1",
				"signal_kind":     string(SignalRunUnverified),
				"reconcile_state": "unknown",
				"action":          string(ActionEscalateHuman),
			},
			MaxAttempts: 3,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return store, pid
}

func readReconcileJob(t *testing.T, store *db.Store, jobID string) *db.SystemJob {
	t.Helper()
	var job *db.SystemJob
	if err := store.Read(func(tx *sql.Tx) error {
		j, err := db.GetSystemJob(tx, jobID)
		job = j
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if job == nil {
		t.Fatal("作业不见了")
	}
	return job
}

// TestReconcileJobUnwired 覆盖验收 3：端口为 nil 时终态 Escalated 且 mode=unwired。
func TestReconcileJobUnwired(t *testing.T) {
	store, _ := newReconcileTestJob(t)
	orch := &Orchestrator{Store: store, Config: config.SystemAgent{MaxAttempts: 3}, WorkerID: "test"}
	if err := orch.RunJobID(context.Background(), "sjob_reconcile_1"); err != nil {
		t.Fatalf("run job: %v", err)
	}
	job := readReconcileJob(t, store, "sjob_reconcile_1")
	if job.Status != db.SystemJobEscalated {
		t.Fatalf("status=%s want escalated", job.Status)
	}
	if job.Result["mode"] != "unwired" {
		t.Fatalf("mode=%v want unwired", job.Result["mode"])
	}
}

// TestReconcileJobKeepBeatsPreclassified 覆盖验收 4 上半：unknown 现场即便信号预分类为
// escalate_human，也交由端口核实；keep 结论作业 done、不写 resources_released。
func TestReconcileJobKeepBeatsPreclassified(t *testing.T) {
	store, pid := newReconcileTestJob(t)
	port := &fakeReconcilePort{out: ReconcileOutcome{
		Verdict: ReconcileKeep,
		Evidence: recovery.Evidence{
			State:   recovery.StateUnknown,
			Journal: journal.Status{PendingRecords: 2},
			Reason:  "进程还在写工作目录，无法判定结束",
		},
	}}
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	orch := &Orchestrator{
		Store: store, Config: config.SystemAgent{MaxAttempts: 3}, WorkerID: "test",
		Reconcile: port, Clock: func() time.Time { return now },
	}
	if err := orch.RunJobID(context.Background(), "sjob_reconcile_1"); err != nil {
		t.Fatalf("run job: %v", err)
	}
	if port.seen != 1 {
		t.Fatalf("预分类 escalate 不应绕过核实，port seen=%d", port.seen)
	}
	if port.req.RunID != "run_1" || port.req.ProjectID != pid || port.req.JobID != "sjob_reconcile_1" {
		t.Fatalf("请求字段不对: %+v", port.req)
	}

	job := readReconcileJob(t, store, "sjob_reconcile_1")
	if job.Status != db.SystemJobDone {
		t.Fatalf("keep 应为 done，status=%s", job.Status)
	}
	if job.Result["mode"] != "keep" {
		t.Fatalf("mode=%v want keep", job.Result["mode"])
	}
	if job.Result["reconcile_state"] != "unknown" {
		t.Fatalf("reconcile_state=%v", job.Result["reconcile_state"])
	}
	if job.Result["evidence_reason"] != "进程还在写工作目录，无法判定结束" {
		t.Fatalf("evidence_reason=%v", job.Result["evidence_reason"])
	}
	if job.Result["journal_pending"] != float64(2) {
		t.Fatalf("journal_pending=%v", job.Result["journal_pending"])
	}
	if job.Result["rechecked_at"] != now.Format(time.RFC3339) {
		t.Fatalf("rechecked_at=%v", job.Result["rechecked_at"])
	}
	if _, ok := job.Result["resources_released"]; ok {
		t.Fatalf("keep 不应记录释放资源: %v", job.Result)
	}
}

// TestReconcileJobReleaseRecordsResources 覆盖验收 4 下半：release 结论作业 done，
// 且只有 release 才写 resources_released。
func TestReconcileJobReleaseRecordsResources(t *testing.T) {
	store, _ := newReconcileTestJob(t)
	port := &fakeReconcilePort{out: ReconcileOutcome{
		Verdict: ReconcileRelease,
		Evidence: recovery.Evidence{
			State:  recovery.StateFinished,
			Reason: "进程已退出，且进程组已确认清空",
		},
		ResourcesReleased: []string{"lease", "node_slot"},
	}}
	orch := &Orchestrator{Store: store, Config: config.SystemAgent{MaxAttempts: 3}, WorkerID: "test", Reconcile: port}
	if err := orch.RunJobID(context.Background(), "sjob_reconcile_1"); err != nil {
		t.Fatalf("run job: %v", err)
	}
	job := readReconcileJob(t, store, "sjob_reconcile_1")
	if job.Status != db.SystemJobDone || job.Result["mode"] != "release" {
		t.Fatalf("status=%s mode=%v", job.Status, job.Result["mode"])
	}
	got, ok := job.Result["resources_released"].([]any)
	if !ok || len(got) != 2 || got[0] != "lease" || got[1] != "node_slot" {
		t.Fatalf("resources_released=%v", job.Result["resources_released"])
	}
}

// TestReconcileJobEscalate 覆盖验收 4：escalate 结论终态 Escalated。
func TestReconcileJobEscalate(t *testing.T) {
	store, _ := newReconcileTestJob(t)
	port := &fakeReconcilePort{out: ReconcileOutcome{
		Verdict:  ReconcileEscalate,
		Evidence: recovery.Evidence{State: recovery.StateRunning, Reason: "占用者身份不明"},
	}}
	orch := &Orchestrator{Store: store, Config: config.SystemAgent{MaxAttempts: 3}, WorkerID: "test", Reconcile: port}
	if err := orch.RunJobID(context.Background(), "sjob_reconcile_1"); err != nil {
		t.Fatalf("run job: %v", err)
	}
	job := readReconcileJob(t, store, "sjob_reconcile_1")
	if job.Status != db.SystemJobEscalated || job.Result["mode"] != "escalate" {
		t.Fatalf("status=%s mode=%v", job.Status, job.Result["mode"])
	}
	if _, ok := job.Result["resources_released"]; ok {
		t.Fatalf("escalate 不应记录释放资源: %v", job.Result)
	}
}

// TestReconcileJobUnknownVerdictRequeues 端口给出封闭集合外的结论时按失败处理，
// 由 Step/RunJobID 的既有重试路径回收，而不是猜一个动作落库。
func TestReconcileJobUnknownVerdictRequeues(t *testing.T) {
	store, _ := newReconcileTestJob(t)
	port := &fakeReconcilePort{out: ReconcileOutcome{Verdict: "oops"}}
	orch := &Orchestrator{Store: store, Config: config.SystemAgent{MaxAttempts: 3}, WorkerID: "test", Reconcile: port}
	if err := orch.RunJobID(context.Background(), "sjob_reconcile_1"); err == nil {
		t.Fatal("未知结论应返回错误")
	}
	job := readReconcileJob(t, store, "sjob_reconcile_1")
	if job.Status != db.SystemJobQueued {
		t.Fatalf("未到 max_attempts 应回 queued，status=%s", job.Status)
	}
	if job.Result["error"] == nil {
		t.Fatalf("重试结果应带原因: %v", job.Result)
	}
}
