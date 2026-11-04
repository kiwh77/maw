package app

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// SplitRequest 是一次模板化拆卡的输入。
//
// lead split 不依赖 LLM：把需求文本按空行 / # 标题 / 列表项切成若干段，
// 每段生成一张 [[task]] 草稿卡。输出是「追加片段」，不是完整计划文件。
type SplitRequest struct {
	// Brief 是需求文本；为空时尝试从 FromPath 读取。
	Brief string
	// FromPath 是需求文档路径（Brief 为空时使用），仅用于生成审计注释。
	FromPath string
	// Lane 用作自动生成 id 的前缀；为空时用 "LD"。
	Lane string
	// IDs 可选：按顺序指定前若干张卡的 id；不足的卡自动补 <Lane>-NNN，多余的忽略。
	// 显式 id 必须非空且互不重复。
	IDs []string
}

// briefSection 是需求文本切出的一段，对应一张卡。
type briefSection struct {
	title     string
	lines     []string // 标题之外的正文，落到 goal
	fromTitle bool     // 是否由 # 标题行生成
}

var numberedBulletRE = regexp.MustCompile(`^\d+[.)]\s+`)

// GenerateTaskBlocks 把需求文本解析成若干 [[task]] TOML 块。
//
// 关键约束（历史事故防线）：
//   - 输出只含 [[task]] 块与注释，绝不含 [meta] / [project] / [iteration]，
//     因此不能当独立计划文件拿去 plan apply（那样会退役蓝图外的 idle 题）；
//     必须交给 AppendTasks 追加到蓝图末尾。
//   - 每块带齐 id/title/goal/priority/merge_order/deps/write_scope/
//     read_scope/acceptance/verify/not_doing；deps 字段名固定为 deps（不是 depends）。
//   - write_scope / verify / acceptance 至少各 1 条，便于 plan check 通过。
//   - 自动生成的 id 唯一且带 Lane 前缀；解析全程容错，空输入返回明确错误、不 panic。
//
// 返回 (块字节, 实际生成的 id 列表, error)。占位字段（TODO）是模板草稿，
// 需人工在「生成 → 校验 → 确认」流程里补齐。
func GenerateTaskBlocks(req SplitRequest) ([]byte, []string, error) {
	text, err := splitBriefText(req)
	if err != nil {
		return nil, nil, err
	}
	sections := mergeHeadingPrefix(splitBrief(text))
	if len(sections) == 0 {
		return nil, nil, fmt.Errorf("未能从需求文本解析出任何任务段：请检查 Brief / FromPath 内容")
	}

	ids, err := resolveSplitIDs(req, len(sections))
	if err != nil {
		return nil, nil, err
	}

	var b strings.Builder
	b.WriteString("# maw lead split 生成的追加片段：仅含任务卡块，必须追加到蓝图末尾，\n")
	b.WriteString("# 不可作为独立计划文件用于 plan apply（会把蓝图外的 idle 题标成 removed_from_plan）。\n")
	if p := strings.TrimSpace(req.FromPath); p != "" {
		b.WriteString("# 来源：" + strings.NewReplacer("\n", " ", "\r", " ").Replace(p) + "\n")
	}
	b.WriteString("\n")

	for i, sec := range sections {
		id := ids[i]
		title := strings.TrimSpace(sec.title)
		if title == "" {
			title = "lead 拆分任务 " + id
		}
		goal := strings.Join(sec.lines, "\n")
		if strings.TrimSpace(goal) == "" {
			goal = "由 maw lead split 模板生成，待补充现状与目标。"
		}
		order := 1000 + i

		b.WriteString("[[task]]\n")
		b.WriteString("id = " + quoteTOML(id) + "\n")
		b.WriteString("title = " + quoteTOML(title) + "\n")
		b.WriteString("goal = " + quoteTOML(goal) + "\n")
		b.WriteString(fmt.Sprintf("priority = %d\n", order))
		b.WriteString(fmt.Sprintf("merge_order = %d\n", order))
		b.WriteString("deps = []\n")
		// 模板无法推断真实独占写盘范围，给出显式占位，交由人工补齐。
		b.WriteString("write_scope = " + arrayTOML([]string{"TODO: 填写本卡独占写入范围"}) + "\n")
		b.WriteString("read_scope = " + arrayTOML([]string{"docs/ARCHITECTURE_VNEXT.zh-CN.md"}) + "\n")
		b.WriteString("acceptance = " + arrayTOML([]string{"TODO: 填写可核对的验收条件"}) + "\n")
		b.WriteString("verify = " + arrayTOML([]string{"manual: 人工核对验收条件与写盘范围"}) + "\n")
		b.WriteString("not_doing = " + arrayTOML([]string{"不改动本卡 write_scope 之外的文件"}) + "\n")
		b.WriteString("\n")
	}
	return []byte(b.String()), ids, nil
}

// splitBriefText 取需求文本：优先 Brief，其次 FromPath 文件。
func splitBriefText(req SplitRequest) (string, error) {
	if strings.TrimSpace(req.Brief) != "" {
		return req.Brief, nil
	}
	if strings.TrimSpace(req.FromPath) != "" {
		b, err := os.ReadFile(req.FromPath)
		if err != nil {
			return "", fmt.Errorf("读取需求文件 %q 失败：%w", req.FromPath, err)
		}
		return string(b), nil
	}
	return "", fmt.Errorf("需求文本为空：请提供 Brief 或 FromPath")
}

// splitBrief 按空行 / # 标题 / 列表项把文本切成若干段。
//
// 规则（逐行扫描）：
//   - 空行：结束当前段；
//   - # 标题行：结束当前段并另起一段，段标题为去掉 # 后的文本；
//   - - / * / + / 数字. 列表项：结束当前段并独立成段，段标题为条目文本；
//   - 其余行：并入当前段，首行作标题、后续行作正文。
func splitBrief(brief string) []briefSection {
	brief = strings.ReplaceAll(brief, "\r\n", "\n")
	var out []briefSection
	var cur briefSection
	has := false
	flush := func() {
		if has {
			out = append(out, cur)
		}
		cur = briefSection{}
		has = false
	}
	for _, raw := range strings.Split(brief, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flush()
		case isHeadingLine(line):
			flush()
			cur = briefSection{title: strings.TrimSpace(strings.TrimLeft(line, "#")), fromTitle: true}
			has = true
		case isBulletLine(line):
			flush()
			cur = briefSection{title: stripBullet(line)}
			has = true
		default:
			if !has {
				cur = briefSection{title: line}
				has = true
			} else {
				cur.lines = append(cur.lines, line)
			}
		}
	}
	flush()
	return out
}

// mergeHeadingPrefix：纯标题段（无正文）不单独成卡，把标题作为下一段标题的前缀，
// 避免「## 模块 / - A / - B」产出一张空洞卡；末尾孤立标题仍保留成卡。
func mergeHeadingPrefix(in []briefSection) []briefSection {
	out := make([]briefSection, 0, len(in))
	pending := ""
	pendingSet := false
	for _, s := range in {
		if s.fromTitle && len(s.lines) == 0 {
			if pendingSet {
				out = append(out, briefSection{title: pending, fromTitle: true})
			}
			pending = strings.TrimSpace(s.title)
			pendingSet = true
			continue
		}
		if pendingSet {
			if pending != "" {
				if strings.TrimSpace(s.title) == "" {
					s.title = pending
				} else {
					s.title = pending + " / " + s.title
				}
			}
			pending = ""
			pendingSet = false
		}
		out = append(out, s)
	}
	if pendingSet {
		out = append(out, briefSection{title: pending, fromTitle: true})
	}
	return out
}

func isHeadingLine(line string) bool {
	return strings.HasPrefix(line, "#")
}

func isBulletLine(line string) bool {
	return strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") ||
		strings.HasPrefix(line, "+ ") || numberedBulletRE.MatchString(line)
}

func stripBullet(line string) string {
	switch {
	case strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "+ "):
		return strings.TrimSpace(line[2:])
	case numberedBulletRE.MatchString(line):
		return strings.TrimSpace(numberedBulletRE.ReplaceAllString(line, ""))
	}
	return line
}

// resolveSplitIDs 生成 n 个唯一 id：显式 IDs 优先，其余按 <Lane>-NNN 补齐。
func resolveSplitIDs(req SplitRequest, n int) ([]string, error) {
	ids := make([]string, n)
	seen := map[string]bool{}
	used := map[string]bool{}
	for i, raw := range req.IDs {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, fmt.Errorf("IDs[%d] 为空：显式 id 不能是空字符串", i)
		}
		if seen[id] {
			return nil, fmt.Errorf("IDs 含重复 id %q", id)
		}
		seen[id] = true
		if i < n {
			used[id] = true
			ids[i] = id
		}
	}
	prefix := leadPrefix(req.Lane)
	seq := 1
	for i := range ids {
		if ids[i] != "" {
			continue
		}
		for {
			cand := fmt.Sprintf("%s-%03d", prefix, seq)
			seq++
			if !used[cand] {
				ids[i] = cand
				used[cand] = true
				break
			}
		}
	}
	return ids, nil
}

// leadPrefix 把 Lane 压成大写字母数字前缀；为空时回退 "LD"。
func leadPrefix(lane string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(lane)) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "LD"
	}
	return b.String()
}

// quoteTOML 把任意文本转成 TOML 基本字符串，避免需求文本里的引号/换行破坏格式。
func quoteTOML(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func arrayTOML(ss []string) string {
	parts := make([]string, 0, len(ss))
	for _, s := range ss {
		parts = append(parts, quoteTOML(s))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
