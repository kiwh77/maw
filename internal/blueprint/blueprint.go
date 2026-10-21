package blueprint

import (
	"os"

	"github.com/pelletier/go-toml/v2"

	"github.com/wuhao/goal-agents/internal/db"
	"github.com/wuhao/goal-agents/internal/errx"
	"github.com/wuhao/goal-agents/internal/scope"
)

type File struct {
	Meta        Meta          `toml:"meta"`
	Tasks       []TaskSpec    `toml:"task"`
	SharedFiles []SharedFile  `toml:"shared_files"`
	Gates       map[string]any `toml:"gates"`
}

type Meta struct {
	Name    string `toml:"name"`
	Version int    `toml:"version"`
}

type TaskSpec struct {
	ID          string   `toml:"id"`
	Title       string   `toml:"title"`
	Priority    int      `toml:"priority"`
	Deps        []string `toml:"deps"`
	WriteScope  []string `toml:"write_scope"`
	ReadScope   []string `toml:"read_scope"`
	Acceptance  []string `toml:"acceptance"`
	MergeOrder  int      `toml:"merge_order"`
}

type SharedFile struct {
	Path      string `toml:"path"`
	OwnerTask string `toml:"owner_task"`
}

const Example = `[meta]
name = "example"
version = 1

[[task]]
id = "T-001"
title = "Example shared setup"
priority = 1
deps = []
write_scope = ["README.md"]
acceptance = ["README updated"]
merge_order = 1

[[task]]
id = "T-002"
title = "Example feature A"
priority = 2
deps = ["T-001"]
write_scope = ["src/a/**"]
read_scope = ["src/common/**"]
acceptance = ["feature A works"]
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

func Validate(f *File) []string {
	var errs []string
	if len(f.Tasks) == 0 {
		return []string{"blueprint has no [[task]] entries"}
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
	}
	graph := map[string][]string{}
	for _, t := range f.Tasks {
		for _, d := range t.Deps {
			if !ids[d] {
				errs = append(errs, "unknown dependency "+d)
			}
		}
		graph[t.ID] = t.Deps
	}
	if cyc := findCycle(graph); cyc != "" {
		errs = append(errs, "dependency cycle: "+cyc)
	}
	for i := 0; i < len(f.Tasks); i++ {
		for j := i + 1; j < len(f.Tasks); j++ {
			o := scope.ScopesOverlap(f.Tasks[i].WriteScope, f.Tasks[j].WriteScope)
			if len(o) > 0 {
				errs = append(errs, "write_scope overlap "+f.Tasks[i].ID+" vs "+f.Tasks[j].ID+": "+o[0])
			}
		}
	}
	for _, sf := range f.SharedFiles {
		if sf.Path == "" || sf.OwnerTask == "" {
			errs = append(errs, "shared_files entry needs path and owner_task")
			continue
		}
		if !ids[sf.OwnerTask] {
			errs = append(errs, "shared_files "+sf.Path+": owner_task "+sf.OwnerTask+" unknown")
		}
	}
	return errs
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
		status := "ready"
		if len(t.Deps) > 0 {
			status = "backlog"
		}
		out = append(out, &db.Task{
			ID: t.ID, Title: t.Title, Status: status,
			Priority: prio, MergeOrder: mo, Deps: t.Deps,
			WriteScope: t.WriteScope, ReadScope: t.ReadScope, Acceptance: t.Acceptance,
			Extra: map[string]any{},
		})
	}
	return out
}
