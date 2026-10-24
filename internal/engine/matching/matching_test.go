package matching

import (
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

var testNow = time.Date(2026, 10, 24, 8, 0, 0, 0, time.UTC)

func observed(name string) Capability {
	return Capability{
		Name:        name,
		State:       model.CapabilityObserved,
		Version:     model.SemVer("2.173.0"),
		EvidenceRef: "docs/evidence/codebuddy",
		VerifiedAt:  testNow,
	}
}

func readyNode(id string, caps ...Capability) Node {
	return Node{
		ID: model.NodeID(id),
		Config: model.NodeConfig{
			NodeID:     model.NodeID(id),
			Mode:       model.NodeManagedCLI,
			SlotLimit:  1,
			AcceptNew:  true,
			Adapter:    model.AdapterCodeBuddy,
			EntryPath:  "/usr/local/bin/codebuddy",
			ProfileRef: "profile/default",
			Platform:   "darwin",
		},
		Entry:        model.EntryReady,
		Auth:         model.ReadyOK,
		Connection:   model.ReadyFresh,
		Capacity:     model.ReadyAvailable,
		ObservedAt:   testNow,
		ActiveSlots:  0,
		Capabilities: caps,
	}
}

func readyTask(id string, priority int) Task {
	return Task{
		ID:         id,
		Status:     model.TaskReady,
		Priority:   priority,
		MergeOrder: priority,
	}
}

func autoPolicy(limit int) Policy {
	return Policy{AutoExecution: model.AutoExecution{Enabled: true, ManagedExecutionLimit: limit}}
}

func mustMatch(t *testing.T, req Request) Decision {
	t.Helper()
	dec, err := Match(req)
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	return dec
}

func hasReason(dec Decision, code ReasonCode, taskID string) (QueueReason, bool) {
	for _, r := range dec.Reasons {
		if r.Code == code && (taskID == "" || r.TaskID == taskID) {
			return r, true
		}
	}
	return QueueReason{}, false
}

func TestAutoDisabledByDefault(t *testing.T) {
	req := Request{
		Tasks: []Task{readyTask("VN-2", 10)},
		Nodes: []Node{readyNode("node-1")},
		Now:   testNow,
	}
	dec := mustMatch(t, req)
	if dec.Matched {
		t.Fatalf("零值策略不得自动选题，得到 %+v", dec)
	}
	if len(dec.Reasons) != 1 || dec.Reasons[0].Code != ReasonAutoDisabled {
		t.Fatalf("默认关闭必须给出 auto_disabled 原因，得到 %+v", dec.Reasons)
	}

	manual := PickManual(req.Tasks)
	if !manual.Matched || manual.TaskID != "VN-2" || manual.Mode != ModeManual {
		t.Fatalf("人工领取不受自动开关影响，得到 %+v", manual)
	}
}

func TestAutoRequiresManagedLimit(t *testing.T) {
	req := Request{
		Policy: Policy{AutoExecution: model.AutoExecution{Enabled: true}},
		Tasks:  []Task{readyTask("VN-2", 10)},
		Nodes:  []Node{readyNode("node-1")},
		Now:    testNow,
	}
	_, err := Match(req)
	if err == nil {
		t.Fatal("开启自动执行但未给上限必须报 E270")
	}
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.CodeAutoExecutionLimit {
		t.Fatalf("期望 E270，得到 %v", err)
	}
}

func TestSelectionStableForEqualCandidates(t *testing.T) {
	tasks := []Task{readyTask("VN-b", 10), readyTask("VN-a", 10)}
	nodes := []Node{readyNode("node-b"), readyNode("node-a")}
	for i := 0; i < 20; i++ {
		dec := mustMatch(t, Request{Policy: autoPolicy(4), Tasks: tasks, Nodes: nodes, Now: testNow})
		if !dec.Matched || dec.TaskID != "VN-a" || dec.NodeID != "node-a" {
			t.Fatalf("第 %d 次结果不稳定: %+v", i, dec)
		}
	}
	// 反转输入顺序，结果必须仍然是编号最小者。
	revTasks := []Task{tasks[1], tasks[0]}
	revNodes := []Node{nodes[1], nodes[0]}
	dec := mustMatch(t, Request{Policy: autoPolicy(4), Tasks: revTasks, Nodes: revNodes, Now: testNow})
	if dec.TaskID != "VN-a" || dec.NodeID != "node-a" {
		t.Fatalf("输入顺序不应影响结果: %+v", dec)
	}
}

func TestCapabilitySelectsMatchingNode(t *testing.T) {
	task := readyTask("VN-2", 10)
	task.RequiredCapabilities = []string{"usage.metrics"}
	nodes := []Node{
		readyNode("node-a", observed("print.headless_mode")),
		readyNode("node-b", observed("usage.metrics")),
	}
	dec := mustMatch(t, Request{Policy: autoPolicy(2), Tasks: []Task{task}, Nodes: nodes, Now: testNow})
	if !dec.Matched || dec.NodeID != "node-b" {
		t.Fatalf("必须选中具备要求能力的节点，得到 %+v", dec)
	}
}

func TestUnknownCapabilityHasQueueReason(t *testing.T) {
	cases := map[string]Capability{
		"unobserved": {
			Name:        "usage.metrics",
			State:       model.CapabilityUnobserved,
			Version:     "2.173.0",
			EvidenceRef: "docs/evidence/codebuddy",
			VerifiedAt:  testNow,
		},
		"unknown": {
			Name:        "usage.metrics",
			State:       model.CapabilityUnknown,
			Version:     "2.173.0",
			EvidenceRef: "docs/evidence/codebuddy",
			VerifiedAt:  testNow,
		},
		"no-evidence": {
			Name:       "usage.metrics",
			State:      model.CapabilityObserved,
			Version:    "2.173.0",
			VerifiedAt: testNow,
		},
		"expired": {
			Name:        "usage.metrics",
			State:       model.CapabilityObserved,
			Version:     "2.173.0",
			EvidenceRef: "docs/evidence/codebuddy",
			VerifiedAt:  testNow.Add(-10 * time.Minute),
		},
	}
	for name, cap := range cases {
		t.Run(name, func(t *testing.T) {
			task := readyTask("VN-2", 10)
			task.RequiredCapabilities = []string{"usage.metrics"}
			dec := mustMatch(t, Request{
				Policy: autoPolicy(2),
				Tasks:  []Task{task},
				Nodes:  []Node{readyNode("node-1", cap)},
				Now:    testNow,
			})
			if dec.Matched {
				t.Fatalf("不可匹配的能力不得被选中: %+v", dec)
			}
			reason, ok := hasReason(dec, ReasonCapabilityUnknown, "VN-2")
			if !ok {
				t.Fatalf("缺少 capability_unknown 原因: %+v", dec.Reasons)
			}
			if len(reason.MissingCapabilities) != 1 || reason.MissingCapabilities[0] != "usage.metrics" {
				t.Fatalf("缺少能力名: %+v", reason)
			}
		})
	}
}

func TestCapacityAndAdmissionReasons(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Node)
		code   ReasonCode
	}{
		{"slot-occupied", func(n *Node) { n.ActiveSlots = 1 }, ReasonNodeOccupied},
		{"capacity-occupied", func(n *Node) { n.Capacity = model.ReadyOccupied }, ReasonNodeOccupied},
		{"draining", func(n *Node) { n.Draining = true }, ReasonNodeDraining},
		{"disabled", func(n *Node) { n.Config.AcceptNew = false }, ReasonNodeDisabled},
		{"stale", func(n *Node) { n.ObservedAt = time.Time{} }, ReasonNodeStale},
		{"needs-login", func(n *Node) { n.Auth = model.ReadyNeedsLogin }, ReasonNodeNotReady},
		{"disconnected", func(n *Node) { n.Connection = model.ReadyDisconnected }, ReasonNodeNotReady},
		{"entry-missing", func(n *Node) { n.Entry = model.EntryMissing }, ReasonNodeNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := readyNode("node-1")
			tc.mutate(&node)
			dec := mustMatch(t, Request{
				Policy: autoPolicy(2),
				Tasks:  []Task{readyTask("VN-2", 10)},
				Nodes:  []Node{node},
				Now:    testNow,
			})
			if dec.Matched {
				t.Fatalf("不可用节点不得被选中: %+v", dec)
			}
			if _, ok := hasReason(dec, tc.code, "VN-2"); !ok {
				t.Fatalf("缺少 %s 原因: %+v", tc.code, dec.Reasons)
			}
		})
	}
}

func TestSharedGroupCapacityIsExplicit(t *testing.T) {
	req := Request{
		Policy: autoPolicy(2),
		Tasks:  []Task{readyTask("VN-2", 10)},
		Nodes:  []Node{readyNode("node-1")},
		Now:    testNow,
	}
	req.Nodes[0].SharedGroup = "shared-account"
	_, err := Match(req)
	if err == nil {
		t.Fatal("声明共享资源组却没有组上限必须被拒绝")
	}
	var me *model.Error
	if !errors.As(err, &me) || me.Code != model.CodePortFieldInvalid {
		t.Fatalf("期望 P100，得到 %v", err)
	}

	node := readyNode("node-1")
	node.SharedGroup = "shared-account"
	node.GroupLimit = 2
	node.GroupActive = 2
	dec := mustMatch(t, Request{Policy: autoPolicy(2), Tasks: []Task{readyTask("VN-2", 10)}, Nodes: []Node{node}, Now: testNow})
	if _, ok := hasReason(dec, ReasonNodeOccupied, "VN-2"); !ok {
		t.Fatalf("共享组占满必须给出 node_occupied: %+v", dec.Reasons)
	}
}

func TestDependencyAndWriteConflictReasons(t *testing.T) {
	waiting := readyTask("VN-b", 10)
	waiting.Deps = []string{"VN-a"}
	done := readyTask("VN-z", 5)
	done.Status = model.TaskDone
	dec := mustMatch(t, Request{
		Policy: autoPolicy(2),
		Tasks:  []Task{done, waiting},
		Nodes:  []Node{readyNode("node-1")},
		Now:    testNow,
	})
	if dec.Matched {
		t.Fatalf("依赖未完成的任务不得被选中: %+v", dec)
	}
	reason, ok := hasReason(dec, ReasonDependencyUnmet, "VN-b")
	if !ok || reason.Detail == "" {
		t.Fatalf("缺少 dependency_unmet: %+v", dec.Reasons)
	}

	first := readyTask("VN-a", 10)
	first.WriteScope = []string{"internal/engine/**"}
	second := readyTask("VN-b", 20)
	second.WriteScope = []string{"internal/engine/matching/**"}
	dec = mustMatch(t, Request{
		Policy: autoPolicy(2),
		Tasks:  []Task{second, first},
		Nodes:  []Node{readyNode("node-1")},
		Now:    testNow,
	})
	if !dec.Matched || dec.TaskID != "VN-a" {
		t.Fatalf("排序更靠前的写盘任务应先被选中: %+v", dec)
	}
	if _, ok := hasReason(dec, ReasonWriteConflict, "VN-b"); !ok {
		t.Fatalf("写盘重叠必须给出 write_conflict: %+v", dec.Reasons)
	}
}

func TestManualIgnoresManagedExecutionLimit(t *testing.T) {
	req := Request{
		Policy:        autoPolicy(1),
		Tasks:         []Task{readyTask("VN-2", 10)},
		Nodes:         []Node{readyNode("node-1")},
		ActiveManaged: 1,
		Now:           testNow,
	}
	auto := mustMatch(t, req)
	if auto.Matched {
		t.Fatalf("达到受管上限时自动匹配必须停下: %+v", auto)
	}
	if _, ok := hasReason(auto, ReasonManagedLimit, ""); !ok {
		t.Fatalf("缺少 managed_execution_limit: %+v", auto.Reasons)
	}

	manual := PickManual(req.Tasks)
	if !manual.Matched || manual.TaskID != "VN-2" {
		t.Fatalf("人工 next 不受受管执行上限约束: %+v", manual)
	}
}

func TestProjectPolicyRestrictsAndOrdersNodes(t *testing.T) {
	project := model.ProjectID("0123456789abcdef")
	task := readyTask("VN-2", 10)
	task.ProjectID = project
	policy := autoPolicy(2)
	policy.Projects = []ProjectRule{{
		ProjectID:      project,
		AllowedNodes:   []model.NodeID{"node-b"},
		PreferredNodes: []model.NodeID{"node-b"},
	}}
	nodes := []Node{readyNode("node-a"), readyNode("node-b")}
	dec := mustMatch(t, Request{Policy: policy, Tasks: []Task{task}, Nodes: nodes, Now: testNow})
	if !dec.Matched || dec.NodeID != "node-b" {
		t.Fatalf("工程策略必须限定候选节点: %+v", dec)
	}

	policy.Projects[0].AllowedNodes = []model.NodeID{"node-c"}
	dec = mustMatch(t, Request{Policy: policy, Tasks: []Task{task}, Nodes: nodes, Now: testNow})
	if dec.Matched {
		t.Fatalf("未允许的节点不得被选中: %+v", dec)
	}
	if _, ok := hasReason(dec, ReasonProjectPolicy, "VN-2"); !ok {
		t.Fatalf("缺少 project_policy 原因: %+v", dec.Reasons)
	}
}

func TestNotReadyAndQueuedBehindReasons(t *testing.T) {
	running := readyTask("VN-a", 10)
	running.Status = model.TaskInProgress
	ready := readyTask("VN-b", 20)
	dec := mustMatch(t, Request{
		Policy: autoPolicy(2),
		Tasks:  []Task{running, ready},
		Nodes:  []Node{readyNode("node-1")},
		Now:    testNow,
	})
	if !dec.Matched || dec.TaskID != "VN-b" {
		t.Fatalf("只应选中 ready 的任务: %+v", dec)
	}
	if _, ok := hasReason(dec, ReasonTaskNotReady, "VN-a"); !ok {
		t.Fatalf("缺少 task_not_ready 原因: %+v", dec.Reasons)
	}

	other := readyTask("VN-c", 30)
	dec = mustMatch(t, Request{
		Policy: autoPolicy(2),
		Tasks:  []Task{ready, other},
		Nodes:  []Node{readyNode("node-1")},
		Now:    testNow,
	})
	if !dec.Matched || dec.TaskID != "VN-b" {
		t.Fatalf("同轮只选一题: %+v", dec)
	}
	if _, ok := hasReason(dec, ReasonQueuedBehind, "VN-c"); !ok {
		t.Fatalf("缺少 queued_behind 原因: %+v", dec.Reasons)
	}
}

func TestNoNodesHasQueueReason(t *testing.T) {
	dec := mustMatch(t, Request{
		Policy: autoPolicy(2),
		Tasks:  []Task{readyTask("VN-2", 10)},
		Now:    testNow,
	})
	if dec.Matched {
		t.Fatalf("没有节点不得匹配: %+v", dec)
	}
	if _, ok := hasReason(dec, ReasonNoCandidateNode, "VN-2"); !ok {
		t.Fatalf("缺少 no_candidate_node 原因: %+v", dec.Reasons)
	}
}
