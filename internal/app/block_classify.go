package app

import (
	"strings"
)

// Block 原因类别（与 Agent 上报约定对齐，供看板 / 清理归档对照）。
const (
	BlockCatScope     = "scope"
	BlockCatDeps      = "deps"
	BlockCatEnv       = "env"
	BlockCatSpec      = "spec"
	BlockCatConflict  = "conflict"
	BlockCatMawCLI    = "maw-cli"
	BlockCatHuman     = "human"
	BlockCatGate      = "gate"
	BlockCatBaseline  = "baseline"
	BlockCatOther     = "other"
)

// ClassifyBlockCategory 从 block 文案推断类别。
// 优先识别显式前缀 "[scope]" / "类别:scope" / "[gate] …"。
func ClassifyBlockCategory(message string) string {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return BlockCatOther
	}
	if cat := explicitBlockCategory(msg); cat != "" {
		return cat
	}
	low := strings.ToLower(msg)
	switch {
	case containsAny(low, "write_scope", "out of scope", "out_of_scope") ||
		containsAny(msg, "写盘", "越界") ||
		(strings.Contains(msg, "不在") && strings.Contains(low, "scope")) ||
		strings.Contains(low, "request-change"):
		return BlockCatScope
	case containsAny(low, "baseline", "required_paths") ||
		containsAny(msg, "基线", "未跟踪", "缺少") && containsAny(msg, "路径", "文件"):
		return BlockCatBaseline
	case containsAny(low, "dep", "dependency") || containsAny(msg, "依赖", "上游未", "后端未实现"):
		return BlockCatDeps
	case containsAny(low, "env_failure", "node_stale", "manual-compat", "e340", "verification") ||
		containsAny(msg, "验证运行环境", "失联", "节点", "心跳"):
		return BlockCatEnv
	case containsAny(low, "dirty_tree", "working tree dirty") ||
		containsAny(msg, "工作区不干净", "主工作区", "未提交"):
		return BlockCatConflict
	case containsAny(low, "gate failed", "go test ./...", "make check") ||
		containsAny(msg, "门禁", "闸门"):
		return BlockCatGate
	case containsAny(msg, "规格", "验收不清", "题卡") || containsAny(low, "spec unclear"):
		return BlockCatSpec
	case containsAny(low, "maw ", "cli") && containsAny(msg, "无", "不可用", "缺失"):
		return BlockCatMawCLI
	case containsAny(msg, "等人", "人工", "需要维护者", "需人工"):
		return BlockCatHuman
	default:
		return BlockCatOther
	}
}

func explicitBlockCategory(msg string) string {
	low := strings.ToLower(strings.TrimSpace(msg))
	known := []string{
		BlockCatScope, BlockCatDeps, BlockCatEnv, BlockCatSpec,
		BlockCatConflict, BlockCatMawCLI, BlockCatHuman, BlockCatGate,
		BlockCatBaseline, BlockCatOther,
	}
	for _, cat := range known {
		if strings.HasPrefix(low, "["+cat+"]") ||
			strings.HasPrefix(low, cat+"]") ||
			strings.HasPrefix(low, cat+":") ||
			strings.HasPrefix(low, cat+"：") ||
			strings.HasPrefix(low, "类别:"+cat) ||
			strings.HasPrefix(low, "类别："+cat) {
			return cat
		}
	}
	return ""
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// suggestedFollowupsForBlock 根据 block 文案给出可执行下一步。
func suggestedFollowupsForBlock(message string) []string {
	cat := ClassifyBlockCategory(message)
	var out []string
	switch cat {
	case BlockCatScope:
		out = append(out,
			"maw request-change --add '<path/**>' -m 'expand scope'",
			"maw task cancel <id> --reason out_of_scope --superseded-by <new>",
		)
	case BlockCatEnv:
		out = append(out,
			"maw node list --json",
			"检查 manual-compat / local_cli 心跳后重试 maw submit",
		)
	case BlockCatConflict:
		out = append(out,
			"列出主区脏文件并交维护者提交/stash；Agent 勿擅清他人改动",
			"maw block 后等人处理主工作区，再 maw merge --yes",
		)
	case BlockCatGate:
		out = append(out,
			"确认失败包是否在 write_scope 外；主干先修门禁或人工放行",
			"勿为过门禁改 scope 外代码",
		)
	case BlockCatBaseline, BlockCatDeps:
		out = append(out,
			"补齐 required_paths / 上游依赖后 unblock 或拆题",
		)
	case BlockCatHuman:
		out = append(out, "等待维护者处理；勿绕过协议硬改")
	}
	return out
}
