package natsconn

import (
	"context"
	"crypto/tls"
	"os"
	"time"

	nats "github.com/nats-io/nats.go"
)

// Conn adapts nats.go to core.NATSConn.
type Conn struct {
	nc *nats.Conn
}

func Dial(url string) (*Conn, error) {
	opts := append([]nats.Option{nats.Name("budgie-bbs"), nats.Timeout(5 * time.Second)}, securityOptionsFromEnv()...)
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, err
	}
	return &Conn{nc: nc}, nil
}

// securityOptionsFromEnv wires NATS authentication/TLS from the environment.
// Without auth, command-log ActorID forgery is prevented only by network
// isolation (identity is trusted from whoever can publish to the subject). All
// options are opt-in: an unset environment yields the prior behavior, so this
// is backward compatible.
func securityOptionsFromEnv() []nats.Option {
	var opts []nats.Option
	if creds := os.Getenv("BUDGIE_NATS_CREDS"); creds != "" {
		opts = append(opts, nats.UserCredentials(creds)) // .creds file (JWT + nkey)
	}
	if token := os.Getenv("BUDGIE_NATS_TOKEN"); token != "" {
		opts = append(opts, nats.Token(token))
	}
	if user := os.Getenv("BUDGIE_NATS_USER"); user != "" {
		opts = append(opts, nats.UserInfo(user, os.Getenv("BUDGIE_NATS_PASSWORD")))
	}
	if ca := os.Getenv("BUDGIE_NATS_CA"); ca != "" {
		opts = append(opts, nats.RootCAs(ca))
	}
	if os.Getenv("BUDGIE_NATS_TLS") == "1" {
		opts = append(opts, nats.Secure(&tls.Config{MinVersion: tls.VersionTLS12}))
	}
	return opts
}

func (c *Conn) Publish(ctx context.Context, subject string, payload []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return c.nc.Publish(subject, payload)
}

func (c *Conn) Subscribe(subject string, handler func(data []byte)) (func() error, error) {
	sub, err := c.nc.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})
	if err != nil {
		return nil, err
	}
	if err := c.nc.FlushTimeout(5 * time.Second); err != nil {
		_ = sub.Unsubscribe()
		return nil, err
	}
	return sub.Unsubscribe, nil
}

func (c *Conn) Close() {
	if c != nil && c.nc != nil {
		c.nc.Close()
	}
}
