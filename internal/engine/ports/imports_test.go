package ports_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/ports"
)

// TestPortLayerHasNoImplementationDependency 是「端口无 HTTP、SQLite 和厂商进程依赖」
// 的机检落点：直接解析源码 import，而不是相信注释。
func TestPortLayerHasNoImplementationDependency(t *testing.T) {
	files := goFiles(t, ".")
	if len(files) == 0 {
		t.Fatal("没有找到端口层源码")
	}
	sawModel := false
	for _, f := range files {
		for _, imp := range importsOf(t, f) {
			if ports.ForbiddenForPort(imp) {
				t.Errorf("%s 引入了被禁止的实现依赖 %q", filepath.Base(f), imp)
			}
			if isInternal(imp) {
				if !ports.AllowedInternalImport(imp) {
					t.Errorf("%s 引入了非值对象的内部包 %q", filepath.Base(f), imp)
				}
				if strings.HasSuffix(imp, "/internal/engine/model") {
					sawModel = true
				}
			}
		}
	}
	if !sawModel {
		t.Fatal("端口层必须引用 model 定义的值对象，而不是自己复制一套身份")
	}
}

// TestModelLayerHasNoInternalDependency 保证值对象层是真正的叶子：
// 它被 app / adapter / infra 任意引用都不会形成反向依赖。
func TestModelLayerHasNoInternalDependency(t *testing.T) {
	files := goFiles(t, "../model")
	if len(files) == 0 {
		t.Fatal("没有找到值对象层源码")
	}
	for _, f := range files {
		for _, imp := range importsOf(t, f) {
			if isInternal(imp) {
				t.Errorf("%s 不得依赖任何 maw 内部包，实际引入 %q", filepath.Base(f), imp)
			}
			if ports.ForbiddenForPort(imp) {
				t.Errorf("%s 引入了外部副作用依赖 %q", filepath.Base(f), imp)
			}
		}
	}
}

func goFiles(t *testing.T, dir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatalf("列举 %s 失败：%v", dir, err)
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if strings.HasSuffix(m, "_test.go") || strings.HasSuffix(m, "doc.go") {
			continue
		}
		out = append(out, m)
	}
	return out
}

func importsOf(t *testing.T, path string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", path, err)
	}
	out := make([]string, 0, len(f.Imports))
	for _, spec := range f.Imports {
		p, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("%s: import 路径 %q 无法解析：%v", path, spec.Path.Value, err)
		}
		out = append(out, p)
	}
	return out
}

func isInternal(importPath string) bool {
	return strings.HasPrefix(importPath, "github.com/kiwh77/maw/")
}
