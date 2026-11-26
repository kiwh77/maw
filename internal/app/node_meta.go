package app

import (
	"encoding/json"
	"strings"
)

func metaValue(meta map[string]any, key string) (any, bool) {
	if meta == nil {
		return nil, false
	}
	v, ok := meta[key]
	return v, ok
}

func metaString(meta map[string]any, key string) string {
	v, ok := metaValue(meta, key)
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

func metaInt(meta map[string]any, key string) int {
	v, ok := metaValue(meta, key)
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i)
		}
	}
	return 0
}

func effectiveModel(meta map[string]any, override string) string {
	if m := strings.TrimSpace(override); m != "" {
		return m
	}
	if meta == nil {
		return ""
	}
	m, _ := meta["default_model"].(string)
	return strings.TrimSpace(m)
}

func nodeAutoProbeEnabled(meta map[string]any) bool {
	if meta == nil {
		return true
	}
	v, ok := meta["auto_probe"]
	if !ok {
		return true
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "false" && t != "0"
	default:
		return true
	}
}

func nodeEntryPath(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if p, ok := meta["entry_path"].(string); ok && strings.TrimSpace(p) != "" {
		return strings.TrimSpace(p)
	}
	raw, ok := meta["argv"]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case []any:
		if len(v) > 0 {
			return strings.TrimSpace(stringify(v[0]))
		}
	case []string:
		if len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}
