package db

import (
	"database/sql"
	"testing"
)

// 空工程 id 表示「全部工程」：看板顶部下拉与全局页面需要一次列全，
// 不能因为没选工程就退化成空列表。
func TestListIterationsEmptyProjectListsAll(t *testing.T) {
	path := openTestDB(t)
	err := WithTx(path, func(tx *sql.Tx) error {
		for _, it := range []*Iteration{
			{ProjectID: "p1", ID: "IT-001", Name: "甲"},
			{ProjectID: "p2", ID: "IT-001", Name: "乙"},
			{ProjectID: "p2", ID: "IT-002", Name: "丙"},
		} {
			if err := UpsertIteration(tx, it); err != nil {
				return err
			}
		}

		all, err := ListIterations(tx, "")
		if err != nil {
			return err
		}
		if len(all) != 3 {
			t.Fatalf("空工程 id 应列出全部迭代，得到 %d", len(all))
		}
		// 顺序固定为工程 → 创建时间 → id，调用方不必自己再排。
		want := [][2]string{{"p1", "IT-001"}, {"p2", "IT-001"}, {"p2", "IT-002"}}
		for i, pair := range want {
			if all[i].ProjectID != pair[0] || all[i].ID != pair[1] {
				t.Fatalf("第 %d 条=%s/%s want %s/%s", i, all[i].ProjectID, all[i].ID, pair[0], pair[1])
			}
		}

		only, err := ListIterations(tx, "p2")
		if err != nil {
			return err
		}
		if len(only) != 2 || only[0].ProjectID != "p2" || only[1].ProjectID != "p2" {
			t.Fatalf("指定工程时不该混入其他工程: %+v", only)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
