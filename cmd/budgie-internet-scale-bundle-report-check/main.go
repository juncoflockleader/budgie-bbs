package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type evidenceEnvelope struct {
	Evidence reportEvidence `json:"evidence"`
}

type reportEvidence struct {
	Tool        string `json:"tool,omitempty"`
	GitRevision string `json:"gitRevision,omitempty"`
	GitModified bool   `json:"gitModified"`
}

type reportSpec struct {
	Label string
	Path  string
	Tool  string
}

type manifestOptions struct {
	Targets       []string
	RemoteStaging bool
}

type manifestVerifyOptions struct {
	ExpectedTargets       []string
	RequireTargets        bool
	ExpectedRemoteStaging bool
	RequireRemoteStaging  bool
}

type bundleManifest struct {
	Tool          string                 `json:"tool"`
	GeneratedAt   int64                  `json:"generatedAt"`
	GitRevision   string                 `json:"gitRevision"`
	Targets       []string               `json:"targets,omitempty"`
	RemoteStaging bool                   `json:"remoteStaging"`
	Reports       []bundleManifestReport `json:"reports"`
}

type bundleManifestReport struct {
	Label       string `json:"label"`
	Path        string `json:"path"`
	Tool        string `json:"tool"`
	GitRevision string `json:"gitRevision"`
	GitModified bool   `json:"gitModified"`
	SHA256      string `json:"sha256"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("budgie-internet-scale-bundle-report-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	preflightReport := flags.String("preflight-report", "", "Optional internet-scale preflight JSON report")
	gatewayReport := flags.String("gateway-report", "", "Optional gateway fanout JSON report")
	natsReport := flags.String("nats-report", "", "Optional native NATS command-log JSON report")
	kafkaReport := flags.String("kafka-report", "", "Optional native Kafka command-log JSON report")
	manifestFile := flags.String("manifest-file", "", "Optional JSON manifest path written after a successful bundle check")
	verifyManifest := flags.String("verify-manifest", "", "Optional JSON manifest path to verify against referenced report files")
	targets := flags.String("targets", "", "Comma-separated bundle targets to record in the manifest: gateway,nats,kafka,all")
	remoteStaging := flags.Bool("remote-staging", false, "Record this bundle as remote/shared staging evidence")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unsupported argument %q; use flags only\n", flags.Arg(0))
		return 2
	}

	targetRaw := strings.TrimSpace(*targets)
	targetList, err := normalizeManifestTargets(*targets)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if strings.TrimSpace(*verifyManifest) != "" {
		violations, err := verifyBundleManifest(strings.TrimSpace(*verifyManifest), manifestVerifyOptions{
			ExpectedTargets:       targetList,
			RequireTargets:        targetRaw != "",
			ExpectedRemoteStaging: *remoteStaging,
			RequireRemoteStaging:  *remoteStaging,
		})
		if err != nil {
			fmt.Fprintf(stderr, "verify bundle manifest: %v\n", err)
			return 2
		}
		if len(violations) > 0 {
			fmt.Fprintf(stderr, "internet-scale bundle manifest violations: %s\n", strings.Join(violations, "; "))
			return 3
		}
		fmt.Fprintf(stdout, "internet-scale bundle manifest verified at %s\n", strings.TrimSpace(*verifyManifest))
		return 0
	}

	specs := selectedReportSpecs(*preflightReport, *gatewayReport, *natsReport, *kafkaReport)
	if len(specs) == 0 {
		fmt.Fprintln(stderr, "at least one report path is required")
		return 2
	}
	violations := evaluateBundleReports(specs)
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "internet-scale bundle evidence violations: %s\n", strings.Join(violations, "; "))
		return 3
	}
	if strings.TrimSpace(*manifestFile) != "" {
		if err := writeBundleManifest(strings.TrimSpace(*manifestFile), specs, manifestOptions{Targets: targetList, RemoteStaging: *remoteStaging}); err != nil {
			fmt.Fprintf(stderr, "write bundle manifest: %v\n", err)
			return 2
		}
		fmt.Fprintf(stdout, "archived internet-scale bundle manifest at %s\n", strings.TrimSpace(*manifestFile))
	}
	fmt.Fprintln(stdout, "internet-scale report bundle has consistent evidence metadata")
	return 0
}

func selectedReportSpecs(preflightReport, gatewayReport, natsReport, kafkaReport string) []reportSpec {
	candidates := []reportSpec{
		{Label: "preflight", Path: preflightReport, Tool: "budgie-internet-scale-preflight"},
		{Label: "gateway", Path: gatewayReport, Tool: "budgie-gateway-loadgen"},
		{Label: "nats", Path: natsReport, Tool: "budgie-commandlog-loadgen"},
		{Label: "kafka", Path: kafkaReport, Tool: "budgie-commandlog-loadgen"},
	}
	out := []reportSpec{}
	for _, spec := range candidates {
		spec.Path = strings.TrimSpace(spec.Path)
		if spec.Path != "" {
			out = append(out, spec)
		}
	}
	return out
}

func evaluateBundleReports(specs []reportSpec) []string {
	violations := []string{}
	var wantRevision string
	for _, spec := range specs {
		evidence, err := readReportEvidence(spec.Path)
		if err != nil {
			violations = append(violations, fmt.Sprintf("%s.report: read %s: %v", spec.Label, spec.Path, err))
			continue
		}
		if strings.TrimSpace(evidence.Tool) != spec.Tool {
			violations = append(violations, fmt.Sprintf("%s.evidence.tool: got %q, want %q", spec.Label, evidence.Tool, spec.Tool))
		}
		revision := strings.TrimSpace(evidence.GitRevision)
		if revision == "" {
			violations = append(violations, spec.Label+".evidence.gitRevision: must be recorded")
		} else if wantRevision == "" {
			wantRevision = revision
		} else if revision != wantRevision {
			violations = append(violations, fmt.Sprintf("%s.evidence.gitRevision: got %q, want %q", spec.Label, revision, wantRevision))
		}
		if evidence.GitModified {
			violations = append(violations, spec.Label+".evidence.gitModified: must be false")
		}
	}
	return violations
}

func normalizeManifestTargets(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	order := map[string]int{"gateway": 0, "nats": 1, "kafka": 2}
	seen := map[string]bool{}
	out := []string{}
	for _, field := range strings.Fields(strings.ReplaceAll(raw, ",", " ")) {
		target := strings.ToLower(strings.TrimSpace(field))
		switch target {
		case "all":
			for _, candidate := range []string{"gateway", "nats", "kafka"} {
				if !seen[candidate] {
					seen[candidate] = true
					out = append(out, candidate)
				}
			}
		case "gateway", "nats", "kafka":
			if !seen[target] {
				seen[target] = true
				out = append(out, target)
			}
		case "":
		default:
			return nil, fmt.Errorf("unsupported bundle manifest target %q; supported targets: gateway,nats,kafka,all", field)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return order[out[i]] < order[out[j]]
	})
	return out, nil
}

func readReportEvidence(path string) (reportEvidence, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return reportEvidence{}, err
	}
	var envelope evidenceEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return reportEvidence{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return reportEvidence{}, fmt.Errorf("unexpected trailing JSON")
	}
	return envelope.Evidence, nil
}

func writeBundleManifest(path string, specs []reportSpec, options manifestOptions) error {
	manifest, err := buildBundleManifest(specs, options)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func buildBundleManifest(specs []reportSpec, options manifestOptions) (bundleManifest, error) {
	manifest := bundleManifest{
		Tool:          "budgie-internet-scale-bundle-report-check",
		GeneratedAt:   time.Now().UnixMilli(),
		Targets:       append([]string(nil), options.Targets...),
		RemoteStaging: options.RemoteStaging,
	}
	for _, spec := range specs {
		data, err := os.ReadFile(spec.Path)
		if err != nil {
			return bundleManifest{}, err
		}
		evidence, err := decodeReportEvidence(data)
		if err != nil {
			return bundleManifest{}, fmt.Errorf("%s: %w", spec.Path, err)
		}
		if manifest.GitRevision == "" {
			manifest.GitRevision = strings.TrimSpace(evidence.GitRevision)
		}
		sum := sha256.Sum256(data)
		manifest.Reports = append(manifest.Reports, bundleManifestReport{
			Label:       spec.Label,
			Path:        spec.Path,
			Tool:        evidence.Tool,
			GitRevision: evidence.GitRevision,
			GitModified: evidence.GitModified,
			SHA256:      fmt.Sprintf("%x", sum),
		})
	}
	return manifest, nil
}

func verifyBundleManifest(path string, options manifestVerifyOptions) ([]string, error) {
	manifest, err := readBundleManifest(path)
	if err != nil {
		return nil, err
	}
	violations := []string{}
	add := func(field, message string) {
		violations = append(violations, field+": "+message)
	}
	if manifest.Tool != "budgie-internet-scale-bundle-report-check" {
		add("tool", `must be "budgie-internet-scale-bundle-report-check"`)
	}
	if manifest.GeneratedAt <= 0 {
		add("generatedAt", "must be positive")
	}
	if strings.TrimSpace(manifest.GitRevision) == "" {
		add("gitRevision", "must be recorded")
	}
	if options.RequireTargets && !equalStringSlices(manifest.Targets, options.ExpectedTargets) {
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
		expectedTool := expectedToolForLabel(label)
		if expectedTool == "" {
			add(field+".label", "unsupported label "+label)
		} else if strings.TrimSpace(report.Tool) == "" {
			add(field+".tool", "must be recorded")
		} else if report.Tool != expectedTool {
			add(field+".tool", fmt.Sprintf("got %q, want %q", report.Tool, expectedTool))
		}
		if strings.TrimSpace(report.GitRevision) == "" {
			add(field+".gitRevision", "must be recorded")
		}
		if path == "" {
			add(field+".path", "must be recorded")
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			add(field+".path", "read "+path+": "+err.Error())
			continue
		}
		sum := sha256.Sum256(data)
		gotHash := fmt.Sprintf("%x", sum)
		if strings.TrimSpace(report.SHA256) == "" {
			add(field+".sha256", "must be recorded")
		} else if !strings.EqualFold(report.SHA256, gotHash) {
			add(field+".sha256", fmt.Sprintf("got %s, want %s", report.SHA256, gotHash))
		}
		evidence, err := decodeReportEvidence(data)
		if err != nil {
			add(field+".evidence", err.Error())
			continue
		}
		if evidence.Tool != report.Tool {
			add(field+".tool", fmt.Sprintf("manifest tool %q does not match report tool %q", report.Tool, evidence.Tool))
		}
		if evidence.GitRevision != report.GitRevision {
			add(field+".gitRevision", fmt.Sprintf("manifest revision %q does not match report revision %q", report.GitRevision, evidence.GitRevision))
		}
		if report.GitRevision != manifest.GitRevision {
			add(field+".gitRevision", fmt.Sprintf("got %q, want bundle revision %q", report.GitRevision, manifest.GitRevision))
		}
		if evidence.GitModified != report.GitModified {
			add(field+".gitModified", fmt.Sprintf("manifest value %v does not match report value %v", report.GitModified, evidence.GitModified))
		}
		if report.GitModified {
			add(field+".gitModified", "must be false")
		}
	}
	reportTargets := manifestReportTargets(manifest.Reports)
	if len(manifest.Targets) > 0 && !equalStringSlices(reportTargets, manifest.Targets) {
		add("reports.targets", fmt.Sprintf("got %s, want manifest targets %s", strings.Join(reportTargets, ","), strings.Join(manifest.Targets, ",")))
	}
	if options.RequireTargets && !equalStringSlices(reportTargets, options.ExpectedTargets) {
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

func readBundleManifest(path string) (bundleManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return bundleManifest{}, err
	}
	var manifest bundleManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&manifest); err != nil {
		return bundleManifest{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return bundleManifest{}, fmt.Errorf("unexpected trailing JSON")
	}
	return manifest, nil
}

func expectedToolForLabel(label string) string {
	switch strings.TrimSpace(label) {
	case "preflight":
		return "budgie-internet-scale-preflight"
	case "gateway":
		return "budgie-gateway-loadgen"
	case "nats", "kafka":
		return "budgie-commandlog-loadgen"
	default:
		return ""
	}
}

func manifestReportTargets(reports []bundleManifestReport) []string {
	seen := map[string]bool{}
	for _, report := range reports {
		switch strings.TrimSpace(report.Label) {
		case "gateway":
			seen["gateway"] = true
		case "nats":
			seen["nats"] = true
		case "kafka":
			seen["kafka"] = true
		}
	}
	out := []string{}
	for _, target := range []string{"gateway", "nats", "kafka"} {
		if seen[target] {
			out = append(out, target)
		}
	}
	return out
}

func hasDurableManifestTarget(targets []string) bool {
	for _, target := range targets {
		switch strings.TrimSpace(target) {
		case "nats", "kafka":
			return true
		}
	}
	return false
}

func equalStringSlices(a, b []string) bool {
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

func decodeReportEvidence(data []byte) (reportEvidence, error) {
	var envelope evidenceEnvelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&envelope); err != nil {
		return reportEvidence{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return reportEvidence{}, fmt.Errorf("unexpected trailing JSON")
	}
	return envelope.Evidence, nil
}
