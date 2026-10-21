package hooks

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wuhao/goal-agents/internal/home"
)

const preCommit = `#!/bin/sh
# maw pre-commit
if command -v maw >/dev/null 2>&1; then
  exec maw hook pre-commit
fi
exit 0
`

const prePush = `#!/bin/sh
# maw pre-push
if command -v maw >/dev/null 2>&1; then
  exec maw hook pre-push
fi
exit 0
`

func Install(repo string) ([]string, error) {
	hdir, err := home.Git(repo, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(hdir) {
		hdir = filepath.Join(repo, hdir)
	}
	if err := os.MkdirAll(hdir, 0o755); err != nil {
		return nil, err
	}
	var written []string
	for name, content := range map[string]string{"pre-commit": preCommit, "pre-push": prePush} {
		p := filepath.Join(hdir, name)
		if err := os.WriteFile(p, []byte(content), 0o755); err != nil {
			return written, err
		}
		written = append(written, p)
	}
	src := filepath.Join(repo, home.MultiagentDir, "hooks")
	_ = os.MkdirAll(src, 0o755)
	_ = os.WriteFile(filepath.Join(src, "pre-commit"), []byte(preCommit), 0o644)
	_ = os.WriteFile(filepath.Join(src, "pre-push"), []byte(prePush), 0o644)
	return written, nil
}

func EnsureGitignore(repo string) (bool, error) {
	gi := filepath.Join(repo, ".gitignore")
	b, _ := os.ReadFile(gi)
	lines := strings.Split(string(b), "\n")
	set := map[string]bool{}
	for _, l := range lines {
		set[l] = true
	}
	changed := false
	for _, e := range []string{".multiagent/", ".worktrees/"} {
		if !set[e] {
			lines = append(lines, e)
			changed = true
		}
	}
	if !changed {
		return false, nil
	}
	text := strings.Join(lines, "\n")
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return true, os.WriteFile(gi, []byte(text), 0o644)
}
