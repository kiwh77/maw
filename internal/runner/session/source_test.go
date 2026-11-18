package session

import (
	"testing"

	"github.com/kiwh77/maw/internal/adapters/pi"
	"github.com/kiwh77/maw/internal/engine/model"
	"github.com/kiwh77/maw/internal/runner/dbstore"
)

func TestIsPiProtocol(t *testing.T) {
	if !IsPiProtocol(model.Protocol{Format: "pi-stream-json", Version: "1"}) {
		t.Fatal("pi-stream-json/1 应被识别")
	}
	if IsPiProtocol(model.Protocol{Format: "stream-json", Version: "1"}) {
		t.Fatal("CodeBuddy 协议不得被当成 Pi")
	}
	if IsPiProtocol(model.Protocol{Format: "pi-stream-json", Version: "2"}) {
		t.Fatal("未知版本不得放行")
	}
}

func TestSourceForRoutesPiProtocol(t *testing.T) {
	src, err := SourceFor(model.LaunchSpec{
		Executable:       "/usr/bin/node",
		Argv:             []string{"/abs/runtimes/pi/src/main.mjs"},
		ExpectedProtocol: model.Protocol{Format: "pi-stream-json", Version: "1"},
	})
	if err != nil {
		t.Fatalf("SourceFor: %v", err)
	}
	if _, ok := src.(*pi.Adapter); !ok {
		t.Fatalf("Pi 协议应选 Pi 适配器，得到 %T", src)
	}

	// 未识别的协议仍走 passthrough：只按退出码判定，不假装理解来源语义。
	src, err = SourceFor(model.LaunchSpec{
		Executable:       "/usr/bin/env",
		Argv:             []string{"echo", "ok"},
		ExpectedProtocol: model.Protocol{Format: "passthrough", Version: "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := src.(dbstore.PassthroughSource); !ok {
		t.Fatalf("未知协议应回退 passthrough，得到 %T", src)
	}
}
