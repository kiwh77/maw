package query_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/query"
)

const (
	projectA = "0123456789abcdef"
	projectB = "fedcba9876543210"
	baseline = "0123456789abcdef0123456789abcdef01234567"
)

// fixture 是一个推进到最新迁移的临时状态库。
type fixture struct {
	store *db.Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("打开状态库失败：%v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	return &fixture{store: store}
}

func (f *fixture) write(t *testing.T, fn func(*sql.Tx) error) {
	t.Helper()
	if err := f.store.Write(fn); err != nil {
		t.Fatalf("写库失败：%v", err)
	}
}

func (f *fixture) read(t *testing.T, fn func(*query.Reader) error) {
	t.Helper()
	if err := query.Read(f.store, fn); err != nil {
		t.Fatalf("查询失败：%v", err)
	}
}

// seedTask 用正式写入口建任务；InstanceID 留空时由 db 生成新实例，避免手写身份。
func (f *fixture) seedTask(t *testing.T, projectID, displayID string, status model.TaskStatus, priority, mergeOrder int) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		return db.UpsertTask(tx, &db.Task{
			ProjectID:  projectID,
			ID:         displayID,
			Title:      "任务 " + displayID,
			Status:     string(status),
			Priority:   priority,
			MergeOrder: mergeOrder,
		})
	})
}

// seedInstance 建一个活跃实例并冻结 v1。
func (f *fixture) seedInstance(t *testing.T, projectID, displayID string) model.InstanceID {
	t.Helper()
	var id model.InstanceID
	f.write(t, func(tx *sql.Tx) error {
		inst, err := db.CreateInstance(tx, projectID, displayID, db.InstanceDraft{Title: "任务 " + displayID})
		if err != nil {
			return err
		}
		id = inst.InstanceID
		return nil
	})
	return id
}

// seedNode 登记节点：槽上限决定能否领取，账号额度只是展示字段。
func (f *fixture) seedNode(t *testing.T, nodeID string) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: nodeID, ProjectID: projectA, Label: nodeID, SlotLimit: 8})
	})
}

// seedRun 领取一次执行，返回 Run 身份；工作目录按任务区分，避免目录占用互斥。
func (f *fixture) seedRun(t *testing.T, projectID, displayID string) (model.InstanceID, model.RunID) {
	t.Helper()
	inst := f.seedInstance(t, projectID, displayID)
	var runID model.RunID
	f.write(t, func(tx *sql.Tx) error {
		out, err := db.ClaimRun(tx, db.ClaimSpec{
			ProjectID:      projectID,
			InstanceID:     string(inst),
			Generation:     1,
			NodeID:         "node-a",
			Workspace:      "/tmp/maw-test/" + displayID,
			Coordinator:    "tester",
			IdempotencyKey: "claim-" + displayID,
		})
		if err != nil {
			return err
		}
		runID = model.RunID(out.Run.ID)
		return nil
	})
	return inst, runID
}

func (f *fixture) seedEvent(t *testing.T, projectID, eventID string, at time.Time) {
	t.Helper()
	f.write(t, func(tx *sql.Tx) error {
		_, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
			ID:            model.EventID(eventID),
			ProjectID:     projectID,
			Kind:          "run.phase",
			SchemaVersion: 1,
			OccurredAt:    at,
			ReceivedAt:    at,
		})
		return err
	})
}

// TestWatermarkAndRowsComeFromOneSnapshot 覆盖「一致快照」：
// 一次 Read 内的多次查询看到同一份数据，期间写入不会让水位或结果漂移。
func TestWatermarkAndRowsComeFromOneSnapshot(t *testing.T) {
	f := newFixture(t)
	f.seedTask(t, projectA, "VN-001", model.TaskReady, 10, 10)
	f.seedEvent(t, projectA, "evt_aaa001", time.Now().UTC())

	var first, second query.Page[query.TaskRow]
	var wmFirst, wmSecond query.Watermark
	var revFirst, revSecond int64

	err := query.Read(f.store, func(r *query.Reader) error {
		var err error
		first, err = r.Tasks(query.TaskQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		wmFirst = r.Watermark()
		if revFirst, err = r.ProjectRevision(projectA); err != nil {
			return err
		}

		// 快照打开后再写：新任务与新事件都不该出现在本次读取里。
		if err := f.store.Write(func(tx *sql.Tx) error {
			if err := db.UpsertTask(tx, &db.Task{
				ProjectID: projectA, ID: "VN-002", Title: "后来者",
				Status: string(model.TaskReady), Priority: 10, MergeOrder: 10,
			}); err != nil {
				return err
			}
			_, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
				ID: "evt_aaa002", ProjectID: projectA, Kind: "run.phase",
				SchemaVersion: 1, OccurredAt: time.Now().UTC(), ReceivedAt: time.Now().UTC(),
			})
			return err
		}); err != nil {
			return err
		}

		second, err = r.Tasks(query.TaskQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		wmSecond = r.Watermark()
		revSecond, err = r.ProjectRevision(projectA)
		return err
	})
	if err != nil {
		t.Fatalf("读取失败：%v", err)
	}

	if len(first.Items) != 1 || len(second.Items) != 1 {
		t.Fatalf("同一快照内两次查询应看到 1 条任务，得到 %d 与 %d", len(first.Items), len(second.Items))
	}
	if wmFirst != wmSecond {
		t.Fatalf("同一次读取的水位不得变化：%+v → %+v", wmFirst, wmSecond)
	}
	if revFirst != revSecond {
		t.Fatalf("工程版本不得变化：%d → %d", revFirst, revSecond)
	}
	if wmFirst.Cursor != 1 || wmFirst.SchemaVersion == 0 {
		t.Fatalf("水位应反映快照：cursor=%d schema=%d", wmFirst.Cursor, wmFirst.SchemaVersion)
	}
}

// TestTasksOver200RowsPageWithoutGapsOrDuplicates 覆盖「大于 200 条」：
// 页大小上限 200，翻页用游标而不是 offset，不漏不重。
func TestTasksOver200RowsPageWithoutGapsOrDuplicates(t *testing.T) {
	f := newFixture(t)
	const total = 250
	f.write(t, func(tx *sql.Tx) error {
		for i := 1; i <= total; i++ {
			task := &db.Task{
				ProjectID:  projectA,
				ID:         fmt.Sprintf("VN-%03d", i),
				Title:      "批量任务",
				Status:     string(model.TaskReady),
				Priority:   i % 7,
				MergeOrder: i % 5,
			}
			if err := db.UpsertTask(tx, task); err != nil {
				return err
			}
		}
		return nil
	})

	seen := map[string]bool{}
	cursor := ""
	pages := 0
	for {
		var page query.Page[query.TaskRow]
		f.read(t, func(r *query.Reader) error {
			var err error
			page, err = r.Tasks(query.TaskQuery{ProjectID: projectA, Limit: query.MaxLimit, Cursor: cursor})
			return err
		})
		pages++
		for _, item := range page.Items {
			key := string(item.InstanceID)
			if seen[key] {
				t.Fatalf("翻页出现重复记录：%s", key)
			}
			if item.DisplayID == "" || item.InstanceID == "" {
				t.Fatalf("列表必须保留稳定身份：%+v", item)
			}
			seen[key] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
		if pages > 10 {
			t.Fatal("翻页没有终止")
		}
	}
	if len(seen) != total {
		t.Fatalf("应翻出 %d 条，实际 %d 条（页数 %d）", total, len(seen), pages)
	}
	if pages != 2 {
		t.Fatalf("250 条按 200 一页应正好 2 页，实际 %d 页", pages)
	}
}

// TestFilteredPageIsNeverEmptyMidway 覆盖「过滤空页」：
// 过滤下推到 SQL，所以第一页就有匹配记录，不会先返回一个空页再让客户端停下。
func TestFilteredPageIsNeverEmptyMidway(t *testing.T) {
	f := newFixture(t)
	f.write(t, func(tx *sql.Tx) error {
		for i := 1; i <= 300; i++ {
			status := model.TaskReady
			if i > 240 {
				status = model.TaskBlocked
			}
			task := &db.Task{
				ProjectID: projectA, ID: fmt.Sprintf("VN-%03d", i), Title: "过滤用例",
				Status: string(status), Priority: i, MergeOrder: i,
			}
			if err := db.UpsertTask(tx, task); err != nil {
				return err
			}
		}
		return nil
	})

	cursor := ""
	got := 0
	for {
		var page query.Page[query.TaskRow]
		f.read(t, func(r *query.Reader) error {
			var err error
			page, err = r.Tasks(query.TaskQuery{
				ProjectID: projectA, Status: model.TaskBlocked, Limit: 50, Cursor: cursor,
			})
			return err
		})
		if len(page.Items) == 0 && page.Next != "" {
			t.Fatal("中间页不得为空：空页会让客户端提前结束，后面的匹配记录再也拿不到")
		}
		got += len(page.Items)
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if got != 60 {
		t.Fatalf("blocked 任务共 60 条，实际分页拿到 %d 条", got)
	}

	// 真的没有匹配时：一页空结果且没有下一页，这才是「空」的正确含义。
	f.read(t, func(r *query.Reader) error {
		page, err := r.Tasks(query.TaskQuery{ProjectID: projectA, Status: model.TaskCancelled})
		if err != nil {
			return err
		}
		if len(page.Items) != 0 || page.Next != "" {
			t.Fatalf("无匹配应为空页且无下一页：%d / %q", len(page.Items), page.Next)
		}
		return nil
	})
}

// TestSameNameHistoryKeepsEveryInstance 覆盖「同名历史」：
// 同一 display_id 有多代实例，且创建时间精度相同，全靠 instance_id 兜底才不会漏。
func TestSameNameHistoryKeepsEveryInstance(t *testing.T) {
	f := newFixture(t)
	const total = 250
	f.write(t, func(tx *sql.Tx) error {
		for i := 0; i < total; i++ {
			inst, err := db.CreateInstance(tx, projectB, "VN-900", db.InstanceDraft{Title: "同名历史"})
			if err != nil {
				return err
			}
			// 立刻退役，让下一轮能重建同编号实例（E120 的合法绕法就是显式退役）。
			if err := db.RetireInstance(tx, string(inst.InstanceID), db.RetireReset); err != nil {
				return err
			}
		}
		return nil
	})

	seen := map[string]bool{}
	cursor := ""
	for {
		var page query.Page[query.InstanceRow]
		f.read(t, func(r *query.Reader) error {
			var err error
			page, err = r.Instances(query.InstanceQuery{
				ProjectID: projectB, DisplayID: "VN-900", Limit: 100, Cursor: cursor,
			})
			return err
		})
		for _, item := range page.Items {
			key := string(item.InstanceID)
			if seen[key] {
				t.Fatalf("同名历史出现重复实例：%s", key)
			}
			seen[key] = true
		}
		if page.Next == "" {
			break
		}
		cursor = page.Next
	}
	if len(seen) != total {
		t.Fatalf("同名实例共 %d 代，实际只翻出 %d 代", total, len(seen))
	}
}

// TestCursorAndLimitValidation 覆盖分页参数：越界要报错，不静默截断、不静默重来。
func TestCursorAndLimitValidation(t *testing.T) {
	f := newFixture(t)
	f.seedTask(t, projectA, "VN-001", model.TaskReady, 10, 10)

	cases := []struct {
		name   string
		q      query.TaskQuery
		expect string
	}{
		{"limit 越上界", query.TaskQuery{Limit: query.MaxLimit + 1}, query.CodeInvalidPagination},
		{"limit 为负", query.TaskQuery{Limit: -1}, query.CodeInvalidPagination},
		{"游标乱码", query.TaskQuery{Cursor: "not-a-cursor"}, query.CodeInvalidPagination},
		{"未知状态", query.TaskQuery{Status: "donee"}, query.CodeUnknownEnum},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := query.Read(f.store, func(r *query.Reader) error {
				_, err := r.Tasks(c.q)
				return err
			})
			if !query.IsCode(err, c.expect) {
				t.Fatalf("期望 %s，得到 %v", c.expect, err)
			}
		})
	}

	// 游标不能跨资源类型复用：排序键不同，静默接受就会错位翻页。
	bad := query.EventCursor(1)
	err := query.Read(f.store, func(r *query.Reader) error {
		_, err := r.Tasks(query.TaskQuery{ProjectID: projectA, Cursor: bad})
		return err
	})
	if !query.IsCode(err, query.CodeInvalidPagination) {
		t.Fatalf("事件游标用在任务列表上应被拒绝，得到 %v", err)
	}
}

// TestRunsOperationsNodesDeliveries 覆盖其余四类资源：稳定身份、一致水位与排序。
func TestRunsOperationsNodesDeliveries(t *testing.T) {
	f := newFixture(t)
	f.seedNode(t, "node-a")

	instances := make([]model.InstanceID, 0, 3)
	runs := make([]model.RunID, 0, 3)
	for i := 1; i <= 3; i++ {
		inst, run := f.seedRun(t, projectA, fmt.Sprintf("VN-%03d", i))
		instances = append(instances, inst)
		runs = append(runs, run)
	}

	// 交付必须追到确定的 Run 与版本，所以用领取出来的 Run 落三条交付。
	f.write(t, func(tx *sql.Tx) error {
		for i, run := range runs {
			d := model.Delivery{
				DeliveryID:     model.DeliveryID(fmt.Sprintf("dlv_aaa%03d", i)),
				TaskInstance:   instances[i],
				TaskVersion:    1,
				RunID:          run,
				Mode:           model.DeliveryCode,
				BaselineCommit: model.Commit(baseline),
				DeliveredAt:    time.Date(2026, 10, 23, 3, i, 0, 0, time.UTC),
				Artifacts: []model.Artifact{{
					ArtifactID: "art_aaa001", Kind: model.ArtifactFile,
					Ref: "internal/query/doc.go", DeclaredBy: model.DeclaredByCLI,
					Exists: true, InWriteScope: true,
				}},
			}
			if _, _, err := db.SaveDelivery(tx, projectA, d, fmt.Sprintf("dlv-key-%d", i)); err != nil {
				return err
			}
		}
		return nil
	})

	f.read(t, func(r *query.Reader) error {
		runPage, err := r.Runs(query.RunQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		if len(runPage.Items) != 3 {
			t.Fatalf("应有 3 个 Run，得到 %d", len(runPage.Items))
		}
		for _, run := range runPage.Items {
			if run.RunID == "" || run.InstanceID == "" {
				t.Fatalf("Run 列表必须保留稳定身份：%+v", run)
			}
			if !run.Occupies || run.Phase != model.PhaseReserved {
				t.Fatalf("刚领取的 Run 应仍在占用：%+v", run)
			}
		}

		opPage, err := r.Operations(query.OperationQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		if len(opPage.Items) != 3 {
			t.Fatalf("应有 3 个派发操作，得到 %d", len(opPage.Items))
		}
		for _, op := range opPage.Items {
			if op.Kind != model.OpDispatch || op.Result != model.ResultPending {
				t.Fatalf("领取应留下 pending 的派发操作：%+v", op)
			}
		}

		nodePage, err := r.Nodes(query.NodeQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		if len(nodePage.Items) != 1 || nodePage.Items[0].NodeID != "node-a" {
			t.Fatalf("节点查询应返回 node-a：%+v", nodePage.Items)
		}

		dlvPage, err := r.Deliveries(query.DeliveryQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		if len(dlvPage.Items) != 3 {
			t.Fatalf("应有 3 条交付，得到 %d", len(dlvPage.Items))
		}
		if dlvPage.Items[0].Fingerprint == "" || dlvPage.Items[0].BaselineCommit != baseline {
			t.Fatalf("交付行必须带指纹与基线：%+v", dlvPage.Items[0])
		}
		// 每次领取都会写一条 run.phase 事件，所以水位至少覆盖这几条；
		// 它必须与本次快照一致，而不是「读到哪算哪」。
		if int(dlvPage.Watermark.Cursor) != len(runs) {
			t.Fatalf("水位应为领取事件数 %d，得到 %d", len(runs), dlvPage.Watermark.Cursor)
		}
		if dlvPage.Watermark.SchemaVersion == 0 {
			t.Fatal("水位应带 schema 版本，便于发现读到了旧库")
		}
		return nil
	})
}

// TestEventsPageByCursorOnly 覆盖事件历史的增量拉取：按入库游标严格递增，不漏不重。
func TestEventsPageByCursorOnly(t *testing.T) {
	f := newFixture(t)
	const total = 250
	base := time.Date(2026, 10, 23, 4, 0, 0, 0, time.UTC)
	f.write(t, func(tx *sql.Tx) error {
		for i := 0; i < total; i++ {
			if _, _, err := db.AppendRunEvent(tx, db.RunEventSpec{
				ID:            model.EventID(fmt.Sprintf("evt_evt%03d", i)),
				ProjectID:     projectA,
				Kind:          "run.phase",
				SchemaVersion: 1,
				OccurredAt:    base.Add(time.Duration(i) * time.Second),
				ReceivedAt:    base.Add(time.Duration(i) * time.Second),
			}); err != nil {
				return err
			}
		}
		return nil
	})

	cursor := ""
	seen := map[int64]bool{}
	var last int64
	pages := 0
	for {
		var page query.Page[query.EventRow]
		f.read(t, func(r *query.Reader) error {
			var err error
			page, err = r.Events(query.EventQuery{ProjectID: projectA, Limit: 100, Cursor: cursor})
			return err
		})
		pages++
		for _, ev := range page.Items {
			if seen[ev.Cursor] {
				t.Fatalf("事件游标重复：%d", ev.Cursor)
			}
			if ev.Cursor <= last {
				t.Fatalf("事件必须按入库游标严格递增：%d 之后出现 %d", last, ev.Cursor)
			}
			seen[ev.Cursor] = true
			last = ev.Cursor
		}
		if page.Next == "" {
			if page.Watermark.Cursor != last {
				t.Fatalf("水位应等于已见最大游标 %d，实际 %d", last, page.Watermark.Cursor)
			}
			break
		}
		cursor = page.Next
		if pages > 10 {
			t.Fatal("事件翻页没有终止")
		}
	}
	if len(seen) != total {
		t.Fatalf("应翻出 %d 条事件，实际 %d 条", total, len(seen))
	}

	// 从最后一条之后继续拉：一页空结果，游标不倒退。
	f.read(t, func(r *query.Reader) error {
		page, err := r.Events(query.EventQuery{ProjectID: projectA, Cursor: query.EventCursor(last)})
		if err != nil {
			return err
		}
		if len(page.Items) != 0 || page.Next != "" {
			t.Fatalf("已经读到末尾，不该再有数据：%d / %q", len(page.Items), page.Next)
		}
		if page.Watermark.Cursor != last {
			t.Fatalf("水位不得倒退：%d vs %d", page.Watermark.Cursor, last)
		}
		return nil
	})

	// 类型过滤同样下推到 SQL：筛掉的类型不占页名额。
	f.read(t, func(r *query.Reader) error {
		page, err := r.Events(query.EventQuery{ProjectID: projectA, Kinds: []string{"lease.released"}, Limit: 10})
		if err != nil {
			return err
		}
		if len(page.Items) != 0 || page.Next != "" {
			t.Fatalf("没有该类型事件时应为空页：%d / %q", len(page.Items), page.Next)
		}
		return nil
	})
}

// TestLimitDefaultsAndNodeOrdering 顺带固定两条小规则：默认页大小与节点排序。
func TestLimitDefaultsAndNodeOrdering(t *testing.T) {
	f := newFixture(t)
	f.write(t, func(tx *sql.Tx) error {
		// 60 条任务：不传 limit 时应返回 50 条并给出下一页，而不是一次全给。
		for i := 1; i <= 60; i++ {
			if err := db.UpsertTask(tx, &db.Task{
				ProjectID: projectA, ID: fmt.Sprintf("VN-%03d", i), Title: "默认页长",
				Status: string(model.TaskReady), Priority: i, MergeOrder: i,
			}); err != nil {
				return err
			}
		}
		for _, id := range []string{"node-c", "node-a", "node-b"} {
			if err := db.UpsertNode(tx, &db.Node{ID: id, SlotLimit: 2}); err != nil {
				return err
			}
		}
		return nil
	})
	f.read(t, func(r *query.Reader) error {
		page, err := r.Nodes(query.NodeQuery{})
		if err != nil {
			return err
		}
		want := []string{"node-a", "node-b", "node-c"}
		if len(page.Items) != len(want) {
			t.Fatalf("应返回 3 个节点，得到 %d", len(page.Items))
		}
		for i, id := range want {
			if string(page.Items[i].NodeID) != id {
				t.Fatalf("节点应按 node_id 升序：第 %d 个是 %s，期望 %s",
					i, page.Items[i].NodeID, id)
			}
		}
		return nil
	})
	f.read(t, func(r *query.Reader) error {
		page, err := r.Tasks(query.TaskQuery{ProjectID: projectA})
		if err != nil {
			return err
		}
		if len(page.Items) != query.DefaultLimit {
			t.Fatalf("省略 limit 应返回 %d 条，得到 %d", query.DefaultLimit, len(page.Items))
		}
		if page.Next == "" {
			t.Fatal("还有 10 条未读，必须给出下一页游标")
		}
		return nil
	})
	if query.DefaultLimit != 50 || query.MaxLimit != 200 {
		t.Fatalf("页大小规则必须与契约一致：default=%d max=%d",
			query.DefaultLimit, query.MaxLimit)
	}
}
