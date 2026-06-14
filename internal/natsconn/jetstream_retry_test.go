package natsconn

import (
	"context"
	"testing"
)

func TestWaitJetStreamCASRetryHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitJetStreamCASRetry(ctx, 0, 2); err == nil {
		t.Fatal("waitJetStreamCASRetry returned nil for canceled retry context")
	}
}

func TestWaitJetStreamCASRetrySkipsDelayAfterFinalAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitJetStreamCASRetry(ctx, 1, 2); err != nil {
		t.Fatalf("waitJetStreamCASRetry final attempt err = %v, want nil", err)
	}
}
