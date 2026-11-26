package query

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
)

const (
	// DefaultLimit 是省略 limit 时的页大小（HTTP 契约 §2）。
	DefaultLimit = 50
	// MaxLimit 是一页的上限；超过它必须报错，不许悄悄改成 200。
	MaxLimit = 200
)

// 游标种类：跨资源复用游标会让排序键错位，所以种类不符直接拒绝。
const (
	kindTasks      = "tasks"
	kindInstances  = "instances"
	kindRuns       = "runs"
	kindOperations = "operations"
	kindNodes      = "nodes"
	kindDeliveries = "deliveries"
	kindEvents     = "events"
)

// Page 是一页结果。
//
// Next 为空表示这是最后一页；非空时必须原样回传给下一次查询。
// Watermark 属于整个快照，同一页的 Items 与它同源。
type Page[T any] struct {
	Items     []T       `json:"items"`
	Next      string    `json:"next_cursor,omitempty"`
	Watermark Watermark `json:"watermark"`
}

// normalizeLimit 按契约校验页大小：省略＝50，允许 1–200，越界即错。
func normalizeLimit(limit int) (int, error) {
	switch {
	case limit == 0:
		return DefaultLimit, nil
	case limit < 1 || limit > MaxLimit:
		return 0, invalidPagination("limit", "必须在 1–%d 之间，收到 %d", MaxLimit, limit)
	}
	return limit, nil
}

// cursorPayload 是游标的内部结构；对外只有 base64 字符串。
//
// Keys 是「上一页最后一条的排序键」，按该实体的 ORDER BY 顺序排列，
// 且一定包含足够的字段把排序键补成唯一。
type cursorPayload struct {
	Kind string   `json:"k"`
	Keys []string `json:"v"`
}

func encodeCursor(kind string, keys ...string) string {
	raw, err := json.Marshal(cursorPayload{Kind: kind, Keys: keys})
	if err != nil {
		// 只有字符串切片，Marshal 不会失败；失败也不该伪造一个游标。
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor 解析游标并核对字段个数：游标是客户端传来的不透明串，
// 解析失败、种类不符、字段缺失都算 invalid_pagination，不能退化成「从头开始」。
func decodeCursor(raw, kind string, want int) ([]string, error) {
	bad := invalidPagination("cursor", "游标无法解析或不属于 %s 列表", kind)
	blob, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, bad
	}
	var p cursorPayload
	if err := json.Unmarshal(blob, &p); err != nil {
		return nil, bad
	}
	if p.Kind != kind || len(p.Keys) != want {
		return nil, bad
	}
	return p.Keys, nil
}

// intKey 把游标里的数字键还原；格式不对同样算游标非法。
func intKey(raw, kind string) (int, error) {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalidPagination("cursor", "%s 列表的游标缺少数字键", kind)
	}
	return n, nil
}

func itoa(n int) string { return strconv.Itoa(n) }
