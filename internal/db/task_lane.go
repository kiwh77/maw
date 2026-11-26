package db

// taskLaneDDL 是 v14：任务上下文子队列（lane）。
//
// Version 12/13 由 system_jobs.go init 追加；本文件名排在 system_jobs 之后，
// 保证 v14 挂在台账末端。Agent 粘性绑定存在 agents.meta_json.bound_lane。
const taskLaneDDL = `
ALTER TABLE tasks ADD COLUMN lane TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS tasks_lane
    ON tasks(project_id, lane, priority, merge_order);
`

func init() {
	migrations = append(migrations, Migration{Version: 14, Name: "task_lane", SQL: taskLaneDDL})
}
