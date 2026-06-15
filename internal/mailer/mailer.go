// Package mailer sends outbound internet email. The default native path
// delivers directly to the recipient domain's MX (zero config, but easily
// spam-filtered); a configured SMTP relay is used instead when set, which is
// also how an email service provider (SendGrid/SES/Postmark, etc.) is wired in.
// The Mailer interface leaves room for a future HTTP/API provider.
package mailer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Message is a plain-text email.
type Message struct {
	From    string
	To      string
	Subject string
	Body    string
}

// Mailer delivers a Message.
type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

const (
	ModeOff    = "off"
	ModeDirect = "direct"
	ModeRelay  = "relay"
)

// Config selects and configures the mailer.
type Config struct {
	Mode     string // off | direct | relay
	From     string // envelope + header From; required to send
	Host     string // relay host (relay mode)
	Port     int    // relay port (default 587)
	Username string // relay auth (optional)
	Password string // relay auth (optional)
}

// New builds a Mailer for the config. Returns (nil, nil) when disabled.
func New(cfg Config) (Mailer, error) {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	if mode == "" || mode == ModeOff {
		return nil, nil
	}
	if strings.TrimSpace(cfg.From) == "" {
		return nil, fmt.Errorf("mailer: a From address is required (set -mail-from)")
	}
	switch mode {
	case ModeDirect:
		return &directMailer{from: cfg.From}, nil
	case ModeRelay:
		if strings.TrimSpace(cfg.Host) == "" {
			return nil, fmt.Errorf("mailer: relay mode requires a host")
		}
		port := cfg.Port
		if port == 0 {
			port = 587
		}
		return &relayMailer{from: cfg.From, host: cfg.Host, port: port, user: cfg.Username, pass: cfg.Password}, nil
	default:
		return nil, fmt.Errorf("mailer: unknown mode %q (want off, direct, or relay)", mode)
	}
}

// buildMessage renders an RFC 5322 message.
func buildMessage(msg Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + msg.From + "\r\n")
	b.WriteString("To: " + msg.To + "\r\n")
	b.WriteString("Subject: " + msg.Subject + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: <" + messageID(msg.From) + ">\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(msg.Body, "\n", "\r\n"))
	return []byte(b.String())
}

func messageID(from string) string {
	r := make([]byte, 12)
	_, _ = rand.Read(r)
	host := "budgie.local"
	if at := strings.LastIndex(from, "@"); at >= 0 && at+1 < len(from) {
		host = from[at+1:]
	}
	return hex.EncodeToString(r) + "@" + host
}

func emailDomain(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 0 || at+1 >= len(addr) {
		return ""
	}
	return strings.TrimSpace(addr[at+1:])
}

// --- relay ---

type relayMailer struct {
	from, host, user, pass string
	port                   int
}

func (m *relayMailer) Send(ctx context.Context, msg Message) error {
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	var auth smtp.Auth
	if m.user != "" {
		auth = smtp.PlainAuth("", m.user, m.pass, m.host)
	}
	// net/smtp.SendMail negotiates STARTTLS when the relay advertises it.
	return smtp.SendMail(addr, auth, m.from, []string{msg.To}, buildMessage(msg))
}

// --- direct to MX ---

type directMailer struct {
	from string
}

func (m *directMailer) Send(ctx context.Context, msg Message) error {
	domain := emailDomain(msg.To)
	if domain == "" {
		return fmt.Errorf("mailer: invalid recipient %q", msg.To)
	}
	mxs, err := net.DefaultResolver.LookupMX(ctx, domain)
	if err != nil || len(mxs) == 0 {
		// Fall back to the domain itself as an implicit MX (RFC 5321 §5.1).
		mxs = []*net.MX{{Host: domain, Pref: 0}}
	}
	var lastErr error
	for _, mx := range mxs {
		host := strings.TrimSuffix(mx.Host, ".")
		if err := smtp.SendMail(host+":25", nil, m.from, []string{msg.To}, buildMessage(msg)); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("mailer: direct delivery to %s failed: %w", domain, lastErr)
}
