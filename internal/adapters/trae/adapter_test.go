package trae_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/trae"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

type fakeRunner struct {
	version string
	help    string
	vCode   int
	hCode   int
}

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) (string, string, int, error) {
	if len(args) > 0 && args[0] == "--version" {
		return f.version + "\n", "", f.vCode, nil
	}
	if len(args) > 0 && args[0] == "--help" {
		return f.help, "", f.hCode, nil
	}
	return "", "unknown", 1, nil
}

func ideCfg(t *testing.T) model.NodeConfig {
	t.Helper()
	return model.NodeConfig{
		NodeID: "trae-ide-1", Name: "Trae IDE", Adapter: model.AdapterTrae,
		EntryPath: "/Applications/Trae.app/Contents/MacOS/Trae",
		ProfileRef: "trae/ide-chat", Mode: model.NodeManualClient,
		Platform: "darwin", SlotLimit: 0, AcceptNew: true,
	}
}

func cliCfg(t *testing.T, entry, profile string) model.NodeConfig {
	t.Helper()
	return model.NodeConfig{
		NodeID: "trae-cli-1", Name: "Trae CLI", Adapter: model.AdapterTrae,
		EntryPath: entry, ProfileRef: profile, Mode: model.NodeManagedCLI,
		Platform: "darwin", SlotLimit: 1, AcceptNew: true, Timeout: time.Minute,
	}
}

func writeFakeCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "trae-cli")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho stub\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestIDEChatStaysManualNoBackgroundClaim(t *testing.T) {
	a, err := trae.New(ideCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Probe(context.Background(), ideCfg(t))
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != "ide-chat" {
		t.Fatalf("version=%s", res.Version)
	}
	byName := map[string]model.CapabilityState{}
	for _, c := range res.Capabilities {
		byName[c.Name] = c.State
	}
	if byName["managed.launch"] != model.CapabilityUnsupported {
		t.Fatalf("managed.launch=%s", byName["managed.launch"])
	}
	if byName["managed.background_result_recovery"] != model.CapabilityUnsupported {
		t.Fatalf("background=%s", byName["managed.background_result_recovery"])
	}
	if !a.ManualOnly() {
		t.Fatal("IDE chat must be manual-only")
	}
	spec := model.RunSpec{
		Instance: "ti_trae01", Version: 1, RunID: "run_trae01", Generation: 1,
		Workspace: model.WorkspaceSpec{
			Path: t.TempDir(), BaselineCommit: model.Commit("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		},
		Input: "hi", InputMode: model.InputArg,
		Permission: model.PermissionProfile{Profile: "default"},
		Timeout:    time.Minute,
	}
	_, err = a.BuildLaunchSpec(context.Background(), spec)
	if err == nil {
		t.Fatal("IDE must not launch")
	}
	if !strings.Contains(err.Error(), "manual") && !strings.Contains(err.Error(), "IDE") &&
		!strings.Contains(err.Error(), "capability") {
		t.Fatalf("err=%v", err)
	}
	_, err = a.Decode(context.Background(), ports.DecodeRequest{
		RecordRef: "r1", Cursor: 1, Protocol: model.Protocol{Format: "json", Version: "1"}, Payload: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("IDE must not decode background results")
	}
}

func TestCLIOpensManagedOnlyWhenProbedAuthedAndVersionMatches(t *testing.T) {
	entry := writeFakeCLI(t)
	cfg := cliCfg(t, entry, "trae/cli-authed")
	a, err := trae.New(cfg, trae.WithRunner(fakeRunner{
		version: "1.0.0", help: "Usage:\n  --print\n  --output-format json\n", vCode: 0, hCode: 0,
	}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]model.CapabilityState{}
	for _, c := range res.Capabilities {
		byName[c.Name] = c.State
	}
	if byName["managed.launch"] != model.CapabilityObserved {
		t.Fatalf("launch=%s caps=%v", byName["managed.launch"], byName)
	}
	if a.ManualOnly() {
		t.Fatal("authed measured CLI should allow managed")
	}

	// 未登录 profile：不开放受管
	cfg2 := cliCfg(t, entry, "trae/cli-needs-login")
	a2, _ := trae.New(cfg2, trae.WithRunner(fakeRunner{
		version: "1.0.0", help: "Usage:\n  --print\n", vCode: 0, hCode: 0,
	}))
	res2, err := a2.Probe(context.Background(), cfg2)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res2.Capabilities {
		if c.Name == "managed.launch" && c.State == model.CapabilityObserved {
			t.Fatal("needs-login must not open managed.launch")
		}
	}
	if res2.Readiness.Auth != model.ReadyNeedsLogin {
		t.Fatalf("auth=%s", res2.Readiness.Auth)
	}

	// 版本不匹配：降级
	cfg3 := cliCfg(t, entry, "trae/cli-authed")
	a3, _ := trae.New(cfg3, trae.WithRunner(fakeRunner{
		version: "9.9.9", help: "Usage:\n  --print\n", vCode: 0, hCode: 0,
	}))
	res3, err := a3.Probe(context.Background(), cfg3)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range res3.Capabilities {
		if c.Name == "managed.launch" && c.State == model.CapabilityObserved {
			t.Fatal("wrong version must not keep managed observed")
		}
	}
}

func TestClassifyDoesNotShareIDEAndCLI(t *testing.T) {
	if trae.Classify(ideCfg(t)) != trae.ClassIDEChat {
		t.Fatal("ide")
	}
	if trae.Classify(cliCfg(t, writeFakeCLI(t), "trae/cli")) != trae.ClassCLI {
		t.Fatal("cli")
	}
}

func TestInterpretExitZeroNotSuccess(t *testing.T) {
	entry := writeFakeCLI(t)
	cfg := cliCfg(t, entry, "trae/cli-authed")
	a, _ := trae.New(cfg, trae.WithRunner(fakeRunner{
		version: "1.0.0", help: "--print\n", vCode: 0, hCode: 0,
	}))
	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	zero := 0
	res, err := a.Interpret(context.Background(), ports.InterpretRequest{ExitCode: &zero})
	if err == nil {
		t.Fatal("missing result must error")
	}
	if res.Outcome == model.OutcomeCompleted {
		t.Fatal("exit 0 must not complete")
	}
}
