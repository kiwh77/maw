package scope

import "testing"

func TestOverlap(t *testing.T) {
	o := ScopesOverlap([]string{"src/orders/**"}, []string{"src/orders/api.py"})
	if len(o) == 0 {
		t.Fatal("expected overlap")
	}
	o = ScopesOverlap([]string{"src/a/**"}, []string{"src/b/**"})
	if len(o) != 0 {
		t.Fatal("expected no overlap")
	}
}

func TestInScope(t *testing.T) {
	if !PathInScope("src/a/x.py", []string{"src/a/**"}) {
		t.Fatal("should be in scope")
	}
	if PathInScope("src/b/x.py", []string{"src/a/**"}) {
		t.Fatal("should be out of scope")
	}
}
