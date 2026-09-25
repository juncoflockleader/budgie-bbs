package natsconn

import (
	"reflect"
	"testing"
	"time"

	nats "github.com/nats-io/nats.go"
)

func TestMergeJetStreamStreamConfigAddsMissingRuntimeSettings(t *testing.T) {
	current := nats.StreamConfig{
		Name:       "BUDGIE_LOG",
		Subjects:   []string{"budgie.commands.>"},
		Duplicates: time.Minute,
	}
	desired := nats.StreamConfig{
		Name:        "BUDGIE_LOG",
		Subjects:    []string{"budgie.commands.>", "budgie.command_commits.>"},
		AllowDirect: true,
		Duplicates:  2 * time.Minute,
	}

	if !mergeJetStreamStreamConfig(&current, desired) {
		t.Fatalf("mergeJetStreamStreamConfig changed = false, want true")
	}
	if want := []string{"budgie.commands.>", "budgie.command_commits.>"}; !reflect.DeepEqual(current.Subjects, want) {
		t.Fatalf("subjects = %+v, want %+v", current.Subjects, want)
	}
	if !current.AllowDirect {
		t.Fatalf("AllowDirect = false, want true")
	}
	if current.Duplicates != 2*time.Minute {
		t.Fatalf("Duplicates = %v, want 2m", current.Duplicates)
	}

	if mergeJetStreamStreamConfig(&current, desired) {
		t.Fatalf("idempotent merge changed = true, want false")
	}
}
