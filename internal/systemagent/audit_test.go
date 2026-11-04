package systemagent

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// TestSystemJobWritesAuditSidecar 验证诊断类作业把 LLM 原文与决策落到侧车，
// 并在作业结果里留下可反查的路径。
func TestSystemJobWritesAuditSidecar(t *testing.T) {
	st := newDispatchStore(t)
	stateDir := filepath.Join(t.TempDir(), "maw-home")
	t.Setenv("MAW_GLOBAL", stateDir)
	cfg := config.SystemAgent{MaxConcurrent: 1, AutoKinds: []string{SystemKindUnblock}}
	raw := `{"action":"notify_only","reason":"等人工确认"}`
	var job *db.SystemJob
	err := st.Write(func(tx *sql.Tx) error {
		var e error
		job, e = Enqueue(tx, cfg, dispatchTestProject, SystemKindUnblock, "T-1", "", "任务阻塞待诊断", map[string]any{}, true)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	orch := &Orchestrator{
		Store:    st,
		Config:   cfg,
		Invoker:  FuncInvoker(func(context.Context, DiagnoseRequest) (string, error) { return raw, nil }),
		Effects:  NopEffects{},
		WorkerID: "system-agent",
	}
	if _, err := orch.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}

	err = st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		path, _ := got.Result["decision_raw_path"].(string)
		if path == "" {
			t.Fatalf("结果缺少 decision_raw_path: %+v", got.Result)
		}
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatalf("读取侧车: %v", e)
		}
		if !strings.Contains(string(b), raw) {
			t.Fatalf("侧车没有保留原文: %s", b)
		}
		if info, e := os.Stat(path); e == nil && info.Mode().Perm() != 0o600 {
			t.Fatalf("侧车权限 = %o", info.Mode().Perm())
		}
		if !strings.Contains(path, filepath.Join(stateDir, "runtime", dispatchTestProject, "system-jobs", sanitize(job.JobID))) {
			t.Fatalf("侧车路径不在状态目录约定位置: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}

// TestSystemJobKeepsRawWhenDecisionUnparseable 验证解析失败也不丢原文：
// 作业重排时结果里带着侧车路径，重跑时能直接对照模型输出。
func TestSystemJobKeepsRawWhenDecisionUnparseable(t *testing.T) {
	st := newDispatchStore(t)
	t.Setenv("MAW_GLOBAL", filepath.Join(t.TempDir(), "maw-home"))
	cfg := config.SystemAgent{MaxConcurrent: 1, AutoKinds: []string{SystemKindUnblock}}
	var job *db.SystemJob
	err := st.Write(func(tx *sql.Tx) error {
		var e error
		job, e = Enqueue(tx, cfg, dispatchTestProject, SystemKindUnblock, "T-1", "", "任务阻塞待诊断", map[string]any{}, true)
		return e
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	orch := &Orchestrator{
		Store:    st,
		Config:   cfg,
		Invoker:  FuncInvoker(func(context.Context, DiagnoseRequest) (string, error) { return "not-json", nil }),
		Effects:  NopEffects{},
		WorkerID: "system-agent",
	}
	if _, err := orch.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	err = st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		if got.Status != db.SystemJobQueued {
			t.Fatalf("解析失败应重排，实际 %s", got.Status)
		}
		path, _ := got.Result["decision_raw_path"].(string)
		if path == "" {
			t.Fatalf("重排结果缺少侧车路径: %+v", got.Result)
		}
		if _, e := os.Stat(path); e != nil {
			t.Fatalf("侧车不存在: %v", e)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}

// TestSystemJobLinksRunAndJournal 验证 dispatch_ready 派发后，作业能反查到
// Run、操作与日志目录（即使作业已经结束）。
func TestSystemJobLinksRunAndJournal(t *testing.T) {
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
		Matched:     true,
		TaskID:      "T-1",
		NodeID:      model.NodeID("node-a"),
		RunID:       "run-audit-1",
		OperationID: "op-audit-1",
		Progressed:  true,
		Action:      "launched",
		LaunchSpec:  true,
		JournalDir:  "/tmp/ops/op-audit-1/journal",
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
			t.Fatalf("作业状态 = %s", got.Status)
		}
		if got.RunID != "run-audit-1" || got.OperationID != "op-audit-1" {
			t.Fatalf("作业未关联 Run/操作: %+v", got)
		}
		if got.JournalDir != "/tmp/ops/op-audit-1/journal" {
			t.Fatalf("日志目录 = %q", got.JournalDir)
		}
		if got.Result["journal_dir"] != got.JournalDir {
			t.Fatalf("结果里的 journal_dir 不一致: %+v", got.Result)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}

// TestSystemJobDerivesJournalDir 验证端口没回日志目录时，作业仍按 op 目录约定
// （<global>/runtime/<project>/ops/<op>/journal）记下可反查的路径。
func TestSystemJobDerivesJournalDir(t *testing.T) {
	st := newDispatchStore(t)
	global := filepath.Join(t.TempDir(), "maw-home")
	t.Setenv("MAW_GLOBAL", global)
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
		TaskID:      "T-1",
		NodeID:      model.NodeID("node-a"),
		RunID:       "run-audit-2",
		OperationID: "op-audit-2",
		Progressed:  true,
		Action:      "launched",
	}}
	orch := &Orchestrator{Store: st, Config: cfg, Dispatch: port, WorkerID: "system-agent"}
	if _, err := orch.Step(context.Background()); err != nil {
		t.Fatalf("step: %v", err)
	}
	wantJournal := filepath.Join(global, "runtime", dispatchTestProject, "ops", "op-audit-2", "journal")
	err = st.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		if got.JournalDir != wantJournal {
			t.Fatalf("推导的日志目录 = %q，期望 %q", got.JournalDir, wantJournal)
		}
		if got.Result["journal_dir"] != wantJournal {
			t.Fatalf("结果缺少推导出的 journal_dir: %+v", got.Result)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("校验作业: %v", err)
	}
}
