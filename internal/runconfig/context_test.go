package runconfig

import (
	"context"
	"testing"
	"time"
)

func TestInterruptTimeoutContextSetsDeadline(t *testing.T) {
	ctx, cleanup := InterruptTimeoutContext(context.Background(), time.Minute)
	defer cleanup()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("InterruptTimeoutContext should set a deadline when timeout is positive")
	}
}

func TestInterruptTimeoutContextCleanupCancels(t *testing.T) {
	ctx, cleanup := InterruptTimeoutContext(context.Background(), 0)
	cleanup()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("cleanup should cancel interrupt context")
	}
}
