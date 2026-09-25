package runbundle

import (
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
)

const ManifestTool = "budgie-internet-scale-bundle-report-check"

type ReportSpec struct {
	Label string
	Path  string
	Tool  string
}

type reportDefinition struct {
	Label   string
	Target  string
	Tool    string
	Durable bool
}

type ManifestOptions struct {
	Targets       []string
	RemoteStaging bool
}

type ManifestVerifyOptions struct {
	ExpectedTargets       []string
	RequireTargets        bool
	ExpectedRemoteStaging bool
	RequireRemoteStaging  bool
}

type Manifest struct {
	Tool          string           `json:"tool"`
	GeneratedAt   int64            `json:"generatedAt"`
	GitRevision   string           `json:"gitRevision"`
	Targets       []string         `json:"targets,omitempty"`
	RemoteStaging bool             `json:"remoteStaging"`
	Reports       []ManifestReport `json:"reports"`
}

type ManifestReport struct {
	Label       string `json:"label"`
	Path        string `json:"path"`
	Tool        string `json:"tool"`
	GitRevision string `json:"gitRevision"`
	GitModified bool   `json:"gitModified"`
	SHA256      string `json:"sha256"`
}

var reportDefinitions = []reportDefinition{
	{Label: "preflight", Tool: "budgie-internet-scale-preflight"},
	{Label: "gateway", Target: "gateway", Tool: "budgie-gateway-loadgen"},
	{Label: "nats", Target: "nats", Tool: "budgie-commandlog-loadgen", Durable: true},
	{Label: "kafka", Target: "kafka", Tool: "budgie-commandlog-loadgen", Durable: true},
}

func SelectedReportSpecs(preflightReport, gatewayReport, natsReport, kafkaReport string) []ReportSpec {
	paths := map[string]string{
		"preflight": preflightReport,
		"gateway":   gatewayReport,
		"nats":      natsReport,
		"kafka":     kafkaReport,
	}
	out := []ReportSpec{}
	for _, definition := range reportDefinitions {
		if path := strings.TrimSpace(paths[definition.Label]); path != "" {
			out = append(out, ReportSpec{Label: definition.Label, Path: path, Tool: definition.Tool})
		}
	}
	return out
}

func EvaluateReports(specs []ReportSpec) []string {
	violations := []string{}
	var wantRevision string
	for _, spec := range specs {
		evidence, err := runevidence.ReadReportEvidence(spec.Path)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s.report: read %s: %v", spec.Label, spec.Path, err))
			continue
		}
		violations = append(violations, runevidence.FormatReportEvidenceViolations(spec.Label+".evidence.",
			runevidence.ValidateToolGitEvidence(evidence, spec.Tool, spec.Label+" report"))...)
		revision := strings.TrimSpace(evidence.GitRevision)
		if revision == "" {
			continue
		}
		if wantRevision == "" {
			wantRevision = revision
		} else if revision != wantRevision {
			violations = append(violations, fmt.Sprintf("%s.evidence.gitRevision: got %q, want %q", spec.Label, revision, wantRevision))
		}
	}
	return violations
}

func WriteManifest(path string, specs []ReportSpec, options ManifestOptions) error {
	manifest := Manifest{
		// Keep the pre-consolidation tool identifier so manifests archived
		// before the budgie-report-check merge still verify unchanged.
		Tool:          ManifestTool,
		GeneratedAt:   time.Now().UnixMilli(),
		Targets:       append([]string(nil), options.Targets...),
		RemoteStaging: options.RemoteStaging,
	}
	for _, spec := range specs {
		report, err := readManifestReport(spec.Path)
		if err != nil {
			if report.Read {
				return fmt.Errorf("%s: %w", spec.Path, err)
			}
			return err
		}
		if manifest.GitRevision == "" {
			manifest.GitRevision = strings.TrimSpace(report.Evidence.GitRevision)
		}
		manifest.Reports = append(manifest.Reports, ManifestReport{
			Label:       spec.Label,
			Path:        spec.Path,
			Tool:        report.Evidence.Tool,
			GitRevision: report.Evidence.GitRevision,
			GitModified: report.Evidence.GitModified,
			SHA256:      report.SHA256,
		})
	}
	return runreport.WriteJSONFile(path, manifest, true)
}

func VerifyManifest(path string, options ManifestVerifyOptions) ([]string, error) {
	manifest, err := runreport.ReadJSONFile[Manifest](path, false)
	if err != nil {
		return nil, err
	}
	violations := []string{}
	add := func(field, message string) {
		violations = append(violations, field+": "+message)
	}
	if manifest.Tool != ManifestTool {
		add("tool", `must be "budgie-internet-scale-bundle-report-check"`)
	}
	if manifest.GeneratedAt <= 0 {
		add("generatedAt", "must be positive")
	}
	if strings.TrimSpace(manifest.GitRevision) == "" {
		add("gitRevision", "must be recorded")
	}
	if options.RequireTargets && !slices.Equal(manifest.Targets, options.ExpectedTargets) {
		add("targets", fmt.Sprintf("got %s, want %s", strings.Join(manifest.Targets, ","), strings.Join(options.ExpectedTargets, ",")))
	}
	if options.RequireRemoteStaging && manifest.RemoteStaging != options.ExpectedRemoteStaging {
		add("remoteStaging", fmt.Sprintf("got %v, want %v", manifest.RemoteStaging, options.ExpectedRemoteStaging))
	}
	if len(manifest.Reports) == 0 {
		add("reports", "must not be empty")
	}
	seenLabels := map[string]bool{}
	for i, report := range manifest.Reports {
		label := strings.TrimSpace(report.Label)
		path := strings.TrimSpace(report.Path)
		field := fmt.Sprintf("reports[%d]", i)
		if label == "" {
			add(field+".label", "must be recorded")
		} else if seenLabels[label] {
			add(field+".label", "duplicate label "+label)
		} else {
			seenLabels[label] = true
		}
		definition, supportedLabel := reportDefinitionForLabel(label)
		if !supportedLabel {
			add(field+".label", "unsupported label "+label)
			if strings.TrimSpace(report.GitRevision) == "" {
				add(field+".gitRevision", "must be recorded")
			}
			if report.GitModified {
				add(field+".gitModified", "must be false")
			}
		} else {
			violations = append(violations, runevidence.FormatReportEvidenceViolations(field+".", runevidence.ValidateToolGitEvidence(runevidence.Evidence{
				Tool:        report.Tool,
				GitRevision: report.GitRevision,
				GitModified: report.GitModified,
			}, definition.Tool, label+" manifest report"))...)
		}
		if path == "" {
			add(field+".path", "must be recorded")
			continue
		}
		reportData, err := readManifestReport(path)
		if err != nil {
			if reportData.Read {
				add(field+".evidence", err.Error())
			} else {
				add(field+".path", "read "+path+": "+err.Error())
			}
			continue
		}
		if strings.TrimSpace(report.SHA256) == "" {
			add(field+".sha256", "must be recorded")
		} else if !strings.EqualFold(report.SHA256, reportData.SHA256) {
			add(field+".sha256", fmt.Sprintf("got %s, want %s", report.SHA256, reportData.SHA256))
		}
		if reportData.Evidence.Tool != report.Tool {
			add(field+".tool", fmt.Sprintf("manifest tool %q does not match report tool %q", report.Tool, reportData.Evidence.Tool))
		}
		if reportData.Evidence.GitRevision != report.GitRevision {
			add(field+".gitRevision", fmt.Sprintf("manifest revision %q does not match report revision %q", report.GitRevision, reportData.Evidence.GitRevision))
		}
		if report.GitRevision != manifest.GitRevision {
			add(field+".gitRevision", fmt.Sprintf("got %q, want bundle revision %q", report.GitRevision, manifest.GitRevision))
		}
		if reportData.Evidence.GitModified != report.GitModified {
			add(field+".gitModified", fmt.Sprintf("manifest value %v does not match report value %v", report.GitModified, reportData.Evidence.GitModified))
		}
	}
	reportTargets := manifestReportTargets(manifest.Reports)
	if len(manifest.Targets) > 0 && !slices.Equal(reportTargets, manifest.Targets) {
		add("reports.targets", fmt.Sprintf("got %s, want manifest targets %s", strings.Join(reportTargets, ","), strings.Join(manifest.Targets, ",")))
	}
	if options.RequireTargets && !slices.Equal(reportTargets, options.ExpectedTargets) {
		add("reports.targets", fmt.Sprintf("got %s, want %s", strings.Join(reportTargets, ","), strings.Join(options.ExpectedTargets, ",")))
	}
	durableTargets := manifest.Targets
	if options.RequireTargets {
		durableTargets = options.ExpectedTargets
	}
	if options.RequireRemoteStaging && hasDurableManifestTarget(durableTargets) && !seenLabels["preflight"] {
		add("reports.preflight", "must be included for remote durable staging")
	}
	sort.Strings(violations)
	return violations, nil
}

type manifestReportData struct {
	Evidence runevidence.Evidence
	SHA256   string
	Read     bool
}

func readManifestReport(path string) (manifestReportData, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifestReportData{}, err
	}
	report := manifestReportData{
		SHA256: runevidence.BytesSHA256(data),
		Read:   true,
	}
	evidence, err := runevidence.DecodeReportEvidence(data)
	if err != nil {
		return report, err
	}
	report.Evidence = evidence
	return report, nil
}

func manifestReportTargets(reports []ManifestReport) []string {
	seen := map[string]bool{}
	for _, report := range reports {
		if definition, ok := reportDefinitionForLabel(report.Label); ok && definition.Target != "" {
			seen[definition.Target] = true
		}
	}
	out := []string{}
	for _, target := range ManifestTargetOrder() {
		if seen[target] {
			out = append(out, target)
		}
	}
	return out
}

func hasDurableManifestTarget(targets []string) bool {
	for _, target := range targets {
		target = strings.TrimSpace(target)
		for _, definition := range reportDefinitions {
			if definition.Target == target && definition.Durable {
				return true
			}
		}
	}
	return false
}

func ManifestTargetOrder() []string {
	out := []string{}
	for _, definition := range reportDefinitions {
		if definition.Target != "" {
			out = append(out, definition.Target)
		}
	}
	return out
}

func reportDefinitionForLabel(label string) (reportDefinition, bool) {
	label = strings.TrimSpace(label)
	for _, definition := range reportDefinitions {
		if definition.Label == label {
			return definition, true
		}
	}
	return reportDefinition{}, false
}
