package codebuddy

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestCommandForPinsRuntime 钉住「启动不依赖宿主 PATH」这件事本身。
//
// 两种形态必须分开：
//   - 自包含二进制：executable 就是入口，argv 原样；
//   - 需要解释器的脚本：executable 换成真实运行时，脚本成为 argv[0]。
//
// 写错任一条，运行时的选择就会退回给宿主 PATH。
func TestCommandForPinsRuntime(t *testing.T) {
	args := []string{"-p", "hi", "--output-format", "stream-json"}

	t.Run("自包含二进制", func(t *testing.T) {
		ent := entry{Resolved: "/opt/tools/codebuddy", Requested: "/opt/tools/codebuddy"}
		exe, argv := commandFor(ent, args)
		if exe != ent.Resolved {
			t.Fatalf("executable 应就是入口，实际 %q", exe)
		}
		if !reflect.DeepEqual(argv, args) {
			t.Fatalf("argv 不应被改动，实际 %#v", argv)
		}
	})

	t.Run("脚本经运行时", func(t *testing.T) {
		ent := entry{
			Resolved:   "/opt/cli/bin/codebuddy",
			Runtime:    "/opt/node/bin/node",
			ViaRuntime: true,
		}
		exe, argv := commandFor(ent, args)
		if exe != ent.Runtime {
			t.Fatalf("executable 应是真实运行时，实际 %q", exe)
		}
		want := append([]string{ent.Resolved}, args...)
		if !reflect.DeepEqual(argv, want) {
			t.Fatalf("脚本必须成为 argv[0]：\n期望 %#v\n实际 %#v", want, argv)
		}
	})
}

// TestProbeRunsInTempDir 钉住探测的工作目录。
//
// 在宿主当前目录跑探测，会命中某个工程的 project settings，把「这台机器的 CLI 能不能跑」
// 变成「这个工程能不能跑」；结论会被工程配置污染。
func TestProbeRunsInTempDir(t *testing.T) {
	cfg, _, _ := testNode(t)
	fake := newFakeRunner()
	a := newAdapter(t, cfg, fake)

	if _, err := a.Probe(context.Background(), cfg); err != nil {
		t.Fatalf("探测失败：%v", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("取当前目录失败：%v", err)
	}
	for _, call := range fake.calls {
		if call.Dir == "" {
			t.Fatal("探测必须显式指定工作目录，不能靠继承")
		}
		if !filepath.IsAbs(call.Dir) {
			t.Fatalf("工作目录必须是绝对路径，实际 %q", call.Dir)
		}
		if filepath.Clean(call.Dir) == filepath.Clean(wd) {
			t.Fatalf("探测不应在宿主当前目录里跑（%q）", call.Dir)
		}
	}
}
