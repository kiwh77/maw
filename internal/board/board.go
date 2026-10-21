package board

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"

	"github.com/wuhao/goal-agents/internal/db"
)

func byStatus(tasks []*db.Task) map[string][]*db.Task {
	m := map[string][]*db.Task{}
	for _, t := range tasks {
		m[t.Status] = append(m[t.Status], t)
	}
	return m
}

func RenderTerminal(tasks []*db.Task, maxParallel int) string {
	by := byStatus(tasks)
	done := len(by["done"])
	total := len(tasks)
	if total == 0 {
		total = 1
	}
	pct := 100 * done / total
	filled := 12 * done / total
	bar := strings.Repeat("█", filled) + strings.Repeat("░", 12-filled)
	active := len(by["claimed"]) + len(by["in_progress"]) + len(by["self_check"]) + len(by["in_review"]) + len(by["merging"])
	blocked := by["blocked"]
	var b strings.Builder
	fmt.Fprintf(&b, "项目进度 %s %d/%d (%d%%)   并行 %d/%d   阻塞 %d\n\n",
		bar, done, len(tasks), pct, active, maxParallel, len(blocked))
	for _, key := range []string{"ready", "in_progress", "in_review", "merging", "done"} {
		for _, t := range by[key] {
			owner := t.Owner
			if owner == "" {
				owner = "-"
			}
			fmt.Fprintf(&b, "  %s  %s  %s  %s\n", t.ID, truncate(t.Title, 40), owner, t.Heartbeat)
		}
	}
	if len(blocked) > 0 {
		b.WriteString("BLOCKED:\n")
		for _, t := range blocked {
			fmt.Fprintf(&b, "  %s (%s)\n", t.ID, t.BlockedReason)
		}
	}
	if len(by["conflict"]) > 0 {
		b.WriteString("CONFLICT:\n")
		for _, t := range by["conflict"] {
			fmt.Fprintf(&b, "  %s — needs author fix then resubmit\n", t.ID)
		}
	}
	if len(by["merging"]) > 0 || len(by["in_review"]) > 0 {
		b.WriteString("NEXT: run `maw merge` (integrator)\n")
	} else if len(by["ready"]) > 0 {
		b.WriteString("NEXT: workers run `maw next`\n")
	} else {
		b.WriteString("NEXT: apply a blueprint or wait for deps\n")
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func RenderHTML(tasks []*db.Task, maxParallel int) string {
	by := byStatus(tasks)
	done := len(by["done"])
	data, _ := json.Marshal(tasks)
	var sections strings.Builder
	for _, col := range []struct{ key, label string }{
		{"ready", "READY"}, {"in_progress", "IN PROGRESS"},
		{"in_review", "IN REVIEW"}, {"merging", "MERGING"},
		{"conflict", "CONFLICT"}, {"done", "DONE"}, {"blocked", "BLOCKED"},
	} {
		items := by[col.key]
		var cards strings.Builder
		for _, t := range items {
			owner := t.Owner
			if owner == "" {
				owner = "-"
			}
			fmt.Fprintf(&cards, `<div class="card"><strong>%s</strong> %s<div class="meta">owner=%s</div></div>`,
				html.EscapeString(t.ID), html.EscapeString(t.Title), html.EscapeString(owner))
		}
		if cards.Len() == 0 {
			cards.WriteString(`<p class="empty">—</p>`)
		}
		fmt.Fprintf(&sections, `<section><h2>%s (%d)</h2>%s</section>`, col.label, len(items), cards.String())
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN"><head><meta charset="utf-8"/><title>maw board</title>
<style>
body{font-family:ui-sans-serif,system-ui,sans-serif;margin:1.5rem;background:#0f1419;color:#e7ecf1}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:1rem}
section{background:#1a2332;border-radius:8px;padding:.75rem}
.card{background:#243044;margin:.4rem 0;padding:.5rem;border-radius:6px;font-size:.85rem}
.meta{opacity:.7;font-size:.75rem}
</style></head><body>
<h1>maw board — %d/%d done · parallel cap %d</h1>
<div class="grid">%s</div>
<!-- data: %s -->
</body></html>`, done, len(tasks), maxParallel, sections.String(), html.EscapeString(string(data)))
}

func Write(home string, tasks []*db.Task, maxParallel int) (string, error) {
	dir := filepath.Join(home, "board")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	htmlPath := filepath.Join(dir, "index.html")
	if err := os.WriteFile(htmlPath, []byte(RenderHTML(tasks, maxParallel)), 0o644); err != nil {
		return "", err
	}
	md := "```\n" + RenderTerminal(tasks, maxParallel) + "```\n"
	_ = os.WriteFile(filepath.Join(dir, "BOARD.md"), []byte(md), 0o644)
	return htmlPath, nil
}
