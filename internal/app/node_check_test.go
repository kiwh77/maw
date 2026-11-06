package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
	"github.com/kiwh77/maw/internal/engine/model"
)

// seedCheckNode 造一个本工程节点，返回 node id。
func seedCheckNode(t *testing.T, f *fixture, nodeID string, meta map[string]any) {
	t.Helper()
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		return db.UpsertNode(tx, &db.Node{
			ID: nodeID, ProjectID: f.pc.ID(), Label: nodeID, SlotLimit: 1, Meta: meta,
		})
	}); err != nil {
		t.Fatalf("register node: %v", err)
	}
}

// writeFakeCLI 写一个只会打印版本的假 CLI，避免测试依赖本机真实工具。
func writeFakeCLI(t *testing.T, versionLine string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-cli")
	body := "#!/bin/sh\necho '" + versionLine + "'\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write fake cli: %v", err)
	}
	return path
}

// 环境正常：二进制存在且 --version 有输出，应回写版本与四维 ready。
func TestNodeCheckMarksReadyOnVersionOutput(t *testing.T) {
	f := newFixture(t)
	script := writeFakeCLI(t, "fake-cli 9.9.9")
	nodeID := "claude-local-test"
	seedCheckNode(t, f, nodeID, map[string]any{
		"cli_flavor":      "claude",
		"argv":            []string{script, "-p", taskPrompt},
		"default_model":   "test-model",
		"permission_mode": "safe",
	})

	out, err := hubNodeChecks{gdb: f.pc.GlobalDB}.Check(context.Background(), model.NodeID(nodeID))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !out.OK {
		t.Fatalf("环境应判定正常: %+v", out)
	}
	if !strings.Contains(out.Version, "9.9.9") {
		t.Fatalf("version=%q", out.Version)
	}
	if len(out.Checks) != 4 {
		t.Fatalf("checks=%+v", out.Checks)
	}
	for _, item := range out.Checks {
		if !item.OK {
			t.Fatalf("检查项 %s 应通过: %+v", item.Name, item)
		}
	}
	last, _ := out.Meta["last_check"].(map[string]any)
	if last == nil || last["ok"] != true {
		t.Fatalf("meta.last_check 未落盘: %+v", out.Meta)
	}
	if out.Meta["entry_state"] != "ready" || out.Meta["auth_state"] != "ready" {
		t.Fatalf("四维状态未回写: %+v", out.Meta)
	}
	caps, _ := out.Meta["capabilities"].(map[string]any)
	if caps == nil || caps["verified_version"] != out.Version {
		t.Fatalf("capabilities 未记录版本: %+v", out.Meta)
	}
}

// 环境异常：入口不存在时不得误判 ready，且失败原因要能展示。
func TestNodeCheckReportsMissingBinary(t *testing.T) {
	f := newFixture(t)
	nodeID := "dsh-local-test"
	seedCheckNode(t, f, nodeID, map[string]any{
		"cli_flavor": "dsh",
		"argv":       []string{"/nonexistent/maw-fake-cli", "--profile", "headless"},
	})

	out, err := hubNodeChecks{gdb: f.pc.GlobalDB}.Check(context.Background(), model.NodeID(nodeID))
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.OK {
		t.Fatalf("缺失二进制不能判正常: %+v", out)
	}
	if strings.TrimSpace(out.Error) == "" {
		t.Fatal("失败必须带原因")
	}
	if out.Meta["entry_state"] != "unavailable" {
		t.Fatalf("entry_state=%v", out.Meta["entry_state"])
	}
	if out.Meta["last_probe_ok"] != false {
		t.Fatalf("last_probe_ok=%v", out.Meta["last_probe_ok"])
	}
}

// 未知节点要返回 not_found，而不是落一条空 meta。
func TestNodeCheckUnknownNode(t *testing.T) {
	f := newFixture(t)
	_, err := hubNodeChecks{gdb: f.pc.GlobalDB}.Check(context.Background(), model.NodeID("no-such-node"))
	if err == nil {
		t.Fatal("未知节点应报错")
	}
}
