package natsconn

import (
	"strings"
	"time"
)

// JetStreamName trims a stream or bucket name and returns fallback when it is blank.
func JetStreamName(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	return raw
}

func jetStreamWait(raw time.Duration) time.Duration {
	if raw <= 0 {
		return defaultJetStreamEventLogWait
	}
	return raw
}

func jetStreamReplicas(raw int) int {
	if raw <= 0 {
		return 1
	}
	return raw
}

func jetStreamDuration(raw, fallback time.Duration) time.Duration {
	if raw <= 0 {
		return fallback
	}
	return raw
}
