package observability

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/projection"
	"github.com/kiwh77/maw/internal/query"
)

const (
	// defaultTZ 是契约 §3 规定的默认展示时区：日切按它的本地零点。
	defaultTZ = "Asia/Shanghai"
	// maxWindowSpan 是单次查询允许的最大跨度。
	//
	// 上限存在的理由是完整性：窗口越大，投影补算越可能没跑完，报表就越容易
	// 被当成「这段时间真的没活动」。超限直接 422，不静默截断。
	maxWindowSpan = 31 * 24 * time.Hour
	// maxHours 是 hours 参数上限（7 天）；heatmap 只接受 24 小时窗口，另有限制。
	maxHours = 168
)

// windowSpec 是解析后的查询窗口与展示上下文。
type windowSpec struct {
	Window projection.Window
	Loc    *time.Location
	TZ     string
	Hours  int
	// Defaulted 为真表示客户端没给窗口，服务端补了默认值；响应必须回显。
	Defaulted bool
}

// parseWindow 是所有读接口共用的窗口解析（时间轴、会话、统计、提醒）。
//
// 支持的写法（互斥，混用即 422）：
//
//	start + end   —— 显式半开窗口 [start, end)，UTC ISO8601
//	day           —— 查询时区的某个本地日 [00:00, 次日 00:00)
//	hours + anchor—— 以 anchor（默认现在）为终点的最近 N 小时
//	（都不给）     —— 最近 defSpan，并标 Defaulted
//
// 统一在这里做三件事：时区解析、半开边界校验、跨度上限。各接口只决定默认跨度，
// 不各自实现时间语义——否则「同一天」在不同接口会切成不同的时间段。
func parseWindow(q url.Values, now time.Time, defSpan time.Duration) (windowSpec, error) {
	spec := windowSpec{Loc: time.UTC, TZ: defaultTZ}
	tz := strings.TrimSpace(q.Get("tz"))
	if tz == "" {
		tz = defaultTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return spec, invalidField(codeInvalidTimezone, "tz",
			"时区必须是 IANA 名称，例如 Asia/Shanghai")
	}
	spec.Loc, spec.TZ = loc, tz

	startRaw := strings.TrimSpace(q.Get("start"))
	endRaw := strings.TrimSpace(q.Get("end"))
	dayRaw := strings.TrimSpace(q.Get("day"))
	hoursRaw := strings.TrimSpace(q.Get("hours"))
	anchorRaw := strings.TrimSpace(q.Get("anchor"))

	switch {
	case startRaw != "" || endRaw != "":
		if startRaw == "" || endRaw == "" {
			return spec, invalidField(codeInvalidWindow, "start", "start 与 end 必须成对给出")
		}
		if dayRaw != "" || hoursRaw != "" || anchorRaw != "" {
			return spec, invalidField(codeInvalidWindow, "start",
				"start／end 与 day／hours／anchor 互斥，不能混用")
		}
		start, err := parseTimeParam("start", startRaw)
		if err != nil {
			return spec, err
		}
		end, err := parseTimeParam("end", endRaw)
		if err != nil {
			return spec, err
		}
		spec.Window = projection.Window{Start: start, End: end}
	case dayRaw != "":
		if hoursRaw != "" || anchorRaw != "" {
			return spec, invalidField(codeInvalidWindow, "day", "day 与 hours／anchor 互斥")
		}
		start, err := time.ParseInLocation("2006-01-02", dayRaw, loc)
		if err != nil {
			return spec, invalidField(codeInvalidWindow, "day", "day 必须是 YYYY-MM-DD")
		}
		spec.Window = projection.Window{
			Start: start.UTC(),
			End:   start.AddDate(0, 0, 1).UTC(),
		}
	case hoursRaw != "" || anchorRaw != "":
		hours, err := parseHours(hoursRaw)
		if err != nil {
			return spec, err
		}
		anchor := now.UTC()
		if anchorRaw != "" {
			anchor, err = parseTimeParam("anchor", anchorRaw)
			if err != nil {
				return spec, err
			}
		}
		spec.Window = projection.Window{
			Start: anchor.Add(-time.Duration(hours) * time.Hour),
			End:   anchor,
		}
	default:
		if defSpan <= 0 {
			defSpan = 24 * time.Hour
		}
		spec.Window = projection.Window{Start: now.Add(-defSpan).UTC(), End: now.UTC()}
		spec.Defaulted = true
	}

	return finishWindow(spec)
}

// finishWindow 做边界校验并算出小时数。
func finishWindow(spec windowSpec) (windowSpec, error) {
	spec.Window.Start = spec.Window.Start.UTC()
	spec.Window.End = spec.Window.End.UTC()
	if !spec.Window.Valid() {
		return spec, invalidField(codeInvalidWindow, "end", "窗口必须满足 end 晚于 start")
	}
	span := spec.Window.End.Sub(spec.Window.Start)
	if span > maxWindowSpan {
		return spec, invalidField(codeWindowTooLarge, "end",
			"单次查询跨度不得超过 "+strconv.Itoa(int(maxWindowSpan.Hours()))+" 小时")
	}
	spec.Hours = int(math.Round(span.Hours()))
	if spec.Hours < 1 {
		spec.Hours = 1
	}
	return spec, nil
}

// parseDayWindow 是热图专用窗口：固定一个本地日，跨度 24 小时。
//
// 热图回答的是「这一天哪个小时忙」，跨多日会得到多组小时桶叠在同一张图上，
// 所以这里比 parseWindow 更严：超过 24 小时直接 422，不静默截成一天。
func parseDayWindow(q url.Values, now time.Time) (windowSpec, error) {
	spec, err := parseWindow(q, now, 24*time.Hour)
	if err != nil {
		return spec, err
	}
	if spec.Window.End.Sub(spec.Window.Start) > 24*time.Hour {
		return spec, invalidField(codeWindowTooLarge, "day",
			"热图只接受单个本地日（跨度不超过 24 小时）")
	}
	return spec, nil
}

// dayOf 返回窗口起点所在的本地日期（YYYY-MM-DD）。
func dayOf(spec windowSpec) string {
	loc := spec.Loc
	if loc == nil {
		loc = time.UTC
	}
	return spec.Window.Start.In(loc).Format("2006-01-02")
}

// parseTimeParam 解析 RFC3339／RFC3339Nano；不带时区偏移的时间按 UTC 拒绝，
// 因为「本地时间」必须由 tz 参数说明，不能靠服务器所在时区猜。
func parseTimeParam(field, raw string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, invalidField(codeInvalidWindow, field,
		field+" 必须是带时区的 ISO8601（例如 2026-10-23T03:00:00Z）")
}

// parseHours 解析 hours：1–168 的整数，省略按 24。
func parseHours(raw string) (int, error) {
	if raw == "" {
		return 24, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > maxHours {
		return 0, invalidField(codeInvalidWindow, "hours",
			"hours 必须是 1–"+strconv.Itoa(maxHours)+" 的整数")
	}
	return n, nil
}

// parseLimit 按契约 §2 校验页大小：省略＝50，允许 1–200，越界即 422。
func parseLimit(q url.Values) (int, error) {
	raw := strings.TrimSpace(q.Get("limit"))
	if raw == "" {
		return query.DefaultLimit, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, &query.Error{Code: query.CodeInvalidPagination, Field: "limit",
			Msg: "limit 必须是整数"}
	}
	if n < 1 || n > query.MaxLimit {
		return 0, &query.Error{Code: query.CodeInvalidPagination, Field: "limit",
			Msg: "limit 必须在 1–" + strconv.Itoa(query.MaxLimit) + " 之间"}
	}
	return n, nil
}

// rejectLegacyPagination 拒绝 offset／page：只支持游标分页（契约 §2）。
func rejectLegacyPagination(q url.Values) error {
	if q.Has("offset") || q.Has("page") {
		return &query.Error{Code: query.CodeInvalidPagination, Field: "offset",
			Msg: "只支持游标分页，禁止 offset／page"}
	}
	return nil
}

// parseKinds 解析并校验区间类别过滤；未知类别报 422 而不是当零匹配。
func parseKinds(q url.Values) ([]projection.Kind, error) {
	raw := make([]string, 0, len(q["kind"]))
	for _, v := range q["kind"] {
		raw = append(raw, v)
	}
	if single := strings.TrimSpace(q.Get("kinds")); single != "" {
		raw = append(raw, strings.Split(single, ",")...)
	}
	out := make([]projection.Kind, 0, len(raw))
	seen := map[projection.Kind]struct{}{}
	for _, v := range raw {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		k := projection.Kind(v)
		if !k.Valid() {
			return nil, invalidField(codeUnknownEnum, "kind", "未知区间类别 "+strconv.Quote(v))
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, nil
}

// parseSourceNS 解析来源过滤。
//
// 来源命名空间是开放集合（适配器注册什么就有什么），所以不做出现在白名单里的校验；
// 只校验形态，避免把空串、超长串或含空白的串当成过滤器下推给 SQL。
func parseSourceNS(q url.Values) (string, error) {
	raw := strings.TrimSpace(q.Get("source_ns"))
	if raw == "" {
		return "", nil
	}
	if len(raw) > 64 {
		return "", invalidField(codeInvalidFilter, "source_ns", "source_ns 最长 64 个字符")
	}
	for _, c := range raw {
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '.' || c == '_' || c == ':' || c == '-'
		if !ok {
			return "", invalidField(codeInvalidFilter, "source_ns", "source_ns 含非法字符")
		}
	}
	return raw, nil
}

// parseEnum 校验封闭枚举过滤词；空串表示不过滤。
func parseEnum(q url.Values, field string, allowed ...string) (string, error) {
	raw := strings.TrimSpace(q.Get(field))
	if raw == "" {
		return "", nil
	}
	for _, a := range allowed {
		if raw == a {
			return raw, nil
		}
	}
	return "", invalidField(codeUnknownEnum, field, "未知取值 "+strconv.Quote(raw))
}

// 游标种类：跨资源复用游标会让排序键错位，所以种类不符直接拒绝。
const (
	kindTimeline  = "timeline"
	kindSessions  = "sessions"
	kindReminders = "reminders"
)

// cursorPayload 是游标内部结构；对外只有 base64 字符串（客户端不得解析）。
type cursorPayload struct {
	Kind string   `json:"k"`
	Keys []string `json:"v"`
}

func encodeCursor(kind string, keys ...string) string {
	raw, err := json.Marshal(cursorPayload{Kind: kind, Keys: keys})
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// decodeCursor 解析游标并核对字段个数；解析失败不退化成「从头开始」，
// 否则客户端会看到重复记录却以为翻到了下一页。
func decodeCursor(raw, kind string, want int) ([]string, error) {
	bad := &query.Error{Code: query.CodeInvalidPagination, Field: "cursor",
		Msg: "游标无法解析或不属于 " + kind + " 列表"}
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

// cursorOrNil 让「最后一页」在 JSON 里是 null 而不是空串（契约 §2）。
func cursorOrNil(next string) any {
	if next == "" {
		return nil
	}
	return next
}
