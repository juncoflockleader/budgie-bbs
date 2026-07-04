package natsconn

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	nats "github.com/nats-io/nats.go"
)

type JetStreamContextOptions struct {
	Name           string
	ConnectTimeout time.Duration
	MaxWait        time.Duration
}

func OpenJetStreamCommandLog(ctx context.Context, url string, options JetStreamCommandLogOptions) (*core.BrokerCommandLog, func(), error) {
	return openJetStreamResource(ctx, url, func(ctx context.Context, conn *Conn) (*core.BrokerCommandLog, error) {
		return NewJetStreamCommandLog(ctx, conn, options)
	})
}

func OpenJetStreamEventStore(ctx context.Context, url string, options JetStreamEventLogOptions) (*core.BrokerEventStore, func(), error) {
	return openJetStreamResource(ctx, url, func(ctx context.Context, conn *Conn) (*core.BrokerEventStore, error) {
		return NewJetStreamEventStore(ctx, conn, options)
	})
}

func OpenJetStreamCommandEventStores(ctx context.Context, url string, commandOptions JetStreamCommandLogOptions, eventOptions JetStreamEventLogOptions) (*core.BrokerCommandLog, *core.BrokerCommandEventTransactionStore, *core.BrokerEventStore, func(), error) {
	conn, cleanup, err := openJetStreamConn(url)
	if err != nil {
		return nil, nil, nil, func() {}, err
	}
	commandClient, err := NewJetStreamCommandLogClient(ctx, conn, commandOptions)
	if err != nil {
		cleanup()
		return nil, nil, nil, func() {}, err
	}
	eventClient, err := NewJetStreamEventLogClient(ctx, conn, eventOptions)
	if err != nil {
		cleanup()
		return nil, nil, nil, func() {}, err
	}
	commandLog := core.NewBrokerCommandLog(commandClient)
	transactions := core.NewBrokerCommandEventTransactionStore(
		NewJetStreamCommandEventTransactionClientFromClients(commandClient, eventClient),
	)
	eventStore := core.NewBrokerEventStore(eventClient)
	return commandLog, transactions, eventStore, cleanup, nil
}

func OpenJetStreamContext(url string, options JetStreamContextOptions) (nats.JetStreamContext, func(), error) {
	conn, cleanup, err := openJetStreamConn(url, DialOptions{
		Name:    options.Name,
		Timeout: options.ConnectTimeout,
	})
	if err != nil {
		return nil, func() {}, err
	}
	maxWait := options.MaxWait
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	js, err := conn.nc.JetStream(nats.MaxWait(maxWait))
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return js, cleanup, nil
}

func openJetStreamResource[T any](ctx context.Context, url string, open func(context.Context, *Conn) (T, error)) (T, func(), error) {
	var zero T
	conn, cleanup, err := openJetStreamConn(url)
	if err != nil {
		return zero, func() {}, err
	}
	resource, err := open(ctx, conn)
	if err != nil {
		cleanup()
		return zero, func() {}, err
	}
	return resource, cleanup, nil
}

func openJetStreamConn(url string, options ...DialOptions) (*Conn, func(), error) {
	url = strings.TrimSpace(url)
	if url == "" {
		return nil, func() {}, fmt.Errorf("nats URL is required")
	}
	var dialOptions DialOptions
	if len(options) > 0 {
		dialOptions = options[0]
	}
	conn, err := DialWithOptions(url, dialOptions)
	if err != nil {
		return nil, func() {}, err
	}
	return conn, func() {
		conn.Close()
	}, nil
}
