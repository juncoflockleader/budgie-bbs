package sqlstore

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
		if got := QueryPlaceholders(tt.n); got != tt.want {
			t.Fatalf("QueryPlaceholders(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestStringQueryArgs(t *testing.T) {
	got := StringQueryArgs([]string{"one", "two"})
	if len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("StringQueryArgs = %#v, want one/two", got)
	}
}

func TestRebindPlaceholdersForPostgres(t *testing.T) {
	oldFlavor := Flavor()
	SetFlavor(PostgresFlavor)
	t.Cleanup(func() { SetFlavor(oldFlavor) })

	got := RebindPlaceholders("SELECT * FROM posts WHERE id=? AND board=?")
	if got != "SELECT * FROM posts WHERE id=$1 AND board=$2" {
		t.Fatalf("RebindPlaceholders select = %q", got)
	}

	got = RebindPlaceholders("INSERT OR IGNORE INTO processed_commands_v2 (cid, actor_id) VALUES (?, ?);")
	want := "INSERT INTO processed_commands_v2 (cid, actor_id) VALUES ($1, $2) ON CONFLICT DO NOTHING;"
	if got != want {
		t.Fatalf("RebindPlaceholders insert ignore = %q, want %q", got, want)
	}
}
