// Package managed_test 验收首个 CodeBuddy 受管闭环（VN-982）。
//
// 使用隔离 Git／临时全局库与受控 CLI 子进程；样本标注为 synthetic fixture，
// 不是厂商现场实测。真实模型 smoke 见 README（MAW_MANAGED_SMOKE=1）。
package managed_test

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/app"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/claim"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
	"github.com/kiwh77/maw/internal/runner/process"
	"github.com/kiwh77/maw/internal/runner/recovery"
)

type harness struct {
	t       *testing.T
	repo    string
	global  string
	fakeCLI string
	pid     string
	gdb     string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("受管闭环验收以当前 macOS 为对象")
	}
	global := t.TempDir()
	t.Setenv("MAW_GLOBAL", global)
	t.Setenv("MAW_RUNNER_ONCE", "1")
	t.Setenv("MAW_AGENT", "cursor-demo-1")
	t.Setenv("MAW_LANG", "en")

	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-b", "main")
	git("config", "user.email", "t@t")
	git("config", "user.name", "t")
	_ = os.WriteFile(filepath.Join(repo, "README.md"), []byte("# managed-loop\n"), 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "mod_a"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_a", ".gitkeep"), nil, 0o644)
	_ = os.MkdirAll(filepath.Join(repo, "mod_b"), 0o755)
	_ = os.WriteFile(filepath.Join(repo, "mod_b", ".gitkeep"), nil, 0o644)
	git("add", ".")
	git("commit", "-m", "init")

	if _, err := app.Init(repo, false, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	bp := filepath.Join(repo, "bp.toml")
	_ = os.WriteFile(bp, []byte(`
[meta]
name = "managed-loop"
version = 1
main_branch = "main"

[[task]]
id = "VN-L1"
title = "managed loop"
priority = 1
deps = []
write_scope = ["mod_a/**"]
acceptance = ["file exists"]
verify = ["manual: ok"]
merge_order = 1

[[task]]
id = "VN-L2"
title = "manual next still works"
priority = 2
deps = []
write_scope = ["mod_b/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
merge_order = 2
`), 0o644)
	if _, err := app.PlanApply(bp, repo, true, false, false); err != nil {
		t.Fatalf("plan apply: %v", err)
	}

	_, thisFile, _, _ := runtime.Caller(0)
	fakeCLI, err := filepath.Abs(filepath.Join(filepath.Dir(thisFile), "testdata", "fake-codebuddy.sh"))
	if err != nil {
		t.Fatal(err)
	}

	initRes, err := app.Init(repo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := initRes["project_id"].(string)
	gdb, _ := initRes["global_db"].(string)
	if pid == "" {
		gdb = filepath.Join(global, "maw.db")
		_ = db.WithTx(gdb, func(tx *sql.Tx) error {
			ps, e := db.ListProjects(tx)
			if e != nil || len(ps) == 0 {
				return e
			}
			pid = ps[0].ID
			return nil
		})
	}
	return &harness{t: t, repo: repo, global: global, fakeCLI: fakeCLI, pid: pid, gdb: gdb}
}

func TestProbeClaimRunnerManifestAndRecovery(t *testing.T) {
	h := newHarness(t)

	// Probe → 受控 CLI（labeled: tests/managed/testdata/fake-codebuddy.sh）
	cfg := model.NodeConfig{
		NodeID:     "codebuddy-lab",
		Name:       "lab",
		Adapter:    model.AdapterCodeBuddy,
		EntryPath:  h.fakeCLI,
		ProfileRef: "profiles/fake",
		Mode:       model.NodeManagedCLI,
		Platform:   runtime.GOOS,
		SlotLimit:  1,
		AcceptNew:  true,
		Timeout:    time.Minute,
	}
	ad, err := codebuddy.New(cfg, codebuddy.WithHelpProbe(true))
	if err != nil {
		t.Fatalf("adapter: %v", err)
	}
	probe, err := ad.Probe(context.Background(), cfg)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if probe.EntryState != model.EntryReady {
		t.Fatalf("entry not ready: %s", probe.EntryState)
	}

	store, err := db.OpenStore(h.gdb)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{
			ID: "codebuddy-lab", ProjectID: h.pid, Label: "lab", SlotLimit: 1,
		})
	}); err != nil {
		t.Fatal(err)
	}

	var inst model.InstanceID
	var version model.VersionNo
	if err := store.Read(func(tx *sql.Tx) error {
		task, e := db.GetTask(tx, h.pid, "VN-L1")
		if e != nil || task == nil {
			t.Fatalf("task: %v %#v", e, task)
		}
		inst = model.InstanceID(task.InstanceID)
		ti, e := db.GetInstance(tx, task.InstanceID)
		if e != nil || ti == nil || ti.ActiveVersion == nil {
			t.Fatalf("instance: %v", e)
		}
		version = *ti.ActiveVersion
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 显式 Claim + Runner：ManagedStart 走同一 Operation 流程并拉起独立 __runner
	res, err := app.ManagedStart(h.repo, "VN-L1", "codebuddy-lab", "cursor-demo-1", false)
	if err != nil {
		t.Fatalf("ManagedStart (explicit claim→runner): %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("ManagedStart not ok: %+v", res)
	}
	if det, _ := res["runner_detached"].(bool); !det {
		t.Fatal("runner must stay detached when hub exits")
	}
	runID, _ := res["run_id"].(string)
	opID, _ := res["operation_id"].(string)
	if runID == "" || opID == "" {
		t.Fatalf("missing run/op in result: %+v", res)
	}
	outcomeRun := model.RunID(runID)
	outcomeOp := model.OperationID(opID)

	// Manifest：退出码 0 形状但交付缺版本／基线／产物 → 不能完成任务
	_ = version
	invalid := model.Manifest{
		Contract:  model.DeliveryContract,
		ProjectID: model.ProjectID(h.pid),
		Deliveries: []model.Delivery{{
			DeliveryID:   model.DeliveryID("dlv_bad001"),
			TaskInstance: inst,
			RunID:        outcomeRun,
			Mode:         model.DeliveryCode,
			DeliveredAt:  time.Now().UTC(),
		}},
	}
	if errs := invalid.Validate(); len(errs) == 0 {
		t.Fatal("exit-code-0 shaped incomplete delivery must fail Manifest.Validate")
	}
	_ = store.Read(func(tx *sql.Tx) error {
		task, _ := db.GetTask(tx, h.pid, "VN-L1")
		if task != nil && task.Status == string(model.TaskDone) {
			t.Fatal("invalid delivery must not mark task done")
		}
		return nil
	})

	// 中枢重启后对账同一 Run
	runDir := filepath.Join(h.global, "runtime", h.pid, "ops", string(outcomeOp))
	_ = os.MkdirAll(runDir, 0o755)
	nonce, err := process.NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	id, err := process.Capture(string(outcomeRun), os.Getpid(), nonce)
	if err != nil {
		t.Fatalf("capture identity: %v", err)
	}
	if err := process.WriteIdentity(filepath.Join(runDir, recovery.IdentityFile), id); err != nil {
		t.Fatal(err)
	}
	ev, err := recovery.Reconcile(recovery.Options{
		Dir: runDir, RunID: string(outcomeRun), ExpectedNonce: nonce,
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if ev.Identity == nil || ev.Identity.RunID != string(outcomeRun) {
		t.Fatalf("must recover same Run: state=%s identity=%+v", ev.State, ev.Identity)
	}

	// 任务、Run 可分别核对
	_ = store.Read(func(tx *sql.Tx) error {
		task, _ := db.GetTask(tx, h.pid, "VN-L1")
		run, _ := db.GetRun(tx, string(outcomeRun))
		if task == nil || run == nil {
			t.Fatal("task and run must be readable separately")
		}
		if run.InstanceID != string(inst) {
			t.Fatalf("run/task instance mismatch")
		}
		return nil
	})
}

func TestManualNextStillAvailable(t *testing.T) {
	h := newHarness(t)
	store, err := db.OpenStore(h.gdb)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: "codebuddy-lab", ProjectID: h.pid, SlotLimit: 1})
	})
	var inst model.InstanceID
	_ = store.Read(func(tx *sql.Tx) error {
		task, _ := db.GetTask(tx, h.pid, "VN-L1")
		inst = model.InstanceID(task.InstanceID)
		return nil
	})
	ws, _ := filepath.Abs(filepath.Join(h.global, "ws-m"))
	_ = os.MkdirAll(ws, 0o755)
	if _, err := claim.New(store, nil).Claim(context.Background(), claim.Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(h.pid), AgentID: "cursor-managed-1",
			RequestedTask: &inst, IdempotencyKey: "m:vn-l1",
			Capacity: ports.Capacity{NodeSlotLimit: 1, ManagedLimit: 1},
		},
		NodeID: "codebuddy-lab", Workspace: ws,
	}); err != nil {
		t.Fatalf("managed claim: %v", err)
	}

	res, err := app.Next(h.repo, "cursor-demo-1", false, "")
	if err != nil {
		t.Fatalf("manual next must still work: %v", err)
	}
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("manual next: %+v", res)
	}
}
