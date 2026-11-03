// GAP-002：__runner 接入 CodeBuddy adapter 的验收。
//
// 三件事必须同时成立：
//  1. 受管 Run 的来源解释用 CodeBuddy adapter（协议命中时），Passthrough 只作回退；
//  2. 受管 Run 在库里 started_at／finished_at 非空；
//  3. run_events 里有阶段推进记录（状态与事实同批提交）。
//
// 来源进程用 tests/managed/testdata 下的受控脚本（synthetic fixture，不是厂商现场实测）。
package managed_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/runner/dbstore"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/session"
)

// TestSourceForPicksCodeBuddyOnlyOnMeasuredProtocol 说明来源实现的判定口径：
// 命中 CodeBuddy 实测协议就用 adapter，其余协议回退 passthrough。
func TestSourceForPicksCodeBuddyOnlyOnMeasuredProtocol(t *testing.T) {
	spec := model.LaunchSpec{
		Executable: "/usr/bin/env",
		Argv:       []string{"codebuddy"},
		ExpectedProtocol: model.Protocol{
			Format:  "stream-json",
			Version: codebuddy.ProtocolVersion,
		},
	}
	src, err := session.SourceFor(spec)
	if err != nil {
		t.Fatalf("SourceFor: %v", err)
	}
	if _, ok := src.(*codebuddy.Adapter); !ok {
		t.Fatalf("stream-json 协议应选 CodeBuddy adapter，实际 %T", src)
	}

	spec.ExpectedProtocol = model.Protocol{Format: "passthrough", Version: "1"}
	src, err = session.SourceFor(spec)
	if err != nil {
		t.Fatalf("SourceFor(passthrough): %v", err)
	}
	if _, ok := src.(dbstore.PassthroughSource); !ok {
		t.Fatalf("非 CodeBuddy 协议应回退 passthrough，实际 %T", src)
	}
}

// TestManagedRunWritesTimesAndPhaseEvents 跑一次受管 Run，核对库里的事实。
func TestManagedRunWritesTimesAndPhaseEvents(t *testing.T) {
	h := newHarness(t)
	store, err := db.OpenStore(h.gdb)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	var inst model.InstanceID
	if err := store.Read(func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, h.pid, "VN-L1")
		if e != nil || task == nil {
			t.Fatalf("task: %v %#v", e, task)
		}
		inst = model.InstanceID(task.InstanceID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: "codebuddy-lab", ProjectID: h.pid, SlotLimit: 1})
	}); err != nil {
		t.Fatal(err)
	}

	ws, _ := filepath.Abs(filepath.Join(h.global, "ws-gap002"))
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := claim.New(store, nil).Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:      model.ProjectID(h.pid),
			AgentID:        "cursor-managed-1",
			RequestedTask:  &inst,
			IdempotencyKey: "m:gap002",
			Capacity:       ports.Capacity{NodeSlotLimit: 1, ManagedLimit: 1},
		},
		NodeID:    "codebuddy-lab",
		Workspace: ws,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	runID := out.RunID

	spec := model.LaunchSpec{
		Executable: h.fakeCLI,
		// argv 必填：来源进程的实参，不是给 shell 的命令串。
		Argv:      []string{"--print", "managed"},
		Cwd:       h.repo,
		StdinMode: model.StdinNone,
		ExpectedProtocol: model.Protocol{
			Format:  "stream-json",
			Version: codebuddy.ProtocolVersion,
		},
	}
	source, err := session.SourceFor(spec)
	if err != nil {
		t.Fatalf("SourceFor: %v", err)
	}
	jr, err := journal.Open(filepath.Join(h.global, "journal-gap002"), journal.Options{RunID: string(runID), Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	defer jr.Close()

	r, err := session.New(&dbstore.Store{DB: store, ProjectID: h.pid}, source, jr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rep, err := r.Run(ctx, session.Config{
		Instance:   inst,
		RunID:      runID,
		Generation: out.Generation,
		Spec:       spec,
		Protocol:   spec.ExpectedProtocol,
		Heartbeat:  time.Second,
	})
	if err != nil {
		t.Fatalf("session run: %v", err)
	}
	if rep.Outcome != model.OutcomeCompleted {
		t.Fatalf("outcome=%q 期望 completed（stderr=%s）", rep.Outcome, rep.Stderr)
	}

	// 1. 起止时间必须落库。
	var run *db.Run
	if err := store.Read(func(tx *sql.Tx) error {
		run, _ = db.GetRun(tx, string(runID))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatalf("run %s 不存在", runID)
	}
	if run.StartedAt == "" {
		t.Fatal("run.started_at 为空：受管 Run 必须记起止时刻")
	}
	if run.FinishedAt == "" {
		t.Fatalf("run.finished_at 为空（phase=%s）", run.Phase)
	}

	// 2. 阶段推进要落在 run_events 里。
	var phases []string
	if err := store.Read(func(tx *sql.Tx) error {
		events, e := db.ReadRunEvents(tx, h.pid, 0, 200)
		if e != nil {
			return e
		}
		for _, ev := range events {
			if ev.Kind == "run.phase" {
				phases = append(phases, string(ev.Phase))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(phases) < 2 {
		t.Fatalf("run_events 里的阶段推进只有 %v：状态与事实没有同批提交", phases)
	}
	sawRunning, sawTerminal := false, false
	for _, p := range phases {
		if p == string(model.PhaseRunning) {
			sawRunning = true
		}
		if model.RunPhase(p).Terminal() {
			sawTerminal = true
		}
	}
	if !sawRunning || !sawTerminal {
		t.Fatalf("阶段推进不完整：%v", phases)
	}
}
