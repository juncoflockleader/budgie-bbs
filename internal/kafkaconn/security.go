package kafkaconn

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

func RuntimeClientOpts(runtime RuntimeConfig, clientID string) ([]kgo.Opt, error) {
	runtime = runtime.Normalize()
	clientID = strings.TrimSpace(clientID)
	if len(runtime.Brokers) == 0 {
		return nil, fmt.Errorf("kafka runtime config: broker list is required")
	}
	if clientID == "" {
		return nil, fmt.Errorf("kafka runtime client: client id is required")
	}
	if err := runtime.ValidateSecurity(); err != nil {
		return nil, err
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(runtime.Brokers...),
		kgo.ClientID(clientID),
	}
	if lvl := kafkaDebugLogLevel(); lvl != kgo.LogLevelNone {
		opts = append(opts, kgo.WithLogger(kgo.BasicLogger(os.Stderr, lvl, nil)))
	}
	securityOpts, err := runtime.SecurityOpts()
	if err != nil {
		return nil, err
	}
	opts = append(opts, securityOpts...)
	return opts, nil
}

// kafkaDebugLogLevel returns the franz-go client log level from
// BUDGIE_KAFKA_LOG_LEVEL (debug|info|warn|error|none). It defaults to none so
// production clients stay silent; it is a diagnostic aid for load/gate runs.
func kafkaDebugLogLevel() kgo.LogLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("BUDGIE_KAFKA_LOG_LEVEL"))) {
	case "debug":
		return kgo.LogLevelDebug
	case "info":
		return kgo.LogLevelInfo
	case "warn", "warning":
		return kgo.LogLevelWarn
	case "error":
		return kgo.LogLevelError
	default:
		return kgo.LogLevelNone
	}
}

func (c RuntimeConfig) SecurityOpts() ([]kgo.Opt, error) {
	c = c.Normalize()
	if err := c.ValidateSecurity(); err != nil {
		return nil, err
	}
	opts := []kgo.Opt{}
	if c.TLS {
		tlsConfig, err := c.TLSConfig()
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}
	if c.SASLMechanism != "" {
		switch c.SASLMechanism {
		case "plain":
			opts = append(opts, kgo.SASL(plain.Auth{
				User: c.SASLUser,
				Pass: c.SASLPassword,
			}.AsMechanism()))
		case "scram-sha-256":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: c.SASLUser,
				Pass: c.SASLPassword,
			}.AsSha256Mechanism()))
		case "scram-sha-512":
			opts = append(opts, kgo.SASL(scram.Auth{
				User: c.SASLUser,
				Pass: c.SASLPassword,
			}.AsSha512Mechanism()))
		default:
			return nil, fmt.Errorf("kafka runtime config: unsupported SASL mechanism %q; supported: plain,scram-sha-256,scram-sha-512", c.SASLMechanism)
		}
	}
	return opts, nil
}

func (c RuntimeConfig) TLSConfig() (*tls.Config, error) {
	c = c.Normalize()
	config := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: c.TLSServerName,
	}
	if c.TLSCAFile == "" {
		return config, nil
	}
	caPEM, err := os.ReadFile(c.TLSCAFile)
	if err != nil {
		return nil, fmt.Errorf("kafka runtime config: read TLS CA file: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if pool == nil {
		pool = x509.NewCertPool()
	}
	if ok := pool.AppendCertsFromPEM(caPEM); !ok {
		return nil, fmt.Errorf("kafka runtime config: TLS CA file %q did not contain any PEM certificates", c.TLSCAFile)
	}
	config.RootCAs = pool
	return config, nil
}
