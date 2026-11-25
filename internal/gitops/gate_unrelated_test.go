package gitops

import "testing"

func TestIsUnrelatedRepoGate(t *testing.T) {
	t.Parallel()
	msg := "gate failed: go test ./...\n--- FAIL: TestX (0.00s)\nFAIL\tgithub.com/x/y/internal/runner/process\t0.1s"
	scope := []string{"internal/board/**"}
	if !IsUnrelatedRepoGate(msg, scope) {
		t.Fatal("expected unrelated")
	}
	scope2 := []string{"internal/runner/**"}
	if IsUnrelatedRepoGate(msg, scope2) {
		t.Fatal("runner path is in scope")
	}
}

func TestMergeIgnorablePath(t *testing.T) {
	t.Parallel()
	if !MergeIgnorablePath("TASK.md") || !MergeIgnorablePath(".maw/task.json") {
		t.Fatal("expected ignorable")
	}
	if MergeIgnorablePath("internal/foo.go") {
		t.Fatal("code path not ignorable")
	}
}
