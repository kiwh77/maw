package app

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/projection"
)

// SystemHealthInput 是 `maw system health` 的入参。
//
// 空 ProjectID 表示当前工程；Start/End 均为 RFC3339（UTC 或带偏移），
// 只给一端时另一端取「现在」，两端都空则默认最近 24 小时。
type SystemHealthInput struct {
	ProjectID string
	Start     string
	End       string
}

// systemHealthMaxSpan 与 HTTP 契约一致：单次查询不得超过 31 天。
const systemHealthMaxSpan = 31 * 24 * time.Hour

// SystemHealthReport 按 `maw.lead.selfheal/v1` §6 输出系统健康报表。
//
// 与 HTTP 接口共用同一套 analytics 计算，只把结果包成 CLI 的 Result：
// 数学口径不在 CLI 里重算，避免两条路径给出不同的比率。
func SystemHealthReport(cwd string, in SystemHealthInput) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, i18n.E("err.resolve_project", "next.init_or_project")
	}
	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		projectID = pc.ID()
	}

	start, end, err := systemHealthWindow(in.Start, in.End, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	store, err := db.OpenStore(pc.GlobalDB)
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	svc := &analytics.Service{}
	var report *analytics.SystemHealthReport
	err = store.Read(func(tx *sql.Tx) error {
		r, e := svc.SystemHealth(tx, analytics.Query{
			ProjectID: projectID,
			Window:    projection.Window{Start: start, End: end},
			Loc:       time.UTC,
		})
		if e != nil {
			return e
		}
		report = r
		return nil
	})
	if err != nil {
		return nil, err
	}

	return Result{
		"ok":         true,
		"project_id": projectID,
		"start":      start.Format(time.RFC3339),
		"end":        end.Format(time.RFC3339),
		"job_rows":   report.JobRows,
		"run_rows":   report.RunRows,
		"agent_rows": report.AgentRows,
		"health":     report,
		"next":       i18n.T("next.system_health"),
	}, nil
}

// systemHealthWindow 解析 CLI 时间参数：半开区间，且必须有正向跨度。
func systemHealthWindow(startRaw, endRaw string, now time.Time) (time.Time, time.Time, error) {
	startRaw, endRaw = strings.TrimSpace(startRaw), strings.TrimSpace(endRaw)
	var start, end time.Time
	var err error
	if startRaw != "" {
		if start, err = parseHealthTime(startRaw); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	if endRaw != "" {
		if end, err = parseHealthTime(endRaw); err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	switch {
	case start.IsZero() && end.IsZero():
		end = now
		start = end.Add(-24 * time.Hour)
	case start.IsZero():
		start = end.Add(-24 * time.Hour)
	case end.IsZero():
		end = now
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("--end 必须晚于 --start")
	}
	if end.Sub(start) > systemHealthMaxSpan {
		return time.Time{}, time.Time{}, fmt.Errorf("查询跨度不得超过 31 天")
	}
	return start.UTC(), end.UTC(), nil
}

// parseHealthTime 只接受 RFC3339：空串由调用方先判断，解析失败即报错。
func parseHealthTime(raw string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("时间必须是 RFC3339（如 2026-10-24T00:00:00Z）: %s", raw)
	}
	return t, nil
}
