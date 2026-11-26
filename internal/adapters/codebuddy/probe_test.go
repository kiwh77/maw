package codebuddy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestProbeReportsObservedFacts 是主路径：版本、能力、四维就绪都按「测到什么说什么」返回。
func TestProbeReportsObservedFacts(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	a := newAdapter(t, cfg, fake)

	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if res.EntryState != model.EntryReady {
		t.Fatalf("入口应就绪，实际 %s", res.EntryState)
	}
	if res.Version != string(MeasuredCLIVersion) {
		t.Fatalf("版本应为 %s，实际 %s", MeasuredCLIVersion, res.Version)
	}
	if res.EvidenceRef != EvidenceRoot {
		t.Fatalf("必须挂证据引用，实际 %q", res.EvidenceRef)
	}
	if len(res.Capabilities) != len(capabilityMatrix) {
		t.Fatalf("能力条数应与证据矩阵一致（%d），实际 %d", len(capabilityMatrix), len(res.Capabilities))
	}
	if vs := res.Validate(); len(vs) != 0 {
		t.Fatalf("探测结果必须自洽：%v", model.Err(vs))
	}

	idx := capabilityIndex(res.Capabilities)
	for _, name := range []string{capVersionProbe, capHelpProbe, capHeadless, capStreamJSON, capExplicitPort} {
		if idx[name] != model.CapabilityObserved {
			t.Errorf("%s 实测版本上应为 observed，实际 %s", name, idx[name])
		}
	}
	// 未观测的能力不能因为「装好了」就翻成可用。
	if idx[capResume] != model.CapabilityUnknown {
		t.Errorf("session.resume 应保持 unknown，实际 %s", idx[capResume])
	}
	if idx["cancel.graceful_stop"] != model.CapabilityUnknown {
		t.Errorf("cancel.graceful_stop 应保持 unknown，实际 %s", idx["cancel.graceful_stop"])
	}

	// 只读探测判不出认证与连接：报 unknown，不报假的 ready。
	if res.Readiness.Auth != model.ReadyUnknown || res.Readiness.Connection != model.ReadyUnknown {
		t.Fatalf("认证／连接应为 unknown，实际 %s／%s", res.Readiness.Auth, res.Readiness.Connection)
	}
}

// TestProbeRunsOnlyReadOnlyCommands 钉住「不登录、不发模型任务」：只出现 --version 与 --help。
func TestProbeRunsOnlyReadOnlyCommands(t *testing.T) {
	cfg, script, nodeBin := testNode(t)
	fake := newFakeRunner()
	a := newAdapter(t, cfg, fake)

	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if len(fake.calls) != 2 {
		t.Fatalf("探测应只跑两次子进程，实际 %d 次", len(fake.calls))
	}

	tails := []string{"--version", "--help"}
	for i, call := range fake.calls {
		if tail := lastArg(call.Args); tail != tails[i] {
			t.Errorf("第 %d 次调用应为 %s，实际 %v", i+1, tails[i], call.Args)
		}
		if call.Name != mustReal(t, nodeBin) {
			t.Errorf("应以真实运行时为 executable，实际 %q", call.Name)
		}
		if len(call.Args) == 0 || call.Args[0] != mustReal(t, script) {
			t.Errorf("argv 第一个元素应是真实入口脚本，实际 %v", call.Args)
		}
		for _, arg := range call.Args {
			if arg == "-p" || arg == "--print" {
				t.Errorf("探测阶段出现了派发任务的参数：%v", call.Args)
			}
		}
	}
}

// TestProbeEnvIsAllowlistOnly 钉住环境白名单：宿主私有变量不得进入被测进程。
// 证据 §五：继承 CODEBUDDY_CONFIG_DIR 时同一条调用会返回认证缺失。
func TestProbeEnvIsAllowlistOnly(t *testing.T) {
	cfg, _, _ := testNode(t)
	t.Setenv("CODEBUDDY_CONFIG_DIR", "/tmp/another-config")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-leak")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid")

	fake := newFakeRunner()
	a := newAdapter(t, cfg, fake)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}

	allowed := make(map[string]bool, len(EnvAllowlist))
	for _, name := range EnvAllowlist {
		allowed[name] = true
	}
	env := fake.calls[0].Env
	if len(env) == 0 {
		t.Fatal("环境不能为空：HOME／PATH 缺失会让探测结论失真")
	}
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if !ok {
			t.Errorf("环境项 %q 不是 KEY=VALUE", kv)
			continue
		}
		if !allowed[name] {
			t.Errorf("白名单外的变量泄漏进探测进程：%s", name)
		}
		if name == "CODEBUDDY_CONFIG_DIR" || name == "AWS_SECRET_ACCESS_KEY" || name == "HTTPS_PROXY" {
			t.Errorf("宿主私有环境泄漏：%s", name)
		}
	}
	// PATH 必须传：解释器查找与 CLI 自身的子进程都依赖它。
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			found = true
		}
	}
	if !found {
		t.Error("PATH 应在白名单内并被传递")
	}
}

// TestProbeVersionUnparsed 覆盖版本输出不合形：不截断、不猜。
func TestProbeVersionUnparsed(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.versionOut = "codebuddy 2.173.0 (build 7)\n"
	a := newAdapter(t, cfg, fake)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrVersionUnparsed)
}

// TestProbeVersionNonZeroExit 覆盖 --version 自己退出非零。
func TestProbeVersionNonZeroExit(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.versionCode = 1
	fake.versionOut = ""
	a := newAdapter(t, cfg, fake)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrVersionUnparsed)
}

// TestProbeTimeout 覆盖探测挂死：按可重试的 probe_timeout 报，不无限等。
func TestProbeTimeout(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.block = true
	a := newAdapter(t, cfg, fake, WithProbeTimeout(20*time.Millisecond))

	start := time.Now()
	_, err := a.Probe(context.Background(), cfg)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("超时保护未生效：耗时 %s", elapsed)
	}
	e := requirePortErr(t, err, model.PortErrProbeTimeout)
	if e.Class != model.ClassRetryable {
		t.Fatalf("probe_timeout 应可重试，实际 %s", e.Class)
	}
}

// TestProbeExecutionFailure 覆盖入口解析过了但根本起不来（例如权限被系统拒绝）。
func TestProbeExecutionFailure(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.failure = errors.New("operation not permitted")
	a := newAdapter(t, cfg, fake)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrEntryMissing)
}

// TestProbeEntryMissing 覆盖入口不存在：不能「等下次再说」。
func TestProbeEntryMissing(t *testing.T) {
	cfg, _, _ := testNode(t)
	cfg.EntryPath = cfg.EntryPath + "-gone"
	a := newAdapter(t, cfg, newFakeRunner())

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrEntryMissing)
}

// TestProbePlatformUnsupported 覆盖平台不符。
func TestProbePlatformUnsupported(t *testing.T) {
	cfg, _, _ := testNode(t)
	a := newAdapter(t, cfg, newFakeRunner())

	foreign := cfg
	foreign.Platform = "windows"
	_, err := a.Probe(context.Background(), foreign)
	requirePortErr(t, err, model.PortErrPlatformUnsupported)
}

// TestProbeVersionDriftDegradesObserved 是 G07 的核心：版本一变，observed 整体降级。
func TestProbeVersionDriftDegradesObserved(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.versionOut = "2.174.0\n"
	a := newAdapter(t, cfg, fake)

	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if res.Version != "2.174.0" {
		t.Fatalf("版本应为 2.174.0，实际 %s", res.Version)
	}
	idx := capabilityIndex(res.Capabilities)
	if idx[capHeadless] != model.CapabilityUnobserved {
		t.Errorf("换版本后 print.headless_mode 应降级为 unobserved，实际 %s", idx[capHeadless])
	}
	if idx["cancel.graceful_stop"] != model.CapabilityUnknown {
		t.Errorf("unknown 行不随版本变化被改写，实际 %s", idx["cancel.graceful_stop"])
	}
	// 本次确实跑通了 --version，这条是「刚刚测到的」，不受上一版本证据影响。
	if idx[capVersionProbe] != model.CapabilityObserved {
		t.Errorf("entry.version_probe 是本次实测到的，应为 observed，实际 %s", idx[capVersionProbe])
	}
}

// TestProbeHelpFailureDegradesHelpCapability 覆盖帮助探测失败：不当致命，但能力必须降级。
func TestProbeHelpFailureDegradesHelpCapability(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	fake.helpCode = 1
	a := newAdapter(t, cfg, fake)

	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("帮助探测失败不应让整个探测失败：%v", err)
	}
	if idx := capabilityIndex(res.Capabilities); idx[capHelpProbe] != model.CapabilityUnobserved {
		t.Fatalf("帮助探测失败后 entry.help_probe 应为 unobserved，实际 %s", idx[capHelpProbe])
	}
}

// TestProbeHelpDisabled 覆盖显式关闭帮助探测。
func TestProbeHelpDisabled(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	a := newAdapter(t, cfg, fake, WithHelpProbe(false))

	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("关闭帮助探测时只应跑 --version，实际 %d 次", len(fake.calls))
	}
	if idx := capabilityIndex(res.Capabilities); idx[capHelpProbe] != model.CapabilityUnobserved {
		t.Fatalf("未测量到帮助清单时 entry.help_probe 应为 unobserved，实际 %s", idx[capHelpProbe])
	}
}

// TestProbeRejectsForeignConfig 覆盖「用 A 节点的适配器探测 B 节点的配置」。
// Launch 端口拿不到节点配置，混用会让启动规格指向另一台机器的入口。
func TestProbeRejectsForeignConfig(t *testing.T) {
	cfg, _, _ := testNode(t)
	a := newAdapter(t, cfg, newFakeRunner())

	other := cfg
	other.NodeID = "other-node"
	_, err := a.Probe(context.Background(), other)
	requireCode(t, err, model.CodePortFieldInvalid)
}

// TestProbeRejectsWrongAdapterKind 覆盖适配器种类错配。
func TestProbeRejectsWrongAdapterKind(t *testing.T) {
	cfg, _, _ := testNode(t)
	cfg.Adapter = model.AdapterTrae
	if _, err := New(cfg); err == nil {
		t.Fatal("非 codebuddy 节点必须在构造时被拒绝")
	}
}

// TestNewRejectsRelativeEntry 覆盖构造期配置校验。
func TestNewRejectsRelativeEntry(t *testing.T) {
	cfg, _, _ := testNode(t)
	cfg.EntryPath = "bin/codebuddy"
	if _, err := New(cfg); err == nil {
		t.Fatal("相对入口必须在构造时被拒绝")
	}
}
