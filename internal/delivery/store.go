package delivery

import (
	"strings"
	"sync"

	"github.com/kiwh77/maw/internal/engine/model"
)

// Violations 是一组契约违反；非空即「这次冻结有硬问题」。
type Violations []*model.Error

func (v Violations) Error() string {
	if len(v) == 0 {
		return ""
	}
	parts := make([]string, 0, len(v))
	for _, e := range v {
		parts = append(parts, e.Error())
	}
	return strings.Join(parts, "; ")
}

// Store 是冻结结果的持久化边界。
//
// 本卡只定义接口与内存实现：落库路径由后续卡接（DB 是 infra，交付包不认识 sql）。
type Store interface {
	// Save 保存一条已冻结的交付；同 ID 重复保存必须是幂等覆盖而不是新增。
	Save(d model.Delivery) error
	// Get 按 ID 取回已冻结的交付。
	Get(id model.DeliveryID) (model.Delivery, bool, error)
}

// MemoryStore 是进程内实现（测试与单跑用）。
type MemoryStore struct {
	mu   sync.Mutex
	byID map[model.DeliveryID]model.Delivery
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{byID: make(map[model.DeliveryID]model.Delivery)}
}

// Save 覆盖写入。
func (s *MemoryStore) Save(d model.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byID == nil {
		s.byID = make(map[model.DeliveryID]model.Delivery)
	}
	s.byID[d.DeliveryID] = d
	return nil
}

// Get 按 ID 取回。
func (s *MemoryStore) Get(id model.DeliveryID) (model.Delivery, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byID[id]
	return d, ok, nil
}
