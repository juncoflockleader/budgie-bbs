package logmodel

import "testing"

func TestNewCommandPartitionClaimNormalizesOwnerAndPartition(t *testing.T) {
	global := Partition{Kind: PartitionGlobal, Key: PartitionGlobal}.Normalize()
	got := NewCommandPartitionClaim(Partition{}, " writer-a ", 42)
	want := CommandPartitionClaim{Partition: global, OwnerID: "writer-a", ExpiresAt: 42}
	if got != want {
		t.Fatalf("NewCommandPartitionClaim = %+v, want %+v", got, want)
	}
}
