package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/blueprint"
)

const appendBaseBlueprint = `[meta]
name = "test"
version = 1
main_branch = "main"
# 既有注释必须原样保留

[[task]]
id = "OLD-001"
title = "旧题一"
priority = 10
merge_order = 10
deps = []
write_scope = ["a/**"]
acceptance = ["ok"]
verify = ["manual: ok"]

[[task]]
id = "OLD-002"
title = "旧题二"
priority = 20
merge_order = 20
deps = ["OLD-001"]
write_scope = ["b/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
`

const appendNewBlock = `[[task]]
id = "NEW-001"
title = "新题"
priority = 30
merge_order = 30
deps = []
write_scope = ["c/**"]
acceptance = ["ok"]
verify = ["manual: ok"]
`

func writeBlueprint(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "blueprint.toml")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// 追加成功：既有题保留、题数不减少、原内容是前缀、结果仍是合法蓝图。
func TestAppendTasksSuccess(t *testing.T) {
	p := writeBlueprint(t, appendBaseBlueprint)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := AppendTasks(p, []byte(appendNewBlock), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "NEW-001") || strings.TrimSpace(diff) == "" {
		t.Fatalf("diff 应含新题 id，得到 %q", diff)
	}
	after, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(after), string(before)) {
		t.Fatal("追加后原内容不再是前缀，文件疑似被截断/重写")
	}
	if got := countTasks(string(after)); got != countTasks(string(before))+1 {
		t.Fatalf("追加后题数应 +1，得到 %d", got)
	}
	for _, id := range []string{"OLD-001", "OLD-002"} {
		if !strings.Contains(string(after), `id = "`+id+`"`) {
			t.Fatalf("既有题 %s 丢失", id)
		}
	}
	if !strings.Contains(string(after), "# 既有注释必须原样保留") {
		t.Fatal("既有注释丢失")
	}
	if !strings.Contains(string(after), `id = "NEW-001"`) {
		t.Fatal("新题未写入")
	}
	if f, err := blueprint.Load(p); err != nil {
		t.Fatalf("追加后蓝图不是合法 TOML: %v", err)
	} else if errs := blueprint.Validate(f); len(errs) != 0 {
		t.Fatalf("追加后 plan check 不通过: %v", errs)
	}
}

// 重复 id 直接报错，且绝不写盘。
func TestAppendTasksRejectsDuplicateID(t *testing.T) {
	p := writeBlueprint(t, appendBaseBlueprint)
	before, _ := os.ReadFile(p)
	dup := strings.Replace(appendNewBlock, "NEW-001", "OLD-001", 1)
	if _, err := AppendTasks(p, []byte(dup), false); err == nil {
		t.Fatal("与既有 id 重复必须报错")
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(before) {
		t.Fatal("拒绝时不得改动文件")
	}

	// blocks 内部自身重复也要拒绝。
	twice := appendNewBlock + appendNewBlock
	if _, err := AppendTasks(p, []byte(twice), false); err == nil {
		t.Fatal("blocks 内重复 id 必须报错")
	}
}

// dryRun 只返回 diff，不写盘。
func TestAppendTasksDryRunDoesNotWrite(t *testing.T) {
	p := writeBlueprint(t, appendBaseBlueprint)
	before, _ := os.ReadFile(p)
	diff, err := AppendTasks(p, []byte(appendNewBlock), true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "dry-run") || !strings.Contains(diff, "NEW-001") {
		t.Fatalf("dry-run diff 内容不足: %q", diff)
	}
	after, _ := os.ReadFile(p)
	if string(after) != string(before) {
		t.Fatal("dryRun=true 不得写盘")
	}
	// 同目录不应残留临时文件。
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".maw-blueprint-") {
			t.Fatalf("dryRun 不应产生临时文件，残留 %s", e.Name())
		}
	}
}

// 空 blocks 报错且不写盘。
func TestAppendTasksRejectsEmptyBlocks(t *testing.T) {
	for name, blocks := range map[string][]byte{
		"nil":    nil,
		"空字节":    {},
		"纯空白":    []byte("  \n\n"),
		"只有注释":   []byte("# 只是注释\n"),
		"整份计划文件": []byte("[meta]\nname = \"x\"\n\n" + appendNewBlock),
	} {
		t.Run(name, func(t *testing.T) {
			p := writeBlueprint(t, appendBaseBlueprint)
			before, _ := os.ReadFile(p)
			if _, err := AppendTasks(p, blocks, false); err == nil {
				t.Fatal("非法 blocks 应报错")
			}
			after, _ := os.ReadFile(p)
			if string(after) != string(before) {
				t.Fatal("报错时不得改动文件")
			}
		})
	}
}

// 端到端：GenerateTaskBlocks 产出追加片段 → AppendTasks 追加 → 蓝图可 plan check。
func TestGenerateThenAppendKeepsExistingTasks(t *testing.T) {
	p := writeBlueprint(t, appendBaseBlueprint)
	blocks, ids, err := GenerateTaskBlocks(SplitRequest{
		Brief: "# 增量需求\n- 补一个能力\n- 再补一个能力\n",
		Lane:  "INC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("应生成 2 张卡，得到 %v", ids)
	}
	diff, err := AppendTasks(p, blocks, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "既有题数 2 → 4") {
		t.Fatalf("diff 应体现题数不减少，得到 %q", diff)
	}
	after, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(after), appendBaseBlueprint) {
		t.Fatal("既有题全集/注释被破坏")
	}
	for _, id := range append([]string{"OLD-001", "OLD-002"}, ids...) {
		if !strings.Contains(string(after), `id = "`+id+`"`) {
			t.Fatalf("题 %s 不在追加后的蓝图里", id)
		}
	}
	f, err := blueprint.Load(p)
	if err != nil {
		t.Fatalf("追加后蓝图非法: %v", err)
	}
	if len(f.Tasks) != 4 {
		t.Fatalf("应有 4 张卡，得到 %d", len(f.Tasks))
	}
	if errs := blueprint.Validate(f); len(errs) != 0 {
		t.Fatalf("追加后 plan check 不通过: %v", errs)
	}
}
