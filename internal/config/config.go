package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/wuhao/goal-agents/internal/errx"
	"github.com/wuhao/goal-agents/internal/home"
)

type Config struct {
	Project   Project   `toml:"project"`
	Approvals Approvals `toml:"approvals"`
	Gates     Gates     `toml:"gates"`
	Locks     Locks     `toml:"locks"`
	Launcher  Launcher  `toml:"launcher"`
	Identity  Identity  `toml:"identity"`
}

type Project struct {
	MainBranch    string `toml:"main_branch"`
	MergeStrategy string `toml:"merge_strategy"`
	MaxParallel   int    `toml:"max_parallel"`
	Blueprint     string `toml:"blueprint"`
}

type Approvals struct {
	Plan     string `toml:"plan"`
	Merge    string `toml:"merge"`
	Teardown string `toml:"teardown"`
	Review   string `toml:"review"`
}

type Gates struct {
	Commands   []string `toml:"commands"`
	TimeoutSec int      `toml:"timeout_sec"`
}

type Locks struct {
	TTLSec          int `toml:"ttl_sec"`
	HeartbeatTTLSec int `toml:"heartbeat_ttl_sec"`
}

type Launcher struct {
	Mode string `toml:"mode"`
}

type Identity struct {
	Workers    []string `toml:"workers"`
	Reviewers  []string `toml:"reviewers"`
	Integrator string   `toml:"integrator"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errx.New("config not found: "+path, "run `maw init`")
	}
	var c Config
	if err := toml.Unmarshal(b, &c); err != nil {
		return nil, errx.Wrap("invalid config.toml", "fix TOML syntax", err)
	}
	if c.Project.MainBranch == "" {
		c.Project.MainBranch = "main"
	}
	if c.Project.MaxParallel == 0 {
		c.Project.MaxParallel = 4
	}
	if c.Identity.Integrator == "" {
		c.Identity.Integrator = "int"
	}
	if c.Approvals.Review == "" {
		c.Approvals.Review = "auto"
	}
	if c.Approvals.Plan == "" {
		c.Approvals.Plan = "manual"
	}
	if c.Approvals.Merge == "" {
		c.Approvals.Merge = "manual"
	}
	if c.Gates.TimeoutSec == 0 {
		c.Gates.TimeoutSec = 900
	}
	return &c, nil
}

func DetectGates(repo string) []string {
	if _, err := os.Stat(filepath.Join(repo, "package.json")); err == nil {
		b, _ := os.ReadFile(filepath.Join(repo, "package.json"))
		s := string(b)
		var cmds []string
		if strings.Contains(s, `"lint"`) {
			cmds = append(cmds, "npm run lint")
		}
		if strings.Contains(s, `"typecheck"`) {
			cmds = append(cmds, "npm run typecheck")
		}
		if strings.Contains(s, `"test"`) {
			cmds = append(cmds, "npm test")
		}
		if len(cmds) == 0 {
			return []string{"npm test"}
		}
		return cmds
	}
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err == nil {
		return []string{"go test ./..."}
	}
	if _, err := os.Stat(filepath.Join(repo, "pyproject.toml")); err == nil {
		return []string{"pytest"}
	}
	if _, err := os.Stat(filepath.Join(repo, "Cargo.toml")); err == nil {
		return []string{"cargo test"}
	}
	return nil
}

func DetectMainBranch(repo string) string {
	if out, err := home.Git(repo, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		parts := strings.Split(out, "/")
		if n := parts[len(parts)-1]; n != "" {
			return n
		}
	}
	for _, name := range []string{"main", "master"} {
		if _, err := home.Git(repo, "show-ref", "--verify", "refs/heads/"+name); err == nil {
			return name
		}
	}
	return "main"
}

func RenderDefault(repo string) string {
	gates := DetectGates(repo)
	var gateLit string
	if len(gates) == 0 {
		gateLit = "[]"
	} else {
		parts := make([]string, len(gates))
		for i, g := range gates {
			parts[i] = `"` + g + `"`
		}
		gateLit = "[" + strings.Join(parts, ", ") + "]"
	}
	main := DetectMainBranch(repo)
	return `[project]
main_branch = "` + main + `"
merge_strategy = "squash"
max_parallel = 4
blueprint = ".multiagent/blueprint.toml"

[approvals]
plan = "manual"
merge = "manual"
teardown = "manual"
review = "auto"

[gates]
commands = ` + gateLit + `
timeout_sec = 900

[locks]
ttl_sec = 1800
heartbeat_ttl_sec = 900

[launcher]
mode = "manual"

[identity]
workers = ["w1", "w2", "w3"]
reviewers = ["r1"]
integrator = "int"
`
}
