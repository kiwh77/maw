package gitops

import "testing"

func TestIsDirtyTree(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"working tree dirty in /tmp/x: M a.go", true},
		{"工作区不干净 /tmp: M a.go", true},
		{"squash merge conflict", false},
		{"gate failed: exit 1", false},
	}
	for _, c := range cases {
		if got := IsDirtyTree(c.msg); got != c.want {
			t.Fatalf("%q: got %v want %v", c.msg, got, c.want)
		}
	}
}
