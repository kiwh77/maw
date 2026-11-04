package app

import (
	"strings"

	"github.com/kiwh77/maw/internal/nodes"
)

// NodeModelsResult 是 ListNodeModels 的结果 DTO：只描述某个 flavor 下有证据的
// 模型枚举，不做路由、不碰 HTTP、不启动任务。Models 恒为非 nil 空切片，
// 以便 --json 与只读 API 稳定输出 [] 而不是 null。
type NodeModelsResult struct {
	Models      []nodes.ModelEntry `json:"models"`
	Flavor      string             `json:"flavor"`
	HasEvidence bool               `json:"has_evidence"`
}

// nodeModelSources 是模型目录的事实来源装配点，由装配层（收口卡）注入。
//
// 默认返回空来源：在注入之前 ListNodeModels 返回空列表 + has_evidence=false，
// 不猜测、不读真实文件、不依赖具体适配器。"读枚举"由装配层完成（例如调用
// workbuddy.Registered() 后经 nodes.SourceFromRegistered(...) 转换），再通过
// SetNodeModelSources 注入。这样 catalog 与 app 都保持纯逻辑，可用假来源单测。
var nodeModelSources = func() []nodes.CatalogSource { return nil }

// SetNodeModelSources 注入模型目录的事实来源，供装配层收口卡调用。
// fn 为 nil 时保持原状，避免误把目录清空。
func SetNodeModelSources(fn func() []nodes.CatalogSource) {
	if fn != nil {
		nodeModelSources = fn
	}
}

// ListNodeModels 列出模型目录：flavor 为空返回全部；未知 flavor 返回空列表与
// has_evidence=false，不报错、不猜测。error 预留给将来可能失败的来源装配，
// 当前恒为 nil。事实来源由 SetNodeModelSources 注入；未注入时返回空列表 +
// has_evidence=false。
//
// 供 CLI `maw node models --flavor x --json` 与只读 API 复用；命令注册与 HTTP
// 由装配方负责，本文件不做。
//
// 确切签名：func ListNodeModels(flavor string) (NodeModelsResult, error)
func ListNodeModels(flavor string) (NodeModelsResult, error) {
	sources := nodeModelSources
	if sources == nil {
		return listNodeModels(flavor, nil), nil
	}
	return listNodeModels(flavor, sources()), nil
}

// listNodeModels 是纯过滤：目录完全来自传入 sources，便于用假来源做单测。
//
// has_evidence 表示本次返回的取值里至少有一条来自「实测」来源；未知 flavor
// 没有任何条目，自然为 false。BuildCatalog 已按 (Flavor, ID) 排好序，过滤是
// 稳定子序列，无需再排。
func listNodeModels(flavor string, sources []nodes.CatalogSource) NodeModelsResult {
	want := strings.TrimSpace(flavor)
	catalog := nodes.BuildCatalog(sources)
	models := make([]nodes.ModelEntry, 0, len(catalog))
	hasEvidence := false
	for _, e := range catalog {
		if want != "" && e.Flavor != want {
			continue
		}
		models = append(models, e)
		if e.Verified {
			hasEvidence = true
		}
	}
	return NodeModelsResult{Models: models, Flavor: want, HasEvidence: hasEvidence}
}
