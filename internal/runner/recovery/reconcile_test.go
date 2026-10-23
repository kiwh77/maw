package recovery

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/process"
)

// errTestProbe 模拟核对过程本身失败。
var errTestProbe = errors.New("process: 无法读取进程信息")

// fakeProbe 直接给出核对结论：本包要测的是「四种现场怎么判」，
// 而 PID 复用／进程组清空的核对由 process 包自己的测试覆盖。
type fakeProbe struct {
	ev  process.StopEvidence
	err error
}

func (f fakeProbe) Recover(process.Identity) (process.StopEvidence, error) {
	return f.ev, f.err
}

// identity 造一份合法身份记录（不启动真实进程）。
func identity(t *testing.T, dir, nonce string) process.Identity {
	t.Helper()
	id := process.Identity{
		RunID:  "run_a405",
		PID:    4242,
		PGID:   4242,
		Start:  time.Date(2026, 10, 23, 7, 0, 0, 0, time.UTC),
		Nonce:  nonce,
		Booted: time.Date(2026, 10, 23, 7, 0, 1, 0, time.UTC),
	}
	if err := process.WriteIdentity(filepath.Join(dir, IdentityFile), id); err != nil {
		t.Fatalf("写身份记录失败：%v", err)
	}
	return id
}

// writeJournal 建一份真实日志并写一条记录，返回其目录。
func writeJournal(t *testing.T, runID string) string {
	t.Helper()
	dir := t.TempDir()
	jr, err := journal.Open(dir, journal.Options{RunID: runID, Sync: true})
	if err != nil {
		t.Fatalf("打开 journal 失败：%v", err)
	}
	if _, err := jr.Append(journal.Record{Kind: "stream.record", Payload: json.RawMessage(`{"type":"system"}`)}); err != nil {
		t.Fatalf("写日志失败：%v", err)
	}
	if err := jr.Close(); err != nil {
		t.Fatalf("关闭日志失败：%v", err)
	}
	return dir
}

// TestReconcileRunning 覆盖「Runner 死了但工具还活着」：保留资源、禁止重跑。
func TestReconcileRunning(t *testing.T) {
	dir := t.TempDir()
	identity(t, dir, "nonce-a")
	probe := fakeProbe{ev: process.StopEvidence{
		Outcome: process.OutcomeUnknown,
		Exited:  false,
		Note:    "进程仍在运行，停止状态未确认",
	}}

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405", Probe: probe})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateRunning {
		t.Fatalf("进程还在，应判 running，实际 %s（%s）", ev.State, ev.Reason)
	}
	if !ev.KeepsResources() || !ev.BlocksRerun() {
		t.Fatal("进程还在时必须保留现场并禁止重跑")
	}
	if err := ev.GuardRerun(); err == nil {
		t.Fatal("禁止重跑时必须返回可上报的错误")
	} else if e, ok := model.AsError(err); !ok || e.Code != model.CodeDoubleOccupancy {
		t.Fatalf("应触发 E240 双占用，实际 %v", err)
	}
}

// TestReconcileFinished 覆盖「确定结束」：进程组清空，目录可复用。
func TestReconcileFinished(t *testing.T) {
	dir := t.TempDir()
	identity(t, dir, "nonce-a")
	probe := fakeProbe{ev: process.StopEvidence{
		Outcome:              process.OutcomeExited,
		Exited:               true,
		Lost:                 true,
		GroupEmpty:           true,
		DescendantsConfirmed: true,
	}}

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405", Probe: probe})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateFinished {
		t.Fatalf("进程已退出且组已清空，应判 finished，实际 %s（%s）", ev.State, ev.Reason)
	}
	if ev.KeepsResources() {
		t.Fatal("确定结束后不应继续占用")
	}
	if !ev.Reusable() {
		t.Fatalf("确定结束且日志无缺失时目录应可复用，实际 %s", ev.Describe())
	}
	if err := ev.GuardRerun(); err != nil {
		t.Fatalf("确定结束时应允许重跑：%v", err)
	}
}

// TestReconcileGroupStillAlive 覆盖「进程没了但组里还有人」：归属不明 → unknown。
func TestReconcileGroupStillAlive(t *testing.T) {
	dir := t.TempDir()
	identity(t, dir, "nonce-a")
	probe := fakeProbe{ev: process.StopEvidence{
		Outcome:    process.OutcomeUnknown,
		Exited:     true,
		Lost:       true,
		GroupEmpty: false,
		Note:       "进程已失联，但进程组仍有存活成员，无法确认归属",
	}}

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405", Probe: probe})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateUnknown {
		t.Fatalf("组仍有成员时应判 unknown，实际 %s", ev.State)
	}
	if !ev.BlocksRerun() {
		t.Fatal("归属不明时不得重跑")
	}
}

// TestReconcileNotStarted 覆盖「从未启动」：没有身份记录也没有日志。
func TestReconcileNotStarted(t *testing.T) {
	ev, err := Reconcile(Options{Dir: t.TempDir(), RunID: "run_a405"})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateNotStarted {
		t.Fatalf("应判 not_started，实际 %s（%s）", ev.State, ev.Reason)
	}
	if ev.BlocksRerun() || !ev.Reusable() {
		t.Fatal("从未启动过的目录应可复用")
	}
}

// TestReconcileJournalWithoutIdentity 覆盖「启动过但身份丢了」：证据不足。
func TestReconcileJournalWithoutIdentity(t *testing.T) {
	dir := writeJournal(t, "run_a405")

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405"})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateUnknown {
		t.Fatalf("日志有记录但身份缺失，应判 unknown，实际 %s", ev.State)
	}
	if !ev.KeepsResources() || !ev.BlocksRerun() {
		t.Fatal("证据不足时必须保留现场并禁止重跑")
	}
	if ev.Journal.Records == 0 {
		t.Fatal("应带回日志事实")
	}
}

// TestReconcileCorruptIdentity 覆盖身份记录损坏。
func TestReconcileCorruptIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, IdentityFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("写坏身份失败：%v", err)
	}

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405"})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateUnknown {
		t.Fatalf("身份损坏应判 unknown，实际 %s", ev.State)
	}
	if !ev.BlocksRerun() {
		t.Fatal("身份损坏时不得重跑")
	}
}

// TestReconcileNonceMismatch 覆盖身份被挪用：nonce 对不上就不认。
func TestReconcileNonceMismatch(t *testing.T) {
	dir := t.TempDir()
	identity(t, dir, "nonce-a")

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405", ExpectedNonce: "another-run-nonce"})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateUnknown {
		t.Fatalf("nonce 不匹配应判 unknown，实际 %s", ev.State)
	}
	if !ev.BlocksRerun() {
		t.Fatal("身份可疑时不得重跑")
	}
	if ev.Identity == nil {
		t.Fatal("应带回读到的身份，便于人工核对")
	}
}

// TestReconcileProbeError 覆盖核对本身失败：不猜结论。
func TestReconcileProbeError(t *testing.T) {
	dir := t.TempDir()
	identity(t, dir, "nonce-a")
	probe := fakeProbe{err: errTestProbe}

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405", Probe: probe})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateUnknown {
		t.Fatalf("核对失败应判 unknown，实际 %s", ev.State)
	}
	if !ev.BlocksRerun() {
		t.Fatal("核对失败时不得重跑")
	}
}

// TestReconcilePendingReplay 覆盖游标恢复：日志里有待补交记录时必须报出来。
func TestReconcilePendingReplay(t *testing.T) {
	dir := t.TempDir()
	identity(t, dir, "nonce-a")
	probe := fakeProbe{ev: process.StopEvidence{
		Outcome: process.OutcomeExited, Exited: true, GroupEmpty: true, DescendantsConfirmed: true,
	}}

	jr, err := journal.Open(dir, journal.Options{RunID: "run_a405", Sync: true})
	if err != nil {
		t.Fatalf("打开 journal 失败：%v", err)
	}
	if _, err := jr.Append(journal.Record{Kind: "stream.record", Payload: json.RawMessage(`{"type":"result"}`)}); err != nil {
		t.Fatalf("写日志失败：%v", err)
	}
	_ = jr.Close()

	ev, err := Reconcile(Options{Dir: dir, RunID: "run_a405", Probe: probe})
	if err != nil {
		t.Fatalf("核实失败：%v", err)
	}
	if ev.State != StateFinished {
		t.Fatalf("进程已结束，应判 finished，实际 %s", ev.State)
	}
	if !ev.PendingReplay() {
		t.Fatalf("应报告待补交记录，实际 %s", ev.Describe())
	}
	if ev.Cursor.Seq != 0 {
		t.Fatalf("游标应停在 0（未交付），实际 %d", ev.Cursor.Seq)
	}
}

// TestReconcileRequiresDir 覆盖输入校验。
func TestReconcileRequiresDir(t *testing.T) {
	if _, err := Reconcile(Options{}); err == nil {
		t.Fatal("空目录必须被拒绝")
	}
}

// TestStateValid 覆盖状态取值封闭。
func TestStateValid(t *testing.T) {
	for _, s := range []State{StateRunning, StateFinished, StateNotStarted, StateUnknown} {
		if !s.Valid() {
			t.Errorf("%s 应合法", s)
		}
	}
	if State("done").Valid() {
		t.Fatal("未登记的现场状态不得通过校验")
	}
}
