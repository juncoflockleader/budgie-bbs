package logmodel

import "testing"

func TestEventAppendTimestamp(t *testing.T) {
	if got := EventAppendTimestamp(42, 100); got != 42 {
		t.Fatalf("EventAppendTimestamp explicit = %d, want 42", got)
	}
	if got := EventAppendTimestamp(0, 100); got != 100 {
		t.Fatalf("EventAppendTimestamp fallback = %d, want 100", got)
	}
}
