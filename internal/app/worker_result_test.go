package app

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/db"
)

// TestSubmitResultValidation 覆盖契约 §3 的形状校验：不合法整次拒绝，不做部分写入。
func TestSubmitResultValidation(t *testing.T) {
	wt := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wt, ".maw"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(s string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(wt, ".maw", "result.json"), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bad := map[string]string{
		"非法 JSON":      `{summary:`,
		"缺 summary":    `{"artifacts":[{"path":"a.txt"}]}`,
		"空白 summary":   `{"summary":"   "}`,
		"artifact 缺路径": `{"summary":"ok","artifacts":[{"path":"  "}]}`,
		"check 缺文案":    `{"summary":"ok","acceptance_checks":[{"text":"","ok":true}]}`,
	}
	for name, payload := range bad {
		write(payload)
		if _, err := loadSubmitResult(wt); err == nil {
			t.Fatalf("%s 应被拒绝", name)
		} else if !strings.Contains(err.Error(), "invalid_result") {
			t.Fatalf("%s 的错误码应为 invalid_result: %v", name, err)
		}
	}

	// artifacts 超过 64 条
	var many []string
	for i := 0; i < MaxResultArtifacts+1; i++ {
		many = append(many, `{"path":"f`+string(rune('a'+i%26))+`.txt"}`)
	}
	write(`{"summary":"ok","artifacts":[` + strings.Join(many, ",") + `]}`)
	if _, err := loadSubmitResult(wt); err == nil || !strings.Contains(err.Error(), "invalid_result") {
		t.Fatalf("超过 %d 条产物应被拒绝: %v", MaxResultArtifacts, err)
	}

	// 合法载荷：能解析且 summary 去空白
	write(`{"summary":"  收工  ","artifacts":[{"path":"a.txt","kind":"file"}],"acceptance_checks":[{"text":"单测绿","ok":true}]}`)
	res, err := loadSubmitResult(wt)
	if err != nil {
		t.Fatalf("合法 result 不该被拒绝: %v", err)
	}
	if res.Summary != "收工" || len(res.Artifacts) != 1 || !res.AcceptanceChecks[0].OK {
		t.Fatalf("解析结果不对: %+v", res)
	}
}

// TestSubmitResultAbsentMeansNoResult 覆盖兼容路径：没有 result 时不报错、不返回载荷。
func TestSubmitResultAbsentMeansNoResult(t *testing.T) {
	wt := t.TempDir()
	res, err := loadSubmitResult(wt)
	if err != nil {
		t.Fatalf("缺 result 不应报错: %v", err)
	}
	if res != nil {
		t.Fatalf("缺 result 应返回 nil，得到 %+v", res)
	}
}

// TestSubmitResultEnvWins 覆盖环境变量入口（脚本可 JSON 直传，不必写文件）。
func TestSubmitResultEnvWins(t *testing.T) {
	wt := t.TempDir()
	t.Setenv("MAW_SUBMIT_RESULT", `{"summary":"来自环境变量"}`)
	res, err := loadSubmitResult(wt)
	if err != nil || res == nil || res.Summary != "来自环境变量" {
		t.Fatalf("环境变量入口失效: %+v %v", res, err)
	}
}

// TestSubmitWithResultRecordsEvent 覆盖端到端：带 result 的 submit 之后事件可读回。
func TestSubmitWithResultRecordsEvent(t *testing.T) {
	f := newFixture(t)
	const id = "RES-1"
	f.seedTask(id, "ready", "", []string{"mod_res/**"})
	_ = os.MkdirAll(filepath.Join(f.repo, "mod_res"), 0o755)

	t.Setenv("MAW_AGENT", "cursor-author")
	if _, err := Next(f.repo, "cursor-author", false, ""); err != nil {
		t.Fatalf("next: %v", err)
	}
	wt := filepath.Join(f.repo, ".worktrees", "cursor-author")
	writeAndCommit(t, wt, "mod_res/a.txt", "a\n", "res")

	resJSON := `{"summary":"补齐 result 载荷","artifacts":[{"path":"mod_res/a.txt"}],"acceptance_checks":[{"text":"事件可读回","ok":true}]}`
	if err := os.WriteFile(filepath.Join(wt, ".maw", "result.json"), []byte(resJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := Submit(wt, id, false)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if got, _ := out["result_summary"].(string); got != "补齐 result 载荷" {
		t.Fatalf("结果里应回显 summary: %v", out["result_summary"])
	}

	var found map[string]any
	if err := db.WithTx(f.pc.GlobalDB, func(tx *sql.Tx) error {
		events, e := db.ListEventsByTask(tx, f.pc.ID(), id, 200)
		if e != nil {
			return e
		}
		for _, ev := range events {
			if ev.Type == "task_result" {
				found = ev.Data
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal("缺少 task_result 事件")
	}
	if found["summary"] != "补齐 result 载荷" {
		t.Fatalf("事件 summary=%v", found["summary"])
	}
	arts, _ := found["artifacts"].([]any)
	if len(arts) != 1 {
		b, _ := json.Marshal(found)
		t.Fatalf("事件 artifacts 不对: %s", b)
	}
	if found["delivery_id"] == "" || found["delivery_id"] == nil {
		t.Fatalf("事件应带 delivery_id: %#v", found["delivery_id"])
	}
}
