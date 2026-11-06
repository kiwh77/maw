package app

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// seedHealthRows 往全局库写一条作业与配套 Run／Agent。
//
// 只造「窗口内一条 done」的最小现场：CLI 的价值是把 analytics 结果正确
// 序列化并做工程隔离，不是重复测算法（算法由 internal/analytics 覆盖）。
func seedHealthRows(t *testing.T, f *fixture, projectID string) {
	t.Helper()
	store, err := db.OpenStore(f.pc.GlobalDB)
	if err != nil {
		t.Fatalf("打开全局库失败：%v", err)
	}
	defer func() { _ = store.Close() }()

	job := db.SystemJob{
		JobID:      "health-" + projectID,
		ProjectID:  projectID,
		Kind:       db.SystemKindUnblock,
		Status:     db.SystemJobDone,
		Attempts:   1,
		CreatedAt:  "2026-10-20T00:05:00Z",
		FinishedAt: "2026-10-20T00:15:00Z",
	}
	err = store.Write(func(tx *sql.Tx) error {
		if _, err := db.InsertSystemJob(tx, &job); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO runs(
			run_id, project_id, instance_id, version_no, generation, phase, reconciliation_state,
			created_at, updated_at, started_at, finished_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
			"run-"+projectID, projectID, "inst-"+projectID, 1, 1,
			model.PhaseCompleted, "confirmed",
			"2026-10-20T00:06:00Z", "2026-10-20T00:06:00Z", "2026-10-20T00:07:00Z",
			"2026-10-20T00:14:00Z"); err != nil {
			return err
		}
		agents := []db.Agent{
			{ProjectID: projectID, ID: "a1-" + projectID, Role: "worker", Status: "idle",
				RegisteredAt: "2026-10-20T00:00:00Z", LastSeen: "2026-10-20T23:50:00Z"},
			{ProjectID: projectID, ID: "a2-" + projectID, Role: "worker", Status: "idle",
				RegisteredAt: "2026-10-20T00:00:00Z", LastSeen: "2026-10-20T00:20:00Z"},
		}
		for i := range agents {
			if err := db.UpsertAgent(tx, &agents[i]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("写入系统健康夹具失败：%v", err)
	}
}

// TestSystemHealthReportCommand 验证 CLI 默认取当前工程、按窗口查询并输出可机读结果。
func TestSystemHealthReportCommand(t *testing.T) {
	f := newFixture(t)
	seedHealthRows(t, f, f.pc.ID())
	// 另一工程的同形数据必须被隔离掉。
	seedHealthRows(t, f, "other-project")

	res, err := SystemHealthReport(f.repo, SystemHealthInput{
		Start: "2026-10-19T00:00:00Z",
		End:   "2026-10-21T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("SystemHealthReport: %v", err)
	}
	if res["project_id"] != f.pc.ID() {
		t.Fatalf("project_id = %v，期望 %s", res["project_id"], f.pc.ID())
	}
	report, ok := res["health"].(*analytics.SystemHealthReport)
	if !ok || report == nil {
		t.Fatalf("health 类型 = %T", res["health"])
	}
	if report.JobRows != 1 || report.RunRows != 1 || report.AgentRows != 2 {
		t.Fatalf("行数 = %d/%d/%d，期望 1/1/2（工程隔离）",
			report.JobRows, report.RunRows, report.AgentRows)
	}
	if report.Jobs.Total == nil || *report.Jobs.Total != 1 {
		t.Fatalf("total = %v，期望 1", report.Jobs.Total)
	}
	if report.Jobs.SuccessRate.Ratio == nil || *report.Jobs.SuccessRate.Ratio != 1 {
		t.Fatalf("success_rate = %v，期望 1", report.Jobs.SuccessRate.Ratio)
	}
	// 心跳：23:50 新鲜、00:20 过期（截至 10-21 00:00 的 15 分钟窗口外）。
	if report.Agents.HeartbeatFreshness.Ratio == nil ||
		*report.Agents.HeartbeatFreshness.Ratio != 0.5 {
		t.Fatalf("heartbeat = %v，期望 0.5", report.Agents.HeartbeatFreshness.Ratio)
	}

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"health"`) || !strings.Contains(string(raw), `"quality"`) {
		t.Fatalf("JSON 缺少 health/quality：%s", raw)
	}
}

// TestSystemHealthReportWindowValidation 覆盖 CLI 侧的时间参数校验。
func TestSystemHealthReportWindowValidation(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		in   SystemHealthInput
	}{
		{name: "倒置窗口", in: SystemHealthInput{Start: "2026-10-21T00:00:00Z", End: "2026-10-20T00:00:00Z"}},
		{name: "非法时间", in: SystemHealthInput{Start: "2026-10-20"}},
		{name: "跨度超限", in: SystemHealthInput{Start: "2026-08-01T00:00:00Z", End: "2026-10-20T00:00:00Z"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := SystemHealthReport(f.repo, tc.in); err == nil {
				t.Fatalf("期望报错，实际通过")
			}
		})
	}
}
