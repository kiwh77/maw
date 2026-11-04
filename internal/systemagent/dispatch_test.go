package systemagent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/matching"
	"github.com/kiwh77/maw/internal/engine/model"
)

const dispatchTestProject = "0123456789abcdef"

func newDispatchStore(t *testing.T) *db.Store {
	t.Helper()
	st, err := db.OpenStore(filepath.Join(t.TempDir(), "maw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedDispatchReadyTask 造一道 ready 题与实例，写盘边界足够窄以便自动匹配。
func seedDispatchReadyTask(t *testing.T, st *db.Store, id string) model.InstanceID {
	t.Helper()
	var instID model.InstanceID
	err := st.Write(func(tx *sql.Tx) error {
		inst, err := db.CreateInstance(tx, dispatchTestProject, id, db.InstanceDraft{Title: id})
		if err != nil {
			return err
		}
		instID = inst.InstanceID
		return db.UpsertTask(tx, &db.Task{
			ProjectID:  dispatchTestProject,
			ID:         id,
			InstanceID: string(inst.InstanceID),
			Title:      id,
			Status:     string(model.TaskReady),
			Priority:   10,
			MergeOrder: 10,
			WriteScope: []string{"pkg/**"},
			Revision:   1,
		})
	})
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return instID
}

func registerDispatchNode(t *testing.T, st *db.Store, id string, slots int) {
	t.Helper()
	err := st.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: id, ProjectID: dispatchTestProject, SlotLimit: slots})
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
}

type fakeDispatchProjection struct {
	policy matching.Policy
	tasks  []matching.Task
	nodes  []matching.Node
	active int
}

func (f fakeDispatchProjection) Policy(context.Context, string) (matching.Policy, error) {
	return f.policy, nil
}

func (f fakeDispatchProjection) Tasks(context.Context, string) ([]matching.Task, error) {
	return f.tasks, nil
}

func (f fakeDispatchProjection) Nodes(context.Context, string) ([]matching.Node, error) {
	return f.nodes, nil
}

func (f fakeDispatchProjection) ActiveManaged(context.Context, string) (int, error) {
	return f.active, nil
}

type fakeDispatchAdvance struct {
	calls int
	step  DispatchStep
}

func (f *fakeDispatchAdvance) Advance(context.Context) (DispatchStep, error) {
	f.calls++
	return f.step, nil
}

func readyDispatchNode(id string, now time.Time) matching.Node {
	return matching.Node{
		ID:        model.NodeID(id),
		ProjectID: model.ProjectID(dispatchTestProject),
		Config: model.NodeConfig{
			NodeID:    model.NodeID(id),
			Mode:      model.NodeManagedCLI,
			SlotLimit: 2,
			AcceptNew: true,
		},
		Entry:      model.EntryReady,
		Auth:       model.ReadyOK,
		Connection: model.ReadyFresh,
		Capacity:   model.ReadyAvailable,
		ObservedAt: now,
	}
}

func readyDispatchTask(id string) matching.Task {
	return matching.Task{
		ID:         id,
		ProjectID:  model.ProjectID(dispatchTestProject),
		Status:     model.TaskReady,
		Priority:   10,
		MergeOrder: 10,
		WriteScope: []string{"pkg/**"},
	}
}

// TestDispatcherMatchClaimLaunchSpecStep 覆盖验收 1：ready 题 + 就绪节点会真的产生
// Run/Operation（非空转），并把 launch_spec 的执行结果带回来。
func TestDispatcherMatchClaimLaunchSpecStep(t *testing.T) {
	st := newDispatchStore(t)
	seedDispatchReadyTask(t, st, "T-1")
	registerDispatchNode(t, st, "node-a", 2)

	now := time.Now().UTC()
	adv := &fakeDispatchAdvance{step: DispatchStep{
		OperationID: "op-stepped",
		Action:      "launched",
		Progressed:  true,
		LaunchSpec:  true,
	}}
	d := &Dispatcher{
		Store: st,
		Projection: fakeDispatchProjection{
			policy: matching.Policy{AutoExecution: model.AutoExecution{Enabled: true, ManagedExecutionLimit: 2}},
			tasks:  []matching.Task{readyDispatchTask("T-1")},
			nodes:  []matching.Node{readyDispatchNode("node-a", now)},
		},
		Advance: adv,
		Workspace: func(_, taskID string) (string, error) {
			dir := filepath.Join(t.TempDir(), taskID)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", err
			}
			return dir, nil
		},
		AgentID: "system-demo-1",
		Clock:   func() time.Time { return now },
	}

	out, err := d.DispatchReady(context.Background(), DispatchRequest{
		ProjectID: dispatchTestProject,
		JobID:     "sysjob-000001",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !out.Matched || out.TaskID != "T-1" || out.NodeID != model.NodeID("node-a") {
		t.Fatalf("unexpected match: %+v", out)
	}
	if out.RunID == "" || out.OperationID == "" {
		t.Fatalf("未产生 Run/Operation: %+v", out)
	}
	if !out.Progressed || !out.LaunchSpec || out.Action != "launched" {
		t.Fatalf("未推进到 launch_spec: %+v", out)
	}
	if adv.calls != 1 {
		t.Fatalf("advance 调用次数 = %d", adv.calls)
	}

	err = st.Read(func(tx *sql.Tx) error {
		op, e := db.GetOperation(tx, out.OperationID)
		if e != nil {
			return e
		}
		if op == nil || op.Result != model.ResultPending {
			t.Fatalf("操作的 pending 语义不对: %+v", op)
		}
		run, e := db.GetRun(tx, out.RunID)
		if e != nil {
			return e
		}
		if run == nil {
			t.Fatalf("Run %s 不存在", out.RunID)
		}
		task, e := db.GetTask(tx, dispatchTestProject, "T-1")
		if e != nil {
			return e
		}
		if task == nil || task.Status != string(model.TaskClaimed) {
			t.Fatalf("任务状态 = %+v", task)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验落库: %v", err)
	}
}

// TestDispatcherDispatchReadyExplainsReasons 覆盖验收 2：无匹配时 job 结果带有可解释原因。
func TestDispatcherDispatchReadyExplainsReasons(t *testing.T) {
	cases := []struct {
		name   string
		proj   fakeDispatchProjection
		expect matching.ReasonCode
	}{
		{
			name: "auto off",
			proj: fakeDispatchProjection{
				policy: matching.Policy{AutoExecution: model.AutoExecution{ManagedExecutionLimit: 1}},
				tasks:  []matching.Task{readyDispatchTask("T-1")},
				nodes:  []matching.Node{readyDispatchNode("node-a", time.Now().UTC())},
			},
			expect: matching.ReasonAutoDisabled,
		},
		{
			name: "no candidate node",
			proj: fakeDispatchProjection{
				policy: matching.Policy{AutoExecution: model.AutoExecution{Enabled: true, ManagedExecutionLimit: 2}},
				tasks:  []matching.Task{readyDispatchTask("T-1")},
			},
			expect: matching.ReasonNoCandidateNode,
		},
		{
			name: "managed limit hit",
			proj: fakeDispatchProjection{
				policy: matching.Policy{AutoExecution: model.AutoExecution{Enabled: true, ManagedExecutionLimit: 1}},
				tasks:  []matching.Task{readyDispatchTask("T-1")},
				nodes:  []matching.Node{readyDispatchNode("node-a", time.Now().UTC())},
				active: 1,
			},
			expect: matching.ReasonManagedLimit,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newDispatchStore(t)
			seedDispatchReadyTask(t, st, "T-1")
			registerDispatchNode(t, st, "node-a", 2)
			d := &Dispatcher{Store: st, Projection: tc.proj, AgentID: "system-demo-1"}
			out, err := d.DispatchReady(context.Background(), DispatchRequest{ProjectID: dispatchTestProject})
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if out.Matched {
				t.Fatalf("不应匹配: %+v", out)
			}
			if len(out.Reasons) == 0 {
				t.Fatalf("缺少可解释原因")
			}
			found := false
			for _, r := range out.Reasons {
				if r.Code == tc.expect {
					found = true
				}
			}
			if !found {
				t.Fatalf("期望原因 %s，实际 %v", tc.expect, RenderQueueReasons(out.Reasons))
			}
			var status string
			_ = st.Read(func(tx *sql.Tx) error {
				task, e := db.GetTask(tx, dispatchTestProject, "T-1")
				if e == nil && task != nil {
					status = task.Status
				}
				return e
			})
			if status != string(model.TaskReady) {
				t.Fatalf("未匹配却改了任务状态: %s", status)
			}
		})
	}
}

type fakeDispatchPort struct {
	got  DispatchRequest
	out  DispatchOutcome
	err  error
	seen int
}

func (f *fakeDispatchPort) DispatchReady(_ context.Context, req DispatchRequest) (DispatchOutcome, error) {
	f.seen++
	f.got = req
	return f.out, f.err
}

// TestOrchestratorConsumesDispatchReadyJob 验证 Step 会消费 dispatch_ready，
// 并把匹配／领取结果写进 job 结果而不是走 LLM 分支。
func TestOrchestratorConsumesDispatchReadyJob(t *testing.T) {
	st := newDispatchStore(t)
	cwd := t.TempDir()

	cfg := config.SystemAgent{MaxConcurrent: 1}
	var job *db.SystemJob
	err := st.Write(func(tx *sql.Tx) error {
		var e error
		job, e = Enqueue(tx, cfg, dispatchTestProject, SystemKindDispatchReady, "", "", "有 ready 题待派发", map[string]any{}, true)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	port := &fakeDispatchPort{out: DispatchOutcome{
		Matched:     true,
		TaskID:      "T-9",
		NodeID:      model.NodeID("node-a"),
		RunID:       "run-x",
		OperationID: "op-x",
		Progressed:  true,
		Action:      "launched",
		LaunchSpec:  true,
	}}
	orch := &Orchestrator{
		Store:    st,
		Config:   cfg,
		Dispatch: port,
		ResolvePath: func(string) (string, error) {
			return cwd, nil
		},
		WorkerID: "system-agent",
	}
	ran, err := orch.Step(context.Background())
	if err != nil {
		t.Fatalf("step: %v", err)
	}
	if ran != 1 {
		t.Fatalf("应消费 1 条作业，实际 %d", ran)
	}
	if port.seen != 1 || port.got.ProjectID != dispatchTestProject || port.got.JobID != job.JobID {
		t.Fatalf("派发请求不对: %+v", port.got)
	}

	err = st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		if got == nil {
			t.Fatalf("作业不见了")
		}
		if got.Status != db.SystemJobDone {
			t.Fatalf("作业状态 = %s", got.Status)
		}
		if ok, _ := got.Result["matched"].(bool); !ok {
			t.Fatalf("结果缺少 matched: %+v", got.Result)
		}
		if got.Result["task_id"] != "T-9" || got.Result["operation_id"] != "op-x" {
			t.Fatalf("结果缺少领取信息: %+v", got.Result)
		}
		if ok, _ := got.Result["launch_spec"].(bool); !ok {
			t.Fatalf("结果缺少 launch_spec: %+v", got.Result)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}

// TestOrchestratorDispatchReadyJobCarriesReasons 验证未匹配时 job 结果含原因行。
func TestOrchestratorDispatchReadyJobCarriesReasons(t *testing.T) {
	st := newDispatchStore(t)
	cfg := config.SystemAgent{MaxConcurrent: 1}
	var job *db.SystemJob
	err := st.Write(func(tx *sql.Tx) error {
		var e error
		job, e = Enqueue(tx, cfg, dispatchTestProject, SystemKindDispatchReady, "", "", "有 ready 题待派发", map[string]any{}, true)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	port := &fakeDispatchPort{out: DispatchOutcome{
		Reasons: []matching.QueueReason{{
			Code:   matching.ReasonNoCandidateNode,
			Detail: "没有登记任何候选节点",
		}},
	}}
	orch := &Orchestrator{Store: st, Config: cfg, Dispatch: port, WorkerID: "system-agent"}
	if _, err := orch.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	err = st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		if got.Status != db.SystemJobDone {
			t.Fatalf("未匹配也应正常结束，实际 %s", got.Status)
		}
		lines, _ := got.Result["reason_lines"].([]any)
		if len(lines) == 0 {
			t.Fatalf("结果缺少 reason_lines: %+v", got.Result)
		}
		if !strings.Contains(lines[0].(string), string(matching.ReasonNoCandidateNode)) {
			t.Fatalf("原因行格式不对: %v", lines)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}

// TestOrchestratorDispatchReadyWithoutPortEscalates 验证未接线的 dispatch_ready
// 不会被静默吞掉，而是升级人工并注明原因。
func TestOrchestratorDispatchReadyWithoutPortEscalates(t *testing.T) {
	st := newDispatchStore(t)
	cfg := config.SystemAgent{MaxConcurrent: 1}
	var job *db.SystemJob
	err := st.Write(func(tx *sql.Tx) error {
		var e error
		job, e = Enqueue(tx, cfg, dispatchTestProject, SystemKindDispatchReady, "", "", "有 ready 题待派发", map[string]any{}, true)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	orch := &Orchestrator{Store: st, Config: cfg, WorkerID: "system-agent"}
	if _, err := orch.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	err = st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		if got.Status != db.SystemJobEscalated {
			t.Fatalf("状态 = %s", got.Status)
		}
		if got.Result["mode"] != "unwired" {
			t.Fatalf("结果未注明未接线: %+v", got.Result)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}
