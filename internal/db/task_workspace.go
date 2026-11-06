package db

import (
	"database/sql"
	"strings"
)

// BindTaskWorkspace 仅为空的分支/工作目录补值；已有值保持不动。
//
// 受管执行在领取后才知道实际 worktree，这里回填是为了让后续 submit/merge 能找到同一目录；
// 不触碰修订号与任务状态，避免与领取事务的状态推进竞争。
func BindTaskWorkspace(tx *sql.Tx, projectID, taskID, branch, worktree string) error {
	if tx == nil {
		return nil
	}
	projectID = strings.TrimSpace(projectID)
	taskID = strings.TrimSpace(taskID)
	if projectID == "" || taskID == "" {
		return nil
	}
	_, err := tx.Exec(`UPDATE tasks SET
		branch = CASE WHEN COALESCE(branch, '') = '' THEN ? ELSE branch END,
		worktree = CASE WHEN COALESCE(worktree, '') = '' THEN ? ELSE worktree END
		WHERE project_id = ? AND id = ?`, branch, worktree, projectID, taskID)
	return err
}
