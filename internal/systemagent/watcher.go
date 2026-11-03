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
	Store       *db.Store
	Config      config.SystemAgent
	Orchestrator *Orchestrator
	// ProjectID 非空时只看该工程；空则全局。
	ProjectID string
	LeaderLock func() (release func(), err error)
	Clock      func() time.Time
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
	if !w.Config.Enabled {
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
	n, err := w.ingestBlocked(ctx)
	if err != nil {
		return n, 0, err
	}
	ran = 0
	if w.Orchestrator != nil {
		ran, err = w.Orchestrator.Step(ctx)
	}
	return n, ran, err
}

// Run 按 poll_interval 循环直到 ctx 取消。
func (w *Watcher) Run(ctx context.Context) error {
	interval := time.Duration(w.Config.PollIntervalSec) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if _, _, err := w.PollOnce(ctx); err != nil {
			// 锁竞争等可忽略，继续
			if !isLockErr(err) {
				// 非锁错误也继续跑，避免守护进程退出
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
}

func isLockErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "locked") || strings.Contains(msg, "锁")
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
	cooldown := time.Duration(w.Config.CooldownoutSec) * time.Second
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
			enqueue, escalate := ClassifyBlocked(reason, w.Config)
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
				MaxAttempts:    w.Config.MaxAttempts,
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
