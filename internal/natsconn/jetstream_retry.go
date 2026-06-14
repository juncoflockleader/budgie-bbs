package natsconn

import (
	"context"
	"time"
)

func waitJetStreamCASRetry(ctx context.Context, attempt, maxAttempts int) error {
	if attempt+1 >= maxAttempts {
		return nil
	}
	delay := time.Duration(attempt+1) * time.Millisecond
	if delay > 25*time.Millisecond {
		delay = 25 * time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
