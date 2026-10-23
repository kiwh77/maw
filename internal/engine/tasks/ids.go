package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/kiwh77/maw/internal/engine/model"
)

// IDAllocator 为新建实例分配稳定身份。测试可注入确定性分配器。
type IDAllocator interface {
	New() (model.InstanceID, error)
}

// RandomIDs 用密码学随机数生成 ti_ 前缀实例号；不占用显示编号形态。
type RandomIDs struct{}

// New 实现 IDAllocator。
func (RandomIDs) New() (model.InstanceID, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tasks: 生成实例号: %w", err)
	}
	return model.InstanceID("ti_" + hex.EncodeToString(b[:])), nil
}
