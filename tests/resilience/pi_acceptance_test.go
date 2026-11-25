package resilience

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/pi"
	"github.com/kiwh77/maw/internal/analytics"
	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/projection"
	"github.com/kiwh77/maw/internal/runner/dbstore"
	"github.com/kiwh77/maw/internal/runner/journal"
	"github.com/kiwh77/maw/internal/runner/observe"
	"github.com/kiwh77/maw/internal/runner/session"
	"github.com/kiwh77/maw/tests/testsupport"
)

// PI-901：Pi 能力端到端验收（隔离工程，不触碰真实主干）。
//
// 两条边界先说清：
//   - 真实供应商 API 需要授权凭据：本机没有凭据时，用**替换运行时**（本文件生成的
//     node 脚本）跑真实进程、真实 journal、真实 SQLite 与真实适配器；供应商响应
//     本身记入「未验项」（docs/verification/pi/acceptance.zh-CN.md）。
//   - 被替换的只有模型流：协议信封、模式回执、事件边界、终态判定、用量归一、
//     去重与归属全部走生产代码。
//
// 覆盖：Worker 会话与逐次用量、目标规划／批准入队／返工、429／断流／缓存／
// 重复回放／取消与恢复的受控样本。

// piFakeRuntime 写一个替换运行时并返回 (node 可执行路径, 入口脚本绝对路径)。
//
// 入口放在 <repo>/runtimes/pi/src/main.mjs：与节点登记识别规则一致，
// 这样「登记 → 探测 → 受管启动」读的是同一份路径，不需要对生产代码做特殊照顾。
func piFakeRuntime(t *testing.T, repo, scenario string) (string, string) {
	t.Helper()
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("需要 node（runtimes/pi 的运行前提）")
	}
	entry := filepath.Join(repo, "runtimes", "pi", "src", "main.mjs")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte(piFakeRuntimeSource), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_SCENARIO", scenario)
	return nodePath, entry
}

// piFakeRuntimeSource 是替换运行时的源码。
//
// 只做三件事：按 mode 回显协议与模式、发一条逐次调用用量、给出 result 终态；
// 场景由 PI_SCENARIO 选择（ok／rate_limit／stream_break／rework）。
const piFakeRuntimeSource = `
import process from 'node:process';

const chunks = [];
for await (const c of process.stdin) chunks.push(c);
const raw = Buffer.concat(chunks).toString('utf8').trim();

if (process.argv.includes('--probe')) {
  process.stdout.write(JSON.stringify({
    ok: true, protocol: { format: 'pi-stream-json', version: '1' },
    node: process.version, sdk: '1.5.2',
  }) + '\n');
  process.exit(0);
}

const cfg = raw ? JSON.parse(raw) : {};
const mode = cfg.mode || 'worker';
const scenario = (cfg.model && String(cfg.model).includes('rework'))
  ? 'rework'
  : (process.env.PI_SCENARIO || 'ok');
const runId = cfg.run_id || 'run_pi';
const now = new Date().toISOString();
let seq = 0;
const emit = (type, payload, callId = '') => {
  process.stdout.write(JSON.stringify({
    contract: 'maw.pi.execution/v1', event_id: 'pe_' + (++seq), type,
    run_id: runId, generation: cfg.generation || 1, call_id: callId,
    occurred_at: now, received_at: now, payload,
  }) + '\n');
};

emit('version_report', { sdk_version: '1.5.2', mode, protocol: { format: 'pi-stream-json', version: '1' } });
emit('session_started', { mode, cwd: cfg.cwd });

if (scenario === 'rate_limit') {
  emit('error', { category: 'rate_limit', message: '429 too many requests', retryable: true });
  emit('result', { ok: false, outcome: 'failed', reason: 'rate_limit', text: '', evidence: ['category:rate_limit'] });
  process.exit(1);
}
if (scenario === 'stream_break') {
  // 断流：只发出半条调用事实就退出，没有 result。
  emit('usage', { scope: 'call', semantic: 'delta', input_tokens: 10, output_tokens: null, quality: 'unknown' }, 'pc_broken');
  process.exit(1);
}

emit('usage', {
  scope: 'call', semantic: 'delta', quality: 'observed',
  input_tokens: 120, output_tokens: 30, cache_read_tokens: 40,
  cache_write_tokens: null, provider: 'fake', model: 'pi-fixed',
}, 'pc_1');

const initialCards = [
  {
    card_id: 'pa-101', id: 'PA-101', title: 'Pi 第一张卡',
    goal: '现状：没有实现。目标：实现 mod_a。',
    priority: 10, merge_order: 10, deps: [],
    write_scope: ['mod_a/**'], read_scope: [],
    acceptance: ['manual: 人工复核|mod_a 行为符合验收'],
    verify: ['go test ./mod_a/...'],
  },
  {
    card_id: 'pa-102', id: 'PA-102', title: 'Pi 第二张卡',
    goal: '现状：没有实现。目标：实现 mod_b。',
    priority: 20, merge_order: 20, deps: ['PA-101'],
    write_scope: ['mod_b/**'], read_scope: [],
    acceptance: ['manual: 人工复核|mod_b 行为符合验收'],
    verify: ['go test ./mod_b/...'],
  },
];
const reworkCards = [{
  card_id: 'pa-201', id: 'PA-201', title: 'Pi 返工卡',
  goal: '现状：整体验收失败。目标：修复失败断言 AC-1。',
  priority: 5, merge_order: 5, deps: [],
  write_scope: ['mod_a/**'], read_scope: [],
  acceptance: ['manual: 人工复核|AC-1 重新通过'],
  verify: ['go test ./mod_a/...'],
}];

let text = 'PI-WORKER-OK';
if (mode === 'lead') {
  text = JSON.stringify(scenario === 'rework' ? reworkCards : initialCards);
} else if (mode === 'diagnose') {
  text = '{"action":"escalate_human","reason":"pi acceptance diagnose"}';
}
emit('result', { ok: true, outcome: 'completed', reason: 'result', text, evidence: ['stop_reason:stop'] });
emit('session_ended', { reason: 'settled', stop_reason: 'stop', aborted: false });
`

// piRecordingStore 只记录运行侧提交的操作：本用例验的是进程、journal 与观测落库，
// 业务状态写入属于别的卡；观测走 dbstore，不经这个端口。
type piRecordingStore struct {
	ops []ports.StoreOp
}

func (s *piRecordingStore) Apply(_ context.Context, req ports.StoreRequest) (ports.StoreResult, error) {
	s.ops = append(s.ops, req.Ops...)
	return ports.StoreResult{Cursor: model.Cursor(len(s.ops)), Revision: 1}, nil
}

// TestPiWorkerSessionWritesAttributedUsage 验收第一条机检落点：
// 真实子进程 → 真实 Pi 适配器 → 真实 journal/SQLite，逐次调用用量按
// 「worker + run + 节点」归属；同一调用重放不会记第二笔。
func TestPiWorkerSessionWritesAttributedUsage(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	nodePath, entry := piFakeRuntime(t, h.Repo, "ok")

	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	adapter, err := pi.New(model.NodeConfig{
		NodeID: "pi-1", Name: "pi-acceptance", Adapter: model.AdapterPi,
		EntryPath: entry, RuntimePath: nodePath,
	})
	if err != nil {
		t.Fatalf("Pi 适配器: %v", err)
	}
	jr, err := journal.Open(t.TempDir(), journal.Options{RunID: "run_pi901", Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	defer jr.Close()

	runner, err := session.New(&piRecordingStore{}, adapter, jr,
		session.WithObserver(&dbstore.Store{DB: store, ProjectID: h.PID}))
	if err != nil {
		t.Fatal(err)
	}
	cfg := piWorkerConfig(t, h, nodePath, entry, "run_pi901", "ti_pi901w")
	for attempt := 1; attempt <= 2; attempt++ {
		rep, err := runner.Run(context.Background(), cfg)
		if err != nil {
			t.Fatalf("第 %d 次运行失败: %v", attempt, err)
		}
		if rep.Outcome != model.OutcomeCompleted || !rep.Finished || rep.Phase != model.PhaseCompleted {
			t.Fatalf("第 %d 次应落 completed，得到 outcome=%s finished=%v phase=%s",
				attempt, rep.Outcome, rep.Finished, rep.Phase)
		}
		if rep.Observed == 0 {
			t.Fatalf("第 %d 次没有落任何观测：用量缺失不能被当成「没有用量」", attempt)
		}
	}

	var purpose, runID, projectID, nodeID, sourceNS string
	var input, output, cacheRead sql.NullInt64
	var rows int
	if err := store.Read(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE run_id = ?`, "run_pi901").Scan(&rows); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT source_ns, purpose, run_id, project_id, node_id,
  input_tokens, output_tokens, cache_read_tokens
FROM usage_records WHERE run_id = ?`, "run_pi901").
			Scan(&sourceNS, &purpose, &runID, &projectID, &nodeID, &input, &output, &cacheRead)
	}); err != nil {
		t.Fatalf("读用量: %v", err)
	}
	if rows != 1 {
		t.Fatalf("同一调用重放后用量行数=%d，期望 1（去重身份不能变）", rows)
	}
	if purpose != observe.PurposeWorker || sourceNS != observe.PiSourceNS {
		t.Fatalf("归属不对: purpose=%s source=%s", purpose, sourceNS)
	}
	if projectID != h.PID || nodeID != "pi-1" || runID != "run_pi901" {
		t.Fatalf("归属不对: project=%s node=%s run=%s", projectID, nodeID, runID)
	}
	if !input.Valid || !output.Valid || input.Int64 != 120 || output.Int64 != 30 {
		t.Fatalf("逐次调用 token 不对: in=%v out=%v", input, output)
	}
	if !cacheRead.Valid || cacheRead.Int64 != 40 {
		t.Fatalf("缓存字段必须独立记账: %v", cacheRead)
	}

	// 统计层核对（看板读的同一份报表）：用途维度能看到 worker 的真实用量，
	// 调用分母来自 run↔轮次关联，不是「有记账的行数」。
	window := projection.Window{Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour)}
	var attribution *analytics.AttributionReport
	if err := store.Read(func(tx *sql.Tx) error {
		var e error
		attribution, e = (&analytics.Service{}).Attribution(tx, analytics.AttributionQuery{
			Query:     analytics.Query{ProjectID: h.PID, Window: window},
			Dimension: analytics.DimensionPurpose,
		}, nil, projection.Cursors{Completeness: projection.CompletenessComplete})
		return e
	}); err != nil {
		t.Fatalf("归属统计: %v", err)
	}
	if len(attribution.Rollups) != 1 || attribution.Rollups[0].Value != observe.PurposeWorker {
		t.Fatalf("用途维度应只有 worker 一行: %+v", attribution.Rollups)
	}
	row := attribution.Rollups[0]
	if row.Tokens.InputTokens == nil || *row.Tokens.InputTokens != 120 ||
		row.Tokens.OutputTokens == nil || *row.Tokens.OutputTokens != 30 {
		t.Fatalf("统计层 token 不对: %+v", row.Tokens)
	}
	if row.UsageObserved.Denominator != 1 || row.UsageObserved.Numerator != 1 {
		t.Fatalf("调用覆盖应是 1/1，得到 %+v", row.UsageObserved)
	}
}

// piWorkerConfig 是一次 Pi Worker 运行的冻结输入：配置走 stdin，协议走 pi-stream-json/1。
func piWorkerConfig(t *testing.T, h *testsupport.Harness, nodePath, entry, runID, instanceID string) session.Config {
	t.Helper()
	stdin, err := json.Marshal(map[string]any{
		"mode": "worker", "cwd": h.Repo, "run_id": runID, "generation": 1,
		"write_scope": []string{"mod_a/**"}, "allow_bash": false, "prompt": "实现 mod_a",
	})
	if err != nil {
		t.Fatal(err)
	}
	return session.Config{
		Instance: model.InstanceID(instanceID), RunID: model.RunID(runID), Generation: 1,
		Protocol:  model.Protocol{Format: pi.ProtocolFormat, Version: pi.ProtocolVersion},
		Heartbeat: 20 * time.Millisecond,
		ProjectID: h.PID, NodeID: "pi-1", Purpose: observe.PurposeWorker,
		Spec: model.LaunchSpec{
			Executable:   nodePath,
			Argv:         []string{entry},
			Cwd:          h.Repo,
			StdinMode:    model.StdinPipe,
			StdinData:    string(stdin),
			EnvAllowlist: []string{"HOME", "PATH", "TMPDIR", "PI_SCENARIO"},
			ExpectedProtocol: model.Protocol{
				Format: pi.ProtocolFormat, Version: pi.ProtocolVersion,
			},
		},
	}
}

// TestPiLeadPlanApproveEnqueueAndRework 覆盖「目标 → 计划 → 批准入队 → 失败 → 返工」。
//
// 返工阶段的硬约束（契约 §12）：旧修订不可变、蓝图不因「提案」而变化、
// 已在执行的题不被改写；返工卡只有再次批准才入队。
func TestPiLeadPlanApproveEnqueueAndRework(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	nodePath, entry := piFakeRuntime(t, h.Repo, "ok")
	t.Setenv("MAW_PI_NODE", nodePath)
	t.Setenv("MAW_PI_RUNTIME", entry)
	t.Setenv("MAW_AGENT", "pi-accept-1")
	// 模型名是允许进入运行时的白名单变量：用它切换「首轮／返工」两种计划输出，
	// 不给运行时代理开任何额外通道。
	t.Setenv("PI_MODEL", "pi-fixed")
	setTOMLValue(t, h.Repo, "identity", "leads", `["pi-accept-1"]`)

	goalID := "gl_0123456789ab"
	plan, err := app.LeadGoalPlan(h.Repo, app.LeadGoalPlanInput{
		GoalID: goalID, Title: "Pi 闭环目标",
		Statement:  "现状：没有 Pi 端到端证据。目标：规划、入队、返工与计量都留痕。",
		Acceptance: []string{"evidence: delivery|全部映射任务已交付"},
		MaxCards:   4,
	})
	if err != nil {
		t.Fatalf("规划失败: %v", err)
	}
	if plan["plan_rev"] != 1 || plan["status"] != "proposed" {
		t.Fatalf("首份计划应是 proposed rev1：%v", plan)
	}

	if _, err := app.LeadGoalApprove(h.Repo, app.LeadGoalApproveInput{
		GoalID: goalID, PlanRev: 1, Policy: "auto_enqueue", AutoEnqueue: true,
	}); err != nil {
		t.Fatalf("批准入队失败: %v", err)
	}
	if !piHasTask(t, h, "PA-101") || !piHasTask(t, h, "PA-102") {
		t.Fatalf("批准入队后两张卡都应存在")
	}
	blueprintBefore := piReadBlueprint(t, h.Repo)
	firstDigest := piPlanDigest(t, h, goalID, 1)

	// 预置集成失败：映射任务尚未交付，整体验收按持久化证据判失败。
	accept, err := app.LeadGoalAccept(h.Repo, app.LeadGoalAcceptInput{GoalID: goalID})
	if err != nil {
		t.Fatalf("整体验收失败: %v", err)
	}
	if accept["overall"] != "fail" {
		t.Fatalf("验收结论应为 fail：%v", accept["overall"])
	}

	t.Setenv("PI_MODEL", "pi-fixed-rework")
	rework, err := app.LeadGoalRework(h.Repo, app.LeadGoalReworkInput{GoalID: goalID})
	if err != nil {
		t.Fatalf("返工规划失败: %v", err)
	}
	if rework["plan_rev"] != 2 {
		t.Fatalf("返工应产出 rev2：%v", rework["plan_rev"])
	}
	// 提案阶段不许改蓝图、不许创建返工卡、旧修订不可变。
	if got := piReadBlueprint(t, h.Repo); got != blueprintBefore {
		t.Fatalf("返工提案不得改写既有蓝图")
	}
	if piHasTask(t, h, "PA-201") {
		t.Fatalf("返工卡只有再次批准后才入队")
	}
	if got := piPlanDigest(t, h, goalID, 1); got != firstDigest {
		t.Fatalf("旧修订被改动：%s → %s", firstDigest, got)
	}
	if !piHasTask(t, h, "PA-101") {
		t.Fatalf("返工不得改写已在执行的题")
	}

	// 再次批准返工修订：只追加一张卡，原卡保留。
	if _, err := app.LeadGoalApprove(h.Repo, app.LeadGoalApproveInput{
		GoalID: goalID, PlanRev: 2, Policy: "auto_enqueue", AutoEnqueue: true,
	}); err != nil {
		t.Fatalf("返工批准入队失败: %v", err)
	}
	if !piHasTask(t, h, "PA-201") {
		t.Fatalf("返工卡应在批准后入队")
	}
	if !piHasTask(t, h, "PA-101") || !piHasTask(t, h, "PA-102") {
		t.Fatalf("返工入队不得删除原有卡")
	}
}

func piHasTask(t *testing.T, h *testsupport.Harness, taskID string) bool {
	t.Helper()
	found := false
	if err := db.WithReadTx(h.GDB, func(tx *sql.Tx) error {
		task, err := db.GetTask(tx, h.PID, taskID)
		if err != nil {
			return err
		}
		found = task != nil
		return nil
	}); err != nil {
		t.Fatalf("读任务: %v", err)
	}
	return found
}

func piReadBlueprint(t *testing.T, repo string) string {
	t.Helper()
	local, err := home.MawHome(repo)
	if err != nil {
		t.Fatalf("maw home: %v", err)
	}
	raw, err := os.ReadFile(home.BlueprintPath(local))
	if err != nil {
		t.Fatalf("读蓝图: %v", err)
	}
	return string(raw)
}

func piPlanDigest(t *testing.T, h *testsupport.Harness, goalID string, rev int) string {
	t.Helper()
	var digest string
	if err := db.WithReadTx(h.GDB, func(tx *sql.Tx) error {
		plan, err := db.GetPlanRevision(tx, h.PID, goalID, rev)
		if err != nil {
			return err
		}
		if plan == nil {
			return fmt.Errorf("修订 %d 不存在", rev)
		}
		digest = plan.PlanDigest
		return nil
	}); err != nil {
		t.Fatalf("读计划修订: %v", err)
	}
	return digest
}

// TestPiControlledSamples 用可控样本覆盖 429／断流／缓存／取消与重复回放。
//
// 样本是事件流，不是录制的供应商响应：替换运行时只决定「发什么」，
// 判定、归一与去重仍是生产实现。
func TestPiControlledSamples(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	nodePath, entry := piFakeRuntime(t, h.Repo, "ok")
	adapter, err := pi.New(model.NodeConfig{
		NodeID: "pi-1", Adapter: model.AdapterPi, EntryPath: entry, RuntimePath: nodePath,
	})
	if err != nil {
		t.Fatal(err)
	}

	interpret := func(t *testing.T, finalResult string, exitCode *int) model.InterpretResult {
		t.Helper()
		req := ports.InterpretRequest{
			FinalResult: []byte(finalResult),
			ExitCode:    exitCode,
		}
		res, err := adapter.Interpret(context.Background(), req)
		if err != nil {
			t.Fatalf("Interpret: %v", err)
		}
		return res
	}

	// 429：结果说失败，category 保留在证据里，不得被判成功。
	rateLimited := `{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":false,"outcome":"failed","reason":"rate_limit","evidence":["category:rate_limit"]}}`
	if got := interpret(t, rateLimited, nil); got.Outcome != model.OutcomeFailed {
		t.Fatalf("429 应判 failed，得到 %s", got.Outcome)
	}

	// 取消：result 说 cancelled，终态必须是 cancelled。
	cancelled := `{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":false,"outcome":"cancelled","reason":"abort_requested"}}`
	if got := interpret(t, cancelled, nil); got.Outcome != model.OutcomeCancelled {
		t.Fatalf("取消应判 cancelled，得到 %s", got.Outcome)
	}

	// 断流：没有 result，只能凭应用错误／非零退出判失败并降级质量。
	code := 1
	if got := interpret(t, "", &code); got.Outcome != model.OutcomeFailed || got.Quality != model.QualityEstimated {
		t.Fatalf("断流应判 failed(estimated)，得到 %s/%s", got.Outcome, got.Quality)
	}
	// 退出码 0 但没有 result：保持 unknown，不猜成功（E333 同源）。
	zero := 0
	if got := interpret(t, "", &zero); got.Outcome != model.OutcomeUnknown {
		t.Fatalf("没有终态应保持 unknown，得到 %s", got.Outcome)
	}

	// 缓存与重复回放：同一 usage 行重复投递只记一次，缓存字段独立且未归一。
	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	extractor, ok := observe.ExtractorFor(model.Protocol{Format: pi.ProtocolFormat, Version: pi.ProtocolVersion})
	if !ok {
		t.Fatal("Pi 协议应能选到观测解释器")
	}
	usageLine := `{"contract":"maw.pi.execution/v1","type":"usage","run_id":"run_samples","call_id":"pc_cache",` +
		`"occurred_at":"2026-11-18T08:00:00Z","received_at":"2026-11-18T08:00:01Z",` +
		`"payload":{"scope":"call","semantic":"delta","quality":"observed","input_tokens":900,` +
		`"output_tokens":null,"cache_read_tokens":300,"cache_write_tokens":null}}`
	facts, ok := extractor.Extract(observe.Record{Payload: []byte(usageLine), ReceivedAt: time.Now().UTC()})
	if !ok || !facts.HasUsage() {
		t.Fatalf("缓存样本应产出用量事实: %+v", facts)
	}
	facts.ProjectID = h.PID
	facts.RunID = "run_samples"
	facts.NodeID = "pi-1"
	facts.Purpose = observe.PurposeWorker
	writer := &dbstore.Store{DB: store, ProjectID: h.PID}
	for i := 0; i < 2; i++ {
		if err := writer.WriteFacts(context.Background(), facts); err != nil {
			t.Fatalf("第 %d 次写观测: %v", i+1, err)
		}
	}
	var rows int
	var cacheState string
	if err := store.Read(func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE run_id = ?`, "run_samples").Scan(&rows); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT cache_state FROM usage_records WHERE run_id = ?`, "run_samples").Scan(&cacheState)
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("重复回放后用量行数=%d，期望 1", rows)
	}
	if cacheState != db.CacheStateUnverified {
		t.Fatalf("缓存归一证据缺失时应记 unverified，得到 %s", cacheState)
	}
}

// TestPiProtocolRejectsOtherSources 是「两条来源不得互相解释」的机检落点：
// 协议名或版本不同即拒绝，不降级成 Passthrough 后照跑。
func TestPiProtocolRejectsOtherSources(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	nodePath, entry := piFakeRuntime(t, h.Repo, "ok")
	adapter, err := pi.New(model.NodeConfig{
		NodeID: "pi-1", Adapter: model.AdapterPi, EntryPath: entry, RuntimePath: nodePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, proto := range []model.Protocol{
		{Format: "stream-json", Version: "1"},
		{Format: pi.ProtocolFormat, Version: "2"},
	} {
		_, err := adapter.Decode(context.Background(), ports.DecodeRequest{
			RecordRef: "sample:1", Protocol: proto, Payload: []byte(`{"type":"result"}`), Cursor: 1,
		})
		if err == nil {
			t.Fatalf("协议 %s/%s 应被拒绝", proto.Format, proto.Version)
		}
		if msg := err.Error(); !strings.Contains(msg, "只支持") && !strings.Contains(msg, "只认版本") {
			t.Fatalf("拒绝原因应可读: %v", err)
		}
	}
}

// flakyObserver 前 failCount 次写库失败，之后透传：模拟中枢短暂不可写。
type flakyObserver struct {
	mu        sync.Mutex
	inner     observe.Writer
	failCount int
	failed    int
}

func (f *flakyObserver) WriteFacts(ctx context.Context, facts observe.Facts) error {
	f.mu.Lock()
	if f.failed < f.failCount {
		f.failed++
		f.mu.Unlock()
		return fmt.Errorf("hub write unavailable")
	}
	f.mu.Unlock()
	return f.inner.WriteFacts(ctx, facts)
}

// TestPiObservationWriteFailureAndBackfill 覆盖「写库失败 → 原文保留 → 补录幂等」。
//
// 观测失败不改运行结论，但必须报出来：没记上不能当成没有；补录重放也只记一笔。
func TestPiObservationWriteFailureAndBackfill(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	nodePath, entry := piFakeRuntime(t, h.Repo, "ok")
	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	adapter, err := pi.New(model.NodeConfig{
		NodeID: "pi-1", Adapter: model.AdapterPi, EntryPath: entry, RuntimePath: nodePath,
	})
	if err != nil {
		t.Fatal(err)
	}
	jr, err := journal.Open(t.TempDir(), journal.Options{RunID: "run_pi901", Sync: true})
	if err != nil {
		t.Fatal(err)
	}
	defer jr.Close()
	observer := &flakyObserver{inner: &dbstore.Store{DB: store, ProjectID: h.PID}, failCount: 2}
	runner, err := session.New(&piRecordingStore{}, adapter, jr, session.WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	cfg := piWorkerConfig(t, h, nodePath, entry, "run_pi901", "ti_pi901w")
	rep, err := runner.Run(context.Background(), cfg)
	if err != nil {
		t.Fatalf("运行失败: %v", err)
	}
	if rep.ObservationFailures == 0 || rep.Observed != 0 {
		t.Fatalf("写库失败必须报出来，且不得声称已观测: failures=%d observed=%d",
			rep.ObservationFailures, rep.Observed)
	}
	if len(rep.ObservationBacklog) == 0 {
		t.Fatal("写库失败应保留待补录位置（原文仍在 journal）")
	}
	if got := piUsageRows(t, store, "run_pi901"); got != 0 {
		t.Fatalf("写库失败时不应有半条用量，得到 %d", got)
	}

	backfill, err := runner.BackfillObservations(context.Background(), cfg)
	if err != nil {
		t.Fatalf("补录失败: %v", err)
	}
	if backfill.Written == 0 {
		t.Fatalf("补录应写入观测: %+v", backfill)
	}
	if got := piUsageRows(t, store, "run_pi901"); got != 1 {
		t.Fatalf("补录后应有 1 条用量，得到 %d", got)
	}
	// 再补一次：幂等，不产生第二条。
	if _, err := runner.BackfillObservations(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if got := piUsageRows(t, store, "run_pi901"); got != 1 {
		t.Fatalf("重复补录后用量行数=%d，期望 1", got)
	}
}

func piUsageRows(t *testing.T, store *db.Store, runID string) int {
	t.Helper()
	var n int
	if err := store.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM usage_records WHERE run_id = ?`, runID).Scan(&n)
	}); err != nil {
		t.Fatalf("读用量: %v", err)
	}
	return n
}

// TestPiConcurrentWorkerRunsKeepUsageSeparate 覆盖并发：两次运行同时进行时，
// 用量按各自 run_id 归属，不互相污染，也不重复记账。
func TestPiConcurrentWorkerRunsKeepUsageSeparate(t *testing.T) {
	h := testsupport.NewHarness(t, "")
	nodePath, entry := piFakeRuntime(t, h.Repo, "ok")
	store, err := db.OpenStore(h.GDB)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	run := func(runID string) error {
		adapter, err := pi.New(model.NodeConfig{
			NodeID: "pi-1", Adapter: model.AdapterPi, EntryPath: entry, RuntimePath: nodePath,
		})
		if err != nil {
			return err
		}
		jr, err := journal.Open(t.TempDir(), journal.Options{RunID: runID, Sync: true})
		if err != nil {
			return err
		}
		defer jr.Close()
		runner, err := session.New(&piRecordingStore{}, adapter, jr,
			session.WithObserver(&dbstore.Store{DB: store, ProjectID: h.PID}))
		if err != nil {
			return err
		}
		cfg := piWorkerConfig(t, h, nodePath, entry, runID, "ti_"+strings.TrimPrefix(runID, "run_"))
		rep, err := runner.Run(context.Background(), cfg)
		if err != nil {
			return err
		}
		if rep.Outcome != model.OutcomeCompleted {
			return fmt.Errorf("%s 未完成: %s", runID, rep.Outcome)
		}
		if rep.ObservationFailures > 0 {
			return fmt.Errorf("%s 观测写库失败 %d 次: %v", runID, rep.ObservationFailures, rep.ObservationBacklog)
		}
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"run_a901c1", "run_a901c2"} {
		wg.Add(1)
		go func(runID string) {
			defer wg.Done()
			errs <- run(runID)
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("并发运行失败: %v", err)
		}
	}
	for _, id := range []string{"run_a901c1", "run_a901c2"} {
		if got := piUsageRows(t, store, id); got != 1 {
			t.Fatalf("%s 的用量行数=%d，期望 1", id, got)
		}
	}
}
