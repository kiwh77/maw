package blueprint

import (
	"regexp"
	"strings"
	"unicode"
)

var (
	// 路径尾部说明性括号：`pkg/**（限单包）` / `pkg/** (note)`
	trailingNoteRE = regexp.MustCompile(`[\s]*[（(][^）)]*[）)]\s*$`)
	// 合法 write_scope glob：路径字符 + glob 通配。
	pathGlobRE = regexp.MustCompile(`^[A-Za-z0-9_./\-*{}[\]?]+$`)
)

// SanitizeWriteScope 清洗蓝图 write_scope：去掉说明性括号残片，拒绝明显非法碎片。
// 保留 lead split 的 TODO 占位与正常路径 glob。
func SanitizeWriteScope(scopes []string) (clean []string, dropped []string) {
	seen := map[string]bool{}
	for _, raw := range scopes {
		orig := strings.TrimSpace(raw)
		if orig == "" {
			continue
		}
		s := trailingNoteRE.ReplaceAllString(orig, "")
		s = strings.TrimSpace(s)
		if isScopeFragment(s) {
			dropped = append(dropped, orig)
			continue
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		clean = append(clean, s)
	}
	return clean, dropped
}

// isScopeFragment 识别误切/说明串进数组的残片（如「限单包）」），
// 不误杀 TODO 占位或正常路径。
func isScopeFragment(s string) bool {
	if s == "" {
		return true
	}
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "TODO") {
		return false
	}
	if pathGlobRE.MatchString(s) {
		return false
	}
	// 半截括号
	if strings.HasPrefix(s, "）") || strings.HasPrefix(s, ")") ||
		strings.HasSuffix(s, "（") || strings.HasSuffix(s, "(") ||
		(strings.ContainsAny(s, "）)") && !strings.ContainsAny(s, "（(")) {
		return true
	}
	// 纯中文说明、无路径字符
	hasPathish := strings.ContainsAny(s, "/.*")
	if !hasPathish {
		onlyHanOrPunct := true
		for _, r := range s {
			if unicode.Is(unicode.Han, r) || unicode.IsPunct(r) || unicode.IsSpace(r) {
				continue
			}
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				onlyHanOrPunct = false
				break
			}
		}
		if onlyHanOrPunct {
			for _, r := range s {
				if unicode.Is(unicode.Han, r) {
					return true
				}
			}
		}
	}
	return false
}

// SanitizeFileScopes 就地清洗 File 内全部 task 的 write_scope。
func SanitizeFileScopes(f *File) map[string][]string {
	droppedByTask := map[string][]string{}
	if f == nil {
		return droppedByTask
	}
	for i := range f.Tasks {
		clean, dropped := SanitizeWriteScope(f.Tasks[i].WriteScope)
		f.Tasks[i].WriteScope = clean
		if len(dropped) > 0 {
			droppedByTask[f.Tasks[i].ID] = dropped
		}
	}
	return droppedByTask
}
