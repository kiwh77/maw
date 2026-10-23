package codebuddy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResolveEntryPinsRuntime 覆盖真实形态：入口是指向脚本的符号链接，脚本首行 `#!/usr/bin/env node`。
// 断言的正是「真实绝对入口 + 真实运行时」，且路径里带空格也不能被拆开。
func TestResolveEntryPinsRuntime(t *testing.T) {
	cfg, script, nodeBin := testNode(t)

	ent, err := resolveEntry(cfg.EntryPath, cfg.RuntimePath)
	if err != nil {
		t.Fatalf("解析入口失败：%v", err)
	}
	if ent.Requested != cfg.EntryPath {
		t.Fatalf("Requested 应保留配置原值，实际 %q", ent.Requested)
	}
	if ent.Resolved != mustReal(t, script) {
		t.Fatalf("Resolved 应是展开链接后的真实路径 %q，实际 %q", mustReal(t, script), ent.Resolved)
	}
	if !ent.ViaRuntime {
		t.Fatal("脚本入口必须经过运行时")
	}
	if ent.Runtime != mustReal(t, nodeBin) {
		t.Fatalf("Runtime 应是真实 node 路径 %q，实际 %q", mustReal(t, nodeBin), ent.Runtime)
	}
	if !strings.Contains(ent.Resolved, " ") {
		t.Fatalf("夹具应带空格路径，实际 %q", ent.Resolved)
	}
}

// TestResolveEntryFollowsSymlinkChain 覆盖 `bin/codebuddy -> ../lib/…/codebuddy` 这类链接链。
func TestResolveEntryFollowsSymlinkChain(t *testing.T) {
	_, script, _ := testNode(t)
	link1 := filepath.Join(t.TempDir(), "codebuddy")
	if err := os.Symlink(script, link1); err != nil {
		t.Fatalf("建链接失败：%v", err)
	}
	link2 := filepath.Join(t.TempDir(), "cbc")
	if err := os.Symlink(link1, link2); err != nil {
		t.Fatalf("建链接失败：%v", err)
	}

	ent, err := resolveEntry(link2, "")
	if err != nil {
		t.Fatalf("解析链接链失败：%v", err)
	}
	if ent.Resolved != mustReal(t, script) {
		t.Fatalf("应穿透两级链接到 %q，实际 %q", mustReal(t, script), ent.Resolved)
	}
}

// TestResolveEntryRejects 覆盖入口失效的四种形态；这些都必须报错而不是「尽力启动」。
func TestResolveEntryRejects(t *testing.T) {
	_, script, _ := testNode(t)

	notExec := filepath.Join(t.TempDir(), "plain")
	mustWrite(t, notExec, "not executable\n", 0o644)

	dirAsEntry := filepath.Join(t.TempDir(), "adir")
	if err := os.MkdirAll(dirAsEntry, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}

	broken := filepath.Join(t.TempDir(), "dangling")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), broken); err != nil {
		t.Fatalf("建断链失败：%v", err)
	}

	cases := []struct {
		name string
		path string
	}{
		{"相对路径", "bin/codebuddy"},
		{"不存在的入口", filepath.Join(t.TempDir(), "missing")},
		{"没有可执行权限", notExec},
		{"目录", dirAsEntry},
		{"断链", broken},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := resolveEntry(c.path, ""); err == nil {
				t.Fatalf("%s 必须被拒绝", c.name)
			}
		})
	}

	// 正例对照：同一个夹具的合法入口必须能解析，避免上面的拒绝来自夹具本身有问题。
	if _, err := resolveEntry(script, ""); err != nil {
		t.Fatalf("合法入口不应失败：%v（平台 %s）", err, runtime.GOOS)
	}
}

// TestResolveEntryNativeBinary 覆盖自包含二进制：没有 shebang 就不需要运行时。
func TestResolveEntryNativeBinary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "native codebuddy")
	mustWrite(t, bin, "\x7fELF-not-really\n", 0o755)

	ent, err := resolveEntry(bin, "")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if ent.ViaRuntime || ent.Runtime != "" {
		t.Fatalf("无 shebang 的入口不应绑定运行时，实际 runtime=%q via=%v", ent.Runtime, ent.ViaRuntime)
	}
}

// TestResolveEntryExplicitRuntime 覆盖配置里显式给了运行时的情况：以配置为准。
func TestResolveEntryExplicitRuntime(t *testing.T) {
	_, _, nodeBin := testNode(t)
	// 入口换成没有 shebang 的文件，运行时只可能来自配置。
	plain := filepath.Join(t.TempDir(), "plain entry")
	mustWrite(t, plain, "binary\n", 0o755)

	ent, err := resolveEntry(plain, nodeBin)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if !ent.ViaRuntime || ent.Runtime != mustReal(t, nodeBin) {
		t.Fatalf("应使用配置里的运行时 %q，实际 runtime=%q via=%v", mustReal(t, nodeBin), ent.Runtime, ent.ViaRuntime)
	}
}

// TestResolveEntryMissingInterpreter 覆盖「入口在，运行时不在」：不能降级成直接执行脚本。
func TestResolveEntryMissingInterpreter(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	script := filepath.Join(t.TempDir(), "codebuddy")
	mustWrite(t, script, "#!/usr/bin/env node-not-installed\nconsole.log(1)\n", 0o755)

	if _, err := resolveEntry(script, ""); err == nil {
		t.Fatal("解释器不在 PATH 时必须报错")
	}
}
