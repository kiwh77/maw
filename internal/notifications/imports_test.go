package notifications_test

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/notifications"
)

// TestPackageHasNoImplementationDependency 是「提醒语义不绑实现」的机检落点：
// 直接解析源码 import，而不是相信包注释。
//
// 允许的依赖只有标准库与契约值对象层 model。尤其不许 import internal/db（存储实现）、
// internal/runner（起进程）、internal/httpapi（传输层）：提醒的判定与「怎么弹、
// 从哪个接口读」无关，混进来之后「谁能决定打扰用户」就说不清了。
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
				continue // 标准库与第三方依赖不在本检查范围
			}
			if path == "github.com/kiwh77/maw/internal/engine/model" {
				continue
			}
			t.Errorf("%s 越界依赖 %s：提醒层只允许依赖契约值对象层", f, path)
		}
	}
	if checked == 0 {
		t.Fatal("没有找到源码文件，检查形同虚设")
	}
}

// TestKindsAndChannelsAreClosedSets 确认枚举自洽，避免配置里出现「登记了但没人认识」的值。
func TestKindsAndChannelsAreClosedSets(t *testing.T) {
	for _, k := range notifications.AllKinds() {
		if !k.Valid() {
			t.Fatalf("类型 %s 自称不合法", k)
		}
	}
	for _, c := range notifications.AllChannels() {
		if !c.Valid() {
			t.Fatalf("渠道 %s 自称不合法", c)
		}
	}
	var unknown notifications.Kind = "pager"
	if unknown.Valid() {
		t.Fatal("未登记类型不得自称合法")
	}
	var unknownCh notifications.Channel = "sms"
	if unknownCh.Valid() {
		t.Fatal("未登记渠道不得自称合法")
	}
}

// TestDefaultPolicyCoversEveryKind 确认默认策略对每个类型都有明确决定：
// 要么给渠道，要么显式关闭——不能因为漏写而在运行时变成「静默不提醒」。
func TestDefaultPolicyCoversEveryKind(t *testing.T) {
	pol := notifications.DefaultPolicy()
	for _, k := range notifications.AllKinds() {
		if _, ok := pol.Rules[k]; !ok {
			t.Fatalf("默认策略漏掉了类型 %s", k)
		}
	}
	if len(pol.Channels(notifications.KindWaiting)) == 0 {
		t.Fatal("「在等人」必须默认提醒，否则整个功能没有意义")
	}
	if len(pol.Channels(notifications.KindFailed)) == 0 {
		t.Fatal("「失败」必须默认提醒")
	}
}

// TestStatusHandledOnlyAcked 钉住状态语义：只有 acked 算处理完。
func TestStatusHandledOnlyAcked(t *testing.T) {
	for _, s := range []notifications.Status{
		notifications.StatusPending, notifications.StatusDelivered, notifications.StatusFailed,
	} {
		if s.Handled() {
			t.Fatalf("%s 不得被当成已处理", s)
		}
		if !s.Valid() {
			t.Fatalf("%s 应属于 schema 允许的四个状态", s)
		}
	}
	if !notifications.StatusAcked.Handled() {
		t.Fatal("acked 必须是已处理")
	}
}
