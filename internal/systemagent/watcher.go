package systemagent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// Watcher 订阅读 events 表，把 blocked 分类入队，并驱动 Orchestrator.Step。
type Watcher struct {
	Store        *db.Store
	Config       config.SystemAgent
	Orchestrator *Orchestrator
	// ProjectID 非空时只看该工程；空则全局。
	ProjectID  string
	LeaderLock func() (release func(), err error)
	Clock      func() time.Time
	// ConfigFunc 可选：非空时每轮替换 Config，使 llm_command / auto_kinds /
	// ops_allowlist 的改动不必重启常驻循环。为 nil 时行为与引入前一致。
	ConfigFunc func() config.SystemAgent
	// SignalFacts 可选：系统级信号的证据源。为 nil 时 PollOnce 完全跳过信号检测，
	// 既有 task.blocked 入队路径不变；由装配卡注入读库 / 读事件的真实实现。
	SignalFacts SignalFacts
	// JobLease 是 running 作业的租约；<=0 时用 db.DefaultSystemJobLease（10 分钟）。
	// 每轮先回收超租约作业，避免进程崩溃后作业永远停在 running。
	JobLease time.Duration
	// OnError 可选：Run 每轮遇到非锁错误时回调。默认只继续循环、不退出进程。
	OnError func(error)
	// Housekeeping 可选：每轮先跑的主动巡检（过期心跳、作业回收等），由宿主注入。
	// 错误向上返回给 PollOnce，由 Run 的 OnError 记录；静默吞掉会让巡检形同虚设。
	// 为 nil 时行为与引入前完全一致。
	Housekeeping func(ctx context.Context) error
}

// conf 返回本轮生效的系统 Agent 配置：有热配置源时先刷新一次。
func (w *Watcher) conf() config.SystemAgent {
	if w == nil {
		return config.SystemAgent{}
	}
	if w.ConfigFunc != nil {
		w.Config = w.ConfigFunc()
	}
	return w.Config
}

func (w *Watcher) now() time.Time {
	if w != nil && w.Clock != nil {
		return w.Clock().UTC()
	}
	return time.Now().UTC()
}

// PollOnce 推进游标、入队、执行一步编排。
func (w *Watcher) PollOnce(ctx context.Context) (enqueued, ran int, err error) {
	if w == nil || w.Store == nil {
		return 0, 0, fmt.Errorf("systemagent: Watcher 未初始化")
	}
	if !w.conf().Enabled {
		return 0, 0, nil
	}
	if w.LeaderLock != nil {
		release, err := w.LeaderLock()
		if err != nil {
			return 0, 0, err
		}
		if release != nil {
			defer release()
		}
	}
	// 主动巡检不依赖任何 worker 调 next：中枢活着就得发现过期的心跳。
	// 放在最前面，错误直接返回本轮结果，不吞、不跳过后续判断。
	if w.Housekeeping != nil {
		if err := w.Housekeeping(ctx); err != nil {
			return 0, 0, err
		}
	}
	// 每轮先回收超租约作业：进程崩溃留下的 running 不会永远占着作业。
	// 回收只改 system_jobs 自身状态，不产生新的动作负担。
	if _, err := w.reapStaleJobs(); err != nil {
		return 0, 0, err
	}
	n, err := w.ingestBlocked(ctx)
	if err != nil {
		return n, 0, err
	}
	// 系统级信号仅在注入了证据源时检测并入队；未注入时现有行为完全不变。
	if w.SignalFacts != nil {
		sn, e := w.ingestSignals(ctx)
		if e != nil {
			return n, 0, e
		}
		n += sn
	}
	ran = 0
	if w.Orchestrator != nil {
		ran, err = w.Orchestrator.Step(ctx)
	}
	return n, ran, err
}

// Run 按 poll_interval 循环直到 ctx 取消。
func (w *Watcher) Run(ctx context.Context) error {
	interval := time.Duration(w.conf().PollIntervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, _, err := w.PollOnce(ctx); err != nil {
			// 锁竞争等可忽略，继续；其它错误交给宿主回调，但守护进程不退出。
			if !isLockErr(err) && w.OnError != nil {
				w.OnError(err)
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func (w *Watcher) reapStaleJobs() (int, error) {
	lease := w.JobLease
	if lease <= 0 {
		lease = db.DefaultSystemJobLease
	}
	var reaped int
	err := w.Store.Write(func(tx *sql.Tx) error {
		var e error
		reaped, e = db.ReapStaleSystemJobs(tx, w.now(), lease)
		return e
	})
	return reaped, err
}

func isLockErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "locked") || strings.Contains(msg, "锁")
}

// ingestSignals 检测系统级信号并经现有 Enqueue 入队观察。
// KindAllowed / 冷却 / max_attempts 的过滤都在 EnqueueSignals 内完成，
// 这里只负责「检测 + 开一次写事务」，不新增任何动作。
func (w *Watcher) ingestSignals(ctx context.Context) (int, error) {
	_ = ctx
	signals := DetectSystemSignals(w.SignalFacts, w.conf(), w.now())
	var jobs []*db.SystemJob
	err := w.Store.Write(func(tx *sql.Tx) error {
		var e error
		jobs, e = EnqueueSignals(tx, w.conf(), signals, w.now())
		return e
	})
	if err != nil {
		return 0, err
	}
	return len(jobs), nil
}

func (w *Watcher) ingestBlocked(ctx context.Context) (int, error) {
	_ = ctx
	var cursor int64
	err := w.Store.Read(func(tx *sql.Tx) error {
		st, e := db.GetProjectionState(tx, db.ProjectionSystemAgentEvents, w.ProjectID)
		if e != nil {
			return e
		}
		if st != nil {
			cursor = st.Cursor
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	var events []*db.Event
	err = w.Store.Read(func(tx *sql.Tx) error {
		var e error
		events, e = db.ListEventsSince(tx, cursor, w.ProjectID, "", 200)
		return e
	})
	if err != nil {
		return 0, err
	}
	if len(events) == 0 {
		return 0, nil
	}

	enqueued := 0
	maxID := cursor
	cooldown := time.Duration(w.conf().CooldownoutSec) * time.Second
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}

	err = w.Store.Write(func(tx *sql.Tx) error {
		for _, ev := range events {
			if ev.ID > maxID {
				maxID = ev.ID
			}
			if ev.Type != "status_changed" {
				continue
			}
			to, reason := parseBlockedData(ev.Data)
			if to != "blocked" {
				continue
			}
			enqueue, escalate := ClassifyBlocked(reason, w.conf())
			if !enqueue {
				continue
			}
			since := w.now().Add(-cooldown)
			n, e := db.CountRecentSystemJobs(tx, ev.ProjectID, SystemKindUnblock, ev.TaskID, since)
			if e != nil {
				return e
			}
			if n > 0 {
				continue
			}
			t, e := db.GetTask(tx, ev.ProjectID, ev.TaskID)
			if e != nil {
				return e
			}
			instanceID := ""
			if t != nil {
				instanceID = t.InstanceID
			}
			decision := map[string]any{}
			if escalate {
				decision["action"] = string(ActionEscalateHuman)
			}
			job := &db.SystemJob{
				JobID:          newJobID(),
				ProjectID:      ev.ProjectID,
				Kind:           SystemKindUnblock,
				Status:         db.SystemJobQueued,
				TaskID:         ev.TaskID,
				InstanceID:     instanceID,
				TriggerEventID: ev.ID,
				Reason:         reason,
				Decision:       decision,
				MaxAttempts:    w.conf().MaxAttempts,
			}
			inserted, e := db.InsertSystemJob(tx, job)
			if e != nil {
				return e
			}
			if inserted != nil {
				enqueued++
			}
		}
		return db.UpsertProjectionState(tx, db.ProjectionState{
			Projection: db.ProjectionSystemAgentEvents,
			ProjectID:  w.ProjectID,
			Cursor:     maxID,
			UpdatedAt:  db.UTCNow(),
		})
	})
	return enqueued, err
}

func parseBlockedData(m map[string]any) (to, reason string) {
	if m == nil {
		return "", ""
	}
	if v, ok := m["to"].(string); ok {
		to = v
	}
	if v, ok := m["reason"].(string); ok {
		reason = v
	}
	return to, reason
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "sjob_" + hex.EncodeToString(b[:])
}

// Enqueue 显式创建作业（CLI / HTTP）。
func Enqueue(tx *sql.Tx, cfg config.SystemAgent, projectID, kind, taskID, instanceID, reason string, decision map[string]any, explicit bool) (*db.SystemJob, error) {
	if !KindAllowed(kind, cfg, explicit) {
		return nil, fmt.Errorf("systemagent: kind %q 未允许", kind)
	}
	if decision == nil {
		decision = map[string]any{}
	}
	job := &db.SystemJob{
		JobID:       newJobID(),
		ProjectID:   projectID,
		Kind:        kind,
		Status:      db.SystemJobQueued,
		TaskID:      taskID,
		InstanceID:  instanceID,
		Reason:      reason,
		Decision:    decision,
		MaxAttempts: cfg.MaxAttempts,
	}
	return db.InsertSystemJob(tx, job)
}
