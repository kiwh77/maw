package identity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/config"
)

func cfgWithLeads(leads ...string) *config.Config {
	return &config.Config{Identity: config.Identity{
		Workers:    []string{"w1"},
		Reviewers:  []string{"r1"},
		Integrator: "int",
		Leads:      leads,
	}}
}

// 空配置 / 命中 / 非命中：IsLead 与 RequireLead 必须一致。
func TestIsLeadAndRequireLead(t *testing.T) {
	cases := []struct {
		name  string
		cfg   *config.Config
		agent string
		want  bool
	}{
		{"空配置", cfgWithLeads(), "alice", false},
		{"命中", cfgWithLeads("alice", "bob"), "alice", true},
		{"命中第二个", cfgWithLeads("alice", "bob"), "bob", true},
		{"非命中", cfgWithLeads("alice"), "carol", false},
		{"空 agent", cfgWithLeads("alice"), "", false},
		{"nil 配置", nil, "alice", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsLead(c.agent, c.cfg); got != c.want {
				t.Fatalf("IsLead(%q)=%v want %v", c.agent, got, c.want)
			}
			err := RequireLead(c.agent, c.cfg)
			if c.want && err != nil {
				t.Fatalf("lead 不应报错，得到 %v", err)
			}
			if !c.want {
				if err == nil {
					t.Fatal("非 lead 必须返回错误")
				}
				if !strings.Contains(err.Error(), "lead") {
					t.Fatalf("错误信息应说明 lead 能力，得到 %q", err.Error())
				}
			}
		})
	}
}

// integrator / reviewer / worker 都不因为角色自动成为 lead。
func TestRolesDoNotBecomeLeadAutomatically(t *testing.T) {
	cfg := cfgWithLeads() // Leads 为空
	for _, agent := range []string{"int", "r1", "w1"} {
		if IsLead(agent, cfg) {
			t.Fatalf("%q 仅因角色就成了 lead", agent)
		}
		if err := RequireLead(agent, cfg); err == nil {
			t.Fatalf("%q 非 lead 却通过了 RequireLead", agent)
		}
	}
}

// lead 可以同时是 worker：IsLead 不改 Role() 返回值。
func TestLeadKeepsWorkerRole(t *testing.T) {
	cfg := cfgWithLeads("alice")
	if !IsLead("alice", cfg) {
		t.Fatal("alice 应是 lead")
	}
	if got := Role("alice", cfg); got != "worker" {
		t.Fatalf("lead 仍是 worker，Role=%q", got)
	}
	if got := Role("int", cfg); got != "integrator" {
		t.Fatalf("integrator 角色不变，Role=%q", got)
	}
	if got := Role("r1", cfg); got != "reviewer" {
		t.Fatalf("reviewer 角色不变，Role=%q", got)
	}
}

// 显式把 integrator 列入 leads 是允许的（能力标记与角色互不排斥）。
func TestIntegratorCanAlsoBeExplicitLead(t *testing.T) {
	cfg := cfgWithLeads("int")
	if !IsLead("int", cfg) {
		t.Fatal("显式登记的 integrator 应是 lead")
	}
	if got := Role("int", cfg); got != "integrator" {
		t.Fatalf("角色不受影响，Role=%q", got)
	}
}

// config.go 的默认模板与 toml 解析（config_test.go 不在本波独占范围，故就地验证）。
func TestConfigLoadParsesLeads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.toml")
	body := "[identity]\nworkers = []\nreviewers = [\"r1\"]\nintegrator = \"int\"\nleads = [\"alice\", \"bob\"]\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Identity.Leads) != 2 || cfg.Identity.Leads[0] != "alice" {
		t.Fatalf("leads 未被解析: %v", cfg.Identity.Leads)
	}
	if !IsLead("bob", cfg) || IsLead("r1", cfg) {
		t.Fatal("解析出的 leads 与 IsLead 不一致")
	}
}

func TestConfigRenderDefaultHasLeads(t *testing.T) {
	if !strings.Contains(config.RenderDefault(t.TempDir()), "leads = []") {
		t.Fatal("默认模板的 [identity] 应含 leads = []")
	}
}
