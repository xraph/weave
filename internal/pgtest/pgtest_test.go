package pgtest

import "testing"

func TestUsesDefaultPort(t *testing.T) {
	// pgx falls back to PGPORT when a DSN names no port; pin it so the cases
	// mean the same thing on every machine.
	t.Setenv("PGPORT", "")
	cases := []struct {
		name string
		dsn  string
		want bool
	}{
		{"url with port and path", "postgres://u:p@localhost:5432/weave?sslmode=disable", true},
		{"url with port and no path", "postgres://u:p@localhost:5432?sslmode=disable", true},
		{"url with port at the end", "postgres://u:p@localhost:5432", true},
		{"url with no port", "postgres://u:p@localhost/weave", true},
		{"postgresql scheme with no port", "postgresql://u:p@localhost/weave", true},
		{"keyword form with port", "host=localhost port=5432 user=u dbname=weave", true},
		{"keyword form with no port", "host=localhost user=u dbname=weave", true},
		{"test container url", "postgres://postgres:weave@localhost:55621/weave?sslmode=disable", false},
		{"test container keyword form", "host=localhost port=55621 user=postgres dbname=weave", false},
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

func TestUsesDefaultPortRejectsUnparsableDSN(t *testing.T) {
	if _, err := usesDefaultPort("postgres://u:p@localhost:notaport/weave"); err == nil {
		t.Fatal("want an error for a DSN pgx cannot parse")
	}
}

func TestPinSearchPath(t *testing.T) {
	url, err := pinSearchPath("postgres://postgres:weave@localhost:55621/weave?sslmode=disable", "s1")
	if err != nil || url != "postgres://postgres:weave@localhost:55621/weave?search_path=s1&sslmode=disable" {
		t.Errorf("url form: got %q, %v", url, err)
	}
	kw, err := pinSearchPath("host=localhost port=55621", "s1")
	if err != nil || kw != "host=localhost port=55621 search_path=s1" {
		t.Errorf("keyword form: got %q, %v", kw, err)
	}
}
