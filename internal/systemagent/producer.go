package systemagent

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// ProducerConfig 是自动选题生产者的开关与时间窗。
//
// 契约 §9：默认全关。AutoSelect 与 auto_enabled / system_agent.enabled 互不推导：
// 生产者只负责「有 ready 题且有空槽时入队一条 dispatch_ready」，执行走既有
// Match→Claim 链，不绕过匹配、领取或节点门禁。
type ProducerConfig struct {
	AutoSelect  bool
	CooldownSec int
	MaxAttempts int
}

// ProducerPorts 把「选题判定／已有作业／入队／执行」做成注入端口。
//
// Selectable 复用 matching.Match 的结论，因此「无 ready 题」「受管槽满」等
// 原因仍由匹配层给出，生产者不另立一套可派发判断。
type ProducerPorts struct {
	Now        func() time.Time
	Projects   func() ([]ProducerProject, error)
	Selectable func(projectID string) (taskID string, ok bool, err error)
	Recent     func(projectID string, since time.Time) (int, error)
	Enqueue    func(projectID, taskID string) (jobID string, inserted bool, err error)
	Run        func(ctx context.Context, jobID string) error
}

// ProducerProject 是生产者需要的工程登记视图。
type ProducerProject struct {
	ID   string
	Path string
}

// ProducerOutcome 记录一个周期里发生了什么，供测试与事件核对。
type ProducerOutcome struct {
	Enqueued int      `json:"enqueued"`
	Executed int      `json:"executed"`
	Skipped  []string `json:"skipped,omitempty"`
	JobID    string   `json:"job_id,omitempty"`
}

// Producer 是自动选题生产者；每轮最多入队一条，且入队后同周期立即执行。
type Producer struct {
	Config ProducerConfig
	Ports  ProducerPorts
}

// Produce 执行一个生产周期。
//
// 顺序：开关 → 冷却/未完成去重 → 可选题判定 → 入队 → 同周期执行。
// 只要有一处写失败就返回错误；单个工程判定失败不影响其它工程继续尝试。
func (p *Producer) Produce(ctx context.Context) (ProducerOutcome, error) {
	out := ProducerOutcome{}
	if p == nil || !p.Config.AutoSelect {
		out.Skipped = append(out.Skipped, "auto_select_off")
		return out, nil
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	projects, err := p.Ports.Projects()
	if err != nil {
		return out, fmt.Errorf("auto select: 列举工程: %w", err)
	}
	now := p.now()
	since := now.Add(-time.Duration(p.cooldownSec()) * time.Second)
	for _, proj := range projects {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if proj.ID == "" || proj.Path == "" {
			out.Skipped = append(out.Skipped, "project_missing")
			continue
		}
		// 同一工程已有未完成 dispatch_ready 或冷却窗口内刚派发过：跳过，
		// 避免每轮都往队列里堆同一道题的重复作业。
		recent, err := p.Ports.Recent(proj.ID, since)
		if err != nil {
			return out, fmt.Errorf("auto select: project=%s 查已有作业: %w", proj.ID, err)
		}
		if recent > 0 {
			out.Skipped = append(out.Skipped, proj.ID+":recent_dispatch")
			continue
		}
		taskID, ok, err := p.Ports.Selectable(proj.ID)
		if err != nil {
			return out, fmt.Errorf("auto select: project=%s 选题判定: %w", proj.ID, err)
		}
		if !ok {
			out.Skipped = append(out.Skipped, proj.ID+":no_ready_or_no_slots")
			continue
		}
		jobID, inserted, err := p.Ports.Enqueue(proj.ID, taskID)
		if err != nil {
			return out, fmt.Errorf("auto select: project=%s 入队: %w", proj.ID, err)
		}
		if !inserted || jobID == "" {
			out.Skipped = append(out.Skipped, proj.ID+":deduped")
			continue
		}
		out.Enqueued++
		out.JobID = jobID
		// 同周期立即执行：不依赖 system_agent.enabled，也不等下一次 Watcher 轮询。
		if p.Ports.Run != nil {
			if err := p.Ports.Run(ctx, jobID); err != nil {
				return out, fmt.Errorf("auto select: project=%s 执行 %s: %w", proj.ID, jobID, err)
			}
			out.Executed++
		}
		// 一个周期恰好一条：命中即停。
		return out, nil
	}
	return out, nil
}

func (p *Producer) now() time.Time {
	if p.Ports.Now != nil {
		return p.Ports.Now().UTC()
	}
	return time.Now().UTC()
}

func (p *Producer) cooldownSec() int {
	if p.Config.CooldownSec > 0 {
		return p.Config.CooldownSec
	}
	return 300
}

// NewStoreProducerPorts 用真实状态库装配生产端口。
//
// Selectable 走 matching.Match（由调用方注入的投影决定 ready/空槽），
// Enqueue 用显式 kind（绕过 auto_kinds 但仍是受控 kind 白名单），
// Run 直接调 Orchestrator.RunJobID——它不检查 Config.Enabled，因此
// system_agent.enabled=false 时生产的作业仍能在同周期被执行并产生 Run。
func NewStoreProducerPorts(store *db.Store, cfg config.SystemAgent,
	projects func() ([]ProducerProject, error),
	selectable func(projectID string) (string, bool, error),
	run func(ctx context.Context, jobID string) error) ProducerPorts {
	return ProducerPorts{
		Projects:   projects,
		Selectable: selectable,
		Recent: func(projectID string, since time.Time) (int, error) {
			var n int
			err := store.Read(func(tx *sql.Tx) error {
				jobs, e := db.ListSystemJobs(tx, projectID, "", 200)
				if e != nil {
					return e
				}
				for _, j := range jobs {
					if j == nil || j.Kind != SystemKindDispatchReady {
						continue
					}
					// 未完成作业无论多旧都算「已有」；已完成作业看冷却窗口。
					if !dispatchJobTerminal(j.Status) {
						n++
						continue
					}
					created, e := time.Parse(time.RFC3339, j.CreatedAt)
					if e == nil && !created.Before(since) {
						n++
					}
				}
				return nil
			})
			return n, err
		},
		Enqueue: func(projectID, taskID string) (string, bool, error) {
			var job *db.SystemJob
			err := store.Write(func(tx *sql.Tx) error {
				var e error
				job, e = Enqueue(tx, cfg, projectID, SystemKindDispatchReady,
					taskID, "", "auto_select: 有 ready 题且受管空槽>0", map[string]any{}, true)
				return e
			})
			if err != nil {
				return "", false, err
			}
			if job == nil {
				return "", false, nil
			}
			return job.JobID, true, nil
		},
		Run: run,
	}
}

func dispatchJobTerminal(status string) bool {
	switch status {
	case db.SystemJobDone, db.SystemJobEscalated, db.SystemJobCancelled:
		return true
	default:
		return false
	}
}
