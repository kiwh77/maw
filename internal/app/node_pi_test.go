package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

func TestIsPiRuntimeArgv(t *testing.T) {
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{"/usr/local/bin/node", "/repo/runtimes/pi/src/main.mjs"}, true},
		{[]string{"node", "/repo/runtimes/pi/src/main.mjs"}, true},
		{[]string{"/usr/bin/node", "/repo/runtimes/pi/src/other.mjs"}, false},
		{[]string{"/usr/bin/python3", "/repo/runtimes/pi/src/main.mjs"}, false},
		{[]string{"/usr/bin/node", "/repo/scripts/main.mjs"}, false},
		{[]string{"/usr/bin/node"}, false},
		{nil, false},
	}
	for _, tc := range cases {
		if got := isPiRuntimeArgv(tc.argv); got != tc.want {
			t.Fatalf("isPiRuntimeArgv(%v) = %v，期望 %v", tc.argv, got, tc.want)
		}
	}
}

func TestNodeRegisterMarksPiRuntime(t *testing.T) {
	f := newFixture(t)
	// 用绝对 node 路径 + runtimes/pi 入口登记：应被识别为 pi 适配器并开启自动探测。
	res, err := NodeRegister(f.repo, NodeRegisterInput{
		ID:       "pi-1",
		ArgvJSON: `["/usr/bin/node","` + f.repo + `/runtimes/pi/src/main.mjs"]`,
	})
	if err != nil {
		t.Fatalf("NodeRegister: %v", err)
	}
	if res["adapter_kind"] != "local_cli" {
		t.Fatalf("节点行仍是通用 local_cli：%v", res["adapter_kind"])
	}
	var node *db.Node
	if err := withGlobalRead(f.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		node, e = db.GetNode(tx, "pi-1")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if node == nil {
		t.Fatal("节点未落库")
	}
	if metaString(node.Meta, "adapter_kind") != "pi" {
		t.Fatalf("应识别为 Pi 适配器：%+v", node.Meta)
	}
	if !metaBool(node.Meta, "auto_probe", false) {
		t.Fatalf("Pi 节点应开启自动探测：%+v", node.Meta)
	}
}

func TestProbePiRuntimeReportsVersionAndProtocol(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "probe.mjs")
	body := `process.stdout.write(JSON.stringify({ok:true,protocol:{format:"pi-stream-json",version:"1"},node:process.version,sdk:"1.5.2"})+"\n");`
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/usr/bin/env"); err != nil {
		t.Skip("需要 /usr/bin/env")
	}
	ok, detail, info := probePiRuntime([]string{"node", script})
	if !ok {
		t.Fatalf("探测失败：%s", detail)
	}
	if info["version"] != "1.5.2" || info["protocol_format"] != "pi-stream-json" {
		t.Fatalf("探测信息不完整：%+v", info)
	}

	// 协议不符：不把「大概兼容」当可用。
	bad := filepath.Join(dir, "bad.mjs")
	_ = os.WriteFile(bad, []byte(`process.stdout.write(JSON.stringify({ok:true,protocol:{format:"stream-json",version:"1"},sdk:"1.5.2"})+"\n");`), 0o644)
	if ok, _, _ := probePiRuntime([]string{"node", bad}); ok {
		t.Fatal("协议不符必须判探测失败")
	}
}

func TestApplyPiLaunchCarriesScopeAndProtocol(t *testing.T) {
	f := newFixture(t)
	rt, err := NewRuntime(f.pc, "")
	if err != nil {
		t.Fatal(err)
	}
	op := launchOp(f.pc)
	// 落一条带写盘范围的任务；实例身份由 UpsertTask 分配，再用它反查。
	var instanceID string
	if err := withGlobalWrite(f.pc.GlobalDB, func(tx *sql.Tx) error {
		task := &db.Task{
			ProjectID: f.pc.ID(), ID: "GL-901",
			Title: "Pi 任务", Status: "in_progress", Priority: 10, MergeOrder: 10,
			WriteScope: []string{"internal/goal/**"}, ReadScope: []string{"docs/**"},
			Deps: []string{}, Acceptance: []string{"ok"},
		}
		if err := db.UpsertTask(tx, task); err != nil {
			return err
		}
		instanceID = task.InstanceID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	op.Instance = model.InstanceID(instanceID)

	node := &db.Node{ID: "pi-1", Meta: map[string]any{
		"adapter_kind":  "pi",
		"allow_bash":    true,
		"default_model": "pi-model",
	}}
	spec := model.LaunchSpec{
		Executable:   "/usr/bin/node",
		Argv:         []string{"/repo/runtimes/pi/src/main.mjs"},
		Cwd:          "/abs/worktree",
		EnvAllowlist: []string{"PATH"},
	}
	if err := rt.applyPiLaunch(op, node, &spec); err != nil {
		t.Fatalf("applyPiLaunch: %v", err)
	}
	if spec.StdinMode != model.StdinPipe || spec.StdinData == "" {
		t.Fatalf("配置必须走 stdin：%+v", spec)
	}
	if spec.ExpectedProtocol.Format != "pi-stream-json" || spec.ExpectedProtocol.Version != "1" {
		t.Fatalf("协议不对：%+v", spec.ExpectedProtocol)
	}
	var cfg piLaunchConfig
	if err := json.Unmarshal([]byte(spec.StdinData), &cfg); err != nil {
		t.Fatalf("stdin 配置不是 JSON：%v", err)
	}
	if len(cfg.WriteScope) != 1 || cfg.WriteScope[0] != "internal/goal/**" {
		t.Fatalf("写盘范围没从任务卡带过来：%+v", cfg)
	}
	if cfg.Cwd != "/abs/worktree" || cfg.RunID != string(op.RunID) || cfg.Generation != int(op.Generation) {
		t.Fatalf("运行身份不完整：%+v", cfg)
	}
	if !cfg.AllowBash || cfg.Model != "pi-model" {
		t.Fatalf("节点配置没生效：%+v", cfg)
	}
	env := spec.EnvAllowlist
	joined := strings.Join(env, ",")
	if !strings.Contains(joined, "PI_MODEL") || !strings.Contains(joined, "PATH") {
		t.Fatalf("环境白名单缺项：%v", env)
	}
	if errs := spec.Validate(); len(errs) > 0 {
		t.Fatalf("启动规格应通过校验：%v", model.Err(errs))
	}
}
