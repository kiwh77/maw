package events

import (
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/query"
)

// pullPage 按「扫描窗」补拉：先取 limit 条未过滤事件，再在内存按 kind 过滤。
//
// next_cursor 永远停在本窗最后扫描到的入库游标（即便 items 为空），
// 这样超过 200 条非匹配积压时客户端靠空页推进，不会跳过后面的匹配。
// has_more 在扫满一整窗时为 true；不足一窗则无后续。
func (a *API) handleEventsPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if rejectLegacyPagination(w, r) {
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	q := r.URL.Query()
	projectID := strings.TrimSpace(q.Get("project_id"))
	cursor := strings.TrimSpace(q.Get("cursor"))
	kinds := filterKinds(q["kind"])
	if single := strings.TrimSpace(q.Get("kinds")); single != "" {
		for _, part := range strings.Split(single, ",") {
			kinds = append(kinds, strings.TrimSpace(part))
		}
		kinds = filterKinds(kinds)
	}

	var (
		page      query.Page[query.EventRow]
		revision  int64
		hasMore   bool
		nextCur   string
		items     []query.EventRow
	)
	err = a.withReader(func(rd *query.Reader) error {
		raw, err := rd.Events(query.EventQuery{
			ProjectID: projectID,
			Cursor:    cursor,
			Limit:     limit,
			// 故意不传 Kinds：过滤在扫描窗之后做，才能产出「空页 + 扫描水位」。
		})
		if err != nil {
			return err
		}
		page = raw
		if projectID != "" {
			revision, err = rd.ProjectRevision(projectID)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		mapQueryErr(w, err)
		return
	}

	scanLimit := limit
	if scanLimit == 0 {
		scanLimit = query.DefaultLimit
	}
	hasMore = len(page.Items) == scanLimit
	if len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		nextCur = query.EventCursor(last.Cursor)
	}
	if !hasMore {
		nextCur = ""
	}

	items = make([]query.EventRow, 0, len(page.Items))
	if len(kinds) == 0 {
		items = append(items, page.Items...)
	} else {
		want := make(map[string]struct{}, len(kinds))
		for _, k := range kinds {
			want[k] = struct{}{}
		}
		for _, row := range page.Items {
			if _, ok := want[row.Kind]; ok {
				items = append(items, row)
			}
		}
	}

	var next any
	if nextCur == "" {
		next = nil
	} else {
		next = nextCur
	}
	writeOK(w, http.StatusOK, map[string]any{
		"items":       items,
		"next_cursor": next,
		"has_more":    hasMore,
		"revision":    revision,
		"watermark":   page.Watermark,
	})
}

func filterKinds(raw []string) []string {
	out := make([]string, 0, len(raw))
	seen := map[string]struct{}{}
	for _, k := range raw {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out
}
