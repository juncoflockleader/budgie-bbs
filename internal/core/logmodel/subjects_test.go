package logmodel

import (
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeScopesTrimsDedupesAndSorts(t *testing.T) {
	got := NormalizeScopes([]string{
		" thread:thr_1 ",
		"",
		"board:general",
		"thread:thr_1",
		"  chat:lobby",
	})
	want := []string{"board:general", "chat:lobby", "thread:thr_1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeScopes = %#v, want %#v", got, want)
	}
}

func TestPartitionKeyRoundTripEscapesUnsafeTokens(t *testing.T) {
	partition := Partition{Kind: "board.topic", Key: "general/space:alpha"}
	key := PartitionKey(partition)
	if strings.ContainsAny(key, "/=: \t\n") {
		t.Fatalf("partition key %q contains unsafe delimiter characters", key)
	}
	decoded, ok := ParsePartitionKey(key)
	if !ok {
		t.Fatalf("ParsePartitionKey(%q) failed", key)
	}
	if decoded != partition.Normalize() {
		t.Fatalf("decoded partition = %+v, want %+v", decoded, partition.Normalize())
	}
}
