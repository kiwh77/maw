package identity

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Worker 命名：{应用}-{工程slug}-{序号}，例如 cursor-demo-1
var canonicalRE = regexp.MustCompile(`^([a-z][a-z0-9]*)-([a-z][a-z0-9]*)-([1-9][0-9]*)$`)

var nonSlugRE = regexp.MustCompile(`[^a-z0-9]+`)

// DetectApp 从环境推断当前 AI 应用短名；可用 MAW_APP 覆盖。
func DetectApp() string {
	if v := strings.TrimSpace(os.Getenv("MAW_APP")); v != "" {
		return ProjectSlug(v)
	}
	switch {
	case envTruthy("CURSOR_AGENT") || hasEnvPrefix("CURSOR_") || strings.Contains(strings.ToLower(os.Getenv("VSCODE_PROCESS_TITLE")), "cursor"):
		return "cursor"
	case envTruthy("CODEBUDDY") || hasEnvPrefix("CODEBUDDY_") || hasEnvPrefix("WORKBUDDY_") ||
		strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "codebuddy"):
		return "codebuddy"
	case hasEnvPrefix("QODER_") || strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "qoder"):
		return "qoder"
	case hasEnvPrefix("TRAE_") || strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "trae"):
		return "trae"
	case hasEnvPrefix("CLAUDE_") || envTruthy("CLAUDECODE") || strings.Contains(os.Getenv("PATH"), "claude"):
		return "claude"
	case hasEnvPrefix("WINDSURF_") || hasEnvPrefix("CODEIUM_"):
		return "windsurf"
	default:
		return "agent"
	}
}

// ProjectSlug 将工程名压成小写字母数字（去掉连字符等），如 my-app → myapp。
func ProjectSlug(projectName string) string {
	s := strings.ToLower(strings.TrimSpace(projectName))
	s = nonSlugRE.ReplaceAllString(s, "")
	if s == "" {
		return "project"
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "p" + s
	}
	return s
}

// Format 生成规范 Worker 名。
func Format(app, projectSlug string, n int) string {
	app = ProjectSlug(app)
	projectSlug = ProjectSlug(projectSlug)
	if n < 1 {
		n = 1
	}
	return app + "-" + projectSlug + "-" + strconv.Itoa(n)
}

// Parse 解析规范名；不合规则 ok=false。
func Parse(name string) (app, project string, n int, ok bool) {
	m := canonicalRE.FindStringSubmatch(strings.TrimSpace(name))
	if m == nil {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(m[3])
	if err != nil || n < 1 {
		return "", "", 0, false
	}
	return m[1], m[2], n, true
}

// IsCanonical 是否符合 {app}-{project}-{n}。
func IsCanonical(name string) bool {
	_, _, _, ok := Parse(name)
	return ok
}

// NextSeq 返回该 app+工程 下尚未占用的最小可用序号（从 1 起，跳过已占用）。
func NextSeq(app, projectSlug string, existingIDs []string) int {
	app = ProjectSlug(app)
	projectSlug = ProjectSlug(projectSlug)
	used := map[int]bool{}
	for _, id := range existingIDs {
		a, p, n, ok := Parse(id)
		if ok && a == app && p == projectSlug {
			used[n] = true
		}
	}
	for i := 1; ; i++ {
		if !used[i] {
			return i
		}
	}
}

// Allocate 分配下一个规范 Worker 名。
func Allocate(app, projectName string, existingIDs []string) string {
	slug := ProjectSlug(projectName)
	app = ProjectSlug(app)
	return Format(app, slug, NextSeq(app, slug, existingIDs))
}

// Suggest 按当前应用与工程名建议一个未占用的规范名。
func Suggest(projectName string, existingIDs []string) string {
	return Allocate(DetectApp(), projectName, existingIDs)
}

// ResolveOrAllocate：有显式名 / MAW_AGENT / .maw/agent 则用之；否则自动分配规范名。
func ResolveOrAllocate(cwd, explicit, projectName string, existingIDs []string) (who string, allocated bool, err error) {
	who, err = Resolve(cwd, explicit)
	if err == nil {
		return who, false, nil
	}
	if strings.TrimSpace(explicit) != "" {
		return "", false, err
	}
	return Suggest(projectName, existingIDs), true, nil
}

func envTruthy(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return v != "" && v != "0" && v != "false" && v != "no"
}

func hasEnvPrefix(prefix string) bool {
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}
