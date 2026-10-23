package nodes

import (
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func mustOk(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustFail(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func nodeConfig(id string, slotLimit int) Registration {
	return Registration{Config: model.NodeConfig{
		NodeID:     model.NodeID(id),
		Name:       "local " + id,
		Adapter:    model.AdapterCodeBuddy,
		EntryPath:  "/usr/local/bin/codebuddy",
		Mode:       model.NodeManagedCLI,
		Platform:   "darwin",
		SlotLimit:  slotLimit,
		AcceptNew:  true,
		ProfileRef: "profile/default",
		Timeout:    5 * time.Minute,
	}}
}

func readyProbe(version string, caps ...model.Capability) model.ProbeResult {
	return model.ProbeResult{
		EntryState:   model.EntryReady,
		Version:      version,
		Readiness:    model.Readiness{Auth: model.ReadyOK, Connection: model.ReadyFresh, Capacity: model.ReadyAvailable},
		Capabilities: caps,
		EvidenceRef:  "docs/evidence/codebuddy",
	}
}

func observedCap(name, version string) model.Capability {
	return model.Capability{Name: name, State: model.CapabilityObserved, Version: model.SemVer(version), EvidenceRef: "docs/evidence/codebuddy"}
}

// registerReady 登记并完成一次全就绪探测。
func registerReady(t *testing.T, svc *Service, reg Registration) Snapshot {
	t.Helper()
	snap, err := svc.Register(reg)
	mustOk(t, err)
	snap, err = svc.Probe(reg.Config.NodeID, snap.Revision, readyProbe("2.173.0", observedCap("print.headless_mode", "2.173.0")))
	mustOk(t, err)
	return snap
}

func TestRegisterHasUnknownReadiness(t *testing.T) {
	svc := New(NewMemoryStore())
	snap, err := svc.Register(nodeConfig("mbp-codebuddy", 2))
	mustOk(t, err)
	if snap.NodeID != "mbp-codebuddy" {
		t.Fatalf("node id=%q", snap.NodeID)
	}
	if snap.FreeSlots != 2 || snap.ActiveSlots != 0 {
		t.Fatalf("slots active=%d free=%d, want 0/2", snap.ActiveSlots, snap.FreeSlots)
	}
	if !snap.Stale || snap.EffectiveCapacity != model.ReadyUnknown {
		t.Fatalf("未探测的节点必须保持 unknown 且视为过期: stale=%v capacity=%q", snap.Stale, snap.EffectiveCapacity)
	}
	if snap.AcceptsNewWork() {
		t.Fatal("未探测的节点不能自动接新任务")
	}
	if snap.Status.Entry != model.EntryUnknown || snap.Status.Auth != model.ReadyUnknown ||
		snap.Status.Connection != model.ReadyUnknown || snap.Status.Capacity != model.ReadyUnknown {
		t.Fatalf("四维初始状态应全部 unknown: %+v", snap.Status)
	}
}

func TestRegisterRejectsInvalidConfig(t *testing.T) {
	svc := New(NewMemoryStore())
	reg := nodeConfig("mbp-codebuddy", 0) // 受管节点至少一个槽
	_, err := svc.Register(reg)
	mustFail(t, err)

	dup := nodeConfig("mbp-codebuddy", 1)
	_, err = svc.Register(dup)
	mustOk(t, err)
	if _, err := svc.Register(dup); !errors.Is(err, ErrExists) {
		t.Fatalf("重复登记应返回 ErrExists，得到 %v", err)
	}
}

func TestFourDimensionsStaySeparate(t *testing.T) {
	svc := New(NewMemoryStore())
	snap, err := svc.Register(nodeConfig("mbp-codebuddy", 2))
	mustOk(t, err)

	probe := readyProbe("2.173.0", observedCap("print.headless_mode", "2.173.0"))
	probe.Readiness.Auth = model.ReadyNeedsLogin
	snap, err = svc.Probe("mbp-codebuddy", snap.Revision, probe)
	mustOk(t, err)
	if snap.Status.Entry != model.EntryReady || snap.Status.Auth != model.ReadyNeedsLogin ||
		snap.Status.Connection != model.ReadyFresh || snap.Status.Capacity != model.ReadyAvailable {
		t.Fatalf("四维应各自独立取值: %+v", snap.Status)
	}
	if snap.Admission != AdmissionOpen || snap.AcceptsNewWork() {
		t.Fatalf("认证未就绪时不能接新任务: admission=%q", snap.Admission)
	}

	probe.Readiness.Auth = model.ReadyOK
	snap, err = svc.Probe("mbp-codebuddy", snap.Revision, probe)
	mustOk(t, err)
	if !snap.AcceptsNewWork() {
		t.Fatalf("四维都就绪且有余量时应可接新任务: %+v", snap)
	}
}

func TestDrainingDoesNotInterruptRunning(t *testing.T) {
	svc := New(NewMemoryStore())
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))
	snap, err := svc.ObserveSlots("mbp-codebuddy", snap.Revision, 1)
	mustOk(t, err)

	drained, err := svc.SetAdmission("mbp-codebuddy", snap.Revision, AdmissionDraining)
	mustOk(t, err)
	if drained.Admission != AdmissionDraining || drained.AcceptsNewWork() {
		t.Fatalf("draining 不应接新任务: admission=%q", drained.Admission)
	}
	if drained.ActiveSlots != 1 || drained.FreeSlots != 1 {
		t.Fatalf("draining 不得打断已有执行: active=%d free=%d", drained.ActiveSlots, drained.FreeSlots)
	}
	if drained.Status.Connection != model.ReadyFresh || drained.EffectiveCapacity != model.ReadyDraining {
		t.Fatalf("连接维度不应被 draining 改写: connection=%q capacity=%q", drained.Status.Connection, drained.EffectiveCapacity)
	}

	disabled, err := svc.SetAdmission("mbp-codebuddy", drained.Revision, AdmissionDisabled)
	mustOk(t, err)
	if disabled.Admission != AdmissionDisabled || disabled.EffectiveCapacity != model.ReadyDisabled {
		t.Fatalf("disabled 是独立管理状态: admission=%q capacity=%q", disabled.Admission, disabled.EffectiveCapacity)
	}

	occupiedSnap := registerReady(t, svc, nodeConfig("mbp-cb2", 1))
	occupiedSnap, err = svc.ObserveSlots("mbp-cb2", occupiedSnap.Revision, 1)
	mustOk(t, err)
	if occupiedSnap.Admission != AdmissionOpen || occupiedSnap.EffectiveCapacity != model.ReadyOccupied {
		t.Fatalf("槽满应为 occupied，而不是 draining/disabled: admission=%q capacity=%q",
			occupiedSnap.Admission, occupiedSnap.EffectiveCapacity)
	}
}

func TestVersionChangeInvalidatesObservedCapabilities(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)}
	svc := New(NewMemoryStore(), WithClock(clock.now), WithProbeTTL(2*time.Hour))
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))

	if _, ok := snap.Capability("print.headless_mode", clock.now(), 2*time.Hour); !ok {
		t.Fatal("首次探测的 observed 能力应可匹配")
	}

	// 版本变化后该能力在本版本未观察到，旧证据必须失效。
	clock.advance(10 * time.Minute)
	next := readyProbe("2.174.0", model.Capability{
		Name: "print.headless_mode", State: model.CapabilityUnobserved,
		Version: "2.174.0", EvidenceRef: "docs/evidence/codebuddy",
	})
	snap, err := svc.Probe("mbp-codebuddy", snap.Revision, next)
	mustOk(t, err)
	if c, ok := snap.Capability("print.headless_mode", clock.now(), 2*time.Hour); ok || c.Version == "2.173.0" {
		t.Fatalf("版本变化后旧能力不得继续可匹配: %+v", c)
	}
	found := false
	for _, inv := range snap.Invalidations {
		if inv.Reason == InvalidateVersionChanged && len(inv.Capabilities) == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("应记录 version_changed 失效原因: %+v", snap.Invalidations)
	}
}

func TestEntryChangeInvalidatesObservedCapabilities(t *testing.T) {
	svc := New(NewMemoryStore())
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))

	reg := nodeConfig("mbp-codebuddy", 2)
	reg.Config.EntryPath = "/opt/other/codebuddy"
	snap, err := svc.UpdateConfig("mbp-codebuddy", snap.Revision, reg)
	mustOk(t, err)
	if _, ok := snap.Capability("print.headless_mode", time.Now(), time.Hour); ok {
		t.Fatal("入口变化后旧能力不得继续可匹配")
	}
	reasonFound := false
	for _, inv := range snap.Invalidations {
		if inv.Reason == InvalidateEntryChanged {
			reasonFound = true
		}
	}
	if !reasonFound {
		t.Fatalf("应记录 entry_changed 失效原因: %+v", snap.Invalidations)
	}
}

func TestUnknownAndUnobservedNeverMatch(t *testing.T) {
	svc := New(NewMemoryStore())
	snap, err := svc.Register(nodeConfig("mbp-codebuddy", 2))
	mustOk(t, err)
	probe := readyProbe("2.173.0",
		model.Capability{Name: "cancel", State: model.CapabilityUnknown, Version: "2.173.0", EvidenceRef: "e"},
		model.Capability{Name: "resume", State: model.CapabilityUnobserved, Version: "2.173.0", EvidenceRef: "e"},
	)
	snap, err = svc.Probe("mbp-codebuddy", snap.Revision, probe)
	mustOk(t, err)

	missing := snap.UnmatchedCapabilities([]string{"cancel", "resume", "print.headless_mode"}, time.Now(), time.Hour)
	if len(missing) != 3 {
		t.Fatalf("unknown/unobserved/缺失能力都不可匹配，missing=%v", missing)
	}
	if snap.AcceptsNewWork() != true {
		// 能力不参与「能否接活」，由派发方按 required 能力判定；这里只确认状态未被污染
		t.Fatalf("能力缺失不应改写四维状态: %+v", snap.Status)
	}
}

func TestProbeExpiry(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)}
	svc := New(NewMemoryStore(), WithClock(clock.now), WithProbeTTL(time.Hour))
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))
	if snap.Stale {
		t.Fatal("刚探测完不应过期")
	}

	clock.advance(2 * time.Hour)
	snap, err := svc.Get("mbp-codebuddy")
	mustOk(t, err)
	if !snap.Stale {
		t.Fatal("超过探测时效应标记过期")
	}
	if snap.AcceptsNewWork() || snap.EffectiveCapacity != model.ReadyUnknown {
		t.Fatalf("过期探测不得继续当作可用: capacity=%q accepts=%v", snap.EffectiveCapacity, snap.AcceptsNewWork())
	}
	if _, ok := snap.Capability("print.headless_mode", clock.now(), time.Hour); ok {
		t.Fatal("过期证据不得继续匹配")
	}
}

func TestHeartbeatDoesNotExtendProbeFreshness(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 23, 3, 0, 0, 0, time.UTC)}
	svc := New(NewMemoryStore(), WithClock(clock.now), WithProbeTTL(time.Hour))
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))

	clock.advance(90 * time.Minute)
	snap, err := svc.Heartbeat("mbp-codebuddy", 0)
	mustOk(t, err)
	if snap.Status.Connection != model.ReadyFresh {
		t.Fatalf("心跳应刷新连接维度: %q", snap.Status.Connection)
	}
	if !snap.Stale || snap.AcceptsNewWork() {
		t.Fatalf("心跳不能延长探测时效: stale=%v accepts=%v", snap.Stale, snap.AcceptsNewWork())
	}
}

func TestSharedGroupCapacity(t *testing.T) {
	svc := New(NewMemoryStore())
	a := nodeConfig("node-a", 2)
	a.SharedGroup, a.GroupLimit = "codebuddy-account", 3
	b := nodeConfig("node-b", 2)
	b.SharedGroup, b.GroupLimit = "codebuddy-account", 3
	snapA := registerReady(t, svc, a)
	snapB := registerReady(t, svc, b)

	if snapA.GroupActive != 0 || snapB.GroupActive != 0 {
		t.Fatalf("无占用时组占用应为 0: a=%d b=%d", snapA.GroupActive, snapB.GroupActive)
	}

	snapA, err := svc.ObserveSlots("node-a", snapA.Revision, 1)
	mustOk(t, err)
	snapB, err = svc.ObserveSlots("node-b", snapB.Revision, 1)
	mustOk(t, err)
	view, _ := svc.Get("node-a")
	if view.GroupActive != 2 || view.EffectiveCapacity != model.ReadyAvailable {
		t.Fatalf("组 2/3 仍可接新: group=%d capacity=%q", view.GroupActive, view.EffectiveCapacity)
	}

	snapB, err = svc.ObserveSlots("node-b", snapB.Revision, 2)
	mustOk(t, err)
	view, _ = svc.Get("node-a")
	if view.GroupActive != 3 || view.EffectiveCapacity != model.ReadyOccupied || view.AcceptsNewWork() {
		t.Fatalf("共享组满后即使本节点有空槽也不得接新: group=%d capacity=%q", view.GroupActive, view.EffectiveCapacity)
	}
}

func TestGroupLimitMustAgree(t *testing.T) {
	svc := New(NewMemoryStore())
	a := nodeConfig("node-a", 1)
	a.SharedGroup, a.GroupLimit = "g", 2
	_, err := svc.Register(a)
	mustOk(t, err)

	b := nodeConfig("node-b", 1)
	b.SharedGroup, b.GroupLimit = "g", 3
	_, err = svc.Register(b)
	mustFail(t, err)

	c := nodeConfig("node-c", 1)
	c.GroupLimit = 2 // 未声明组却给组上限
	_, err = svc.Register(c)
	mustFail(t, err)
}

func TestSlotLimitChange(t *testing.T) {
	svc := New(NewMemoryStore())
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))
	snap, err := svc.ObserveSlots("mbp-codebuddy", snap.Revision, 2)
	mustOk(t, err)

	_, err = svc.SetSlotLimit("mbp-codebuddy", snap.Revision, 1)
	mustFail(t, err)

	snap, err = svc.SetSlotLimit("mbp-codebuddy", snap.Revision, 3)
	mustOk(t, err)
	if snap.FreeSlots != 1 || snap.EffectiveCapacity != model.ReadyAvailable {
		t.Fatalf("扩容后应有空槽: free=%d capacity=%q", snap.FreeSlots, snap.EffectiveCapacity)
	}
}

func TestRevisionConflict(t *testing.T) {
	svc := New(NewMemoryStore())
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))
	staleRev := snap.Revision
	if _, err := svc.ObserveSlots("mbp-codebuddy", staleRev, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ObserveSlots("mbp-codebuddy", staleRev, 2); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("旧 revision 写入必须冲突，得到 %v", err)
	}
}

func TestIdentityIsNodeNotAgent(t *testing.T) {
	// 节点记录只持有 NodeID 与可选归属工程；没有 Agent 名字段。
	// 非法节点标识（含大写/空格）直接拒绝，不能拿工作者名顶上。
	svc := New(NewMemoryStore())
	bad := nodeConfig("Mac Book", 1)
	_, err := svc.Register(bad)
	mustFail(t, err)

	_, err = svc.Get("cursor-demo-1") // 未登记的字符串不产生记录
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("未登记节点应返回 ErrNotFound，得到 %v", err)
	}

	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 1))
	var _ model.NodeID = snap.NodeID
	if snap.ProjectID != "" {
		t.Fatalf("节点不是工程 Agent，默认不带工程归属: %q", snap.ProjectID)
	}
}

func TestDispatchableRequiresReadyAndCapabilities(t *testing.T) {
	svc := New(NewMemoryStore())
	snap := registerReady(t, svc, nodeConfig("mbp-codebuddy", 2))

	ok, missing, err := svc.Dispatchable("mbp-codebuddy", []string{"print.headless_mode"})
	mustOk(t, err)
	if !ok || len(missing) != 0 {
		t.Fatalf("就绪且能力可匹配时应可派发: ok=%v missing=%v", ok, missing)
	}

	ok, missing, err = svc.Dispatchable("mbp-codebuddy", []string{"print.headless_mode", "cancel"})
	mustOk(t, err)
	if ok || len(missing) != 1 || missing[0] != "cancel" {
		t.Fatalf("未知能力不得自动派发: ok=%v missing=%v", ok, missing)
	}

	_, err = svc.SetAdmission("mbp-codebuddy", snap.Revision, AdmissionDraining)
	mustOk(t, err)
	ok, _, err = svc.Dispatchable("mbp-codebuddy", nil)
	mustOk(t, err)
	if ok {
		t.Fatal("draining 节点不得被自动派发")
	}
}

func TestListSorted(t *testing.T) {
	svc := New(NewMemoryStore())
	for _, id := range []string{"node-z", "node-b", "node-a", "node-m"} {
		if _, err := svc.Register(nodeConfig(id, 1)); err != nil {
			t.Fatal(err)
		}
	}
	list, err := svc.List()
	mustOk(t, err)
	want := []string{"node-a", "node-b", "node-m", "node-z"}
	for i := range want {
		if list[i].NodeID != model.NodeID(want[i]) {
			t.Fatalf("order[%d]=%q want %q", i, list[i].NodeID, want[i])
		}
	}
}
