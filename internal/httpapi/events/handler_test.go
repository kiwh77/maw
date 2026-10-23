package events_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	srcEvents "github.com/kiwh77/maw/internal/events"
	httpevents "github.com/kiwh77/maw/internal/httpapi/events"
	"github.com/kiwh77/maw/internal/localauth"
	"github.com/kiwh77/maw/internal/query"
)

const projectID = "0123456789abcdef"

type fixture struct {
	store   *db.Store
	auth    *localauth.Authenticator
	readTok string
	writeTok string
	changes *httpevents.MemoryChanges
	api     *httpevents.API
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatal(err)
	}
	tokStore, err := localauth.OpenTokenStore(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	readPlain, _, err := tokStore.Mint("reader-1", localauth.ScopeRead)
	if err != nil {
		t.Fatal(err)
	}
	writePlain, _, err := tokStore.Mint("cursor-goalagents-1", localauth.ScopeWrite)
	if err != nil {
		t.Fatal(err)
	}
	auth := &localauth.Authenticator{Tokens: tokStore}
	changes := httpevents.NewMemoryChanges()
	t.Cleanup(changes.Close)
	api := &httpevents.API{
		Store:   store,
		Auth:    auth,
		Changes: changes,
		Reports: httpevents.NewStoreReporter(store),
	}
	f := &fixture{store: store, auth: auth, readTok: readPlain, writeTok: writePlain, changes: changes, api: api}
	f.write(t, func(tx *sql.Tx) error {
		return db.UpsertProject(tx, &db.Project{
			ID: projectID, Name: "demo", Path: "/tmp/demo", MainBranch: "main",
		})
	})
	return f
}

func (f *fixture) write(t *testing.T, fn func(*sql.Tx) error) {
	t.Helper()
	if err := f.store.Write(fn); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) seedEvents(t *testing.T, n int, kind string) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		for i := 0; i < n; i++ {
			k := kind
			if k == "" {
				if i%3 == 0 {
					k = "agent.progress"
				} else {
					k = "run.phase"
				}
			}
			if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
				ID: model.EventID(fmt.Sprintf("evt_seed%04d", i)),
				ProjectID: projectID, Kind: k, SchemaVersion: 1,
				OccurredAt: now.Add(time.Duration(i) * time.Second),
				ReceivedAt: now.Add(time.Duration(i) * time.Second),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (f *fixture) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030"+path, nil)
	req.Header.Set("Authorization", "Bearer "+f.readTok)
	rec := httptest.NewRecorder()
	f.api.Handler().ServeHTTP(rec, req)
	return rec
}

func decodeOK(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["ok"] != true {
		t.Fatalf("ok!=true: %v", body)
	}
	return body
}

func TestPullScanWindowEmptyPagesDoNotSkipMatches(t *testing.T) {
	f := newFixture(t)
	// 250 条：前 220 条非匹配，后 30 条匹配 agent.progress
	f.write(t, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		for i := 0; i < 250; i++ {
			kind := "run.phase"
			if i >= 220 {
				kind = "agent.progress"
			}
			if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
				ID: model.EventID(fmt.Sprintf("evt_bulk%04d", i)),
				ProjectID: projectID, Kind: kind, SchemaVersion: 1,
				OccurredAt: now.Add(time.Duration(i) * time.Millisecond),
				ReceivedAt: now.Add(time.Duration(i) * time.Millisecond),
			}); err != nil {
				return err
			}
		}
		return nil
	})

	var (
		cursor   string
		seen     int
		pages    int
		empty    int
		hasMore  = true
	)
	for hasMore && pages < 20 {
		pages++
		path := "/api/v1/events?project_id=" + projectID + "&limit=200&kind=agent.progress"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		body := decodeOK(t, f.get(t, path))
		items, _ := body["items"].([]any)
		seen += len(items)
		if len(items) == 0 {
			empty++
		}
		hasMore, _ = body["has_more"].(bool)
		if body["next_cursor"] == nil {
			cursor = ""
			if hasMore {
				t.Fatal("has_more=true 时 next_cursor 不得为 null")
			}
			break
		}
		cursor, _ = body["next_cursor"].(string)
	}
	if empty == 0 {
		t.Fatal("积压过滤应至少出现一页空结果（扫描水位推进）")
	}
	if seen != 30 {
		t.Fatalf("应补齐全部 30 条匹配，得到 %d（pages=%d empty=%d）", seen, pages, empty)
	}
	if hasMore {
		t.Fatal("扫完后 has_more 应为 false")
	}
}

func TestPullRejectsOffsetAndRequiresAuth(t *testing.T) {
	f := newFixture(t)
	rec := f.get(t, "/api/v1/events?offset=0")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("offset → 422，得到 %d", rec.Code)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/events", nil)
	rec = httptest.NewRecorder()
	f.api.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无凭据 → 401，得到 %d", rec.Code)
	}
}

func TestSSEInvalidateAndLegacyBoardChanged(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8030/api/v1/events/stream", nil).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+f.readTok)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		f.api.Handler().ServeHTTP(rec, req)
		close(done)
	}()

	// 等 ready
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(rec.Body.String(), "event: ready") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(rec.Body.String(), "event: ready") {
		t.Fatalf("未收到 ready: %q", rec.Body.String())
	}

	f.changes.Publish(httpevents.ChangeSignal{
		Watermark: query.Watermark{Cursor: 9, Revision: 3},
		Revision:  3,
		At:        time.Now().UTC(),
	})

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		body := rec.Body.String()
		if strings.Contains(body, "event: board-changed") && strings.Contains(body, "event: invalidate") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: board-changed") {
		t.Fatalf("缺旧帧 board-changed: %q", body)
	}
	if !strings.Contains(body, "event: invalidate") {
		t.Fatalf("缺新帧 invalidate: %q", body)
	}
	// 不得把事实正文伪装成 SSE data 数组
	if strings.Contains(body, `"event_id"`) {
		t.Fatalf("SSE 不应承载事件正文: %q", body)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("stream 未随取消退出")
	}
}

func TestStatusReportDoesNotChangeTaskStatus(t *testing.T) {
	f := newFixture(t)
	var taskStatus string
	f.write(t, func(tx *sql.Tx) error {
		task := &db.Task{
			ProjectID: projectID, ID: "VN-605", Title: "events",
			Status: string(model.TaskReady), Priority: 10, MergeOrder: 10,
		}
		if err := db.UpsertTask(tx, task); err != nil {
			return err
		}
		taskStatus = task.Status
		return nil
	})

	payload := map[string]any{
		"actor": "cursor-goalagents-1", "project_id": projectID,
		"task_id": "VN-605", "status": "working", "message": "补拉中", "phase": "http", "pct": 40,
	}
	raw, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/status-reports", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.writeTok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-status-report-0001")
	rec := httptest.NewRecorder()
	f.api.Handler().ServeHTTP(rec, req)
	body := decodeOK(t, rec)
	if body["changed_task_status"] != false {
		t.Fatalf("旁路上报不得改任务状态: %v", body)
	}
	if body["accepted"].(float64) < 1 {
		t.Fatalf("应接受观测: %v", body)
	}

	f.write(t, func(tx *sql.Tx) error {
		task, err := db.GetTask(tx, projectID, "VN-605")
		if err != nil {
			return err
		}
		if task.Status != taskStatus {
			t.Fatalf("任务状态被改成 %s", task.Status)
		}
		return nil
	})

	// 控制类观测必须拒收
	now := time.Now().UTC()
	ctrl := map[string]any{
		"actor": "cursor-goalagents-1", "project_id": projectID,
		"observations": []any{map[string]any{
			"record_ref": "r1", "record_type": "x", "schema_version": 1,
			"kind": "task.status", "occurred_at": now.Format(time.RFC3339Nano),
			"task_status": "done",
		}},
	}
	raw, _ = json.Marshal(ctrl)
	req = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/status-reports", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+f.writeTok)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "idem-status-report-0002")
	rec = httptest.NewRecorder()
	f.api.Handler().ServeHTTP(rec, req)
	body = decodeOK(t, rec)
	if body["rejected"].(float64) < 1 {
		t.Fatalf("控制类观测应拒收: %v", body)
	}
	if body["changed_task_status"] != false {
		t.Fatal("拒收路径仍不得改任务状态")
	}
}

func TestStatusReportIdempotentReplay(t *testing.T) {
	f := newFixture(t)
	payload := map[string]any{
		"actor": "cursor-goalagents-1", "project_id": projectID,
		"message": "hi", "status": "working",
	}
	raw, _ := json.Marshal(payload)
	do := func() map[string]any {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8030/api/v1/status-reports", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+f.writeTok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "idem-status-report-replay")
		rec := httptest.NewRecorder()
		f.api.Handler().ServeHTTP(rec, req)
		return decodeOK(t, rec)
	}
	first := do()
	second := do()
	if second["idempotent_replay"] != true {
		t.Fatalf("同键重放应标记 idempotent_replay: %v / %v", first, second)
	}
}

func TestReconnectContinuesFromCursor(t *testing.T) {
	f := newFixture(t)
	f.seedEvents(t, 5, "agent.progress")
	body := decodeOK(t, f.get(t, "/api/v1/events?project_id="+projectID+"&limit=2"))
	items, _ := body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("page1=%d", len(items))
	}
	cursor, _ := body["next_cursor"].(string)
	if cursor == "" {
		t.Fatal("应有 next_cursor")
	}
	body = decodeOK(t, f.get(t, "/api/v1/events?project_id="+projectID+"&limit=10&cursor="+cursor))
	items, _ = body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("断线重连补拉应得剩余 3 条，得到 %d", len(items))
	}
	if body["has_more"] != false {
		t.Fatal("最后一页 has_more=false")
	}
}

// 确保 MemoryChanges 可被当作 ChangeSource 编译。
var _ httpevents.ChangeSource = (*httpevents.MemoryChanges)(nil)
var _ httpevents.Reporter = (*httpevents.StoreReporter)(nil)

func TestObservationSourceConstant(t *testing.T) {
	if srcEvents.DomainObserved != "observed" {
		t.Fatal("旁路域常量漂移")
	}
}
