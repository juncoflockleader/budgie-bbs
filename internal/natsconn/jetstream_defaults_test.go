package natsconn

import (
	"slices"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	nats "github.com/nats-io/nats.go"
)

func TestJetStreamDefaults(t *testing.T) {
	if got := JetStreamName("  custom  ", "fallback"); got != "custom" {
		t.Fatalf("JetStreamName trim = %q, want custom", got)
	}
	if got := JetStreamName(" ", "fallback"); got != "fallback" {
		t.Fatalf("JetStreamName fallback = %q, want fallback", got)
	}
	if got := jetStreamWait(0); got != defaultJetStreamEventLogWait {
		t.Fatalf("jetStreamWait fallback = %v, want %v", got, defaultJetStreamEventLogWait)
	}
	if got := jetStreamWait(2 * time.Second); got != 2*time.Second {
		t.Fatalf("jetStreamWait explicit = %v, want 2s", got)
	}
	if got := jetStreamReplicas(0); got != 1 {
		t.Fatalf("jetStreamReplicas fallback = %d, want 1", got)
	}
	if got := jetStreamReplicas(3); got != 3 {
		t.Fatalf("jetStreamReplicas explicit = %d, want 3", got)
	}
	if got := jetStreamDuration(0, time.Minute); got != time.Minute {
		t.Fatalf("jetStreamDuration fallback = %v, want 1m", got)
	}
	if got := jetStreamDuration(5*time.Second, time.Minute); got != 5*time.Second {
		t.Fatalf("jetStreamDuration explicit = %v, want 5s", got)
	}
}

func TestJetStreamCommandLogStreamConfig(t *testing.T) {
	cfg := JetStreamCommandLogStreamConfig(JetStreamCommandLogOptions{
		Stream:          " BUDGIE_COMMAND_LOG_LOAD ",
		Replicas:        3,
		DuplicateWindow: time.Hour,
	})
	if cfg.Name != "BUDGIE_COMMAND_LOG_LOAD" {
		t.Fatalf("command stream name = %q, want trimmed custom name", cfg.Name)
	}
	if cfg.Retention != nats.LimitsPolicy || cfg.Storage != nats.FileStorage || !cfg.AllowDirect {
		t.Fatalf("command stream config = %+v, want limits/file/direct", cfg)
	}
	if cfg.Replicas != 3 || cfg.Duplicates != time.Hour {
		t.Fatalf("command stream replicas/window = %d/%v, want 3/1h", cfg.Replicas, cfg.Duplicates)
	}
	if !slices.Contains(cfg.Subjects, core.BrokerCommandSubjectWildcard()) ||
		!slices.Contains(cfg.Subjects, core.BrokerCommandCommitSubjectWildcard()) {
		t.Fatalf("command stream subjects = %+v, want command and commit wildcards", cfg.Subjects)
	}
}

func TestJetStreamEventLogStreamConfigDefaults(t *testing.T) {
	cfg := JetStreamEventLogStreamConfig(JetStreamEventLogOptions{})
	if cfg.Name != DefaultJetStreamEventLogStream {
		t.Fatalf("event stream name = %q, want default", cfg.Name)
	}
	if cfg.Replicas != 1 || cfg.Duplicates != defaultJetStreamEventLogDuplicateWindow {
		t.Fatalf("event stream replicas/window = %d/%v, want default replicas/window", cfg.Replicas, cfg.Duplicates)
	}
	if len(cfg.Subjects) != 1 || cfg.Subjects[0] != core.BrokerEventSubjectWildcard() {
		t.Fatalf("event stream subjects = %+v, want event wildcard", cfg.Subjects)
	}
}
