package command

import "testing"

func TestPrimaryPrefix(t *testing.T) {
	cases := []struct {
		name     string
		prefixes []string
		want     string
	}{
		{"nil", nil, "/*"},
		{"empty", []string{}, "/*"},
		{"blank", []string{"", "   "}, "/*"},
		{"slash", []string{"/"}, "/"},
		{"custom", []string{"/*"}, "/*"},
		{"first non-empty wins", []string{"/", "/*"}, "/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PrimaryPrefix(tc.prefixes); got != tc.want {
				t.Fatalf("PrimaryPrefix(%#v) = %q, want %q", tc.prefixes, got, tc.want)
			}
		})
	}
}

func TestRouterDefaultPrefix(t *testing.T) {
	r := NewRouter(nil)

	if got := r.PrimaryPrefix(); got != "/*" {
		t.Fatalf("PrimaryPrefix() = %q, want /*", got)
	}
	if prefixes := r.Prefixes(); len(prefixes) != 1 || prefixes[0] != "/*" {
		t.Fatalf("Prefixes() = %#v, want [/*]", prefixes)
	}

	parsed := r.Parse("/*help model")
	if !parsed.OK || parsed.Prefix != "/*" || parsed.Name != "help" || parsed.Args != "model" {
		t.Fatalf("Parse(/*help model) = %#v", parsed)
	}
	if !r.IsCommand("/*help") {
		t.Fatal("/*help should be a command with the default prefix")
	}
	if r.IsCommand("/help") {
		t.Fatal("/help should not match the default /* prefix")
	}
}
