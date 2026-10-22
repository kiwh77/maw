package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/kiwh77/maw/internal/config"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
)

type projectMetaFile struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

type projCtx struct {
	Repo      string
	LocalHome string
	GlobalDB  string
	Project   *db.Project
	Config    *config.Config
}

func (p *projCtx) ID() string { return p.Project.ID }

func writeProjectMeta(localHome string, p *db.Project) error {
	b, _ := json.MarshalIndent(projectMetaFile{ID: p.ID, Name: p.Name, Path: p.Path}, "", "  ")
	return os.WriteFile(home.ProjectMetaPath(localHome), append(b, '\n'), 0o644)
}

func resolveProject(cwd string) (*projCtx, error) {
	repo, err := home.RepoRoot(cwd)
	if err != nil {
		return nil, err
	}
	local, err := home.RequireHome(cwd)
	if err != nil {
		return nil, err
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if err := db.Init(gdb); err != nil {
		return nil, err
	}
	cfg, err := config.Load(home.ConfigPath(local))
	if err != nil {
		return nil, err
	}
	pid, pname := "", filepath.Base(repo)
	if b, err := os.ReadFile(home.ProjectMetaPath(local)); err == nil {
		var m projectMetaFile
		if json.Unmarshal(b, &m) == nil && m.ID != "" {
			pid, pname = m.ID, m.Name
		}
	}
	if pid == "" {
		pid = home.ProjectIDFromPath(repo)
	}
	if pname == "" {
		pname = filepath.Base(repo)
	}
	var proj *db.Project
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		p, e := db.GetProject(tx, pid)
		if e != nil {
			return e
		}
		if p == nil {
			p = &db.Project{
				ID: pid, Name: pname, Path: repo,
				MainBranch: cfg.Project.MainBranch, MaxParallel: cfg.Project.MaxParallel,
			}
		} else {
			p.Name = pname
			p.Path = repo
			p.MainBranch = cfg.Project.MainBranch
			p.MaxParallel = cfg.Project.MaxParallel
		}
		if e := db.UpsertProject(tx, p); e != nil {
			return e
		}
		proj = p
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = writeProjectMeta(local, proj)
	_ = migrateLegacyLocalDB(local, gdb, proj.ID)
	return &projCtx{Repo: repo, LocalHome: local, GlobalDB: gdb, Project: proj, Config: cfg}, nil
}

// migrateLegacyLocalDB 将旧版 .multiagent/maw.db 任务导入全局库（仅当全局尚无该工程任务时）
func migrateLegacyLocalDB(localHome, gdb, projectID string) error {
	legacy := home.LegacyLocalDBPath(localHome)
	if _, err := os.Stat(legacy); err != nil {
		return nil
	}
	var n int
	_ = db.WithTx(gdb, func(tx *sql.Tx) error {
		tasks, err := db.ListTasks(tx, projectID)
		if err != nil {
			return err
		}
		n = len(tasks)
		return nil
	})
	if n > 0 {
		return nil
	}
	old, err := db.OpenRaw(legacy)
	if err != nil {
		return nil
	}
	defer old.Close()
	rows, err := old.Query(`SELECT id, title, status, priority, merge_order, owner, deps_json, write_scope_json, read_scope_json, acceptance_json, branch, worktree, attempts, blocked_reason, heartbeat, prev_status, revision, extra_json FROM tasks`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	return db.WithTx(gdb, func(tx *sql.Tx) error {
		for rows.Next() {
			var t db.Task
			var owner, branch, wt, blocked, hb, prev sql.NullString
			var deps, ws, rs, acc, extra string
			if err := rows.Scan(&t.ID, &t.Title, &t.Status, &t.Priority, &t.MergeOrder, &owner,
				&deps, &ws, &rs, &acc, &branch, &wt, &t.Attempts, &blocked, &hb, &prev, &t.Revision, &extra); err != nil {
				return err
			}
			t.ProjectID = projectID
			t.Owner = owner.String
			t.Branch = branch.String
			t.Worktree = wt.String
			t.BlockedReason = blocked.String
			t.Heartbeat = hb.String
			t.PrevStatus = prev.String
			_ = json.Unmarshal([]byte(deps), &t.Deps)
			_ = json.Unmarshal([]byte(ws), &t.WriteScope)
			_ = json.Unmarshal([]byte(rs), &t.ReadScope)
			_ = json.Unmarshal([]byte(acc), &t.Acceptance)
			t.Extra = map[string]any{}
			_ = json.Unmarshal([]byte(extra), &t.Extra)
			if err := db.UpsertTask(tx, &t); err != nil {
				return err
			}
		}
		return nil
	})
}
