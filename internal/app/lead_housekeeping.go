package app

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
)

// leadHousekeepingDeps 把「列举工程／读配置／跑过期检测」变成端口。
//
// 之前 expireStaleAgents 只由 worker 调 maw next 时被动触发，中枢常驻但没人
// 领题时过期 Agent 会一直挂在工作态。这里由 Watcher 每轮主动巡检，按工程
// 借用同一套判定与 TTL 语义，不改 next 调用点、不改心跳语义。
type leadHousekeepingDeps struct {
	projects func() ([]serveLeaderProject, error)
	pathOK   func(string) bool
	load     func(string) (*projCtx, config.SystemAgent, error)
	// expire 返回错误时整个巡检事务回滚，绝不留下半状态。
	expire func(pc *projCtx, now time.Time) error
}

// leadHousekeeping 返回给 Watcher 注入的每轮巡检钩子。
//
// now 由 Watcher 的时钟提供：测试可注入确定性时刻，验证「一个 poll 周期内」
// 就能发现超时心跳，而不是依赖真实时间流逝。
func leadHousekeeping(now func() time.Time, deps leadHousekeepingDeps) func(context.Context) error {
	if now == nil {
		now = time.Now
	}
	return func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		projects, err := deps.projects()
		if err != nil {
			return fmt.Errorf("lead housekeeping: 列举已登记工程: %w", err)
		}
		stamp := now().UTC()
		for _, p := range projects {
			if err := ctx.Err(); err != nil {
				return err
			}
			if p.ID == "" || p.Path == "" || !deps.pathOK(p.Path) {
				continue
			}
			pc, _, err := deps.load(p.Path)
			if err != nil {
				// 单个工程配置不可读不应让其它工程的巡检停摆：跳过继续。
				continue
			}
			if err := deps.expire(pc, stamp); err != nil {
				return fmt.Errorf("lead housekeeping: project=%s 过期检测失败: %w", pc.ID(), err)
			}
		}
		return nil
	}
}

// defaultLeadHousekeepingDeps 是生产装配：过期检测在独立事务里跑，
// 任一写失败由 db.WithTx 整体回滚。
func defaultLeadHousekeepingDeps(gdb string) leadHousekeepingDeps {
	base := defaultServeLeaderDeps(gdb)
	return leadHousekeepingDeps{
		projects: base.projects,
		pathOK:   base.pathOK,
		load:     base.load,
		expire: func(pc *projCtx, now time.Time) error {
			return db.WithTx(gdb, func(tx *sql.Tx) error {
				return expireStaleAgentsAt(tx, pc, now)
			})
		},
	}
}
