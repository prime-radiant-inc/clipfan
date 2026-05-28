package daemon

import "testing"

func TestHostsMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"paradise-park", "paradise-park", true},
		{"paradise-park.local", "paradise-park", true},
		{"jesse-paradise-park", "paradise-park", true},
		{"paradise-park", "jesse-paradise-park", true},
		{"jesse-paradise-park", "jesse-paradise-park", true},
		{"flower-garden.local", "flower-garden", true},
		{"flower-garden", "magic-kingdom", false},
		{"park", "paradise-park", false},
		{"jesse-paradise-park", "magic-kingdom", false},
	}
	for _, tt := range tests {
		if got := hostsMatch(tt.a, tt.b); got != tt.want {
			t.Errorf("hostsMatch(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}
