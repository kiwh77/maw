package identity

import (
	"os"
	"testing"
)

func TestProjectSlug(t *testing.T) {
	cases := map[string]string{
		"my-app":  "myapp",
		"DemoApp": "demoapp",
		"Foo_Bar": "foobar",
		"  X  ":   "x",
		"":        "project",
		"123abc":  "p123abc",
	}
	for in, want := range cases {
		if got := ProjectSlug(in); got != want {
			t.Fatalf("ProjectSlug(%q)=%q want %q", in, got, want)
		}
	}
}

func TestFormatParseCanonical(t *testing.T) {
	name := Format("Cursor", "my-app", 1)
	if name != "cursor-myapp-1" {
		t.Fatalf("got %s", name)
	}
	if !IsCanonical(name) {
		t.Fatal("expected canonical")
	}
	a, p, n, ok := Parse(name)
	if !ok || a != "cursor" || p != "myapp" || n != 1 {
		t.Fatalf("parse: %s %s %d %v", a, p, n, ok)
	}
	for _, bad := range []string{"w1", "cursor-admin-fe", "cursor-myapp-0", "Cursor-myapp-1"} {
		if IsCanonical(bad) {
			t.Fatalf("%s should not be canonical", bad)
		}
	}
}

func TestNextSeqAllocate(t *testing.T) {
	existing := []string{"w1", "cursor-myapp-1", "cursor-myapp-3", "codebuddy-myapp-1"}
	if got := NextSeq("cursor", "myapp", existing); got != 2 {
		t.Fatalf("next=%d want 2", got)
	}
	if got := Allocate("cursor", "my-app", existing); got != "cursor-myapp-2" {
		t.Fatalf("alloc=%s", got)
	}
	if got := Allocate("cursor", "my-app", nil); got != "cursor-myapp-1" {
		t.Fatalf("alloc empty=%s", got)
	}
}

func TestDetectAppMAWOverride(t *testing.T) {
	t.Setenv("MAW_APP", "Trae")
	if got := DetectApp(); got != "trae" {
		t.Fatalf("got %s", got)
	}
	_ = os.Unsetenv("MAW_APP")
}
