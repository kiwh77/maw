package app

import (
	"database/sql"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/gitops"
	"github.com/kiwh77/maw/internal/systemagent"
)

// hubSignalFacts 把本机状态库里的真实事实喂给 systemagent.DetectSystemSignals。
//
// 「事实从哪来」与「怎么判定」分离：本文件只读库、不做判断；四类信号的判定
// 全在 systemagent 的纯函数里，可以用假证据完整覆盖。读库失败时返回空快照，
// 绝不猜测、绝不伪造证据。
type hubSignalFacts struct {
	projects   []string
	gateFails  []systemagent.GateFailureFact
	heartbeats []systemagent.AgentHeartbeatFact
	merging    []systemagent.MergingTaskFact
	locks      []systemagent.HeldLockFact
	runs       []systemagent.UnverifiedRunFact
}

func newHubSignalFacts(gdb string, cfg config.SystemAgent) systemagent.SignalFacts {
	f := &hubSignalFacts{}
	if strings.TrimSpace(gdb) == "" {
		return f
	}
	_ = db.WithTx(gdb, func(tx *sql.Tx) error {
		f.load(tx, cfg)
		return nil
	})
	return f
}

func (f *hubSignalFacts) load(tx *sql.Tx, cfg config.SystemAgent) {
	projects, err := db.ListProjects(tx)
	if err != nil {
		return
	}
	mergeCmd := allowedMergeCommand(cfg.OpsAllowlist)
	for _, p := range projects {
		if p == nil || p.ID == "" {
			continue
		}
		f.projects = append(f.projects, p.ID)

		if agents, e := db.ListAgents(tx, p.ID); e == nil {
			for _, a := range agents {
				if a == nil {
					continue
				}
				f.heartbeats = append(f.heartbeats, systemagent.AgentHeartbeatFact{
					ProjectID: p.ID, AgentID: a.ID, Status: a.Status,
					LastSeen: parseRFC3339(a.LastSeen),
				})
			}
		}
		if tasks, e := db.ListTasksFiltered(tx, p.ID, "", "merging"); e == nil {
			for _, t := range tasks {
				if t == nil {
					continue
				}
				f.merging = append(f.merging, systemagent.MergingTaskFact{
					ProjectID: p.ID, TaskID: t.ID,
					Since: parseRFC3339(t.Heartbeat), OpsCommand: mergeCmd,
				})
			}
		}
		if leases, e := db.ListActiveLeases(tx, p.ID); e == nil {
			for _, l := range leases {
				if l == nil {
					continue
				}
				f.locks = append(f.locks, systemagent.HeldLockFact{
					ProjectID: p.ID, ResourceKey: l.ResourceKey, Holder: l.Holder,
					AcquiredAt: parseRFC3339(l.AcquiredAt),
					// 活跃租约的占用者身份一律按「证据不足」处理：只上报，
					// 不允许自动释放（与 maw prune 的 unknown 占用保护一致）。
					UnknownHolder: true,
				})
			}
		}
		// Run 核实证据：状态取 reconciliation_state，时间取 updated_at。
		// keep 会刷新 updated_at，因此「最近一次核实」天然成为 15 分钟窗口的一部分。
		taskByInstance := map[string]string{}
		if tasks, e := db.ListTasks(tx, p.ID); e == nil {
			for _, t := range tasks {
				if t != nil && t.InstanceID != "" {
					taskByInstance[t.InstanceID] = t.ID
				}
			}
		}
		if runs, e := db.ListRuns(tx, p.ID); e == nil {
			for _, r := range runs {
				if r == nil || r.ID == "" {
					continue
				}
				// 已确认的终态 Run 不再产出证据；unknown 或非终态才需要核实。
				if r.Reconciliation != model.ReconciliationUnknown && r.Phase.Terminal() {
					continue
				}
				seen := parseRFC3339(r.UpdatedAt)
				f.runs = append(f.runs, systemagent.UnverifiedRunFact{
					ProjectID:  p.ID,
					TaskID:     taskByInstance[r.InstanceID],
					RunID:      r.ID,
					InstanceID: r.InstanceID,
					State:      string(r.Reconciliation),
					LastSeen:   seen,
					CheckedAt:  seen,
				})
			}
		}
	}

	// 门禁失败证据：gate_env_failed 直接采信；gate_failed 需再用词表复核，
	// 只有「环境失败」才算系统级信号，代码失败不在这里升级。
	events, err := db.ListEventsSince(tx, 0, "", "", 5000)
	if err != nil {
		return
	}
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	for _, e := range events {
		if e == nil || e.Type != "gate_env_failed" && e.Type != "gate_failed" {
			continue
		}
		if ts := parseRFC3339(e.TS); !ts.IsZero() && ts.Before(cutoff) {
			continue
		}
		out, _ := e.Data["error"].(string)
		env := e.Type == "gate_env_failed" || gitops.IsEnvFailure(out)
		if !env {
			continue
		}
		f.gateFails = append(f.gateFails, systemagent.GateFailureFact{
			ProjectID: e.ProjectID, TaskID: e.TaskID, Output: out, EnvFailure: env,
		})
	}
}

func parseRFC3339(s string) time.Time {
	v := strings.TrimSpace(s)
	if v == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// allowedMergeCommand 只从 ops_allowlist 里挑一条 merge 命令，绝不拼新命令。
func allowedMergeCommand(allowlist []string) string {
	for _, c := range allowlist {
		if strings.TrimSpace(c) == "maw merge --yes" {
			return c
		}
	}
	return ""
}

func (f *hubSignalFacts) Projects() []string { return f.projects }
func (f *hubSignalFacts) RecentGateFailures() []systemagent.GateFailureFact {
	return f.gateFails
}
func (f *hubSignalFacts) AgentHeartbeats() []systemagent.AgentHeartbeatFact {
	return f.heartbeats
}
func (f *hubSignalFacts) MergingTasks() []systemagent.MergingTaskFact { return f.merging }
func (f *hubSignalFacts) HeldLocks() []systemagent.HeldLockFact       { return f.locks }
func (f *hubSignalFacts) UnverifiedRuns() []systemagent.UnverifiedRunFact {
	return f.runs
}
