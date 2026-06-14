package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
)

const (
	targetPostgres = "postgres"
	targetNATS     = "nats"
	targetKafka    = "kafka"
)

type preflightReport struct {
	Config     preflightReportConfig  `json:"config"`
	Runtime    preflightReportRuntime `json:"runtime"`
	Evidence   preflightEvidence      `json:"evidence"`
	StartedAt  int64                  `json:"startedAt"`
	FinishedAt int64                  `json:"finishedAt"`
	Passed     bool                   `json:"passed"`
	Probes     []preflightProbeReport `json:"probes"`
}

type preflightReportConfig struct {
	Targets       []string `json:"targets"`
	RemoteStaging bool     `json:"remoteStaging"`
	ID            string   `json:"id"`
	TimeoutMS     int64    `json:"timeoutMs"`
}

type preflightReportRuntime struct {
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

type preflightEvidence struct {
	Tool        string `json:"tool"`
	GitRevision string `json:"gitRevision,omitempty"`
	GitModified bool   `json:"gitModified"`
}

type preflightProbeReport struct {
	Target     string   `json:"target"`
	Name       string   `json:"name"`
	Resources  []string `json:"resources,omitempty"`
	StartedAt  int64    `json:"startedAt"`
	FinishedAt int64    `json:"finishedAt"`
	Passed     bool     `json:"passed"`
	Error      string   `json:"error,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budgie-internet-scale-preflight-report-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	reportFile := flags.String("report-file", "", "Path to a budgie-internet-scale-preflight JSON report; use - for stdin")
	targets := flags.String("targets", "", "Expected preflight targets: postgres,nats,kafka,all; nats/kafka imply postgres")
	remoteStaging := flags.Bool("remote-staging", false, "Require non-local endpoint evidence and remote staging mode")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	report, err := readPreflightReport(*reportFile, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "read preflight report: %v\n", err)
		return 2
	}
	expectedTargets, err := normalizeExpectedTargets(*targets)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	violations, err := evaluatePreflightReport(report, expectedTargets, *remoteStaging)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "preflight report violations: %s\n", strings.Join(violations, "; "))
		return 3
	}
	fmt.Fprintln(stdout, "internet-scale preflight report satisfies staging evidence")
	return 0
}

func readPreflightReport(path string, stdin io.Reader) (preflightReport, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return preflightReport{}, fmt.Errorf("-report-file is required")
	}
	var (
		data []byte
		err  error
	)
	if path == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return preflightReport{}, err
	}
	var report preflightReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return preflightReport{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return preflightReport{}, fmt.Errorf("unexpected trailing JSON")
	}
	return report, nil
}

func evaluatePreflightReport(report preflightReport, expectedTargets []string, remoteStaging bool) ([]string, error) {
	reportTargets, err := normalizeExpectedTargets(strings.Join(report.Config.Targets, ","))
	if err != nil {
		return nil, fmt.Errorf("report config targets: %w", err)
	}
	if len(expectedTargets) == 0 {
		expectedTargets = reportTargets
	}
	violations := []string{}
	add := func(path, message string) {
		violations = append(violations, path+": "+message)
	}

	if !equalTargets(reportTargets, expectedTargets) {
		add("config.targets", fmt.Sprintf("got %s, want %s", strings.Join(reportTargets, ","), strings.Join(expectedTargets, ",")))
	}
	if !report.Passed {
		add("passed", "must be true")
	}
	if strings.TrimSpace(report.Evidence.Tool) != "budgie-internet-scale-preflight" {
		add("evidence.tool", `must be "budgie-internet-scale-preflight"`)
	}
	if strings.TrimSpace(report.Evidence.GitRevision) == "" {
		add("evidence.gitRevision", "must be recorded")
	}
	if report.Evidence.GitModified {
		add("evidence.gitModified", "must be false for archived staging evidence")
	}
	if strings.TrimSpace(report.Config.ID) == "" {
		add("config.id", "must be recorded")
	}
	if report.Config.TimeoutMS <= 0 {
		add("config.timeoutMs", "must be positive")
	}
	if report.StartedAt <= 0 || report.FinishedAt <= 0 || report.FinishedAt < report.StartedAt {
		add("timing", "startedAt and finishedAt must be positive and ordered")
	}
	if remoteStaging && !report.Config.RemoteStaging {
		add("config.remoteStaging", "must be true for remote staging evidence")
	}

	targetSet := targetSet(expectedTargets)
	if targetSet[targetPostgres] {
		if strings.TrimSpace(report.Runtime.PostgresEndpoint) == "" {
			add("runtime.postgresEndpoint", "must be recorded")
		} else {
			if message := endpointEvidenceViolation(report.Runtime.PostgresEndpoint); message != "" {
				add("runtime.postgresEndpoint", message)
			}
			if remoteStaging && endpointHostIsLocal(report.Runtime.PostgresEndpoint) {
				add("runtime.postgresEndpoint", "must be non-local for remote staging evidence")
			}
		}
	}
	if targetSet[targetNATS] {
		if strings.TrimSpace(report.Runtime.NATSEndpoint) == "" {
			add("runtime.natsEndpoint", "must be recorded")
		} else {
			if message := endpointEvidenceViolation(report.Runtime.NATSEndpoint); message != "" {
				add("runtime.natsEndpoint", message)
			}
			if remoteStaging && endpointHostIsLocal(report.Runtime.NATSEndpoint) {
				add("runtime.natsEndpoint", "must be non-local for remote staging evidence")
			}
		}
		if report.Runtime.NATSReplicas <= 0 {
			add("runtime.natsReplicas", "must be positive")
		}
	}
	if targetSet[targetKafka] {
		if len(report.Runtime.KafkaBrokers) == 0 {
			add("runtime.kafkaBrokers", "must be recorded")
		}
		for i, broker := range report.Runtime.KafkaBrokers {
			if strings.TrimSpace(broker) == "" {
				add(fmt.Sprintf("runtime.kafkaBrokers[%d]", i), "must not be empty")
			} else {
				if message := endpointEvidenceViolation(broker); message != "" {
					add(fmt.Sprintf("runtime.kafkaBrokers[%d]", i), message)
				}
				if remoteStaging && endpointHostIsLocal(broker) {
					add(fmt.Sprintf("runtime.kafkaBrokers[%d]", i), "must be non-local for remote staging evidence")
				}
			}
		}
		if report.Runtime.KafkaCommandPartitions <= 0 {
			add("runtime.kafkaCommandPartitions", "must be positive")
		}
		if report.Runtime.KafkaEventPartitions <= 0 {
			add("runtime.kafkaEventPartitions", "must be positive")
		}
		if report.Runtime.KafkaTopicReplicas <= 0 {
			add("runtime.kafkaTopicReplicas", "must be positive")
		}
	}

	violations = append(violations, evaluateProbeEvidence(report, expectedTargets)...)
	sort.Strings(violations)
	return violations, nil
}

func endpointEvidenceViolation(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "must be URL-shaped sanitized endpoint evidence with scheme and host"
	}
	if parsed.User != nil {
		return "must not include userinfo or credentials"
	}
	if strings.TrimSpace(parsed.RawQuery) != "" {
		return "must not include query parameters"
	}
	if strings.TrimSpace(parsed.Fragment) != "" {
		return "must not include URL fragments"
	}
	return ""
}

func evaluateProbeEvidence(report preflightReport, expectedTargets []string) []string {
	violations := []string{}
	add := func(path, message string) {
		violations = append(violations, path+": "+message)
	}
	probes := map[string]preflightProbeReport{}
	for i, probe := range report.Probes {
		target := strings.TrimSpace(probe.Target)
		if target == "" {
			add(fmt.Sprintf("probes[%d].target", i), "must be recorded")
			continue
		}
		if _, exists := probes[target]; exists {
			add(fmt.Sprintf("probes[%d].target", i), "duplicate probe target "+target)
			continue
		}
		probes[target] = probe
	}
	if len(probes) != len(expectedTargets) {
		add("probes", fmt.Sprintf("got %d probe targets, want %d", len(probes), len(expectedTargets)))
	}

	id := strings.TrimSpace(report.Config.ID)
	expectedResources := map[string][]string{
		targetPostgres: {"budgie_cmdlog_load_preflight_" + id},
		targetNATS: {
			"BUDGIE_COMMAND_LOG_LOAD_PREFLIGHT_" + strings.ToUpper(id),
			"BUDGIE_EVENT_LOG_LOAD_PREFLIGHT_" + strings.ToUpper(id),
		},
		targetKafka: {
			"budgie.commands.load.preflight." + id,
			"budgie.events.load.preflight." + id,
		},
	}
	for _, target := range expectedTargets {
		probe, ok := probes[target]
		if !ok {
			add("probes."+target, "missing probe")
			continue
		}
		if !probe.Passed {
			add("probes."+target+".passed", "must be true")
		}
		if strings.TrimSpace(probe.Error) != "" {
			add("probes."+target+".error", "must be empty")
		}
		if strings.TrimSpace(probe.Name) == "" {
			add("probes."+target+".name", "must be recorded")
		}
		if probe.StartedAt <= 0 || probe.FinishedAt <= 0 || probe.FinishedAt < probe.StartedAt {
			add("probes."+target+".timing", "startedAt and finishedAt must be positive and ordered")
		}
		for _, resource := range expectedResources[target] {
			if !containsString(probe.Resources, resource) {
				add("probes."+target+".resources", "missing "+resource)
			}
		}
	}
	return violations
}

func normalizeExpectedTargets(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	fields := strings.Fields(strings.ReplaceAll(raw, ",", " "))
	out := []string{}
	for _, field := range fields {
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "all":
			out = append(out, targetPostgres, targetNATS, targetKafka)
		case targetPostgres, targetNATS, targetKafka:
			out = append(out, strings.ToLower(strings.TrimSpace(field)))
		case "":
		default:
			return nil, fmt.Errorf("unsupported preflight report target %q; supported targets: postgres,nats,kafka,all", field)
		}
	}
	if containsString(out, targetNATS) || containsString(out, targetKafka) {
		out = append([]string{targetPostgres}, out...)
	}
	return dedupeTargets(out), nil
}

func dedupeTargets(in []string) []string {
	order := map[string]int{targetPostgres: 0, targetNATS: 1, targetKafka: 2}
	seen := map[string]bool{}
	out := []string{}
	for _, target := range in {
		target = strings.ToLower(strings.TrimSpace(target))
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i]] < order[out[j]]
	})
	return out
}

func equalTargets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func targetSet(targets []string) map[string]bool {
	out := map[string]bool{}
	for _, target := range targets {
		out[target] = true
	}
	return out
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func endpointHostIsLocal(endpoint string) bool {
	host := endpointHost(endpoint)
	switch host {
	case "", "localhost", "localhost.localdomain", "::1", "[::1]":
		return host != ""
	}
	if strings.HasPrefix(host, "localhost.") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func endpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" {
		return strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	}
	fields := dsnFields(endpoint)
	host := strings.TrimSpace(fields["host"])
	if host != "" {
		return strings.ToLower(strings.Trim(host, "[]"))
	}
	if host, _, err := net.SplitHostPort(endpoint); err == nil {
		return strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	}
	if strings.Count(endpoint, ":") == 1 {
		before, _, _ := strings.Cut(endpoint, ":")
		return strings.ToLower(strings.TrimSpace(before))
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(endpoint), "[]"))
}

func dsnFields(raw string) map[string]string {
	fields := map[string]string{}
	for _, field := range strings.Fields(raw) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		fields[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), "'\"")
	}
	return fields
}
