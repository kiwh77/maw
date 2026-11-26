package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/db"
)

// requiredPathsOf 从 extra.required_paths 或 acceptance「required: path」收集基线路径。
func requiredPathsOf(t *db.Task) []string {
	if t == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		p = strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
		p = strings.TrimPrefix(p, "./")
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range extraStringSlice(t, "required_paths") {
		add(p)
	}
	for _, a := range t.Acceptance {
		a = strings.TrimSpace(a)
		low := strings.ToLower(a)
		switch {
		case strings.HasPrefix(low, "required:"):
			add(strings.TrimSpace(a[len("required:"):]))
		case strings.HasPrefix(a, "基线："):
			add(strings.TrimSpace(strings.TrimPrefix(a, "基线：")))
		case strings.HasPrefix(low, "baseline:"):
			add(strings.TrimSpace(a[len("baseline:"):]))
		}
	}
	return out
}

// missingRequiredPaths 返回 worktree（或仓根）上不存在的 required 路径。
func missingRequiredPaths(root string, t *db.Task) []string {
	var missing []string
	for _, p := range requiredPathsOf(t) {
		full := filepath.Join(root, filepath.FromSlash(p))
		if _, err := os.Stat(full); err != nil {
			missing = append(missing, p)
		}
	}
	return missing
}
