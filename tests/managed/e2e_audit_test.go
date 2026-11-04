// GAP-010：受管真实 Run 的端到端验收。
//
// 一条受控链路把三件事同时钉住：
//  1. 真实 Run 起止：库里的 runs 有 started_at／finished_at，run_events 有阶段推进；
//  2. 盘上留痕：op 目录里有 journal（journal.jsonl）与执行后的 report.json；
//  3. 作业可反查：system_jobs 通过 run_id／operation_id／journal_dir 对得上这次执行。
//
// 来源进程用 tests/managed/testdata/fake-codebuddy.sh（synthetic fixture，不是厂商现场实测）。
// 受管 Run 依赖 macOS 的进程组与文件锁语义，非 darwin 环境按跳过处理（见 requireDarwin）。
package managed_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/process"
)

// skipReason 决定当前平台是否跳过受管 E2E。
//
// 跳过要留下可读原因：CI 日志里 `--- SKIP` 与 `--- FAIL` 因此能分开看。
func skipReason(goos string) (string, bool) {
	if goos == "darwin" {
		return "", false
	}
	return fmt.Sprintf("受管真实 Run 需要 macOS 的进程组与文件锁语义（当前 %s）；本用例记为跳过而不是失败", goos), true
}

func requireDarwin(t *testing.T) {
	t.Helper()
	if reason, skip := skipReason(runtime.GOOS); skip {
		t.Skip(reason)
	}
}

// TestDarwinGuardClassifiesSkipNotFailure 在任意平台上验证守卫的判定口径本身。
func TestDarwinGuardClassifiesSkipNotFailure(t *testing.T) {
	if reason, skip := skipReason("darwin"); skip || reason != "" {
		t.Fatalf("darwin 不应跳过：skip=%v reason=%q", skip, reason)
	}
	for _, goos := range []string{"linux", "windows", "freebsd"} {
		reason, skip := skipReason(goos)
		if !skip {
			t.Fatalf("%s 应判为跳过（而非失败）", goos)
		}
		if !strings.Contains(reason, goos) {
			t.Fatalf("%s 的跳过原因没写清平台：%q", goos, reason)
		}
	}
}

// TestManagedRunEndToEndAudit 跑一次受管 Run，核对库与盘上的全部事实。
func TestManagedRunEndToEndAudit(t *testing.T) {
	requireDarwin(t)
	h := newHarness(t)
	store, err := db.OpenStore(h.gdb)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	counts := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		for _, table := range []string{"runs", "run_events", "operations", "system_jobs"} {
			var n int
			if err := store.Read(func(tx *sql.Tx) error {
				return tx.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
			}); err != nil {
				t.Fatalf("count %s: %v", table, err)
			}
			out[table] = n
		}
		return out
	}
	before := counts()

	// 节点与领取：Run／Operation 由真实的 Claim 建立。
	if err := store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: "codebuddy-lab", ProjectID: h.pid, SlotLimit: 1})
	}); err != nil {
		t.Fatal(err)
	}
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
	ws, err := filepath.Abs(filepath.Join(h.global, "ws-gap010"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := claim.New(store, nil).Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:      model.ProjectID(h.pid),
			AgentID:        "cursor-managed-1",
			RequestedTask:  &inst,
			IdempotencyKey: "m:gap010",
			Capacity:       ports.Capacity{NodeSlotLimit: 1, ManagedLimit: 1},
		},
		NodeID:    "codebuddy-lab",
		Workspace: ws,
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	runID := string(out.RunID)
	opID := string(out.OperationID)
	if runID == "" || opID == "" {
		t.Fatalf("claim 结果缺少 run/op: %+v", out)
	}

	// Runner 只读文件：元数据、启动规格与引导令牌都必须先落盘。
	state := filepath.Join(h.global, "runtime", h.pid)
	opDir := filepath.Join(state, "ops", opID)
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := app.RunMeta{
		OperationID: opID,
		RunID:       runID,
		InstanceID:  string(inst),
		Generation:  int(out.Generation),
		ProjectID:   h.pid,
		GlobalDB:    h.gdb,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}
	writeJSONFile(t, filepath.Join(opDir, "run_meta.json"), meta)
	spec := model.LaunchSpec{
		Executable:       h.fakeCLI,
		Argv:             []string{"--print", "managed"},
		Cwd:              h.repo,
		StdinMode:        model.StdinNone,
		ExpectedProtocol: model.Protocol{Format: "stream-json", Version: codebuddy.ProtocolVersion},
	}
	writeJSONFile(t, filepath.Join(opDir, "launch_spec.json"), spec)
	nonce, err := process.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	token, err := process.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(opDir, "bootstrap.token"), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 与生产同一条 Runner 入口：Once 模式下跑完一次真实子进程即返回。
	res, err := app.InternalRunner(app.RunnerOpts{
		OperationID: opID,
		Nonce:       nonce,
		StateDir:    state,
		Token:       token,
		Once:        true,
	})
	if err != nil {
		t.Fatalf("runner: %v", err)
	}
	if wired, _ := res["session_wired"].(bool); !wired {
		t.Fatalf("Runner 没接上 session（launch_spec 未被识别）: %+v", res)
	}
	reportPath, _ := res["report_path"].(string)
	if reportPath == "" {
		t.Fatalf("执行后没有 report.json: %+v", res)
	}
	if got, _ := res["session_outcome"].(string); got != string(model.OutcomeCompleted) {
		t.Fatalf("session_outcome=%q 期望 completed: %+v", got, res)
	}

	// 1. 盘上留痕：journal 与 report 都在 op 目录里。
	if _, err := os.Stat(filepath.Join(opDir, "journal", "journal.jsonl")); err != nil {
		t.Fatalf("op 目录缺少 journal: %v", err)
	}
	rep, err := journal.ReadReport(opDir)
	if err != nil {
		t.Fatalf("读 report: %v", err)
	}
	if rep.RunID != runID || rep.OperationID != opID {
		t.Fatalf("report 身份不对: run=%q op=%q", rep.RunID, rep.OperationID)
	}
	if rep.Outcome != string(model.OutcomeCompleted) || !rep.Finished {
		t.Fatalf("report 事实不对: outcome=%q finished=%v", rep.Outcome, rep.Finished)
	}

	// 2. 库里的 Run 起止与阶段推进。
	var run *db.Run
	if err := store.Read(func(tx *sql.Tx) error {
		run, _ = db.GetRun(tx, runID)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatalf("run %s 不存在", runID)
	}
	if run.StartedAt == "" || run.FinishedAt == "" {
		t.Fatalf("run 缺起止时刻: started=%q finished=%q phase=%s", run.StartedAt, run.FinishedAt, run.Phase)
	}
	var phases []string
	if err := store.Read(func(tx *sql.Tx) error {
		events, e := db.ReadRunEvents(tx, h.pid, 0, 200)
		if e != nil {
			return e
		}
		for _, ev := range events {
			if ev.Kind == "run.phase" && ev.RunID == runID {
				phases = append(phases, string(ev.Phase))
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(phases) < 2 {
		t.Fatalf("run_events 阶段推进不完整: %v", phases)
	}

	// 3. 作业反查：system_jobs 记下这次 Run／操作／日志目录，且日志目录在盘上存在。
	journalDir := filepath.Join(opDir, "journal")
	var job *db.SystemJob
	if err := store.Write(func(tx *sql.Tx) error {
		var e error
		job, e = db.InsertSystemJob(tx, &db.SystemJob{
			JobID:          "sjob_e2e_audit",
			ProjectID:      h.pid,
			Kind:           db.SystemKindDispatchReady,
			Reason:         "GAP-010 端到端验收",
			TriggerEventID: 0,
			MaxAttempts:    3,
		})
		return e
	}); err != nil {
		t.Fatalf("插入作业: %v", err)
	}
	if err := store.Write(func(tx *sql.Tx) error {
		return db.SetSystemJobRun(tx, job.JobID, runID, opID, journalDir)
	}); err != nil {
		t.Fatalf("写作业反查链接: %v", err)
	}
	if err := store.Read(func(tx *sql.Tx) error {
		got, e := db.GetSystemJob(tx, job.JobID)
		if e != nil {
			return e
		}
		if got == nil {
			t.Fatalf("作业 %s 读不回来", job.JobID)
		}
		if got.RunID != runID || got.OperationID != opID {
			t.Fatalf("作业未关联本次 Run: %+v", got)
		}
		if got.JournalDir != journalDir {
			t.Fatalf("作业日志目录 = %q，期望 %q", got.JournalDir, journalDir)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(journalDir); err != nil {
		t.Fatalf("作业指向的日志目录不存在: %v", err)
	}

	after := counts()
	receipt := e2eReceipt{
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		GoVersion: runtime.Version(),
		Fixture:   "tests/managed/testdata/fake-codebuddy.sh（synthetic fixture，非厂商实测）",
		RunID:     runID,
		OpID:      opID,
		Before:    before,
		After:     after,
		OpFiles:   listFiles(t, opDir),
		Report:    reportPath,
		Phases:    phases,
	}
	if err := writeReceipt(t, receipt); err != nil {
		t.Fatalf("写验收报告: %v", err)
	}
}

type e2eReceipt struct {
	GOOS, GOARCH, GoVersion string
	Fixture                 string
	RunID, OpID             string
	Before, After           map[string]int
	OpFiles                 []string
	Report                  string
	Phases                  []string
}

// writeReceipt 把本次验收的事实写成 Markdown。
//
// 设置了 MAW_E2E_REPORT 才落盘（默认 CI 只断言、不写仓库文件）；
// 生成报告的命令见 README 的「受管 Run 端到端验收」一节。
func writeReceipt(t *testing.T, r e2eReceipt) error {
	t.Helper()
	path := strings.TrimSpace(os.Getenv("MAW_E2E_REPORT"))
	if path == "" {
		t.Logf("E2E 事实：run=%s op=%s phases=%v op_files=%v", r.RunID, r.OpID, r.Phases, r.OpFiles)
		return nil
	}
	// 相对路径按仓库根解析：go test 的工作目录是包目录，不是仓库根。
	if !filepath.IsAbs(path) {
		_, thisFile, _, _ := runtime.Caller(0)
		root := filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
		path = filepath.Join(root, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# 受管真实 Run 端到端验收（GAP-010）\n\n")
	b.WriteString("由 `tests/managed/e2e_audit_test.go` 生成；样本来源是受控 CLI 脚本，" +
		"只证明装配与留痕，不代表厂商现场实测。\n\n")
	b.WriteString("## 命令\n\n```sh\nCGO_ENABLED=0 go test ./tests/managed -count=1 -run TestManagedRunEndToEndAudit\n" +
		"CGO_ENABLED=0 go test ./tests/managed -count=1   # 默认 CI 全量\n```\n\n")
	b.WriteString("## 环境\n\n")
	b.WriteString(fmt.Sprintf("- 平台：%s/%s\n- Go：%s\n", r.GOOS, r.GOARCH, r.GoVersion))
	b.WriteString("- 隔离：临时 `MAW_GLOBAL` + 临时 Git 仓库（不触碰真实状态库）\n")
	b.WriteString("- 来源：`" + r.Fixture + "`\n")
	b.WriteString(fmt.Sprintf("- Run：`%s`；Operation：`%s`\n\n", r.RunID, r.OpID))
	b.WriteString("## 库行数前后差\n\n")
	b.WriteString("| 表 | 执行前 | 执行后 | 差 |\n|---|---:|---:|---:|\n")
	tables := make([]string, 0, len(r.After))
	for name := range r.After {
		tables = append(tables, name)
	}
	sort.Strings(tables)
	for _, name := range tables {
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %+d |\n", name, r.Before[name], r.After[name], r.After[name]-r.Before[name]))
	}
	b.WriteString("\n## op 目录文件列表\n\n```\n")
	for _, f := range r.OpFiles {
		b.WriteString(f + "\n")
	}
	b.WriteString("```\n\n")
	b.WriteString(fmt.Sprintf("report.json 路径（本次运行在临时目录，测试结束即清理）：`%s`\n\n", r.Report))
	b.WriteString("阶段推进（run_events）：" + "`" + strings.Join(r.Phases, "` → `") + "`\n\n")
	b.WriteString("## Darwin 守卫\n\n")
	b.WriteString("非 macOS 环境下本用例调用 `t.Skip`，`go test` 输出 `--- SKIP` 而不是 `--- FAIL`；\n")
	b.WriteString("`TestDarwinGuardClassifiesSkipNotFailure` 在任意平台校验这条判定口径。\n")
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// listFiles 列出 op 目录下的文件（相对路径，排序后输出）。
func listFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("列出 op 目录: %v", err)
	}
	sort.Strings(files)
	return files
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}
