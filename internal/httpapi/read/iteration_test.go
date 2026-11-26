package read_test

import (
	"database/sql"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// seedIterationTask 造一条带迭代归属的任务；iteration 为空表示「未归属」。
func (f *fixture) seedIterationTask(t *testing.T, display string, priority int, iteration string) string {
	t.Helper()
	var instanceID string
	f.write(t, func(tx *sql.Tx) error {
		task := &db.Task{
			ProjectID: projectID, ID: display, Title: "题 " + display,
			Status: string(model.TaskReady), Priority: priority, MergeOrder: priority,
			IterationID: iteration,
		}
		if err := db.UpsertTask(tx, task); err != nil {
			return err
		}
		instanceID = task.InstanceID
		return nil
	})
	return instanceID
}

func (f *fixture) seedIterations(t *testing.T, ids ...string) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		for _, id := range ids {
			if err := db.UpsertIteration(tx, &db.Iteration{
				ProjectID: projectID, ID: id, Name: "迭代 " + id, TargetBranch: "main",
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

func iterationFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.seedIterations(t, "IT-001", "IT-002")
	f.seedIterationTask(t, "VN-001", 10, "IT-001")
	f.seedIterationTask(t, "VN-002", 20, "IT-001")
	f.seedIterationTask(t, "VN-003", 30, "IT-002")
	f.seedIterationTask(t, "VN-004", 40, "")
	return f
}

func itemsOf(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	raw, ok := body["items"].([]any)
	if !ok {
		t.Fatalf("items 不是数组: %v", body["items"])
	}
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		out = append(out, item.(map[string]any))
	}
	return out
}

func TestTaskListFilterByIteration(t *testing.T) {
	f := iterationFixture(t)
	cases := []struct {
		query    string
		want     []string
		wantIter any
	}{
		{"", []string{"VN-001", "VN-002", "VN-003", "VN-004"}, nil},
		{"?iteration=IT-001", []string{"VN-001", "VN-002"}, "IT-001"},
		{"?iteration_id=IT-002", []string{"VN-003"}, "IT-002"},
		// 参数出现但为空＝只要未归属任务，与「不传参数」区分开。
		{"?iteration=", []string{"VN-004"}, ""},
	}
	for _, c := range cases {
		body := decodeOK(t, f.get(t, "/api/v1/projects/"+projectID+"/tasks"+c.query))
		items := itemsOf(t, body)
		if len(items) != len(c.want) {
			t.Fatalf("%q items=%d want %d", c.query, len(items), len(c.want))
		}
		for i, item := range items {
			if item["display_id"] != c.want[i] {
				t.Fatalf("%q 第 %d 行=%v want %s", c.query, i, item["display_id"], c.want[i])
			}
			// 列表字段必须暴露迭代归属（未归属是空串，而不是缺字段）。
			iter, ok := item["iteration_id"]
			if !ok {
				t.Fatalf("%q 任务行缺 iteration_id: %v", c.query, item)
			}
			if c.wantIter != nil && iter != c.wantIter {
				t.Fatalf("%q 混入了其他迭代的任务: %v", c.query, iter)
			}
		}
		if body["filter_iteration"] != c.wantIter {
			t.Fatalf("%q filter_iteration=%v want %v", c.query, body["filter_iteration"], c.wantIter)
		}
	}
}

func TestBoardFilterByIteration(t *testing.T) {
	f := iterationFixture(t)
	body := decodeOK(t, f.get(t, "/api/v1/board?iteration=IT-002"))
	items := itemsOf(t, body)
	if len(items) != 1 || items[0]["display_id"] != "VN-003" {
		t.Fatalf("items=%v", items)
	}
	if items[0]["iteration_id"] != "IT-002" {
		t.Fatalf("iteration_id=%v", items[0]["iteration_id"])
	}

	// 不筛迭代时全量返回，且每行都带 iteration_id。
	all := itemsOf(t, decodeOK(t, f.get(t, "/api/v1/board")))
	if len(all) != 4 {
		t.Fatalf("全量 items=%d", len(all))
	}
	for _, item := range all {
		if _, ok := item["iteration_id"]; !ok {
			t.Fatalf("任务行缺 iteration_id: %v", item)
		}
	}
}

func TestTaskDetailExposesIterationID(t *testing.T) {
	f := iterationFixture(t)
	instanceID := f.seedIterationTask(t, "VN-005", 50, "IT-001")
	body := decodeOK(t, f.get(t, "/api/v1/tasks/"+instanceID))
	task, ok := body["task"].(map[string]any)
	if !ok {
		t.Fatalf("task=%v", body["task"])
	}
	if task["iteration_id"] != "IT-001" {
		t.Fatalf("iteration_id=%v", task["iteration_id"])
	}
}
