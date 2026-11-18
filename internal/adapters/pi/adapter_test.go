package pi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

type fakeRunner struct {
	res commandResult
	err error
}

func (f fakeRunner) Run(context.Context, string, []string, []string, string) (commandResult, error) {
	return f.res, f.err
}

func probedConfig() model.NodeConfig {
	return model.NodeConfig{
		NodeID: "pi-node", Name: "pi", Adapter: model.AdapterPi,
		EntryPath: "/abs/runtimes/pi/src/main.mjs", RuntimePath: "/abs/node",
		Platform: "darwin", Mode: model.NodeManagedCLI, SlotLimit: 1, AcceptNew: true,
	}
}

func newProbedAdapter(t *testing.T, out string, code int) *Adapter {
	t.Helper()
	ad, err := New(probedConfig(), WithRunner(fakeRunner{res: commandResult{Stdout: []byte(out), Code: code}}))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ad
}

func TestNewRejectsWrongAdapterAndRelativeEntry(t *testing.T) {
	cfg := probedConfig()
	cfg.Adapter = model.AdapterCodeBuddy
	if _, err := New(cfg); err == nil {
		t.Fatal("非 Pi 适配器必须拒绝")
	}
	cfg = probedConfig()
	cfg.EntryPath = "relative/main.mjs"
	if _, err := New(cfg); err == nil {
		t.Fatal("相对入口必须拒绝")
	}
	cfg = probedConfig()
	cfg.RuntimePath = "relative/node"
	if _, err := New(cfg); err == nil {
		t.Fatal("相对 runtime_path 必须拒绝")
	}
}

func TestProbeReadsStructuredSelfReport(t *testing.T) {
	out := `{"ok":true,"protocol":{"format":"pi-stream-json","version":"1"},"node":"v24.22.2","sdk":"1.5.2"}`
	ad := newProbedAdapter(t, out, 0)
	res, err := ad.Probe(context.Background(), probedConfig())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if res.EntryState != model.EntryReady || res.Version != "1.5.2" {
		t.Fatalf("探测结果 = %+v", res)
	}
	if !res.Readiness.Ready() {
		t.Fatalf("就绪四维不完整: %+v", res.Readiness)
	}
	if len(res.Capabilities) == 0 || res.Capabilities[0].Version != "1.5.2" {
		t.Fatalf("能力必须带版本: %+v", res.Capabilities)
	}
	if errs := res.Validate(); len(errs) > 0 {
		t.Fatalf("探测结果应自洽: %v", model.Err(errs))
	}
}

func TestProbeRejectsProtocolMismatchAndBadOutput(t *testing.T) {
	ad := newProbedAdapter(t, `{"ok":true,"protocol":{"format":"stream-json","version":"1"},"sdk":"1.5.2"}`, 0)
	if _, err := ad.Probe(context.Background(), probedConfig()); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("协议不符应报 version_unsupported，得到 %v", err)
	}
	ad = newProbedAdapter(t, "not json", 0)
	if _, err := ad.Probe(context.Background(), probedConfig()); err == nil {
		t.Fatal("非 JSON 输出必须拒绝")
	}
	ad = newProbedAdapter(t, "", 2)
	if _, err := ad.Probe(context.Background(), probedConfig()); err == nil {
		t.Fatal("非零退出码必须拒绝")
	}
}

func decodeReq(payload string) ports.DecodeRequest {
	return ports.DecodeRequest{
		RecordRef: "journal:1:1", Cursor: 3,
		Protocol: model.Protocol{Format: ProtocolFormat, Version: ProtocolVersion},
		Payload:  []byte(payload),
	}
}

func TestDecodeMapsOnlyFactsCarryingRecords(t *testing.T) {
	ad := newProbedAdapter(t, "{}", 0)
	now := time.Now().UTC()
	cases := []struct {
		name      string
		payload   string
		wantKind  string
		wantEvent int
	}{
		{"session_started", `{"contract":"maw.pi.execution/v1","type":"session_started","run_id":"run_a1","occurred_at":"` + now.Format(time.RFC3339Nano) + `"}`, KindTurnStarted, 1},
		{"result", `{"contract":"maw.pi.execution/v1","type":"result","run_id":"run_a1","payload":{"ok":true}}`, KindTurnFinished, 1},
		{"usage", `{"contract":"maw.pi.execution/v1","type":"usage","run_id":"run_a1"}`, "", 0},
		{"tool_call", `{"contract":"maw.pi.execution/v1","type":"tool_call","run_id":"run_a1"}`, "", 0},
		{"version_report", `{"contract":"maw.pi.execution/v1","type":"version_report"}`, "", 0},
	}
	for _, tc := range cases {
		res, err := ad.Decode(context.Background(), decodeReq(tc.payload))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(res.Events) != tc.wantEvent {
			t.Fatalf("%s: 事件数 = %d", tc.name, len(res.Events))
		}
		if tc.wantEvent == 1 && res.Events[0].Kind != tc.wantKind {
			t.Fatalf("%s: kind = %q", tc.name, res.Events[0].Kind)
		}
	}
}

func TestDecodeRejectsUnknownAndWrongContract(t *testing.T) {
	ad := newProbedAdapter(t, "{}", 0)
	if _, err := ad.Decode(context.Background(), decodeReq(`{"contract":"maw.pi.execution/v1","type":"mystery"}`)); err == nil {
		t.Fatal("未知类型必须报错，不猜语义")
	}
	if _, err := ad.Decode(context.Background(), decodeReq(`{"contract":"other/v1","type":"result"}`)); err == nil {
		t.Fatal("契约标识不符必须拒绝")
	}
	if _, err := ad.Decode(context.Background(), decodeReq("not json")); err == nil {
		t.Fatal("非法 JSON 必须拒绝")
	}
	if _, err := ad.Decode(context.Background(), ports.DecodeRequest{
		Protocol: model.Protocol{Format: "stream-json", Version: "1"}, Payload: []byte("{}"),
	}); err == nil {
		t.Fatal("非 Pi 协议必须拒绝")
	}
}

func TestDecodeClockSkewDowngradesQuality(t *testing.T) {
	ad, err := New(probedConfig(), WithRunner(fakeRunner{}), WithClock(func() time.Time {
		return time.Date(2026, 11, 18, 2, 0, 0, 0, time.UTC)
	}))
	if err != nil {
		t.Fatal(err)
	}
	future := time.Date(2026, 11, 18, 3, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	res, err := ad.Decode(context.Background(), decodeReq(
		`{"contract":"maw.pi.execution/v1","type":"result","occurred_at":"`+future+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	if res.Events[0].Quality != model.QualityUnknown {
		t.Fatalf("发生晚于接收应降级 unknown: %+v", res.Events[0])
	}
}

func TestInterpretNeverInfersSuccessFromExitCode(t *testing.T) {
	ad := newProbedAdapter(t, "{}", 0)
	zero := 0
	// 只有退出码 0：保持 unknown，不能判 completed。
	res, err := ad.Interpret(context.Background(), ports.InterpretRequest{ExitCode: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("退出码不得判成功: %+v", res)
	}

	// 带证据的 result：completed。
	payload := `{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":true,"outcome":"completed","reason":"result","evidence":["stop_reason:stop"]}}`
	res, err = ad.Interpret(context.Background(), ports.InterpretRequest{FinalResult: []byte(payload), ExitCode: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != model.OutcomeCompleted || res.Quality != model.QualityObserved {
		t.Fatalf("结果判定不对: %+v", res)
	}

	// 结果说成功但退出码非零：结论仍完成，但质量降级。
	one := 1
	res, _ = ad.Interpret(context.Background(), ports.InterpretRequest{FinalResult: []byte(payload), ExitCode: &one})
	if res.Outcome != model.OutcomeCompleted || res.Quality != model.QualityEstimated {
		t.Fatalf("矛盾证据应降级: %+v", res)
	}

	// 失败与取消照抄运行时结论。
	failed := `{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":false,"outcome":"failed","reason":"auth","evidence":["category:auth"]}}`
	res, _ = ad.Interpret(context.Background(), ports.InterpretRequest{FinalResult: []byte(failed)})
	if res.Outcome != model.OutcomeFailed {
		t.Fatalf("失败映射不对: %+v", res)
	}

	// 无结果 + 应用错误：failed；无结果 + 超时：timed_out。
	res, _ = ad.Interpret(context.Background(), ports.InterpretRequest{ApplicationError: "boom"})
	if res.Outcome != model.OutcomeFailed {
		t.Fatalf("应用错误应判失败: %+v", res)
	}
	res, _ = ad.Interpret(context.Background(), ports.InterpretRequest{TimedOut: true})
	if res.Outcome != model.OutcomeTimedOut {
		t.Fatalf("超时应判 timed_out: %+v", res)
	}

	// 认不出的 outcome：unknown，不猜。
	weird := `{"contract":"maw.pi.execution/v1","type":"result","payload":{"ok":true,"outcome":"maybe"}}`
	res, _ = ad.Interpret(context.Background(), ports.InterpretRequest{FinalResult: []byte(weird)})
	if res.Outcome != model.OutcomeUnknown {
		t.Fatalf("未知结论必须保持 unknown: %+v", res)
	}
}

func TestBuildLaunchSpecUsesStdinAndPiProtocol(t *testing.T) {
	ad := newProbedAdapter(t, "{}", 0)
	spec := model.RunSpec{
		Instance: "ti_abcdefgh", Version: 1, RunID: "run_a001", Generation: 2,
		Workspace:  model.WorkspaceSpec{Path: "/abs/worktree", BaselineCommit: "abc1234"},
		Input:      "Read TASK.md and finish it",
		InputMode:  model.InputStdin,
		Permission: model.PermissionProfile{Profile: "safe", AllowWrite: true},
		Timeout:    time.Minute,
		WriteScope: []string{"internal/goal/**"},
		ReadScope:  []string{"docs/**"},
	}
	launch, err := ad.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatalf("BuildLaunchSpec: %v", err)
	}
	if launch.Executable != "/abs/node" || len(launch.Argv) != 1 || launch.Argv[0] != "/abs/runtimes/pi/src/main.mjs" {
		t.Fatalf("启动参数不对: %+v", launch)
	}
	if launch.StdinMode != model.StdinPipe || launch.StdinData == "" {
		t.Fatalf("配置必须走 stdin: %+v", launch)
	}
	if launch.ExpectedProtocol.Format != ProtocolFormat || launch.ExpectedProtocol.Version != ProtocolVersion {
		t.Fatalf("协议不对: %+v", launch.ExpectedProtocol)
	}
	var cfg piRunConfig
	if err := json.Unmarshal([]byte(launch.StdinData), &cfg); err != nil {
		t.Fatalf("stdin 配置不是 JSON: %v", err)
	}
	if len(cfg.WriteScope) != 1 || cfg.WriteScope[0] != "internal/goal/**" || cfg.Cwd != "/abs/worktree" {
		t.Fatalf("执行边界没带进配置: %+v", cfg)
	}
	if cfg.Prompt == "" || cfg.Mode != "worker" {
		t.Fatalf("运行配置不完整: %+v", cfg)
	}
	joined := strings.Join(launch.EnvAllowlist, ",")
	if !strings.Contains(joined, "PI_MODEL") || !strings.Contains(joined, "PATH") {
		t.Fatalf("环境白名单缺项: %v", launch.EnvAllowlist)
	}
	if errs := launch.Validate(); len(errs) > 0 {
		t.Fatalf("启动规格应通过校验: %v", model.Err(errs))
	}

	// 不写盘的运行：write_scope 为空，运行时会把写入一律拒绝。
	spec.Permission.AllowWrite = false
	spec.WriteScope = nil
	launch, err = ad.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(launch.StdinData), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.WriteScope) != 0 {
		t.Fatalf("未声明范围时不得凭空给范围: %+v", cfg)
	}
}
