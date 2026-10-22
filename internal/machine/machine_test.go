package machine

import "testing"

func TestAssertTransitionRejectsSameStatus(t *testing.T) {
	if err := AssertTransition("done", "done"); err == nil {
		t.Fatal("done→done must fail")
	}
	if err := AssertTransition("ready", "ready"); err == nil {
		t.Fatal("ready→ready must fail")
	}
	if err := AssertTransition("blocked", "ready"); err != nil {
		t.Fatalf("blocked→ready should allow: %v", err)
	}
	if err := AssertTransition("merging", "done"); err != nil {
		t.Fatalf("merging→done should allow: %v", err)
	}
}

func TestPreserveOnPlanApply(t *testing.T) {
	keep := []string{"done", "cancelled", "blocked", "claimed", "in_progress", "self_check", "in_review", "merging", "conflict"}
	for _, s := range keep {
		if !PreserveOnPlanApply(s) {
			t.Fatalf("%s 应保留", s)
		}
	}
	drop := []string{"backlog", "ready", ""}
	for _, s := range drop {
		if PreserveOnPlanApply(s) {
			t.Fatalf("%s 不应保留（idle 可被蓝图替换/剔除）", s)
		}
	}
}
