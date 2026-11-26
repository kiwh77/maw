package protocol

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/version"
)

// ClaimFields 是人工领取提示模板必须保留的身份字段（不含任何令牌）。
type ClaimFields struct {
	TaskID     string // 展示编号，如 VN-901
	InstanceID string // ti_…
	Version    int    // 活跃定义版本
	Source     string // 领取来源：manual_next / status_attach / managed_cli
	AgentID    string
	ProjectID  string
	Worktree   string
}

// TokenPattern 匹配不得进入提示词、仓库协议段或事件载荷的本机令牌形态。
var TokenPattern = regexp.MustCompile(`(?i)\b(maw_pat_[a-z0-9]+|Bearer\s+[A-Za-z0-9._\-+=/]+)\b`)

// RenderClaimTemplate 生成人工领取上下文模板：保留实例／版本／来源，绝不嵌入令牌。
func RenderClaimTemplate(f ClaimFields, lang i18n.Lang) string {
	src := strings.TrimSpace(f.Source)
	if src == "" {
		src = "manual_next"
	}
	if lang == i18n.Zh {
		return fmt.Sprintf(`# maw 领取上下文（人工）
- 任务：%s
- 实例：%s
- 版本：%d
- 来源：%s
- Agent：%s
- 工程：%s
- worktree：%s

规则：令牌只走本机 Authorization 头或受权文件，不得写入本模板、仓库或事件。
旁路 status report 只能上报自己的观察，不得夺取 managed Run 所有权。
`, f.TaskID, f.InstanceID, f.Version, src, f.AgentID, f.ProjectID, f.Worktree)
	}
	return fmt.Sprintf(`# maw claim context (manual)
- task: %s
- instance: %s
- version: %d
- source: %s
- agent: %s
- project: %s
- worktree: %s

Rules: tokens stay in Authorization / local auth files only — never in this template, the repo, or events.
Bypass status reports may publish their own observations only; they must not seize managed Run ownership.
`, f.TaskID, f.InstanceID, f.Version, src, f.AgentID, f.ProjectID, f.Worktree)
}

// RedactSecrets 从文本中抹去令牌形态，供提示词／事件载荷消毒。
func RedactSecrets(s string) string {
	return TokenPattern.ReplaceAllString(s, "[REDACTED]")
}

// ContainsSecret 报告文本是否仍含令牌形态。
func ContainsSecret(s string) bool {
	return TokenPattern.MatchString(s)
}

// ObserverMayClaimRun 判断旁路上报方能否「拥有」某 Run。
// 旁路永远返回 false：只能观察，不能夺取 managed Run。
func ObserverMayClaimRun(reporterAgent, runOwner string, managed bool) bool {
	_ = reporterAgent
	_ = runOwner
	if managed {
		return false
	}
	// 非 managed 也不通过旁路夺取所有权；领取只走 maw next / managed start。
	return false
}

// UpgradeHint 在协议文件版本落后时给出升级提示（中英文随当前语言）。
func UpgradeHint(fileVersion int) string {
	cur := version.ProtocolVersion
	if fileVersion >= cur {
		return ""
	}
	if i18n.Current() == i18n.Zh {
		return fmt.Sprintf("协议文件为 v%d，当前 CLI 需要 v%d：运行 `maw init --upgrade` 刷新 AGENTS.md／规则，勿手改协议段。", fileVersion, cur)
	}
	return fmt.Sprintf("protocol file is v%d; this CLI expects v%d — run `maw init --upgrade` to refresh AGENTS.md/rules; do not edit the block by hand", fileVersion, cur)
}
