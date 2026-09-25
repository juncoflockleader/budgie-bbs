package httpapi

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/ratelimit"
)

// maxRetryAfter returns the longest current lockout across the given keys on a
// limiter, or 0 if all are allowed.
func maxRetryAfter(l *ratelimit.Limiter, keys ...string) time.Duration {
	return l.MaxRetryAfter(keys...)
}

// writeRateLimited sends a 429 with a Retry-After header (seconds).
func writeRateLimited(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(math.Ceil(retryAfter.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "rate_limited", "too many attempts; try again later", true)
}
