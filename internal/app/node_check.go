package app

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
	httpresources "github.com/kiwh77/maw/internal/httpapi/resources"
)

// hubNodeChecks 把看板的「测试环境」按钮接到本机 CLI 自检。
//
// 与自动心跳的区别：自动探测只看入口文件在不在；这里会真的起一次
// `<cli> --version`，把版本、耗时与失败原因写回节点 meta，供看板展示。
type hubNodeChecks struct{ gdb string }

func (h hubNodeChecks) Check(ctx context.Context, nodeID model.NodeID) (httpresources.NodeCheckResult, error) {
	var out httpresources.NodeCheckResult
	err := db.WithTx(h.gdb, func(tx *sql.Tx) error {
		n, err := db.GetNode(tx, string(nodeID))
		if err != nil {
			return err
		}
		if n == nil {
			return httpresources.ErrNotFound{Message: "节点 " + string(nodeID) + " 不存在"}
		}
		meta := n.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		flavor, _ := meta["cli_flavor"].(string)
		argv := metaStrings(meta["argv"])
		entry := nodeEntryPath(meta)
		if entry == "" && strings.TrimSpace(flavor) != "" {
			if resolved, err := resolveCLIBinary(flavor); err == nil {
				entry = resolved
			}
		}

		started := time.Now()
		items := make([]httpresources.NodeCheckItem, 0, 4)
		exe, binErr := resolveCheckEntry(entry)
		items = append(items, httpresources.NodeCheckItem{
			Name: "binary", OK: binErr == "", Detail: firstNonEmpty(exe, binErr),
		})

		version := ""
		versionErr := ""
		if binErr == "" {
			version, versionErr = runVersionCheck(ctx, exe)
		}
		items = append(items, httpresources.NodeCheckItem{
			Name: "version", OK: version != "", Detail: firstNonEmpty(version, versionErr),
		})

		argDetail := strings.Join(argv, " ")
		if argDetail == "" {
			argDetail = "未登记 argv"
		}
		items = append(items, httpresources.NodeCheckItem{
			Name: "argv", OK: len(argv) >= 2, Detail: argDetail,
		})

		modelName := effectiveModel(meta, "")
		modelDetail := modelName
		if modelDetail == "" {
			modelDetail = "未配置，沿用 CLI 默认"
		}
		items = append(items, httpresources.NodeCheckItem{Name: "model", OK: true, Detail: modelDetail})

		ok := binErr == "" && version != ""
		latency := time.Since(started).Milliseconds()
		now := time.Now().UTC().Format(time.RFC3339)
		errText := joinCheckErrors(binErr, versionErr)

		meta["last_probe_at"] = now
		meta["observed_at"] = now
		meta["auto_probe"] = nodeAutoProbeEnabled(meta)
		if version != "" {
			meta["cli_version"] = version
		}
		capVersion := version
		if capVersion == "" {
			capVersion = strings.TrimSpace(flavor)
		}
		meta["capabilities"] = map[string]any{
			"declared":         []string{"local_cli"},
			"verified":         []string{"local_cli"},
			"cli_version":      capVersion,
			"verified_version": capVersion,
		}
		meta["last_check"] = map[string]any{
			"ok":         ok,
			"version":    version,
			"latency_ms": latency,
			"checked_at": now,
			"checks":     items,
			"error":      errText,
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
			meta["last_probe_error"] = errText
		}
		n.Meta = meta
		if err := db.UpsertNode(tx, n); err != nil {
			return err
		}

		out = httpresources.NodeCheckResult{
			NodeID: nodeID, OK: ok, Version: version, LatencyMS: latency,
			CheckedAt: now, Checks: items, Error: errText, Meta: meta,
		}
		return nil
	})
	if err != nil {
		return httpresources.NodeCheckResult{}, err
	}
	return out, nil
}

// resolveCheckEntry 解析入口：绝对路径看文件，相对命令名交给 LookPath。
func resolveCheckEntry(entry string) (string, string) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", "缺少 entry_path／argv[0]"
	}
	if filepath.IsAbs(entry) {
		st, err := os.Stat(entry)
		if err != nil {
			return "", err.Error()
		}
		if st.IsDir() {
			return "", "entry 指向目录: " + entry
		}
		return entry, ""
	}
	resolved, err := exec.LookPath(entry)
	if err != nil {
		return "", err.Error()
	}
	return resolved, ""
}

// runVersionCheck 起一次 `--version`，只取首个非空行作为版本号。
func runVersionCheck(ctx context.Context, exe string) (string, string) {
	cmd := exec.CommandContext(ctx, exe, "--version")
	out, err := cmd.CombinedOutput()
	line := firstLine(string(out))
	if err != nil {
		if line != "" {
			return "", line
		}
		return "", err.Error()
	}
	if line == "" {
		return "", "命令没有输出"
	}
	return line, ""
}

func firstLine(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

func joinCheckErrors(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, "；")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// metaStrings 兼容 meta 里 argv 的两种落盘形状（[]any / []string）。
func metaStrings(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
