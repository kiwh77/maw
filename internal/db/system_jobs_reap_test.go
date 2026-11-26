package db

import (
	"database/sql"
	"testing"
	"time"
)

func seedSystemJob(t *testing.T, path string, j *SystemJob) {
	t.Helper()
	err := WithTx(path, func(tx *sql.Tx) error {
		_, e := InsertSystemJob(tx, j)
		return e
	})
	if err != nil {
		t.Fatalf("seed %s: %v", j.JobID, err)
	}
}

func readSystemJob(t *testing.T, path, id string) *SystemJob {
	t.Helper()
	var job *SystemJob
	err := WithTx(path, func(tx *sql.Tx) error {
		var e error
		job, e = GetSystemJob(tx, id)
		return e
	})
	if err != nil || job == nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return job
}

func reap(t *testing.T, path string, now time.Time, lease time.Duration) int {
	t.Helper()
	var reaped int
	err := WithTx(path, func(tx *sql.Tx) error {
		var e error
		reaped, e = ReapStaleSystemJobs(tx, now, lease)
		return e
	})
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	return reaped
}

// TestReapStaleSystemJobsRequeuesOrFails 覆盖 SH-201 第一、二条验收：
// 超租约 running 未达 max_attempts 回 queued，已达 max_attempts 记 failed；
// 两者都要留下 reaped_from=running，且清空锁。
func TestReapStaleSystemJobsRequeuesOrFails(t *testing.T) {
	path := openTestDB(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)
	lease := 10 * time.Minute
	staleAt := now.Add(-11 * time.Minute).Format(time.RFC3339)

	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_retry", Kind: SystemKindUnblock, Status: SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "dead-worker", LockedAt: staleAt,
		CreatedAt: staleAt, UpdatedAt: staleAt,
	})
	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_exhausted", Kind: SystemKindUnblock, Status: SystemJobRunning,
		Attempts: 3, MaxAttempts: 3, LockedBy: "dead-worker", LockedAt: staleAt,
		CreatedAt: staleAt, UpdatedAt: staleAt,
	})
	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_fresh", Kind: SystemKindUnblock, Status: SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "live-worker",
		LockedAt:  now.Add(-5 * time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
	})

	if got := reap(t, path, now, lease); got != 2 {
		t.Fatalf("reaped=%d，期望只回收 2 条超租约作业", got)
	}

	retry := readSystemJob(t, path, "sjob_retry")
	if retry.Status != SystemJobQueued {
		t.Fatalf("未达上限应回 queued，实际 %s", retry.Status)
	}
	if retry.Attempts != 1 {
		t.Fatalf("回收不应额外递增 attempts，实际 %d", retry.Attempts)
	}
	if retry.LockedBy != "" || retry.LockedAt != "" {
		t.Fatalf("回收后应清空锁：by=%q at=%q", retry.LockedBy, retry.LockedAt)
	}
	if retry.FinishedAt != "" {
		t.Fatalf("回 queued 不应写 finished_at，实际 %q", retry.FinishedAt)
	}
	if retry.Result["reaped_from"] != SystemJobRunning {
		t.Fatalf("result.reaped_from=%v，期望 running", retry.Result["reaped_from"])
	}
	if retry.Result["reaped_at"] != now.Format(time.RFC3339) {
		t.Fatalf("result.reaped_at=%v，期望回收时刻", retry.Result["reaped_at"])
	}
	if retry.UpdatedAt != now.Format(time.RFC3339) {
		t.Fatalf("updated_at=%q，期望回收时刻（作为退避基准）", retry.UpdatedAt)
	}

	exhausted := readSystemJob(t, path, "sjob_exhausted")
	if exhausted.Status != SystemJobFailed {
		t.Fatalf("达上限应记 failed，实际 %s", exhausted.Status)
	}
	if exhausted.Result["reaped_from"] != SystemJobRunning {
		t.Fatalf("耗尽回收也要留 reaped_from，实际 %v", exhausted.Result["reaped_from"])
	}
	if exhausted.FinishedAt != now.Format(time.RFC3339) {
		t.Fatalf("failed 必须写 finished_at，实际 %q", exhausted.FinishedAt)
	}

	fresh := readSystemJob(t, path, "sjob_fresh")
	if fresh.Status != SystemJobRunning || fresh.LockedBy != "live-worker" {
		t.Fatalf("未超租约的 running 不得被动：status=%s by=%q", fresh.Status, fresh.LockedBy)
	}
}

// TestReapStaleSystemJobsDefaultLease 覆盖「默认 10 分钟，可注入」：
// 传 0 走默认值，9 分钟前仍在租约内，11 分钟前超时。
func TestReapStaleSystemJobsDefaultLease(t *testing.T) {
	path := openTestDB(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_default_inside", Kind: SystemKindUnblock, Status: SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "w",
		LockedAt:  now.Add(-9 * time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-9 * time.Minute).Format(time.RFC3339),
	})
	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_default_expired", Kind: SystemKindUnblock, Status: SystemJobRunning,
		Attempts: 1, MaxAttempts: 3, LockedBy: "w",
		LockedAt:  now.Add(-11 * time.Minute).Format(time.RFC3339),
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-11 * time.Minute).Format(time.RFC3339),
	})

	if got := reap(t, path, now, 0); got != 1 {
		t.Fatalf("默认租约应只回收 11 分钟那条，实际 %d", got)
	}
	if j := readSystemJob(t, path, "sjob_default_inside"); j.Status != SystemJobRunning {
		t.Fatalf("租约内作业被误回收：%s", j.Status)
	}
	if j := readSystemJob(t, path, "sjob_default_expired"); j.Status != SystemJobQueued {
		t.Fatalf("超默认租约作业应回 queued：%s", j.Status)
	}
}

// TestClaimNextSystemJobHonorsBackoff 覆盖第三条验收：失败后的作业在退避窗口内
// 不被再次领取，过了窗口才可领。
func TestClaimNextSystemJobHonorsBackoff(t *testing.T) {
	path := openTestDB(t)
	now := time.Date(2026, 11, 6, 8, 0, 0, 0, time.UTC)

	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_first", Kind: SystemKindUnblock, Status: SystemJobQueued,
		Attempts: 0, MaxAttempts: 3,
		CreatedAt: now.Add(-3 * time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-3 * time.Hour).Format(time.RFC3339),
	})
	// attempts=1 → 30s 退避；只过了 10s，仍在窗口内。
	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_wait30", Kind: SystemKindUnblock, Status: SystemJobQueued,
		Attempts: 1, MaxAttempts: 3,
		CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-10 * time.Second).Format(time.RFC3339),
	})
	// attempts=2 → 60s 退避；已过 61s，可领取。
	seedSystemJob(t, path, &SystemJob{
		JobID: "sjob_ready60", Kind: SystemKindUnblock, Status: SystemJobQueued,
		Attempts: 2, MaxAttempts: 3,
		CreatedAt: now.Add(-time.Hour).Format(time.RFC3339),
		UpdatedAt: now.Add(-61 * time.Second).Format(time.RFC3339),
	})

	claim := func() *SystemJob {
		var job *SystemJob
		err := WithTx(path, func(tx *sql.Tx) error {
			var e error
			job, e = ClaimNextSystemJob(tx, "w", []string{SystemKindUnblock}, now)
			return e
		})
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		return job
	}

	first := claim()
	if first == nil || first.JobID != "sjob_first" {
		t.Fatalf("应先领 attempts=0 的最早作业，实际 %+v", first)
	}
	// 下一条：sjob_wait30 被退避挡住，应跳过它领到 sjob_ready60。
	second := claim()
	if second == nil || second.JobID != "sjob_ready60" {
		t.Fatalf("退避窗口内不得领取，应领 sjob_ready60，实际 %+v", second)
	}
	if got := second.Attempts; got != 3 {
		t.Fatalf("领取应把 attempts 递增到 3，实际 %d", got)
	}
	// 只剩 sjob_wait30，仍在 30s 窗口内。
	if third := claim(); third != nil {
		t.Fatalf("sjob_wait30 仍在退避窗口，不应被领取：%+v", third)
	}
	// 20 秒后窗口结束。
	now = now.Add(20 * time.Second)
	if late := claim(); late == nil || late.JobID != "sjob_wait30" {
		t.Fatalf("退避窗口结束后应可领取，实际 %+v", late)
	}
}

// TestSystemJobBackoff 固定 30s * 2^(attempts-1) 封顶 10 分钟的口径。
func TestSystemJobBackoff(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{0, 0},
		{1, 30 * time.Second},
		{2, time.Minute},
		{3, 2 * time.Minute},
		{5, 8 * time.Minute},
		{6, 10 * time.Minute},
		{10, 10 * time.Minute},
	}
	for _, c := range cases {
		if got := systemJobBackoff(c.attempts); got != c.want {
			t.Fatalf("attempts=%d backoff=%v，期望 %v", c.attempts, got, c.want)
		}
	}
}
