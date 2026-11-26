package events

import (
	"time"

	"github.com/kiwh77/maw/internal/engine/model"
)

// entry 是去重索引里的一条：某来源某键对应的事件身份与内容指纹。
type entry struct {
	eventID     model.EventID
	fingerprint string
	occurredAt  time.Time
	cursor      model.Cursor
	key         string
}

// lookupResult 是一次键查找的三种结局。
type lookupResult int

const (
	// lookupMiss 键未见：可以作为新事件接受。
	lookupMiss lookupResult = iota
	// lookupReplay 同键同指纹：幂等重放，复用原事件，不新建。
	lookupReplay
	// lookupConflict 同键不同指纹：身份被两种内容争用，不覆盖也不新建。
	lookupConflict
)

// index 是分域去重索引 + 发生时间前沿 + 游标水位。
//
// 三件事共用一张表，因为它们必须同步推进：接受一个新事件就要同时登记键、把前沿
// 推到该事件的发生时间、把水位提升到它的游标。分开放会出现「键已登记但前沿没动」
// 的中间态，那时一条乱序记录会被误判成正常到达。
type index struct {
	// byScope 的第一层是去重域（域+接入器），第二层是来源键。
	byScope map[string]map[string]entry
	// frontier 是每个域已见的最大发生时间：低于它的新记录即迟到。
	frontier map[string]time.Time
	// maxCursor 是已发出的最大游标，游标只前进。
	maxCursor model.Cursor
}

func newIndex() *index {
	return &index{
		byScope:  make(map[string]map[string]entry),
		frontier: make(map[string]time.Time),
	}
}

// lookup 判定同一域内某键的结局。
func (x *index) lookup(scope, key, fingerprint string) (lookupResult, entry) {
	keys, ok := x.byScope[scope]
	if !ok {
		return lookupMiss, entry{}
	}
	e, ok := keys[key]
	if !ok {
		return lookupMiss, entry{}
	}
	if e.fingerprint == fingerprint {
		return lookupReplay, e
	}
	return lookupConflict, e
}

// register 登记一条已接受的事件：键、指纹、发生时间与游标一起落。
func (x *index) register(scope string, e entry) {
	keys, ok := x.byScope[scope]
	if !ok {
		keys = make(map[string]entry)
		x.byScope[scope] = keys
	}
	keys[e.key] = e
	if prev, ok := x.frontier[scope]; !ok || e.occurredAt.After(prev) {
		x.frontier[scope] = e.occurredAt
	}
	if e.cursor > x.maxCursor {
		x.maxCursor = e.cursor
	}
}

// isLate 报告该发生时间是否落在域前沿之后方（严格早于即迟到）。
// 相等不算迟到：同一毫秒内的多条记录是正常同批，不是乱序。
func (x *index) isLate(scope string, occurred time.Time) bool {
	prev, ok := x.frontier[scope]
	if !ok || prev.IsZero() {
		return false
	}
	return occurred.Before(prev)
}

// frontierOf 返回某域已见的最大发生时间。
func (x *index) frontierOf(scope string) (time.Time, bool) {
	t, ok := x.frontier[scope]
	return t, ok
}
