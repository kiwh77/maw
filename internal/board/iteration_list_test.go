package board

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

func snapshotIterations() IterationList {
	return IterationList{
		{ProjectID: "p1", ID: "IT-001", Name: "第一波", Status: db.IterationRunning},
		{ProjectID: "p1", ID: "IT-002", Name: "第二波", Status: db.IterationPlanning},
	}
}

// 快照必须始终带 iterations 数组：顶栏下拉靠它出选项，前端不该再分辨 null 与空集合。
func TestSnapshotIterationsAlwaysArray(t *testing.T) {
	raw, err := json.Marshal(BuildSnapshot(nil, nil, 4))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"iterations":[]`) {
		t.Fatalf("空快照也必须输出 iterations 数组: %s", raw)
	}
}

func TestSnapshotIterationsListed(t *testing.T) {
	s := BuildSnapshot(iterationTasks(), nil, 4)
	s.Iterations = snapshotIterations()
	s.Normalize()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	list, ok := body["iterations"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("iterations 未输出成数组: %v", body["iterations"])
	}
	first, ok := list[0].(map[string]any)
	if !ok || first["id"] != "IT-001" || first["name"] != "第一波" {
		t.Fatalf("迭代字段缺失: %v", list[0])
	}
}

// 按迭代筛任务时，迭代清单必须保留：筛完只剩一个迭代，下拉不能因此清空。
func TestSnapshotWithIterationKeepsIterationList(t *testing.T) {
	s := BuildSnapshot(iterationTasks(), nil, 4)
	s.Iterations = snapshotIterations()
	out := s.WithIteration("IT-001")
	if len(out.Iterations) != 2 {
		t.Fatalf("筛选后迭代清单被清掉: %+v", out.Iterations)
	}
	if got, ok := out.IterationFilter(); !ok || got != "IT-001" {
		t.Fatalf("filter_iteration=%q ok=%v", got, ok)
	}
}

func TestAPIBoardCarriesIterations(t *testing.T) {
	load := func(_, _, _ string) (Snapshot, error) {
		s := BuildSnapshot(iterationTasks(), nil, 4)
		s.Iterations = snapshotIterations()
		return s, nil
	}
	rr := httptest.NewRecorder()
	newHandler(load, nil).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/board", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	list, ok := body["iterations"].([]any)
	if !ok || len(list) != 2 {
		t.Fatalf("/api/board 必须带 iterations 数组: %v", body["iterations"])
	}
	second, _ := list[1].(map[string]any)
	if second["id"] != "IT-002" {
		t.Fatalf("迭代顺序应保持列表顺序: %v", list)
	}
}
