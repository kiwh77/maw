package journal

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSpecRedactsSecretsAndFreezesOnce(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_spec"})
	spec := map[string]any{
		"run_id": "run_spec",
		"workspace": map[string]any{
			"path":   "/tmp/worktree",
			"branch": "agent/run_spec",
		},
		"session": map[string]any{
			"resume_session_id": "sess-1",
			"authorization":     "Bearer abcdefghijkl",
		},
		"env":   map[string]any{"API_KEY": "supersecret1234"},
		"input": "use token: abcdef123456 now",
	}
	rep, err := j.WriteSpec(spec)
	if err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}
	if rep.Redactions != 3 {
		t.Fatalf("脱敏次数 = %d，期望 3", rep.Redactions)
	}
	if !rep.Available {
		t.Fatal("新写入的规格报告应可用")
	}
	data, err := os.ReadFile(filepath.Join(dir, SpecFile))
	if err != nil {
		t.Fatalf("读取规格: %v", err)
	}
	for _, secret := range []string{"supersecret1234", "abcdefghijkl", "abcdef123456"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("秘密 %q 落盘", secret)
		}
	}
	if !strings.Contains(string(data), Redacted) {
		t.Fatal("缺少脱敏占位")
	}
	if strings.Contains(string(data), "sess-1") == false {
		t.Fatal("非秘密的会话 ID 不应被删除")
	}

	same, err := j.WriteSpec(spec)
	if err != nil {
		t.Fatalf("相同规格应幂等: %v", err)
	}
	if same.Redactions != rep.Redactions || !same.Available {
		t.Fatalf("幂等写入报告不一致: %+v", same)
	}
	if _, err := j.WriteSpec(map[string]any{"run_id": "run_other"}); !errors.Is(err, ErrSpecFrozen) {
		t.Fatalf("不同规格应拒绝改写: %v", err)
	}
	raw, report, err := j.ReadSpec()
	if err != nil {
		t.Fatalf("ReadSpec: %v", err)
	}
	if report.Redactions != 3 || !report.Available || report.Bytes != len(raw) {
		t.Fatalf("读取报告 = %+v", report)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("规格不是合法 JSON: %v", err)
	}
}

func TestReadSpecWithoutReportIsExplicitlyUnavailable(t *testing.T) {
	dir := t.TempDir()
	j := openTestJournal(t, dir, Options{RunID: "run_spec_meta"})
	if _, err := j.WriteSpec(map[string]any{"run_id": "run_spec_meta"}); err != nil {
		t.Fatalf("WriteSpec: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, SpecMeta)); err != nil {
		t.Fatalf("删除规格报告: %v", err)
	}
	raw, report, err := j.ReadSpec()
	if err != nil {
		t.Fatalf("ReadSpec: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("规格内容不应丢失")
	}
	if report.Available {
		t.Fatal("缺少报告时不得声称脱敏信息可用")
	}
	if report.Bytes != len(raw) {
		t.Fatalf("报告字节 = %d，实际 %d", report.Bytes, len(raw))
	}
}

func TestRedactJSONRejectsNonJSON(t *testing.T) {
	if _, _, err := RedactJSON([]byte("plain text")); err == nil {
		t.Fatal("非 JSON 负载应拒绝")
	}
}
