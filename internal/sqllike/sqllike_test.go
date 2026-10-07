package sqllike

import "testing"

func TestEscape(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"50%", `50\%`},
		{"a_b", `a\_b`},
		{`a\b`, `a\\b`},
		{`%_\`, `\%\_\\`},
		{".*(", ".*("},
	}
	for _, tt := range tests {
		if got := Escape(tt.in); got != tt.want {
			t.Errorf("Escape(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
