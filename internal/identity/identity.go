package identity

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wuhao/goal-agents/internal/config"
	"github.com/wuhao/goal-agents/internal/db"
	"github.com/wuhao/goal-agents/internal/errx"
	"github.com/wuhao/goal-agents/internal/home"
)

func Resolve(cwd, explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if v := strings.TrimSpace(os.Getenv("MAW_AGENT")); v != "" {
		return v, nil
	}
	root, err := home.WorktreeRoot(cwd)
	if err != nil {
		if cwd != "" {
			root = cwd
		} else {
			root, _ = os.Getwd()
		}
	}
	b, err := os.ReadFile(filepath.Join(root, ".maw", "agent"))
	if err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return s, nil
		}
	}
	return "", errx.New("cannot determine caller identity", "export MAW_AGENT=w1 (or integrator), or run `maw next` first")
}

func Role(agent string, cfg *config.Config) string {
	if agent == cfg.Identity.Integrator {
		return "integrator"
	}
	for _, r := range cfg.Identity.Reviewers {
		if r == agent {
			return "reviewer"
		}
	}
	return "worker"
}

func RequireIntegrator(agent string, cfg *config.Config) error {
	if Role(agent, cfg) != "integrator" {
		return errx.New("agent "+agent+" is not integrator; merge denied", "export MAW_AGENT="+cfg.Identity.Integrator)
	}
	return nil
}

func RequireOwner(agent string, t *db.Task) error {
	if t.Owner != "" && t.Owner != agent {
		return errx.New("task "+t.ID+" is owned by "+t.Owner+", not "+agent, "use the owning agent identity")
	}
	return nil
}
