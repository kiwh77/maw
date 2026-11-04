package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/kiwh77/maw/internal/blueprint"
)

// decodeBlocks 确认生成的块是合法 TOML，并能被蓝图模型解析。
func decodeBlocks(t *testing.T, blocks []byte) *blueprint.File {
	t.Helper()
	var f blueprint.File
	if err := toml.Unmarshal(blocks, &f); err != nil {
		t.Fatalf("生成的块不是合法 TOML: %v\n%s", err, blocks)
	}
	return &f
}

// 分段（标题/列表/空行）、字段齐全、id 唯一带前缀、plan check 可过、不是独立计划文件。
func TestGenerateTaskBlocksSegmentsAndFields(t *testing.T) {
	brief := `# 目标一
- 实现登录页
- 修复登出跳转

目标二描述第一行
目标二描述第二行

* 补一条单测
`
	blocks, ids, err := GenerateTaskBlocks(SplitRequest{Brief: brief, Lane: "LEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 4 {
		t.Fatalf("应解析出 4 张卡，得到 %d (%v)", len(ids), ids)
	}
	f := decodeBlocks(t, blocks)
	if len(f.Tasks) != 4 {
		t.Fatalf("TOML 里应有 4 个 [[task]]，得到 %d", len(f.Tasks))
	}
	seen := map[string]bool{}
	for i, task := range f.Tasks {
		if !strings.HasPrefix(task.ID, "LEAD-") {
			t.Fatalf("%q 缺少 Lane 前缀", task.ID)
		}
		if task.ID != ids[i] {
			t.Fatalf("返回 id 与块内不一致: %q vs %q", ids[i], task.ID)
		}
		if seen[task.ID] {
			t.Fatalf("重复 id %q", task.ID)
		}
		seen[task.ID] = true
		if strings.TrimSpace(task.Title) == "" {
			t.Fatalf("%s 缺 title", task.ID)
		}
		if strings.TrimSpace(task.Goal) == "" {
			t.Fatalf("%s 缺 goal", task.ID)
		}
		if task.Priority == 0 || task.MergeOrder == 0 {
			t.Fatalf("%s 缺 priority/merge_order", task.ID)
		}
		if len(task.WriteScope) < 1 {
			t.Fatalf("%s write_scope 至少 1 条", task.ID)
		}
		if len(task.ReadScope) < 1 {
			t.Fatalf("%s 缺 read_scope", task.ID)
		}
		if len(task.Acceptance) < 1 {
			t.Fatalf("%s acceptance 至少 1 条", task.ID)
		}
		if len(task.Verify) < 1 {
			t.Fatalf("%s verify 至少 1 条", task.ID)
		}
		if len(task.NotDoing) < 1 {
			t.Fatalf("%s 缺 not_doing", task.ID)
		}
	}
	text := string(blocks)
	if !strings.Contains(text, "deps = []") {
		t.Fatal("deps 字段名必须是 deps（且为合法数组）")
	}
	if strings.Contains(text, "depends") {
		t.Fatal("不得写成 depends")
	}
	if errs := blueprint.Validate(f); len(errs) != 0 {
		t.Fatalf("生成的卡应通过 plan check 校验，得到 %v", errs)
	}
	// 输出是追加片段，绝不能是含 [meta] 的独立计划文件。
	for _, bad := range []string{"[meta]", "[project]", "[[iteration]]"} {
		if strings.Contains(text, bad) {
			t.Fatalf("输出含 %s，疑似独立计划文件", bad)
		}
	}
}

// 空输入返回明确错误、不 panic。
func TestGenerateTaskBlocksEmptyInput(t *testing.T) {
	for name, req := range map[string]SplitRequest{
		"全空":             {},
		"纯空白":            {Brief: "  \n\n  "},
		"只有空行":           {Brief: "\n\n"},
		"FromPath 文件不存在": {FromPath: filepath.Join(t.TempDir(), "missing.md")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := GenerateTaskBlocks(req); err == nil {
				t.Fatal("空输入应返回明确错误")
			}
		})
	}
	// 需求文件存在但只有空白，也应报错。
	p := filepath.Join(t.TempDir(), "blank.md")
	if err := os.WriteFile(p, []byte("  \n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GenerateTaskBlocks(SplitRequest{FromPath: p}); err == nil {
		t.Fatal("空白需求文件应报错")
	}
}

// FromPath 读文件；Brief 优先于 FromPath。
func TestGenerateTaskBlocksFromPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(p, []byte("# 需求\n- A\n- B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	blocks, ids, err := GenerateTaskBlocks(SplitRequest{FromPath: p, Lane: "DOC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("FromPath 应解析出 2 张卡，得到 %d", len(ids))
	}
	if !strings.HasPrefix(ids[0], "DOC-") {
		t.Fatalf("id 应带 Lane 前缀，得到 %q", ids[0])
	}
	if !strings.Contains(string(blocks), "# 来源：") {
		t.Fatal("生成块应保留来源审计注释")
	}
	if !strings.Contains(string(blocks), p) {
		t.Fatal("来源注释应含 FromPath")
	}
	// Brief 非空时以 Brief 为准。
	_, ids2, err := GenerateTaskBlocks(SplitRequest{Brief: "只有一段", FromPath: p, Lane: "X"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids2) != 1 {
		t.Fatalf("Brief 优先，应只有 1 张卡，得到 %d", len(ids2))
	}
}

// 显式 IDs：按顺序使用，不足自动补，重复/空直接报错。
func TestGenerateTaskBlocksExplicitIDs(t *testing.T) {
	blocks, ids, err := GenerateTaskBlocks(SplitRequest{
		Brief: "第一段\n\n第二段\n\n第三段",
		Lane:  "LEAD",
		IDs:   []string{"CUSTOM-1", "CUSTOM-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"CUSTOM-1", "CUSTOM-2", "LEAD-001"}
	if len(ids) != len(want) {
		t.Fatalf("ids=%v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids[%d]=%q want %q", i, ids[i], want[i])
		}
	}
	f := decodeBlocks(t, blocks)
	for i, task := range f.Tasks {
		if task.ID != want[i] {
			t.Fatalf("块内 id[%d]=%q want %q", i, task.ID, want[i])
		}
	}

	for _, bad := range [][]string{{"X", "X"}, {"", "X"}} {
		if _, _, err := GenerateTaskBlocks(SplitRequest{Brief: "A\n\nB", IDs: bad}); err == nil {
			t.Fatalf("非法 IDs %v 应报错", bad)
		}
	}
}

// 需求文本含引号/反斜杠/制表符等，生成结果仍是合法 TOML。
func TestGenerateTaskBlocksEscapesTOML(t *testing.T) {
	brief := "修复 \"引号\" 与反斜杠 \\ 以及制表\t符\n\n包含 [meta] 字样的标题\n"
	blocks, ids, err := GenerateTaskBlocks(SplitRequest{Brief: brief, Lane: "ESC"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("应生成 2 张卡，得到 %d", len(ids))
	}
	f := decodeBlocks(t, blocks) // 转义不对会在这里失败
	if len(f.Tasks) != 2 {
		t.Fatalf("TOML 应有 2 张卡，得到 %d", len(f.Tasks))
	}
	if !strings.Contains(f.Tasks[0].Title, `"引号"`) {
		t.Fatalf("引号未正确还原: %q", f.Tasks[0].Title)
	}
	if !strings.Contains(f.Tasks[1].Title, "[meta]") {
		t.Fatalf("标题里的方括号文本不应被吞掉: %q", f.Tasks[1].Title)
	}
}

// 纯标题段（含空标题）也容错产出占位卡，不 panic。
func TestGenerateTaskBlocksHeadingOnly(t *testing.T) {
	blocks, ids, err := GenerateTaskBlocks(SplitRequest{Brief: "###\n"})
	if err != nil {
		t.Fatalf("纯标题不应报错: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("应产出 1 张占位卡，得到 %d", len(ids))
	}
	f := decodeBlocks(t, blocks)
	if strings.TrimSpace(f.Tasks[0].Title) == "" {
		t.Fatal("空标题应回退成占位标题")
	}
}
