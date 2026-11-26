package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/kiwh77/maw/internal/i18n"
	"github.com/kiwh77/maw/internal/version"
)

var blockRE = regexp.MustCompile(`(?s)<!--\s*maw:begin\s+v(\d+)\s*-->.*?<!--\s*maw:end\s*-->`)

// Segment returns the protocol block for the active locale (MAW_LANG / LANG).
func Segment() string {
	return SegmentLang(i18n.Resolve())
}

// SegmentLang returns the protocol block for an explicit language.
func SegmentLang(lang i18n.Lang) string {
	begin := "<!-- maw:begin v" + strconv.Itoa(version.ProtocolVersion) + " -->"
	var body string
	if lang == i18n.Zh {
		body = segmentZh()
	} else {
		body = segmentEn()
	}
	return begin + "\n" + body + "<!-- maw:end -->\n"
}

func segmentZh() string {
	return `## 多 Agent 协议（由 maw 管理，请勿手改）
1. 状态库在本机全局 ` + "`~/.maw/maw.db`" + `；本目录 ` + "`.multiagent/`" + ` 仅存配置与蓝图。多工程共用同一看板。
2. 先设定身份并注册：Worker 名统一为 ` + "`{应用}-{工程}-{序号}`" + `（工程名去连字符小写，例 ` + "`cursor-demo-1`" + `）。` + "`export MAW_AGENT=<名>`" + ` 后 ` + "`maw register`" + `；未指定时 ` + "`maw register`" + ` / ` + "`maw next --claim`" + ` 会按当前应用自动分配。应用可用 ` + "`MAW_APP`" + ` 覆盖。
3. 先 ` + "`maw next`" + ` 查询下一题：默认只预览候选，不改任务状态、不建 Run、不写 worktree；确认后再 ` + "`maw next --claim`" + ` 领取，之后只在该 Agent 的持久 worktree（` + "`.worktrees/{agent}`" + `）中工作；maw 会记录你正在执行哪道题。人工领取仍然是一等路径：领取上下文须保留实例 ID、定义版本与来源（` + "`manual_next`" + `），不得省略。
4. 只修改任务卡 write_scope 内的文件；需要越界时运行 ` + "`maw request-change`" + `。
5. 里程碑运行 ` + "`maw progress`" + `；非编排链路的进度可用 ` + "`maw status report`" + ` 注入本机中枢。完成运行 ` + "`maw submit`" + `，无法继续运行 ` + "`maw block`" + `。
6. 换人或中断前运行 ` + "`maw handoff`" + `；本会话无更多可领任务时再 ` + "`maw unregister`" + `。
7. 完成 submit 后可自行执行 ` + "`maw merge --yes`" + ` 合入；合入成功删除 Agent worktree 与题分支；禁止直接用 git merge/rebase/push 主干。
8. 合入完成后若仍有 ready 任务，继续 ` + "`maw next`" + ` 查询、` + "`maw next --claim`" + ` 领取（重建 ` + "`.worktrees/{agent}`" + ` 到主干 tip 后换新题分支）循环直至无题可领或用户叫停。
9. 全局看板：` + "`maw board --serve`" + `，可按工程/Agent/状态筛选；历史残留可用 ` + "`maw prune`" + ` 清理（仅保留 working / 进行中题占用）。看板为观测面，不自动派发。
10. 出错时先读 maw 输出里的「下一步」提示，不要绕过。
11. 本机 CLI／会话令牌只允许出现在 Authorization 头或受权文件（权限 0600）；禁止写入提示词、仓库文件、蓝图、URL、日志或看板事件。
12. 旁路（` + "`maw status report`" + `／观测客户端）只能上报自己的观察与心跳，不得把题状态改成 claimed／in_progress；真正领题仍须 ` + "`maw next --claim`" + `。
13. 协议段由 ` + "`maw init`" + `／` + "`maw init --upgrade`" + ` 维护；版本落后时先升级，勿手改 ` + "`maw:begin`" + ` 块。
`
}

func segmentEn() string {
	return `## Multi-agent protocol (managed by maw; do not edit by hand)
1. Global state lives in ` + "`~/.maw/maw.db`" + `; this repo's ` + "`.multiagent/`" + ` holds config and blueprints only. One board covers all projects.
2. Set identity and register: worker names are ` + "`{app}-{project}-{n}`" + ` (project slug lowercased without hyphens, e.g. ` + "`cursor-demo-1`" + `). ` + "`export MAW_AGENT=<name>`" + ` then ` + "`maw register`" + `; if unset, ` + "`maw register`" + ` / ` + "`maw next --claim`" + ` auto-assign for the current app. Override the app with ` + "`MAW_APP`" + `.
3. Query first with ` + "`maw next`" + ` — by default it only previews the next candidate: no state change, no Run, no worktree write. Claim explicitly with ` + "`maw next --claim`" + `, then work only in that agent's persistent worktree (` + "`.worktrees/{agent}`" + `). maw records which task you hold. Manual claim remains first-class: claim context must keep instance id, definition version, and source (` + "`manual_next`" + `).
4. Edit only files inside the task ` + "`write_scope`" + `; to go outside it run ` + "`maw request-change`" + `.
5. Milestones: ` + "`maw progress`" + `. Non-orchestrated progress can use ` + "`maw status report`" + ` into the local hub. Finish with ` + "`maw submit`" + `; if stuck run ` + "`maw block`" + `.
6. Before handoff or interrupt run ` + "`maw handoff`" + `; when this session has no more claimable tasks run ` + "`maw unregister`" + `.
7. After submit you may ` + "`maw merge --yes`" + `; a successful merge deletes the agent worktree and the task branch. Do not git merge/rebase/push trunk yourself.
8. After merge, if ready tasks remain, keep querying with ` + "`maw next`" + ` and claiming with ` + "`maw next --claim`" + ` (recreate ` + "`.worktrees/{agent}`" + ` at trunk tip, new task branch) until none left or the user stops you.
9. Global board: ` + "`maw board --serve`" + ` with project/Agent/status filters; prune leftovers with ` + "`maw prune`" + ` (keeps only working / in-progress task holds). The board is observation-only; it does not auto-dispatch.
10. On errors, read maw's Next hint first; do not bypass it.
11. Local CLI/session tokens may appear only in the Authorization header or a 0600 auth file — never in prompts, repo files, blueprints, URLs, logs, or board events.
12. Bypass clients (` + "`maw status report`" + ` / observers) may publish their own observations and heartbeats only; they must not flip a task to claimed/in_progress. Real claims still require ` + "`maw next --claim`" + `.
13. Protocol blocks are maintained by ` + "`maw init`" + ` / ` + "`maw init --upgrade`" + `; if the file version lags, upgrade first — do not hand-edit the ` + "`maw:begin`" + ` block.
`
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

// UpsertWithFrontmatter writes a rule file with YAML frontmatter (Cursor/Qoder/Trae, …).
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
		rel = ".codebuddy/rules/maw/RULE.mdc"
		fm := "---\ndescription: maw multi-agent protocol\nalwaysApply: true\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
		if err == nil {
			_, _ = UpsertWithFrontmatter(filepath.Join(repo, ".codebuddy/rules/maw.md"), fm)
		}
	case "claude":
		rel = "CLAUDE.md"
		status, err = Upsert(filepath.Join(repo, rel))
	case "windsurf":
		rel = ".windsurf/rules/maw.md"
		fm := "---\ntrigger: always_on\nalwaysApply: true\n---\n"
		status, err = UpsertWithFrontmatter(filepath.Join(repo, rel), fm)
	default:
		return "", "", nil
	}
	return rel, status, err
}

// InstallResult is the outcome of protocol installation on init.
type InstallResult struct {
	Files    map[string]string `json:"files"`
	Detected map[string]string `json:"detected"`
	Fallback string            `json:"fallback"`
}

// InstallFiles detects agent tools and writes rules; AGENTS.md is always the fallback.
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
