package app

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
)

// 工程编辑要同时落到两处：中枢 projects 行与仓外 config.toml。
// 少写一处，下次看板/调度就会拿旧值，界面上「改成功了」却依然按老策略跑。
func TestProjectUpdateAppliesPolicyAndWritesConfig(t *testing.T) {
	f := newFixture(t)
	cfgPath := home.ConfigPath(f.pc.LocalHome)

	name, mainBranch, worker, llm := "renamed", "trunk", "node-a", "node-b"
	maxP, limit, auto := 3, 2, true
	allowed := []string{"node-a", "node-b"}
	res, err := ProjectUpdate(f.repo, ProjectUpdateInput{
		Ref: f.pc.ID(), Name: &name, MainBranch: &mainBranch, MaxParallel: &maxP,
		AutoEnabled: &auto, Limit: &limit, AllowedNodes: &allowed,
		WorkerNode: &worker, LLMNode: &llm,
	})
	if err != nil {
		t.Fatalf("ProjectUpdate: %v", err)
	}
	if res["config_written"] != true {
		t.Fatalf("策略字段变化必须回写 config.toml: %v", res)
	}

	var got *db.Project
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		got, e = db.GetProject(tx, f.pc.ID())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if got.Name != name || got.MainBranch != mainBranch || got.MaxParallel != maxP {
		t.Fatalf("中枢工程行未更新: %+v", got)
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("读回配置失败: %v", err)
	}
	if cfg.Project.MainBranch != mainBranch || cfg.Project.MaxParallel != maxP {
		t.Fatalf("project 段未写回: %+v", cfg.Project)
	}
	if !cfg.Managed.AutoEnabled || cfg.Managed.Limit != limit {
		t.Fatalf("managed 段未写回: %+v", cfg.Managed)
	}
	if len(cfg.Managed.AllowedNodes) != 2 ||
		cfg.Managed.AllowedNodes[0] != "node-a" || cfg.Managed.AllowedNodes[1] != "node-b" {
		t.Fatalf("allowed_nodes 未写回: %+v", cfg.Managed.AllowedNodes)
	}
	if cfg.SystemAgent.WorkerNode != worker || cfg.SystemAgent.LLMNode != llm {
		t.Fatalf("系统节点未写回: %+v", cfg.SystemAgent)
	}
	// 只动目标键：原有注释与段落顺序不能因一次编辑被整份重排。
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "[managed]") || !strings.Contains(string(raw), "[system_agent]") {
		t.Fatalf("写回后段落丢失:\n%s", raw)
	}
}

func TestProjectUpdateDryRunDoesNotWrite(t *testing.T) {
	f := newFixture(t)
	cfgPath := home.ConfigPath(f.pc.LocalHome)
	before, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	name := "renamed"
	if _, err := ProjectUpdate(f.repo, ProjectUpdateInput{
		Ref: f.pc.ID(), Name: &name, DryRun: true,
	}); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	var got *db.Project
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		var e error
		got, e = db.GetProject(tx, f.pc.ID())
		return e
	}); err != nil {
		t.Fatal(err)
	}
	if got.Name == name {
		t.Fatal("dry-run 不该写中枢")
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("dry-run 不该改 config.toml")
	}
}
