package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/version"
)

var blockRE = regexp.MustCompile(`(?s)<!--\s*maw:begin\s+v(\d+)\s*-->.*?<!--\s*maw:end\s*-->`)

func Segment() string {
	begin := "<!-- maw:begin v" + strconv.Itoa(version.ProtocolVersion) + " -->"
	body := `## 多 Agent 协议（由 maw 管理，请勿手改）
1. 状态库在本机全局 ` + "`~/.maw/maw.db`" + `；本目录 ` + "`.multiagent/`" + ` 仅存配置与蓝图。多工程共用同一看板。
2. 先设定身份并注册：Worker 名统一为 ` + "`{应用}-{工程}-{序号}`" + `（工程名去连字符小写，例 ` + "`cursor-demo-1`" + `）。` + "`export MAW_AGENT=<名>`" + ` 后 ` + "`maw register`" + `；未指定时 ` + "`maw register`" + ` / ` + "`maw next`" + ` 会按当前应用自动分配。应用可用 ` + "`MAW_APP`" + ` 覆盖。
3. 运行 ` + "`maw next`" + ` 领取任务，只在该 Agent 的持久 worktree（` + "`.worktrees/{agent}`" + `）中工作；maw 会记录你正在执行哪道题。
4. 只修改任务卡 write_scope 内的文件；需要越界时运行 ` + "`maw request-change`" + `。
5. 里程碑运行 ` + "`maw progress`" + `，完成运行 ` + "`maw submit`" + `，无法继续运行 ` + "`maw block`" + `。
6. 换人或中断前运行 ` + "`maw handoff`" + `；本会话无更多可领任务时再 ` + "`maw unregister`" + `。
7. 完成 submit 后可自行执行 ` + "`maw merge --yes`" + ` 合入；合入成功保留 Agent worktree、仅删除题分支；禁止直接用 git merge/rebase/push 主干。
8. 合入完成后若仍有 ready 任务，继续 ` + "`maw next`" + `（同树 reset 到主干 tip 后换新题分支）循环直至无题可领或用户叫停。
9. 全局看板：` + "`maw board --serve`" + `，可按工程/Agent/状态筛选；残留可用 ` + "`maw prune`" + ` 清理。
10. 出错时先读 maw 输出里的「下一步」提示，不要绕过。
`
	return begin + "\n" + body + "<!-- maw:end -->\n"
}

func Upsert(path string) (string, error) {
	seg := Segment()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "created", os.WriteFile(path, []byte(seg), 0o644)
		}
		return "", err
	}
	text := string(b)
	if !blockRE.MatchString(text) {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return "updated", os.WriteFile(path, []byte(text+"\n"+seg), 0o644)
	}
	newText := blockRE.ReplaceAllString(text, strings.TrimRight(seg, "\n"))
	if !strings.HasSuffix(newText, "\n") {
		newText += "\n"
	}
	if newText == text {
		return "unchanged", nil
	}
	return "updated", os.WriteFile(path, []byte(newText), 0o644)
}

// UpsertWithFrontmatter 写入带 YAML frontmatter 的规则文件（Cursor/Qoder/Trae 等）
func UpsertWithFrontmatter(path, frontmatter string) (string, error) {
	body := Segment()
	content := frontmatter + "\n" + body
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "created", os.WriteFile(path, []byte(content), 0o644)
		}
		return "", err
	}
	text := string(b)
	if !blockRE.MatchString(text) {
		// 保留原 frontmatter/正文，追加协议段
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return "updated", os.WriteFile(path, []byte(text+"\n"+body), 0o644)
	}
	newText := blockRE.ReplaceAllString(text, strings.TrimRight(body, "\n"))
	if !strings.HasSuffix(newText, "\n") {
		newText += "\n"
	}
	if newText == text {
		return "unchanged", nil
	}
	return "updated", os.WriteFile(path, []byte(newText), 0o644)
}

func ReadVersion(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	m := blockRE.FindStringSubmatch(string(b))
	if m == nil {
		return 0, false
	}
	v, _ := strconv.Atoi(m[1])
	return v, true
}

func installToolRule(repo string, d Detected) (rel string, status string, err error) {
	seg := Segment()
	switch d.ID {
	case "cursor":
		rel = ".cursor/rules/maw.mdc"
		fm := "---\ndescription: maw multi-agent protocol\nglobs:\nalwaysApply: true\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
	case "qoder":
		rel = ".qoder/rules/maw.md"
		fm := "---\ntrigger: always_on\nalwaysApply: true\ndescription: maw multi-agent protocol\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
	case "trae":
		rel = ".trae/rules/maw.md"
		fm := "---\nalwaysApply: true\ndescription: maw multi-agent protocol\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
	case "codebuddy":
		// IDE 侧常见 RULE.mdc 目录结构；CLI 也读 .md
		rel = ".codebuddy/rules/maw/RULE.mdc"
		fm := "---\ndescription: maw multi-agent protocol\nalwaysApply: true\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
		if err == nil {
			// 同步一份纯 md，兼容 CLI 记忆加载
			_, _ = UpsertWithFrontmatter(filepath.Join(repo, ".codebuddy/rules/maw.md"), fm)
		}
	case "claude":
		rel = "CLAUDE.md"
		status, err = Upsert(filepath.Join(repo, rel))
	case "windsurf":
		rel = ".windsurf/rules/maw.md"
		fm := "---\ntrigger: always_on\nalwaysApply: true\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
		_ = seg
	default:
		return "", "", nil
	}
	return rel, status, err
}

// InstallResult init 协议安装结果
type InstallResult struct {
	Files    map[string]string `json:"files"`
	Detected map[string]string `json:"detected"` // tool -> reasons
	Fallback string            `json:"fallback"` // 始终写入的兜底
}

// InstallFiles 检测工程/本机 Agent，写入对应规则；AGENTS.md 始终作为兜底。
func InstallFiles(repo string) (InstallResult, error) {
	out := InstallResult{
		Files:    map[string]string{},
		Detected: map[string]string{},
		Fallback: "AGENTS.md",
	}
	var err error
	out.Files["AGENTS.md"], err = Upsert(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		return out, err
	}

	detected := DetectTools(repo)
	out.Detected = DetectSummary(detected)
	for _, d := range detected {
		rel, status, e := installToolRule(repo, d)
		if e != nil {
			return out, e
		}
		if rel != "" {
			out.Files[rel] = status
		}
	}
	return out, nil
}
