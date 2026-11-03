package systemagent

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// Effects 是编排器对外的副作用端口（由 app 层注入，避免循环依赖）。
type Effects interface {
	UnblockTask(ctx context.Context, projectPath, taskID string) error
	RelaunchWorker(ctx context.Context, projectPath, taskID, nodeID string) error
	WriteSplitProposal(ctx context.Context, projectPath, taskID, blueprint string) (string, error)
	ApplyPlan(ctx context.Context, projectPath, blueprintPath string) error
	PlanApprovalAuto(projectPath string) bool
}

// NopEffects 提供拆题文件写入的默认实现；其余动作需 app 层接线。
type NopEffects struct{}

func (NopEffects) UnblockTask(context.Context, string, string) error {
	return fmt.Errorf("systemagent: unblock 未接线")
}
func (NopEffects) RelaunchWorker(context.Context, string, string, string) error {
	return fmt.Errorf("systemagent: relaunch 未接线")
}
func (NopEffects) WriteSplitProposal(_ context.Context, projectPath, taskID, blueprint string) (string, error) {
	dir := filepath.Join(projectPath, ".multiagent", "handoffs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("system-split-%s-%d.toml", sanitize(taskID), time.Now().Unix()))
	return path, os.WriteFile(path, []byte(blueprint), 0o644)
}
func (NopEffects) ApplyPlan(context.Context, string, string) error {
	return fmt.Errorf("systemagent: plan apply 未接线")
}
func (NopEffects) PlanApprovalAuto(string) bool { return false }

func sanitize(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, s)
	if s == "" {
		return "task"
	}
	return s
}

// Orchestrator 领取并执行 system_jobs。
type Orchestrator struct {
	Store       *db.Store
	Config      config.SystemAgent
	Invoker     Invoker
	Effects     Effects
	ResolvePath func(projectID string) (string, error)
	WorkerID    string
	Clock       func() time.Time
}

func (o *Orchestrator) now() time.Time {
	if o != nil && o.Clock != nil {
		return o.Clock().UTC()
	}
	return time.Now().UTC()
}

func (o *Orchestrator) effects() Effects {
	if o != nil && o.Effects != nil {
		return o.Effects
	}
	return NopEffects{}
}

// Step 领取至多 max_concurrent 条作业并执行；返回成功开跑条数。
func (o *Orchestrator) Step(ctx context.Context) (int, error) {
	if o == nil || o.Store == nil {
		return 0, fmt.Errorf("systemagent: 缺少 Store")
	}
	n := o.Config.MaxConcurrent
	if n <= 0 {
		n = 1
	}
	kinds := append([]string{}, o.Config.AutoKinds...)
	// 显式入队的 task_split / ops_run 也要能被消费
	for _, extra := range []string{SystemKindTaskSplit, SystemKindOpsRun} {
		found := false
		for _, k := range kinds {
			if k == extra {
				found = true
				break
			}
		}
		if !found {
			kinds = append(kinds, extra)
		}
	}
	worker := o.WorkerID
	if worker == "" {
		worker = "system-agent"
	}
	ran := 0
	for i := 0; i < n; i++ {
		var job *db.SystemJob
		err := o.Store.Write(func(tx *sql.Tx) error {
			var e error
			job, e = db.ClaimNextSystemJob(tx, worker, kinds)
			return e
		})
		if err != nil {
			return ran, err
		}
		if job == nil {
			break
		}
		ran++
		if err := o.runJob(ctx, job); err != nil {
			_ = o.Store.Write(func(tx *sql.Tx) error {
				return db.RequeueSystemJob(tx, job.JobID, map[string]any{"error": err.Error()})
			})
			continue
		}
	}
	return ran, nil
}

// RunJobID 执行指定作业（CLI 手工重放）。
func (o *Orchestrator) RunJobID(ctx context.Context, jobID string) error {
	var job *db.SystemJob
	err := o.Store.Write(func(tx *sql.Tx) error {
		j, e := db.GetSystemJob(tx, jobID)
		if e != nil {
			return e
		}
		if j == nil {
			return fmt.Errorf("systemagent: 未知 job %s", jobID)
		}
		if j.Status != db.SystemJobQueued && j.Status != db.SystemJobFailed {
			return fmt.Errorf("systemagent: job 状态不可跑: %s", j.Status)
		}
		now := db.UTCNow()
		_, e = tx.Exec(`
UPDATE system_jobs SET status=?, locked_by=?, locked_at=?, attempts=attempts+1, updated_at=?
WHERE job_id=?`, db.SystemJobRunning, o.WorkerID, now, now, jobID)
		if e != nil {
			return e
		}
		job, e = db.GetSystemJob(tx, jobID)
		return e
	})
	if err != nil {
		return err
	}
	if err := o.runJob(ctx, job); err != nil {
		_ = o.Store.Write(func(tx *sql.Tx) error {
			return db.RequeueSystemJob(tx, job.JobID, map[string]any{"error": err.Error()})
		})
		return err
	}
	return nil
}

func (o *Orchestrator) runJob(ctx context.Context, job *db.SystemJob) error {
	projectPath := ""
	if o.ResolvePath != nil && job.ProjectID != "" {
		p, err := o.ResolvePath(job.ProjectID)
		if err != nil {
			return err
		}
		projectPath = p
	}

	// 规则：escalate 模式（decision 预置）或 escalate 原因 → 不调 LLM
	if pre, ok := job.Decision["action"].(string); ok && Action(pre) == ActionEscalateHuman {
		return o.finish(job, Decision{Action: ActionEscalateHuman, Reason: job.Reason}, map[string]any{"mode": "preclassified"}, db.SystemJobEscalated)
	}

	var decision Decision
	switch job.Kind {
	case SystemKindOpsRun:
		cmd, _ := job.Decision["ops_command"].(string)
		if cmd == "" {
			cmd = job.Reason
		}
		decision = Decision{Action: ActionOpsRun, OpsCommand: cmd, Reason: "ops_run"}
	default:
		if o.Invoker == nil {
			decision = Decision{Action: ActionEscalateHuman, Reason: "无 LLM Invoker"}
		} else {
			raw, err := o.Invoker.Diagnose(ctx, DiagnoseRequest{
				ProjectPath: projectPath,
				ProjectID:   job.ProjectID,
				TaskID:      job.TaskID,
				Reason:      job.Reason,
				Kind:        job.Kind,
				Message:     fmt.Sprintf("%v", job.Decision["message"]),
			})
			if err != nil {
				return err
			}
			decision, err = ParseDecision(raw)
			if err != nil {
				return err
			}
		}
	}

	result := map[string]any{}
	status := db.SystemJobDone
	eff := o.effects()

	switch decision.Action {
	case ActionEscalateHuman, ActionNotifyOnly:
		status = db.SystemJobEscalated
		result["note"] = "escalated_or_notify"
	case ActionUnblockAndRetry:
		if err := eff.UnblockTask(ctx, projectPath, job.TaskID); err != nil {
			return err
		}
		result["unblocked"] = true
		if o.Config.WorkerNode != "" {
			if err := eff.RelaunchWorker(ctx, projectPath, job.TaskID, o.Config.WorkerNode); err != nil {
				result["relaunch_error"] = err.Error()
			} else {
				result["relaunched"] = true
			}
		}
	case ActionRelaunchWorker:
		node := o.Config.WorkerNode
		if node == "" {
			node = o.Config.LLMNode
		}
		if err := eff.RelaunchWorker(ctx, projectPath, job.TaskID, node); err != nil {
			return err
		}
		result["relaunched"] = true
	case ActionSplitTasks:
		path, err := eff.WriteSplitProposal(ctx, projectPath, job.TaskID, decision.Blueprint)
		if err != nil {
			return err
		}
		result["proposal"] = path
		if decision.Blueprint != "" && eff.PlanApprovalAuto(projectPath) {
			if err := eff.ApplyPlan(ctx, projectPath, path); err != nil {
				result["apply_error"] = err.Error()
			} else {
				result["applied"] = true
			}
		}
	case ActionOpsRun:
		if !OpsAllowed(decision.OpsCommand, o.Config.OpsAllowlist) {
			return fmt.Errorf("systemagent: ops_command 不在白名单: %s", decision.OpsCommand)
		}
		out, err := runOps(ctx, projectPath, decision.OpsCommand)
		result["stdout"] = truncate(out, 2000)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("systemagent: 未处理 action %s", decision.Action)
	}

	return o.finish(job, decision, result, status)
}

func (o *Orchestrator) finish(job *db.SystemJob, d Decision, result map[string]any, status string) error {
	return o.Store.Write(func(tx *sql.Tx) error {
		return db.FinishSystemJob(tx, job.JobID, status, DecisionJSON(d), result)
	})
}

func runOps(ctx context.Context, dir, cmdline string) (string, error) {
	parts := strings.Fields(cmdline)
	if len(parts) == 0 {
		return "", fmt.Errorf("空命令")
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}
