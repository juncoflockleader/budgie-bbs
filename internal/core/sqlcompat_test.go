package core

import "testing"

func TestQueryPlaceholders(t *testing.T) {
	for _, tt := range []struct {
		n    int
		want string
	}{
		{0, ""},
		{-1, ""},
		{1, "?"},
		{3, "?,?,?"},
	} {
		if got := queryPlaceholders(tt.n); got != tt.want {
			t.Fatalf("queryPlaceholders(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestStringQueryArgs(t *testing.T) {
	got := stringQueryArgs([]string{"one", "two"})
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("stringQueryArgs = %#v, want one/two", got)
	}
}
