package natsconn

import (
	"context"
	"errors"
	"fmt"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	nats "github.com/nats-io/nats.go"
)

func JetStreamCommandLogStreamConfig(options JetStreamCommandLogOptions) nats.StreamConfig {
	return nats.StreamConfig{
		Name:        JetStreamName(options.Stream, defaultJetStreamCommandLogStream),
		Subjects:    []string{core.BrokerCommandSubjectWildcard(), core.BrokerCommandCommitSubjectWildcard()},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		Replicas:    jetStreamReplicas(options.Replicas),
		AllowDirect: true,
		Duplicates:  jetStreamDuration(options.DuplicateWindow, defaultJetStreamCommandLogDuplicateWindow),
	}
}

func JetStreamEventLogStreamConfig(options JetStreamEventLogOptions) nats.StreamConfig {
	return nats.StreamConfig{
		Name:        JetStreamName(options.Stream, defaultJetStreamEventLogStream),
		Subjects:    []string{core.BrokerEventSubjectWildcard()},
		Retention:   nats.LimitsPolicy,
		Storage:     nats.FileStorage,
		Replicas:    jetStreamReplicas(options.Replicas),
		AllowDirect: true,
		Duplicates:  jetStreamDuration(options.DuplicateWindow, defaultJetStreamEventLogDuplicateWindow),
	}
}

func ensureJetStreamStream(ctx context.Context, js nats.JetStreamContext, config nats.StreamConfig) error {
	info, err := js.StreamInfo(config.Name, nats.Context(ctx))
	if errors.Is(err, nats.ErrStreamNotFound) {
		_, err = js.AddStream(&config, nats.Context(ctx))
		return err
	}
	if err != nil {
		return err
	}
	current := info.Config
	if !mergeJetStreamStreamConfig(&current, config) {
		return nil
	}
	_, err = js.UpdateStream(&current, nats.Context(ctx))
	return err
}

func validateJetStreamStream(ctx context.Context, js nats.JetStreamContext, stream, label string, subjects []string) error {
	info, err := js.StreamInfo(stream, nats.Context(ctx))
	if err != nil {
		return err
	}
	if !info.Config.AllowDirect {
		return fmt.Errorf("%s: stream %s does not allow direct reads", label, stream)
	}
	for _, subject := range subjects {
		if !hasSubject(info.Config.Subjects, subject) {
			return fmt.Errorf("%s: stream %s does not include subject %s", label, stream, subject)
		}
	}
	return nil
}

func mergeJetStreamStreamConfig(current *nats.StreamConfig, desired nats.StreamConfig) bool {
	if current == nil {
		return false
	}
	changed := false
	for _, subject := range desired.Subjects {
		if !hasSubject(current.Subjects, subject) {
			current.Subjects = append(current.Subjects, subject)
			changed = true
		}
	}
	if desired.AllowDirect && !current.AllowDirect {
		current.AllowDirect = true
		changed = true
	}
	if desired.Duplicates > 0 && current.Duplicates != desired.Duplicates {
		current.Duplicates = desired.Duplicates
		changed = true
	}
	return changed
}

func hasSubject(subjects []string, want string) bool {
	for _, subject := range subjects {
		if subject == want {
			return true
		}
	}
	return false
}
