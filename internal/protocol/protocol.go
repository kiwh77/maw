package protocol

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/wuhao/goal-agents/internal/version"
)

var blockRE = regexp.MustCompile(`(?s)<!--\s*maw:begin\s+v(\d+)\s*-->.*?<!--\s*maw:end\s*-->`)

func Segment() string {
	begin := "<!-- maw:begin v" + strconv.Itoa(version.ProtocolVersion) + " -->"
	body := `## 多 Agent 协议（由 maw 管理，请勿手改）
1. 开始工作前运行 ` + "`maw next`" + ` 领取任务，只在它创建的 worktree 中工作。
2. 只修改任务卡 write_scope 内的文件；需要越界时运行 ` + "`maw request-change`" + `。
3. 里程碑运行 ` + "`maw progress`" + `，完成运行 ` + "`maw submit`" + `，无法继续运行 ` + "`maw block`" + `。
4. 换人或中断前运行 ` + "`maw handoff`" + `，交接包必须自包含。
5. 禁止自行 merge、rebase 主干或 push 主干，合并只由 ` + "`maw merge`" + ` 执行。
6. 出错时先读 maw 输出里的 Next 提示，不要绕过。
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

func InstallFiles(repo string) (map[string]string, error) {
	out := map[string]string{}
	var err error
	out["AGENTS.md"], err = Upsert(filepath.Join(repo, "AGENTS.md"))
	if err != nil {
		return out, err
	}
	out["CLAUDE.md"], err = Upsert(filepath.Join(repo, "CLAUDE.md"))
	if err != nil {
		return out, err
	}
	cursor := filepath.Join(repo, ".cursor", "rules", "maw.mdc")
	if _, err := os.Stat(cursor); os.IsNotExist(err) {
		_ = os.MkdirAll(filepath.Dir(cursor), 0o755)
		content := "---\ndescription: maw multi-agent protocol\nglobs:\nalwaysApply: true\n---\n\n" + Segment()
		if err := os.WriteFile(cursor, []byte(content), 0o644); err != nil {
			return out, err
		}
		out[".cursor/rules/maw.mdc"] = "created"
	} else {
		out[".cursor/rules/maw.mdc"], err = Upsert(cursor)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}
