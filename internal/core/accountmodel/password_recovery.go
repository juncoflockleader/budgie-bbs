package accountmodel

import (
	"fmt"
	"strings"
)

type PasswordHashFunc func(password string) (string, error)

func PasswordRecoveryReviewHash(decision, newPassword string, hash PasswordHashFunc) (string, error) {
	if strings.ToLower(strings.TrimSpace(decision)) != "reset" {
		return "", nil
	}
	if strings.TrimSpace(newPassword) == "" {
		return "", fmt.Errorf("new password required")
	}
	return hash(newPassword)
}
