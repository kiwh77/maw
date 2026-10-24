package trae

import (
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// EntryClass 是 Trae 入口分级。
type EntryClass string

const (
	// ClassIDEChat 是 IDE 内人工对话入口：只能 manual_client。
	ClassIDEChat EntryClass = "ide_chat"
	// ClassCLI 是独立已授权 CLI：经探测后才可开受管能力。
	ClassCLI EntryClass = "cli"
	// ClassUnknown 无法判定：不开放受管。
	ClassUnknown EntryClass = "unknown"
)

// EvidenceRoot 是证据目录。
const EvidenceRoot = "docs/evidence/trae"

// MeasuredCLIVersion 是矩阵成立的 CLI 版本；仅对该版本开放 observed 受管能力。
const MeasuredCLIVersion model.SemVer = "1.0.0"

// Classify 按节点模式与入口路径判定分级；IDE 与 CLI 不互相继承。
func Classify(cfg model.NodeConfig) EntryClass {
	if cfg.Mode == model.NodeManualClient {
		return ClassIDEChat
	}
	path := strings.ToLower(cfg.EntryPath)
	base := strings.ToLower(filepath.Base(cfg.EntryPath))
	// 常见 IDE 包路径／占位：明确不是独立 CLI
	if strings.Contains(path, "trae.app") || strings.Contains(path, "/trae/") && strings.Contains(path, "chat") ||
		base == "trae" && strings.HasSuffix(path, ".app/contents/macos/trae") ||
		strings.Contains(cfg.ProfileRef, "ide") || strings.Contains(cfg.ProfileRef, "chat") {
		return ClassIDEChat
	}
	if cfg.Mode == model.NodeManagedCLI {
		return ClassCLI
	}
	// 显式 cli profile
	if strings.Contains(cfg.ProfileRef, "cli") {
		return ClassCLI
	}
	return ClassUnknown
}
