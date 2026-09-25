package kafkaconn

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kmsg"
)

// TopicListOptions configures Kafka topic listing.
type TopicListOptions struct {
	Runtime  RuntimeConfig
	ClientID string
	Timeout  time.Duration
}

// TopicDeletionOptions configures Kafka topic deletion.
type TopicDeletionOptions struct {
	Runtime       RuntimeConfig
	ClientID      string
	Topics        []string
	Timeout       time.Duration
	IgnoreMissing bool
}

// ListTopics returns broker topic names without enabling topic auto-creation.
func ListTopics(ctx context.Context, options TopicListOptions) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client, err := openTopicAdminClient("listing", options.Runtime, options.ClientID)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return listTopicsWithRequestor(ctx, client, options.Timeout)
}

// DeleteTopics deletes broker topics by name.
func DeleteTopics(ctx context.Context, options TopicDeletionOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	client, err := openTopicAdminClient("deletion", options.Runtime, options.ClientID)
	if err != nil {
		return err
	}
	defer client.Close()
	return deleteTopicsWithRequestor(ctx, client, options.Topics, options.Timeout, options.IgnoreMissing)
}

func listTopicsWithRequestor(ctx context.Context, requestor kmsg.Requestor, timeout time.Duration) ([]string, error) {
	if requestor == nil {
		return nil, fmt.Errorf("kafka topic listing: requestor is required")
	}
	ctx, cancel := context.WithTimeout(ctx, effectiveTopicProvisioningTimeout(timeout))
	defer cancel()
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	resp, err := req.RequestWith(ctx, requestor)
	if err != nil {
		return nil, fmt.Errorf("kafka topic listing: metadata request: %w", err)
	}
	if resp == nil {
		return nil, fmt.Errorf("kafka topic listing: metadata request returned no response")
	}
	if err := kerr.ErrorForCode(resp.ErrorCode); err != nil {
		return nil, fmt.Errorf("kafka topic listing: metadata response: %w", err)
	}
	topics := make([]string, 0, len(resp.Topics))
	for _, topicResp := range resp.Topics {
		if topicResp.Topic == nil || strings.TrimSpace(*topicResp.Topic) == "" {
			continue
		}
		if err := kerr.ErrorForCode(topicResp.ErrorCode); err != nil {
			return nil, fmt.Errorf("kafka topic listing: metadata topic %q: %w", *topicResp.Topic, err)
		}
		topics = append(topics, strings.TrimSpace(*topicResp.Topic))
	}
	sort.Strings(topics)
	return topics, nil
}

func deleteTopicsWithRequestor(ctx context.Context, requestor kmsg.Requestor, topics []string, timeout time.Duration, ignoreMissing bool) error {
	if requestor == nil {
		return fmt.Errorf("kafka topic deletion: requestor is required")
	}
	topics, err := normalizeKafkaTopicNames(topics)
	if err != nil {
		return err
	}
	if len(topics) == 0 {
		return nil
	}
	timeout = effectiveTopicProvisioningTimeout(timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req := kmsg.NewPtrDeleteTopicsRequest()
	req.TimeoutMillis = topicProvisioningTimeoutMillis(timeout)
	req.TopicNames = append(req.TopicNames, topics...)
	for _, topicName := range topics {
		topic := kmsg.NewDeleteTopicsRequestTopic()
		topic.Topic = kmsg.StringPtr(topicName)
		req.Topics = append(req.Topics, topic)
	}
	resp, err := req.RequestWith(ctx, requestor)
	if err != nil {
		return fmt.Errorf("kafka topic deletion: delete topics: %w", err)
	}
	if resp == nil {
		return fmt.Errorf("kafka topic deletion: delete topics returned no response")
	}
	seen := map[string]bool{}
	for i, topicResp := range resp.Topics {
		topicName := deleteTopicResponseName(topicResp, topics, i)
		seen[topicName] = true
		if err := kerr.ErrorForCode(topicResp.ErrorCode); err != nil {
			if ignoreMissing && (errors.Is(err, kerr.UnknownTopicOrPartition) || errors.Is(err, kerr.UnknownTopicID)) {
				continue
			}
			return fmt.Errorf("kafka topic deletion: delete topic %q: %w%s", topicName, err, kafkaErrorMessageSuffix(topicResp.ErrorMessage))
		}
	}
	for _, topicName := range topics {
		if !seen[topicName] {
			return fmt.Errorf("kafka topic deletion: delete topics response missing topic %q", topicName)
		}
	}
	return nil
}

func normalizeKafkaTopicNames(topics []string) ([]string, error) {
	out := make([]string, 0, len(topics))
	seen := map[string]bool{}
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" {
			return nil, fmt.Errorf("kafka topic deletion: topic name is required")
		}
		if seen[topic] {
			continue
		}
		seen[topic] = true
		out = append(out, topic)
	}
	sort.Strings(out)
	return out, nil
}

func deleteTopicResponseName(topic kmsg.DeleteTopicsResponseTopic, requested []string, index int) string {
	if topic.Topic != nil && strings.TrimSpace(*topic.Topic) != "" {
		return strings.TrimSpace(*topic.Topic)
	}
	if index >= 0 && index < len(requested) {
		return requested[index]
	}
	return "<unknown>"
}
