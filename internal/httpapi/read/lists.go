package read

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/query"
)

func (a *API) handleProjects(w http.ResponseWriter, r *http.Request) {
	if rejectLegacyPagination(w, r) {
		return
	}
	var (
		items []map[string]any
		wm    query.Watermark
	)
	err := a.withReader(func(rd *query.Reader, tx *sql.Tx) error {
		wm = rd.Watermark()
		rows, err := tx.Query(`SELECT id, name, path, main_branch, max_parallel, created_at, last_seen
FROM projects ORDER BY name ASC, id ASC`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, name, path, mainBranch, created, lastSeen string
			var maxParallel int
			if err := rows.Scan(&id, &name, &path, &mainBranch, &maxParallel, &created, &lastSeen); err != nil {
				return err
			}
			items = append(items, map[string]any{
				"project_id": id, "name": name, "path": path,
				"main_branch": mainBranch, "max_parallel": maxParallel,
				"created_at": created, "last_seen": lastSeen,
			})
		}
		return rows.Err()
	})
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	writeOK(w, http.StatusOK, map[string]any{
		"items": items, "next_cursor": nil, "watermark": wm,
	})
}

func (a *API) handleBoard(w http.ResponseWriter, r *http.Request) {
	if rejectLegacyPagination(w, r) {
		return
	}
	projectID := strings.TrimSpace(r.URL.Query().Get("project_id"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	agent := strings.TrimSpace(r.URL.Query().Get("agent"))
	limit, err := parseLimit(r)
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	var (
		page     query.Page[query.TaskRow]
		revision int64
		wm       query.Watermark
	)
	err = a.withReader(func(rd *query.Reader, _ *sql.Tx) error {
		q := query.TaskQuery{
			ProjectID: projectID, Owner: agent, Limit: limit,
			Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
		}
		if status != "" {
			q.Status = model.TaskStatus(status)
		}
		var e error
		page, e = rd.Tasks(q)
		if e != nil {
			return e
		}
		wm = rd.Watermark()
		revision, e = rd.ProjectRevision(projectID)
		return e
	})
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	body := pageBody(page, revision)
	body["watermark"] = wm
	writeOK(w, http.StatusOK, body)
}

func (a *API) handleProjectTasks(w http.ResponseWriter, r *http.Request) {
	a.listProject(w, r, listTasks)
}

func (a *API) handleProjectNodes(w http.ResponseWriter, r *http.Request) {
	a.listProject(w, r, listNodes)
}

func (a *API) handleProjectRuns(w http.ResponseWriter, r *http.Request) {
	a.listProject(w, r, listRuns)
}

func (a *API) handleProjectOperations(w http.ResponseWriter, r *http.Request) {
	a.listProject(w, r, listOperations)
}

func (a *API) handleProjectDeliveries(w http.ResponseWriter, r *http.Request) {
	a.listProject(w, r, listDeliveries)
}

func (a *API) handleProjectInstances(w http.ResponseWriter, r *http.Request) {
	a.listProject(w, r, listInstances)
}

type listFn func(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error)

func (a *API) listProject(w http.ResponseWriter, r *http.Request, fn listFn) {
	if rejectLegacyPagination(w, r) {
		return
	}
	projectID := r.PathValue("project_id")
	if !model.ProjectID(projectID).Valid() {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_project_id",
			"project_id 必须是 16 位小写十六进制", nil)
		return
	}
	var body map[string]any
	err := a.withReader(func(rd *query.Reader, _ *sql.Tx) error {
		var e error
		body, e = fn(rd, projectID, r)
		return e
	})
	if isSent(err) {
		return
	}
	if ae, ok := err.(ambiguousError); ok {
		writeErr(w, http.StatusConflict, "ambiguous_display_id",
			"显示编号命中多个活跃实例，请改用 task_instance",
			map[string]any{"display_id": ae.DisplayID, "count": ae.Count})
		return
	}
	if err != nil {
		mapQueryErr(w, err)
		return
	}
	writeOK(w, http.StatusOK, body)
}

func listTasks(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error) {
	limit, err := parseLimit(r)
	if err != nil {
		return nil, err
	}
	displayID := strings.TrimSpace(r.URL.Query().Get("display_id"))
	if displayID != "" {
		instPage, err := rd.Instances(query.InstanceQuery{
			ProjectID: projectID, DisplayID: displayID, State: "active", Limit: 2,
		})
		if err != nil {
			return nil, err
		}
		if len(instPage.Items) > 1 {
			return nil, ambiguousDisplay(displayID, len(instPage.Items))
		}
	}
	q := query.TaskQuery{
		ProjectID: projectID,
		Owner:     strings.TrimSpace(r.URL.Query().Get("owner")),
		Limit:     limit,
		Cursor:    strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if st := strings.TrimSpace(r.URL.Query().Get("status")); st != "" {
		q.Status = model.TaskStatus(st)
	}
	page, err := rd.Tasks(q)
	if err != nil {
		return nil, err
	}
	if displayID != "" {
		filtered := make([]query.TaskRow, 0, 1)
		for _, item := range page.Items {
			if string(item.DisplayID) == displayID {
				filtered = append(filtered, item)
			}
		}
		page.Items = filtered
		page.Next = ""
	}
	rev, err := rd.ProjectRevision(projectID)
	if err != nil {
		return nil, err
	}
	return pageBody(page, rev), nil
}

func listNodes(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error) {
	limit, err := parseLimit(r)
	if err != nil {
		return nil, err
	}
	page, err := rd.Nodes(query.NodeQuery{
		ProjectID: projectID,
		Limit:     limit,
		Cursor:    strings.TrimSpace(r.URL.Query().Get("cursor")),
	})
	if err != nil {
		return nil, err
	}
	rev, err := rd.ProjectRevision(projectID)
	if err != nil {
		return nil, err
	}
	return pageBody(page, rev), nil
}

func listRuns(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error) {
	limit, err := parseLimit(r)
	if err != nil {
		return nil, err
	}
	q := query.RunQuery{
		ProjectID: projectID,
		Limit:     limit,
		Cursor:    strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if inst := strings.TrimSpace(r.URL.Query().Get("task_instance")); inst != "" {
		q.InstanceID = model.InstanceID(inst)
	}
	if ph := strings.TrimSpace(r.URL.Query().Get("phase")); ph != "" {
		q.Phase = model.RunPhase(ph)
	}
	page, err := rd.Runs(q)
	if err != nil {
		return nil, err
	}
	rev, err := rd.ProjectRevision(projectID)
	if err != nil {
		return nil, err
	}
	return pageBody(page, rev), nil
}

func listOperations(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error) {
	limit, err := parseLimit(r)
	if err != nil {
		return nil, err
	}
	q := query.OperationQuery{
		ProjectID: projectID,
		Limit:     limit,
		Cursor:    strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if inst := strings.TrimSpace(r.URL.Query().Get("task_instance")); inst != "" {
		q.InstanceID = model.InstanceID(inst)
	}
	if k := strings.TrimSpace(r.URL.Query().Get("kind")); k != "" {
		q.Kind = model.OperationKind(k)
	}
	page, err := rd.Operations(q)
	if err != nil {
		return nil, err
	}
	rev, err := rd.ProjectRevision(projectID)
	if err != nil {
		return nil, err
	}
	return pageBody(page, rev), nil
}

func listDeliveries(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error) {
	limit, err := parseLimit(r)
	if err != nil {
		return nil, err
	}
	q := query.DeliveryQuery{
		ProjectID: projectID,
		Limit:     limit,
		Cursor:    strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if inst := strings.TrimSpace(r.URL.Query().Get("task_instance")); inst != "" {
		q.InstanceID = model.InstanceID(inst)
	}
	page, err := rd.Deliveries(q)
	if err != nil {
		return nil, err
	}
	rev, err := rd.ProjectRevision(projectID)
	if err != nil {
		return nil, err
	}
	return pageBody(page, rev), nil
}

func listInstances(rd *query.Reader, projectID string, r *http.Request) (map[string]any, error) {
	limit, err := parseLimit(r)
	if err != nil {
		return nil, err
	}
	q := query.InstanceQuery{
		ProjectID: projectID,
		DisplayID: strings.TrimSpace(r.URL.Query().Get("display_id")),
		State:     strings.TrimSpace(r.URL.Query().Get("state")),
		Limit:     limit,
		Cursor:    strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	page, err := rd.Instances(q)
	if err != nil {
		return nil, err
	}
	if q.DisplayID != "" && q.State == "active" && len(page.Items) > 1 && q.Cursor == "" {
		// 活跃同名歧义：契约要求 409，而不是静默返回第一页。
		return nil, ambiguousDisplay(q.DisplayID, len(page.Items))
	}
	rev, err := rd.ProjectRevision(projectID)
	if err != nil {
		return nil, err
	}
	return pageBody(page, rev), nil
}

type ambiguousError struct {
	DisplayID string
	Count     int
}

func (e ambiguousError) Error() string { return "ambiguous_display_id" }

func ambiguousDisplay(displayID string, count int) error {
	return ambiguousError{DisplayID: displayID, Count: count}
}
