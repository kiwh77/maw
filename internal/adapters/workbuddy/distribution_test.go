package workbuddy

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestRegisteredDistributionsAreValid 是登记表自检：两包各自完整，且彼此不同。
func TestRegisteredDistributionsAreValid(t *testing.T) {
	dists := Registered()
	if len(dists) != 2 {
		t.Fatalf("应登记两个发行包，实际 %d 个", len(dists))
	}
	for _, d := range dists {
		if problems := d.validate(); len(problems) != 0 {
			t.Errorf("%s 登记不完整：%v", d.Kind, problems)
		}
		if d.ManifestRef() != EvidenceRoot+"/manifest.json" {
			t.Errorf("%s 的证据清单引用错误：%q", d.Kind, d.ManifestRef())
		}
	}

	cn, _ := Lookup(DistributionCN)
	intl, _ := Lookup(DistributionIntl)
	if cn.AppName == intl.AppName || cn.BundleID == intl.BundleID || cn.AppVersion == intl.AppVersion {
		t.Fatalf("两包必须是两个独立登记项：cn=%+v intl=%+v", cn, intl)
	}
	// 入口位置是两包实测相同的形态，登记表只存一份（EntrySuffix），
	// 不允许出现「两份各自不同的入口位置」这种自相矛盾的状态。
	if strings.TrimSpace(EntrySuffix) == "" {
		t.Fatal("EntrySuffix 不能为空：判定随包入口必须有关键形状")
	}
	if !strings.Contains(EntrySuffix, "app.asar.unpacked/cli/bin/") {
		t.Fatalf("EntrySuffix 应指向随包 cli/bin 目录，实际 %q", EntrySuffix)
	}
	// 版本号相同这一事实必须被显式保留：它正是「不能靠版本区分来源」的依据。
	if cn.BundledCLI != intl.BundledCLI {
		t.Fatalf("两包随包 CLI 版本实测相同（2.161.0），实际 %s／%s", cn.BundledCLI, intl.BundledCLI)
	}
}

// TestRegisteredReturnsCopies 保证调用方改不动登记表。
func TestRegisteredReturnsCopies(t *testing.T) {
	first := Registered()
	first[0].ModelIDs[0] = "tampered"
	first[0].RequiredEnv["CODEBUDDY_FORCE_LITE_WB_BUNDLE"] = "0"

	again, _ := Lookup(first[0].Kind)
	if again.ModelIDs[0] == "tampered" {
		t.Fatal("Registered 返回的模型列表被外部改写后污染了登记表")
	}
	if again.RequiredEnv["CODEBUDDY_FORCE_LITE_WB_BUNDLE"] != "1" {
		t.Fatalf("登记表的环境条件被外部改写：%v", again.RequiredEnv)
	}
}

// TestDetectDistinguishesPackages 覆盖两包入口的判定。
func TestDetectDistinguishesPackages(t *testing.T) {
	for _, kind := range []DistributionKind{DistributionCN, DistributionIntl} {
		cfg, script, _ := testNode(t, kind)
		got, err := Detect(mustReal(t, script))
		if err != nil {
			t.Fatalf("%s 入口应被识别：%v", kind, err)
		}
		if got.Kind != kind {
			t.Fatalf("应识别为 %s，实际 %s", kind, got.Kind)
		}
		if got.AppName != cfg.Name {
			t.Errorf("%s 的 AppName 不符：%q", kind, got.AppName)
		}
	}
}

// TestDetectRejectsUnregisteredEntries 覆盖「看起来像」不算登记。
func TestDetectRejectsUnregisteredEntries(t *testing.T) {
	base := t.TempDir()
	cases := []struct {
		name string
		path string
		want string
	}{
		{
			name: "不在 .app 内",
			path: filepath.Join(base, "bin", "codebuddy"),
			want: ".app",
		},
		{
			name: "同目录的其他程序",
			path: filepath.Join(base, "WorkBuddy.app", "Contents/Resources/app.asar.unpacked/cli/bin/cbc-prewarm"),
			want: "不是登记的",
		},
		{
			name: "未登记的发行包",
			path: filepath.Join(base, "ZCode.app", filepath.FromSlash(EntrySuffix)),
			want: "未登记",
		},
		{
			name: "相对路径",
			path: filepath.FromSlash("WorkBuddy.app/" + EntrySuffix),
			want: "绝对路径",
		},
	}
	for _, c := range cases {
		if _, err := Detect(c.path); err == nil {
			t.Errorf("%s：应拒绝 %q", c.name, c.path)
			continue
		} else if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s：错误文案应说明 %q，实际 %v", c.name, c.want, err)
		}
	}
}

// TestModelListsAreNotInterchangeable 是「不自动继承对方能力」的最直接用例：
// 两包唯一的实测差异就在 --model 枚举上，取值不能跨包复制。
func TestModelListsAreNotInterchangeable(t *testing.T) {
	cn, _ := Lookup(DistributionCN)
	intl, _ := Lookup(DistributionIntl)

	if !cn.SupportsModel("glm-5.1") {
		t.Error("cn 包应支持 glm-5.1（帮助清单实测枚举里有）")
	}
	if cn.SupportsModel("fast-model") {
		t.Error("cn 包不应接受 intl 包的 fast-model")
	}
	if !intl.SupportsModel("fast-model") {
		t.Error("intl 包应支持 fast-model")
	}
	if intl.SupportsModel("glm-5.1") {
		t.Error("intl 包不应接受 cn 包的 glm-5.1：两包枚举不可互认")
	}
	for _, d := range []Distribution{cn, intl} {
		if d.SupportsModel("") || d.SupportsModel("not-a-model") {
			t.Errorf("%s 接受了未登记的模型取值", d.Kind)
		}
	}
}

// TestMissingEnv 覆盖启动条件的三种状态：齐全、缺失、取值不符。
func TestMissingEnv(t *testing.T) {
	dist, _ := Lookup(DistributionCN)

	full := func(name string) (string, bool) { return dist.RequiredEnv[name], true }
	if got := dist.MissingEnv(full); len(got) != 0 {
		t.Fatalf("条件齐全时不应报缺失，实际 %v", got)
	}

	absent := func(string) (string, bool) { return "", false }
	got := dist.MissingEnv(absent)
	if len(got) != len(dist.RequiredEnv) {
		t.Fatalf("应报出全部缺失项，实际 %v", got)
	}
	// 顺序稳定，便于比对文案与日志。
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("缺失项应按变量名排序，实际 %v", got)
		}
	}

	wrong := func(name string) (string, bool) { return "0", true }
	if got := dist.MissingEnv(wrong); len(got) != len(dist.RequiredEnv) {
		t.Fatalf("取值不符也算缺失，实际 %v", got)
	} else if !strings.Contains(got[0], "当前") {
		t.Fatalf("取值不符的文案应说明当前值，实际 %q", got[0])
	}
}

// TestSamplePathsArePerDistribution 钉住证据归属：每条能力的证据必须落在该包自己的目录下。
func TestSamplePathsArePerDistribution(t *testing.T) {
	cn, _ := Lookup(DistributionCN)
	intl, _ := Lookup(DistributionIntl)

	if got := cn.Sample("S02-help-probe"); got != EvidenceRoot+"/samples/VN305-CN-S02-help-probe" {
		t.Fatalf("cn 样本路径不符：%q", got)
	}
	if got := intl.Sample("S02-help-probe"); got != EvidenceRoot+"/samples/VN305-INTL-S02-help-probe" {
		t.Fatalf("intl 样本路径不符：%q", got)
	}
	if got := cn.Sample(""); got != EvidenceRoot+"/capability-matrix.json" {
		t.Fatalf("无样本时证据应指向矩阵本身，实际 %q", got)
	}
	// 两包共用的记录型反例不加包前缀。
	if got := cn.Sample("X01-auth-required"); got != EvidenceRoot+"/samples/VN305-X01-auth-required" {
		t.Fatalf("共用反例路径不符：%q", got)
	}
}
