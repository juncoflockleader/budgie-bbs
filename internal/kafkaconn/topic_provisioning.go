package kafkaconn

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
)

const defaultTopicProvisioningTimeout = 30 * time.Second

// TopicProvisioningSpec describes a Kafka topic shape the runtime requires.
type TopicProvisioningSpec struct {
	Topic             string
	Partitions        int32
	ReplicationFactor int16
}

// TopicProvisioningOptions configures Kafka topic setup and verification.
type TopicProvisioningOptions struct {
	Runtime  RuntimeConfig
	ClientID string
	Topics   []TopicProvisioningSpec
	Timeout  time.Duration
}

// EnsureTopics creates missing topics and verifies existing topics satisfy the
// requested partition counts without relying on broker auto-create defaults.
func EnsureTopics(ctx context.Context, options TopicProvisioningOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime := options.Runtime.Normalize()
	if len(runtime.Brokers) == 0 {
		return fmt.Errorf("kafka topic provisioning: broker list is required")
	}
	clientID := strings.TrimSpace(options.ClientID)
	if clientID == "" {
		return fmt.Errorf("kafka topic provisioning: client id is required")
	}
	opts, err := RuntimeClientOpts(runtime, clientID)
	if err != nil {
		return err
	}
	client, err := kgo.NewClient(opts...)
	if err != nil {
		return fmt.Errorf("kafka topic provisioning: create admin client: %w", err)
	}
	defer client.Close()
	return ensureTopicsWithRequestor(ctx, client, options.Topics, options.Timeout)
}

func ensureTopicsWithRequestor(ctx context.Context, requestor kmsg.Requestor, specs []TopicProvisioningSpec, timeout time.Duration) error {
	if requestor == nil {
		return fmt.Errorf("kafka topic provisioning: requestor is required")
	}
	specs, err := normalizeTopicProvisioningSpecs(specs)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return nil
	}
	timeout = effectiveTopicProvisioningTimeout(timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := createKafkaTopics(ctx, requestor, specs, timeout); err != nil {
		return err
	}
	return waitForKafkaTopicMetadata(ctx, requestor, specs)
}

func normalizeTopicProvisioningSpecs(specs []TopicProvisioningSpec) ([]TopicProvisioningSpec, error) {
	out := make([]TopicProvisioningSpec, 0, len(specs))
	seen := map[string]bool{}
	for _, spec := range specs {
		topic := strings.TrimSpace(spec.Topic)
		if topic == "" {
			return nil, fmt.Errorf("kafka topic provisioning: topic name is required")
		}
		if seen[topic] {
			return nil, fmt.Errorf("kafka topic provisioning: duplicate topic %q", topic)
		}
		if spec.Partitions <= 0 {
			return nil, fmt.Errorf("kafka topic provisioning: topic %q requires a positive partition count", topic)
		}
		replicationFactor := spec.ReplicationFactor
		if replicationFactor <= 0 {
			replicationFactor = 1
		}
		seen[topic] = true
		out = append(out, TopicProvisioningSpec{
			Topic:             topic,
			Partitions:        spec.Partitions,
			ReplicationFactor: replicationFactor,
		})
	}
	return out, nil
}

func createKafkaTopics(ctx context.Context, requestor kmsg.Requestor, specs []TopicProvisioningSpec, timeout time.Duration) error {
	req := kmsg.NewPtrCreateTopicsRequest()
	req.TimeoutMillis = topicProvisioningTimeoutMillis(timeout)
	for _, spec := range specs {
		topic := kmsg.NewCreateTopicsRequestTopic()
		topic.Topic = spec.Topic
		topic.NumPartitions = spec.Partitions
		topic.ReplicationFactor = spec.ReplicationFactor
		req.Topics = append(req.Topics, topic)
	}
	resp, err := req.RequestWith(ctx, requestor)
	if err != nil {
		return fmt.Errorf("kafka topic provisioning: create topics: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("kafka topic provisioning: create topics returned no response")
	}
	seen := map[string]bool{}
	for _, topicResp := range resp.Topics {
		seen[topicResp.Topic] = true
		if err := kerr.ErrorForCode(topicResp.ErrorCode); err != nil && !errors.Is(err, kerr.TopicAlreadyExists) {
			return fmt.Errorf("kafka topic provisioning: create topic %q: %w%s", topicResp.Topic, err, kafkaErrorMessageSuffix(topicResp.ErrorMessage))
		}
	}
	for _, spec := range specs {
		if !seen[spec.Topic] {
			return fmt.Errorf("kafka topic provisioning: create topics response missing topic %q", spec.Topic)
		}
	}
	return nil
}

func waitForKafkaTopicMetadata(ctx context.Context, requestor kmsg.Requestor, specs []TopicProvisioningSpec) error {
	var lastErr error
	for {
		retry, err := verifyKafkaTopicMetadata(ctx, requestor, specs)
		if err == nil {
			return nil
		}
		if !retry {
			return err
		}
		lastErr = err
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("kafka topic provisioning: metadata did not converge before timeout: %w", lastErr)
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func verifyKafkaTopicMetadata(ctx context.Context, requestor kmsg.Requestor, specs []TopicProvisioningSpec) (bool, error) {
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	for _, spec := range specs {
		topic := kmsg.NewMetadataRequestTopic()
		topic.Topic = kmsg.StringPtr(spec.Topic)
		req.Topics = append(req.Topics, topic)
	}
	resp, err := req.RequestWith(ctx, requestor)
	if err != nil {
		return true, fmt.Errorf("metadata request: %w", err)
	}
	if resp == nil {
		return true, fmt.Errorf("metadata request returned no response")
	}
	if err := kerr.ErrorForCode(resp.ErrorCode); err != nil {
		return kerr.IsRetriable(err), fmt.Errorf("metadata response: %w", err)
	}

	specsByTopic := map[string]TopicProvisioningSpec{}
	for _, spec := range specs {
		specsByTopic[spec.Topic] = spec
	}
	seen := map[string]bool{}
	for _, topicResp := range resp.Topics {
		if topicResp.Topic == nil {
			continue
		}
		topic := *topicResp.Topic
		spec, ok := specsByTopic[topic]
		if !ok {
			continue
		}
		if err := kerr.ErrorForCode(topicResp.ErrorCode); err != nil {
			return kerr.IsRetriable(err), fmt.Errorf("metadata topic %q: %w", topic, err)
		}
		if int32(len(topicResp.Partitions)) < spec.Partitions {
			return false, fmt.Errorf("kafka topic provisioning: topic %q has %d partitions, requires at least %d", topic, len(topicResp.Partitions), spec.Partitions)
		}
		seen[topic] = true
	}
	for _, spec := range specs {
		if !seen[spec.Topic] {
			return true, fmt.Errorf("metadata response missing topic %q", spec.Topic)
		}
	}
	return false, nil
}

func effectiveTopicProvisioningTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return defaultTopicProvisioningTimeout
	}
	return timeout
}

func topicProvisioningTimeoutMillis(timeout time.Duration) int32 {
	timeout = effectiveTopicProvisioningTimeout(timeout)
	if timeout < time.Millisecond {
		return 1
	}
	millis := timeout.Milliseconds()
	if millis > int64(^uint32(0)>>1) {
		return int32(^uint32(0) >> 1)
	}
	return int32(millis)
}

func kafkaErrorMessageSuffix(message *string) string {
	if message == nil || strings.TrimSpace(*message) == "" {
		return ""
	}
	return ": " + strings.TrimSpace(*message)
}
