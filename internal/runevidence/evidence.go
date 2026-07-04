package runevidence

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/runreport"
)

type Evidence struct {
	Tool         string `json:"tool,omitempty"`
	BudgetFile   string `json:"budgetFile,omitempty"`
	BudgetSHA256 string `json:"budgetSha256,omitempty"`
	GitRevision  string `json:"gitRevision,omitempty"`
	GitModified  bool   `json:"gitModified"`
}

type ReportEnvelope struct {
	Evidence Evidence `json:"evidence"`
}

type ReportEvidencePolicy struct {
	Tool               string
	RequiredBudgetFile string
	ReportName         string
}

type ReportEvidenceViolation struct {
	Field   string
	Value   any
	Want    any
	Message string
}

func newReportEvidenceViolation(field string, value, want any, message string) ReportEvidenceViolation {
	return ReportEvidenceViolation{Field: field, Value: value, Want: want, Message: message}
}

func FormatReportEvidenceViolations(prefix string, violations []ReportEvidenceViolation) []string {
	out := make([]string, 0, len(violations))
	for _, violation := range violations {
		out = append(out, fmt.Sprintf("%s%s: %s (value=%v want=%v)",
			prefix, violation.Field, violation.Message, violation.Value, violation.Want))
	}
	return out
}

func collect(budgetFile string) Evidence {
	budgetFile = strings.TrimSpace(budgetFile)
	evidence := Evidence{
		BudgetFile: budgetFile,
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				evidence.GitRevision = strings.TrimSpace(setting.Value)
			case "vcs.modified":
				evidence.GitModified = strings.EqualFold(strings.TrimSpace(setting.Value), "true")
			}
		}
	}
	if evidence.GitRevision == "" {
		if revision, ok := gitOutput("rev-parse", "HEAD"); ok {
			evidence.GitRevision = revision
		}
	}
	if status, ok := gitOutput("status", "--porcelain"); ok && strings.TrimSpace(status) != "" {
		evidence.GitModified = true
	}
	if budgetFile != "" {
		if hash, err := ReadFileSHA256(budgetFile); err == nil {
			evidence.BudgetSHA256 = hash
		}
	}
	return evidence
}

func CollectForTool(tool, budgetFile string) Evidence {
	evidence := collect(budgetFile)
	evidence.Tool = strings.TrimSpace(tool)
	return evidence
}

func ReadFileSHA256(path string) (string, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return "", err
	}
	return BytesSHA256(data), nil
}

func ReadReportEvidence(path string) (Evidence, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return Evidence{}, err
	}
	return DecodeReportEvidence(data)
}

func DecodeReportEvidence(data []byte) (Evidence, error) {
	envelope, err := runreport.DecodeJSON[ReportEnvelope](bytes.NewReader(data), false)
	if err != nil {
		return Evidence{}, err
	}
	return envelope.Evidence, nil
}

func BytesSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func normalizeBudgetPath(path string) string {
	return filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
}

func ValidateToolGitEvidence(evidence Evidence, tool, reportName string) []ReportEvidenceViolation {
	reportName = normalizedReportName(reportName)
	out := []ReportEvidenceViolation{}
	out = append(out, validateReportToolEvidence(evidence, tool, reportName)...)
	out = append(out, validateReportGitEvidence(evidence, reportName)...)
	return out
}

func ValidateReportEvidence(evidence Evidence, policy ReportEvidencePolicy) []ReportEvidenceViolation {
	reportName := normalizedReportName(policy.ReportName)
	out := []ReportEvidenceViolation{}
	out = append(out, validateReportToolEvidence(evidence, policy.Tool, reportName)...)
	budgetFile := strings.TrimSpace(evidence.BudgetFile)
	requiredBudgetFile := strings.TrimSpace(policy.RequiredBudgetFile)
	if budgetFile == "" {
		out = append(out, newReportEvidenceViolation("budgetFile", evidence.BudgetFile, "non-empty", reportName+" must record the budget file"))
	} else if requiredBudgetFile != "" && normalizeBudgetPath(budgetFile) != normalizeBudgetPath(requiredBudgetFile) {
		out = append(out, newReportEvidenceViolation("budgetFile", evidence.BudgetFile, requiredBudgetFile, reportName+" must record the required budget file"))
	}
	if strings.TrimSpace(evidence.BudgetSHA256) == "" {
		out = append(out, newReportEvidenceViolation("budgetSha256", evidence.BudgetSHA256, "non-empty", reportName+" must record the budget file hash"))
	}
	out = append(out, validateReportGitEvidence(evidence, reportName)...)
	return out
}

func normalizedReportName(reportName string) string {
	reportName = strings.TrimSpace(reportName)
	if reportName == "" {
		return "report"
	}
	return reportName
}

func validateReportToolEvidence(evidence Evidence, tool, reportName string) []ReportEvidenceViolation {
	tool = strings.TrimSpace(tool)
	if strings.TrimSpace(evidence.Tool) == tool {
		return nil
	}
	return []ReportEvidenceViolation{newReportEvidenceViolation("tool", evidence.Tool, tool, reportName+" must record the producing tool")}
}

func validateReportGitEvidence(evidence Evidence, reportName string) []ReportEvidenceViolation {
	out := []ReportEvidenceViolation{}
	if strings.TrimSpace(evidence.GitRevision) == "" {
		out = append(out, newReportEvidenceViolation("gitRevision", evidence.GitRevision, "non-empty", reportName+" must record the git revision"))
	}
	if evidence.GitModified {
		out = append(out, newReportEvidenceViolation("gitModified", evidence.GitModified, false, reportName+" must come from a clean git tree"))
	}
	return out
}

func SanitizeEndpoint(raw, fallbackScheme string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if parsed, err := url.Parse(raw); err == nil && parsed.Scheme != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	fields := dsnFields(raw)
	host := strings.TrimSpace(fields["host"])
	port := strings.TrimSpace(fields["port"])
	dbname := strings.TrimSpace(fields["dbname"])
	if host == "" && dbname == "" {
		return strings.TrimSpace(fallbackScheme) + "-endpoint"
	}
	if port != "" && !strings.Contains(host, ":") {
		host += ":" + port
	}
	endpoint := strings.TrimSpace(fallbackScheme) + "://"
	if host != "" {
		endpoint += host
	}
	if dbname != "" {
		endpoint += "/" + url.PathEscape(dbname)
	}
	return endpoint
}

func SanitizeKafkaBrokers(raw string) []string {
	return SanitizeKafkaBrokerEndpoints(strings.Split(raw, ","))
}

func SanitizeKafkaBrokerEndpoints(brokers []string) []string {
	out := make([]string, 0, len(brokers))
	for _, broker := range brokers {
		broker = strings.TrimSpace(broker)
		if broker == "" {
			continue
		}
		out = append(out, SanitizeEndpoint(KafkaBrokerEndpointURL(broker), "kafka"))
	}
	return out
}

func SanitizedEndpointEvidenceViolation(endpoint string) string {
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

func EndpointLooksSensitive(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return false
	}
	lower := strings.ToLower(endpoint)
	if strings.Contains(lower, "password=") || strings.Contains(lower, "token=") {
		return true
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" {
		return false
	}
	return parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != ""
}

func KafkaBrokerEndpointURL(broker string) string {
	broker = strings.TrimSpace(broker)
	if broker == "" || strings.Contains(broker, "://") {
		return broker
	}
	return "kafka://" + broker
}

func EndpointHostIsLocal(endpoint string) bool {
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

func KafkaBrokerListHasLocal(brokers string) bool {
	for _, broker := range strings.Split(brokers, ",") {
		if EndpointHostIsLocal(KafkaBrokerEndpointURL(broker)) {
			return true
		}
	}
	return false
}

func endpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	if parsed, err := url.Parse(endpoint); err == nil && parsed.Scheme != "" {
		if host := strings.TrimSpace(parsed.Hostname()); host != "" {
			return strings.ToLower(host)
		}
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
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.Trim(strings.TrimSpace(value), "'\"")
		fields[key] = value
	}
	return fields
}

func gitOutput(args ...string) (string, bool) {
	cmd := exec.Command("git", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}
