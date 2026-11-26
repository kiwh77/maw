package model_test

import (
	"errors"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

func TestIdentityNamespacesAreSeparate(t *testing.T) {
	// E111：instance_id 不得写成显示编号的样子。
	if inst, err := model.ParseInstanceID("VN-001"); err == nil {
		t.Fatalf("把显示编号解析成实例身份：%q", inst)
	} else if e, ok := model.AsError(err); !ok || e.Code != model.CodeInstanceIDShape {
		t.Fatalf("期望 E111，得到 %v", err)
	}

	// E112：display_id 不得写成实例身份的样子。
	if d, err := model.ParseDisplayID("ti_abc123"); err == nil {
		t.Fatalf("把实例身份解析成显示编号：%q", d)
	} else if e, ok := model.AsError(err); !ok || e.Code != model.CodeDisplayIDShape {
		t.Fatalf("期望 E112，得到 %v", err)
	}

	if inst, err := model.ParseInstanceID("ti_vn001a"); err != nil || string(inst) != "ti_vn001a" {
		t.Fatalf("合法实例身份被拒：%v", err)
	}
	if d, err := model.ParseDisplayID("VN-001"); err != nil || string(d) != "VN-001" {
		t.Fatalf("合法显示编号被拒：%v", err)
	}
}

func TestIdentityTypesDoNotConvertImplicitly(t *testing.T) {
	// 类型不同即编译期隔离：下面这行如果去掉显式转换就会编译失败，
	// 这是「Run 身份当任务实例用」这类混用在类型系统里的第一道闸。
	run := model.RunID("run_a001")
	inst := model.InstanceID(run)
	if inst.Valid() {
		t.Fatal("run_ 前缀的值不满足实例身份形态")
	}
	if !run.Valid() {
		t.Fatal("run_a001 应是合法运行身份")
	}
	if model.InstanceID("run_a001").Valid() {
		t.Fatal("E111 应拦住形态混用")
	}
}

func TestCommitAndHashShapes(t *testing.T) {
	if _, err := model.ParseCommit("abc1234"); err != nil {
		t.Fatalf("7 位提交应合法：%v", err)
	}
	if _, err := model.ParseCommit("HEAD~1"); err == nil {
		t.Fatal("别名不得当提交哈希（E313）")
	}
	good := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if _, err := model.ParseSHA256(good); err != nil {
		t.Fatalf("64 位哈希应合法：%v", err)
	}
	if _, err := model.ParseSHA256(good[:63]); err == nil {
		t.Fatal("63 位哈希应被拒")
	}
}

func TestProjectAndNodeIdentity(t *testing.T) {
	if !testProject.Valid() {
		t.Fatal("16 位十六进制工程 ID 应合法")
	}
	if model.ProjectID("716C30878BA1B95F").Valid() {
		t.Fatal("大写工程 ID 应被拒")
	}
	if !model.NodeID("mbp-codebuddy").Valid() {
		t.Fatal("小写节点标识应合法")
	}
	if model.NodeID("Mac Book").Valid() {
		t.Fatal("含空格的节点标识应被拒")
	}
	if !model.AgentID("codebuddy-goalagents-2").Valid() {
		t.Fatal("规范工作者名应合法")
	}
	if model.AgentID("w1").Valid() {
		t.Fatal("业务绰号式命名应被拒")
	}
}

func TestTypedErrorMatchesByCode(t *testing.T) {
	err := model.Err([]*model.Error{
		model.Violation(model.CodeInstanceIDShape, "instances[0].instance_id", "形态非法"),
		model.Violation(model.CodeLegacyAnchorMissing, "instances[0].legacy", "锚点缺失"),
	})
	if err == nil {
		t.Fatal("两条违反应压成非 nil 错误")
	}
	if !errors.Is(err, &model.Error{Code: model.CodeInstanceIDShape}) {
		t.Fatalf("errors.Is 应按首个码匹配：%v", err)
	}
	if errors.Is(err, &model.Error{Code: model.CodeVersionSequence}) {
		t.Fatal("不该匹配未出现的码")
	}
	e, ok := model.AsError(err)
	if !ok || e.Code != model.CodeInstanceIDShape {
		t.Fatalf("AsError 应取回首个类型化错误：%+v", e)
	}
}
