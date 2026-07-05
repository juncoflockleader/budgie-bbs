package logmodel

import "testing"

func TestNormalizePartitionFieldsTrimsAndDefaults(t *testing.T) {
	kind, key := NormalizePartitionFields(" board ", "\tgeneral\n")
	if kind != PartitionBoard || key != "general" {
		t.Fatalf("NormalizePartitionFields = %q/%q, want board/general", kind, key)
	}

	kind, key = NormalizePartitionFields(" ", "")
	if kind != PartitionGlobal || key != PartitionGlobal {
		t.Fatalf("NormalizePartitionFields empty = %q/%q, want global/global", kind, key)
	}
}
