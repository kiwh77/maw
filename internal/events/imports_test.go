package events_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/events"
)

// TestPackageHasNoImplementationDependency 是「摄取层只写事实、不碰落库与会话」的机检落点：
// 直接解析源码 import，而不是相信包注释。
//
// 允许的依赖只有标准库与契约值对象层 model；除此之外任何 internal 包都算越界——
// 尤其不许 import internal/db（落库）、internal/runner（起进程）、internal/board（展示）。
func TestPackageHasNoImplementationDependency(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列举源文件失败：%v", err)
	}
	fset := token.NewFileSet()
	var checked int
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		ast, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", f, err)
		}
		checked++
		for _, spec := range ast.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("%s 的 import 字面量非法：%v", f, err)
			}
			if !strings.HasPrefix(path, "github.com/kiwh77/maw/") {
				continue // 标准库与第三方依赖不在本检查范围（当前没有第三方依赖）
			}
			if path == "github.com/kiwh77/maw/internal/engine/model" {
				continue
			}
			t.Errorf("%s 越界依赖 %s：摄取层只允许依赖契约值对象层", f, path)
		}
	}
	if checked == 0 {
		t.Fatal("没有找到源码文件，检查形同虚设")
	}
}

// TestReasonClassExposed 从包外确认原因码能给出登记表分类，
// 上层据此决定重试还是终止，而不必再维护一份映射。
func TestReasonClassExposed(t *testing.T) {
	for _, r := range events.AllReasons() {
		if !r.Class().Valid() {
			t.Fatalf("%s 的分类非法：%s", r, r.Class())
		}
	}
	// 未登记的原因码不得被当成成功：按未知处理。
	var unknown events.Reason = "not_registered"
	if unknown.Known() {
		t.Fatal("未登记原因码不应自称已登记")
	}
	if unknown.Class().Valid() != true || string(unknown.Class()) != "unknown" {
		t.Fatalf("未登记原因码应保留现场（unknown），实际 %s", unknown.Class())
	}
}
