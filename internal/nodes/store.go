package nodes

import (
	"errors"
	"sort"
	"sync"

	"github.com/kiwh77/maw/internal/engine/model"
)

var (
	// ErrNotFound 表示节点尚未登记。
	ErrNotFound = errors.New("nodes: 节点未登记")
	// ErrExists 表示同一 node_id 已经登记过。
	ErrExists = errors.New("nodes: 节点已登记")
)

// Store 是节点注册表的持久化端口；实现负责按 node_id 唯一。
//
// 服务层已在自身互斥锁内做读改写，Store 只需保证单次 Load/Save 的原子性。
type Store interface {
	Load(id model.NodeID) (*Record, error)
	Save(rec *Record) error
	List() ([]*Record, error)
}

// MemoryStore 是测试与装配用的内存实现，返回深拷贝，避免调用方误改内部状态。
type MemoryStore struct {
	mu   sync.Mutex
	byID map[string]*Record
}

// NewMemoryStore 创建空注册表。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: map[string]*Record{}}
}

// Load 返回节点副本；不存在时返回 ErrNotFound。
func (m *MemoryStore) Load(id model.NodeID) (*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byID[string(id)]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneRecord(rec), nil
}

// Save 写入副本。
func (m *MemoryStore) Save(rec *Record) error {
	if rec == nil {
		return errors.New("nodes: 不能保存空记录")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byID[string(rec.NodeID)] = cloneRecord(rec)
	return nil
}

// List 按 node_id 升序返回全部节点副本。
func (m *MemoryStore) List() ([]*Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Record, 0, len(m.byID))
	for _, rec := range m.byID {
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out, nil
}

func cloneRecord(rec *Record) *Record {
	cp := *rec
	cp.Capabilities = append([]CapabilityEvidence(nil), rec.Capabilities...)
	cp.Invalidations = append([]Invalidation(nil), rec.Invalidations...)
	for i := range cp.Invalidations {
		cp.Invalidations[i].Capabilities = append([]string(nil), rec.Invalidations[i].Capabilities...)
	}
	return &cp
}
