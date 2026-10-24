package zcode

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// EvidenceRoot 是证据目录。
const EvidenceRoot = "docs/evidence/zcode"

// MeasuredCLIVersion 是矩阵成立的 CLI 版本。
const MeasuredCLIVersion model.SemVer = "1.0.0"

// ConfigView 是显式配置就绪视图（三列分开）。
type ConfigView struct {
	RuntimeReady  bool   `json:"runtime_ready"`
	RuntimePath   string `json:"runtime_path,omitempty"`
	RuntimeNote   string `json:"runtime_note,omitempty"`
	ProviderReady bool   `json:"provider_ready"`
	ProviderRef   string `json:"provider_ref,omitempty"`
	ProviderNote  string `json:"provider_note,omitempty"`
	ModelReady    bool   `json:"model_ready"`
	ModelID       string `json:"model_id,omitempty"`
	ModelNote     string `json:"model_note,omitempty"`
	// ConfigMutated 永远为 false：本适配器不改用户配置。
	ConfigMutated bool `json:"config_mutated"`
}

// ResolveEntry 用受控引用解析入口：只接受绝对路径；相对路径拒绝（避免偷偷拼家目录）。
func ResolveEntry(cfg model.NodeConfig) (string, error) {
	p := strings.TrimSpace(cfg.EntryPath)
	if p == "" {
		return "", model.PortFailure(model.PortErrEntryMissing, "入口为空")
	}
	if !filepath.IsAbs(p) {
		return "", model.PortFailure(model.PortErrEntryMissing,
			"入口必须是绝对路径：路径补齐只使用受控配置引用，不自动拼接用户目录")
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", model.PortFailuref(model.PortErrEntryMissing, "入口不可用：%v", err)
	}
	if st.IsDir() || st.Mode().Perm()&0o111 == 0 {
		return "", model.PortFailure(model.PortErrEntryUnavailable, "入口不是可执行文件")
	}
	return p, nil
}

// ParseProfile 从 profile_ref 读取显式配置片段：zcode/provider=<id>/model=<id>。
// 缺段不猜测、不写回用户配置。
func ParseProfile(ref string) (provider, modelID string) {
	ref = strings.TrimSpace(ref)
	for _, part := range strings.Split(ref, "/") {
		if strings.HasPrefix(part, "provider=") {
			provider = strings.TrimPrefix(part, "provider=")
		}
		if strings.HasPrefix(part, "model=") {
			modelID = strings.TrimPrefix(part, "model=")
		}
	}
	return provider, modelID
}

// InspectConfig 生成三列就绪视图；永不修改磁盘配置。
func InspectConfig(cfg model.NodeConfig, runtimeOK bool) ConfigView {
	provider, modelID := ParseProfile(cfg.ProfileRef)
	v := ConfigView{
		RuntimeReady:  runtimeOK,
		RuntimePath:   cfg.RuntimePath,
		ProviderRef:   provider,
		ModelID:       modelID,
		ConfigMutated: false,
	}
	if !runtimeOK {
		v.RuntimeNote = "运行时未就绪或未探测"
	} else {
		v.RuntimeNote = "运行时可执行"
	}
	if provider == "" {
		v.ProviderReady = false
		v.ProviderNote = "profile_ref 缺少 provider=；不自动补齐"
	} else {
		v.ProviderReady = true
		v.ProviderNote = "provider 已由受控引用给出"
	}
	if modelID == "" {
		v.ModelReady = false
		v.ModelNote = "缺模型：不自动改用户配置、不替用户选模型"
	} else {
		v.ModelReady = true
		v.ModelNote = "模型已由受控引用给出"
	}
	return v
}
