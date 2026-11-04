package app

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
)

// startNodeProbeLoop 按节点 meta.heartbeat_interval_sec 循环刷新心跳（自动探测）。
// 只在 maw board --serve / startServeLoops 持有者进程里跑。
func startNodeProbeLoop(gdb string) {
	gdb = strings.TrimSpace(gdb)
	if gdb == "" {
		return
	}
	go func() {
		// 扫描周期固定 5s：每个节点自己的 interval 决定是否真正探测。
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_ = probeDueNodes(gdb)
		}
	}()
}

func probeDueNodes(gdb string) error {
	return db.WithTx(gdb, func(tx *sql.Tx) error {
		rows, err := db.ListNode(tx, "")
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		nowStr := now.Format(time.RFC3339)
		for _, n := range rows {
			if n == nil {
				continue
			}
			meta := n.Meta
			if meta == nil {
				meta = map[string]any{}
			}
			if !nodeAutoProbeEnabled(meta) {
				continue
			}
			interval := nodeHeartbeatInterval(meta)
			last := nodeLastProbeAt(n, meta)
			if !last.IsZero() && now.Sub(last) < time.Duration(interval)*time.Second {
				continue
			}
			entry := nodeEntryPath(meta)
			ok, detail := probeNodeEntry(entry)
			meta["last_probe_at"] = nowStr
			meta["observed_at"] = nowStr
			meta["auto_probe"] = true
			meta["heartbeat_interval_sec"] = interval
			if ok {
				meta["entry_state"] = "ready"
				meta["auth_state"] = "ready"
				meta["connection_state"] = "fresh"
				meta["capacity_state"] = "available"
				meta["last_probe_ok"] = true
				delete(meta, "last_probe_error")
			} else {
				meta["entry_state"] = "unavailable"
				meta["connection_state"] = "stale"
				meta["last_probe_ok"] = false
				meta["last_probe_error"] = detail
			}
			n.Meta = meta
			n.LastSeen = nowStr
			if err := db.UpsertNode(tx, n); err != nil {
				return err
			}
		}
		return nil
	})
}

func nodeAutoProbeEnabled(meta map[string]any) bool {
	if meta == nil {
		return true
	}
	v, ok := meta["auto_probe"]
	if !ok {
		return true
	}
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t != "false" && t != "0"
	default:
		return true
	}
}

func nodeHeartbeatInterval(meta map[string]any) int {
	interval := metaInt(meta, "heartbeat_interval_sec")
	if interval <= 0 {
		interval = defaultHeartbeatIntervalSec
	}
	if interval < minHeartbeatIntervalSec {
		return minHeartbeatIntervalSec
	}
	if interval > maxHeartbeatIntervalSec {
		return maxHeartbeatIntervalSec
	}
	return interval
}

func nodeLastProbeAt(n *db.Node, meta map[string]any) time.Time {
	if meta != nil {
		if raw, ok := meta["last_probe_at"].(string); ok && strings.TrimSpace(raw) != "" {
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				return t
			}
		}
	}
	if n != nil && strings.TrimSpace(n.LastSeen) != "" {
		if t, err := time.Parse(time.RFC3339, n.LastSeen); err == nil {
			return t
		}
	}
	return time.Time{}
}

func nodeEntryPath(meta map[string]any) string {
	if meta == nil {
		return ""
	}
	if p, ok := meta["entry_path"].(string); ok && strings.TrimSpace(p) != "" {
		return strings.TrimSpace(p)
	}
	raw, ok := meta["argv"]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case []any:
		if len(v) > 0 {
			return strings.TrimSpace(stringify(v[0]))
		}
	case []string:
		if len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

func stringify(v any) string {
	switch t := v.(type) {
	case string:
		return t
	default:
		return ""
	}
}

// probeNodeEntry：绝对路径看文件是否存在；命令名走 LookPath。
func probeNodeEntry(entry string) (bool, string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return false, "missing entry_path"
	}
	if filepath.IsAbs(entry) {
		st, err := os.Stat(entry)
		if err != nil {
			return false, err.Error()
		}
		if st.IsDir() {
			return false, "entry_path is a directory"
		}
		return true, ""
	}
	if _, err := exec.LookPath(entry); err != nil {
		return false, err.Error()
	}
	return true, ""
}
