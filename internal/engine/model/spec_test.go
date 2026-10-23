package model_test

import (
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

func validRunSpec() model.RunSpec {
	return model.RunSpec{
		Instance:   "ti_vn001a",
		Version:    1,
		RunID:      "run_a001",
		Generation: 1,
		Workspace: model.WorkspaceSpec{
			Path:           "/Users/dev/worktrees/agent-1",
			BaselineCommit: "abc1234",
		},
		Input:      "按任务卡实现端口层",
		InputMode:  model.InputStdin,
		Permission: model.PermissionProfile{Profile: "default"},
		Session:    model.SessionSpec{Isolation: "per-run"},
		Timeout:    30 * time.Minute,
	}
}

func TestRunSpecRejectsIncompleteSpec(t *testing.T) {
	assertClean(t, "完整运行规格", validRunSpec().Validate())

	cases := map[string]func(*model.RunSpec){
		"缺任务实例":   func(s *model.RunSpec) { s.Instance = "" },
		"缺版本":     func(s *model.RunSpec) { s.Version = 0 },
		"缺运行身份":   func(s *model.RunSpec) { s.RunID = "" },
		"缺代际":     func(s *model.RunSpec) { s.Generation = 0 },
		"相对工作目录":  func(s *model.RunSpec) { s.Workspace.Path = "worktrees/agent-1" },
		"基线写成别名":  func(s *model.RunSpec) { s.Workspace.BaselineCommit = "main" },
		"空输入":     func(s *model.RunSpec) { s.Input = "  " },
		"未知输入方式":  func(s *model.RunSpec) { s.InputMode = "socket" },
		"缺权限档":    func(s *model.RunSpec) { s.Permission.Profile = "" },
		"超时不设":    func(s *model.RunSpec) { s.Timeout = 0 },
		"恢复会话无隔离": func(s *model.RunSpec) { s.Session.ResumeSessionID = "sess_1"; s.Session.Isolation = "" },
	}
	for name, mut := range cases {
		s := validRunSpec()
		mut(&s)
		if len(s.Validate()) == 0 {
			t.Errorf("%s：应被拒绝", name)
		}
	}
}

func TestLaunchSpecForbidsShellAndBlacklists(t *testing.T) {
	valid := model.LaunchSpec{
		Executable:       "/opt/codebuddy/bin/codebuddy",
		Argv:             []string{"--print", "--output-format", "stream-json"},
		Cwd:              "/Users/dev/worktrees/agent-1",
		StdinMode:        model.StdinPipe,
		EnvAllowlist:     []string{"HOME", "PATH"},
		ExpectedProtocol: model.Protocol{Format: "stream-json", Version: "1"},
	}
	assertClean(t, "合法启动规格", valid.Validate())

	viaShell := valid
	viaShell.Executable = "/bin/sh"
	viaShell.Argv = []string{"-c", "codebuddy --print 'do it'"}
	assertHas(t, "借 shell 拼命令", viaShell.Validate(), model.CodePortFieldInvalid)

	noArgv := valid
	noArgv.Argv = nil
	assertHas(t, "argv 为空", noArgv.Validate(), model.CodePortFieldInvalid)

	kvEnv := valid
	kvEnv.EnvAllowlist = []string{"PATH=/usr/bin"}
	assertHas(t, "环境变量写成 KEY=VALUE", kvEnv.Validate(), model.CodePortFieldInvalid)

	noProtocol := valid
	noProtocol.ExpectedProtocol.Version = ""
	assertHas(t, "协议版本含糊", noProtocol.Validate(), model.CodePortFieldInvalid)

	relativeExe := valid
	relativeExe.Executable = "codebuddy"
	assertHas(t, "相对入口", relativeExe.Validate(), model.CodePortFieldInvalid)
}

func TestNodeConfigAndCapability(t *testing.T) {
	cfg := model.NodeConfig{
		NodeID:      "mbp-codebuddy",
		Name:        "MacBook CodeBuddy",
		Adapter:     model.AdapterCodeBuddy,
		EntryPath:   "/opt/codebuddy/bin/codebuddy",
		RuntimePath: "/opt/node/bin/node",
		ProfileRef:  "domestic",
		Mode:        model.NodeManagedCLI,
		Platform:    "darwin-arm64",
		SlotLimit:   2,
		AcceptNew:   true,
		Timeout:     30 * time.Minute,
	}
	assertClean(t, "完整节点配置", cfg.Validate())

	noSlot := cfg
	noSlot.SlotLimit = 0
	assertHas(t, "受管节点无槽", noSlot.Validate(), model.CodePortFieldInvalid)

	badAdapter := cfg
	badAdapter.Adapter = "unknown-tool"
	assertHas(t, "未知接入器", badAdapter.Validate(), model.CodePortFieldInvalid)

	// P140：能力必须绑定版本与证据。
	capability := model.Capability{Name: "cancel", State: model.CapabilityObserved}
	vs := capability.Validate("node.capabilities[0]")
	assertHas(t, "能力无版本", vs, model.CodeCapabilityUnversioned)

	ok := model.Capability{
		Name:        "cancel",
		State:       model.CapabilityObserved,
		Version:     "2.173.0",
		EvidenceRef: "docs/evidence/codebuddy/capability-matrix.json",
	}
	assertClean(t, "能力绑定版本与证据", ok.Validate("node.capabilities[0]"))
	if !ok.State.AutoMatchable() {
		t.Fatal("observed 才可参与自动匹配")
	}
	if model.CapabilityUnobserved.AutoMatchable() || model.CapabilityUnknown.AutoMatchable() {
		t.Fatal("unobserved/unknown 不得参与自动匹配")
	}
}

func TestProbeResultNeedsVersionAndEvidence(t *testing.T) {
	probe := model.ProbeResult{
		EntryState: model.EntryReady,
		Version:    "2.173.0",
		Readiness: model.Readiness{
			Entry:      model.EntryReady,
			Auth:       model.ReadyOK,
			Connection: model.ReadyFresh,
			Capacity:   model.ReadyAvailable,
		},
		Capabilities: []model.Capability{{
			Name:        "structured-events",
			State:       model.CapabilityObserved,
			Version:     "2.173.0",
			EvidenceRef: "docs/evidence/codebuddy/manifest.json",
		}},
		EvidenceRef: "docs/evidence/codebuddy/manifest.json",
	}
	assertClean(t, "完整探测结果", probe.Validate())
	if !probe.Readiness.Ready() {
		t.Fatal("入口、认证与容量都就绪时应 Ready")
	}

	noVersion := probe
	noVersion.Version = "  "
	assertHas(t, "探测无版本", noVersion.Validate(), model.CodeCapabilityUnversioned)

	unverified := probe
	unverified.Capabilities = []model.Capability{{Name: "cancel", State: model.CapabilityUnobserved, Version: "2.173.0", EvidenceRef: "x"}}
	assertClean(t, "未观测能力可上报但不可自动匹配", unverified.Validate())

	draining := probe
	draining.Readiness.Capacity = model.ReadyDraining
	if draining.Readiness.Ready() {
		t.Fatal("draining 不应算可接新任务")
	}
}

func TestInterpretResultUnknownIsNotSuccess(t *testing.T) {
	if model.OutcomeUnknown.IsSuccess() {
		t.Fatal("未知协议结果不得判成功（P142）")
	}
	if !model.OutcomeCompleted.IsSuccess() {
		t.Fatal("completed 才是成功")
	}

	bad := model.InterpretResult{Outcome: "success", Quality: model.QualityObserved}
	assertHas(t, "未知结果字面量", bad.Validate(), model.CodePortFieldInvalid)

	cand := model.InterpretResult{
		Outcome:   model.OutcomeUnknown,
		Quality:   model.QualityUnknown,
		Artifacts: []model.ArtifactCandidate{{Kind: model.ArtifactFile, DeclaredBy: model.DeclaredByCLI}},
	}
	assertHas(t, "候选产物缺引用", cand.Validate(), model.CodePortFieldInvalid)
}

func TestProcessSpecs(t *testing.T) {
	stop := model.StopRequest{RunID: "run_a001", Generation: 1, Mode: model.StopForce}
	assertClean(t, "合法停止请求", stop.Validate())

	badStop := stop
	badStop.Generation = 0
	assertHas(t, "停止缺代际", badStop.Validate(), model.CodePortFieldInvalid)

	handle := model.ProcessHandle{PID: 4321, PGID: 4321, StartToken: "nonce-1"}
	assertClean(t, "带启动令牌的进程身份", handle.Validate())

	noToken := model.ProcessHandle{PID: 4321}
	assertHas(t, "只凭 PID 判归属", noToken.Validate(), model.CodePortFieldInvalid)

	unconfirmed := model.StopEvidence{StopConfirmed: false, Method: "SIGTERM"}
	assertClean(t, "停止未确认是合法结果", unconfirmed.Validate())

	noMethod := model.StopEvidence{StopConfirmed: true}
	assertHas(t, "停止无手段", noMethod.Validate(), model.CodePortFieldInvalid)
}
