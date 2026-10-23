package claim

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/engine/ports"
)

const (
	testProject = "0123456789abcdef"
	testAgent   = model.AgentID("worker-1")
)

type seedOpts struct {
	status     model.TaskStatus
	priority   int
	deps       []string
	writeScope []string
	owner      string
}

func newStore(t *testing.T) *db.Store {
	t.Helper()
	st, err := db.OpenStore(filepath.Join(t.TempDir(), "maw.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func seedTask(t *testing.T, st *db.Store, id string, opts seedOpts) model.InstanceID {
	t.Helper()
	if opts.status == "" {
		opts.status = model.TaskReady
	}
	var instID model.InstanceID
	err := st.Write(func(tx *sql.Tx) error {
		inst, err := db.CreateInstance(tx, testProject, id, db.InstanceDraft{Title: id})
		if err != nil {
			return err
		}
		instID = inst.InstanceID
		return db.UpsertTask(tx, &db.Task{
			ProjectID:  testProject,
			ID:         id,
			InstanceID: string(inst.InstanceID),
			Title:      id,
			Status:     string(opts.status),
			Owner:      opts.owner,
			Priority:   opts.priority,
			MergeOrder: opts.priority,
			Deps:       opts.deps,
			WriteScope: opts.writeScope,
			Revision:   1,
		})
	})
	if err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
	return instID
}

func registerNode(t *testing.T, st *db.Store, id string, slots int) model.NodeID {
	t.Helper()
	if id == "" {
		return ""
	}
	err := st.Write(func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{ID: id, ProjectID: testProject, SlotLimit: slots})
	})
	if err != nil {
		t.Fatalf("register node: %v", err)
	}
	return model.NodeID(id)
}

func occupyNodeSlot(t *testing.T, st *db.Store, node model.NodeID) {
	t.Helper()
	err := st.Write(func(tx *sql.Tx) error {
		_, err := db.AcquireNodeSlot(tx, db.LeaseSpec{
			ProjectID:  testProject,
			InstanceID: "ti_other000001",
			Generation: 1,
		}, string(node))
		return err
	})
	if err != nil {
		t.Fatalf("occupy node slot: %v", err)
	}
}

func declareShared(t *testing.T, st *db.Store, path, owner string) {
	t.Helper()
	err := st.Write(func(tx *sql.Tx) error {
		return db.UpsertSharedFile(tx, testProject, path, owner)
	})
	if err != nil {
		t.Fatalf("declare shared: %v", err)
	}
}

func getTask(t *testing.T, st *db.Store, id string) *db.Task {
	t.Helper()
	var task *db.Task
	err := st.Read(func(tx *sql.Tx) error {
		got, err := db.GetTask(tx, testProject, id)
		task = got
		return err
	})
	if err != nil {
		t.Fatalf("get task %s: %v", id, err)
	}
	return task
}

func count(t *testing.T, st *db.Store, query string, args ...any) int {
	t.Helper()
	var n int
	err := st.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(query, args...).Scan(&n)
	})
	if err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func activeLeases(t *testing.T, st *db.Store) int {
	t.Helper()
	return count(t, st, `SELECT COUNT(*) FROM resource_leases WHERE released_at IS NULL`)
}

func runCount(t *testing.T, st *db.Store) int {
	t.Helper()
	return count(t, st, `SELECT COUNT(*) FROM runs`)
}

func operationCount(t *testing.T, st *db.Store) int {
	t.Helper()
	return count(t, st, `SELECT COUNT(*) FROM operations`)
}

func eventCount(t *testing.T, st *db.Store, typ string) int {
	t.Helper()
	return count(t, st, `SELECT COUNT(*) FROM events WHERE type = ? AND project_id = ?`, typ, testProject)
}

func workspace(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	// 目录不必真实存在：占用只按规范化绝对路径记账。
	return dir
}

func request(inst model.InstanceID, key string, ws string) Request {
	requested := inst
	return Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID:      model.ProjectID(testProject),
			AgentID:        testAgent,
			RequestedTask:  &requested,
			IdempotencyKey: key,
		},
		Workspace: ws,
	}
}

func expectPort(t *testing.T, err error, code model.PortErrorCode) {
	t.Helper()
	var me *model.Error
	if !errors.As(err, &me) {
		t.Fatalf("期望端口错误 %s，得到 %v", code, err)
	}
	if me.Port != code {
		t.Fatalf("期望端口错误 %s，得到 %s（%s）", code, me.Port, me.Msg)
	}
}

// 指定 A 时即使 A 不满足领取条件，也绝不改领 B。
func TestSpecifiedTaskDoesNotFallBack(t *testing.T) {
	st := newStore(t)
	registerNode(t, st, "node-a", 4)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})
	seedTask(t, st, "T-B", seedOpts{priority: 20, writeScope: []string{"b/**"}})
	svc := New(st, nil)

	req := request(a, "key-a", workspace(t, "ws-a"))
	wrong := model.Revision(99)
	req.ExpectedRevision = &wrong
	if _, err := svc.Claim(context.Background(), req); err == nil {
		t.Fatal("revision 不符时不应领取成功")
	} else {
		expectPort(t, err, model.PortErrRevisionMismatch)
	}
	if got := getTask(t, st, "T-A"); got.Status != string(model.TaskReady) || got.Revision != 1 {
		t.Fatalf("A 不应被改动：status=%s revision=%d", got.Status, got.Revision)
	}
	if got := getTask(t, st, "T-B"); got.Status != string(model.TaskReady) {
		t.Fatalf("B 不应被顺手领取：status=%s", got.Status)
	}
	if got := runCount(t, st); got != 0 {
		t.Fatalf("失败领取不应留下 Run，得到 %d", got)
	}
	if got := activeLeases(t, st); got != 0 {
		t.Fatalf("失败领取不应留下占用，得到 %d", got)
	}

	good := request(a, "key-a-ok", workspace(t, "ws-a2"))
	good.NodeID = "node-a"
	out, err := svc.Claim(context.Background(), good)
	if err != nil {
		t.Fatalf("指定 A 应成功：%v", err)
	}
	if out.TaskID != "T-A" || out.RunID == "" || out.Replay {
		t.Fatalf("应领取 A 并建 Run：%+v", out)
	}
	if got := getTask(t, st, "T-A"); got.Status != string(model.TaskClaimed) || got.Owner != string(testAgent) {
		t.Fatalf("A 应进入 claimed 并归本人：status=%s owner=%s", got.Status, got.Owner)
	}
	if got := getTask(t, st, "T-B"); got.Status != string(model.TaskReady) {
		t.Fatalf("指定 A 不应改领 B：status=%s", got.Status)
	}
}

// 指定任务被依赖挡住时报 conflict，不会退回自动选题。
func TestSpecifiedBlockedDoesNotFallBack(t *testing.T) {
	st := newStore(t)
	registerNode(t, st, "node-a", 4)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, deps: []string{"T-B"}, writeScope: []string{"a/**"}})
	b := seedTask(t, st, "T-B", seedOpts{priority: 1, writeScope: []string{"b/**"}})
	svc := New(st, nil)

	req := request(a, "key-a", workspace(t, "ws-a"))
	req.NodeID = "node-a"
	if _, err := svc.Claim(context.Background(), req); err == nil {
		t.Fatal("依赖未完成时不应领取成功")
	} else {
		expectPort(t, err, model.PortErrConflict)
	}
	if got := getTask(t, st, "T-B"); got.Status != string(model.TaskReady) || got.Owner != "" {
		t.Fatalf("被依赖的 B 不应被顺手领取：%+v", got)
	}
	if got := runCount(t, st); got != 0 {
		t.Fatalf("失败领取不应留下 Run，得到 %d", got)
	}

	// 不指定任务时才允许按调度规则改领 B。
	auto := Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(testProject), AgentID: testAgent, IdempotencyKey: "key-auto",
		},
		NodeID: "node-a", Workspace: workspace(t, "ws-b"),
	}
	out, err := svc.Claim(context.Background(), auto)
	if err != nil {
		t.Fatalf("自动选题应领到 B：%v", err)
	}
	if out.TaskID != "T-B" || out.Instance != b {
		t.Fatalf("自动选题应领取 B，得到 %s", out.TaskID)
	}
}

// 写盘冲突与共享文件在同一事务内检查；失败不留下任何占用。
func TestWriteScopeAndSharedFileBlock(t *testing.T) {
	st := newStore(t)
	registerNode(t, st, "node-a", 4)
	seedTask(t, st, "T-X", seedOpts{status: model.TaskInProgress, owner: "worker-2",
		priority: 1, writeScope: []string{"internal/db/**"}})
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"internal/db/x.go"}})
	declareShared(t, st, "internal/db/store.go", "T-X")
	svc := New(st, nil)

	specified := request(a, "key-a", workspace(t, "ws-a"))
	specified.NodeID = "node-a"
	if _, err := svc.Claim(context.Background(), specified); err == nil {
		t.Fatal("写盘被占用时不应领取成功")
	} else {
		expectPort(t, err, model.PortErrConflict)
	}

	auto := Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(testProject), AgentID: testAgent, IdempotencyKey: "key-auto",
		},
		NodeID: "node-a", Workspace: workspace(t, "ws-auto"),
	}
	if _, err := svc.Claim(context.Background(), auto); err == nil {
		t.Fatal("自动领取也不应绕过写盘约束")
	} else {
		expectPort(t, err, model.PortErrConflict)
	}
	if got := runCount(t, st); got != 0 {
		t.Fatalf("被挡住时不应留下 Run，得到 %d", got)
	}
	if got := activeLeases(t, st); got != 0 {
		t.Fatalf("被挡住时不应留下占用，得到 %d", got)
	}
	if got := getTask(t, st, "T-A"); got.Revision != 1 || got.Status != string(model.TaskReady) {
		t.Fatalf("A 不应被改动：%+v", got)
	}
}

// 节点槽位与受管上限都必须与任务规则一起检查。
func TestNodeCapacityAndManagedLimit(t *testing.T) {
	st := newStore(t)
	node := registerNode(t, st, "node-a", 1)
	occupyNodeSlot(t, st, node)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})
	svc := New(st, nil)

	req := request(a, "key-a", workspace(t, "ws-a"))
	req.NodeID = node
	if _, err := svc.Claim(context.Background(), req); err == nil {
		t.Fatal("节点槽位占满时不应领取成功")
	} else {
		expectPort(t, err, model.PortErrCapacityExhausted)
	}
	if got := runCount(t, st); got != 0 {
		t.Fatalf("容量不足时不应留下 Run，得到 %d", got)
	}
	if got := getTask(t, st, "T-A"); got.Status != string(model.TaskReady) {
		t.Fatalf("容量不足不应改任务状态：%s", got.Status)
	}

	// 受管执行上限单独记账，不靠节点槽位顶替。
	st2 := newStore(t)
	node2 := registerNode(t, st2, "node-b", 4)
	first := seedTask(t, st2, "T-FIRST", seedOpts{priority: 10, writeScope: []string{"f/**"}})
	second := seedTask(t, st2, "T-SECOND", seedOpts{priority: 20, writeScope: []string{"s/**"}})
	svc2 := New(st2, nil)
	one := request(first, "key-first", workspace(t, "ws-first"))
	one.NodeID = node2
	if _, err := svc2.Claim(context.Background(), one); err != nil {
		t.Fatalf("首次领取应成功：%v", err)
	}
	two := request(second, "key-second", workspace(t, "ws-second"))
	two.NodeID = node2
	two.Capacity.ManagedLimit = 1
	if _, err := svc2.Claim(context.Background(), two); err == nil {
		t.Fatal("受管上限为 1 时第二次不应成功")
	} else {
		expectPort(t, err, model.PortErrCapacityExhausted)
	}
}

type guardFunc func(model.NodeID, []string) (bool, []string, error)

func (f guardFunc) Dispatchable(id model.NodeID, required []string) (bool, []string, error) {
	return f(id, required)
}

// 节点就绪与能力缺失分别报 conflict 与 capability_unsupported。
func TestNodeGuardRejects(t *testing.T) {
	st := newStore(t)
	node := registerNode(t, st, "node-a", 4)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})

	draining := New(st, guardFunc(func(model.NodeID, []string) (bool, []string, error) {
		return false, nil, nil
	}))
	req := request(a, "key-a", workspace(t, "ws-a"))
	req.NodeID = node
	if _, err := draining.Claim(context.Background(), req); err == nil {
		t.Fatal("draining 节点不应领取成功")
	} else {
		expectPort(t, err, model.PortErrConflict)
	}

	missing := New(st, guardFunc(func(model.NodeID, []string) (bool, []string, error) {
		return true, []string{"codex"}, nil
	}))
	req = request(a, "key-a", workspace(t, "ws-a"))
	req.NodeID = node
	req.RequiredCapabilities = []string{"codex"}
	if _, err := missing.Claim(context.Background(), req); err == nil {
		t.Fatal("能力不匹配不应领取成功")
	} else {
		expectPort(t, err, model.PortErrCapabilityMissing)
	}
	if got := activeLeases(t, st); got != 0 {
		t.Fatalf("被节点挡住时不应留下占用，得到 %d", got)
	}
}

// 同题并发只允许一个成功，失败方不留半状态。
func TestConcurrentSameTaskOnlyOneWins(t *testing.T) {
	st := newStore(t)
	node := registerNode(t, st, "node-a", 8)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})
	svc := New(st, nil)
	ws := workspace(t, "ws-a")

	const workers = 4
	var wg sync.WaitGroup
	errs := make([]error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := request(a, "key-"+string(rune('a'+i)), ws)
			req.NodeID = node
			_, errs[i] = svc.Claim(context.Background(), req)
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("同题并发应只有 1 个成功，得到 %d（%v）", ok, errs)
	}
	if got := getTask(t, st, "T-A"); got.Status != string(model.TaskClaimed) {
		t.Fatalf("任务应只被领取一次：status=%s attempts=%d", got.Status, got.Attempts)
	}
	if got := runCount(t, st); got != 1 {
		t.Fatalf("应只建 1 个 Run，得到 %d", got)
	}
	if got := operationCount(t, st); got != 1 {
		t.Fatalf("应只建 1 个 Operation，得到 %d", got)
	}
	if got := activeLeases(t, st); got != 2 {
		t.Fatalf("应只占 1 个节点槽 + 1 个目录，得到 %d", got)
	}
	if got := eventCount(t, st, "claimed"); got != 1 {
		t.Fatalf("应只有 1 条 claimed 事件，得到 %d", got)
	}
}

// 不同任务抢同一目录时也只允许一个成功。
func TestConcurrentSameWorkspaceOnlyOneWins(t *testing.T) {
	st := newStore(t)
	node := registerNode(t, st, "node-a", 8)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})
	b := seedTask(t, st, "T-B", seedOpts{priority: 20, writeScope: []string{"b/**"}})
	svc := New(st, nil)
	ws := workspace(t, "ws-shared")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, inst := range []model.InstanceID{a, b} {
		wg.Add(1)
		go func(i int, inst model.InstanceID) {
			defer wg.Done()
			req := request(inst, "key-"+string(rune('a'+i)), ws)
			req.NodeID = node
			_, errs[i] = svc.Claim(context.Background(), req)
		}(i, inst)
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		if err == nil {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("同目录并发应只有 1 个成功，得到 %d（%v）", ok, errs)
	}
	if got := runCount(t, st); got != 1 {
		t.Fatalf("应只建 1 个 Run，得到 %d", got)
	}
	if got := activeLeases(t, st); got != 2 {
		t.Fatalf("应只占 1 个节点槽 + 1 个目录，得到 %d", got)
	}
	ready := 0
	for _, id := range []string{"T-A", "T-B"} {
		if getTask(t, st, id).Status == string(model.TaskReady) {
			ready++
		}
	}
	if ready != 1 {
		t.Fatalf("失败方的任务应保持 ready，得到 %d 个 ready", ready)
	}
}

// 同幂等键同摘要重放不新写任何行；同键不同摘要报 E252。
func TestIdempotentReplay(t *testing.T) {
	st := newStore(t)
	node := registerNode(t, st, "node-a", 4)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})
	svc := New(st, nil)
	ws := workspace(t, "ws-a")

	req := request(a, "key-replay", ws)
	req.NodeID = node
	first, err := svc.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("首次领取失败：%v", err)
	}
	runs, ops, leases, events := runCount(t, st), operationCount(t, st), activeLeases(t, st), eventCount(t, st, "claimed")
	revision := getTask(t, st, "T-A").Revision

	second, err := svc.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("同键重放应返回原结果：%v", err)
	}
	if !second.Replay || second.RunID != first.RunID || second.Generation != first.Generation {
		t.Fatalf("重放应命中同一次 Run：first=%+v second=%+v", first, second)
	}
	if runCount(t, st) != runs || operationCount(t, st) != ops ||
		activeLeases(t, st) != leases || eventCount(t, st, "claimed") != events {
		t.Fatal("重放不应新写 Run、Operation、占用或事件")
	}
	if got := getTask(t, st, "T-A").Revision; got != revision {
		t.Fatalf("重放不应推进 revision：%d -> %d", revision, got)
	}

	other := req
	other.Workspace = workspace(t, "ws-other")
	if _, err := svc.Claim(context.Background(), other); err == nil {
		t.Fatal("同键不同内容应报错")
	} else if !errors.Is(err, model.Violation(model.CodeIdempotencyConflict, "", "")) {
		t.Fatalf("同键不同内容应报 E252，得到 %v", err)
	}
}

// 未绑定节点的人工领取只占目录，不占节点槽、不建 Run。
func TestManualClaimWithoutNode(t *testing.T) {
	st := newStore(t)
	a := seedTask(t, st, "T-A", seedOpts{priority: 10, writeScope: []string{"a/**"}})
	svc := New(st, nil)
	ws := workspace(t, "ws-a")

	req := request(a, "key-manual", ws)
	out, err := svc.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("人工领取应成功：%v", err)
	}
	if out.RunID != "" || out.Replay {
		t.Fatalf("人工领取不应建 Run：%+v", out)
	}
	if got := runCount(t, st); got != 0 {
		t.Fatalf("人工领取不应建 Run，得到 %d", got)
	}
	if got := activeLeases(t, st); got != 1 {
		t.Fatalf("人工领取应只占目录，得到 %d 条占用", got)
	}
	if got := count(t, st, `SELECT COUNT(*) FROM resource_leases WHERE kind = 'node_slot'`); got != 0 {
		t.Fatalf("人工领取不应占节点槽，得到 %d", got)
	}
	if got := getTask(t, st, "T-A"); got.Status != string(model.TaskClaimed) {
		t.Fatalf("任务应进入 claimed：%s", got.Status)
	}

	again, err := svc.Claim(context.Background(), req)
	if err != nil {
		t.Fatalf("人工领取重放应成功：%v", err)
	}
	if !again.Replay || len(again.Leases) != 1 {
		t.Fatalf("重放应返回原占用且不新写：%+v", again)
	}
	if got := operationCount(t, st); got != 1 {
		t.Fatalf("重放不应新增 Operation，得到 %d", got)
	}
}

// 没有 ready 任务时报 no_ready，而不是笼统的 conflict。
func TestNoReady(t *testing.T) {
	st := newStore(t)
	registerNode(t, st, "node-a", 4)
	seedTask(t, st, "T-DONE", seedOpts{status: model.TaskDone, priority: 1, writeScope: []string{"d/**"}})
	svc := New(st, nil)

	auto := Request{
		ClaimRequest: ports.ClaimRequest{
			ProjectID: model.ProjectID(testProject), AgentID: testAgent, IdempotencyKey: "key-auto",
		},
		NodeID: "node-a", Workspace: workspace(t, "ws"),
	}
	if _, err := svc.Claim(context.Background(), auto); err == nil {
		t.Fatal("没有 ready 任务时不应成功")
	} else {
		expectPort(t, err, model.PortErrNoReady)
	}
}
