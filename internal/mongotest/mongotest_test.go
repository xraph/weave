package mongotest

import "testing"

func TestUsesDefaultPort(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{"explicit default port", "mongodb://localhost:27017/weave", true},
		{"default port with no path", "mongodb://localhost:27017", true},
		{"default port with credentials", "mongodb://u:p@localhost:27017/weave?authSource=admin", true},
		{"no port", "mongodb://localhost/weave", true},
		{"no port and no path", "mongodb://localhost", true},
		{"srv scheme", "mongodb+srv://cluster.example.net/weave", true},
		{"srv scheme upper case", "MONGODB+SRV://cluster.example.net/weave", true},
		{"test container", "mongodb://localhost:57021/weave", false},
		{"test container with credentials", "mongodb://u:p@localhost:57021/weave?authSource=admin", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := usesDefaultPort(tc.dsn)
			if err != nil {
				t.Fatalf("usesDefaultPort(%q): %v", tc.dsn, err)
			}
			if got != tc.want {
				t.Errorf("usesDefaultPort(%q) = %v, want %v", tc.dsn, got, tc.want)
			}
		})
	}
}
