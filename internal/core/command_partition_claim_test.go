package core

import "testing"

func TestCommandPartitionClaimForOwnerNormalizesOwnerAndPartition(t *testing.T) {
	global := LogPartition{Kind: partitionGlobal, Key: partitionGlobal}.Normalize()
	got := commandPartitionClaimForOwner(LogPartition{}, " writer-a ", 42)
	want := CommandPartitionClaim{Partition: global, OwnerID: "writer-a", ExpiresAt: 42}
	if got != want {
		t.Fatalf("commandPartitionClaimForOwner = %+v, want %+v", got, want)
	}
}
