package protocol

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Tool 可写入专属规则的 AI 工具
type Tool struct {
	ID          string // cursor | qoder | trae | codebuddy | claude | windsurf
	DisplayName string
}

// Detected 在工程或本机发现的工具（用于决定是否写专属规则）
type Detected struct {
	Tool
	Reasons []string // 命中依据，便于 init 输出
}

func pathExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func lookPath(names ...string) bool {
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			return true
		}
	}
	return false
}

func appExists(names ...string) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	for _, n := range names {
		if pathExists(filepath.Join("/Applications", n)) {
			return true
		}
		// 用户目录下的 Applications
		if h := homeDir(); h != "" {
			if pathExists(filepath.Join(h, "Applications", n)) {
				return true
			}
		}
	}
	return false
}

func supportExists(names ...string) bool {
	h := homeDir()
	if h == "" {
		return false
	}
	base := filepath.Join(h, "Library", "Application Support")
	for _, n := range names {
		if pathExists(filepath.Join(base, n)) {
			return true
		}
	}
	return false
}

// DetectTools 检测工程内痕迹 + 本机是否安装/使用过对应 Agent。
// AGENTS.md 始终作为兜底，不在此列表中单独返回。
func DetectTools(repo string) []Detected {
	var out []Detected
	add := func(id, name string, reasons ...string) {
		var rs []string
		for _, r := range reasons {
			if r != "" {
				rs = append(rs, r)
			}
		}
		if len(rs) == 0 {
			return
		}
		out = append(out, Detected{Tool: Tool{ID: id, DisplayName: name}, Reasons: rs})
	}

	// Cursor
	var cursor []string
	if pathExists(filepath.Join(repo, ".cursor")) {
		cursor = append(cursor, "工程含 .cursor/")
	}
	if lookPath("cursor") {
		cursor = append(cursor, "PATH 有 cursor")
	}
	if appExists("Cursor.app") {
		cursor = append(cursor, "已安装 Cursor.app")
	}
	add("cursor", "Cursor", cursor...)

	// Qoder
	var qoder []string
	if pathExists(filepath.Join(repo, ".qoder")) {
		qoder = append(qoder, "工程含 .qoder/")
	}
	if pathExists(filepath.Join(homeDir(), ".qoder")) {
		qoder = append(qoder, "本机有 ~/.qoder")
	}
	if lookPath("qoder", "qoder-cli") {
		qoder = append(qoder, "PATH 有 qoder")
	}
	if appExists("Qoder IDE.app", "Qoder.app") {
		qoder = append(qoder, "已安装 Qoder")
	}
	if supportExists("Qoder") {
		qoder = append(qoder, "Application Support/Qoder")
	}
	add("qoder", "Qoder", qoder...)

	// Trae（国际版 .trae / 国内版 .trae-cn）
	var trae []string
	if pathExists(filepath.Join(repo, ".trae")) || pathExists(filepath.Join(repo, ".trae-cn")) {
		trae = append(trae, "工程含 .trae/")
	}
	if pathExists(filepath.Join(homeDir(), ".trae")) || pathExists(filepath.Join(homeDir(), ".trae-cn")) {
		trae = append(trae, "本机有 ~/.trae")
	}
	if lookPath("trae", "trae-cli") {
		trae = append(trae, "PATH 有 trae")
	}
	if appExists("Trae.app", "Trae CN.app") {
		trae = append(trae, "已安装 Trae")
	}
	if supportExists("Trae", "Trae CN") {
		trae = append(trae, "Application Support/Trae")
	}
	add("trae", "Trae", trae...)

	// CodeBuddy / WorkBuddy（腾讯，规则目录均为 .codebuddy）
	var cb []string
	if pathExists(filepath.Join(repo, ".codebuddy")) {
		cb = append(cb, "工程含 .codebuddy/")
	}
	if pathExists(filepath.Join(repo, ".workbuddy")) || pathExists(filepath.Join(repo, ".workbuddy-ai")) {
		cb = append(cb, "工程含 .workbuddy/")
	}
	if pathExists(filepath.Join(homeDir(), ".codebuddy")) {
		cb = append(cb, "本机有 ~/.codebuddy")
	}
	if lookPath("codebuddy", "workbuddy") {
		cb = append(cb, "PATH 有 codebuddy/workbuddy")
	}
	if appExists("CodeBuddy.app", "CodeBuddy CN.app", "WorkBuddy.app", "WorkBuddy AI.app") {
		cb = append(cb, "已安装 CodeBuddy/WorkBuddy")
	}
	if supportExists("CodeBuddy", "WorkBuddy") {
		cb = append(cb, "Application Support/CodeBuddy")
	}
	add("codebuddy", "CodeBuddy/WorkBuddy", cb...)

	// Claude Code：仅工程内已有痕迹时写入（避免本机安装就强行新建 CLAUDE.md）
	var claude []string
	if pathExists(filepath.Join(repo, "CLAUDE.md")) {
		claude = append(claude, "工程已有 CLAUDE.md")
	}
	if pathExists(filepath.Join(repo, ".claude")) {
		claude = append(claude, "工程含 .claude/")
	}
	add("claude", "Claude Code", claude...)

	// Windsurf / Cascade
	var ws []string
	if pathExists(filepath.Join(repo, ".windsurf")) || pathExists(filepath.Join(repo, ".windsurfrules")) {
		ws = append(ws, "工程含 windsurf 配置")
	}
	if pathExists(filepath.Join(homeDir(), ".codeium")) {
		ws = append(ws, "本机有 ~/.codeium")
	}
	if lookPath("windsurf") {
		ws = append(ws, "PATH 有 windsurf")
	}
	if appExists("Windsurf.app") {
		ws = append(ws, "已安装 Windsurf")
	}
	add("windsurf", "Windsurf", ws...)

	return out
}

// DetectSummary 便于日志：id=reason
func DetectSummary(ds []Detected) map[string]string {
	m := map[string]string{}
	for _, d := range ds {
		m[d.ID] = strings.Join(d.Reasons, "; ")
	}
	return m
}
