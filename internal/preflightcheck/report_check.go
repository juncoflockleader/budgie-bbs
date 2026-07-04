package preflightcheck

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/preflightmodel"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

func EvaluateReport(report preflightmodel.Report, expectedTargets []string, remoteStaging bool) ([]string, error) {
	reportTargets, err := runconfig.NormalizePreflightTargets(strings.Join(report.Config.Targets, ","), nil)
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

	if !slices.Equal(reportTargets, expectedTargets) {
		add("config.targets", fmt.Sprintf("got %s, want %s", strings.Join(reportTargets, ","), strings.Join(expectedTargets, ",")))
	}
	if !report.Passed {
		add("passed", "must be true")
	}
	violations = append(violations, runevidence.FormatReportEvidenceViolations("evidence.", runevidence.ValidateToolGitEvidence(
		report.Evidence, "budgie-internet-scale-preflight", "preflight report"))...)
	if strings.TrimSpace(report.Config.ID) == "" {
		add("config.id", "must be recorded")
	}
	addPositiveEvidence(add, "config.timeoutMs", report.Config.TimeoutMS)
	if report.StartedAt <= 0 || report.FinishedAt <= 0 || report.FinishedAt < report.StartedAt {
		add("timing", "startedAt and finishedAt must be positive and ordered")
	}
	if remoteStaging && !report.Config.RemoteStaging {
		add("config.remoteStaging", "must be true for remote staging evidence")
	}

	targetSet := runconfig.TargetSet(expectedTargets)
	if targetSet[runconfig.PreflightTargetPostgres] {
		evaluateRuntimeEndpointEvidence(add, "runtime.postgresEndpoint", report.Runtime.PostgresEndpoint, "must be recorded", remoteStaging)
	}
	if targetSet[runconfig.PreflightTargetNATS] {
		evaluateRuntimeEndpointEvidence(add, "runtime.natsEndpoint", report.Runtime.NATSEndpoint, "must be recorded", remoteStaging)
		addPositiveEvidence(add, "runtime.natsReplicas", report.Runtime.NATSReplicas)
	}
	if targetSet[runconfig.PreflightTargetKafka] {
		if len(report.Runtime.KafkaBrokers) == 0 {
			add("runtime.kafkaBrokers", "must be recorded")
		}
		for i, broker := range report.Runtime.KafkaBrokers {
			evaluateRuntimeEndpointEvidence(add, fmt.Sprintf("runtime.kafkaBrokers[%d]", i), broker, "must not be empty", remoteStaging)
		}
		addPositiveEvidence(add, "runtime.kafkaCommandPartitions", report.Runtime.KafkaCommandPartitions)
		addPositiveEvidence(add, "runtime.kafkaEventPartitions", report.Runtime.KafkaEventPartitions)
		addPositiveEvidence(add, "runtime.kafkaTopicReplicas", report.Runtime.KafkaTopicReplicas)
	}

	violations = append(violations, evaluateProbeEvidence(report, expectedTargets)...)
	sort.Strings(violations)
	return violations, nil
}

func addPositiveEvidence[T ~int | ~int16 | ~int32 | ~int64](add func(string, string), path string, value T) {
	if value <= 0 {
		add(path, "must be positive")
	}
}

func evaluateRuntimeEndpointEvidence(add func(string, string), path, endpoint, emptyMessage string, remoteStaging bool) {
	if strings.TrimSpace(endpoint) == "" {
		add(path, emptyMessage)
		return
	}
	if message := runevidence.SanitizedEndpointEvidenceViolation(endpoint); message != "" {
		add(path, message)
	}
	if remoteStaging && runevidence.EndpointHostIsLocal(endpoint) {
		add(path, "must be non-local for remote staging evidence")
	}
}

func evaluateProbeEvidence(report preflightmodel.Report, expectedTargets []string) []string {
	violations := []string{}
	add := func(path, message string) {
		violations = append(violations, path+": "+message)
	}
	probes := map[string]preflightmodel.Probe{}
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
	expectedResources := map[string][]string{}
	for _, spec := range ProbeSpecs(Config{ID: id}) {
		expectedResources[spec.Target] = spec.Resources
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
			if !slices.Contains(probe.Resources, resource) {
				add("probes."+target+".resources", "missing "+resource)
			}
		}
	}
	return violations
}
