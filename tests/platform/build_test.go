package platform

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// osName 是本次构建实际跑的平台。
var osName = runtime.GOOS

// releaseTarget 是发布脚本要产出的组合。
type releaseTarget struct {
	os   string
	arch string
}

// releaseTargets 与 `.github/workflows/release.yml` 的目标列表必须一致。
//
// 两边不同步的症状很隐蔽：某个平台没编出来，发布页上只是少了一个包。
var releaseTargets = []releaseTarget{
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
}

const repoRoot = "../.."

// TestCrossBuildTargets 验证每个发布目标都能交叉编译。
//
// 交叉编译通过只证明「代码能编到那个平台」，**不**证明能起子进程、
// 能复用 worktree、能停进程——那些要原生实测。用例名与注释都按这个边界写，
// 避免把绿灯当能力证据。
func TestCrossBuildTargets(t *testing.T) {
	if testing.Short() {
		t.Skip("交叉编译在 -short 下跳过")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("没有 go 命令，跳过交叉编译检查")
	}
	outDir := t.TempDir()
	for _, target := range releaseTargets {
		target := target
		t.Run(target.os+"/"+target.arch, func(t *testing.T) {
			out := filepath.Join(outDir, "maw_"+target.os+"_"+target.arch)
			if target.os == "windows" {
				out += ".exe"
			}
			cmd := exec.Command("go", "build", "-trimpath", "-o", out, "./cmd/maw")
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(),
				"CGO_ENABLED=0",
				"GOOS="+target.os,
				"GOARCH="+target.arch,
			)
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("交叉编译 %s/%s 失败: %v\n%s", target.os, target.arch, err, output)
			}
			info, err := os.Stat(out)
			if err != nil || info.Size() == 0 {
				t.Fatalf("交叉编译 %s/%s 未产出可用文件: %v", target.os, target.arch, err)
			}
		})
	}
}

// TestFrontendBuildPrerequisites 验证前端产物与构建入口都在。
//
// CI 的浏览器任务依赖这几样东西；缺一样都会在任务里表现为「找不到文件」，
// 而不是「样式不对」，先在这里说清楚。
func TestFrontendBuildPrerequisites(t *testing.T) {
	mustExist := []string{
		"internal/board/web/package.json",
		"internal/board/web/vite.config.ts",
		"internal/board/web/src/main.ts",
		"internal/board/assets/shell/board-shell.mjs",
		"internal/board/assets/dist/board-web.js",
		"tests/browser/package.json",
		"tests/browser/playwright.config.ts",
		"tests/browser/cmd/harness/main.go",
	}
	for _, rel := range mustExist {
		info, err := os.Stat(filepath.Join(repoRoot, rel))
		if err != nil {
			t.Fatalf("缺少前端构建前置条件 %s: %v", rel, err)
		}
		if info.Size() == 0 {
			t.Fatalf("前置条件 %s 是空文件", rel)
		}
	}

	// 打包入口必须真的引用 shell：否则 bundle 里的工作台会是空的。
	main, err := os.ReadFile(filepath.Join(repoRoot, "internal/board/web/src/main.ts"))
	if err != nil {
		t.Fatalf("读不到 main.ts: %v", err)
	}
	if !strings.Contains(string(main), "assets/shell/board-shell.mjs") {
		t.Fatal("main.ts 没有引用 assets/shell/board-shell.mjs，打包产物不会带上工作台")
	}

	// 浏览器用例锁死 playwright 版本，CI 才能复现同一浏览器。
	pkg, err := os.ReadFile(filepath.Join(repoRoot, "tests/browser/package.json"))
	if err != nil {
		t.Fatalf("读不到 tests/browser/package.json: %v", err)
	}
	if !strings.Contains(string(pkg), "@playwright/test") {
		t.Fatal("tests/browser/package.json 未声明 @playwright/test")
	}
}

// TestDesktopBuildPrerequisites 验证桌面壳的构建前置条件。
//
// CI 不跑完整的 tauri 构建（要装 Rust 工具链且耗时长），但前置条件必须可检查：
// 通知插件、能力声明、导航守卫与桥接源都要在，否则桌面构建会在很后面才失败。
func TestDesktopBuildPrerequisites(t *testing.T) {
	base := filepath.Join(repoRoot, "clients/desktop/src-tauri")
	for _, rel := range []string{"Cargo.toml", "tauri.conf.json", "build.rs", "src/main.rs", "src/bridge.rs", "src/pump.rs"} {
		if info, err := os.Stat(filepath.Join(base, rel)); err != nil || info.Size() == 0 {
			t.Fatalf("桌面壳缺少 %s: %v", rel, err)
		}
	}

	capabilities, err := os.ReadFile(filepath.Join(base, "capabilities/default.json"))
	if err != nil {
		t.Fatalf("读不到 capabilities/default.json: %v", err)
	}
	if !strings.Contains(string(capabilities), "notification:default") {
		t.Fatal("capabilities 未声明 notification:default，桌面提醒不会生效")
	}
	if err := json.Unmarshal(capabilities, new(map[string]any)); err != nil {
		t.Fatalf("capabilities/default.json 不是合法 JSON: %v", err)
	}

	cargo, err := os.ReadFile(filepath.Join(base, "Cargo.toml"))
	if err != nil {
		t.Fatalf("读不到 Cargo.toml: %v", err)
	}
	if !strings.Contains(string(cargo), "tauri-plugin-notification") {
		t.Fatal("Cargo.toml 未依赖 tauri-plugin-notification")
	}
}
