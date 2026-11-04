package nodes

import (
	"sort"
	"strings"
)

// ModelEntry 是模型目录里的一条登记。
//
// 四个字段分别表达：取值 ID、归属 flavor、来源标识、是否实测。目录本身不内置
// 任何模型名：能列出什么，完全由传入的登记来源决定（见 BuildCatalog）。Source
// 与 Verified 让上层能区分「有证据的实测枚举」与「手填／占位值」，而不是把两者
// 混成一张拍脑袋的通用常量表。
type ModelEntry struct {
	ID       string `json:"id"`
	Flavor   string `json:"flavor"`
	Source   string `json:"source"`
	Verified bool   `json:"verified"`
}

// CatalogSource 是一条 --model 枚举的登记来源。
//
// 项目铁律：同版本不代表同能力、两包枚举不可跨包复制、能力必须按包挂证据。
// 因此「枚举从哪来」被抽成接口由调用方注入，目录只做汇总与判定：
//   - Flavor 是该来源归属的维度（去重与查询都按 (Flavor, ID)）；
//   - ModelIDs 是该来源登记的取值清单；
//   - Source 是来源标识（写入 ModelEntry.Source）；
//   - Verified 由来源自己声明是否实测，目录绝不替来源猜。
//
// 事实来源用接口、判定用假数据测：单测注入自己的假 CatalogSource，不读真实文件。
type CatalogSource interface {
	Flavor() string
	Source() string
	ModelIDs() []string
	Verified() bool
}

// staticSource 是 CatalogSource 的最小实现，供装配层与测试构造来源。
type staticSource struct {
	flavor   string
	source   string
	modelIDs []string
	verified bool
}

func (s staticSource) Flavor() string { return s.flavor }
func (s staticSource) Source() string { return s.source }
func (s staticSource) Verified() bool { return s.verified }
func (s staticSource) ModelIDs() []string {
	return append([]string(nil), s.modelIDs...)
}

// NewSource 用中性字段构造一条登记来源。
//
// 装配层拿到发行包登记后，把 flavor／枚举／是否实测显式填进来；verified=false
// 的来源会在目录里标成「未实测」，供 UI 标「未标定」。modelIDs 会被复制，
// 调用方之后改动原切片不会影响目录。
func NewSource(flavor, source string, modelIDs []string, verified bool) CatalogSource {
	return staticSource{
		flavor:   strings.TrimSpace(flavor),
		source:   strings.TrimSpace(source),
		modelIDs: append([]string(nil), modelIDs...),
		verified: verified,
	}
}

// RegisteredModels 是装配层传入的一条发行包登记（中性数据，不依赖具体适配器）。
//
// Flavor 是归属维度（去重与查询按 (Flavor, ID)），Source 是来源标识，
// ModelIDs 是该来源登记的 --model 枚举，Verified 由装配层按证据声明是否实测。
type RegisteredModels struct {
	Flavor   string
	Source   string
	ModelIDs []string
	Verified bool
}

// SourceFromRegistered 把装配层传入的登记转成 []CatalogSource（只读适配）。
//
// 为什么不由本包直接读 workbuddy：internal/adapters/workbuddy 的 distributions
// 是包内私有变量，登记表虽通过导出函数 Registered() 暴露，但「读枚举、把发行包
// 种类映射到 flavor、按证据判定是否实测」属于装配层的职责（收口卡），不是目录的
// 纯逻辑。把这一步延后、由装配层拆成 RegisteredModels 传进来，catalog 全程不
// import 任何具体适配器，保持纯函数 + 假来源可测，也不会碰 adapters 包。
//
// 装配层用 workbuddy.Registered() 取登记后，逐条把 Kind→Flavor、ModelIDs、
// 来源标识与 Verified 填成 RegisteredModels 再传入；两包务必分别登记、绝不合并，
// 这正是「模型取值不可跨包复制」的落点。Verified 完全随装配层声明，适配函数不改写。
func SourceFromRegistered(entries []RegisteredModels) []CatalogSource {
	out := make([]CatalogSource, 0, len(entries))
	for _, e := range entries {
		out = append(out, NewSource(e.Flavor, e.Source, e.ModelIDs, e.Verified))
	}
	return out
}

// BuildCatalog 把传入的登记来源汇总成模型目录。
//
// 严格只汇总 sources：没有来源就返回空列表，绝不补内置默认值。去重按
// (Flavor, ID)，先登记者胜；不同 flavor 的同名 ID 互不去重（例如 cn 与 intl
// 各有自己的清单）。输出按 (Flavor, ID) 稳定排序，便于 UI 与只读 API 得到
// 确定顺序。空的、首尾带空白的取值会被清理，不会凭空产生条目。
func BuildCatalog(sources []CatalogSource) []ModelEntry {
	seen := make(map[string]struct{})
	out := make([]ModelEntry, 0)
	for _, src := range sources {
		if src == nil {
			continue
		}
		flavor := strings.TrimSpace(src.Flavor())
		source := strings.TrimSpace(src.Source())
		verified := src.Verified()
		for _, raw := range src.ModelIDs() {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			key := flavor + "\x00" + id
			if _, dup := seen[key]; dup {
				continue // 先登记者胜
			}
			seen[key] = struct{}{}
			out = append(out, ModelEntry{
				ID:       id,
				Flavor:   flavor,
				Source:   source,
				Verified: verified,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Flavor != out[j].Flavor {
			return out[i].Flavor < out[j].Flavor
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// FindModel 在目录里查 (flavor, id) 命中的条目；查询辅助，不做任何猜测。
func FindModel(catalog []ModelEntry, flavor, id string) (ModelEntry, bool) {
	wantFlavor := strings.TrimSpace(flavor)
	wantID := strings.TrimSpace(id)
	for _, e := range catalog {
		if e.Flavor == wantFlavor && e.ID == wantID {
			return e, true
		}
	}
	return ModelEntry{}, false
}

// Valid 报告 (flavor, id) 是否为目录内取值。
func Valid(catalog []ModelEntry, flavor, id string) bool {
	_, ok := FindModel(catalog, flavor, id)
	return ok
}

// ValidateModelForFlavor 校验 (flavor, id) 是否有实测依据，供 UI 标「未标定」。
//
// 返回：
//   - entry：命中的条目（未命中为零值）；
//   - ok：目录中存在该 (flavor, id) 条目；
//   - unverified：该取值对该 flavor 没有实测依据，UI 应标「未标定」。
//     目录外的取值（含把 intl 的模型填到 cn，即跨包复制），或命中条目
//     Verified==false（占位／未实测来源）都为 true。
func ValidateModelForFlavor(catalog []ModelEntry, flavor, id string) (ModelEntry, bool, bool) {
	entry, ok := FindModel(catalog, flavor, id)
	if !ok {
		return ModelEntry{}, false, true
	}
	return entry, true, !entry.Verified
}
