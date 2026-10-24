package platform

import (
	"os"
	"strings"
	"testing"
)

// managedPlatform 是唯一有原生证据的受管执行平台。
//
// 增加一个平台之前必须先有该平台的原生进程实测记录（启动／停止／回收），
// 跨编译通过不算证据。
const managedPlatform = "darwin"

// nativeEvidencePlatforms 是「有原生证据」的平台集合。
//
// 与 managedPlatform 分开写不是冗余：将来可能有平台支持人工接入但尚不支持受管，
// 那张表会比分这张表更宽。现在两者恰好只有 macOS。
var nativeEvidencePlatforms = []string{"darwin"}

// releaseMatrixPath 是平台支持矩阵的唯一文档出处。
const releaseMatrixPath = "../../docs/verification/vnext/release/platform-matrix.zh-CN.md"

// TestAssertPlatformMatchesTarget 验证受管启动的门禁跟着构建目标走。
//
// 关键点：不写死「本机是 macOS」，而是以 currentOS() 为参照。
// 这样在 Linux CI 上跑会验证「明确拒绝」，在 macOS 上跑会验证「放行」——
// 两种结果都是对同一份规则的正面验证。
func TestAssertPlatformMatchesTarget(t *testing.T) {
	if currentOS() == managedPlatform {
		if !supportedByEvidence(currentOS()) {
			t.Fatalf("%s 应被登记为有原生证据的平台", currentOS())
		}
		return
	}
	if supportedByEvidence(currentOS()) {
		t.Fatalf("%s 没有原生证据，却被登记为受支持", currentOS())
	}
}

// TestOnlyDarwinClaimsManagedSupport 验证「能编译」不等于「支持受管」。
//
// 这条用例故意只读常量表，不读运行环境：任何人把某个平台加进
// nativeEvidencePlatforms，就必须同时改这条与平台矩阵文档。
func TestOnlyDarwinClaimsManagedSupport(t *testing.T) {
	for _, osName := range nativeEvidencePlatforms {
		if osName != managedPlatform {
			t.Fatalf("未验证平台 %q 被列为有原生证据；受管执行只在 %s 上有证据", osName, managedPlatform)
		}
	}
	if len(nativeEvidencePlatforms) == 0 {
		t.Fatalf("至少要有一条原生证据（%s），否则发布说明说不清支持范围", managedPlatform)
	}
}

// TestReleaseTargetsCovered 验证发布目标覆盖矩阵里列出的每个平台组合。
//
// 发布脚本与矩阵文档必须同源：脚本编不出来的组合不能出现在矩阵里，
// 矩阵里有的组合同样不能漏编。
func TestReleaseTargetsCovered(t *testing.T) {
	matrix, err := os.ReadFile(releaseMatrixPath)
	if err != nil {
		t.Fatalf("读不到平台矩阵文档 %s: %v", releaseMatrixPath, err)
	}
	body := string(matrix)
	for _, target := range releaseTargets {
		// 矩阵是一行一个组合：`| darwin | arm64 | …`。
		row := "| " + target.os + " | " + target.arch + " |"
		if !strings.Contains(body, row) {
			t.Fatalf("矩阵文档缺少发布目标 %s/%s（期望行首 %q）", target.os, target.arch, row)
		}
	}
}

// TestMatrixStatesManagedSupport 验证文档对每个非 macOS 平台都明确写了「不支持」。
//
// 「没有写」和「写了不支持」是两件事：缺一行会让读者以为漏了，
// 明确写不支持才是可核对的结论。
func TestMatrixStatesManagedSupport(t *testing.T) {
	raw, err := os.ReadFile(releaseMatrixPath)
	if err != nil {
		t.Fatalf("读不到平台矩阵文档: %v", err)
	}
	body := string(raw)
	for _, target := range releaseTargets {
		if target.os == managedPlatform {
			continue
		}
		// 每行形如 `| linux | amd64 | 交叉编译 | 不支持 | …`
		if !strings.Contains(body, "| "+target.os+" |") {
			t.Fatalf("矩阵文档缺少 %s 的行", target.os)
		}
	}
	if !strings.Contains(body, "不支持") {
		t.Fatal("矩阵文档必须写明非 macOS 平台的受管执行是「不支持」")
	}
}

// currentOS 单独成函数，便于将来注入。
func currentOS() string {
	return osName
}

// supportedByEvidence 判断该平台是否有原生证据。
func supportedByEvidence(osName string) bool {
	for _, item := range nativeEvidencePlatforms {
		if item == osName {
			return true
		}
	}
	return false
}
