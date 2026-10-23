package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/runner/process"
)

func TestManagedStatusDefaults(t *testing.T) {
	f := newFixture(t)
	res, err := ManagedStatus(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("status: %+v", res)
	}
	if v, _ := res["config_version"].(int); v != 1 {
		t.Fatalf("config_version=%v", res["config_version"])
	}
	if on, _ := res["auto_enabled"].(bool); on {
		t.Fatal("auto_enabled 默认必须关闭")
	}
	if lim, _ := res["managed_execution_limit"].(int); lim != 1 {
		t.Fatalf("limit=%v want 1", res["managed_execution_limit"])
	}
	supported, _ := res["platform_supported"].(bool)
	if runtime.GOOS == "darwin" && !supported {
		t.Fatal("darwin 应报告 platform_supported")
	}
	if runtime.GOOS != "darwin" && supported {
		t.Fatal("非 darwin 不得报告已验证")
	}
}

func TestManagedStartRequiresExplicitTaskAndNode(t *testing.T) {
	f := newFixture(t)
	_, err := ManagedStart(f.repo, "", "", "cursor-demo-1", true)
	if err == nil {
		t.Fatal("缺 task/node 应失败")
	}
	if e, ok := err.(*errx.Error); !ok || !strings.Contains(e.Msg, i18n.T("err.managed_need_explicit")) {
		// 消息已翻译；核对 next 提示指向显式启动
		if e, ok := err.(*errx.Error); ok {
			if !strings.Contains(e.Next, "managed start") {
				t.Fatalf("next 应指向 managed start，得到 %q", e.Next)
			}
		} else {
			t.Fatalf("应返回结构化错误，得到 %T %v", err, err)
		}
	}
}

func TestManagedStartDryRunAndAutoOff(t *testing.T) {
	f := newFixture(t)
	f.seedTask("VN-M9", "ready", "", []string{"mod_m9/**"})
	res, err := ManagedStart(f.repo, "VN-M9", "codebuddy-lab", "cursor-demo-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if dry, _ := res["dry_run"].(bool); !dry {
		t.Fatalf("dry_run 缺失: %+v", res)
	}
	if on, _ := res["auto_enabled"].(bool); on {
		t.Fatal("dry-run 视图仍应显示 auto_enabled=false")
	}
}

func TestManagedStartLaunchesDetachedRunner(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("受管进程控制仅在 darwin 验收")
	}
	f := newFixture(t)
	f.seedTask("VN-M8", "ready", "", []string{"mod_m8/**"})
	t.Setenv("MAW_RUNNER_ONCE", "1")
	t.Setenv("MAW_AGENT", "cursor-demo-1")

	res, err := ManagedStart(f.repo, "VN-M8", "codebuddy-lab", "cursor-demo-1", false)
	if err != nil {
		t.Fatalf("ManagedStart: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("%+v", res)
	}
	op, _ := res["operation_id"].(string)
	if op == "" || !strings.HasPrefix(op, "op_") {
		t.Fatalf("缺少 operation_id: %+v", res)
	}
	runID, _ := res["run_id"].(string)
	if runID == "" || !strings.HasPrefix(runID, "run_") {
		t.Fatalf("缺少 run_id: %+v", res)
	}
	step, _ := res["dispatch_step"].(map[string]any)
	if step == nil {
		t.Fatalf("缺少 dispatch_step: %+v", res)
	}
	if launched, _ := step["launched"].(bool); !launched {
		t.Fatalf("应启动 Runner: %+v", step)
	}
	if det, _ := res["runner_detached"].(bool); !det {
		t.Fatal("应声明 runner_detached")
	}
}

func TestInternalRunnerTokenWhitelistAndDup(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("锁与身份仅在 darwin 验收")
	}
	state := t.TempDir()
	opID := "op_testrunner01"
	opDir := filepath.Join(state, "ops", opID)
	if err := os.MkdirAll(opDir, 0o755); err != nil {
		t.Fatal(err)
	}
	token := "tok-abc"
	if err := os.WriteFile(filepath.Join(opDir, runnerTokenFile), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nonce, err := process.NewNonce()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv(runnerTokenEnv, "")
	if _, err := InternalRunner(RunnerOpts{OperationID: opID, Nonce: nonce, StateDir: state, Token: "wrong", Once: true}); err == nil {
		t.Fatal("错误令牌应失败")
	}
	if _, err := InternalRunner(RunnerOpts{OperationID: opID, Nonce: nonce, StateDir: state, Token: "", Once: true}); err == nil {
		t.Fatal("缺令牌应失败")
	}

	res, err := InternalRunner(RunnerOpts{
		OperationID: opID, Nonce: nonce, StateDir: state, Token: token, Once: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("%+v", res)
	}
	if _, err := os.Stat(filepath.Join(opDir, "process.json")); err != nil {
		t.Fatalf("应写入身份: %v", err)
	}

	// 持锁期间第二次启动应被拒绝。
	lock, err := process.AcquireLock(filepath.Join(opDir, runnerLockFile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Release() }()
	if _, err := InternalRunner(RunnerOpts{
		OperationID: opID, Nonce: nonce, StateDir: state, Token: token, Once: true,
	}); err == nil {
		t.Fatal("重复启动应失败")
	}
}

func TestEnsureManagedAllowedRejectsAutoWhenOff(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.EnsureManagedAllowed(false); err == nil {
		t.Fatal("auto off 时无显式任务应拒绝")
	}
	if runtime.GOOS == "darwin" {
		if err := rt.EnsureManagedAllowed(true); err != nil {
			t.Fatalf("显式任务在 darwin 应允许: %v", err)
		}
	}
}

func TestConfigEnsureManagedSectionMigrates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	old := `[project]
main_branch = "main"
max_parallel = 0
`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err := config.EnsureManagedSection(path)
	if err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("应写入 [managed]")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Managed.ConfigVersion != 1 || cfg.Managed.AutoEnabled || cfg.Managed.Limit != 1 {
		t.Fatalf("managed=%+v", cfg.Managed)
	}
	wrote2, err := config.EnsureManagedSection(path)
	if err != nil || wrote2 {
		t.Fatalf("第二次不得再写: wrote=%v err=%v", wrote2, err)
	}
}

func TestRenderDefaultIncludesManaged(t *testing.T) {
	text := config.RenderDefault(t.TempDir())
	if !strings.Contains(text, "[managed]") || !strings.Contains(text, "auto_enabled = false") {
		t.Fatalf("RenderDefault 缺少 managed 默认:\n%s", text)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	en, lim := cfg.AutoExecution()
	if en || lim != 1 {
		t.Fatalf("AutoExecution=%v,%d", en, lim)
	}
}

func TestInitConfigHasManagedSection(t *testing.T) {
	f := newFixture(t)
	local, err := home.RequireHome(f.repo)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(home.ConfigPath(local))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[managed]") {
		t.Fatalf("init 配置应含 [managed]:\n%s", raw)
	}
}
