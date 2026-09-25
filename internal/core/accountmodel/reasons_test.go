package accountmodel

import (
	"strings"
	"testing"
)

func TestNormalizeAccountClosureReason(t *testing.T) {
	if got := NormalizeAccountClosureReason("  cleanup note  "); got != "cleanup note" {
		t.Fatalf("NormalizeAccountClosureReason() = %q, want trimmed reason", got)
	}
	long := strings.Repeat("x", MaxAccountClosureReasonLength+1)
	if got := NormalizeAccountClosureReason(long); len(got) != MaxAccountClosureReasonLength {
		t.Fatalf("NormalizeAccountClosureReason() length = %d, want %d", len(got), MaxAccountClosureReasonLength)
	}
}
