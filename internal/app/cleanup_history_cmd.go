package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
)

// CleanupHistoryOptions 控制历史 block 事件与噪声 system_jobs 的归档清理。
type CleanupHistoryOptions struct {
	Yes            bool
	DryRun         bool
	OlderThanHours int // system_jobs 冷却龄；默认 24h
	ProjectID      string
}

// CleanupHistory 导出报告后，删除终态题的历史 →blocked 事件与过期 unblock_assist 作业。
// 默认 dry-run；须 --yes 才写入。
func CleanupHistory(cwd string, opts CleanupHistoryOptions) (Result, error) {
	pc, err := resolveProject(cwd)
	if err != nil {
		return nil, err
	}
	preview := opts.DryRun || !opts.Yes
	ageH := opts.OlderThanHours
	if ageH <= 0 {
		ageH = 24
	}
	before := time.Now().UTC().Add(-time.Duration(ageH) * time.Hour)
	projectID := opts.ProjectID
	if projectID == "" {
		projectID = pc.ID()
	}

	var events []db.BlockedTransitionEvent
	var jobs []*db.SystemJob
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		events, e = db.ListTerminalBlockedTransitions(tx, projectID, 5000)
		if e != nil {
			return e
		}
		jobs, e = db.ListStaleUnblockJobs(tx, projectID, before, 5000)
		return e
	})
	if err != nil {
		return nil, err
	}

	report := map[string]any{
		"generated_at":           db.UTCNow(),
		"project_id":             projectID,
		"blocked_events":         events,
		"stale_unblock_jobs":     summarizeJobs(jobs),
		"older_than_hours":       ageH,
		"blocked_event_count":    len(events),
		"stale_unblock_job_count": len(jobs),
	}
	exportPath, exportErr := writeHistoryExport(report)
	if exportErr != nil {
		return nil, exportErr
	}

	res := Result{
		"ok": true, "dry_run": preview, "project_id": projectID,
		"export_path": exportPath,
		"blocked_events": len(events), "stale_unblock_jobs": len(jobs),
		"older_than_hours": ageH,
	}
	if preview {
		res["next"] = "确认后执行：maw prune --history --yes"
		return res, nil
	}

	var deletedEvents, deletedJobs int64
	err = db.WithTx(pc.GlobalDB, func(tx *sql.Tx) error {
		ids := make([]int64, 0, len(events))
		for _, e := range events {
			ids = append(ids, e.ID)
		}
		n, e := db.DeleteEventsByIDs(tx, ids)
		if e != nil {
			return e
		}
		deletedEvents = n
		jids := make([]string, 0, len(jobs))
		for _, j := range jobs {
			if j != nil {
				jids = append(jids, j.JobID)
			}
		}
		n2, e := db.DeleteSystemJobsByIDs(tx, jids)
		if e != nil {
			return e
		}
		deletedJobs = n2
		_ = db.AppendEvent(tx, projectID, "orchestrator", "history_cleaned", "", map[string]any{
			"deleted_events": deletedEvents, "deleted_jobs": deletedJobs,
			"export_path": exportPath,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	_, _ = refreshBoard(pc)
	res["dry_run"] = false
	res["deleted_events"] = deletedEvents
	res["deleted_jobs"] = deletedJobs
	res["next"] = i18n.T("next.inspect_board")
	return res, nil
}

func summarizeJobs(jobs []*db.SystemJob) []map[string]any {
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		if j == nil {
			continue
		}
		out = append(out, map[string]any{
			"job_id": j.JobID, "project_id": j.ProjectID, "task_id": j.TaskID,
			"status": j.Status, "reason": j.Reason, "updated_at": j.UpdatedAt,
		})
	}
	return out
}

func writeHistoryExport(report map[string]any) (string, error) {
	gdir, err := home.GlobalDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(gdir, "exports")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name := "block-feedback-" + time.Now().UTC().Format("20060102-150405") + ".json"
	path := filepath.Join(dir, name)
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
