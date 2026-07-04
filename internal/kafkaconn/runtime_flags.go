package kafkaconn

import "flag"

type RuntimeSecurityFlags struct {
	TLS           *bool
	TLSCAFile     *string
	TLSServerName *string
	SASLMechanism *string
	SASLUser      *string
	SASLPassword  *string
}

func RegisterRuntimeSecurityFlags(flags *flag.FlagSet) RuntimeSecurityFlags {
	defaults := RuntimeSecurityConfigFromEnv()
	return RuntimeSecurityFlags{
		TLS:           flags.Bool("kafka-tls", defaults.TLS, "Enable TLS for Kafka/Redpanda connections (also read from BUDGIE_KAFKA_TLS)"),
		TLSCAFile:     flags.String("kafka-tls-ca-file", defaults.TLSCAFile, "Optional PEM CA bundle for Kafka/Redpanda TLS (also read from BUDGIE_KAFKA_TLS_CA_FILE)"),
		TLSServerName: flags.String("kafka-tls-server-name", defaults.TLSServerName, "Optional TLS server name override for Kafka/Redpanda (also read from BUDGIE_KAFKA_TLS_SERVER_NAME)"),
		SASLMechanism: flags.String("kafka-sasl-mechanism", defaults.SASLMechanism, "Kafka/Redpanda SASL mechanism: plain, scram-sha-256, or scram-sha-512 (also read from BUDGIE_KAFKA_SASL_MECHANISM)"),
		SASLUser:      flags.String("kafka-sasl-user", defaults.SASLUser, "Kafka/Redpanda SASL user (also read from BUDGIE_KAFKA_SASL_USER)"),
		SASLPassword:  flags.String("kafka-sasl-password", defaults.SASLPassword, "Kafka/Redpanda SASL password (also read from BUDGIE_KAFKA_SASL_PASSWORD)"),
	}
}

func (f RuntimeSecurityFlags) Config() RuntimeSecurityConfig {
	return RuntimeSecurityConfig{
		TLS:           *f.TLS,
		TLSCAFile:     *f.TLSCAFile,
		TLSServerName: *f.TLSServerName,
		SASLMechanism: *f.SASLMechanism,
		SASLUser:      *f.SASLUser,
		SASLPassword:  *f.SASLPassword,
	}
}
