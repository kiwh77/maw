package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
			applyNodeProbe(meta, now, interval)
			n.Meta = meta
			n.LastSeen = nowStr
			if err := db.UpsertNode(tx, n); err != nil {
				return err
			}
		}
		return nil
	})
}

// probePiRuntime 执行 `<node> <main.mjs> --probe`，读运行时自报的版本与协议。
//
// 只读探测：不跑模型、不建会话；凭据不在环境里也能通过（--probe 不需要密钥）。
// 输出必须是首行 JSON 且 protocol 与契约一致，否则按「入口不可用」处理。
func probePiRuntime(argv []string) (bool, string, map[string]any) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], append(append([]string{}, argv[1:]...), "--probe")...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return false, "probe timeout", nil
		}
		return false, strings.TrimSpace(string(out)), nil
	}
	line := strings.TrimSpace(string(out))
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	var report struct {
		OK       bool `json:"ok"`
		Protocol struct {
			Format  string `json:"format"`
			Version string `json:"version"`
		} `json:"protocol"`
		SDK  string `json:"sdk"`
		Node string `json:"node"`
	}
	if err := json.Unmarshal([]byte(line), &report); err != nil {
		return false, "probe 输出不是 JSON: " + err.Error(), nil
	}
	if !report.OK || strings.TrimSpace(report.SDK) == "" {
		return false, "probe 自报 ok=false 或缺 sdk 版本", nil
	}
	if report.Protocol.Format != "pi-stream-json" || report.Protocol.Version != "1" {
		return false, "协议不匹配: " + report.Protocol.Format + "/" + report.Protocol.Version, nil
	}
	return true, "", map[string]any{
		"version":          report.SDK,
		"protocol_format":  report.Protocol.Format,
		"protocol_version": report.Protocol.Version,
		"runtime_node":     report.Node,
	}
}

// applyNodeProbe 刷新一次节点观测证据（只改 meta，不碰库）。
//
// 抽出来是为了让两条路径共用同一套判定：周期探测（probeDueNodes）与
// 「打开自动执行时立刻探活」（probeNodeNow）。两边各写一份的实现迟早会分叉，
// 而分叉的后果是「界面说就绪、匹配层说过期」。
func applyNodeProbe(meta map[string]any, now time.Time, interval int) (ok bool, detail string) {
	if meta == nil {
		return false, "missing meta"
	}
	nowStr := now.Format(time.RFC3339)
	entry := nodeEntryPath(meta)
	ok, detail = probeNodeEntry(entry)
	// Pi 节点：入口存在还不够，必须让运行时自报版本与协议。
	if ok && metaString(meta, "adapter_kind") == "pi" {
		if argv := metaStrings(meta["argv"]); len(argv) >= 2 {
			var info map[string]any
			ok, detail, info = probePiRuntime(argv)
			if ok {
				for k, v := range info {
					meta[k] = v
				}
			}
		}
	}
	meta["last_probe_at"] = nowStr
	meta["observed_at"] = nowStr
	meta["auto_probe"] = true
	if interval > 0 {
		meta["heartbeat_interval_sec"] = interval
	}
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
	return ok, detail
}

// probeNodeNow 立刻探测一个节点并回写证据，不看间隔、不排队。
//
// 用在「打开自动执行」这类必须当场知道节点能不能接活的场景：匹配层只认
// 5 分钟内的观测，等下一次周期探测意味着开关先失灵几分钟。
func probeNodeNow(gdb, nodeID string) error {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return fmt.Errorf("probeNodeNow: 缺少 node_id")
	}
	return db.WithTx(gdb, func(tx *sql.Tx) error {
		n, err := db.GetNode(tx, nodeID)
		if err != nil {
			return err
		}
		if n == nil {
			return fmt.Errorf("节点 %s 不存在", nodeID)
		}
		meta := n.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		applyNodeProbe(meta, time.Now().UTC(), nodeHeartbeatInterval(meta))
		n.Meta = meta
		n.LastSeen = time.Now().UTC().Format(time.RFC3339)
		return db.UpsertNode(tx, n)
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
