package identity

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
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
	return "", i18n.E("err.no_identity", "next.set_agent")
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
		return i18n.EN("err.not_integrator", []any{agent}, "next.export_integrator", cfg.Identity.Integrator)
	}
	return nil
}

func RequireOwner(agent string, t *db.Task) error {
	if t.Owner != "" && t.Owner != agent {
		return i18n.E("err.owner_mismatch", "next.use_owner", t.ID, t.Owner, agent)
	}
	return nil
}
