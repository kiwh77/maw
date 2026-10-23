package managed_test

import (
	"os"
	"os/exec"
	"testing"
)

// TestRealCodeBuddySmokeOptIn 不进默认 CI：需要本机已验证的真实 CLI。
func TestRealCodeBuddySmokeOptIn(t *testing.T) {
	if os.Getenv("MAW_MANAGED_SMOKE") != "1" {
		t.Skip("opt-in only: set MAW_MANAGED_SMOKE=1 after local CLI verification")
	}
	path, err := exec.LookPath("codebuddy")
	if err != nil {
		path, err = exec.LookPath("cbc")
	}
	if err != nil {
		t.Fatalf("real CLI not on PATH: %v", err)
	}
	cmd := exec.Command(path, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("real --version failed: %v\n%s", err, out)
	}
	t.Logf("real CLI version output (file evidence separately): %s", out)
}
