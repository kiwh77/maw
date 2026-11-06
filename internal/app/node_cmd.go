package app

import (
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/errx"
	"github.com/kiwh77/maw/internal/home"
)

// NodeRegisterInput 登记一个通用本地 CLI 节点。
type NodeRegisterInput struct {
	ID        string
	Label     string
	ProjectID string
	Slots     int
	// ArgvJSON 是启动参数 JSON 数组，例如 ["/usr/bin/env","echo","ok"]。
	ArgvJSON string
	// CLI 是内置预设名（claude / codex / cursor / traecli / codebuddy / dsh）。
	// 给定时自动生成非交互 argv、任务卡提示与权限档；与 ArgvJSON 互斥。
	CLI string
	// Model 是节点默认模型；启动时可用 managed start --model 覆盖。
	Model string
	// Permission 是权限档：safe（默认，最小权限）或 unattended（跳过确认）。
	Permission string
	// System 为真时写入 meta.system_role，供系统 Agent 选用。
	System bool
	DryRun bool
}

// NodeList 列出中枢 nodes 表。
func NodeList(cwd string) (Result, error) {
	_ = cwd
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	var rows []*db.Node
	err = withGlobalRead(gdb, func(tx *sql.Tx) error {
		var e error
		rows, e = db.ListNode(tx, "")
		return e
	})
	if err != nil {
		return nil, err
	}
	items := make([]Result, 0, len(rows))
	for _, n := range rows {
		items = append(items, Result{
			"node_id": n.ID, "label": n.Label, "project_id": n.ProjectID,
			"slot_limit": n.SlotLimit, "last_seen": n.LastSeen, "meta": n.Meta,
		})
	}
	return Result{"ok": true, "nodes": items, "count": len(items)}, nil
}

// NodeRegister 写入通用 local_cli 节点（含匹配所需的就绪 meta 与 argv）。
func NodeRegister(cwd string, in NodeRegisterInput) (Result, error) {
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	id := strings.TrimSpace(in.ID)
	if id == "" {
		return nil, errx.New("需要 --id", "例如 maw node register --id cli-a --cmd '[\"/bin/echo\",\"ok\"]'")
	}
	label := strings.TrimSpace(in.Label)
	if label == "" {
		label = id
	}
	slots := in.Slots
	if slots <= 0 {
		slots = 1
	}
	projectID := strings.TrimSpace(in.ProjectID)
	if projectID == "" {
		if pc, err := resolveProject(cwd); err == nil {
			projectID = pc.ID()
		}
	}
	cliName := strings.ToLower(strings.TrimSpace(in.CLI))
	permission := strings.ToLower(strings.TrimSpace(in.Permission))
	if permission == "" {
		permission = "safe"
	}
	if permission != "safe" && permission != "unattended" {
		return nil, errx.New("--permission 只支持 safe 或 unattended", "例如 --permission unattended")
	}
	var argv []string
	rawCmd := strings.TrimSpace(in.ArgvJSON)
	if cliName != "" && rawCmd != "" {
		return nil, errx.New("--cli 与 --cmd 不能同时使用", "用 --cli 自动生成 argv，或用 --cmd 自定义")
	}
	if cliName != "" {
		var err error
		argv, err = buildCLIArgv(cliName, permission)
		if err != nil {
			return nil, err
		}
	} else if rawCmd != "" {
		if err := json.Unmarshal([]byte(rawCmd), &argv); err != nil {
			return nil, errx.New("--cmd 必须是 JSON 数组: "+err.Error(),
				`例如 --cmd '["/usr/bin/env","echo","hi"]'`)
		}
		if len(argv) < 1 {
			return nil, errx.New("--cmd 不能为空数组", `例如 --cmd '["/bin/echo","ok"]'`)
		}
	} else {
		return nil, errx.New("需要 --cli 或 --cmd", "例如 maw node register --id claude-1 --cli claude --model sonnet")
	}
	meta := localCLINodeMeta(argv, in.System)
	if flavor := cliFlavorFromID(id, label); flavor != "" {
		meta["cli_flavor"] = flavor
	}
	if cliName != "" {
		p, _ := cliPresetFor(cliName)
		meta["cli_flavor"] = p.Flavor
		meta["permission_mode"] = permission
		// 预设 argv 的末项是任务提示；模型与 dsh patch 在启动时插到它前面。
		meta["prompt_last"] = true
		meta["task_card"] = true
		meta["auto_probe"] = true
		meta["heartbeat_interval_sec"] = 60
	}
	if modelName := strings.TrimSpace(in.Model); modelName != "" {
		if cliName == "" {
			return nil, errx.New("--model 需要配合 --cli 使用", "例如 maw node register --id codex-1 --cli codex --model gpt-5")
		}
		meta["default_model"] = modelName
	}
	if in.DryRun {
		return Result{
			"ok": true, "dry_run": true, "node_id": id, "label": label,
			"project_id": projectID, "slot_limit": slots, "meta": meta,
		}, nil
	}
	if err := ensureWritableDB(gdb); err != nil {
		return nil, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{
			ID: id, ProjectID: projectID, Label: label, SlotLimit: slots,
			CreatedAt: now, LastSeen: now, Meta: meta,
		})
	})
	if err != nil {
		return nil, err
	}
	return Result{
		"ok": true, "node_id": id, "label": label, "project_id": projectID,
		"slot_limit": slots, "adapter_kind": "local_cli",
	}, nil
}

// NodeRemove 删除节点行（不清理历史占用记录）。
func NodeRemove(cwd, nodeID string, dryRun bool) (Result, error) {
	_ = cwd
	id := strings.TrimSpace(nodeID)
	if id == "" {
		return nil, errx.New("需要 node id", "maw node rm <id>")
	}
	gdb, err := home.GlobalDBPath()
	if err != nil {
		return nil, err
	}
	if dryRun {
		return Result{"ok": true, "dry_run": true, "node_id": id}, nil
	}
	if err := ensureWritableDB(gdb); err != nil {
		return nil, err
	}
	err = db.WithTx(gdb, func(tx *sql.Tx) error {
		return db.DeleteNode(tx, id)
	})
	if err != nil {
		return nil, err
	}
	return Result{"ok": true, "node_id": id, "removed": true}, nil
}

func localCLINodeMeta(argv []string, system bool) map[string]any {
	now := time.Now().UTC().Format(time.RFC3339)
	meta := map[string]any{
		"adapter_kind": "local_cli",
		"accept_new":   true,
		"entry_state":  "ready",
		// 看板四维认证态认 ready／needs-*／unknown；ok 会被当成 unknown。
		"auth_state":       "ready",
		"connection_state": "fresh",
		"capacity_state":   "available",
		"observed_at":      now,
	}
	if len(argv) > 0 {
		meta["argv"] = argv
		meta["entry_path"] = argv[0]
	}
	if system {
		meta["system_role"] = true
	}
	return meta
}
