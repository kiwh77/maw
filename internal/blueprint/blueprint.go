package blueprint

import (
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
)

type File struct {
	Meta        Meta          `toml:"meta"`
	Tasks       []TaskSpec    `toml:"task"`
	SharedFiles []SharedFile  `toml:"shared_files"`
	Gates       map[string]any `toml:"gates"`
}

type Meta struct {
	Name       string `toml:"name"`
	Version    int    `toml:"version"`
	MainBranch string `toml:"main_branch"` // 本波合入/worktree 基线；plan apply 时同步到 config
}

type TaskSpec struct {
	ID         string   `toml:"id"`
	Title      string   `toml:"title"`
	Goal       string   `toml:"goal"`
	Priority   int      `toml:"priority"`
	Deps       []string `toml:"deps"`
	WriteScope []string `toml:"write_scope"`
	ReadScope  []string `toml:"read_scope"`
	Acceptance []string `toml:"acceptance"`
	Verify     []string `toml:"verify"`
	NotDoing   []string `toml:"not_doing"`
	MergeOrder int      `toml:"merge_order"`
}

type SharedFile struct {
	Path      string `toml:"path"`
	OwnerTask string `toml:"owner_task"`
}

const Example = `[meta]
name = "example"
version = 1
main_branch = "main"

[[task]]
id = "T-001"
title = "更新 README 安装说明"
goal = "现状：安装步骤缺失。目标：README 含一键安装命令。"
priority = 1
deps = []
write_scope = ["README.md"]
acceptance = ["README 含 ./install.sh 与 maw --version 说明"]
verify = ["manual: 按 README 安装后 maw --version 可运行"]
merge_order = 1

[[task]]
id = "T-002"
title = "实现模块 A 入口"
goal = "现状：src/a 无入口。目标：可编译的 main 包。"
priority = 2
deps = ["T-001"]
write_scope = ["src/a/**"]
read_scope = ["src/common/**"]
acceptance = ["src/a 可 go build"]
verify = ["go build ./src/a/..."]
not_doing = ["不改 src/common"]
merge_order = 2
`

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errx.New("blueprint not found: "+path, "pass a blueprint.toml path")
	}
	var f File
	if err := toml.Unmarshal(b, &f); err != nil {
		return nil, errx.Wrap("invalid blueprint", "fix TOML syntax", err)
	}
	return &f, nil
}

var titleFuzzy = []string{"优化", "完善", "改进", "支持", "处理", "调整", "初步", "相关", "以及", "等", "后续"}

func Validate(f *File) []string {
	var errs []string
	if mb := strings.TrimSpace(f.Meta.MainBranch); f.Meta.MainBranch != "" && mb == "" {
		errs = append(errs, "meta.main_branch is blank")
	} else if f.Meta.MainBranch != "" && strings.ContainsAny(f.Meta.MainBranch, " \t\n") {
		errs = append(errs, "meta.main_branch must not contain whitespace")
	}
	if len(f.Tasks) == 0 {
		errs = append(errs, "blueprint has no [[task]] entries")
		return errs
	}
	ids := map[string]bool{}
	for _, t := range f.Tasks {
		if t.ID == "" {
			errs = append(errs, "task missing id")
			continue
		}
		if ids[t.ID] {
			errs = append(errs, "duplicate task id "+t.ID)
		}
		ids[t.ID] = true
		if len(t.WriteScope) == 0 {
			errs = append(errs, t.ID+": write_scope required")
		}
		if t.Title == "" {
			errs = append(errs, t.ID+": title required")
		}
		// TASK-SPEC 规则 2：acceptance 必需
		if len(nonEmpty(t.Acceptance)) == 0 {
			errs = append(errs, t.ID+": acceptance required (可判定断言)")
		}
		// TASK-SPEC 规则 3：verify 必需（或 acceptance 内 Verify: 前缀兼容）
		if len(nonEmpty(t.Verify)) == 0 && !hasVerifyPrefix(t.Acceptance) {
			errs = append(errs, t.ID+": verify required（可执行命令或 manual: …）")
		}
	}
	graph := map[string][]string{}
	for _, t := range f.Tasks {
		// deps 允许指向蓝图外的历史题（增量 apply 场景：依赖已 done 的 T-xxx）。
		// 运行时 CanActivate 若找不到对应 done 记录，题会留在 backlog，不会误激活。
		// 仅当边两端都在本文件内时参与环检测。
		var inFileDeps []string
		for _, d := range t.Deps {
			if ids[d] {
				inFileDeps = append(inFileDeps, d)
			}
		}
		graph[t.ID] = inFileDeps
	}
	if cyc := findCycle(graph); cyc != "" {
		errs = append(errs, "dependency cycle: "+cyc)
	}
	// write_scope 允许重叠：运行时由 schedule 按 deps + priority→merge_order→id 串行激活，
	// 因而可一次压入同目录后续题（如 T-061→T-062→T-073），无需拆多波 apply。
	for _, sf := range f.SharedFiles {
		if sf.Path == "" || sf.OwnerTask == "" {
			errs = append(errs, "shared_files entry needs path and owner_task")
		}
		// owner_task 允许指向蓝图外历史题（增量 apply）
	}
	return errs
}

// ValidateLint returns non-fatal warnings (TASK-SPEC 规则 1/5/7/8 等)。
func ValidateLint(f *File) []string {
	var warns []string
	if f == nil || len(f.Tasks) == 0 {
		return warns
	}
	entryCount := 0
	for _, t := range f.Tasks {
		if len(t.Deps) == 0 {
			entryCount++
		}
		low := strings.ToLower(t.Title)
		for _, w := range titleFuzzy {
			if strings.Contains(t.Title, w) || strings.Contains(low, strings.ToLower(w)) {
				warns = append(warns, t.ID+": title 含模糊词「"+w+"」（规则 1）")
				break
			}
		}
		tops := topModules(t.WriteScope)
		if len(tops) >= 3 {
			warns = append(warns, t.ID+": write_scope 跨 ≥3 个顶层模块（规则 5）")
		}
	}
	if entryCount == 0 {
		warns = append(warns, "波次无 deps 入口为 0（规则 7：至少保留无 deps 入口）")
	}
	depth := maxDepsDepth(f)
	if depth > 8 {
		warns = append(warns, "依赖链深度 "+itoa(depth)+" > 8（规则 8）")
	}
	return warns
}

// ParallelWidth reports deps depth / entries / layer widths for plan check.
func ParallelWidth(f *File) map[string]any {
	ids := map[string]bool{}
	graph := map[string][]string{}
	for _, t := range f.Tasks {
		ids[t.ID] = true
	}
	for _, t := range f.Tasks {
		var deps []string
		for _, d := range t.Deps {
			if ids[d] {
				deps = append(deps, d)
			}
		}
		graph[t.ID] = deps
	}
	depthOf := map[string]int{}
	var depth func(string) int
	depth = func(id string) int {
		if v, ok := depthOf[id]; ok {
			return v
		}
		maxD := 0
		for _, d := range graph[id] {
			if dd := depth(d) + 1; dd > maxD {
				maxD = dd
			}
		}
		depthOf[id] = maxD
		return maxD
	}
	entries := 0
	maxDepth := 0
	layers := map[int]int{}
	for _, t := range f.Tasks {
		if len(graph[t.ID]) == 0 {
			entries++
		}
		d := depth(t.ID)
		if d > maxDepth {
			maxDepth = d
		}
		layers[d]++
	}
	widths := make([]int, maxDepth+1)
	for d, n := range layers {
		if d >= 0 && d < len(widths) {
			widths[d] = n
		}
	}
	maxParallel := 0
	for _, w := range widths {
		if w > maxParallel {
			maxParallel = w
		}
	}
	return map[string]any{
		"deps_depth": maxDepth, "entries": entries,
		"layer_widths": widths, "max_parallel_theory": maxParallel,
		"task_count": len(f.Tasks),
	}
}

func nonEmpty(ss []string) []string {
	var out []string
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func hasVerifyPrefix(acc []string) bool {
	for _, a := range acc {
		if strings.HasPrefix(strings.TrimSpace(a), "Verify:") || strings.HasPrefix(strings.TrimSpace(a), "verify:") {
			return true
		}
	}
	return false
}

func topModules(scopes []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range scopes {
		s = strings.ReplaceAll(s, "\\", "/")
		s = strings.TrimPrefix(s, "./")
		parts := strings.Split(s, "/")
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		top := parts[0]
		if !seen[top] {
			seen[top] = true
			out = append(out, top)
		}
	}
	return out
}

func maxDepsDepth(f *File) int {
	rep := ParallelWidth(f)
	if d, ok := rep["deps_depth"].(int); ok {
		return d
	}
	return 0
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func findCycle(graph map[string][]string) string {
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var stack []string
	var dfs func(string) string
	dfs = func(n string) string {
		if visited[n] {
			return ""
		}
		if visiting[n] {
			for i, x := range stack {
				if x == n {
					return join(append(stack[i:], n))
				}
			}
		}
		visiting[n] = true
		stack = append(stack, n)
		for _, d := range graph[n] {
			if c := dfs(d); c != "" {
				return c
			}
		}
		stack = stack[:len(stack)-1]
		visiting[n] = false
		visited[n] = true
		return ""
	}
	for n := range graph {
		if c := dfs(n); c != "" {
			return c
		}
	}
	return ""
}

func join(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	s := parts[0]
	for i := 1; i < len(parts); i++ {
		s += " -> " + parts[i]
	}
	return s
}

func TasksForDB(f *File) []*db.Task {
	var out []*db.Task
	for _, t := range f.Tasks {
		prio := t.Priority
		if prio == 0 {
			prio = 100
		}
		mo := t.MergeOrder
		if mo == 0 {
			mo = prio
		}
		// 一律先入 backlog，由 promoteReady / schedule.CanActivate 按 deps+写盘优先级抬升为 ready。
		status := "backlog"
		extra := map[string]any{}
		if g := strings.TrimSpace(t.Goal); g != "" {
			extra["goal"] = g
		}
		if len(t.Verify) > 0 {
			extra["verify"] = t.Verify
		}
		if len(t.NotDoing) > 0 {
			extra["not_doing"] = t.NotDoing
		}
		out = append(out, &db.Task{
			ID: t.ID, Title: t.Title, Status: status,
			Priority: prio, MergeOrder: mo, Deps: t.Deps,
			WriteScope: t.WriteScope, ReadScope: t.ReadScope, Acceptance: t.Acceptance,
			Extra: extra,
		})
	}
	return out
}
