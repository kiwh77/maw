package workbuddy

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kiwh77/maw/internal/engine/model"
)

// DistributionKind 是 WorkBuddy 的发行包种类。
//
// 两款 WorkBuddy 是两个独立发行包，不是同一个东西的两个版本号：App 版本、bundle id、
// 模型枚举、登录上下文都各自成立。所以它们在这里是两个登记项，而不是一个带参数的模板。
type DistributionKind string

// 两个已登记发行包。
const (
	DistributionCN   DistributionKind = "cn"
	DistributionIntl DistributionKind = "intl"
)

// Valid 报告取值是否合法。
func (k DistributionKind) Valid() bool {
	return k == DistributionCN || k == DistributionIntl
}

// EntrySuffix 是随包 CLI 在 .app 内的固定相对位置。
//
// 两包实测一致（VN305-CN-S02／VN305-INTL-S02 的入口都落在这个位置），
// 所以它是「随包入口」的形状判据：只认这个位置上的 codebuddy，
// 同目录的 cbc-prewarm／codebuddy-lowmem 是别的程序，不在登记范围内。
const EntrySuffix = "Contents/Resources/app.asar.unpacked/cli/bin/codebuddy"

// RuntimeCommand 是入口 shebang 声明的运行时（两包首行都是 `#!/usr/bin/env node`）。
//
// 运行时也是「入口形状」的一部分：换成别的解释器，同一条 --version 拿到的也可能是
// 另一份实现，所以本包拒绝非 node 运行时，而不是「能跑就行」。
const RuntimeCommand = "node"

// EvidenceRoot 是两包证据的总目录；样本按发行包分目录登记。
const EvidenceRoot = "docs/evidence/workbuddy"

// Distribution 是一个发行包的独立登记。
//
// 五项分别对应验收要求的「入口、运行时、认证就绪、版本」：
//   - 入口：AppName + EntrySuffix（拼出唯一合法入口）；
//   - 运行时：RuntimeCommand（shebang 声明的解释器）；
//   - 版本：AppVersion（桌面包版本）与 BundledCLI（随包 CLI 版本）分开登记，
//     两者不能互相替代——桌面包升级不等于随包 CLI 升级；
//   - 认证就绪：RequiredEnv 是「能起得来」的前置条件，登录态另算（见 AuthHint）；
//   - 模型枚举：ModelIDs 是 --model 的取值清单，两包唯一实测差异就在这里。
type Distribution struct {
	Kind       DistributionKind
	AppName    string
	AppVersion string
	BundleID   string
	// BundledCLI 是该包随包 CLI 的实测版本；证据只对这个版本成立。
	BundledCLI model.SemVer
	// ModelIDs 是该包 --model 的取值枚举（逐字抄自实测帮助清单）。
	ModelIDs []string
	// RequiredEnv 是选中「实测过的那份随包实现」所需的启动条件，按包登记。
	RequiredEnv map[string]string
}

// distributions 是唯一的发行包登记表。
//
// 两包的 BundledCLI 目前相同（都是 2.161.0），所以**版本号不能用来区分来源**：
// 区分只能靠发行包登记 + 各自的证据目录。这也正是「不自动继承对方能力」的落点——
// 同版本不代表同能力，能力必须按包挂证据。
var distributions = []Distribution{
	{
		Kind:       DistributionCN,
		AppName:    "WorkBuddy",
		AppVersion: "5.8.3",
		BundleID:   "com.tencent.workbuddy.mac",
		BundledCLI: "2.161.0",
		ModelIDs: []string{
			"auto", "glm-5v-turbo", "glm-5.1", "glm-5.0-turbo", "glm-5.0",
			"glm-4.7", "kimi-k2.5", "minimax-m2.7", "deepseek-v3-2-volc",
		},
		RequiredEnv: requiredEnvLiteWB(),
	},
	{
		Kind:        DistributionIntl,
		AppName:     "WorkBuddy AI",
		AppVersion:  "5.8.4",
		BundleID:    "com.workbuddy.workbuddy-ai",
		BundledCLI:  "2.161.0",
		ModelIDs:    []string{"fast-model", "balanced-model", "primary-model", "deep-model"},
		RequiredEnv: requiredEnvLiteWB(),
	},
}

// requiredEnvLiteWB 返回实测启动条件。
//
// 两个变量都来自最小调用证据：CODEBUDDY_FORCE_LITE_WB_BUNDLE=1 用来选中随包实现
// （工作目录里只随包携带 codebuddy-lite-wb.mjs），CODEBUDDY_DISABLE_COMPILE_CACHE=1
// 是同一轮调用里一起设置的编译缓存开关。少任何一个，跑起来的就不是被证据覆盖的那份实现。
//
// 两包当前取值相同，仍然分别登记：某个包的启动脚本若改了条件，只应影响它自己。
func requiredEnvLiteWB() map[string]string {
	return map[string]string{
		"CODEBUDDY_FORCE_LITE_WB_BUNDLE":  "1",
		"CODEBUDDY_DISABLE_COMPILE_CACHE": "1",
	}
}

// Registered 返回登记表的副本（按 Kind 稳定排序）。
//
// 返回副本而不是切片本身：调用方改不动登记表，能力归属就不会被外部悄悄改写。
func Registered() []Distribution {
	out := make([]Distribution, 0, len(distributions))
	for _, d := range distributions {
		out = append(out, d.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}

// Lookup 按种类取登记项。
func Lookup(kind DistributionKind) (Distribution, bool) {
	for _, d := range distributions {
		if d.Kind == kind {
			return d.clone(), true
		}
	}
	return Distribution{}, false
}

func (d Distribution) clone() Distribution {
	out := d
	out.ModelIDs = append([]string(nil), d.ModelIDs...)
	out.RequiredEnv = make(map[string]string, len(d.RequiredEnv))
	for k, v := range d.RequiredEnv {
		out.RequiredEnv[k] = v
	}
	return out
}

// AppBundle 是该包的 .app 目录名。
func (d Distribution) AppBundle() string { return d.AppName + ".app" }

// SamplePrefix 是该包样本目录的编号前缀（VN305-CN／VN305-INTL）。
func (d Distribution) SamplePrefix() string {
	return "VN305-" + strings.ToUpper(string(d.Kind))
}

// ManifestRef 是该包证据清单的引用（ProbeResult 用）。
func (d Distribution) ManifestRef() string { return EvidenceRoot + "/manifest.json" }

// Sample 把样本标识翻成证据目录引用。
//
// sampleID 为空表示「这条能力没有单条样本，证据是矩阵本身」；
// 以 X 开头的是两包共用的反例（例如认证缺失形态），不加发行包前缀。
func (d Distribution) Sample(sampleID string) string {
	switch {
	case sampleID == "":
		return EvidenceRoot + "/capability-matrix.json"
	case strings.HasPrefix(sampleID, "X"):
		return EvidenceRoot + "/samples/VN305-" + sampleID
	default:
		return EvidenceRoot + "/samples/" + d.SamplePrefix() + "-" + sampleID
	}
}

// SupportsModel 报告某个 --model 取值是否属于本包的实测枚举。
//
// 存在的理由就是两包唯一的实测差异：CN 包列的是 glm/kimi/minimax 等具体模型 ID，
// intl 包列的是 fast-model／balanced-model 等占位 ID。任何「按模型名匹配」的实现
// 都不得跨包复制取值——所以这里按包判定，而不是查一张全局表。
func (d Distribution) SupportsModel(id string) bool {
	want := strings.TrimSpace(id)
	if want == "" {
		return false
	}
	for _, v := range d.ModelIDs {
		if v == want {
			return true
		}
	}
	return false
}

// MissingEnv 报告缺少（或取值不符）的启动条件，按变量名排序返回。
//
// lookup 由调用方注入（生产用 os.LookupEnv，测试用固定表）：
// 需要检查的是「启动这个入口时的环境」，不是「本包进程里恰好有什么」。
func (d Distribution) MissingEnv(lookup func(string) (string, bool)) []string {
	names := make([]string, 0, len(d.RequiredEnv))
	for name := range d.RequiredEnv {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []string
	for _, name := range names {
		got, ok := lookup(name)
		if !ok {
			out = append(out, name+"（未设置，需要 "+name+"="+d.RequiredEnv[name]+"）")
			continue
		}
		if got != d.RequiredEnv[name] {
			out = append(out, fmt.Sprintf("%s（当前 %q，需要 %q）", name, got, d.RequiredEnv[name]))
		}
	}
	return out
}

// EnvNames 返回本包要求的环境变量名（按字母序），用于启动规格的白名单。
func (d Distribution) EnvNames() []string {
	names := make([]string, 0, len(d.RequiredEnv))
	for name := range d.RequiredEnv {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Detect 从真实入口路径判定它属于哪个发行包。
//
// 判据是两段硬条件，不是「路径里出现过 WorkBuddy」：
//   - 存在一段 `<AppName>.app`，且该名字在登记表里；
//   - 其后的相对路径逐字等于 EntrySuffix。
//
// 不满足就拒绝：一个任意路径上的 codebuddy 不是被证据覆盖的随包入口，
// 「看起来像」不能替代「登记过」。
func Detect(entryPath string) (Distribution, error) {
	clean := filepath.Clean(strings.TrimSpace(entryPath))
	if !filepath.IsAbs(clean) {
		return Distribution{}, fmt.Errorf("入口 %q 不是绝对路径", entryPath)
	}
	segs := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))

	bundleAt := -1
	for i := len(segs) - 1; i >= 0; i-- {
		if strings.HasSuffix(segs[i], ".app") {
			bundleAt = i
			break
		}
	}
	if bundleAt < 0 {
		return Distribution{}, fmt.Errorf("入口 %q 不在任何 .app 发行包内：随包入口必须位于 <App>.app/%s", entryPath, EntrySuffix)
	}

	appName := strings.TrimSuffix(segs[bundleAt], ".app")
	var dist Distribution
	found := false
	for _, d := range distributions {
		if d.AppName == appName {
			dist, found = d.clone(), true
			break
		}
	}
	if !found {
		return Distribution{}, fmt.Errorf("发行包 %q 未登记：已登记的是 %s；新发行包必须先采集证据再登记", appName, registeredNames())
	}

	rel := filepath.Join(segs[bundleAt+1:]...)
	if rel != filepath.Clean(EntrySuffix) {
		return Distribution{}, fmt.Errorf("%s 的入口位置是 %q，不是登记的 %q：同目录的其他程序不属于随包 AI CLI", appName, rel, EntrySuffix)
	}
	return dist, nil
}

func registeredNames() string {
	names := make([]string, 0, len(distributions))
	for _, d := range distributions {
		names = append(names, fmt.Sprintf("%s（%s，%s）", d.AppName, d.AppVersion, d.Kind))
	}
	return strings.Join(names, "、")
}

// validate 是登记表自检：缺字段的登记项会让判断凭空成立，必须机检挡掉。
func (d Distribution) validate() []string {
	var out []string
	if !d.Kind.Valid() {
		out = append(out, fmt.Sprintf("未知发行包种类 %q", d.Kind))
	}
	if strings.TrimSpace(d.AppName) == "" || strings.ContainsAny(d.AppName, "/\\") {
		out = append(out, fmt.Sprintf("%s: AppName 必须是非空且不含路径分隔符", d.Kind))
	}
	if strings.TrimSpace(d.AppVersion) == "" {
		out = append(out, fmt.Sprintf("%s: 必须登记桌面包版本", d.Kind))
	}
	if !d.BundledCLI.Valid() {
		out = append(out, fmt.Sprintf("%s: 随包 CLI 版本必须是 x.y.z，实际 %q", d.Kind, d.BundledCLI))
	}
	if len(d.ModelIDs) == 0 {
		out = append(out, fmt.Sprintf("%s: 必须登记 --model 取值枚举", d.Kind))
	}
	if len(d.RequiredEnv) == 0 {
		out = append(out, fmt.Sprintf("%s: 必须登记启动前置环境", d.Kind))
	}
	for name, want := range d.RequiredEnv {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(want) == "" {
			out = append(out, fmt.Sprintf("%s: 环境项 %q=%q 不完整", d.Kind, name, want))
		}
	}
	return out
}
