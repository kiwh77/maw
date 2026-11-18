package systemagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// 本文件钉住 PI-305 的四条验收：
//   1. Pi 诊断跑的是 diagnose 模式：无写权限、不拼 shell、决策原文交给 ParseDecision；
//   2. 用量归属诊断与对应作业（purpose=diagnose + run_id 由 job 派生 + job 行反查）；
//   3. 超时／认证失败／非法结构都有可审计结论（类别 + journal + 审计摘要）；
//   4. 事件流里的未知类型不静默丢弃，坏行只计数不猜语义。

const piJobID = "sjob_pi305"

// piEvents 拼一段最小的运行时事件流：版本自述 + 会话 + 一次调用用量 + 终态。
func piEvents(runID string, extra ...string) string {
	lines := []string{
		`{"contract":"maw.pi.execution/v1","type":"version_report","payload":{"sdk_version":"1.5.2","mode":"diagnose","protocol":{"format":"pi-stream-json","version":"1"}}}`,
		`{"contract":"maw.pi.execution/v1","type":"session_started","payload":{"mode":"diagnose","cwd":"/repo"}}`,
		`{"contract":"maw.pi.execution/v1","type":"usage","call_id":"pc_1","payload":{"scope":"call","semantic":"delta","input_tokens":1200,"output_tokens":340,"quality":"observed"}}`,
	}
	lines = append(lines, extra...)
	lines = append(lines,
		`{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":true,"text":"{\"action\":\"escalate_human\",\"reason\":\"scope too large\"}","outcome":"completed","evidence":["stop_reason:stop"]}}`)
	return strings.Join(lines, "\n") + "\n"
}

func newPiTestStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MAW_GLOBAL", dir)
	st, err := db.OpenStore(filepath.Join(dir, "maw.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, dir
}

func seedSystemJob(t *testing.T, st *db.Store, projectID string) {
	t.Helper()
	err := st.Write(func(tx *sql.Tx) error {
		_, e := db.InsertSystemJob(tx, &db.SystemJob{
			JobID: piJobID, ProjectID: projectID, Kind: SystemKindUnblock,
			Status: db.SystemJobQueued, TaskID: "PI-305", Reason: "blocked",
			MaxAttempts: 3,
		})
		return e
	})
	if err != nil {
		t.Fatalf("InsertSystemJob: %v", err)
	}
}

func piTestRequest() DiagnoseRequest {
	return DiagnoseRequest{
		ProjectID: "p1", TaskID: "PI-305", JobID: piJobID,
		Kind: SystemKindUnblock, Reason: "blocked", ProjectPath: "/repo",
	}
}

func TestPiDiagnoseUsesReadOnlyDiagnoseMode(t *testing.T) {
	st, _ := newPiTestStore(t)
	var gotStdin, gotArgv string
	var gotCwd string
	inv := &PiInvoker{
		NodePath: "/usr/bin/node", RuntimePath: "/repo/runtimes/pi/src/main.mjs",
		Cwd: "/repo", NodeID: "pi-1", Store: st, Timeout: 5 * time.Second,
		Exec: func(ctx context.Context, argv []string, cwd string, env []string, stdin string) (piRunResult, error) {
			gotStdin, gotArgv, gotCwd = stdin, strings.Join(argv, " "), cwd
			return piRunResult{Stdout: piEvents("run_diag_" + piJobID)}, nil
		},
	}
	raw, err := inv.Diagnose(context.Background(), piTestRequest())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	d, err := ParseDecision(raw)
	if err != nil || d.Action != ActionEscalateHuman {
		t.Fatalf("决策仍必须经过解析与动作策略：%v / %+v", err, d)
	}

	var cfg piDiagnoseConfig
	if err := json.Unmarshal([]byte(gotStdin), &cfg); err != nil {
		t.Fatalf("stdin 不是 JSON 配置: %v", err)
	}
	if cfg.Mode != "diagnose" {
		t.Fatalf("mode=%q，诊断必须是 diagnose", cfg.Mode)
	}
	if len(cfg.WriteScope) != 0 {
		t.Fatalf("诊断不得有写权限，write_scope=%v", cfg.WriteScope)
	}
	if cfg.AllowBash {
		t.Fatal("诊断不得启用 bash")
	}
	if !strings.Contains(gotArgv, "/repo/runtimes/pi/src/main.mjs") || strings.Contains(gotArgv, "sh") {
		t.Fatalf("argv 必须是数组且只有运行时入口: %s", gotArgv)
	}
	if gotCwd != "/repo" {
		t.Fatalf("cwd=%q", gotCwd)
	}
}

func TestPiDiagnoseAttributesUsageToJob(t *testing.T) {
	st, _ := newPiTestStore(t)
	seedSystemJob(t, st, "p1")
	inv := &PiInvoker{
		NodePath: "node", RuntimePath: "/repo/runtimes/pi/src/main.mjs",
		Cwd: "/repo", NodeID: "pi-1", Model: "pi-model", Store: st,
		Exec: func(context.Context, []string, string, []string, string) (piRunResult, error) {
			return piRunResult{Stdout: piEvents("run_diag_" + piJobID)}, nil
		},
	}
	if _, err := inv.Diagnose(context.Background(), piTestRequest()); err != nil {
		t.Fatalf("Diagnose: %v", err)
	}

	var purpose, runID, projectID, nodeID, model string
	var input, output sql.NullInt64
	err := st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT purpose, run_id, project_id, node_id, model, input_tokens, output_tokens
FROM usage_records WHERE purpose = 'diagnose'`).Scan(&purpose, &runID, &projectID, &nodeID, &model, &input, &output)
	})
	if err != nil {
		t.Fatalf("诊断用量没有落库（归属断链）：%v", err)
	}
	if purpose != "diagnose" || projectID != "p1" || nodeID != "pi-1" || model != "pi-model" {
		t.Fatalf("归属字段不对: purpose=%s project=%s node=%s model=%s", purpose, projectID, nodeID, model)
	}
	wantRun := diagnoseRunPrefix + sanitize(piJobID)
	if runID != wantRun {
		t.Fatalf("run_id=%q，期望由作业派生 %q", runID, wantRun)
	}
	if !input.Valid || !output.Valid || input.Int64 != 1200 || output.Int64 != 340 {
		t.Fatalf("逐次调用用量不对: in=%v out=%v", input, output)
	}

	// 作业行必须有同一个 run_id：用量与作业的对应关系靠这条反查。
	var jobRun, journal string
	err = st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT run_id, journal_dir FROM system_jobs WHERE job_id = ?`, piJobID).Scan(&jobRun, &journal)
	})
	if err != nil {
		t.Fatalf("读作业行: %v", err)
	}
	if jobRun != wantRun {
		t.Fatalf("作业行 run_id=%q，期望 %q", jobRun, wantRun)
	}
	if journal == "" {
		t.Fatal("作业行应记录诊断审计目录")
	}
	if _, err := os.Stat(filepath.Join(journal, diagnoseJournalFile)); err != nil {
		t.Fatalf("journal 原文缺失: %v", err)
	}
	if _, err := os.Stat(filepath.Join(journal, diagnoseAuditFile)); err != nil {
		t.Fatalf("审计摘要缺失: %v", err)
	}
}

func TestPiDiagnoseTimeoutIsAuditable(t *testing.T) {
	st, _ := newPiTestStore(t)
	seedSystemJob(t, st, "p1")
	inv := &PiInvoker{
		RuntimePath: "/repo/runtimes/pi/src/main.mjs", Cwd: "/repo", NodeID: "pi-1",
		Store: st, Timeout: 20 * time.Millisecond,
		Exec: func(ctx context.Context, _ []string, _ string, _ []string, _ string) (piRunResult, error) {
			<-ctx.Done()
			return piRunResult{Stderr: "still thinking", ExitCode: -1}, ctx.Err()
		},
	}
	_, err := inv.Diagnose(context.Background(), piTestRequest())
	var pe *PiDiagnoseError
	if !errors.As(err, &pe) {
		t.Fatalf("超时必须是可审计的 PiDiagnoseError，得到 %T %v", err, err)
	}
	if pe.Category != "timeout" {
		t.Fatalf("category=%q，期望 timeout", pe.Category)
	}
	if pe.Journal == "" {
		t.Fatal("超时也必须留下 journal")
	}
	if _, statErr := os.Stat(pe.Journal); statErr != nil {
		t.Fatalf("journal 文件不存在: %v", statErr)
	}
	audit := filepath.Join(filepath.Dir(pe.Journal), diagnoseAuditFile)
	if _, statErr := os.Stat(audit); statErr != nil {
		t.Fatalf("超时也要写审计摘要: %v", statErr)
	}
}

func TestPiDiagnoseAuthFailureCarriesCategory(t *testing.T) {
	st, _ := newPiTestStore(t)
	events := strings.Join([]string{
		`{"contract":"maw.pi.execution/v1","type":"version_report","payload":{"sdk_version":"1.5.2","mode":"diagnose","protocol":{"format":"pi-stream-json","version":"1"}}}`,
		`{"contract":"maw.pi.execution/v1","type":"error","payload":{"category":"auth","message":"401 invalid api key (redacted)","retryable":false}}`,
		`{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":false,"reason":"auth","text":"","evidence":["category:auth"]}}`,
	}, "\n") + "\n"
	inv := &PiInvoker{
		RuntimePath: "/repo/runtimes/pi/src/main.mjs", Cwd: "/repo", Store: st,
		Exec: func(context.Context, []string, string, []string, string) (piRunResult, error) {
			return piRunResult{Stdout: events, ExitCode: 1}, nil
		},
	}
	_, err := inv.Diagnose(context.Background(), piTestRequest())
	var pe *PiDiagnoseError
	if !errors.As(err, &pe) {
		t.Fatalf("认证失败必须带类别，得到 %T %v", err, err)
	}
	if pe.Category != "auth" {
		t.Fatalf("category=%q，期望 auth", pe.Category)
	}
	if !strings.Contains(pe.Error(), "journal=") {
		t.Fatalf("错误信息应带 journal 便于取证：%s", pe.Error())
	}
}

func TestPiDiagnoseInvalidStreamIsInvalidOutput(t *testing.T) {
	cases := map[string]string{
		"没有终态": `{"contract":"maw.pi.execution/v1","type":"session_started","payload":{"mode":"diagnose"}}` + "\n",
		"坏行":   "not-json\n",
		"模式不符": strings.Join([]string{
			`{"contract":"maw.pi.execution/v1","type":"version_report","payload":{"mode":"worker","protocol":{"format":"pi-stream-json","version":"1"}}}`,
		}, "\n") + "\n",
		"契约不符": `{"contract":"other/v9","type":"result","payload":{"ok":true,"text":"{}"}}` + "\n",
	}
	for name, stdout := range cases {
		t.Run(name, func(t *testing.T) {
			inv := &PiInvoker{
				RuntimePath: "/repo/runtimes/pi/src/main.mjs", Cwd: "/repo",
				Exec: func(context.Context, []string, string, []string, string) (piRunResult, error) {
					return piRunResult{Stdout: stdout}, nil
				},
			}
			_, err := inv.Diagnose(context.Background(), piTestRequest())
			var pe *PiDiagnoseError
			if !errors.As(err, &pe) {
				t.Fatalf("非法结构必须是 PiDiagnoseError，得到 %T %v", err, err)
			}
			if pe.Category != "invalid_output" {
				t.Fatalf("category=%q，期望 invalid_output（%s）", pe.Category, name)
			}
		})
	}
}

func TestPiDiagnoseUnknownEventsAreCountedNotGuessed(t *testing.T) {
	stdout := strings.Join([]string{
		`{"contract":"maw.pi.execution/v1","type":"version_report","payload":{"mode":"diagnose","protocol":{"format":"pi-stream-json","version":"1"}}}`,
		`{"contract":"maw.pi.execution/v1","type":"mystery","payload":{}}`,
		`{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":true,"text":"{\"action\":\"notify_only\",\"reason\":\"x\"}"}}`,
	}, "\n") + "\n"
	stream := parsePiDiagnoseStream(stdout)
	if stream.UnknownTypes != 1 {
		t.Fatalf("未知类型应计数为 1，得到 %d", stream.UnknownTypes)
	}
	if !stream.OK || !stream.ResultSeen {
		t.Fatalf("终态应被识别：%+v", stream)
	}
}

func TestPiDiagnoseUsageWithoutTokensIsNotZero(t *testing.T) {
	stdout := strings.Join([]string{
		`{"contract":"maw.pi.execution/v1","type":"usage","call_id":"pc_2","payload":{"scope":"call","semantic":"delta","input_tokens":null,"output_tokens":null,"quality":"unknown"}}`,
	}, "\n") + "\n"
	stream := parsePiDiagnoseStream(stdout)
	if len(stream.Usage) != 0 {
		t.Fatalf("没有 token 的调用不得写用量行（0 不等于未知）：%+v", stream.Usage)
	}
}

// 端到端：编排器领到作业 → Pi 诊断（只读）→ 决策仍走 ParseDecision 与动作策略
// → 作业落终态，用量与作业用同一个 run_id 对上。
func TestOrchestratorRunsPiDiagnoseAndAttributesUsage(t *testing.T) {
	st, _ := newPiTestStore(t)
	seedSystemJob(t, st, "p1")
	inv := &PiInvoker{
		NodePath: "node", RuntimePath: "/repo/runtimes/pi/src/main.mjs",
		Cwd: "/repo", NodeID: "pi-1", Store: st,
		Exec: func(context.Context, []string, string, []string, string) (piRunResult, error) {
			return piRunResult{Stdout: piEvents("run_diag_" + piJobID)}, nil
		},
	}
	orch := &Orchestrator{
		Store:       st,
		Config:      config.SystemAgent{AutoKinds: []string{SystemKindUnblock}, MaxConcurrent: 1},
		Invoker:     inv,
		Effects:     NopEffects{},
		WorkerID:    "test-worker",
		ResolvePath: func(string) (string, error) { return "/repo", nil },
	}
	// 作业是 queued：RunJobID 会自己置 running 再跑，避免与并发领取抢时序。
	if err := orch.RunJobID(context.Background(), piJobID); err != nil {
		t.Fatalf("RunJobID: %v", err)
	}
	var status, jobRun string
	var decision []byte
	err := st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT status, run_id, decision_json FROM system_jobs WHERE job_id = ?`, piJobID).
			Scan(&status, &jobRun, &decision)
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != db.SystemJobEscalated {
		t.Fatalf("派工类决策应落 escalated，得到 %s", status)
	}
	if !strings.Contains(string(decision), "escalate_human") {
		t.Fatalf("作业应记录解析后的决策：%s", decision)
	}
	if jobRun != diagnoseRunPrefix+sanitize(piJobID) {
		t.Fatalf("作业 run_id=%q 应与用量一致", jobRun)
	}
	if err := st.Read(func(tx *sql.Tx) error {
		var n int
		if e := tx.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE purpose = 'diagnose' AND run_id = ?`, jobRun).Scan(&n); e != nil {
			return e
		}
		if n == 0 {
			t.Fatalf("作业 %s 的 run_id 下没有诊断用量", jobRun)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
