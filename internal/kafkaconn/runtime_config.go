package kafkaconn

import (
	"fmt"
	"os"
	"strings"
)

type RuntimeConfig struct {
	Brokers       []string
	CommandTopic  string
	EventTopic    string
	ConsumerGroup string
	TLS           bool
	TLSCAFile     string
	TLSServerName string
	SASLMechanism string
	SASLUser      string
	SASLPassword  string
}

type RuntimeSecurityConfig struct {
	TLS           bool
	TLSCAFile     string
	TLSServerName string
	SASLMechanism string
	SASLUser      string
	SASLPassword  string
}

func RuntimeConfigFromFlags(brokers, commandTopic, eventTopic, consumerGroup string) RuntimeConfig {
	return RuntimeConfigFromOptions(brokers, commandTopic, eventTopic, consumerGroup, RuntimeSecurityConfig{})
}

func RuntimeConfigFromOptions(brokers, commandTopic, eventTopic, consumerGroup string, security RuntimeSecurityConfig) RuntimeConfig {
	return RuntimeConfig{
		Brokers:       splitKafkaBrokers(brokers),
		CommandTopic:  commandTopic,
		EventTopic:    eventTopic,
		ConsumerGroup: consumerGroup,
		TLS:           security.TLS,
		TLSCAFile:     security.TLSCAFile,
		TLSServerName: security.TLSServerName,
		SASLMechanism: security.SASLMechanism,
		SASLUser:      security.SASLUser,
		SASLPassword:  security.SASLPassword,
	}.Normalize()
}

func RuntimeSecurityConfigFromEnv() RuntimeSecurityConfig {
	return RuntimeSecurityConfig{
		TLS:           boolEnv("BUDGIE_KAFKA_TLS"),
		TLSCAFile:     os.Getenv("BUDGIE_KAFKA_TLS_CA_FILE"),
		TLSServerName: os.Getenv("BUDGIE_KAFKA_TLS_SERVER_NAME"),
		SASLMechanism: os.Getenv("BUDGIE_KAFKA_SASL_MECHANISM"),
		SASLUser:      os.Getenv("BUDGIE_KAFKA_SASL_USER"),
		SASLPassword:  os.Getenv("BUDGIE_KAFKA_SASL_PASSWORD"),
	}
}

func (c RuntimeConfig) Normalize() RuntimeConfig {
	c.Brokers = normalizeKafkaBrokers(c.Brokers)
	c.CommandTopic = strings.TrimSpace(c.CommandTopic)
	if c.CommandTopic == "" {
		c.CommandTopic = DefaultCommandTopic
	}
	c.EventTopic = strings.TrimSpace(c.EventTopic)
	if c.EventTopic == "" {
		c.EventTopic = DefaultEventTopic
	}
	c.ConsumerGroup = strings.TrimSpace(c.ConsumerGroup)
	if c.ConsumerGroup == "" {
		c.ConsumerGroup = DefaultWriterConsumerGroup
	}
	c.TLSCAFile = strings.TrimSpace(c.TLSCAFile)
	c.TLSServerName = strings.TrimSpace(c.TLSServerName)
	c.SASLMechanism = normalizeKafkaSASLMechanism(c.SASLMechanism)
	c.SASLUser = strings.TrimSpace(c.SASLUser)
	if c.TLSCAFile != "" || c.TLSServerName != "" {
		c.TLS = true
	}
	return c
}

func (c RuntimeConfig) ValidateCommandLog() error {
	c = c.Normalize()
	if len(c.Brokers) == 0 {
		return fmt.Errorf("kafka runtime config: broker list is required")
	}
	if strings.TrimSpace(c.CommandTopic) == "" {
		return fmt.Errorf("kafka runtime config: command topic is required")
	}
	return c.ValidateSecurity()
}

func (c RuntimeConfig) ValidateEventLog() error {
	c = c.Normalize()
	if len(c.Brokers) == 0 {
		return fmt.Errorf("kafka runtime config: broker list is required")
	}
	if strings.TrimSpace(c.EventTopic) == "" {
		return fmt.Errorf("kafka runtime config: event topic is required")
	}
	return c.ValidateSecurity()
}

func (c RuntimeConfig) ValidateCommandEventTransaction() error {
	c = c.Normalize()
	if err := c.ValidateCommandLog(); err != nil {
		return err
	}
	if err := c.ValidateEventLog(); err != nil {
		return err
	}
	if c.CommandTopic == c.EventTopic {
		return fmt.Errorf("kafka runtime config: command and event topics must be distinct")
	}
	if strings.TrimSpace(c.ConsumerGroup) == "" {
		return fmt.Errorf("kafka runtime config: consumer group is required")
	}
	if err := c.ValidateSecurity(); err != nil {
		return err
	}
	return nil
}

func (c RuntimeConfig) ValidateSecurity() error {
	c = c.Normalize()
	if c.SASLMechanism == "" {
		if c.SASLUser != "" || c.SASLPassword != "" {
			return fmt.Errorf("kafka runtime config: SASL mechanism is required when SASL user or password is set")
		}
		return nil
	}
	switch c.SASLMechanism {
	case "plain", "scram-sha-256", "scram-sha-512":
	default:
		return fmt.Errorf("kafka runtime config: unsupported SASL mechanism %q; supported: plain,scram-sha-256,scram-sha-512", c.SASLMechanism)
	}
	if c.SASLUser == "" {
		return fmt.Errorf("kafka runtime config: SASL user is required for mechanism %s", c.SASLMechanism)
	}
	if c.SASLPassword == "" {
		return fmt.Errorf("kafka runtime config: SASL password is required for mechanism %s", c.SASLMechanism)
	}
	return nil
}

func splitKafkaBrokers(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, part)
	}
	return out
}

func normalizeKafkaBrokers(brokers []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(brokers))
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" || seen[broker] {
			continue
		}
		seen[broker] = true
		out = append(out, broker)
	}
	return out
}

func normalizeKafkaSASLMechanism(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "off", "none":
		return ""
	case "plain":
		return "plain"
	case "scram-sha-256", "scram_sha_256", "scram-sha256", "scram256", "sha256":
		return "scram-sha-256"
	case "scram-sha-512", "scram_sha_512", "scram-sha512", "scram512", "sha512":
		return "scram-sha-512"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func boolEnv(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}
