package systemagent

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

type fakeProducerPorts struct {
	projects   []ProducerProject
	selectable map[string]string // projectID -> taskID；不存在表示无 ready/无空槽
	recent     map[string]int
	enqueued   []string
	run        []string
	runErr     error
}

func (f *fakeProducerPorts) ports() ProducerPorts {
	return ProducerPorts{
		Now: func() time.Time { return time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC) },
		Projects: func() ([]ProducerProject, error) {
			return f.projects, nil
		},
		Selectable: func(projectID string) (string, bool, error) {
			id, ok := f.selectable[projectID]
			return id, ok, nil
		},
		Recent: func(projectID string, since time.Time) (int, error) {
			return f.recent[projectID], nil
		},
		Enqueue: func(projectID, taskID string) (string, bool, error) {
			f.enqueued = append(f.enqueued, projectID+":"+taskID)
			return "sjob_produced", true, nil
		},
		Run: func(_ context.Context, jobID string) error {
			f.run = append(f.run, jobID)
			return f.runErr
		},
	}
}

func readyProducerPorts(tasks ...string) *fakeProducerPorts {
	m := map[string]string{}
	for _, t := range tasks {
		m["aaaaaaaaaaaaaaaa"] = t
	}
	return &fakeProducerPorts{
		projects:   []ProducerProject{{ID: "aaaaaaaaaaaaaaaa", Path: "/tmp/alpha"}},
		selectable: m,
		recent:     map[string]int{},
	}
}

// TestProducerAutoSelectOffEnqueuesNothing 覆盖验收 1：默认关闭时零入队。
func TestProducerAutoSelectOffEnqueuesNothing(t *testing.T) {
	f := readyProducerPorts("T-1")
	p := &Producer{Config: ProducerConfig{CooldownSec: 300}, Ports: f.ports()}
	out, err := p.Produce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Enqueued != 0 || len(f.enqueued) != 0 || len(f.run) != 0 {
		t.Fatalf("auto_select=false 应零入队零执行，实际 %+v", out)
	}
	if len(out.Skipped) != 1 || out.Skipped[0] != "auto_select_off" {
		t.Fatalf("应留默认关闭的跳过原因，实际 %v", out.Skipped)
	}
}

// TestProducerOneJobPerCycle 覆盖验收 2：一个周期恰好入队并执行一条。
func TestProducerOneJobPerCycle(t *testing.T) {
	f := &fakeProducerPorts{
		projects: []ProducerProject{
			{ID: "aaaaaaaaaaaaaaaa", Path: "/tmp/alpha"},
			{ID: "bbbbbbbbbbbbbbbb", Path: "/tmp/beta"},
		},
		selectable: map[string]string{"aaaaaaaaaaaaaaaa": "T-1", "bbbbbbbbbbbbbbbb": "T-2"},
		recent:     map[string]int{},
	}
	p := &Producer{Config: ProducerConfig{AutoSelect: true, CooldownSec: 300}, Ports: f.ports()}
	out, err := p.Produce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Enqueued != 1 || len(f.enqueued) != 1 {
		t.Fatalf("一个周期应恰好入队 1 条，实际 %+v", out)
	}
	if out.Executed != 1 || len(f.run) != 1 || f.run[0] != "sjob_produced" {
		t.Fatalf("入队后应同周期执行，实际 %+v run=%v", out, f.run)
	}
}

// TestProducerSkipsUnfinishedAndCooldown 覆盖验收 2 的去重/冷却部分。
func TestProducerSkipsUnfinishedAndCooldown(t *testing.T) {
	for _, tc := range []struct {
		name   string
		recent map[string]int
	}{
		{"同工程未完成 dispatch_ready", map[string]int{"aaaaaaaaaaaaaaaa": 1}},
		{"cooldown 内已有 dispatch_ready", map[string]int{"aaaaaaaaaaaaaaaa": 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := readyProducerPorts("T-1")
			f.recent = tc.recent
			p := &Producer{Config: ProducerConfig{AutoSelect: true, CooldownSec: 300}, Ports: f.ports()}
			out, err := p.Produce(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if out.Enqueued != 0 || len(f.enqueued) != 0 {
				t.Fatalf("已有作业/冷却内应跳过入队，实际 %+v", out)
			}
			if len(out.Skipped) != 1 || out.Skipped[0] != "aaaaaaaaaaaaaaaa:recent_dispatch" {
				t.Fatalf("跳过原因不对: %v", out.Skipped)
			}
		})
	}
}

// TestProducerNoReadyOrNoSlots 覆盖验收 3：无 ready 或无空槽时不入队。
func TestProducerNoReadyOrNoSlots(t *testing.T) {
	f := readyProducerPorts() // selectable 为空 = 匹配不到（无 ready 或无空槽）
	p := &Producer{Config: ProducerConfig{AutoSelect: true, CooldownSec: 300}, Ports: f.ports()}
	out, err := p.Produce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Enqueued != 0 || len(f.enqueued) != 0 || len(f.run) != 0 {
		t.Fatalf("无 ready 或无空槽应零入队，实际 %+v", out)
	}
	if len(out.Skipped) != 1 || out.Skipped[0] != "aaaaaaaaaaaaaaaa:no_ready_or_no_slots" {
		t.Fatalf("跳过原因不对: %v", out.Skipped)
	}
}

// TestProducerExecutesJobSameCycleWithSystemAgentDisabled 覆盖验收 4：
// 真实状态库 + 真实 Orchestrator，system_agent.enabled=false 时生产的作业
// 同周期被执行（Orchestrator.RunJobID 不检查 Enabled）并产生 Run 链接。
func TestProducerExecutesJobSameCycleWithSystemAgentDisabled(t *testing.T) {
	st := newDispatchStore(t)
	cfg := config.SystemAgent{Enabled: false, MaxConcurrent: 1, CooldownoutSec: 300, MaxAttempts: 3}
	port := &fakeDispatchPort{out: DispatchOutcome{
		Matched: true, TaskID: "T-9", NodeID: model.NodeID("node-a"),
		RunID: "run_produced", OperationID: "op_produced",
		Progressed: true, Action: "launched", LaunchSpec: true,
	}}
	orch := &Orchestrator{
		Store: st, Config: cfg, Dispatch: port,
		ResolvePath: func(string) (string, error) { return t.TempDir(), nil },
		WorkerID:    "system-agent",
	}
	ports := NewStoreProducerPorts(st, cfg,
		func() ([]ProducerProject, error) {
			return []ProducerProject{{ID: dispatchTestProject, Path: "/tmp/alpha"}}, nil
		},
		func(string) (string, bool, error) { return "T-9", true, nil },
		func(ctx context.Context, jobID string) error { return orch.RunJobID(ctx, jobID) },
	)
	p := &Producer{Config: ProducerConfig{AutoSelect: true, CooldownSec: cfg.CooldownoutSec}, Ports: ports}
	out, err := p.Produce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if out.Enqueued != 1 || out.Executed != 1 || out.JobID == "" {
		t.Fatalf("生产的作业应同周期被执行，实际 %+v", out)
	}
	if port.seen != 1 {
		t.Fatalf("应走既有 Match→Claim 链一次，实际 %d", port.seen)
	}
	var job *db.SystemJob
	if err := st.Read(func(tx *sql.Tx) error {
		var e error
		job, e = db.GetSystemJob(tx, out.JobID)
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if job == nil || job.Kind != SystemKindDispatchReady || job.Status != db.SystemJobDone {
		t.Fatalf("作业应以 dispatch_ready/done 落库，实际 %+v", job)
	}
	if job.Result["run_id"] != "run_produced" || job.Result["operation_id"] != "op_produced" {
		t.Fatalf("执行应回填 Run/Operation 链接，实际 %v", job.Result)
	}
}

// TestProducerStorePortsDetectUnfinishedAndCooldown 覆盖真实库端口的去重口径：
// 未完成的 dispatch_ready 无论多旧都算已有；已完成作业只在冷却窗口内算。
func TestProducerStorePortsDetectUnfinishedAndCooldown(t *testing.T) {
	st := newDispatchStore(t)
	cfg := config.SystemAgent{MaxAttempts: 3}
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	ports := NewStoreProducerPorts(st, cfg,
		func() ([]ProducerProject, error) { return nil, nil },
		func(string) (string, bool, error) { return "", false, nil },
		nil,
	)
	seed := func(status, created string) {
		t.Helper()
		if err := st.Write(func(tx *sql.Tx) error {
			_, err := db.InsertSystemJob(tx, &db.SystemJob{
				JobID: "sjob_" + status + "_" + created, ProjectID: dispatchTestProject,
				Kind: SystemKindDispatchReady, Status: status, CreatedAt: created,
				UpdatedAt: created, MaxAttempts: 3,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	seed(db.SystemJobQueued, "2020-01-01T00:00:00Z")
	if n, err := ports.Recent(dispatchTestProject, now.Add(-300*time.Second)); err != nil || n != 1 {
		t.Fatalf("未完成的旧作业也应算已有，n=%d err=%v", n, err)
	}

	st2 := newDispatchStore(t)
	ports2 := NewStoreProducerPorts(st2, cfg,
		func() ([]ProducerProject, error) { return nil, nil },
		func(string) (string, bool, error) { return "", false, nil },
		nil,
	)
	recent := now.Add(-10 * time.Second).Format(time.RFC3339)
	old := now.Add(-time.Hour).Format(time.RFC3339)
	for _, ts := range []string{recent, old} {
		if err := st2.Write(func(tx *sql.Tx) error {
			_, err := db.InsertSystemJob(tx, &db.SystemJob{
				JobID: "sjob_done_" + ts, ProjectID: dispatchTestProject,
				Kind: SystemKindDispatchReady, Status: db.SystemJobDone,
				CreatedAt: ts, UpdatedAt: ts, MaxAttempts: 3,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if n, err := ports2.Recent(dispatchTestProject, now.Add(-300*time.Second)); err != nil || n != 1 {
		t.Fatalf("只有冷却窗口内的完成作业该计数，n=%d err=%v", n, err)
	}
}

// TestProducerPropagatesErrors 覆盖写失败不静默。
func TestProducerPropagatesErrors(t *testing.T) {
	want := errors.New("run failed")
	f := readyProducerPorts("T-1")
	f.runErr = want
	p := &Producer{Config: ProducerConfig{AutoSelect: true, CooldownSec: 300}, Ports: f.ports()}
	out, err := p.Produce(context.Background())
	if !errors.Is(err, want) {
		t.Fatalf("执行失败应上抛，实际 %v", err)
	}
	if out.Enqueued != 1 {
		t.Fatalf("入队事实仍应保留，实际 %+v", out)
	}
}
