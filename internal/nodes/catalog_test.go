package nodes

import (
	"testing"
)

// fakeSource 是测试专用的假 CatalogSource：不读任何真实文件、不依赖发行包数据。
type fakeSource struct {
	flavor   string
	source   string
	ids      []string
	verified bool
}

func (f fakeSource) Flavor() string     { return f.flavor }
func (f fakeSource) Source() string     { return f.source }
func (f fakeSource) ModelIDs() []string { return f.ids }
func (f fakeSource) Verified() bool     { return f.verified }

// genericNames 是一组「通用模型名」，用于证明目录绝不内置默认值。
var genericNames = []string{"sonnet", "gpt-5", "opus", "claude-3-opus", "gpt-4o", "default"}

func catalogIDs(catalog []ModelEntry) map[string]ModelEntry {
	out := make(map[string]ModelEntry, len(catalog))
	for _, e := range catalog {
		out[e.Flavor+"\x00"+e.ID] = e
	}
	return out
}

// TestBuildCatalogSeparatesFlavors：CN 与 INTL 的枚举不混、不可跨包复制。
func TestBuildCatalogSeparatesFlavors(t *testing.T) {
	cn := fakeSource{flavor: "cn", source: "workbuddy:cn", ids: []string{"glm-5.1", "auto"}, verified: true}
	intl := fakeSource{flavor: "intl", source: "workbuddy:intl", ids: []string{"fast-model", "deep-model"}, verified: true}

	catalog := BuildCatalog([]CatalogSource{cn, intl})
	if len(catalog) != 4 {
		t.Fatalf("目录应有 4 条（cn 2 + intl 2），实际 %d：%+v", len(catalog), catalog)
	}
	byKey := catalogIDs(catalog)
	for _, tc := range []struct{ flavor, id string }{
		{"cn", "glm-5.1"}, {"cn", "auto"}, {"intl", "fast-model"}, {"intl", "deep-model"},
	} {
		e, ok := byKey[tc.flavor+"\x00"+tc.id]
		if !ok {
			t.Errorf("缺少 (%s, %s)", tc.flavor, tc.id)
			continue
		}
		if e.Source == "" {
			t.Errorf("(%s, %s) 未登记来源标识", tc.flavor, tc.id)
		}
	}
	// 跨包复制：intl 的模型不属于 cn，cn 的模型不属于 intl。
	if Valid(catalog, "cn", "fast-model") {
		t.Error("cn 目录里不应出现 intl 的模型 fast-model（枚举不可跨包复制）")
	}
	if Valid(catalog, "intl", "glm-5.1") {
		t.Error("intl 目录里不应出现 cn 的模型 glm-5.1（枚举不可跨包复制）")
	}
	// 每条条目都只带本 flavor 的取值。
	for _, e := range catalog {
		if e.Flavor == "cn" && e.ID != "glm-5.1" && e.ID != "auto" {
			t.Errorf("cn 出现异类取值 %q", e.ID)
		}
		if e.Flavor == "intl" && e.ID != "fast-model" && e.ID != "deep-model" {
			t.Errorf("intl 出现异类取值 %q", e.ID)
		}
	}
}

// TestBuildCatalogDeduplicates：同 (Flavor, ID) 去重，不同 flavor 同名不去重。
func TestBuildCatalogDeduplicates(t *testing.T) {
	sources := []CatalogSource{
		fakeSource{flavor: "cn", source: "a", ids: []string{"m1", "m2"}, verified: true},
		fakeSource{flavor: "cn", source: "b", ids: []string{"m2", "m3"}, verified: true}, // m2 重复
		fakeSource{flavor: "intl", source: "c", ids: []string{"m1"}, verified: true},     // 同名不同 flavor
	}
	catalog := BuildCatalog(sources)
	if len(catalog) != 4 { // cn:m1,cn:m2,cn:m3,intl:m1
		t.Fatalf("去重后应有 4 条，实际 %d：%+v", len(catalog), catalog)
	}
	seen := make(map[string]int)
	for _, e := range catalog {
		seen[e.Flavor+"\x00"+e.ID]++
	}
	for k, n := range seen {
		if n != 1 {
			t.Errorf("键 %q 出现 %d 次，应恰好 1 次", k, n)
		}
	}
	// 先登记者胜：cn:m2 的 Source 应来自第一条来源 "a"。
	if e, ok := FindModel(catalog, "cn", "m2"); !ok || e.Source != "a" {
		t.Errorf("去重应保留先登记者（Source=a），实际 %+v, ok=%v", e, ok)
	}
}

// TestBuildCatalogNoSourcesReturnsEmpty：无来源返回空列表，绝不用内置默认值兜底。
func TestBuildCatalogNoSourcesReturnsEmpty(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sources []CatalogSource
	}{
		{"nil", nil},
		{"空切片", []CatalogSource{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			catalog := BuildCatalog(tc.sources)
			if len(catalog) != 0 {
				t.Fatalf("无来源应返回空列表，实际 %d 条：%+v", len(catalog), catalog)
			}
			if catalog == nil {
				t.Error("返回 nil 会让 --json 输出 null，应返回非 nil 空切片")
			}
		})
	}
}

// TestBuildCatalogNoHardcodedDefaults：目录只含来源登记的取值，不出现任何硬编码通用模型名。
func TestBuildCatalogNoHardcodedDefaults(t *testing.T) {
	declared := []string{"alpha-id", "beta-id"}
	catalog := BuildCatalog([]CatalogSource{
		fakeSource{flavor: "cn", source: "fake", ids: declared, verified: true},
	})
	if len(catalog) != len(declared) {
		t.Fatalf("目录应恰好含来源登记的 %d 条，实际 %d 条（疑似内置默认值兜底）", len(declared), len(catalog))
	}
	for _, name := range genericNames {
		if Valid(catalog, "cn", name) {
			t.Errorf("目录里出现了硬编码通用模型名 %q", name)
		}
	}
	for _, id := range declared {
		if !Valid(catalog, "cn", id) {
			t.Errorf("来源登记的 %q 反而没进目录", id)
		}
	}
}

// TestBuildCatalogPropagatesVerified：Verified 只随来源声明。
func TestBuildCatalogPropagatesVerified(t *testing.T) {
	catalog := BuildCatalog([]CatalogSource{
		fakeSource{flavor: "cn", source: "measured", ids: []string{"real-id"}, verified: true},
		fakeSource{flavor: "intl", source: "placeholder", ids: []string{"placeholder-id"}, verified: false},
	})
	if e, ok := FindModel(catalog, "cn", "real-id"); !ok || !e.Verified {
		t.Errorf("实测来源应标 Verified=true，实际 %+v, ok=%v", e, ok)
	}
	if e, ok := FindModel(catalog, "intl", "placeholder-id"); !ok || e.Verified {
		t.Errorf("未实测来源应标 Verified=false，实际 %+v, ok=%v", e, ok)
	}
}

// TestValidateModelForFlavor：目录内／外／未实测／跨包取值的行为。
func TestValidateModelForFlavor(t *testing.T) {
	catalog := BuildCatalog([]CatalogSource{
		fakeSource{flavor: "cn", source: "workbuddy:cn", ids: []string{"glm-5.1"}, verified: true},
		fakeSource{flavor: "intl", source: "placeholder", ids: []string{"fast-model"}, verified: false},
	})

	// 目录内且实测：命中，且不算「未标定」。
	if e, ok, unverified := ValidateModelForFlavor(catalog, "cn", "glm-5.1"); !ok || unverified || e.ID != "glm-5.1" {
		t.Errorf("目录内实测值：期望 ok=true, unverified=false，实际 ok=%v, unverified=%v, entry=%+v", ok, unverified, e)
	}

	// 目录内但未实测（占位来源）：命中，但仍标「未标定」。
	if _, ok, unverified := ValidateModelForFlavor(catalog, "intl", "fast-model"); !ok || !unverified {
		t.Errorf("目录内未实测值：期望 ok=true, unverified=true，实际 ok=%v, unverified=%v", ok, unverified)
	}

	// 目录外取值：未命中，标「未标定」。
	if e, ok, unverified := ValidateModelForFlavor(catalog, "cn", "手填的自由文本"); ok || !unverified {
		t.Errorf("目录外取值：期望 ok=false, unverified=true，实际 ok=%v, unverified=%v, entry=%+v", ok, unverified, e)
	}

	// 跨包复制：intl 的模型填到 cn，视为 cn 的目录外取值。
	if _, ok, unverified := ValidateModelForFlavor(catalog, "cn", "fast-model"); ok || !unverified {
		t.Errorf("跨包取值：期望 ok=false, unverified=true，实际 ok=%v, unverified=%v", ok, unverified)
	}
}

// TestSourceFromRegisteredSeparatesPackages：只读适配函数把装配层传入的登记按包拆开，
// 不合并枚举。用手工构造的中性假登记（不依赖 workbuddy、不读真实文件），验证适配形状。
func TestSourceFromRegisteredSeparatesPackages(t *testing.T) {
	entries := []RegisteredModels{
		{Flavor: "cn", Source: "workbuddy:cn", ModelIDs: []string{"cn-a", "cn-b"}, Verified: true},
		{Flavor: "intl", Source: "workbuddy:intl", ModelIDs: []string{"intl-a"}, Verified: true},
	}
	sources := SourceFromRegistered(entries)
	if len(sources) != len(entries) {
		t.Fatalf("每条登记应单独成一条来源，期望 %d 条，实际 %d 条", len(entries), len(sources))
	}
	catalog := BuildCatalog(sources)
	if len(catalog) != 3 {
		t.Fatalf("适配后目录应有 3 条，实际 %d 条：%+v", len(catalog), catalog)
	}
	if !Valid(catalog, "cn", "cn-a") || !Valid(catalog, "cn", "cn-b") || !Valid(catalog, "intl", "intl-a") {
		t.Errorf("两包枚举应分别登记，实际 %+v", catalog)
	}
	if Valid(catalog, "cn", "intl-a") || Valid(catalog, "intl", "cn-a") {
		t.Error("适配函数把两包枚举混到了一起（跨包复制）")
	}
	// 来源标识与 Verified 随装配层声明，不被适配函数改写。
	if e, ok := FindModel(catalog, "cn", "cn-a"); !ok || !e.Verified || e.Source != "workbuddy:cn" {
		t.Errorf("登记应带来源标识与实测标记，实际 %+v, ok=%v", e, ok)
	}
}

// TestSourceFromRegisteredRespectsVerified：Verified 完全由装配层声明，目录不替来源猜。
func TestSourceFromRegisteredRespectsVerified(t *testing.T) {
	catalog := BuildCatalog(SourceFromRegistered([]RegisteredModels{
		{Flavor: "cn", Source: "measured", ModelIDs: []string{"real-id"}, Verified: true},
		{Flavor: "intl", Source: "placeholder", ModelIDs: []string{"placeholder-id"}, Verified: false},
	}))
	if e, ok := FindModel(catalog, "cn", "real-id"); !ok || !e.Verified {
		t.Errorf("Verified=true 应被保留，实际 %+v, ok=%v", e, ok)
	}
	if e, ok := FindModel(catalog, "intl", "placeholder-id"); !ok || e.Verified {
		t.Errorf("Verified=false 应被保留，实际 %+v, ok=%v", e, ok)
	}
}
