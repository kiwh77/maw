package scope

import (
	"path"
	"strings"

	"github.com/wuhao/goal-agents/internal/errx"
)

func norm(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	p = strings.TrimPrefix(p, "./")
	return path.Clean("/" + p)[1:]
}

func prefixOf(pat string) string {
	pat = norm(pat)
	if strings.HasSuffix(pat, "/**") {
		return strings.TrimSuffix(pat, "/**")
	}
	pat = strings.TrimRight(pat, "*")
	return strings.TrimRight(pat, "/")
}

func ScopesOverlap(a, b []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range a {
		xn := norm(x)
		xp := prefixOf(x)
		for _, y := range b {
			yn := norm(y)
			yp := prefixOf(y)
			hit := xn == yn
			if !hit && xp != "" && yp != "" {
				hit = xp == yp || strings.HasPrefix(xp, yp+"/") || strings.HasPrefix(yp, xp+"/")
			}
			if hit {
				key := xn + " ∩ " + yn
				if !seen[key] {
					seen[key] = true
					out = append(out, key)
				}
			}
		}
	}
	return out
}

func PathInScope(file string, patterns []string) bool {
	rel := norm(file)
	for _, pat := range patterns {
		pn := norm(pat)
		if strings.HasSuffix(pn, "/**") {
			prefix := strings.TrimSuffix(pn, "/**")
			prefix = strings.TrimRight(prefix, "/")
			if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
				return true
			}
			continue
		}
		if strings.ContainsAny(pn, "*?[") {
			ok, _ := path.Match(pn, rel)
			if ok {
				return true
			}
			continue
		}
		if rel == pn || strings.HasPrefix(rel, strings.TrimRight(pn, "/")+"/") {
			return true
		}
	}
	return false
}

func AssertFilesInScope(files, patterns []string, taskID string) error {
	var bad []string
	for _, f := range files {
		if !PathInScope(f, patterns) {
			bad = append(bad, f)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	msg := "task " + taskID + " out-of-scope files: " + strings.Join(bad, ", ")
	if len(bad) > 8 {
		msg = "task " + taskID + " out-of-scope files: " + strings.Join(bad[:8], ", ") + "..."
	}
	return errx.New(msg, "run `maw request-change` or narrow the diff to write_scope")
}
