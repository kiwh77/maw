package retention

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrProtected 表示护栏拒绝删除。
var ErrProtected = errors.New("retention: protected by occupancy or evidence")

// ErrOutsideRoot 拒绝清理根目录之外的路径（不碰用户目录）。
var ErrOutsideRoot = errors.New("retention: path outside allowed root")

// GuardFunc 在执行前再次读取运行护栏（防止计划后新占用）。
type GuardFunc func(runID string) (RunGuard, error)

// Service 编排评估、意图与文件删除对账。
type Service struct {
	Policy Policy
	Ledger *Ledger
	// Root 允许删除的根（通常为 ~/.maw）；空则拒绝一切文件删除。
	Root string
	// Guard 可选；提供时 Execute 前复检占用。
	Guard GuardFunc
	// Remove 可注入（测试失败／并发）；默认 os.RemoveAll。
	Remove func(path string) error
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *Service) remove(path string) error {
	if s.Remove != nil {
		return s.Remove(path)
	}
	return os.RemoveAll(path)
}

// Plan 对候选做评估；可 purge 的写入 pending 意图并返回。已存在 deleted 意图则跳过（幂等）。
func (s *Service) Plan(candidates []Candidate, guards map[string]RunGuard) ([]Intent, error) {
	if s.Ledger == nil {
		return nil, errors.New("retention: ledger required")
	}
	var out []Intent
	for _, c := range candidates {
		if !c.Kind.Valid() || strings.TrimSpace(c.Path) == "" {
			continue
		}
		g := guards[c.RunID]
		d := Evaluate(s.Policy, c, g)
		if d.Action != ActionPurge {
			continue
		}
		id := intentIDFor(c.Kind, c.Path)
		existing, err := s.Ledger.Get(id)
		if err != nil {
			return nil, err
		}
		if existing != nil && existing.State == IntentDeleted {
			continue // 幂等：已清理不再建意图
		}
		in := Intent{
			IntentID: id,
			Kind:     c.Kind,
			Path:     filepath.Clean(c.Path),
			RunID:    c.RunID,
			Artifact: c.Artifact,
			State:    IntentPending,
		}
		if existing != nil {
			in.CreatedAt = existing.CreatedAt
			in.Attempts = existing.Attempts
		}
		if err := s.Ledger.Put(in); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, nil
}

// Execute 核实意图目标后删除；失败记 failed 可重试，成功态重入保持幂等。
func (s *Service) Execute(intentID string) (*Intent, error) {
	if s.Ledger == nil {
		return nil, errors.New("retention: ledger required")
	}
	in, err := s.Ledger.Get(intentID)
	if err != nil {
		return nil, err
	}
	if in == nil {
		return nil, fmt.Errorf("retention: intent %s not found", intentID)
	}
	if in.State == IntentDeleted {
		return in, nil // 幂等成功
	}
	if err := s.ensureUnderRoot(in.Path); err != nil {
		in.State = IntentFailed
		in.LastError = err.Error()
		in.Attempts++
		_ = s.Ledger.Put(*in)
		return in, err
	}

	// 执行前复检护栏
	if s.Guard != nil && in.RunID != "" {
		g, err := s.Guard(in.RunID)
		if err != nil {
			in.State = IntentFailed
			in.LastError = err.Error()
			in.Attempts++
			_ = s.Ledger.Put(*in)
			return in, err
		}
		d := Evaluate(s.Policy, Candidate{Kind: in.Kind, Path: in.Path, RunID: in.RunID, Artifact: in.Artifact, Age: s.Policy.RunDirMaxAge + time.Hour}, g)
		if d.Action == ActionRetain {
			in.State = IntentFailed
			in.LastError = ErrProtected.Error() + ": " + d.Explain()
			in.Attempts++
			_ = s.Ledger.Put(*in)
			return in, ErrProtected
		}
	}

	in.State = IntentVerified
	in.Attempts++
	if err := s.Ledger.Put(*in); err != nil {
		return in, err
	}

	if err := s.remove(in.Path); err != nil {
		in.State = IntentFailed
		in.LastError = err.Error()
		_ = s.Ledger.Put(*in)
		return in, err
	}

	in.State = IntentDeleted
	in.LastError = ""
	if err := s.Ledger.Put(*in); err != nil {
		return in, err
	}
	return in, nil
}

// RunPending 执行账本中所有非 deleted 意图；单条失败不中断其余。
func (s *Service) RunPending() (done, failed int, err error) {
	list, err := s.Ledger.List()
	if err != nil {
		return 0, 0, err
	}
	for _, in := range list {
		if in.State == IntentDeleted {
			continue
		}
		if _, e := s.Execute(in.IntentID); e != nil {
			failed++
			continue
		}
		done++
	}
	return done, failed, nil
}

// Query 区分 present／protected／cleaned／missing／failed。
func (s *Service) Query(kind TargetKind, path string, g RunGuard) (Presence, *Intent, error) {
	path = filepath.Clean(path)
	var in *Intent
	var err error
	if s.Ledger != nil {
		in, err = s.Ledger.GetByPath(kind, path)
		if err != nil {
			return "", nil, err
		}
	}
	_, statErr := os.Stat(path)
	exists := statErr == nil
	if in != nil && in.State == IntentFailed {
		return PresenceFailed, in, nil
	}
	if in != nil && in.State == IntentDeleted {
		if exists {
			// 意图称已删但文件又出现——按 present 回报，供对账
			return PresencePresent, in, nil
		}
		return PresenceCleaned, in, nil
	}
	d := Evaluate(s.Policy, Candidate{Kind: kind, Path: path, RunID: g.RunID, Age: 0}, g)
	if d.Action == ActionRetain && (g.Occupies || g.ReconciliationUnknown || g.PendingJournal > 0 || g.DeliveryRefs > 0) {
		if exists {
			return PresenceProtected, in, nil
		}
		// 受保护但路径不见：缺测，不是 cleaned
		return PresenceMissing, in, nil
	}
	if exists {
		return PresencePresent, in, nil
	}
	if in == nil {
		return PresenceMissing, nil, nil
	}
	return PresenceMissing, in, nil
}

func (s *Service) ensureUnderRoot(path string) error {
	if strings.TrimSpace(s.Root) == "" {
		return ErrOutsideRoot
	}
	root, err := filepath.Abs(s.Root)
	if err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ErrOutsideRoot
	}
	return nil
}
