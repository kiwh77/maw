package workbuddy

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestAdapterLayerImports 是 P120 的机检落点：接入器不认识业务状态机，也不认识数据库。
//
// 额外一条：**不允许 import 另一个适配器**（含 codebuddy）。本卡要求「CodeBuddy 解析委托」，
// 但委托落在协议语义层——把两个适配器用 import 粘起来，会让一个包的证据悄悄变成另一个包的能力，
// 正好违背「不自动继承对方能力」。
func TestAdapterLayerImports(t *testing.T) {
	forbidden := []string{
		"github.com/kiwh77/maw/internal/app",
		"github.com/kiwh77/maw/internal/db",
		"github.com/kiwh77/maw/internal/gitops",
		"github.com/kiwh77/maw/internal/schedule",
		"github.com/kiwh77/maw/internal/machine",
		"github.com/kiwh77/maw/internal/runner",
		"github.com/kiwh77/maw/internal/nodes",
		"github.com/kiwh77/maw/internal/adapters/codebuddy",
	}
	allowedInternal := map[string]bool{
		"github.com/kiwh77/maw/internal/engine/model": true,
		"github.com/kiwh77/maw/internal/engine/ports": true,
	}
	// 端口层禁止的传输／存储依赖，适配器同样不该出现：落库与 HTTP 都不属于来源侧。
	forbiddenStd := []string{"database/sql", "net/http", "modernc.org/sqlite"}

	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列举源码失败：%v", err)
	}
	checked := 0
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		checked++
		for _, imp := range importsOf(t, path) {
			for _, bad := range forbidden {
				if imp == bad || strings.HasPrefix(imp, bad+"/") {
					t.Errorf("%s 引入了被禁止的实现依赖 %q（P120）", path, imp)
				}
			}
			for _, bad := range forbiddenStd {
				if imp == bad {
					t.Errorf("%s 引入了 %q：来源适配器不做存储与传输", path, imp)
				}
			}
			if strings.HasPrefix(imp, "github.com/kiwh77/maw/") && !allowedInternal[imp] {
				t.Errorf("%s 引入了未授权的内部包 %q：只允许 port／model", path, imp)
			}
		}
	}
	if checked == 0 {
		t.Fatal("没有找到适配器源码")
	}
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
			t.Fatalf("%s: import %q 无法解析：%v", path, spec.Path.Value, err)
		}
		out = append(out, p)
	}
	return out
}
