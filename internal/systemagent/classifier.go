package systemagent

import (
	"strings"

	"github.com/kiwh77/maw/internal/config"
)

// ClassifyBlocked 根据阻塞原因与配置决定是否入队、是否直接升级给人。
//
// 返回 enqueue=false 表示仅通知；escalate=true 表示入队但动作固定为 escalate_human（不调 LLM）。
func ClassifyBlocked(reason string, cfg config.SystemAgent) (enqueue, escalate bool) {
	reason = strings.ToLower(strings.TrimSpace(reason))
	for _, p := range cfg.EscalatePatterns {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" && strings.Contains(reason, p) {
			return true, true
		}
	}
	for _, k := range cfg.AutoKinds {
		if strings.TrimSpace(k) == SystemKindUnblock {
			return true, false
		}
	}
	return false, false
}

// KindAllowed 报告 kind 是否在 auto_kinds 或显式触发白名单内。
func KindAllowed(kind string, cfg config.SystemAgent, explicit bool) bool {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return false
	}
	if explicit {
		switch kind {
		case SystemKindUnblock, SystemKindTaskSplit, SystemKindOpsRun, SystemKindDispatchReady, SystemKindRunReconcile:
			return true
		}
		return false
	}
	for _, k := range cfg.AutoKinds {
		if strings.TrimSpace(k) == kind {
			return true
		}
	}
	return false
}

// OpsAllowed 检查运维命令是否在白名单（整串前缀匹配）。
func OpsAllowed(cmd string, allowlist []string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	for _, a := range allowlist {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if cmd == a || strings.HasPrefix(cmd, a+" ") {
			return true
		}
	}
	return false
}

const (
	SystemKindUnblock       = "unblock_assist"
	SystemKindTaskSplit     = "task_split"
	SystemKindOpsRun        = "ops_run"
	SystemKindDispatchReady = "dispatch_ready"
	SystemKindRunReconcile  = "run_reconcile"
)
