package codebuddy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiwh77/maw/internal/engine/model"
)

// matrixDoc 是证据矩阵里本包要对照的字段子集。
type matrixDoc struct {
	Source struct {
		CLIVersion  string `json:"cli_version"`
		EvidenceDir string `json:"evidence_dir"`
	} `json:"source"`
	Capabilities []struct {
		ID         string   `json:"id"`
		Capability string   `json:"capability"`
		State      string   `json:"state"`
		Samples    []string `json:"samples"`
	} `json:"capabilities"`
}

// TestCapabilityMatrixMatchesEvidence 把代码里的能力表与 VN-301 的证据矩阵逐条对账。
//
// 这条测试是「消费证据」的机检落点：代码里多一条、少一条、状态被悄悄改过，都会红。
// 证据矩阵是证据目录里的事实文件，代码不能成为第二份互相矛盾的真相。
func TestCapabilityMatrixMatchesEvidence(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "evidence", "codebuddy", "capability-matrix.json"))
	if err != nil {
		t.Fatalf("读证据矩阵失败（本卡依赖 VN-301 产物）：%v", err)
	}
	var doc matrixDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析证据矩阵失败：%v", err)
	}

	if doc.Source.CLIVersion != string(MeasuredCLIVersion) {
		t.Fatalf("证书矩阵实测版本是 %q，代码绑定 %q：版本一变必须重采而不是改常量", doc.Source.CLIVersion, MeasuredCLIVersion)
	}
	if doc.Source.EvidenceDir != EvidenceRoot {
		t.Fatalf("证据目录应为 %q，代码写的是 %q", doc.Source.EvidenceDir, EvidenceRoot)
	}
	if len(doc.Capabilities) != len(capabilityMatrix) {
		t.Fatalf("能力条数不符：证据 %d 条，代码 %d 条", len(doc.Capabilities), len(capabilityMatrix))
	}

	for i, want := range doc.Capabilities {
		got := capabilityMatrix[i]
		if got.ID != want.ID {
			t.Fatalf("第 %d 条编号不符：证据 %s，代码 %s", i, want.ID, got.ID)
		}
		if got.Name != want.Capability {
			t.Errorf("%s 能力名不符：证据 %q，代码 %q", want.ID, want.Capability, got.Name)
		}
		if string(got.State) != want.State {
			t.Errorf("%s 状态不符：证据 %q，代码 %q", want.ID, want.State, got.State)
		}
		if !strings.HasPrefix(got.Evidence, EvidenceRoot+"/") {
			t.Errorf("%s 的证据引用必须在 %s 之下，实际 %q", want.ID, EvidenceRoot, got.Evidence)
		}
		if len(want.Samples) == 0 {
			if !strings.Contains(got.Evidence, "capability-matrix.json") {
				t.Errorf("%s 没有样本，证据引用应指向矩阵本身，实际 %q", want.ID, got.Evidence)
			}
			continue
		}
		for _, sample := range want.Samples {
			if !strings.Contains(got.Evidence, sample) && !sampleListed(got.Evidence, want.Samples) {
				t.Errorf("%s 应挂样本 %s 的证据，实际 %q", want.ID, sample, got.Evidence)
			}
		}
	}
}

// sampleListed 判定证据引用是否落在该能力的任一实测样本上。
func sampleListed(evidence string, samples []string) bool {
	for _, s := range samples {
		if strings.Contains(evidence, s) {
			return true
		}
	}
	return false
}

// TestCapabilitySetIsContractValid 保证能力表能通过契约校验（P140：版本 + 证据）。
func TestCapabilitySetIsContractValid(t *testing.T) {
	for _, version := range []model.SemVer{MeasuredCLIVersion, "2.174.0"} {
		for _, c := range capabilitySet(version, true) {
			if vs := c.Validate("cap[" + c.Name + "]"); len(vs) != 0 {
				t.Errorf("%s 不合契约：%v", c.Name, model.Err(vs))
			}
			if c.Version != version {
				t.Errorf("%s 的版本应绑定本次探测版本 %s，实际 %s", c.Name, version, c.Version)
			}
		}
	}
}

// TestCapabilitySetKeepsUnknown 覆盖「未知不得被升级」：unknown／unobserved 行在任何版本下都不翻成 observed。
func TestCapabilitySetKeepsUnknown(t *testing.T) {
	idx := capabilityIndex(capabilitySet(MeasuredCLIVersion, true))
	for _, name := range []string{"usage.aggregation_semantics", "turn.num_turns_semantics", "cost.total_cost_usd", capResume, "cancel.graceful_stop"} {
		if idx[name] != model.CapabilityUnknown {
			t.Errorf("%s 应保持 unknown，实际 %s", name, idx[name])
		}
	}
	for _, name := range []string{"session.id_reuse", "error.stable_code", "tool_use.events", "stderr.carries_error"} {
		if idx[name] != model.CapabilityUnobserved {
			t.Errorf("%s 应保持 unobserved，实际 %s", name, idx[name])
		}
	}
	// 未知能力不得参与自动匹配。
	for name, state := range idx {
		if state != model.CapabilityObserved && state.AutoMatchable() {
			t.Errorf("%s 是 %s，不应可自动匹配", name, state)
		}
	}
}
