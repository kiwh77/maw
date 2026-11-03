package blueprint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func loadBlueprintText(t *testing.T, body string) *File {
	t.Helper()
	p := filepath.Join(t.TempDir(), "bp.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestIterationParsingAndDBMapping(t *testing.T) {
	f := loadBlueprintText(t, `
[meta]
name = "batch"
main_branch = "main"

[[iteration]]
id = "IT-001"
name = "第一批"
goal = "打好地基"
target_branch = ""

[[iteration]]
id = "IT-002"
name = "第二批"
status = "执行中"
deps = ["IT-001"]

[[task]]
id = "T-001"
title = "第一批任务"
iteration = "IT-001"
write_scope = ["a/**"]
acceptance = ["a"]
verify = ["manual: a"]

[[task]]
id = "T-002"
title = "第二批任务"
iteration = "IT-002"
deps = ["T-001"]
write_scope = ["b/**"]
acceptance = ["b"]
verify = ["manual: b"]
`)
	if errs := Validate(f); len(errs) != 0 {
		t.Fatalf("合法迭代蓝图不应报错：%v", errs)
	}

	tasks := TasksForDB(f)
	if len(tasks) != 2 || tasks[0].IterationID != "IT-001" || tasks[1].IterationID != "IT-002" {
		t.Fatalf("任务归属未落入模型：%+v", tasks)
	}

	iterations := IterationsForDB(f)
	if len(iterations) != 2 {
		t.Fatalf("迭代数量不符：%+v", iterations)
	}
	first := iterations[0]
	if first.Status != db.IterationPlanning || first.TargetBranch != "main" {
		t.Fatalf("空状态应默认 planning，空目标应继承 main：%+v", first)
	}
	second := iterations[1]
	if second.Status != db.IterationRunning {
		t.Fatalf("中文状态标签应归一为 running：%+v", second)
	}
	if deps, ok := second.Extra["deps"].([]string); !ok || len(deps) != 1 || deps[0] != "IT-001" {
		t.Fatalf("迭代依赖应保留：%+v", second.Extra)
	}
}

func TestUnknownTaskIterationRejected(t *testing.T) {
	f := loadBlueprintText(t, `
[[iteration]]
id = "IT-001"
name = "唯一迭代"

[[task]]
id = "T-001"
title = "挂错批次"
iteration = "IT-999"
write_scope = ["a/**"]
acceptance = ["a"]
verify = ["manual: a"]
`)
	errs := Validate(f)
	found := false
	for _, e := range errs {
		if strings.Contains(e, "unknown iteration IT-999") {
			found = true
		}
	}
	if !found {
		t.Fatalf("未知 task.iteration 应被拒绝：%v", errs)
	}
}

func TestIterationDeclarationValidation(t *testing.T) {
	f := loadBlueprintText(t, `
[[iteration]]
id = "IT-001"
name = ""
status = "draft"
target_branch = "dev branch"
deps = ["IT-404"]

[[iteration]]
id = "IT-001"
name = "重复"

[[iteration]]
id = "IT-002"
name = "A"
deps = ["IT-003"]

[[iteration]]
id = "IT-003"
name = "B"
deps = ["IT-002"]

[[task]]
id = "T-001"
title = "任务"
iteration = "IT-001"
write_scope = ["a/**"]
acceptance = ["a"]
verify = ["manual: a"]
`)
	errs := Validate(f)
	want := []string{
		"duplicate iteration id IT-001",
		"IT-001: iteration name required",
		"IT-001: invalid iteration status draft",
		"IT-001: target_branch must not contain whitespace",
		"IT-001: unknown iteration dependency IT-404",
		"iteration dependency cycle:",
	}
	for _, w := range want {
		ok := false
		for _, e := range errs {
			if strings.Contains(e, w) {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("缺少错误 %q，实际 %v", w, errs)
		}
	}
}

func TestLegacyBlueprintWithoutIterationsStillValid(t *testing.T) {
	f := loadBlueprintText(t, `
[[task]]
id = "T-001"
title = "老蓝图"
write_scope = ["a/**"]
acceptance = ["a"]
verify = ["manual: a"]
`)
	if errs := Validate(f); len(errs) != 0 {
		t.Fatalf("没有迭代的老蓝图应继续合法：%v", errs)
	}
	if tasks := TasksForDB(f); len(tasks) != 1 || tasks[0].IterationID != "" {
		t.Fatalf("未声明迭代的任务应保持空归属：%+v", tasks)
	}
}

func TestIterationContractSamples(t *testing.T) {
	base := filepath.Join("..", "..", "docs", "contracts", "vnext", "iteration", "samples")
	valid, err := Load(filepath.Join(base, "valid", "01-two-iterations.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if errs := Validate(valid); len(errs) != 0 {
		t.Fatalf("契约正例应通过：%v", errs)
	}
	invalid, err := Load(filepath.Join(base, "invalid", "01-unknown-task-iteration.toml"))
	if err != nil {
		t.Fatal(err)
	}
	ok := false
	for _, e := range Validate(invalid) {
		if strings.Contains(e, "unknown iteration IT-999") {
			ok = true
		}
	}
	if !ok {
		t.Fatalf("契约反例应触发未知迭代错误：%v", Validate(invalid))
	}
}
