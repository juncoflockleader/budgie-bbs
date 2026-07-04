package preflightmodel

import "github.com/juncoflockleader/budgie-bbs/internal/runevidence"

type Report struct {
	Config     Config               `json:"config"`
	Runtime    Runtime              `json:"runtime"`
	Evidence   runevidence.Evidence `json:"evidence"`
	StartedAt  int64                `json:"startedAt"`
	FinishedAt int64                `json:"finishedAt"`
	Passed     bool                 `json:"passed"`
	Probes     []Probe              `json:"probes"`
}

type Config struct {
	Targets       []string `json:"targets"`
	RemoteStaging bool     `json:"remoteStaging"`
	ID            string   `json:"id"`
	TimeoutMS     int64    `json:"timeoutMs"`
}

type Runtime struct {
	PostgresEndpoint       string   `json:"postgresEndpoint,omitempty"`
	NATSEndpoint           string   `json:"natsEndpoint,omitempty"`
	NATSReplicas           int      `json:"natsReplicas,omitempty"`
	KafkaBrokers           []string `json:"kafkaBrokers,omitempty"`
	KafkaTLS               bool     `json:"kafkaTls,omitempty"`
	KafkaSASLMechanism     string   `json:"kafkaSaslMechanism,omitempty"`
	KafkaCommandPartitions int32    `json:"kafkaCommandPartitions,omitempty"`
	KafkaEventPartitions   int32    `json:"kafkaEventPartitions,omitempty"`
	KafkaTopicReplicas     int16    `json:"kafkaTopicReplicas,omitempty"`
}

type Probe struct {
	Target     string   `json:"target"`
	Name       string   `json:"name"`
	Resources  []string `json:"resources,omitempty"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt"`
	Passed     bool     `json:"passed"`
	Error      string   `json:"error,omitempty"`
}
