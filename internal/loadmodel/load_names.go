package loadmodel

import (
	"fmt"
	"sort"
	"strings"
)

const (
	CommandLogLoadPostgresSchemaPrefix    = "budgie_cmdlog_load"
	CommandLogLoadCommandNATSStreamPrefix = "BUDGIE_COMMAND_LOG_LOAD_"
	CommandLogLoadEventNATSStreamPrefix   = "BUDGIE_EVENT_LOG_LOAD_"
	CommandLogLoadKafkaCommandTopicPrefix = "budgie.commands.load."
	CommandLogLoadKafkaEventTopicPrefix   = "budgie.events.load."
)

func SelectCommandLogLoadKafkaTopics(topics []string, commandPrefix, eventPrefix string) ([]string, error) {
	commandPrefix = strings.TrimSpace(commandPrefix)
	eventPrefix = strings.TrimSpace(eventPrefix)
	if commandPrefix == "" || eventPrefix == "" {
		return nil, fmt.Errorf("Kafka load-topic cleanup prefixes must be non-empty")
	}
	if commandPrefix == eventPrefix {
		return nil, fmt.Errorf("Kafka load-topic cleanup prefixes must be distinct")
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(topics))
	for _, topic := range topics {
		topic = strings.TrimSpace(topic)
		if topic == "" || seen[topic] {
			continue
		}
		if strings.HasPrefix(topic, commandPrefix) || strings.HasPrefix(topic, eventPrefix) {
			seen[topic] = true
			out = append(out, topic)
		}
	}
	sort.Strings(out)
	return out, nil
}
