package analytics

import (
	"database/sql"
	"math"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/projection"
)

const (
	healthProject = "p1"
	healthStart   = "2026-10-24T00:00:00Z"
	healthEnd     = "2026-10-24T01:00:00Z"
	healthBefore  = "2026-10-23T23:59:59Z"
)

func healthQuery(projectID, start, end string) Query {
	s, _ := time.Parse(time.RFC3339, start)
	e, _ := time.Parse(time.RFC3339, end)
	return Query{ProjectID: projectID, Window: projection.Window{Start: s, End: e}}
}

// seedHealthJobs 写入窗口内的作业：5 unblock、1 task_split、2 dispatch、2 ops。
//
// 关键口径样本：
//   - j6 从未被领取（attempts=0）→ 不计入重试/回收分母；
//   - j12 被回收（result.reaped_from=running）→ 回收率 1/9；
//   - j2 领取两次 → 重试率 1/9；
//   - j3 无 reason 也无 decision.action → 记 unspecified；
//   - j4 cancelled → 不进成功率分母。
func seedHealthJobs(t *testing.T, f *fixture, projectID string, prefix string) {
	t.Helper()
	jobs := []db.SystemJob{
		{JobID: prefix + "j1", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobDone,
			Attempts: 1, CreatedAt: "2026-10-24T00:05:00Z", FinishedAt: "2026-10-24T00:20:00Z"},
		{JobID: prefix + "j2", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobFailed,
			Attempts: 2, CreatedAt: "2026-10-24T00:10:00Z", FinishedAt: "2026-10-24T00:30:00Z",
			Result: map[string]any{"reason": "boom"}},
		{JobID: prefix + "j3", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobEscalated,
			Attempts: 1, CreatedAt: "2026-10-24T00:15:00Z", FinishedAt: "2026-10-24T00:40:00Z"},
		{JobID: prefix + "j4", ProjectID: projectID, Kind: db.SystemKindTaskSplit, Status: db.SystemJobCancelled,
			Attempts: 1, CreatedAt: "2026-10-24T00:20:00Z", FinishedAt: "2026-10-24T00:35:00Z"},
		{JobID: prefix + "j6", ProjectID: projectID, Kind: db.SystemKindOpsRun, Status: db.SystemJobQueued,
			CreatedAt: "2026-10-24T00:25:00Z"},
		{JobID: prefix + "j12", ProjectID: projectID, Kind: db.SystemKindOpsRun, Status: db.SystemJobQueued,
			Attempts: 1, CreatedAt: "2026-10-24T00:55:00Z",
			Result: map[string]any{"reaped_from": "running"}},
		{JobID: prefix + "j14", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobDone,
			Attempts: 1, CreatedAt: "2026-10-24T00:45:00Z", FinishedAt: "2026-10-24T00:50:00Z"},
		{JobID: prefix + "j15", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobDone,
			Attempts: 1, CreatedAt: "2026-10-24T00:50:00Z", FinishedAt: "2026-10-24T00:58:00Z"},
		// 窗口外：起止边界各一条，用于验证半开区间。
		{JobID: prefix + "early", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobDone,
			Attempts: 1, CreatedAt: healthBefore, FinishedAt: healthBefore},
		{JobID: prefix + "late", ProjectID: projectID, Kind: db.SystemKindUnblock, Status: db.SystemJobDone,
			Attempts: 1, CreatedAt: healthEnd, FinishedAt: healthEnd},
	}
	// dispatch_ready 作业带 run_id，窗口边界内各一条，用于排队时延。
	jobs = append(jobs,
		db.SystemJob{JobID: prefix + "j5", ProjectID: projectID, Kind: db.SystemKindDispatchReady,
			Status: db.SystemJobDone, Attempts: 1, RunID: projectID + "-r1",
			CreatedAt: "2026-10-24T00:00:00Z", FinishedAt: "2026-10-24T00:10:00Z"},
		db.SystemJob{JobID: prefix + "j13", ProjectID: projectID, Kind: db.SystemKindDispatchReady,
			Status: db.SystemJobDone, Attempts: 1, RunID: projectID + "-r5",
			CreatedAt: "2026-10-24T00:40:00Z", FinishedAt: "2026-10-24T00:50:00Z"},
	)
	f.write(t, func(tx *sql.Tx) error {
		for i := range jobs {
			if _, err := db.InsertSystemJob(tx, &jobs[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

// seedHealthRuns 写入窗口内 5 个 Run；created→started 依次为 15/20/18/30/20 秒。
func seedHealthRuns(t *testing.T, f *fixture, projectID string) {
	t.Helper()
	runs := [][3]string{
		{"2026-10-24T00:00:30Z", "2026-10-24T00:00:45Z", "2026-10-24T00:05:00Z"},
		{"2026-10-24T00:31:00Z", "2026-10-24T00:31:20Z", "2026-10-24T00:44:00Z"},
		{"2026-10-24T00:32:00Z", "2026-10-24T00:32:18Z", "2026-10-24T00:42:00Z"},
		{"2026-10-24T00:33:00Z", "2026-10-24T00:33:30Z", "2026-10-24T00:48:00Z"},
		{"2026-10-24T00:40:20Z", "2026-10-24T00:40:40Z", "2026-10-24T00:41:00Z"},
	}
	f.write(t, func(tx *sql.Tx) error {
		for i, r := range runs {
			runID := []string{"r1", "r2", "r3", "r4", "r5"}[i]
			// 每个工程的行都是独立实例，避免 UNIQUE(instance_id, generation) 冲突。
			instance := projectID + "-" + runID
			if _, err := tx.Exec(`INSERT INTO runs(
				run_id, project_id, instance_id, version_no, generation, phase, reconciliation_state,
				created_at, updated_at, started_at, finished_at)
				VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
				projectID+"-"+runID, projectID, instance, 1, 1,
				model.PhaseCompleted, "confirmed", r[0], r[0], r[1], r[2]); err != nil {
				return err
			}
		}
		return nil
	})
}

func seedHealthAgents(t *testing.T, f *fixture, projectID, prefix string) {
	t.Helper()
	agents := []db.Agent{
		{ProjectID: projectID, ID: prefix + "a1", Role: "worker", Status: "idle",
			RegisteredAt: "2026-10-24T00:10:00Z", LastSeen: "2026-10-24T00:55:00Z"},
		{ProjectID: projectID, ID: prefix + "a2", Role: "worker", Status: "idle",
			RegisteredAt: "2026-10-24T00:10:00Z", LastSeen: "2026-10-24T00:40:00Z"},
		{ProjectID: projectID, ID: prefix + "a3", Role: "worker", Status: "idle",
			RegisteredAt: "2026-10-24T00:10:00Z", LastSeen: "2026-10-24T00:59:00Z"},
	}
	f.write(t, func(tx *sql.Tx) error {
		for i := range agents {
			if err := db.UpsertAgent(tx, &agents[i]); err != nil {
				return err
			}
		}
		return nil
	})
}

func closeTo(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s 为 nil，期望 %v", label, want)
	}
	if math.Abs(*got-want) > 1e-9 {
		t.Fatalf("%s = %v，期望 %v", label, *got, want)
	}
}

func expectUnknown(t *testing.T, label string, m RatioMetric) {
	t.Helper()
	if m.Ratio != nil {
		t.Fatalf("%s ratio = %v，无数据时必须为 nil", label, *m.Ratio)
	}
	if m.Quality != model.QualityUnknown {
		t.Fatalf("%s quality = %q，期望 unknown", label, m.Quality)
	}
}

// TestSystemHealthFixedFixture 用固定夹具逐项对表：10 类作业、5 个 Run、3 个 Agent。
func TestSystemHealthFixedFixture(t *testing.T) {
	f := newFixture(t)
	seedHealthJobs(t, f, healthProject, "")
	seedHealthRuns(t, f, healthProject)
	seedHealthAgents(t, f, healthProject, "")

	svc := &Service{}
	q := healthQuery(healthProject, healthStart, healthEnd)
	var report *SystemHealthReport
	f.read(t, func(tx *sql.Tx) error {
		var err error
		report, err = svc.SystemHealth(tx, q)
		return err
	})

	if report.JobRows != 10 || report.RunRows != 5 || report.AgentRows != 3 {
		t.Fatalf("行数 = %d/%d/%d，期望 10/5/3",
			report.JobRows, report.RunRows, report.AgentRows)
	}
	if report.Scope.Start.Format(time.RFC3339) != healthStart ||
		report.Scope.End.Format(time.RFC3339) != healthEnd {
		t.Fatalf("窗口回显 = %v..%v", report.Scope.Start, report.Scope.End)
	}

	jobs := report.Jobs
	if jobs.Total == nil || *jobs.Total != 10 {
		t.Fatalf("total = %v，期望 10", jobs.Total)
	}
	wantKind := map[string]int64{
		db.SystemKindUnblock: 5, db.SystemKindTaskSplit: 1,
		db.SystemKindDispatchReady: 2, db.SystemKindOpsRun: 2,
	}
	for kind, want := range wantKind {
		if jobs.ByKind[kind] != want {
			t.Fatalf("by_kind[%s] = %d，期望 %d", kind, jobs.ByKind[kind], want)
		}
	}
	if jobs.Backlog != 2 {
		t.Fatalf("backlog = %d，期望 2", jobs.Backlog)
	}
	// 最久积压 00:25，截至窗口 End 01:00 即 2100 秒。
	closeTo(t, "backlog_oldest_seconds", jobs.BacklogOldestSecond, 2100)

	// 成功率：done 5 / (done 5 + failed 1 + escalated 1)，cancelled 不计。
	closeTo(t, "success_rate", jobs.SuccessRate.Ratio, 5.0/7.0)
	if jobs.SuccessRate.Denominator != 7 {
		t.Fatalf("success_rate 分母 = %d，期望 7", jobs.SuccessRate.Denominator)
	}
	closeTo(t, "success_rate_strict", jobs.SuccessRateStrict.Ratio, 5.0/6.0)

	// 重试与回收分母都是「被领取过」的 9 条（j6 从未领取）。
	closeTo(t, "retry_rate", jobs.RetryRate.Ratio, 1.0/9.0)
	if jobs.RetryRate.Denominator != 9 {
		t.Fatalf("retry 分母 = %d，期望 9", jobs.RetryRate.Denominator)
	}
	closeTo(t, "lease_reclaim_rate", jobs.LeaseReclaimRate.Ratio, 1.0/9.0)

	if jobs.FailureReasons["boom"] != 1 || jobs.FailureReasons["reconcile_failed"] != 0 {
		t.Fatalf("failure_reasons = %v，期望 boom=1", jobs.FailureReasons)
	}
	if jobs.UnspecifiedFailures != 1 {
		t.Fatalf("unspecified = %d，期望 1", jobs.UnspecifiedFailures)
	}

	// 派发排队：r1 30 秒、r5 20 秒，仅 2 个样本 → 只给均值并标 unknown。
	dq := report.Dispatch.QueueWait
	if dq.Sample != 2 || dq.P50Seconds != nil || dq.P95Seconds != nil {
		t.Fatalf("queue_wait sample/p50/p95 = %d/%v/%v", dq.Sample, dq.P50Seconds, dq.P95Seconds)
	}
	if dq.Quality != model.QualityUnknown {
		t.Fatalf("queue_wait quality = %q，期望 unknown", dq.Quality)
	}
	closeTo(t, "queue_wait_mean", dq.MeanSeconds, 25)

	// 领取到启动：15/18/20/20/30 → p50=20，p95=30，均值 20.6。
	cs := report.Dispatch.ClaimToStart
	if cs.Sample != 5 {
		t.Fatalf("claim_to_start sample = %d，期望 5", cs.Sample)
	}
	closeTo(t, "claim_to_start_p50", cs.P50Seconds, 20)
	closeTo(t, "claim_to_start_p95", cs.P95Seconds, 30)
	closeTo(t, "claim_to_start_mean", cs.MeanSeconds, 20.6)

	// 心跳：00:55 与 00:59 新鲜，00:40 过期 → 2/3。
	closeTo(t, "heartbeat_freshness", report.Agents.HeartbeatFreshness.Ratio, 2.0/3.0)
	if report.Agents.HeartbeatFreshness.Denominator != 3 {
		t.Fatalf("heartbeat 分母 = %d，期望 3", report.Agents.HeartbeatFreshness.Denominator)
	}
	expectUnknown(t, "node_success_rate", report.Agents.NodeSuccessRate)

	// run_reconcile 尚未放开 CHECK（SH-302），本夹具无此 kind：必须报 unknown，不填 0。
	expectUnknown(t, "reconcile_rate", report.Reconcile.Rate)
	if report.Reconcile.MTTR.Sample != 0 || report.Reconcile.MTTR.MeanSeconds != nil {
		t.Fatalf("reconcile mttr = %+v，期望空样本", report.Reconcile.MTTR)
	}
	if report.Reconcile.Release != 0 || report.Reconcile.Keep != 0 {
		t.Fatalf("reconcile 分布 = %+v，期望全 0", report.Reconcile)
	}
}

// TestSystemHealthEmptyWindow 验证无数据时 unknown 而非 0。
func TestSystemHealthEmptyWindow(t *testing.T) {
	f := newFixture(t)
	svc := &Service{}
	q := healthQuery("p-empty", healthStart, healthEnd)
	var report *SystemHealthReport
	f.read(t, func(tx *sql.Tx) error {
		var err error
		report, err = svc.SystemHealth(tx, q)
		return err
	})

	if report.Jobs.Total != nil {
		t.Fatalf("无作业时 total 必须为 nil，实际 %v", *report.Jobs.Total)
	}
	expectUnknown(t, "success_rate", report.Jobs.SuccessRate)
	expectUnknown(t, "success_rate_strict", report.Jobs.SuccessRateStrict)
	expectUnknown(t, "retry_rate", report.Jobs.RetryRate)
	expectUnknown(t, "lease_reclaim_rate", report.Jobs.LeaseReclaimRate)
	expectUnknown(t, "heartbeat_freshness", report.Agents.HeartbeatFreshness)
	if report.Jobs.BacklogOldestSecond != nil {
		t.Fatalf("无 queued 时 backlog_oldest 必须为 nil")
	}
	if report.Dispatch.QueueWait.MeanSeconds != nil || report.Dispatch.ClaimToStart.MeanSeconds != nil {
		t.Fatalf("无 Run 时耗时分位必须为 nil")
	}
	if len(report.Jobs.FailureReasons) != 0 || report.Jobs.UnspecifiedFailures != 0 {
		t.Fatalf("无终态作业时失败原因必须为空")
	}
}

// TestSystemHealthProjectFilter 验证跨工程隔离：p2 只看到自己的作业/Run/Agent。
func TestSystemHealthProjectFilter(t *testing.T) {
	f := newFixture(t)
	seedHealthJobs(t, f, healthProject, "")
	seedHealthRuns(t, f, healthProject)
	seedHealthAgents(t, f, healthProject, "")

	other := []db.SystemJob{
		{JobID: "p2-j1", ProjectID: "p2", Kind: db.SystemKindDispatchReady, Status: db.SystemJobDone,
			Attempts: 1, RunID: "p2-r1", CreatedAt: "2026-10-24T00:10:00Z",
			FinishedAt: "2026-10-24T00:20:00Z"},
	}
	f.write(t, func(tx *sql.Tx) error {
		for i := range other {
			if _, err := db.InsertSystemJob(tx, &other[i]); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT INTO runs(
			run_id, project_id, instance_id, version_no, generation, phase, reconciliation_state,
			created_at, updated_at, started_at, finished_at)
			VALUES ('p2-r1','p2','p2-inst',1,1,?,?, '2026-10-24T00:12:00Z','2026-10-24T00:12:00Z',
			'2026-10-24T00:13:00Z','2026-10-24T00:18:00Z')`,
			model.PhaseCompleted, "confirmed"); err != nil {
			return err
		}
		for _, a := range []db.Agent{
			{ProjectID: "p2", ID: "p2-a1", Role: "worker", Status: "idle",
				RegisteredAt: "2026-10-24T00:10:00Z", LastSeen: "2026-10-24T00:57:00Z"},
			{ProjectID: "p2", ID: "p2-a2", Role: "worker", Status: "idle",
				RegisteredAt: "2026-10-24T00:10:00Z", LastSeen: "2026-10-24T00:20:00Z"},
		} {
			agent := a
			if err := db.UpsertAgent(tx, &agent); err != nil {
				return err
			}
		}
		return nil
	})

	svc := &Service{}
	q := healthQuery("p2", healthStart, healthEnd)
	var report *SystemHealthReport
	f.read(t, func(tx *sql.Tx) error {
		var err error
		report, err = svc.SystemHealth(tx, q)
		return err
	})

	if report.JobRows != 1 || report.RunRows != 1 || report.AgentRows != 2 {
		t.Fatalf("p2 行数 = %d/%d/%d，期望 1/1/2",
			report.JobRows, report.RunRows, report.AgentRows)
	}
	if report.Jobs.Total == nil || *report.Jobs.Total != 1 {
		t.Fatalf("p2 total = %v，期望 1", report.Jobs.Total)
	}
	closeTo(t, "p2 success", report.Jobs.SuccessRate.Ratio, 1)
	// p2 的 dispatch 排队：00:10 → 00:12 即 120 秒；单样本 → unknown。
	closeTo(t, "p2 queue_wait_mean", report.Dispatch.QueueWait.MeanSeconds, 120)
	// p2 只有一个 Run：00:12 → 00:13 即 60 秒。
	closeTo(t, "p2 claim_to_start_mean", report.Dispatch.ClaimToStart.MeanSeconds, 60)
	closeTo(t, "p2 heartbeat", report.Agents.HeartbeatFreshness.Ratio, 0.5)
}
