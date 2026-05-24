package core

import (
	"crypto/rand"
	"encoding/hex"
	"time"
)

// newID generates a collision-resistant ID with an optional prefix.
// Format: <prefix><16 random hex chars>
func newID(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("budgie: crypto/rand unavailable: " + err.Error())
	}
	return prefix + hex.EncodeToString(b)
}

// nowMS returns the current time in milliseconds since Unix epoch.
func nowMS() int64 {
	return time.Now().UnixMilli()
}
