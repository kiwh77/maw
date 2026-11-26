package app

import (
	"testing"

	"github.com/kiwh77/maw/internal/nodes"
)

// fakeModelSource 是测试专用的假 CatalogSource：不读真实文件、不依赖发行包数据。
type fakeModelSource struct {
	flavor   string
	source   string
	ids      []string
	verified bool
}

func (f fakeModelSource) Flavor() string     { return f.flavor }
func (f fakeModelSource) Source() string     { return f.source }
func (f fakeModelSource) ModelIDs() []string { return f.ids }
func (f fakeModelSource) Verified() bool     { return f.verified }

func modelIDs(result NodeModelsResult) map[string]nodes.ModelEntry {
	out := make(map[string]nodes.ModelEntry, len(result.Models))
	for _, e := range result.Models {
		out[e.Flavor+"\x00"+e.ID] = e
	}
	return out
}

var genericModelNames = []string{"sonnet", "gpt-5", "opus", "claude-3-opus", "gpt-4o", "default"}

// 三个假来源：cn 实测、intl 实测、一个未实测的占位来源，覆盖主要判定路径。
func baseFakeSources() []nodes.CatalogSource {
	return []nodes.CatalogSource{
		fakeModelSource{flavor: "cn", source: "workbuddy:cn", ids: []string{"glm-5.1", "auto"}, verified: true},
		fakeModelSource{flavor: "intl", source: "workbuddy:intl", ids: []string{"fast-model", "deep-model"}, verified: true},
		fakeModelSource{flavor: "placeholder", source: "guess", ids: []string{"mystery-id"}, verified: false},
	}
}

// TestListNodeModelsUnknownFlavor：未知 flavor 返回空列表 + has_evidence=false，不报错。
func TestListNodeModelsUnknownFlavor(t *testing.T) {
	// 走纯过滤入口（假来源）。
	got := listNodeModels("ghost-flavor", baseFakeSources())
	if len(got.Models) != 0 {
		t.Errorf("未知 flavor 应返回空列表，实际 %d 条：%+v", len(got.Models), got.Models)
	}
	if got.Models == nil {
		t.Error("Models 应为非 nil 空切片，保证 --json 输出 [] 而非 null")
	}
	if got.HasEvidence {
		t.Error("未知 flavor 的 has_evidence 应为 false")
	}
	if got.Flavor != "ghost-flavor" {
		t.Errorf("结果应回显请求的 flavor，实际 %q", got.Flavor)
	}

	// 走导出的 ListNodeModels（装配点默认空来源，未知 flavor 不依赖具体数据）。
	res, err := ListNodeModels("ghost-flavor")
	if err != nil {
		t.Fatalf("未知 flavor 不应报错，实际 %v", err)
	}
	if len(res.Models) != 0 || res.HasEvidence {
		t.Errorf("导出函数：未知 flavor 应为空且 has_evidence=false，实际 %+v", res)
	}
}

// TestListNodeModelsEmptyFlavorReturnsAll：flavor 为空返回全部来源的取值。
func TestListNodeModelsEmptyFlavorReturnsAll(t *testing.T) {
	got := listNodeModels("", baseFakeSources())
	// cn 2 + intl 2 + placeholder 1 = 5 条。
	if len(got.Models) != 5 {
		t.Fatalf("空 flavor 应返回全部 5 条，实际 %d 条：%+v", len(got.Models), got.Models)
	}
	if !got.HasEvidence {
		t.Error("存在实测来源时 has_evidence 应为 true")
	}
	if got.Flavor != "" {
		t.Errorf("空 flavor 结果里 flavor 应为空串，实际 %q", got.Flavor)
	}
	byKey := modelIDs(got)
	for _, key := range []string{"cn\x00glm-5.1", "cn\x00auto", "intl\x00fast-model", "intl\x00deep-model", "placeholder\x00mystery-id"} {
		if _, ok := byKey[key]; !ok {
			t.Errorf("空 flavor 结果缺少 %q", key)
		}
	}
}

// TestListNodeModelsKnownFlavor：只返回该 flavor 的取值，不混入别的 flavor。
func TestListNodeModelsKnownFlavor(t *testing.T) {
	got := listNodeModels("cn", baseFakeSources())
	if len(got.Models) != 2 {
		t.Fatalf("cn 应只有 2 条，实际 %d 条：%+v", len(got.Models), got.Models)
	}
	for _, e := range got.Models {
		if e.Flavor != "cn" {
			t.Errorf("cn 结果里混入了 flavor=%q 的条目 %q", e.Flavor, e.ID)
		}
	}
	if !got.HasEvidence {
		t.Error("cn 为实测来源，has_evidence 应为 true")
	}
	if e, ok := modelIDs(got)["cn\x00glm-5.1"]; !ok || e.Source != "workbuddy:cn" {
		t.Errorf("cn 结果应带来源标识，实际 %+v, ok=%v", e, ok)
	}
}

// TestListNodeModelsNoSources：无来源返回空列表 + has_evidence=false，不兜底默认值。
func TestListNodeModelsNoSources(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []nodes.CatalogSource
	}{
		{"nil", nil},
		{"空切片", []nodes.CatalogSource{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := listNodeModels("", tc.sources)
			if len(got.Models) != 0 || got.HasEvidence {
				t.Errorf("无来源应为空且 has_evidence=false，实际 %+v", got)
			}
			if got.Models == nil {
				t.Error("Models 应为非 nil 空切片")
			}
		})
	}
}

// TestListNodeModelsUnverifiedFlavorHasNoEvidence：未实测来源有条目但无证据。
func TestListNodeModelsUnverifiedFlavorHasNoEvidence(t *testing.T) {
	got := listNodeModels("placeholder", baseFakeSources())
	if len(got.Models) != 1 {
		t.Fatalf("placeholder 应有 1 条，实际 %d 条", len(got.Models))
	}
	if got.HasEvidence {
		t.Error("唯一来源是未实测的，has_evidence 应为 false")
	}
	if got.Models[0].Verified {
		t.Error("该条目的 Verified 应随来源为 false")
	}
}

// TestListNodeModelsNoHardcodedDefaults：结果不含任何硬编码通用模型名，也不凭空兜底。
func TestListNodeModelsNoHardcodedDefaults(t *testing.T) {
	declared := []string{"alpha-id", "beta-id"}
	got := listNodeModels("", []nodes.CatalogSource{
		fakeModelSource{flavor: "cn", source: "fake", ids: declared, verified: true},
	})
	if len(got.Models) != len(declared) {
		t.Fatalf("结果应恰好含来源登记的 %d 条，实际 %d 条（疑似内置默认值兜底）", len(declared), len(got.Models))
	}
	byKey := modelIDs(got)
	for _, name := range genericModelNames {
		if _, ok := byKey["cn\x00"+name]; ok {
			t.Errorf("结果里出现了硬编码通用模型名 %q", name)
		}
	}
	for _, id := range declared {
		if _, ok := byKey["cn\x00"+id]; !ok {
			t.Errorf("来源登记的 %q 反而没进结果", id)
		}
	}
}

// TestListNodeModelsCNINTLNotMixed：CN／INTL 枚举不混，跨包取值不会出现在对方的 flavor 里。
func TestListNodeModelsCNINTLNotMixed(t *testing.T) {
	sources := []nodes.CatalogSource{
		fakeModelSource{flavor: "cn", source: "workbuddy:cn", ids: []string{"glm-5.1"}, verified: true},
		fakeModelSource{flavor: "intl", source: "workbuddy:intl", ids: []string{"fast-model"}, verified: true},
	}
	cn := modelIDs(listNodeModels("cn", sources))
	intl := modelIDs(listNodeModels("intl", sources))
	if _, ok := cn["cn\x00fast-model"]; ok {
		t.Error("cn 里出现了 intl 的 fast-model（枚举跨包复制）")
	}
	if _, ok := intl["intl\x00glm-5.1"]; ok {
		t.Error("intl 里出现了 cn 的 glm-5.1（枚举跨包复制）")
	}
	if _, ok := cn["intl\x00fast-model"]; ok {
		t.Error("cn 过滤结果里出现了 intl flavor 的条目")
	}
	if _, ok := cn["cn\x00glm-5.1"]; !ok {
		t.Error("cn 过滤结果里缺少本包的 glm-5.1")
	}
	if _, ok := intl["intl\x00fast-model"]; !ok {
		t.Error("intl 过滤结果里缺少本包的 fast-model")
	}
}

// TestListNodeModelsDefaultHasNoSources：未注入来源时返回空 + has_evidence=false，不猜测。
func TestListNodeModelsDefaultHasNoSources(t *testing.T) {
	// 隔离全局装配点，避免依赖/污染其它测试。
	prev := nodeModelSources
	nodeModelSources = func() []nodes.CatalogSource { return nil }
	t.Cleanup(func() { nodeModelSources = prev })

	res, err := ListNodeModels("")
	if err != nil {
		t.Fatalf("不应报错，实际 %v", err)
	}
	if len(res.Models) != 0 || res.HasEvidence {
		t.Errorf("未注入来源应为空且 has_evidence=false，实际 %+v", res)
	}
	if res.Models == nil {
		t.Error("Models 应为非 nil 空切片")
	}
}

// TestSetNodeModelSourcesInjects：装配层注入后 ListNodeModels 用注入的来源产出目录。
func TestSetNodeModelSourcesInjects(t *testing.T) {
	prev := nodeModelSources
	t.Cleanup(func() { nodeModelSources = prev })

	SetNodeModelSources(func() []nodes.CatalogSource {
		return []nodes.CatalogSource{
			fakeModelSource{flavor: "cn", source: "workbuddy:cn", ids: []string{"glm-5.1"}, verified: true},
			fakeModelSource{flavor: "intl", source: "workbuddy:intl", ids: []string{"fast-model"}, verified: true},
		}
	})
	cn, err := ListNodeModels("cn")
	if err != nil || len(cn.Models) != 1 || !cn.HasEvidence || cn.Models[0].ID != "glm-5.1" {
		t.Errorf("注入后 cn 应返回该来源的取值，实际 %+v, err=%v", cn, err)
	}
	// SetNodeModelSources(nil) 不改现状。
	SetNodeModelSources(nil)
	still, _ := ListNodeModels("cn")
	if len(still.Models) != 1 {
		t.Errorf("SetNodeModelSources(nil) 不应清空已注入来源，实际 %+v", still)
	}
}
