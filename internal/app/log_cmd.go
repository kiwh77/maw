package app

import (
	"database/sql"
	"strings"

	"github.com/kiwh77/maw/internal/board"
	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/home"
	"github.com/kiwh77/maw/internal/i18n"
)

// AgentLog reads a chunk of the agent's reported session log (not stored in SQLite).
// fromTail applies only when offset is 0 (initial drawer open).
func AgentLog(projectID, agent string, offset int64, fromTail bool) (board.LogChunk, error) {
	projectID = strings.TrimSpace(projectID)
	agent = strings.TrimSpace(agent)
	if projectID == "" || agent == "" {
		return board.LogChunk{OK: false, Error: "project and agent required"}, i18n.E("err.log_args", "next.fix_log_path")
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return board.LogChunk{OK: false, Error: err.Error()}, err
	}
	if err := db.Init(gdb); err != nil {
		return board.LogChunk{OK: false, Error: err.Error()}, err
	}
	var path string
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		a, e := db.GetAgent(tx, projectID, agent)
		if e != nil {
			return e
		}
		if a == nil {
			return i18n.E("err.agent_missing", "next.register_first", agent)
		}
		path = board.AgentMetaLogPath(a.Meta)
		return nil
	})
	if err != nil {
		return board.LogChunk{OK: false, Error: err.Error()}, err
	}
	if path == "" {
		return board.LogChunk{OK: true, Missing: true, Offset: offset, NextOffset: offset, EOF: true}, nil
	}
	return board.ReadLogFile(path, offset, board.DefaultLogChunkBytes, fromTail)
}
