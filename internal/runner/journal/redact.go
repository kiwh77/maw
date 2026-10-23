package journal

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// Redacted 是脱敏后的占位文本。
const Redacted = "[REDACTED]"

var (
	// sensitiveKey 匹配字段名本身表达秘密的词。宁可多脱敏，也不让凭据落盘。
	sensitiveKey = regexp.MustCompile(`(?i)(^|[._-])(token|secret|password|passwd|api[_-]?key|apikey|authorization|cookie|credential|private[_-]?key|access[_-]?key|session[_-]?key|client[_-]?secret)s?([._-]|$)`)

	valuePatterns = []struct {
		re      *regexp.Regexp
		replace string
	}{
		{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{8,}`), "Bearer " + Redacted},
		{regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{8,}`), "sk-" + Redacted},
		{regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?key|secret|token|password)\b\s*[:=]\s*["']?[^\s"',;}]{6,}`), "${1}=" + Redacted},
	}
)

// RedactJSON 对 JSON 文档做递归脱敏，返回脱敏后的字节与替换次数。
// 非 JSON 输入直接拒绝：日志里不应出现无法判定的自由文本负载。
func RedactJSON(data []byte) ([]byte, int, error) {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, 0, fmt.Errorf("journal: payload 不是合法 JSON: %w", err)
	}
	clean, n := redactValue(v)
	out, err := json.Marshal(clean)
	if err != nil {
		return nil, 0, fmt.Errorf("journal: 脱敏后无法序列化: %w", err)
	}
	return out, n, nil
}

func redactValue(v any) (any, int) {
	switch x := v.(type) {
	case map[string]any:
		n := 0
		for k, val := range x {
			if isSensitiveKey(k) {
				if val != nil {
					x[k] = Redacted
					n++
				}
				continue
			}
			clean, c := redactValue(val)
			x[k] = clean
			n += c
		}
		return x, n
	case []any:
		n := 0
		for i, val := range x {
			clean, c := redactValue(val)
			x[i] = clean
			n += c
		}
		return x, n
	case string:
		return redactText(x)
	default:
		return v, 0
	}
}

func redactText(s string) (string, int) {
	n := 0
	for _, p := range valuePatterns {
		found := p.re.FindAllString(s, -1)
		if len(found) == 0 {
			continue
		}
		n += len(found)
		s = p.re.ReplaceAllString(s, p.replace)
	}
	return s, n
}

func isSensitiveKey(key string) bool {
	return sensitiveKey.MatchString(key)
}
