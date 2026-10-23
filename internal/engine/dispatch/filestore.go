package dispatch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kiwh77/maw/internal/engine/model"
)

// FileStore 把意图存放在状态目录下，每个操作一个 JSON 文件。
//
// 写路径是「同目录临时文件 + rename」：中枢在任意时刻被杀掉，读者要么看到旧文件，
// 要么看到完整的新文件，不会看到半个 JSON。
type FileStore struct {
	dir string
}

// NewFileStore 返回以 <state>/dispatch/intents 为根的意图存储。
func NewFileStore(stateDir string) *FileStore {
	return &FileStore{dir: intentDir(stateDir)}
}

// Dir 返回意图目录，便于排障。
func (s *FileStore) Dir() string { return s.dir }

// Load 读取一个操作的意图；文件不存在时返回 (nil, nil)。
func (s *FileStore) Load(id model.OperationID) (*Intent, error) {
	name, err := safeOpFile(id)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.dir, name))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("dispatch: 读取意图 %s: %w", id, err)
	}
	var it Intent
	if err := json.Unmarshal(data, &it); err != nil {
		return nil, fmt.Errorf("dispatch: 解析意图 %s: %w", id, err)
	}
	if err := it.Validate(); err != nil {
		return nil, err
	}
	return &it, nil
}

// Save 原子写入意图并补全时间戳。
func (s *FileStore) Save(it *Intent) error {
	if it == nil {
		return errors.New("dispatch: 不能保存空意图")
	}
	if err := it.Validate(); err != nil {
		return err
	}
	name, err := safeOpFile(it.OperationID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("dispatch: 创建意图目录: %w", err)
	}
	data, err := json.MarshalIndent(it, "", "  ")
	if err != nil {
		return fmt.Errorf("dispatch: 编码意图 %s: %w", it.OperationID, err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(s.dir, "."+name+".tmp-*")
	if err != nil {
		return fmt.Errorf("dispatch: 创建意图临时文件 %s: %w", it.OperationID, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("dispatch: 写意图 %s: %w", it.OperationID, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("dispatch: 同步意图 %s: %w", it.OperationID, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("dispatch: 关闭意图 %s: %w", it.OperationID, err)
	}
	if err := os.Rename(tmpName, filepath.Join(s.dir, name)); err != nil {
		return fmt.Errorf("dispatch: 替换意图 %s: %w", it.OperationID, err)
	}
	return nil
}

// List 按更新时间升序返回全部意图。
func (s *FileStore) List() ([]*Intent, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("dispatch: 列意图目录: %w", err)
	}
	out := make([]*Intent, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var it Intent
		data, err := os.ReadFile(filepath.Join(s.dir, entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("dispatch: 读取意图 %s: %w", entry.Name(), err)
		}
		if err := json.Unmarshal(data, &it); err != nil {
			return nil, fmt.Errorf("dispatch: 解析意图 %s: %w", entry.Name(), err)
		}
		if err := it.Validate(); err != nil {
			return nil, err
		}
		out = append(out, &it)
	}
	sortIntents(out)
	return out, nil
}

// 编译期确认 FileStore 实现了端口。
var _ IntentStore = (*FileStore)(nil)
