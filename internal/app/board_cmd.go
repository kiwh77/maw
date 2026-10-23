package app

import (
	"fmt"
	"path/filepath"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
)

// BoardFilters 看板筛选条件（空字符串表示不限）
type BoardFilters struct {
	Project string
	Agent   string
	Status  string
}

func Board(cwd string, serve bool, addr string, filters BoardFilters) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if err := db.Init(gdb); err != nil {
		return nil, err
	}
	// 若在某工程内且未指定 --project，仍默认看全局；可用 --project 收窄
	if filters.Project == "" {
		if pc, err := resolveProject(cwd); err == nil {
			_ = pc // 确保工程已注册进全局库
		}
	}

	snap, err := loadBoardSnapshot(gdb, filters.Project, filters.Agent, filters.Status, 4)
	if err != nil {
		return nil, err
	}

	gdir, err := home.GlobalDir()
	if err != nil {
		return nil, err
	}
	htmlPath, err := board.Write(gdir, snap)
	if err != nil {
		return nil, err
	}
	// 若当前在工程内，同步写一份到工程 .multiagent/board
	if pc, err := resolveProject(cwd); err == nil {
		_, _ = board.Write(pc.LocalHome, snap)
	}

	term := board.RenderTerminal(snap)
	res := Result{
		"ok": true, "terminal": term, "html": htmlPath, "counts": snap.Counts,
		"agents": snap.Agents, "projects": snap.Projects,
		"filters": map[string]string{
			"project": filters.Project, "agent": filters.Agent, "status": filters.Status,
		},
		"global_db": gdb,
		"snapshot":  filepath.Join(gdir, "board", "snapshot.json"),
		"next":      i18n.T("next.open_html"),
	}
	if serve {
		if addr == "" {
			addr = "127.0.0.1:7420"
		}
		url, err := board.ServeWith(addr, board.ServeOpts{
			Load: func(projectID, agent, status string) (board.Snapshot, error) {
				if projectID == "" {
					projectID = filters.Project
				}
				if agent == "" {
					agent = filters.Agent
				}
				if status == "" {
					status = filters.Status
				}
				return loadBoardSnapshot(gdb, projectID, agent, status, 4)
			},
			Detail: func(projectID, taskID string) (board.TaskDetail, error) {
				return loadTaskDetail(gdb, projectID, taskID)
			},
			Status: func(rep board.StatusReport) (map[string]any, error) {
				r, err := StatusReport(cwd, StatusReportInput{
					ProjectID: rep.ProjectID,
					Actor:     rep.Actor,
					TaskID:    rep.TaskID,
					Message:   rep.Message,
					Phase:     rep.Phase,
					Pct:       rep.Pct,
					Client:    rep.Client,
					Status:    rep.Status,
				})
				if err != nil {
					return nil, err
				}
				return map[string]any(r), nil
			},
			Feed: func(since int64, projectID, agent string) ([]*db.Event, error) {
				return LoadEventFeed(gdb, since, projectID, agent)
			},
		})
		if err != nil {
			return nil, i18n.EW("err.board_serve", "next.change_addr", err)
		}
		res["url"] = url
		res["next"] = i18n.T("board.next_url", url)
		fmt.Println(i18n.T("board.hub_started"), url)
		fmt.Println(i18n.T("board.data_source"), gdb)
		fmt.Println(i18n.T("board.ctrl_c"))
		select {}
	}
	return res, nil
}

// Serve is an alias for Board(..., serve=true) — recommended hub entrypoint.
func Serve(cwd, addr string, filters BoardFilters) (Result, error) {
	return Board(cwd, true, addr, filters)
}
