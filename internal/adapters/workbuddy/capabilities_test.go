package workbuddy

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
		EvidenceDir string `json:"evidence_dir"`
	} `json:"source"`
	Distributions []struct {
		Kind       string `json:"kind"`
		BundledCLI string `json:"bundled_cli"`
	} `json:"distributions"`
	Capabilities []struct {
		ID         string   `json:"id"`
		Capability string   `json:"capability"`
		State      string   `json:"state"`
		Samples    []string `json:"samples"`
	} `json:"capabilities"`
}

func readMatrix(t *testing.T) matrixDoc {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "docs", "evidence", "workbuddy", "capability-matrix.json"))
	if err != nil {
		t.Fatalf("读证据矩阵失败（本卡依赖 VN-305 证据产物）：%v", err)
	}
	var doc matrixDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析证据矩阵失败：%v", err)
	}
	return doc
}

// TestCapabilityMatrixMatchesEvidence 把代码里的能力表与证据矩阵逐条对账。
//
// 这条测试是「消费证据」的机检落点：代码里多一条、少一条、状态被悄悄改过，都会红。
func TestCapabilityMatrixMatchesEvidence(t *testing.T) {
	doc := readMatrix(t)

	if doc.Source.EvidenceDir != EvidenceRoot {
		t.Fatalf("证据目录应为 %q，代码写的是 %q", EvidenceRoot, doc.Source.EvidenceDir)
	}
	if len(doc.Capabilities) != len(capabilityMatrix) {
		t.Fatalf("能力条数不符：证据 %d 条，代码 %d 条", len(doc.Capabilities), len(capabilityMatrix))
	}

	cn, _ := Lookup(DistributionCN)
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
		ref := cn.Sample(got.Sample)
		if !strings.HasPrefix(ref, EvidenceRoot+"/") {
			t.Errorf("%s 的证据引用必须在 %s 之下，实际 %q", want.ID, EvidenceRoot, ref)
		}
		if len(want.Samples) == 0 {
			if !strings.Contains(ref, "capability-matrix.json") {
				t.Errorf("%s 没有样本，证据引用应指向矩阵本身，实际 %q", want.ID, ref)
			}
			continue
		}
		if !strings.Contains(ref, want.Samples[0]) {
			t.Errorf("%s 应挂样本 %s 的证据，实际 %q", want.ID, want.Samples[0], ref)
		}
	}
}

// TestMatrixDistributionsMatchRegistry 保证证据矩阵里的两包与代码登记表一致。
func TestMatrixDistributionsMatchRegistry(t *testing.T) {
	doc := readMatrix(t)
	if len(doc.Distributions) != len(distributions) {
		t.Fatalf("证据登记 %d 个发行包，代码登记 %d 个", len(doc.Distributions), len(distributions))
	}
	for _, got := range doc.Distributions {
		dist, ok := Lookup(DistributionKind(got.Kind))
		if !ok {
			t.Errorf("证据里有未登记的发行包 %q", got.Kind)
			continue
		}
		if got.BundledCLI != string(dist.BundledCLI) {
			t.Errorf("%s 随包 CLI 版本不符：证据 %q，代码 %q", got.Kind, got.BundledCLI, dist.BundledCLI)
		}
	}
}

// TestCapabilitySetIsContractValid 保证能力表能通过契约校验（P140：版本 + 证据）。
func TestCapabilitySetIsContractValid(t *testing.T) {
	for _, kind := range []DistributionKind{DistributionCN, DistributionIntl} {
		dist, _ := Lookup(kind)
		for _, version := range []model.SemVer{dist.BundledCLI, "2.162.0"} {
			for _, c := range capabilitySet(dist, version, true) {
				if vs := c.Validate("cap[" + c.Name + "]"); len(vs) != 0 {
					t.Errorf("%s %s 不合契约：%v", kind, c.Name, model.Err(vs))
				}
				if c.Version != version {
					t.Errorf("%s %s 的版本应绑定本次探测版本 %s，实际 %s", kind, c.Name, version, c.Version)
				}
			}
		}
	}
}

// TestCapabilitySetKeepsDegradedStates 覆盖「未知不得被升级」。
func TestCapabilitySetKeepsDegradedStates(t *testing.T) {
	dist, _ := Lookup(DistributionCN)
	idx := capabilityIndex(capabilitySet(dist, dist.BundledCLI, true))

	for _, name := range []string{capStreamJSON, capPartialMsgs, capRecordSeq, capResultRecord, capUsage, capResume} {
		if idx[name] != model.CapabilityUnobserved {
			t.Errorf("%s 应保持 unobserved，实际 %s", name, idx[name])
		}
	}
	for _, name := range []string{capNumTurns, capCancel} {
		if idx[name] != model.CapabilityUnknown {
			t.Errorf("%s 应保持 unknown，实际 %s", name, idx[name])
		}
	}
	if idx[capModelShared] != model.CapabilityUnsupported {
		t.Errorf("%s 应保持 unsupported，实际 %s", capModelShared, idx[capModelShared])
	}

	// 未知能力不得参与自动匹配。
	for name, state := range idx {
		if state != model.CapabilityObserved && state.AutoMatchable() {
			t.Errorf("%s 是 %s，不应可自动匹配", name, state)
		}
	}
}

// TestCapabilitySetDegradesWithoutHelp 覆盖帮助探测没跑成时，依赖帮助清单的行全部降级。
func TestCapabilitySetDegradesWithoutHelp(t *testing.T) {
	dist, _ := Lookup(DistributionCN)
	idx := capabilityIndex(capabilitySet(dist, dist.BundledCLI, false))

	for _, row := range capabilityMatrix {
		if !row.NeedsHelp {
			continue
		}
		if idx[row.Name] == model.CapabilityObserved {
			t.Errorf("%s 依赖帮助清单，帮助探测失败后不应是 observed", row.Name)
		}
	}
	if idx[capVersionProbe] != model.CapabilityObserved {
		t.Errorf("entry.version_probe 不依赖帮助清单，应保持 observed，实际 %s", idx[capVersionProbe])
	}
}
