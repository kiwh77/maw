package app

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// blueprintIDRE 匹配蓝图里独占一行的 id = "..."（既覆盖 [[task]] 也覆盖 [[iteration]]）。
var blueprintIDRE = regexp.MustCompile(`(?m)^[ \t]*id[ \t]*=[ \t]*"([^"]+)"[ \t]*$`)

// blueprintTaskRE 只数独占一行的 [[task]] 表头，避免把字符串里的同名文本算进去。
var blueprintTaskRE = regexp.MustCompile(`(?m)^[ \t]*\[\[task\]\][ \t]*$`)

// appendPlan 是「原内容 + 片段」的候选全文与配套信息。
//
// 校验与写盘共用同一份候选内容：先 planAppend 得到全文，调用方在校验通过后
// 才落盘，避免「写进去了但内容不合法」的中间态。
type appendPlan struct {
	content    []byte
	fragment   string
	newIDs     []string
	addedTasks int
}

// MergeForAppend 返回「原内容 + 片段」的候选全文，不写盘。
//
// 只用于增长场景：片段必须只含 [[task]]，新 id 不得与既有 id 重复，
// 结果必须以原内容为前缀（绝不截断），任务块数必须等于既有 + 新增。
func MergeForAppend(orig, blocks []byte) ([]byte, error) {
	p, err := planAppend(orig, blocks)
	if err != nil {
		return nil, err
	}
	return p.content, nil
}

// planAppend 是 MergeForAppend 与 AppendTasks 的共同实现。
func planAppend(orig, blocks []byte) (*appendPlan, error) {
	blockText := string(blocks)
	if strings.TrimSpace(blockText) == "" {
		return nil, fmt.Errorf("blocks 为空：没有可追加的任务块")
	}
	if hasNonTaskTable(blockText) {
		return nil, fmt.Errorf("blocks 含 [[task]] 之外的表头：只允许追加任务块，不能把整份计划文件塞进蓝图")
	}
	addedTasks := countTasks(blockText)
	if addedTasks == 0 {
		return nil, fmt.Errorf("blocks 不含任何 [[task]] 块，拒绝追加")
	}
	newIDs, err := extractBlockIDs(blockText)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, m := range blueprintIDRE.FindAllStringSubmatch(string(orig), -1) {
		existing[m[1]] = true
	}
	seen := map[string]bool{}
	for _, id := range newIDs {
		if seen[id] {
			return nil, fmt.Errorf("blocks 内出现重复 id %q，拒绝追加", id)
		}
		seen[id] = true
		if existing[id] {
			return nil, fmt.Errorf("新题 id %q 与蓝图既有 id 重复，拒绝追加", id)
		}
	}

	// 只在末尾追加：base 至多补一个换行，原文件内容始终是 newContent 的前缀。
	base := string(orig)
	if !strings.HasSuffix(base, "\n") {
		base += "\n"
	}
	add := blockText
	if !strings.HasSuffix(add, "\n") {
		add += "\n"
	}
	newContent := base + "\n" + add

	oldTasks := countTasks(base)
	if !strings.HasPrefix(newContent, string(orig)) {
		return nil, fmt.Errorf("内部校验失败：追加后原内容不再是前缀，疑似被截断")
	}
	if got := countTasks(newContent); got != oldTasks+addedTasks {
		return nil, fmt.Errorf("内部校验失败：追加后任务块数 %d != 既有 %d + 新增 %d", got, oldTasks, addedTasks)
	}
	return &appendPlan{content: []byte(newContent), fragment: add, newIDs: newIDs, addedTasks: addedTasks}, nil
}

// AppendTasks 把 blocks 追加到蓝图文件末尾，返回可读 diff。
//
// 事故防线（STATUS.md 记过：单题临时 toml 做增量 plan apply 把 30 道 idle 题
// 标成 removed_from_plan），这里强制：
//   - 读取原文件全部内容，只在末尾追加，绝不截断、绝不重写；
//   - 新 id 与既有 ^id = "..." 不重复，重复直接报错；
//   - 追加后 [[task]] 数 = 既有 + 新增，绝不少于既有；
//   - 拒绝含 [meta] / [project] / [[iteration]] 等表头的「整份计划文件」；
//   - dryRun=true 只返回 diff，不写盘；
//   - 写盘用同目录临时文件 + rename（原子），并保留原文件权限。
func AppendTasks(blueprintPath string, blocks []byte, dryRun bool) (diff string, err error) {
	orig, err := os.ReadFile(blueprintPath)
	if err != nil {
		return "", fmt.Errorf("读取蓝图 %q 失败：%w", blueprintPath, err)
	}
	plan, err := planAppend(orig, blocks)
	if err != nil {
		return "", err
	}
	diff = renderAppendDiff(blueprintPath, dryRun, countTasks(string(orig)), plan.addedTasks, plan.newIDs, plan.fragment)
	if dryRun {
		return diff, nil
	}
	if err := writeBytesAtomic(blueprintPath, plan.content); err != nil {
		return "", err
	}
	return diff, nil
}

// extractBlockIDs 取出 blocks 里的新题 id，并保证唯一。
func extractBlockIDs(blockText string) ([]string, error) {
	matches := blueprintIDRE.FindAllStringSubmatch(blockText, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("blocks 里找不到 id = \"...\" 行，无法校验重复")
	}
	ids := make([]string, 0, len(matches))
	seen := map[string]bool{}
	for _, m := range matches {
		id := m[1]
		if seen[id] {
			return nil, fmt.Errorf("blocks 内出现重复 id %q，拒绝追加", id)
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

// hasNonTaskTable 判断 blocks 是否含 [[task]] 之外的 TOML 表头。
// 数组元素行（如 write_scope = ["..."]）不以 [ 开头，不会被误判。
func hasNonTaskTable(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") || t == "[[task]]" {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			return true
		}
	}
	return false
}

func countTasks(s string) int {
	return len(blueprintTaskRE.FindAllStringIndex(s, -1))
}

func renderAppendDiff(path string, dryRun bool, oldTasks, addedTasks int, newIDs []string, add string) string {
	mode := "写盘"
	if dryRun {
		mode = "dry-run（不写盘）"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "蓝图追加 %s：%s\n", mode, path)
	fmt.Fprintf(&b, "既有题数 %d → %d（新增 %d，绝不减少）\n", oldTasks, oldTasks+addedTasks, addedTasks)
	b.WriteString("新题 id：" + strings.Join(newIDs, ", ") + "\n")
	b.WriteString("--- 追加内容 ---\n")
	b.WriteString(add)
	return b.String()
}

// writeBytesAtomic 先写同目录临时文件再 rename，避免读到半截蓝图。
func writeBytesAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".maw-blueprint-*")
	if err != nil {
		return fmt.Errorf("创建蓝图临时文件失败：%w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入蓝图临时文件失败：%w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("设置蓝图文件权限失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭蓝图临时文件失败：%w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("重命名蓝图临时文件失败：%w", err)
	}
	return nil
}
