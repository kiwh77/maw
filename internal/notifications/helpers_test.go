package notifications_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/notifications"
)

// testProject 是 16 位十六进制工程身份（maw.task.instance/v1 的 ProjectID 形态）。
const testProject = "0123456789abcdef"

// clock 是可控时钟：去重窗口与保留期都按时间判定，测试不能靠 sleep。
type clock struct{ t time.Time }

func newClock() *clock {
	return &clock{t: time.Date(2026, 10, 24, 2, 0, 0, 0, time.UTC)}
}

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }
func (c *clock) rewind(d time.Duration)  { c.t = c.t.Add(-d) }

func openAt(t *testing.T, path string) *db.Store {
	t.Helper()
	store, err := db.OpenStore(path)
	if err != nil {
		t.Fatalf("打开库失败：%v", err)
	}
	if err := store.Migrate(); err != nil {
		t.Fatalf("迁移失败：%v", err)
	}
	return store
}

// newStore 返回一个用完即关的临时库。
func newStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "notifications.db")
	store := openAt(t, path)
	t.Cleanup(func() { _ = store.Close() })
	return store, path
}

func newService(c *clock) *notifications.Service {
	svc := notifications.New()
	svc.Now = c.now
	return svc
}

// raise 在事务里登记一条提醒。
func raise(t *testing.T, store *db.Store, svc *notifications.Service, ev notifications.BusinessEvent) notifications.RaiseResult {
	t.Helper()
	var res notifications.RaiseResult
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		res, err = svc.Raise(tx, ev)
		return err
	}); err != nil {
		t.Fatalf("登记提醒失败：%v", err)
	}
	return res
}

func raiseErr(store *db.Store, svc *notifications.Service, ev notifications.BusinessEvent) error {
	return store.Write(func(tx *sql.Tx) error {
		_, err := svc.Raise(tx, ev)
		return err
	})
}

func unhandled(t *testing.T, store *db.Store, svc *notifications.Service) []notifications.Notification {
	t.Helper()
	var out []notifications.Notification
	if err := store.Read(func(tx *sql.Tx) error {
		var err error
		out, err = svc.Unhandled(tx, testProject, 200)
		return err
	}); err != nil {
		t.Fatalf("读取未处理列表失败：%v", err)
	}
	return out
}

func begin(t *testing.T, store *db.Store, svc *notifications.Service, id string) notifications.Attempt {
	t.Helper()
	var att notifications.Attempt
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		att, err = svc.BeginAttempt(tx, id)
		return err
	}); err != nil {
		t.Fatalf("记投递意图失败：%v", err)
	}
	return att
}

func complete(t *testing.T, store *db.Store, svc *notifications.Service, id string, no int, out notifications.Outcome) notifications.Notification {
	t.Helper()
	var n notifications.Notification
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		n, err = svc.CompleteAttempt(tx, id, no, out)
		return err
	}); err != nil {
		t.Fatalf("记投递结果失败：%v", err)
	}
	return n
}

func ack(t *testing.T, store *db.Store, svc *notifications.Service, id string, at time.Time) bool {
	t.Helper()
	var changed bool
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		changed, err = svc.Ack(tx, id, at)
		return err
	}); err != nil {
		t.Fatalf("确认失败：%v", err)
	}
	return changed
}

func recoverAll(t *testing.T, store *db.Store, svc *notifications.Service) notifications.RecoveryReport {
	t.Helper()
	var rep notifications.RecoveryReport
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Recover(tx, testProject)
		return err
	}); err != nil {
		t.Fatalf("恢复失败：%v", err)
	}
	return rep
}

func prune(t *testing.T, store *db.Store, svc *notifications.Service) notifications.PruneReport {
	t.Helper()
	var rep notifications.PruneReport
	if err := store.Write(func(tx *sql.Tx) error {
		var err error
		rep, err = svc.Prune(tx, testProject)
		return err
	}); err != nil {
		t.Fatalf("保留策略执行失败：%v", err)
	}
	return rep
}

// countRows 直接数库里的记录数，用来验证「没多写一条」这类断言。
func countRows(t *testing.T, store *db.Store) int {
	t.Helper()
	var n int
	if err := store.Read(func(tx *sql.Tx) error {
		return tx.QueryRow(`SELECT COUNT(*) FROM notification_deliveries`).Scan(&n)
	}); err != nil {
		t.Fatalf("统计记录数失败：%v", err)
	}
	return n
}

// runEvent 造一条 run.phase 业务事件。
func runEvent(eventID string, phase model.RunPhase) notifications.BusinessEvent {
	return notifications.BusinessEvent{
		EventID:   eventID,
		ProjectID: testProject,
		Kind:      notifications.EventRunPhase,
		Subject:   "ti_abc123",
		Phase:     phase,
	}
}

// taskEvent 造一条 task.status 业务事件。
func taskEvent(eventID, subject string, status model.TaskStatus) notifications.BusinessEvent {
	return notifications.BusinessEvent{
		EventID:    eventID,
		ProjectID:  testProject,
		Kind:       notifications.EventTaskStatus,
		Subject:    subject,
		TaskStatus: status,
	}
}

// recorder 是记录调用的假投递实现。
type recorder struct {
	requests []notifications.DeliveryRequest
	err      error
}

func (r *recorder) Deliver(_ context.Context, req notifications.DeliveryRequest) (notifications.DeliveryReceipt, error) {
	r.requests = append(r.requests, req)
	if r.err != nil {
		return notifications.DeliveryReceipt{}, r.err
	}
	return notifications.DeliveryReceipt{Evidence: "popup:" + req.NotificationID}, nil
}

// dispatch 跑一次投递循环。
func dispatch(t *testing.T, store *db.Store, svc *notifications.Service) notifications.DispatchReport {
	t.Helper()
	var rep notifications.DispatchReport
	rep, err := svc.Dispatch(context.Background(), store, testProject)
	if err != nil {
		t.Fatalf("投递失败：%v", err)
	}
	return rep
}
