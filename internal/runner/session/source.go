package session

import (
	"runtime"
	"strings"

	"github.com/kiwh77/maw/internal/adapters/codebuddy"
	"github.com/kiwh77/maw/internal/adapters/pi"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/dbstore"
)

// SourceFor 按启动规格选来源协议实现。
//
// CodeBuddy 实测过的协议是 stream-json v1：命中就实例化 CodeBuddy adapter，
// 让阶段推进、错误分类与用量按来源真实字段解释；其余协议回退
// PassthroughSource——它只按退出码判定，不假装理解来源语义。
//
// 判定口径是协议而不是厂商名：受管节点信息还没进 run_meta，先用实测协议把
// CodeBuddy 这条路接上；节点注册接入后改按 node.adapter_kind 判定。
func SourceFor(spec model.LaunchSpec) (Source, error) {
	if IsPiProtocol(spec.ExpectedProtocol) {
		return newPiSource(spec)
	}
	if !IsCodeBuddyProtocol(spec.ExpectedProtocol) {
		return dbstore.PassthroughSource{}, nil
	}
	ad, err := codebuddy.New(model.NodeConfig{
		NodeID:    "node_codebuddy",
		Name:      "codebuddy",
		Adapter:   model.AdapterCodeBuddy,
		EntryPath: spec.Executable,
		Platform:  runtime.GOOS,
		Mode:      model.NodeManagedCLI,
		SlotLimit: 1,
		AcceptNew: true,
	})
	if err != nil {
		return nil, err
	}
	return ad, nil
}

// IsPiProtocol 报告协议是否是 Pi 运行时冻结的那一套（pi-stream-json/1）。
func IsPiProtocol(p model.Protocol) bool {
	return strings.TrimSpace(p.Format) == pi.ProtocolFormat &&
		strings.TrimSpace(p.Version) == pi.ProtocolVersion
}

// newPiSource 按启动规格构造 Pi 适配器。
//
// 入口脚本取 argv 的最后一个参数（Launch 只放一个参数：运行时入口）；
// 可执行文件即 node。两者都在启动规格里冻结，适配器不再猜路径。
func newPiSource(spec model.LaunchSpec) (Source, error) {
	entry := strings.TrimSpace(spec.Executable)
	if len(spec.Argv) > 0 {
		entry = strings.TrimSpace(spec.Argv[len(spec.Argv)-1])
	}
	ad, err := pi.New(model.NodeConfig{
		NodeID:      "node_pi",
		Name:        "pi",
		Adapter:     model.AdapterPi,
		EntryPath:   entry,
		RuntimePath: spec.Executable,
		Platform:    runtime.GOOS,
		Mode:        model.NodeManagedCLI,
		SlotLimit:   1,
		AcceptNew:   true,
	})
	if err != nil {
		return nil, err
	}
	return ad, nil
}

// IsCodeBuddyProtocol 报告协议是否是 CodeBuddy 实测过的那一套。
//
// 「大概是兼容的」不算兼容：版本对不上就走回退路径，由人确认后再加版本。
func IsCodeBuddyProtocol(p model.Protocol) bool {
	return strings.TrimSpace(p.Format) == "stream-json" &&
		strings.TrimSpace(p.Version) == codebuddy.ProtocolVersion
}
