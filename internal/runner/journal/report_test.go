package journal

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWriteReportPersistsFacts 验证 report.json 落到 Run 目录、权限 0600，
// 并且读回来还是同一份事实。
func TestWriteReportPersistsFacts(t *testing.T) {
	dir := t.TempDir()
	code := 7
	rep := Report{
		RunID:       "run-1",
		OperationID: "op-1",
		Outcome:     "failed",
		Phase:       "finished",
		Finished:    true,
		ExitCode:    &code,
		Appended:    12,
		Submitted:   11,
		Pending:     1,
		WrittenAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC).Format(time.RFC3339Nano),
	}
	path, _, err := WriteReport(dir, rep)
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if path != filepath.Join(dir, ReportFile) {
		t.Fatalf("报告路径 = %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("权限 = %o", info.Mode().Perm())
	}
	got, err := ReadReport(dir)
	if err != nil {
		t.Fatalf("ReadReport: %v", err)
	}
	if got.RunID != "run-1" || got.OperationID != "op-1" || got.Submitted != 11 || got.Pending != 1 {
		t.Fatalf("读回的 report 不一致: %+v", got)
	}
	if got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("退出码丢了: %+v", got.ExitCode)
	}
}

// TestWriteReportRedactsCredentials 验证报告与 journal 共用同一套秘密过滤：
// 报告能落盘，但凭据不会跟着落盘。
func TestWriteReportRedactsCredentials(t *testing.T) {
	dir := t.TempDir()
	rep := Report{RunID: "run-1", Stderr: `api_key=sk-abcdef123456`}
	path, n, err := WriteReport(dir, rep)
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if n == 0 {
		t.Fatalf("凭据没有被过滤")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(b), "sk-abcdef123456") {
		t.Fatalf("凭据仍在 report.json 里: %s", b)
	}
	if !strings.Contains(string(b), `"run_id"`) {
		t.Fatalf("报告的 run_id 丢了: %s", b)
	}
}

// TestDefaultMaxBytesIsBounded 说明日志预算有确定上限，且不是 0（0 表示无预算）。
func TestDefaultMaxBytesIsBounded(t *testing.T) {
	if DefaultMaxBytes <= 0 {
		t.Fatalf("默认日志预算应为正数，实际 %d", DefaultMaxBytes)
	}
	if DefaultMaxBytes > 64<<20 {
		t.Fatalf("默认预算过大，单次执行可能写出 %d 字节", DefaultMaxBytes)
	}
}
