package nodes

import (
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Violations 把领域校验结果聚合成一个 error。
type Violations []*model.Error

func (v Violations) Error() string {
	if len(v) == 0 {
		return ""
	}
	parts := make([]string, 0, len(v))
	for _, e := range v {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// Admission 是管理侧的接收策略，和四维观测状态分开存放。
type Admission string

// 接收中／排空中／已停用。
const (
	AdmissionOpen     Admission = "open"
	AdmissionDraining Admission = "draining"
	AdmissionDisabled Admission = "disabled"
)

// Valid 报告取值是否合法。
func (a Admission) Valid() bool {
	return a == AdmissionOpen || a == AdmissionDraining || a == AdmissionDisabled
}

// Registration 是一次节点登记／配置更新请求。
//
// ProjectID 只是归属展示，不是工作者身份；SharedGroup 为空表示不参与共享资源组，
// 非空时必须显式给出 GroupLimit，且同组成员必须一致。
type Registration struct {
	Config      model.NodeConfig `json:"config"`
	ProjectID   string           `json:"project_id,omitempty"`
	SharedGroup string           `json:"shared_group,omitempty"`
	GroupLimit  int              `json:"group_limit,omitempty"`
}

// CapabilityEvidence 是一项能力的声明值与验证值。
//
// Declared 来自能力矩阵或配置声明，Verified 只由实测探测写入；自动匹配只看 Verified。
type CapabilityEvidence struct {
	Name        string                `json:"name"`
	Declared    model.CapabilityState `json:"declared"`
	Verified    model.CapabilityState `json:"verified"`
	Version     model.SemVer          `json:"version"`
	EvidenceRef string                `json:"evidence"`
	VerifiedAt  time.Time             `json:"verified_at,omitempty"`
}

// Matchable 报告该能力能否参与自动匹配：只有本版本、带证据、未过期的 observed 可以。
func (c CapabilityEvidence) Matchable(now time.Time, ttl time.Duration) bool {
	if c.Verified != model.CapabilityObserved {
		return false
	}
	if !c.Version.Valid() || strings.TrimSpace(c.EvidenceRef) == "" {
		return false
	}
	if c.VerifiedAt.IsZero() {
		return false
	}
	if ttl > 0 && now.Sub(c.VerifiedAt) > ttl {
		return false
	}
	return true
}

// Invalidation 记录一次能力证据失效及其原因。
type Invalidation struct {
	Reason       string       `json:"reason"`
	FromVersion  model.SemVer `json:"from_version,omitempty"`
	ToVersion    model.SemVer `json:"to_version,omitempty"`
	Capabilities []string     `json:"capabilities,omitempty"`
	At           time.Time    `json:"at"`
}

// 失效原因：版本变化／入口或平台变化／入口不可用。
const (
	InvalidateVersionChanged   = "version_changed"
	InvalidateEntryChanged     = "entry_changed"
	InvalidateEntryUnavailable = "entry_unavailable"
)

// Status 是四维观测状态，任一行都不等价于「进程已停止」。
type Status struct {
	Entry      model.EntryState     `json:"entry"`
	Auth       model.ReadinessState `json:"auth"`
	Connection model.ReadinessState `json:"connection"`
	Capacity   model.ReadinessState `json:"capacity"`
	ObservedAt time.Time            `json:"observed_at,omitempty"`
	ProbeRef   string               `json:"probe_ref,omitempty"`
}

// UnknownStatus 是未探测时的状态：四个维度都保持 unknown，不猜 ready。
func UnknownStatus() Status {
	return Status{
		Entry:      model.EntryUnknown,
		Auth:       model.ReadyUnknown,
		Connection: model.ReadyUnknown,
		Capacity:   model.ReadyUnknown,
	}
}

// Record 是注册表里的一条节点；Revision 用于条件写入。
type Record struct {
	NodeID      model.NodeID     `json:"node_id"`
	ProjectID   string           `json:"project_id,omitempty"`
	SharedGroup string           `json:"shared_group,omitempty"`
	GroupLimit  int              `json:"group_limit,omitempty"`
	Config      model.NodeConfig `json:"config"`
	Revision    model.Revision   `json:"revision"`

	Status        Status               `json:"status"`
	Capabilities  []CapabilityEvidence `json:"capabilities,omitempty"`
	Invalidations []Invalidation       `json:"invalidations,omitempty"`

	// CLIVersion 与 EntryFingerprint 是最近一次探测确认的版本与入口身份；
	// 任一项变化都使旧的 observed 能力证据失效。
	CLIVersion       model.SemVer `json:"cli_version,omitempty"`
	EntryFingerprint string       `json:"entry_fingerprint,omitempty"`

	ActiveSlots int       `json:"active_slots"`
	Draining    bool      `json:"draining"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Admission 返回管理侧接收策略：disabled 优先于 draining。
func (r *Record) Admission() Admission {
	if !r.Config.AcceptNew {
		return AdmissionDisabled
	}
	if r.Draining {
		return AdmissionDraining
	}
	return AdmissionOpen
}

// Snapshot 是某一时刻的只读投影：观测状态叠加过期降级与共享组容量。
type Snapshot struct {
	NodeID      model.NodeID     `json:"node_id"`
	ProjectID   string           `json:"project_id,omitempty"`
	SharedGroup string           `json:"shared_group,omitempty"`
	GroupLimit  int              `json:"group_limit,omitempty"`
	Config      model.NodeConfig `json:"config"`
	Revision    model.Revision   `json:"revision"`

	Status            Status               `json:"status"`
	Admission         Admission            `json:"admission"`
	EffectiveCapacity model.ReadinessState `json:"effective_capacity"`

	ActiveSlots int `json:"active_slots"`
	FreeSlots   int `json:"free_slots"`
	GroupActive int `json:"group_active,omitempty"`

	ProbeAge time.Duration `json:"probe_age,omitempty"`
	Stale    bool          `json:"stale"`

	Capabilities  []CapabilityEvidence `json:"capabilities,omitempty"`
	Invalidations []Invalidation       `json:"invalidations,omitempty"`
}

// AcceptsNewWork 报告此刻能否接新任务：四维都就绪、管理策略接收、还有空槽与组余量。
func (s Snapshot) AcceptsNewWork() bool {
	return !s.Stale &&
		s.Admission == AdmissionOpen &&
		s.EffectiveCapacity == model.ReadyAvailable &&
		s.Status.Entry == model.EntryReady &&
		s.Status.Auth == model.ReadyOK &&
		s.Status.Connection == model.ReadyFresh &&
		s.FreeSlots > 0
}

// Capability 返回指定能力的证据与是否可用于自动匹配。
func (s Snapshot) Capability(name string, now time.Time, ttl time.Duration) (CapabilityEvidence, bool) {
	for _, c := range s.Capabilities {
		if c.Name == name {
			return c, c.Matchable(now, ttl)
		}
	}
	return CapabilityEvidence{Name: name, Declared: model.CapabilityUnknown, Verified: model.CapabilityUnknown}, false
}

// UnmatchedCapabilities 返回要求中既无证据也不可自动匹配的能力名。
func (s Snapshot) UnmatchedCapabilities(required []string, now time.Time, ttl time.Duration) []string {
	var missing []string
	for _, name := range required {
		if _, ok := s.Capability(name, now, ttl); !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// fingerprint 是入口侧身份的指纹：版本之外的入口变化同样会使证据失效。
func fingerprint(cfg model.NodeConfig) string {
	return fmt.Sprintf("%s|%s|%s|%s", cfg.Adapter, cfg.EntryPath, cfg.RuntimePath, cfg.Platform)
}
