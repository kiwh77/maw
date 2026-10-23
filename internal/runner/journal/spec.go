package journal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SpecReport 是规格落盘的脱敏报告。ReadSpec 返回 Available=false 表示
// 规格文件存在但缺少脱敏报告（例如在两次原子改名之间中断），此时
// 不能断定规格完整。
type SpecReport struct {
	Redactions int       `json:"redactions"`
	Bytes      int       `json:"bytes"`
	WrittenAt  time.Time `json:"written_at,omitempty"`
	Available  bool      `json:"-"`
}

// WriteSpec 原子落盘冻结规格，并过滤其中的秘密。同一 Run 的规格只能写一次：
// 内容相同视为幂等，内容不同返回 ErrSpecFrozen。
func (j *Journal) WriteSpec(spec any) (SpecReport, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return SpecReport{}, ErrClosed
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return SpecReport{}, fmt.Errorf("journal: 序列化规格: %w", err)
	}
	clean, redactions, err := RedactJSON(raw)
	if err != nil {
		return SpecReport{}, fmt.Errorf("journal: 规格脱敏: %w", err)
	}
	path := filepath.Join(j.dir, SpecFile)
	if existing, err := os.ReadFile(path); err == nil {
		if !bytes.Equal(existing, clean) {
			return SpecReport{}, ErrSpecFrozen
		}
		return j.readSpecReport()
	} else if !errors.Is(err, os.ErrNotExist) {
		return SpecReport{}, fmt.Errorf("journal: 读取既有规格: %w", err)
	}
	if err := writeFileAtomic(path, clean, 0o600); err != nil {
		return SpecReport{}, fmt.Errorf("journal: 写冻结规格: %w", err)
	}
	rep := SpecReport{Redactions: redactions, Bytes: len(clean), WrittenAt: j.now().UTC(), Available: true}
	meta, err := json.Marshal(rep)
	if err != nil {
		return SpecReport{}, err
	}
	if err := writeFileAtomic(filepath.Join(j.dir, SpecMeta), meta, 0o600); err != nil {
		return SpecReport{}, fmt.Errorf("journal: 写规格报告: %w", err)
	}
	return rep, nil
}

// ReadSpec 读取冻结规格与它的脱敏报告。
func (j *Journal) ReadSpec() ([]byte, SpecReport, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(j.dir, SpecFile))
	if err != nil {
		return nil, SpecReport{}, fmt.Errorf("journal: 读取冻结规格: %w", err)
	}
	return data, j.readSpecReportLocked(len(data)), nil
}

func (j *Journal) readSpecReport() (SpecReport, error) {
	data, err := os.ReadFile(filepath.Join(j.dir, SpecFile))
	if err != nil {
		return SpecReport{}, fmt.Errorf("journal: 读取冻结规格: %w", err)
	}
	return j.readSpecReportLocked(len(data)), nil
}

func (j *Journal) readSpecReportLocked(specBytes int) SpecReport {
	data, err := os.ReadFile(filepath.Join(j.dir, SpecMeta))
	if err != nil {
		return SpecReport{Bytes: specBytes}
	}
	var rep SpecReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return SpecReport{Bytes: specBytes}
	}
	rep.Available = true
	rep.Bytes = specBytes
	return rep
}
