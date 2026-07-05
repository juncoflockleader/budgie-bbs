package logmodel

import "testing"

func TestEventStoreProjectionWatermarkNameNormalizesSourceAndPartition(t *testing.T) {
	got := EventStoreProjectionWatermarkName(" Broker ", Partition{})
	want := "event-store-projection:YnJva2Vy:Z2xvYmFs:Z2xvYmFs"
	if got != want {
		t.Fatalf("EventStoreProjectionWatermarkName = %q, want %q", got, want)
	}
}
