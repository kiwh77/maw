package systemagent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Action 是 LLM / 规则层产出的决策动作。
type Action string

const (
	ActionRelaunchWorker   Action = "relaunch_worker"
	ActionSplitTasks       Action = "split_tasks"
	ActionUnblockAndRetry  Action = "unblock_and_retry"
	ActionEscalateHuman    Action = "escalate_human"
	ActionOpsRun           Action = "ops_run"
	ActionNotifyOnly       Action = "notify_only"
	// ActionDispatchReady 是 dispatch_ready 的系统内动作，只由编排器产出，不进 LLM 决策空间。
	ActionDispatchReady Action = "dispatch_ready"
)

// Decision 是系统 Agent 对一次作业的结构化决策。
type Decision struct {
	Action     Action         `json:"action"`
	Reason     string         `json:"reason"`
	Blueprint  string         `json:"blueprint,omitempty"`
	OpsCommand string         `json:"ops_command,omitempty"`
	Extra      map[string]any `json:"extra,omitempty"`
}

// ParseDecision 从 LLM 输出中解析 Decision（容忍前后杂文，取第一个 JSON 对象）。
func ParseDecision(raw string) (Decision, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Decision{}, fmt.Errorf("systemagent: 空决策")
	}
	var d Decision
	if err := json.Unmarshal([]byte(raw), &d); err == nil && d.Action != "" {
		return d, validateDecision(d)
	}
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return Decision{}, fmt.Errorf("systemagent: 无法从输出中找到 JSON 决策")
	}
	if err := json.Unmarshal([]byte(raw[start:end+1]), &d); err != nil {
		return Decision{}, fmt.Errorf("systemagent: 解析决策 JSON: %w", err)
	}
	return d, validateDecision(d)
}

func validateDecision(d Decision) error {
	switch d.Action {
	case ActionRelaunchWorker, ActionSplitTasks, ActionUnblockAndRetry, ActionEscalateHuman, ActionOpsRun, ActionNotifyOnly:
		return nil
	default:
		return fmt.Errorf("systemagent: 未知 action %q", d.Action)
	}
}

// DecisionJSON 序列化决策。
func DecisionJSON(d Decision) map[string]any {
	b, _ := json.Marshal(d)
	out := map[string]any{}
	_ = json.Unmarshal(b, &out)
	return out
}
