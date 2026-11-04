package journal

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReportFile 是 Run 目录里的执行报告文件名。
const ReportFile = "report.json"

// DefaultMaxBytes 是单次执行的日志总量预算（8 MiB）。
//
// 预算按「一次执行」计，不是全局配额：超出后 Append 按预算裁剪并记录裁剪量，
// 免得长跑或刷屏的任务把本机磁盘写满。
const DefaultMaxBytes int64 = 8 << 20

// Report 是执行完成后落盘的只读事实快照。
//
// 它只记录事实与计数（阶段、退出码、补交数量与失败诊断），不带完整提示词、
// 环境变量或凭据；写盘前仍走 RedactJSON，与 journal 采用同一套秘密过滤口径。
type Report struct {
	RunID               string `json:"run_id"`
	OperationID         string `json:"operation_id,omitempty"`
	Outcome             string `json:"outcome,omitempty"`
	Phase               string `json:"phase,omitempty"`
	Finished            bool   `json:"finished"`
	ExitCode            *int   `json:"exit_code,omitempty"`
	Appended            int    `json:"appended"`
	Decoded             int    `json:"decoded"`
	Submitted           int    `json:"submitted"`
	Pending             int    `json:"pending"`
	ObservationFailures int64  `json:"observation_failures,omitempty"`
	Stderr              string `json:"stderr,omitempty"`
	InterpretError      string `json:"interpret_error,omitempty"`
	Stop                string `json:"stop,omitempty"`
	Error               string `json:"error,omitempty"`
	WrittenAt           string `json:"written_at"`
}

// WriteReport 原子写入 Run 目录的 report.json，返回路径与秘密过滤条数。
func WriteReport(dir string, rep Report) (string, int, error) {
	if strings.TrimSpace(dir) == "" {
		return "", 0, fmt.Errorf("journal: 缺少 Run 目录")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", 0, fmt.Errorf("journal: 建立 Run 目录: %w", err)
	}
	if rep.WrittenAt == "" {
		rep.WrittenAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", 0, fmt.Errorf("journal: 编码 report: %w", err)
	}
	data = append(data, '\n')
	redacted, n, err := RedactJSON(data)
	if err != nil {
		return "", 0, fmt.Errorf("journal: 过滤 report: %w", err)
	}
	path := filepath.Join(dir, ReportFile)
	if err := writeFileAtomic(path, redacted, 0o600); err != nil {
		return "", 0, fmt.Errorf("journal: 写入 report: %w", err)
	}
	return path, n, nil
}

// ReadReport 读取 Run 目录里的 report.json。
func ReadReport(dir string) (Report, error) {
	b, err := os.ReadFile(filepath.Join(dir, ReportFile))
	if err != nil {
		return Report{}, fmt.Errorf("journal: 读取 report: %w", err)
	}
	var rep Report
	if err := json.Unmarshal(b, &rep); err != nil {
		return Report{}, fmt.Errorf("journal: 解析 report: %w", err)
	}
	return rep, nil
}
