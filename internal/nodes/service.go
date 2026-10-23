package nodes

import (
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// ErrRevisionMismatch 表示条件写入基于旧 revision，必须重新读取后再试。
var ErrRevisionMismatch = errors.New("nodes: 节点 revision 不匹配（已有并发修改）")

// DefaultProbeTTL 是探测结果的默认时效；超过后不再视为「永久可用」。
const DefaultProbeTTL = 5 * time.Minute

// Option 定制 Service。
type Option func(*Service)

// WithClock 注入时钟（测试用）。
func WithClock(fn func() time.Time) Option {
	return func(s *Service) {
		if fn != nil {
			s.now = fn
		}
	}
}

// WithProbeTTL 设置探测时效。
func WithProbeTTL(d time.Duration) Option {
	return func(s *Service) { s.ttl = d }
}

// Service 是节点登记与就绪模型的应用服务。
type Service struct {
	mu    sync.Mutex
	store Store
	now   func() time.Time
	ttl   time.Duration
}

// New 创建服务。
func New(store Store, opts ...Option) *Service {
	s := &Service{store: store, now: time.Now, ttl: DefaultProbeTTL}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Register 登记一个新节点。已存在返回 ErrExists；配置不完整返回 Violations。
func (s *Service) Register(reg Registration) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if v := validateRegistration(reg); len(v) > 0 {
		return Snapshot{}, v
	}
	if _, err := s.store.Load(reg.Config.NodeID); err == nil {
		return Snapshot{}, ErrExists
	} else if !errors.Is(err, ErrNotFound) {
		return Snapshot{}, err
	}
	if err := s.checkGroup(reg.SharedGroup, reg.GroupLimit, ""); err != nil {
		return Snapshot{}, err
	}

	now := s.now()
	rec := &Record{
		NodeID:      reg.Config.NodeID,
		ProjectID:   reg.ProjectID,
		SharedGroup: reg.SharedGroup,
		GroupLimit:  reg.GroupLimit,
		Config:      reg.Config,
		Revision:    1,
		Status:      UnknownStatus(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// UpdateConfig 更新已有节点的配置；入口、运行时、适配器或平台变化会使能力证据失效。
func (s *Service) UpdateConfig(id model.NodeID, expected model.Revision, reg Registration) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := checkRevision(rec, expected); err != nil {
		return Snapshot{}, err
	}
	if reg.Config.NodeID != id {
		return Snapshot{}, Violations{model.Violation(model.CodePortFieldInvalid, "node.node_id", "更新配置不得更换 node_id")}
	}
	if v := validateRegistration(reg); len(v) > 0 {
		return Snapshot{}, v
	}
	if err := s.checkGroup(reg.SharedGroup, reg.GroupLimit, id); err != nil {
		return Snapshot{}, err
	}

	now := s.now()
	if rec.EntryFingerprint != "" && rec.EntryFingerprint != fingerprint(reg.Config) {
		s.invalidate(rec, InvalidateEntryChanged, now)
	}
	rec.ProjectID = reg.ProjectID
	rec.SharedGroup = reg.SharedGroup
	rec.GroupLimit = reg.GroupLimit
	rec.Config = reg.Config
	rec.Revision++
	rec.UpdatedAt = now
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// Probe 应用一次探测结果：更新四维状态与能力证据，并在版本／入口变化时使旧证据失效。
func (s *Service) Probe(id model.NodeID, expected model.Revision, p model.ProbeResult) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := checkRevision(rec, expected); err != nil {
		return Snapshot{}, err
	}
	if v := p.Validate(); len(v) > 0 {
		return Snapshot{}, Violations(v)
	}

	now := s.now()
	if rec.CLIVersion != "" && model.SemVer(p.Version) != rec.CLIVersion {
		s.invalidate(rec, InvalidateVersionChanged, now)
	}
	if rec.EntryFingerprint != "" && rec.EntryFingerprint != fingerprint(rec.Config) {
		s.invalidate(rec, InvalidateEntryChanged, now)
	}
	if p.EntryState != model.EntryReady {
		s.invalidate(rec, InvalidateEntryUnavailable, now)
	}

	rec.Capabilities = mergeCapabilities(rec.Capabilities, p.Capabilities, now)
	rec.CLIVersion = model.SemVer(p.Version)
	rec.EntryFingerprint = fingerprint(rec.Config)
	rec.Status = Status{
		Entry:      p.EntryState,
		Auth:       p.Readiness.Auth,
		Connection: p.Readiness.Connection,
		Capacity:   p.Readiness.Capacity,
		ObservedAt: now,
		ProbeRef:   p.EvidenceRef,
	}
	rec.Revision++
	rec.UpdatedAt = now
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// Heartbeat 只刷新连接与心跳维度，不延长探测时效，也不改变认证结论。
//
// expected=0 表示不校验 revision（心跳允许被更新的探测覆盖）。
func (s *Service) Heartbeat(id model.NodeID, expected model.Revision) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if expected != 0 && rec.Revision != expected {
		return Snapshot{}, ErrRevisionMismatch
	}
	now := s.now()
	rec.Status.Connection = model.ReadyFresh
	rec.Revision++
	rec.UpdatedAt = now
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// SetAdmission 切换管理侧接收策略。draining 只停止接新任务，不改变占用与已有执行。
func (s *Service) SetAdmission(id model.NodeID, expected model.Revision, a Admission) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !a.Valid() {
		return Snapshot{}, Violations{model.Violationf(model.CodePortFieldInvalid, "node.admission", "未知接收策略 %q", a)}
	}
	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := checkRevision(rec, expected); err != nil {
		return Snapshot{}, err
	}
	switch a {
	case AdmissionOpen:
		rec.Config.AcceptNew = true
		rec.Draining = false
	case AdmissionDraining:
		rec.Config.AcceptNew = true
		rec.Draining = true
	case AdmissionDisabled:
		rec.Config.AcceptNew = false
		rec.Draining = false
	}
	now := s.now()
	rec.Revision++
	rec.UpdatedAt = now
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// SetSlotLimit 调整节点槽上限；不允许缩到低于当前占用。
func (s *Service) SetSlotLimit(id model.NodeID, expected model.Revision, limit int) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := checkRevision(rec, expected); err != nil {
		return Snapshot{}, err
	}
	if limit < 0 {
		return Snapshot{}, Violations{model.Violation(model.CodePortFieldInvalid, "node.slot_limit", "槽上限不得为负")}
	}
	if limit < rec.ActiveSlots {
		return Snapshot{}, Violations{model.Violationf(model.CodePortFieldInvalid, "node.slot_limit",
			"槽上限 %d 低于当前占用 %d，先等运行结束再缩容", limit, rec.ActiveSlots)}
	}
	if rec.Config.Mode == model.NodeManagedCLI && limit < 1 {
		return Snapshot{}, Violations{model.Violation(model.CodePortFieldInvalid, "node.slot_limit", "受管节点至少一个槽")}
	}
	rec.Config.SlotLimit = limit
	now := s.now()
	rec.Revision++
	rec.UpdatedAt = now
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// ObserveSlots 记录节点当前占用槽数（来自租约投影，不是账号额度）。
func (s *Service) ObserveSlots(id model.NodeID, expected model.Revision, active int) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	if err := checkRevision(rec, expected); err != nil {
		return Snapshot{}, err
	}
	if active < 0 || active > rec.Config.SlotLimit {
		return Snapshot{}, Violations{model.Violationf(model.CodePortFieldInvalid, "node.active_slots",
			"占用 %d 不在 0..%d 之间", active, rec.Config.SlotLimit)}
	}
	rec.ActiveSlots = active
	now := s.now()
	rec.Revision++
	rec.UpdatedAt = now
	if err := s.store.Save(rec); err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, now)
}

// Get 返回节点只读投影。
func (s *Service) Get(id model.NodeID) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, err := s.load(id)
	if err != nil {
		return Snapshot{}, err
	}
	return s.view(rec, s.now())
}

// Dispatchable 报告节点能否自动接活：四维就绪、管理策略接收、还有余量，
// 且要求的每项能力都有本版本、未过期的 observed 证据。返回缺失能力供解释。
func (s *Service) Dispatchable(id model.NodeID, required []string) (bool, []string, error) {
	snap, err := s.Get(id)
	if err != nil {
		return false, nil, err
	}
	now := s.now()
	missing := snap.UnmatchedCapabilities(required, now, s.ttl)
	return snap.AcceptsNewWork() && len(missing) == 0, missing, nil
}

// List 返回全部节点投影，按 node_id 升序。
func (s *Service) List() ([]Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	recs, err := s.store.List()
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := make([]Snapshot, 0, len(recs))
	for _, rec := range recs {
		snap, err := s.view(rec, now)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

// view 把记录投影成快照：叠加探测时效降级与共享组容量。
func (s *Service) view(rec *Record, now time.Time) (Snapshot, error) {
	all, err := s.store.List()
	if err != nil {
		return Snapshot{}, err
	}
	groupActive := 0
	if rec.SharedGroup != "" {
		for _, other := range all {
			if other.SharedGroup == rec.SharedGroup {
				groupActive += other.ActiveSlots
			}
		}
	}
	free := rec.Config.SlotLimit - rec.ActiveSlots
	if free < 0 {
		free = 0
	}
	stale := rec.Status.ObservedAt.IsZero() || (s.ttl > 0 && now.Sub(rec.Status.ObservedAt) > s.ttl)
	age := time.Duration(0)
	if !rec.Status.ObservedAt.IsZero() {
		age = now.Sub(rec.Status.ObservedAt)
	}
	return Snapshot{
		NodeID:            rec.NodeID,
		ProjectID:         rec.ProjectID,
		SharedGroup:       rec.SharedGroup,
		GroupLimit:        rec.GroupLimit,
		Config:            rec.Config,
		Revision:          rec.Revision,
		Status:            rec.Status,
		Admission:         rec.Admission(),
		EffectiveCapacity: effectiveCapacity(rec, stale, free, groupActive),
		ActiveSlots:       rec.ActiveSlots,
		FreeSlots:         free,
		GroupActive:       groupActive,
		ProbeAge:          age,
		Stale:             stale,
		Capabilities:      append([]CapabilityEvidence(nil), rec.Capabilities...),
		Invalidations:     append([]Invalidation(nil), rec.Invalidations...),
	}, nil
}

func effectiveCapacity(rec *Record, stale bool, free, groupActive int) model.ReadinessState {
	switch rec.Admission() {
	case AdmissionDisabled:
		return model.ReadyDisabled
	case AdmissionDraining:
		return model.ReadyDraining
	}
	if stale {
		return model.ReadyUnknown
	}
	switch rec.Status.Capacity {
	case model.ReadyAvailable:
		if free <= 0 {
			return model.ReadyOccupied
		}
		if rec.SharedGroup != "" && rec.GroupLimit > 0 && groupActive >= rec.GroupLimit {
			return model.ReadyOccupied
		}
		return model.ReadyAvailable
	case model.ReadyOccupied:
		return model.ReadyOccupied
	default:
		return model.ReadyUnknown
	}
}

func (s *Service) load(id model.NodeID) (*Record, error) {
	if !id.Valid() {
		return nil, Violations{model.Violationf(model.CodePortFieldInvalid, "node.node_id", "非法节点标识 %q", id)}
	}
	return s.store.Load(id)
}

func checkRevision(rec *Record, expected model.Revision) error {
	if expected != 0 && rec.Revision != expected {
		return ErrRevisionMismatch
	}
	return nil
}

// checkGroup 要求共享资源组显式且组内上限一致；self 为空表示新登记。
func (s *Service) checkGroup(group string, limit int, self model.NodeID) error {
	if group == "" {
		return nil
	}
	recs, err := s.store.List()
	if err != nil {
		return err
	}
	for _, other := range recs {
		if other.NodeID == self || other.SharedGroup != group {
			continue
		}
		if other.GroupLimit != limit {
			return Violations{model.Violationf(model.CodePortFieldInvalid, "node.shared_group",
				"共享资源组 %q 的组上限不一致：已有 %d，收到 %d", group, other.GroupLimit, limit)}
		}
	}
	return nil
}

// invalidate 把旧的 observed 证据降级为 unobserved，并登记失效原因。
func (s *Service) invalidate(rec *Record, reason string, now time.Time) {
	var names []string
	for i := range rec.Capabilities {
		if rec.Capabilities[i].Verified == model.CapabilityObserved {
			rec.Capabilities[i].Verified = model.CapabilityUnobserved
			rec.Capabilities[i].VerifiedAt = time.Time{}
			names = append(names, rec.Capabilities[i].Name)
		}
	}
	if len(names) == 0 {
		return
	}
	rec.Invalidations = append(rec.Invalidations, Invalidation{
		Reason:       reason,
		FromVersion:  rec.CLIVersion,
		Capabilities: names,
		At:           now,
	})
}

// mergeCapabilities 用新的探测值覆盖同名能力；声明值只保留已知部分，不被探测改写。
func mergeCapabilities(old []CapabilityEvidence, next []model.Capability, now time.Time) []CapabilityEvidence {
	byName := make(map[string]CapabilityEvidence, len(old))
	for _, c := range old {
		byName[c.Name] = c
	}
	out := make([]CapabilityEvidence, 0, len(next))
	for _, c := range next {
		ev := CapabilityEvidence{
			Name:        c.Name,
			Declared:    c.State,
			Verified:    c.State,
			Version:     c.Version,
			EvidenceRef: c.EvidenceRef,
		}
		if prev, ok := byName[c.Name]; ok && prev.Declared != model.CapabilityUnknown {
			ev.Declared = prev.Declared
		}
		if c.State == model.CapabilityObserved {
			ev.VerifiedAt = now
		}
		out = append(out, ev)
		delete(byName, c.Name)
	}
	// 本次探测没报告的能力不保留成 observed：没有证据就不能参与自动匹配。
	rest := make([]string, 0, len(byName))
	for name := range byName {
		rest = append(rest, name)
	}
	sort.Strings(rest)
	for _, name := range rest {
		ev := byName[name]
		if ev.Verified == model.CapabilityObserved {
			ev.Verified = model.CapabilityUnobserved
			ev.VerifiedAt = time.Time{}
		}
		out = append(out, ev)
	}
	return out
}

func validateRegistration(reg Registration) Violations {
	var out Violations
	if errs := reg.Config.Validate(); len(errs) > 0 {
		out = append(out, errs...)
	}
	group := strings.TrimSpace(reg.SharedGroup)
	if group != reg.SharedGroup {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "node.shared_group", "共享资源组名不能带首尾空白"))
	}
	if reg.SharedGroup != "" && reg.GroupLimit < 1 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "node.group_limit", "声明共享资源组时必须给 >=1 的组上限"))
	}
	if reg.SharedGroup == "" && reg.GroupLimit != 0 {
		out = append(out, model.Violation(model.CodePortFieldInvalid, "node.group_limit", "未声明共享资源组时不能给组上限"))
	}
	return out
}
