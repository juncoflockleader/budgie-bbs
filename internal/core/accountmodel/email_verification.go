package accountmodel

import (
	"strings"
	"time"
)

const EmailVerificationTokenTTL = 24 * time.Hour

type VerificationEmailMessage struct {
	Subject string
	Body    string
}

func VerificationEmail(baseURL, token string) VerificationEmailMessage {
	link := "/api/v1/auth/verify-email?token=" + token
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL != "" {
		link = baseURL + link
	}
	return VerificationEmailMessage{
		Subject: "Confirm your email",
		Body: "Welcome! Please confirm your email address by opening this link:\n\n" +
			link + "\n\nThis link expires in 24 hours. If you did not sign up, ignore this message.\n",
	}
}
