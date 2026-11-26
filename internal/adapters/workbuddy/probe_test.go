package workbuddy

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// TestProbeReportsObservedFacts 是主路径：两包各自探测，版本、能力、四维就绪都按「测到什么说什么」返回。
func TestProbeReportsObservedFacts(t *testing.T) {
	for _, kind := range []DistributionKind{DistributionCN, DistributionIntl} {
		cfg, _, _ := testNode(t, kind)
		dist, _ := Lookup(kind)
		fake := newFakeRunner(dist)
		a := newAdapter(t, cfg, fake)

		res, err := a.Probe(context.Background(), cfg)
		if err != nil {
			t.Fatalf("%s 探测失败：%v", kind, err)
		}
		if res.EntryState != model.EntryReady {
			t.Fatalf("%s 入口应就绪，实际 %s", kind, res.EntryState)
		}
		if res.Version != string(dist.BundledCLI) {
			t.Fatalf("%s 版本应为 %s，实际 %s", kind, dist.BundledCLI, res.Version)
		}
		if res.EvidenceRef != dist.ManifestRef() {
			t.Fatalf("%s 必须挂该包证据清单，实际 %q", kind, res.EvidenceRef)
		}
		if len(res.Capabilities) != len(capabilityMatrix) {
			t.Fatalf("%s 能力条数应与证据矩阵一致（%d），实际 %d", kind, len(capabilityMatrix), len(res.Capabilities))
		}
		if vs := res.Validate(); len(vs) != 0 {
			t.Fatalf("%s 探测结果必须自洽：%v", kind, model.Err(vs))
		}

		idx := capabilityIndex(res.Capabilities)
		for _, name := range []string{
			capVersionProbe, capHelpProbe, capRuntimeNode, capHeadless, capOutputJSON,
			capPermission, capMCPStrict, capPortAuto, capModelEnum, capNoPersist,
		} {
			if idx[name] != model.CapabilityObserved {
				t.Errorf("%s：%s 实测版本上应为 observed，实际 %s", kind, name, idx[name])
			}
		}
		// 未观测的能力不能因为「装好了」就翻成可用。
		for _, name := range []string{capStreamJSON, capPartialMsgs, capRecordSeq, capResultRecord, capUsage, capResume} {
			if idx[name] != model.CapabilityUnobserved {
				t.Errorf("%s：%s 应保持 unobserved，实际 %s", kind, name, idx[name])
			}
		}
		if idx[capModelShared] != model.CapabilityUnsupported {
			t.Errorf("%s：两包模型枚举不可互认，应保持 unsupported，实际 %s", kind, idx[capModelShared])
		}

		// 只读探测判不出认证与连接：报 unknown，不报假的 ready，也不写死 needs-login。
		if res.Readiness.Auth != model.ReadyUnknown || res.Readiness.Connection != model.ReadyUnknown {
			t.Fatalf("%s 认证／连接应为 unknown，实际 %s／%s", kind, res.Readiness.Auth, res.Readiness.Connection)
		}
		if res.Readiness.Ready() {
			t.Errorf("%s 认证未知时 Readiness.Ready() 必须为假", kind)
		}
	}
}

// TestProbeEvidenceIsPerDistribution 是本卡验收第一条的机检落点：
// **按包实测出来的能力**必须各挂各的样本目录，不能共用一份；
// 只有「矩阵本身」与「两包共用的记录型反例」才允许同一条引用。
func TestProbeEvidenceIsPerDistribution(t *testing.T) {
	refs := map[DistributionKind]map[string]string{}

	for _, kind := range []DistributionKind{DistributionCN, DistributionIntl} {
		cfg, _, _ := testNode(t, kind)
		dist, _ := Lookup(kind)
		a := newAdapter(t, cfg, newFakeRunner(dist))
		res, err := a.Probe(context.Background(), cfg)
		if err != nil {
			t.Fatalf("%s 探测失败：%v", kind, err)
		}
		m := make(map[string]string, len(res.Capabilities))
		for _, c := range res.Capabilities {
			m[c.Name] = c.EvidenceRef
		}
		refs[kind] = m
	}

	cnRefs := refs[DistributionCN]
	intlRefs := refs[DistributionIntl]
	if len(cnRefs) != len(capabilityMatrix) || len(intlRefs) != len(capabilityMatrix) {
		t.Fatalf("能力条数不符：cn %d／intl %d／矩阵 %d", len(cnRefs), len(intlRefs), len(capabilityMatrix))
	}

	for _, row := range capabilityMatrix {
		cnRef, intlRef := cnRefs[row.Name], intlRefs[row.Name]
		switch {
		case row.Sample == "":
			// 没有单条样本：证据就是矩阵本身，两包共用同一份矩阵是设计如此。
			want := EvidenceRoot + "/capability-matrix.json"
			if cnRef != want || intlRef != want {
				t.Errorf("%s 无样本，证据应指向矩阵本身：cn %q／intl %q", row.Name, cnRef, intlRef)
			}
		case strings.HasPrefix(row.Sample, "X"):
			// 两包共用的记录型反例（认证缺失形态），不加包前缀。
			want := EvidenceRoot + "/samples/VN305-" + row.Sample
			if cnRef != want || intlRef != want {
				t.Errorf("%s 共用反例路径不符：cn %q／intl %q", row.Name, cnRef, intlRef)
			}
		default:
			// 按包实测的样本：两包必须指向各自的目录，且不能引用对方。
			if cnRef != EvidenceRoot+"/samples/VN305-CN-"+row.Sample {
				t.Errorf("%s 的 cn 证据不符：%q", row.Name, cnRef)
			}
			if intlRef != EvidenceRoot+"/samples/VN305-INTL-"+row.Sample {
				t.Errorf("%s 的 intl 证据不符：%q", row.Name, intlRef)
			}
			if cnRef == intlRef {
				t.Errorf("%s 在两包上指向了同一份证据 %q：按包实测的能力必须各挂各的", row.Name, cnRef)
			}
		}
		if strings.Contains(cnRef, "VN305-INTL-") {
			t.Errorf("cn 的能力引用了 intl 的证据：%s → %q", row.Name, cnRef)
		}
		if strings.Contains(intlRef, "VN305-CN-") {
			t.Errorf("intl 的能力引用了 cn 的证据：%s → %q", row.Name, intlRef)
		}
	}
}

// TestProbeRunsOnlyReadOnlyCommands 钉住「不登录、不发模型任务」：只出现 --version 与 --help。
func TestProbeRunsOnlyReadOnlyCommands(t *testing.T) {
	cfg, script, nodeBin := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
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

// TestProbeEnvCarriesLaunchConditions 钉住环境白名单 + 启动条件：
// 宿主私有变量不得进入被测进程，随包实现需要的两个变量必须传进去。
func TestProbeEnvCarriesLaunchConditions(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	t.Setenv("CODEBUDDY_CONFIG_DIR", "/tmp/another-config")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "should-not-leak")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid")

	fake := newFakeRunner(dist)
	a := newAdapter(t, cfg, fake)
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}

	allowed := map[string]bool{}
	for _, name := range EnvAllowlist {
		allowed[name] = true
	}
	for _, name := range dist.EnvNames() {
		allowed[name] = true
	}

	env := fake.calls[0].Env
	if len(env) == 0 {
		t.Fatal("环境不能为空：HOME／PATH 缺失会让探测结论失真")
	}
	seen := map[string]string{}
	for _, kv := range env {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			t.Errorf("环境项 %q 不是 KEY=VALUE", kv)
			continue
		}
		seen[name] = value
		if !allowed[name] {
			t.Errorf("白名单外的变量泄漏进探测进程：%s", name)
		}
	}
	if _, ok := seen["PATH"]; !ok {
		t.Error("PATH 应在白名单内并被传递：解释器查找依赖它")
	}
	for name, want := range dist.RequiredEnv {
		if seen[name] != want {
			t.Errorf("启动条件 %s=%s 必须传给探测进程，实际 %q", name, want, seen[name])
		}
	}
}

// TestProbeVersionUnparsed 覆盖版本输出不合形：不截断、不猜。
func TestProbeVersionUnparsed(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
	fake.versionOut = "codebuddy 2.161.0 (build 7)\n"
	a := newAdapter(t, cfg, fake)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrVersionUnparsed)
}

// TestProbeVersionNonZeroExit 覆盖 --version 自己退出非零。
func TestProbeVersionNonZeroExit(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
	fake.versionCode = 1
	fake.versionOut = ""
	a := newAdapter(t, cfg, fake)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrVersionUnparsed)
}

// TestProbeTimeout 覆盖探测挂死：按可重试的 probe_timeout 报，不无限等。
func TestProbeTimeout(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
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

// TestProbeExecutionFailure 覆盖入口解析过了但根本起不来。
func TestProbeExecutionFailure(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
	fake.failure = errors.New("operation not permitted")
	a := newAdapter(t, cfg, fake)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrEntryMissing)
}

// TestProbeEntryMissing 覆盖入口不存在。
func TestProbeEntryMissing(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	cfg.EntryPath = cfg.EntryPath + "-gone"
	a := newAdapter(t, cfg, nil)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrEntryMissing)
}

// TestProbePlatformUnsupported 覆盖平台不符。
func TestProbePlatformUnsupported(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cfg, nil)

	foreign := cfg
	foreign.Platform = "windows"
	_, err := a.Probe(context.Background(), foreign)
	requirePortErr(t, err, model.PortErrPlatformUnsupported)
}

// TestProbeRejectsUnregisteredEntry 覆盖「路径上有 WorkBuddy 字样」但不在登记的随包位置。
func TestProbeRejectsUnregisteredEntry(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	sibling := cfg.EntryPath
	sibling = strings.Replace(sibling, "bin/codebuddy", "bin/cbc-prewarm", 1)
	mustWrite(t, sibling, "#!/usr/bin/env node\n", 0o755)

	cfg.EntryPath = sibling
	a := newAdapter(t, cfg, nil)

	_, err := a.Probe(context.Background(), cfg)
	requirePortErr(t, err, model.PortErrEntryUnavailable)
}

// TestProbeRejectsNonNodeRuntime 覆盖入口形态不符：别的解释器不在证据覆盖范围内。
func TestProbeRejectsNonNodeRuntime(t *testing.T) {
	cfg, script, _ := testNode(t, DistributionCN)
	mustWrite(t, script, "#!/bin/sh\nprintf '2.161.0\\n'\n", 0o755)
	a := newAdapter(t, cfg, nil)

	_, err := a.Probe(context.Background(), cfg)
	e := requirePortErr(t, err, model.PortErrEntryUnavailable)
	if !strings.Contains(e.Msg, "运行时") {
		t.Fatalf("错误文案应说明运行时形态，实际 %q", e.Msg)
	}
}

// TestProbeRejectsMissingLaunchConditions 覆盖缺启动条件：
// 不是「降级跑一遍」，而是直接说明要导出什么（人工接入信息）。
func TestProbeRejectsMissingLaunchConditions(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	for name := range cfgRequiredEnv(t) {
		t.Setenv(name, "")
	}
	a := newAdapter(t, cfg, nil)

	_, err := a.Probe(context.Background(), cfg)
	e := requirePortErr(t, err, model.PortErrCapabilityMissing)
	for _, name := range []string{"CODEBUDDY_FORCE_LITE_WB_BUNDLE", "CODEBUDDY_DISABLE_COMPILE_CACHE"} {
		if !strings.Contains(e.Msg, name) {
			t.Errorf("错误文案应点名缺失的变量 %s，实际 %q", name, e.Msg)
		}
	}
	if !strings.Contains(e.Msg, "重新探测") {
		t.Errorf("错误文案应给出下一步，实际 %q", e.Msg)
	}
}

func cfgRequiredEnv(t *testing.T) map[string]string {
	t.Helper()
	dist, _ := Lookup(DistributionCN)
	return dist.RequiredEnv
}

// TestProbeVersionDriftDegradesObserved 是 G07 的核心：版本一变，observed 整体降级。
func TestProbeVersionDriftDegradesObserved(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
	fake.versionOut = "2.162.0\n"
	a := newAdapter(t, cfg, fake)

	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("探测失败：%v", err)
	}
	if res.Version != "2.162.0" {
		t.Fatalf("版本应为 2.162.0，实际 %s", res.Version)
	}
	idx := capabilityIndex(res.Capabilities)
	if idx[capHeadless] != model.CapabilityUnobserved {
		t.Errorf("换版本后 print.headless_mode 应降级为 unobserved，实际 %s", idx[capHeadless])
	}
	if idx[capPortAuto] != model.CapabilityUnobserved {
		t.Errorf("换版本后 launch.port_auto_assign 应降级，实际 %s", idx[capPortAuto])
	}
	// unknown／unsupported 行不随版本变化被改写。
	if idx[capCancel] != model.CapabilityUnknown {
		t.Errorf("cancel.graceful_stop 应保持 unknown，实际 %s", idx[capCancel])
	}
	if idx[capModelShared] != model.CapabilityUnsupported {
		t.Errorf("model.list_interchangeable 应保持 unsupported，实际 %s", idx[capModelShared])
	}
	// 本次确实跑通了 --version，这条是「刚刚测到的」。
	if idx[capVersionProbe] != model.CapabilityObserved {
		t.Errorf("entry.version_probe 是本次实测到的，应为 observed，实际 %s", idx[capVersionProbe])
	}
}

// TestProbeHelpFailureDegradesHelpCapability 覆盖帮助探测失败：不当致命，但依赖帮助的能力必须全部降级。
func TestProbeHelpFailureDegradesHelpCapability(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
	fake.helpCode = 1
	a := newAdapter(t, cfg, fake)

	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("帮助探测失败不应让整个探测失败：%v", err)
	}
	idx := capabilityIndex(res.Capabilities)
	for _, name := range []string{capHelpProbe, capHeadless, capOutputJSON, capPermission, capMCPStrict, capPortAuto, capModelEnum, capNoPersist} {
		if idx[name] != model.CapabilityUnobserved {
			t.Errorf("帮助探测失败后 %s 应为 unobserved，实际 %s", name, idx[name])
		}
	}
	// 版本探测本身仍然成立。
	if idx[capVersionProbe] != model.CapabilityObserved {
		t.Errorf("entry.version_probe 不受帮助探测影响，应为 observed，实际 %s", idx[capVersionProbe])
	}
}

// TestProbeHelpDisabled 覆盖显式关闭帮助探测。
func TestProbeHelpDisabled(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	dist, _ := Lookup(DistributionCN)
	fake := newFakeRunner(dist)
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

// TestProbeRejectsForeignConfig 覆盖「用 A 节点的适配器探测 B 节点的配置」：
// 这正是「两包共用一份探测结果」的技术路径，必须被挡死。
func TestProbeRejectsForeignConfig(t *testing.T) {
	cnCfg, _, _ := testNode(t, DistributionCN)
	a := newAdapter(t, cnCfg, nil)

	intlCfg, _, _ := testNode(t, DistributionIntl)
	_, err := a.Probe(context.Background(), intlCfg)
	requireCode(t, err, model.CodePortFieldInvalid)
}

// TestProbeRejectsWrongAdapterKind 覆盖适配器种类错配。
func TestProbeRejectsWrongAdapterKind(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	cfg.Adapter = model.AdapterCodeBuddy
	if _, err := New(cfg); err == nil {
		t.Fatal("非 workbuddy 节点必须在构造时被拒绝")
	}
}

// TestNewRejectsRelativeEntry 覆盖构造期配置校验。
func TestNewRejectsRelativeEntry(t *testing.T) {
	cfg, _, _ := testNode(t, DistributionCN)
	cfg.EntryPath = "bin/codebuddy"
	if _, err := New(cfg); err == nil {
		t.Fatal("相对入口必须在构造时被拒绝")
	}
}
