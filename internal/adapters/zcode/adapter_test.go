package zcode_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/zcode"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

type fakeRunner struct{ version string }

func (f fakeRunner) Run(_ context.Context, _ string, args ...string) (string, string, int, error) {
	if len(args) > 0 && args[0] == "--version" {
		return f.version + "\n", "", 0, nil
	}
	return "", "", 1, nil
}

func writeCLI(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "zcode")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func baseCfg(t *testing.T, entry, profile string) model.NodeConfig {
	t.Helper()
	return model.NodeConfig{
		NodeID: "zcode-1", Name: "ZCode", Adapter: model.AdapterZCode,
		EntryPath: entry, ProfileRef: profile, Mode: model.NodeManagedCLI,
		Platform: "darwin", SlotLimit: 1, AcceptNew: true, Timeout: time.Minute,
	}
}

func TestRuntimeProviderModelReportedSeparately(t *testing.T) {
	entry := writeCLI(t)
	cfg := baseCfg(t, entry, "zcode/provider=openai")
	a, err := zcode.New(cfg, zcode.WithRunner(fakeRunner{version: "1.0.0"}))
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	view, ok := a.LastConfig()
	if !ok {
		t.Fatal("missing config view")
	}
	if !view.RuntimeReady || !view.ProviderReady || view.ModelReady {
		t.Fatalf("view=%+v", view)
	}
	if view.ConfigMutated {
		t.Fatal("must not mutate config")
	}
	if res.Readiness.Auth != model.ReadyNeedsModel {
		t.Fatalf("auth=%s", res.Readiness.Auth)
	}
	by := map[string]model.CapabilityState{}
	for _, c := range res.Capabilities {
		by[c.Name] = c.State
	}
	if by["config.model_ready"] != model.CapabilityUnobserved {
		t.Fatalf("model_ready=%s", by["config.model_ready"])
	}
	if by["managed.launch"] == model.CapabilityObserved {
		t.Fatal("缺模型不得开放受管启动")
	}
	if by["model.invoke"] != model.CapabilityUnobserved {
		t.Fatal("未验证模型调用不得 observed")
	}
	if by["cancel.graceful_stop"] != model.CapabilityUnknown || by["session.resume"] != model.CapabilityUnknown {
		t.Fatal("取消／恢复保持 unknown")
	}
}

func TestMissingModelDoesNotRewriteConfigAndBlocksLaunch(t *testing.T) {
	entry := writeCLI(t)
	cfg := baseCfg(t, entry, "zcode/provider=p")
	a, _ := zcode.New(cfg, zcode.WithRunner(fakeRunner{version: "1.0.0"}))
	_, _ = a.Probe(context.Background(), cfg)
	view, _ := a.LastConfig()
	if view.ConfigMutated || view.ModelReady {
		t.Fatalf("%+v", view)
	}
	spec := model.RunSpec{
		Instance: "ti_zcode1", Version: 1, RunID: "run_zcode1", Generation: 1,
		Workspace: model.WorkspaceSpec{
			Path: t.TempDir(), BaselineCommit: model.Commit("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		},
		Input: "x", InputMode: model.InputArg,
		Permission: model.PermissionProfile{Profile: "default"}, Timeout: time.Minute,
	}
	_, err := a.BuildLaunchSpec(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "缺模型") {
		t.Fatalf("err=%v", err)
	}
}

func TestExplicitModelOpensManagedLaunchNotInvoke(t *testing.T) {
	entry := writeCLI(t)
	cfg := baseCfg(t, entry, "zcode/provider=openai/model=gpt-test")
	a, _ := zcode.New(cfg, zcode.WithRunner(fakeRunner{version: "1.0.0"}))
	res, err := a.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]model.CapabilityState{}
	for _, c := range res.Capabilities {
		by[c.Name] = c.State
	}
	if by["managed.launch"] != model.CapabilityObserved {
		t.Fatalf("launch=%s", by["managed.launch"])
	}
	if by["model.invoke"] == model.CapabilityObserved {
		t.Fatal("invoke must stay unobserved")
	}
	spec := model.RunSpec{
		Instance: "ti_zcode2", Version: 1, RunID: "run_zcode2", Generation: 1,
		Workspace: model.WorkspaceSpec{
			Path: t.TempDir(), BaselineCommit: model.Commit("cccccccccccccccccccccccccccccccccccccccc"),
		},
		Input: "hi", InputMode: model.InputArg,
		Permission: model.PermissionProfile{Profile: "default"}, Timeout: time.Minute,
	}
	launch, err := a.BuildLaunchSpec(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(launch.Argv, " ")
	if !strings.Contains(joined, "--model") || !strings.Contains(joined, "gpt-test") {
		t.Fatalf("argv=%v", launch.Argv)
	}
	_, err = a.Decode(context.Background(), ports.DecodeRequest{
		RecordRef: "r1", Cursor: 1, Protocol: model.Protocol{Format: "json", Version: "1"}, Payload: []byte(`{}`),
	})
	if err == nil {
		t.Fatal("decode must refuse unverified invoke")
	}
}

func TestRelativePathRejected(t *testing.T) {
	_, err := zcode.ResolveEntry(model.NodeConfig{EntryPath: "bin/zcode"})
	if err == nil {
		t.Fatal("relative path must fail")
	}
}
