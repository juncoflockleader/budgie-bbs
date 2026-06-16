// Package mailer sends outbound internet email. The default native path
// delivers directly to the recipient domain's MX (zero config, but easily
// spam-filtered); a configured SMTP relay is used instead when set, which is
// also how an email service provider (SendGrid/SES/Postmark, etc.) is wired in.
// The Mailer interface leaves room for a future HTTP/API provider.
package mailer

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/mail"
	"net/smtp"
	"strings"
	"syscall"
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

// validateRecipient rejects an address that is malformed or carries control
// characters. Without this, a recipient like "v@x.com\r\nBcc: e@evil.com" would
// smuggle extra headers into the composed message (M1), and an attacker-chosen
// address is also the SSRF vector for the direct mailer (H4).
func validateRecipient(addr string) error {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return fmt.Errorf("mailer: empty recipient")
	}
	if strings.ContainsAny(addr, "\r\n\x00") {
		return fmt.Errorf("mailer: recipient contains control characters")
	}
	parsed, err := mail.ParseAddress(addr)
	if err != nil {
		return fmt.Errorf("mailer: invalid recipient %q: %w", addr, err)
	}
	// Reject display-name forms ("Name <a@b>") — we only send to bare addresses,
	// and the angle-bracket form is another way to sneak content into headers.
	if parsed.Address != addr {
		return fmt.Errorf("mailer: recipient must be a bare address")
	}
	return nil
}

// sanitizeHeader strips CR/LF so a value cannot inject additional headers.
func sanitizeHeader(v string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(v)
}

// buildMessage renders an RFC 5322 message. Header values are CRLF-stripped so
// no field can inject extra headers (the recipient is also validated upstream).
func buildMessage(msg Message) []byte {
	var b strings.Builder
	b.WriteString("From: " + sanitizeHeader(msg.From) + "\r\n")
	b.WriteString("To: " + sanitizeHeader(msg.To) + "\r\n")
	b.WriteString("Subject: " + sanitizeHeader(msg.Subject) + "\r\n")
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
	if err := validateRecipient(msg.To); err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	var auth smtp.Auth
	if m.user != "" {
		auth = smtp.PlainAuth("", m.user, m.pass, m.host)
	}
	// net/smtp.SendMail negotiates STARTTLS when the relay advertises it. The
	// relay host is operator-configured and trusted, so no SSRF IP guard here.
	return smtp.SendMail(addr, auth, m.from, []string{msg.To}, buildMessage(msg))
}

// --- direct to MX ---

type directMailer struct {
	from string
}

func (m *directMailer) Send(ctx context.Context, msg Message) error {
	if err := validateRecipient(msg.To); err != nil {
		return err
	}
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
		if err := m.deliver(ctx, host, msg); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("mailer: direct delivery to %s failed: %w", domain, lastErr)
}

// deliver dials the MX through a guarded dialer that refuses to connect to
// private/loopback/link-local addresses, so an attacker-chosen recipient domain
// (e.g. x@127.0.0.1 or x@169.254.169.254) cannot turn outbound SMTP into an
// SSRF probe of internal hosts. The IP check runs on the actually-resolved
// address (Dialer.Control), which also defeats DNS-rebinding to internal IPs.
func (m *directMailer) deliver(ctx context.Context, host string, msg Message) error {
	d := net.Dialer{Timeout: 30 * time.Second, Control: blockPrivateControl}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, "25"))
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer func() { _ = c.Close() }()
	if ok, _ := c.Extension("STARTTLS"); ok {
		if err := c.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return err
		}
	}
	if err := c.Mail(m.from); err != nil {
		return err
	}
	if err := c.Rcpt(msg.To); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(buildMessage(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// isPublicIP reports whether ip is a routable public address — i.e. not
// loopback, private (RFC1918 / ULA fc00::/7), link-local (incl. the
// 169.254.169.254 cloud-metadata address), multicast, or unspecified.
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	return true
}

// blockPrivateControl is a net.Dialer.Control hook that rejects dialing any
// non-public address. It runs after DNS resolution with the concrete IP.
func blockPrivateControl(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("mailer: refusing to dial malformed address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil || !isPublicIP(ip) {
		return fmt.Errorf("mailer: refusing to dial non-public address %s", host)
	}
	return nil
}
