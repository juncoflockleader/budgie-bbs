package natsconn

import (
	"context"
	"strings"
	"testing"
)

func TestOpenJetStreamHelpersRejectMissingURL(t *testing.T) {
	requireMissingURL := func(name string, cleanup func(), err error) {
		t.Helper()
		cleanup()
		requireErrorContains(t, err, "nats URL is required")
	}
	_, cleanup, err := OpenJetStreamCommandLog(context.Background(), " ", JetStreamCommandLogOptions{})
	requireMissingURL("OpenJetStreamCommandLog", cleanup, err)
	_, cleanup, err = OpenJetStreamEventStore(context.Background(), "", JetStreamEventLogOptions{})
	requireMissingURL("OpenJetStreamEventStore", cleanup, err)
	_, _, _, cleanup, err = OpenJetStreamCommandEventStores(context.Background(), "", JetStreamCommandLogOptions{}, JetStreamEventLogOptions{})
	requireMissingURL("OpenJetStreamCommandEventStores", cleanup, err)
	_, cleanup, err = OpenJetStreamContext("", JetStreamContextOptions{})
	requireMissingURL("OpenJetStreamContext", cleanup, err)
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want containing %q", err, want)
	}
}
