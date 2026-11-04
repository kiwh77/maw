package app

import (
	"github.com/kiwh77/maw/internal/adapters/workbuddy"
	"github.com/kiwh77/maw/internal/nodes"
)

// 模型目录的事实来源装配点。
//
// 分层理由：internal/nodes/catalog.go 只做「汇总与判定」的纯逻辑，不依赖任何
// 具体适配器，单测可以注入假来源；真正「哪些模型有实测证据」由本文件从
// internal/adapters/workbuddy 的发行包登记表读取。登记表里的 ModelIDs 是逐字
// 抄自两包 --help 的实测清单（docs/evidence/workbuddy/capability-matrix.json
// 的 W15 model.enumeration = observed），且明确「同版本不代表同能力、不可跨包
// 复制」，所以两包分别登记、绝不合并成一份通用清单。
//
// 用 init 注入而不是在 ListNodeModels 里直接 import，是为了让 CLI 与只读 API
// 两条调用路径拿到同一份来源，同时保持 nodes 包零适配器依赖。
func init() {
	SetNodeModelSources(func() []nodes.CatalogSource {
		dists := workbuddy.Registered()
		entries := make([]nodes.RegisteredModels, 0, len(dists))
		for _, d := range dists {
			entries = append(entries, nodes.RegisteredModels{
				Flavor:   string(d.Kind),
				Source:   "workbuddy:" + string(d.Kind),
				ModelIDs: append([]string(nil), d.ModelIDs...),
				Verified: true,
			})
		}
		return nodes.SourceFromRegistered(entries)
	})
}
