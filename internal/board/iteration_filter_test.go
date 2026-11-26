package board

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

// iterationTasks 造一批横跨两个迭代与「未归属」的任务，供筛选断言共用。
func iterationTasks() []*db.Task {
	return []*db.Task{
		{ID: "T-1", Status: "done", Title: "第一波", IterationID: "IT-001"},
		{ID: "T-2", Status: "ready", Title: "第一波遗留", IterationID: "IT-001"},
		{ID: "T-3", Status: "ready", Title: "第二波", IterationID: "IT-002"},
		{ID: "T-4", Status: "blocked", Title: "未归属", BlockedReason: "wait"},
	}
}

func taskIDs(tasks []*db.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func TestSnapshotWithIterationFiltersTasks(t *testing.T) {
	s := BuildSnapshot(iterationTasks(), nil, 4).WithIteration("IT-001")
	if got := strings.Join(taskIDs(s.Tasks), ","); got != "T-1,T-2" {
		t.Fatalf("tasks=%s", got)
	}
	// 聚合必须跟着筛后的任务集重算，否则卡片上的进度与列表对不上。
	if s.Total != 2 || s.Done != 1 || s.Active != 0 || s.Blocked != 0 || s.Percent != 50 {
		t.Fatalf("total=%d done=%d active=%d blocked=%d percent=%d",
			s.Total, s.Done, s.Active, s.Blocked, s.Percent)
	}
	if s.Counts["ready"] != 1 || s.Counts["done"] != 1 || len(s.Counts) != 2 {
		t.Fatalf("counts=%v", s.Counts)
	}
	if len(s.Risks) != 0 {
		t.Fatalf("阻塞任务不在该迭代，risks 不该带出来: %v", s.Risks)
	}
	got, ok := s.IterationFilter()
	if !ok || got != "IT-001" {
		t.Fatalf("filter_iteration=%v ok=%v", got, ok)
	}
}

func TestSnapshotWithIterationEmptySelectsUnassigned(t *testing.T) {
	// 空迭代是合法筛选值：只留没归属任何迭代的任务，而不是「不筛」。
	s := BuildSnapshot(iterationTasks(), nil, 4).WithIteration("")
	if got := strings.Join(taskIDs(s.Tasks), ","); got != "T-4" {
		t.Fatalf("tasks=%s", got)
	}
	if s.Total != 1 || s.Blocked != 1 || s.Percent != 0 {
		t.Fatalf("total=%d blocked=%d percent=%d", s.Total, s.Blocked, s.Percent)
	}
	value, ok := s.IterationFilter()
	if !ok || value != "" {
		t.Fatalf("空迭代筛选必须可回显: value=%q ok=%v", value, ok)
	}
}

func TestSnapshotWithoutIterationFilterKeepsAll(t *testing.T) {
	s := BuildSnapshot(iterationTasks(), nil, 4)
	if len(s.Tasks) != 4 || s.Total != 4 {
		t.Fatalf("tasks=%d total=%d", len(s.Tasks), s.Total)
	}
	if _, ok := s.IterationFilter(); ok {
		t.Fatal("未按迭代筛时 filter_iteration 必须是 nil")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"filter_iteration":null`) {
		t.Fatalf("空筛选应编码成 null: %s", raw)
	}
}

func TestSnapshotTasksExposeIterationID(t *testing.T) {
	// 看板任务对象（*db.Task）必须暴露 iteration_id，未归属时是空串而不是缺字段。
	raw, err := json.Marshal(BuildSnapshot(iterationTasks(), nil, 4).Tasks)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"iteration_id":"IT-001"`) {
		t.Fatalf("缺 iteration_id: %s", body)
	}
	if !strings.Contains(body, `"iteration_id":""`) {
		t.Fatalf("未归属任务也要带空的 iteration_id: %s", body)
	}
}

func apiBoardFilterBody(t *testing.T, query string) map[string]any {
	t.Helper()
	load := func(_, _, _ string) (Snapshot, error) {
		return BuildSnapshot(iterationTasks(), nil, 4), nil
	}
	rr := httptest.NewRecorder()
	newHandler(load, nil).ServeHTTP(rr, httptest.NewRequest("GET", "/api/board"+query, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestAPIBoardFilterByIteration(t *testing.T) {
	cases := []struct {
		query     string
		wantTotal float64
		wantIter  any
	}{
		{"", 4, nil},
		{"?iteration=IT-002", 1, "IT-002"},
		// 参数出现但为空＝只要未归属任务，与「不传参数」区分开。
		{"?iteration=", 1, ""},
	}
	for _, c := range cases {
		body := apiBoardFilterBody(t, c.query)
		if body["total"] != c.wantTotal {
			t.Fatalf("%q total=%v want %v", c.query, body["total"], c.wantTotal)
		}
		if body["filter_iteration"] != c.wantIter {
			t.Fatalf("%q filter_iteration=%v want %v", c.query, body["filter_iteration"], c.wantIter)
		}
		tasks, _ := body["tasks"].([]any)
		if float64(len(tasks)) != c.wantTotal {
			t.Fatalf("%q tasks=%d", c.query, len(tasks))
		}
		for _, item := range tasks {
			task := item.(map[string]any)
			if _, ok := task["iteration_id"]; !ok {
				t.Fatalf("%q 任务缺 iteration_id: %v", c.query, task)
			}
			if c.wantIter != nil && task["iteration_id"] != c.wantIter {
				t.Fatalf("%q 混入了其他迭代的任务: %v", c.query, task["iteration_id"])
			}
		}
	}
}
