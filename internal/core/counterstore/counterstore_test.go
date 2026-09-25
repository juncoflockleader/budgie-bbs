package counterstore

import "testing"

func TestShardForIdentityIsStable(t *testing.T) {
	if got := ShardForIdentity(""); got != 0 {
		t.Fatalf("ShardForIdentity empty = %d, want 0", got)
	}
	if got, want := ShardForIdentity("alice"), ShardForIdentity("alice"); got != want {
		t.Fatalf("ShardForIdentity repeated = %d, want %d", got, want)
	}
	if got := ShardForIdentity("alice"); got < 0 || got >= 64 {
		t.Fatalf("ShardForIdentity = %d, want [0,64)", got)
	}
}
