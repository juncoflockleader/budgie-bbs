package natsconn

import (
	"context"
	"time"

	nats "github.com/nats-io/nats.go"
)

// Conn adapts nats.go to core.NATSConn.
type Conn struct {
	nc *nats.Conn
}

func Dial(url string) (*Conn, error) {
	nc, err := nats.Connect(url, nats.Name("budgie-bbs"), nats.Timeout(5*time.Second))
	if err != nil {
		return nil, err
	}
	return &Conn{nc: nc}, nil
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
