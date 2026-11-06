package systemagent

import (
	"context"
	"fmt"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/runner/recovery"
)

// ReconcileVerdict 是核实作业的唯一合法结论集合（契约 §5）。
//
// 编排层只转述端口结论，不按 Evidence.State 自行推导 mode。
type ReconcileVerdict string

const (
	// ReconcileRelease 释放占用：Run 落终态并回退租约/槽位。
	ReconcileRelease ReconcileVerdict = "release"
	// ReconcileKeep 保留占用：现场未确认或日志待补交。
	ReconcileKeep ReconcileVerdict = "keep"
	// ReconcileEscalate 现场无法自动处置，升级人工。
	ReconcileEscalate ReconcileVerdict = "escalate"
)

// ReconcileRequest 是一次核实作业的输入。
type ReconcileRequest struct {
	ProjectID   string
	ProjectPath string
	JobID       string
	RunID       string
	InstanceID  string
	TaskID      string
}

// ReconcileOutcome 是端口给出的核实结论。
//
// ResourcesReleased 仅在 release 时非空，元素取 node_slot / workspace / lease。
type ReconcileOutcome struct {
	Verdict           ReconcileVerdict
	Evidence          recovery.Evidence
	ResourcesReleased []string
}

// ReconcilePort 由装配层注入（真实进程探测见 SH-303）。
type ReconcilePort interface {
	Reconcile(ctx context.Context, req ReconcileRequest) (ReconcileOutcome, error)
}

// runReconcileJob 执行一条 run_reconcile 作业。
//
// 端口未接线时终态 escalated 且 mode=unwired，不修改任何资源；
// 接线后只按端口结论写 status 与 result，result 键见契约 §4.3。
func (o *Orchestrator) runReconcileJob(ctx context.Context, job *db.SystemJob, projectPath string) error {
	if o.Reconcile == nil {
		return o.finish(job, Decision{Action: ActionEscalateHuman, Reason: "Run 核实未接线"},
			map[string]any{"mode": "unwired"}, db.SystemJobEscalated)
	}

	runID, _ := job.Decision["run_id"].(string)
	out, err := o.Reconcile.Reconcile(ctx, ReconcileRequest{
		ProjectID:   job.ProjectID,
		ProjectPath: projectPath,
		JobID:       job.JobID,
		RunID:       runID,
		InstanceID:  job.InstanceID,
		TaskID:      job.TaskID,
	})
	if err != nil {
		return err
	}

	status := db.SystemJobDone
	action := ActionNotifyOnly
	switch out.Verdict {
	case ReconcileRelease, ReconcileKeep:
	case ReconcileEscalate:
		status = db.SystemJobEscalated
		action = ActionEscalateHuman
	default:
		return fmt.Errorf("systemagent: run_reconcile 返回未知结论 %q", out.Verdict)
	}

	result := map[string]any{
		"reconcile_state": string(out.Evidence.State),
		"mode":            string(out.Verdict),
		"evidence_reason": out.Evidence.Reason,
		"journal_pending": out.Evidence.Journal.PendingRecords,
		"rechecked_at":    o.now().Format(time.RFC3339),
	}
	if out.Verdict == ReconcileRelease && len(out.ResourcesReleased) > 0 {
		result["resources_released"] = out.ResourcesReleased
	}

	return o.finish(job, Decision{Action: action, Reason: "run_reconcile " + string(out.Verdict)}, result, status)
}
