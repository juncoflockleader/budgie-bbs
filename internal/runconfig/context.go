package runconfig

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// InterruptTimeoutContext returns a context canceled by SIGINT/SIGTERM and,
// when timeout is positive, by a deadline. Call the returned cleanup once.
func InterruptTimeoutContext(parent context.Context, timeout time.Duration) (context.Context, func()) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	if timeout <= 0 {
		return ctx, stop
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, func() {
		cancel()
		stop()
	}
}
