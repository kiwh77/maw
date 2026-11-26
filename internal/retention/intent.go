package retention

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// IntentState 是删除意图的持久状态机。
type IntentState string

const (
	IntentPending  IntentState = "pending"
	IntentVerified IntentState = "verified"
	IntentDeleted  IntentState = "deleted"
	IntentFailed   IntentState = "failed"
)

// Intent 是跨 DB／文件删除的持久意图（可重启续跑、幂等）。
type Intent struct {
	IntentID  string      `json:"intent_id"`
	Kind      TargetKind  `json:"kind"`
	Path      string      `json:"path"`
	RunID     string      `json:"run_id,omitempty"`
	Artifact  string      `json:"artifact,omitempty"`
	State     IntentState `json:"state"`
	Attempts  int         `json:"attempts"`
	LastError string      `json:"last_error,omitempty"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
}

// Presence 是查询侧对路径的结论。
type Presence string

const (
	// PresencePresent 文件仍在，未被本模块清理。
	PresencePresent Presence = "present"
	// PresenceProtected 评估为保留（占用／证据等）。
	PresenceProtected Presence = "protected"
	// PresenceCleaned 有 deleted 意图且路径已不存在。
	PresenceCleaned Presence = "cleaned"
	// PresenceMissing 无意图记录且路径不存在——缺测，不能当成已清理。
	PresenceMissing Presence = "missing"
	// PresenceFailed 最近意图失败，路径状态需人工核实。
	PresenceFailed Presence = "failed"
)

// Ledger 持久化意图；默认落在 ledgerDir 下的 JSON 文件。
type Ledger struct {
	mu  sync.Mutex
	dir string
}

// OpenLedger 打开／创建意图账本目录。
func OpenLedger(dir string) (*Ledger, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("retention: ledger dir required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Ledger{dir: dir}, nil
}

func intentIDFor(kind TargetKind, path string) string {
	sum := sha256.Sum256([]byte(string(kind) + "\n" + filepath.Clean(path)))
	return "ri_" + hex.EncodeToString(sum[:8])
}

func (l *Ledger) pathFor(id string) string {
	return filepath.Join(l.dir, id+".json")
}

// Put 写入意图（同 id 覆盖为推进状态）。
func (l *Ledger) Put(in Intent) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if in.IntentID == "" {
		in.IntentID = intentIDFor(in.Kind, in.Path)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if in.CreatedAt == "" {
		in.CreatedAt = now
	}
	in.UpdatedAt = now
	raw, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	tmp := l.pathFor(in.IntentID) + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, l.pathFor(in.IntentID))
}

// Get 读取意图；不存在返回 nil。
func (l *Ledger) Get(id string) (*Intent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	raw, err := os.ReadFile(l.pathFor(id))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var in Intent
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// GetByPath 按 kind+path 稳定 id 查找。
func (l *Ledger) GetByPath(kind TargetKind, path string) (*Intent, error) {
	return l.Get(intentIDFor(kind, path))
}

// List 返回全部意图（按 IntentID 排序）。
func (l *Ledger) List() ([]Intent, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var out []Intent
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(l.dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var in Intent
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IntentID < out[j].IntentID })
	return out, nil
}
