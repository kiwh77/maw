package identity

import "testing"

func TestLooksLikeDump(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want bool
	}{
		{"cursor-demo-1", false},
		{"codebuddy-niumabuddy-4", false},
		{`{'agent': 'cursor-niumabuddy-2', 'ok': True}`, true},
		{`{"agent":"cursor-demo-1","ok":true}`, true},
		{"export MAW_AGENT=cursor-demo-1 && maw next", true},
		{string(make([]byte, 80)), true},
	}
	for _, c := range cases {
		if got := LooksLikeDump(c.in); got != c.want {
			t.Fatalf("%q: got %v want %v", c.in[:min(40, len(c.in))], got, c.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
