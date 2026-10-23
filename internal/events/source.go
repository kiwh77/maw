package events

import "github.com/kiwh77/maw/internal/engine/model"

// Domain 是接入域，一一对应架构文档的三种接入模式（ARCHITECTURE_VNEXT §5.1）。
//
// 分域是去重的第一维：不同接入域即便送来相同内容，也属于两条独立记录，
// 因为「谁说的」本身就是事实的一部分（F44 要求每条记录可追到来源）。
type Domain string

const (
	// DomainManaged 是 managed_cli：maw 拥有进程控制，可写控制类事实。
	DomainManaged Domain = "managed"
	// DomainManual 是 manual_client：人工领取与交付，可声明自己持有的任务。
	DomainManual Domain = "manual"
	// DomainObserved 是 observed_only：旁路上报与回顾，只有观测权。
	DomainObserved Domain = "observed"
)

// Valid 报告接入域是否属于封闭集合。
func (d Domain) Valid() bool {
	return d == DomainManaged || d == DomainManual || d == DomainObserved
}

// CanControl 报告该域能否写控制类事实（运行阶段、任务状态、占用、领取声明）。
// 观测域一律返回 false：看得见不等于能改（§5.1「控制与观测」）。
func (d Domain) CanControl() bool { return d == DomainManaged || d == DomainManual }

// Source 是来源绑定：谁在报、属于哪个接入域、哪个接入器版本。
//
// Adapter 参与去重域，缺失就无法分域，因此必填；它也是「同一 CLI 的两份日志
// 是否算同一来源」的唯一判据。
type Source struct {
	Domain  Domain        `json:"domain"`
	Adapter string        `json:"adapter"`
	NodeID  model.NodeID  `json:"node_id,omitempty"`
	Agent   model.AgentID `json:"agent,omitempty"`
}

// Validate 检查来源绑定是否足以支撑分域去重与归属判定。
//
// 只拒「无法判定」的情况：域未知就不可能有正确的权限与去重域，接入器缺名就无法
// 把两条记录归到同一来源。Agent 缺省是合法的（旁路上报常常没有登录身份），
// 但一旦给了就必须是合法工作者身份。
func (s Source) Validate() []*model.Error {
	var out []*model.Error
	if !s.Domain.Valid() {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, "source.domain",
			"只能是 managed/manual/observed，收到 %q", s.Domain))
	}
	if s.Adapter == "" {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "source.adapter",
			"接入器标识必填，否则无法按来源分域去重"))
	}
	if s.Agent != "" && !s.Agent.Valid() {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, "source.agent",
			"必须是规范的 {应用}-{工程slug}-{序号}，收到 %q", s.Agent))
	}
	if s.NodeID != "" && !s.NodeID.Valid() {
		out = append(out, model.Violationf(model.CodePortFieldInvalid, "source.node_id",
			"节点身份非法：%q", s.NodeID))
	}
	return out
}

// scope 是去重域键：接入域 + 接入器。节点与上报者不进域键——
// 同一个 CLI 在同一节点换个人登录，仍然是同一份来源日志。
func (s Source) scope() string { return string(s.Domain) + "/" + s.Adapter }
