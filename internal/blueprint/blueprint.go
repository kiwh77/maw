package blueprint

import (
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/i18n"
)

type File struct {
	Meta        Meta            `toml:"meta"`
	Iterations  []IterationSpec `toml:"iteration"`
	Tasks       []TaskSpec      `toml:"task"`
	SharedFiles []SharedFile    `toml:"shared_files"`
	Gates       map[string]any  `toml:"gates"`
}

type Meta struct {
	Name       string `toml:"name"`
	Version    int    `toml:"version"`
	MainBranch string `toml:"main_branch"` // 本波合入/worktree 基线；plan apply 时同步到 config
}

// IterationSpec 是一批任务的容器声明（F49/F50/F54）。
//
// Status 留空时按 planning 处理；TargetBranch 留空时继承 meta.main_branch。
type IterationSpec struct {
	ID           string   `toml:"id"`
	Name         string   `toml:"name"`
	Goal         string   `toml:"goal"`
	TargetBranch string   `toml:"target_branch"`
	Status       string   `toml:"status"`
	Deps         []string `toml:"deps"`
}

type TaskSpec struct {
	ID         string   `toml:"id"`
	Title      string   `toml:"title"`
	Goal       string   `toml:"goal"`
	Priority   int      `toml:"priority"`
	Iteration  string   `toml:"iteration"`
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

[[iteration]]
id = "IT-001"
name = "第一批交付"
goal = "现状：任务散落无批次。目标：统一归属并作为合入单位。"
target_branch = "main"
status = "planning"

[[task]]
id = "T-001"
title = "更新 README 安装说明"
goal = "现状：安装步骤缺失。目标：README 含一键安装命令。"
priority = 1
iteration = "IT-001"
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
iteration = "IT-001"
deps = ["T-001"]
write_scope = ["src/a/**"]
read_scope = ["src/common/**"]
acceptance = ["src/a 可 go build"]
verify = ["go build ./src/a/..."]
not_doing = ["不改 src/common"]
merge_order = 2
`

// iterationStatusAliases 同时接受契约里的英文规范值与人读的中文标签。
var iterationStatusAliases = map[string]string{
	db.IterationPlanning:  db.IterationPlanning,
	db.IterationRunning:   db.IterationRunning,
	db.IterationFinishing: db.IterationFinishing,
	db.IterationMerged:    db.IterationMerged,
	db.IterationAbandoned: db.IterationAbandoned,
	"规划中":                 db.IterationPlanning,
	"执行中":                 db.IterationRunning,
	"收尾中":                 db.IterationFinishing,
	"已合入":                 db.IterationMerged,
	"已放弃":                 db.IterationAbandoned,
}

func normalizeIterationStatus(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return db.IterationPlanning, true
	}
	v, ok := iterationStatusAliases[s]
	return v, ok
}

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, i18n.E("err.blueprint_missing", "next.blueprint_path", path)
	}
	var f File
	if err := toml.Unmarshal(b, &f); err != nil {
		return nil, i18n.EW("err.blueprint_invalid", "next.fix_toml", err)
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

	iterationIDs := map[string]bool{}
	for _, it := range f.Iterations {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			errs = append(errs, "iteration missing id")
			continue
		}
		if iterationIDs[id] {
			errs = append(errs, "duplicate iteration id "+id)
		}
		iterationIDs[id] = true
		if strings.TrimSpace(it.Name) == "" {
			errs = append(errs, id+": iteration name required")
		}
		if _, ok := normalizeIterationStatus(it.Status); !ok {
			errs = append(errs, id+": invalid iteration status "+strings.TrimSpace(it.Status))
		}
		if tb := strings.TrimSpace(it.TargetBranch); strings.ContainsAny(tb, " \t\n") {
			errs = append(errs, id+": target_branch must not contain whitespace")
		}
	}
	iterationGraph := map[string][]string{}
	for _, it := range f.Iterations {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			continue
		}
		var deps []string
		for _, d := range it.Deps {
			d = strings.TrimSpace(d)
			if d == "" {
				continue
			}
			if !iterationIDs[d] {
				errs = append(errs, id+": unknown iteration dependency "+d)
				continue
			}
			deps = append(deps, d)
		}
		iterationGraph[id] = deps
	}
	if cyc := findCycle(iterationGraph); cyc != "" {
		errs = append(errs, "iteration dependency cycle: "+cyc)
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
		if ref := strings.TrimSpace(t.Iteration); ref != "" && !iterationIDs[ref] {
			errs = append(errs, t.ID+": unknown iteration "+ref)
		}
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
			ID: t.ID, IterationID: strings.TrimSpace(t.Iteration), Title: t.Title, Status: status,
			Priority: prio, MergeOrder: mo, Deps: t.Deps,
			WriteScope: t.WriteScope, ReadScope: t.ReadScope, Acceptance: t.Acceptance,
			Extra: extra,
		})
	}
	return out
}

// IterationsForDB 把蓝图迭代声明转成待写库的迭代模型。
//
// 调用方负责补 ProjectID 与时间戳；TargetBranch 未声明时继承 meta.main_branch，
// Status 未声明或使用中文标签时归一为 db 层的规范值。
func IterationsForDB(f *File) []*db.Iteration {
	var out []*db.Iteration
	for _, it := range f.Iterations {
		id := strings.TrimSpace(it.ID)
		if id == "" {
			continue
		}
		status, ok := normalizeIterationStatus(it.Status)
		if !ok {
			status = db.IterationPlanning
		}
		target := strings.TrimSpace(it.TargetBranch)
		if target == "" {
			target = strings.TrimSpace(f.Meta.MainBranch)
		}
		extra := map[string]any{}
		if len(it.Deps) > 0 {
			extra["deps"] = it.Deps
		}
		out = append(out, &db.Iteration{
			ID: id, Name: strings.TrimSpace(it.Name), Goal: strings.TrimSpace(it.Goal),
			Status: status, TargetBranch: target, Extra: extra,
		})
	}
	return out
}
