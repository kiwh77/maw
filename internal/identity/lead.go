package identity

import (
	"fmt"
	"strings"

	"github.com/kiwh77/maw/internal/config"
)

// IsLead 判断 agent 是否被列为本机 lead 能力标记。
//
// lead 是「同一个 worker 多一项授权」的本机标记，不是新角色：
//   - 不改 Role() 的返回值（仍是 worker / integrator / reviewer 三类）；
//   - 不改 agents 表结构；
//   - integrator / reviewer 不会因为角色自动成为 lead，必须显式列入 [identity] leads。
//
// 因此这里只查 cfg.Identity.Leads，不复用 Role()。
func IsLead(agent string, cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	agent = strings.TrimSpace(agent)
	if agent == "" {
		return false
	}
	for _, l := range cfg.Identity.Leads {
		if strings.TrimSpace(l) == agent {
			return true
		}
	}
	return false
}

// RequireLead 校验 agent 是否具备 lead 能力；非 lead 返回明确错误。
//
// 错误用 fmt.Errorf 直接构造，不新增 i18n catalog 键（catalog 由收口卡独占）。
func RequireLead(agent string, cfg *config.Config) error {
	if IsLead(agent, cfg) {
		return nil
	}
	return fmt.Errorf("agent %q 不是 lead：需在 [identity] leads 中显式登记（lead 是本机能力标记，integrator/reviewer 不会自动成为 lead）", agent)
}

// 系统 actor：中枢/编排侧。它们不是「人」，不需要在 leads 名单里登记。
var systemActors = map[string]bool{
	"system":       true,
	"orchestrator": true,
}

// LeadViolation 是权限分层的稳定错误形状（契约 §5.2/§7：code=not_lead）。
//
// 与 errx.Error 分开的原因：identity 不依赖 app 的错误装配，调用方按 Code()
// 决定 HTTP 403 / CLI 退出码，错误文本可以随语言变，码不变。
type LeadViolation struct {
	Agent  string
	Action string
}

func (e *LeadViolation) Error() string {
	return "not_lead: agent " + e.Agent + " 无权执行 " + e.Action +
		"（需在本机 [identity] leads 登记，或由中枢 system 发起）"
}

// Code 是稳定错误码。
func (e *LeadViolation) Code() string { return "not_lead" }

// RequireLeadAction 校验 agent 是否有权执行受控动作（reassign / interrupt / archive …）。
//
//   - leads 名单中的本机 Agent：可以；
//   - system / orchestrator：可以（中枢自己）；
//   - 其它：返回 *LeadViolation。
func RequireLeadAction(agent string, cfg *config.Config, action string) error {
	a := strings.TrimSpace(agent)
	if systemActors[strings.ToLower(a)] || IsLead(a, cfg) {
		return nil
	}
	return &LeadViolation{Agent: a, Action: action}
}
