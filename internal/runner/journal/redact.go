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

	// meteringTokenKey 匹配计量语义的 token 字段（input_tokens、output_tokens…）。
	//
	// 这些字段是计量事实，不是凭据：把它们脱敏等于把用量观测销毁掉，
	// 下游只会看到「没看到」，而不是「看到了多少」。
	meteringTokenKey = regexp.MustCompile(`(?i)(^|[._-])tokens([._-]|$)`)

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
				// 数字不是凭据：计量字段（token 计数）被按字段名误判时，
				// 靠这一条保住数据——「看到 0」和「没看到」不是一回事。
				if val != nil && !isNumericValue(val) {
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
	if !sensitiveKey.MatchString(key) {
		return false
	}
	// 计量语义的 token 字段不是凭据。
	return !meteringTokenKey.MatchString(key)
}

// isNumericValue 报告值是否是数字。
//
// 凭据是字符串；数字出现在「秘密」字段名下，几乎都是计量值（token 计数、
// 密钥版本号之类）。把它抹掉等于把观测数据抹掉。
func isNumericValue(v any) bool {
	switch v.(type) {
	case float64, float32, int, int32, int64, json.Number:
		return true
	default:
		return false
	}
}
