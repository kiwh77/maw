package systemagent

import (
	"testing"

	"github.com/kiwh77/maw/internal/config"
)

func TestParseDecision(t *testing.T) {
	d, err := ParseDecision(`{"action":"escalate_human","reason":"auth"}`)
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionEscalateHuman {
		t.Fatalf("got %s", d.Action)
	}
	d, err = ParseDecision("here is json:\n{\"action\":\"split_tasks\",\"reason\":\"big\",\"blueprint\":\"[[tasks]]\"}\n")
	if err != nil {
		t.Fatal(err)
	}
	if d.Action != ActionSplitTasks || d.Blueprint == "" {
		t.Fatalf("%+v", d)
	}
	if _, err := ParseDecision(`{"action":"nope"}`); err == nil {
		t.Fatal("expected error")
	}
}

func TestClassifyBlocked(t *testing.T) {
	cfg := config.SystemAgent{
		AutoKinds:        []string{SystemKindUnblock},
		EscalatePatterns: []string{"needs_human", "auth_required"},
	}
	en, esc := ClassifyBlocked("needs_human: please look", cfg)
	if !en || !esc {
		t.Fatalf("expected escalate enqueue")
	}
	en, esc = ClassifyBlocked("scope too large", cfg)
	if !en || esc {
		t.Fatalf("expected llm path")
	}
}

func TestOpsAllowed(t *testing.T) {
	allow := []string{"maw next", "maw merge --yes"}
	if !OpsAllowed("maw next", allow) {
		t.Fatal("exact")
	}
	if !OpsAllowed("maw next --json", allow) {
		t.Fatal("prefix")
	}
	if OpsAllowed("rm -rf /", allow) {
		t.Fatal("should deny")
	}
}
