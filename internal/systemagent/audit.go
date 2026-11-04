package systemagent

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
)

// 一份作业一个审计目录，重跑覆盖旧原文：
//   - decision.raw 逐字节保留模型输出，便于 grep 与人工比对；
//   - decision.json 记录作业信息与原文路径，不复制原文（避免两份脱敏口径）。
const (
	auditDecisionFile = "decision.json"
	auditRawFile      = "decision.raw"
)

// systemJobsAuditDir 是本机状态目录下的审计子目录。
const systemJobsAuditDir = "system-jobs"

// projectStateDir 返回本工程受管运行状态根目录，布局与 app.Runtime.StateDir 一致：
// <global>/runtime/<project_id>。系统 Agent 因此不依赖宿主注入就能定位审计落点；
// 工程 ID 为空或全局目录不可用时返回空串，调用方退化为结果内联。
func projectStateDir(projectID string) string {
	if strings.TrimSpace(projectID) == "" {
		return ""
	}
	g, err := home.GlobalDir()
	if err != nil {
		return ""
	}
	return filepath.Join(g, "runtime", projectID)
}

// opsJournalDir 是 op 目录约定里的语义日志位置：<state>/ops/<op>/journal。
func opsJournalDir(projectID, operationID string) string {
	base := projectStateDir(projectID)
	if base == "" || operationID == "" {
		return ""
	}
	return filepath.Join(base, "ops", operationID, "journal")
}

// decisionParseError 把「LLM 原文解析失败」连同侧车路径一起带回重排结果，
// 免得重排后只看到一行解析错误、却查不到模型到底输出了什么。
type decisionParseError struct {
	sidecar string
	err     error
}

func (e *decisionParseError) Error() string { return e.err.Error() }
func (e *decisionParseError) Unwrap() error { return e.err }

// requeueResult 构造重排结果：默认只记错误，解析失败时追加原文侧车路径。
func requeueResult(err error) map[string]any {
	res := map[string]any{"error": err.Error()}
	var pe *decisionParseError
	if errors.As(err, &pe) && pe.sidecar != "" {
		res["decision_raw_path"] = pe.sidecar
	}
	return res
}

// writeDecisionSidecar 把 LLM 原文逐字节写到 <state>/system-jobs/<job>/decision.raw，
// 返回该路径（作业结果里的 decision_raw_path 指向它）。
//
// 原文按 0600 写盘：它可能包含工程路径与命令片段。没有状态目录时不猜路径，
// 返回空串，由调用方退化为结果内联。
func (o *Orchestrator) writeDecisionSidecar(job *db.SystemJob, raw string) string {
	if o == nil || job == nil {
		return ""
	}
	base := projectStateDir(job.ProjectID)
	if base == "" {
		return ""
	}
	dir := filepath.Join(base, systemJobsAuditDir, sanitize(job.JobID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	rawPath := filepath.Join(dir, auditRawFile)
	if err := os.WriteFile(rawPath, []byte(raw), 0o600); err != nil {
		return ""
	}
	payload := map[string]any{
		"job_id":     job.JobID,
		"project_id": job.ProjectID,
		"kind":       job.Kind,
		"task_id":    job.TaskID,
		"reason":     job.Reason,
		"raw_path":   rawPath,
		"raw_bytes":  len(raw),
		"written_at": o.now().Format(time.RFC3339Nano),
	}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		// 原文已落盘，元数据写不出也不影响取证。
		return rawPath
	}
	b = append(b, '\n')
	_ = os.WriteFile(filepath.Join(dir, auditDecisionFile), b, 0o600)
	return rawPath
}
