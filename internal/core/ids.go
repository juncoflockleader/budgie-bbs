package core

import (
	"crypto/rand"
	"crypto/sha256"
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

func deterministicAttachmentID(prefix, namespace, seed string) string {
	sum := sha256.Sum256([]byte(namespace + "\x00" + seed))
	return prefix + hex.EncodeToString(sum[:8])
}

func NewPostAttachmentID(commandID string) string {
	if commandID != "" {
		return deterministicAttachmentID("att_", "post-attachment", commandID)
	}
	return newID("att_")
}

func NewMailAttachmentID(commandID string) string {
	if commandID != "" {
		return deterministicAttachmentID("matt_", "mail-attachment", commandID)
	}
	return newID("matt_")
}

// nowMS returns the current time in milliseconds since Unix epoch.
func nowMS() int64 {
	return time.Now().UnixMilli()
}
