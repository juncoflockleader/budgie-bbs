package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/runbundle"
	"github.com/juncoflockleader/budgie-bbs/internal/runevidence"
)

func TestRunAcceptsConsistentBundleEvidence(t *testing.T) {
	dir := t.TempDir()
	preflight := writeCleanBundleEvidenceReport(t, dir, "preflight")
	gateway := writeCleanBundleEvidenceReport(t, dir, "gateway")
	kafka := writeCleanBundleEvidenceReport(t, dir, "kafka")

	result := runBundleForTest(t, []string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
		"-kafka-report", kafka,
	})
	requireReportCheckExit(t, result, 0)
	requireReportCheckOutputContains(t, "stdout", result.Stdout, "internet-scale report bundle has consistent evidence metadata")
}

func TestRunWritesBundleManifest(t *testing.T) {
	dir := t.TempDir()
	preflight := writeCleanBundleEvidenceReport(t, dir, "preflight")
	gateway := writeCleanBundleEvidenceReport(t, dir, "gateway")
	kafka := writeCleanBundleEvidenceReport(t, dir, "kafka")
	manifestPath := filepath.Join(dir, "nested", "bundle-manifest.json")

	result := runBundleForTest(t, []string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "kafka,gateway",
		"-remote-staging",
	})
	requireReportCheckExit(t, result, 0)
	requireReportCheckOutputContains(t, "stdout", result.Stdout, "archived internet-scale bundle manifest at "+manifestPath)

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest runbundle.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Tool != "budgie-internet-scale-bundle-report-check" {
		t.Fatalf("manifest tool = %q", manifest.Tool)
	}
	if manifest.GitRevision != "abc123" {
		t.Fatalf("manifest git revision = %q", manifest.GitRevision)
	}
	if !reflect.DeepEqual(manifest.Targets, []string{"gateway", "kafka"}) {
		t.Fatalf("manifest targets = %+v", manifest.Targets)
	}
	if !manifest.RemoteStaging {
		t.Fatalf("manifest remoteStaging = false, want true")
	}
	if manifest.GeneratedAt <= 0 {
		t.Fatalf("manifest generatedAt = %d, want positive", manifest.GeneratedAt)
	}
	if len(manifest.Reports) != 3 {
		t.Fatalf("manifest reports = %d, want 3", len(manifest.Reports))
	}
	want := map[string]struct {
		path string
		tool string
		hash string
	}{
		"preflight": {path: preflight, tool: "budgie-internet-scale-preflight", hash: fileSHA256(t, preflight)},
		"gateway":   {path: gateway, tool: "budgie-gateway-loadgen", hash: fileSHA256(t, gateway)},
		"kafka":     {path: kafka, tool: "budgie-commandlog-loadgen", hash: fileSHA256(t, kafka)},
	}
	for _, report := range manifest.Reports {
		expected, ok := want[report.Label]
		if !ok {
			t.Fatalf("unexpected manifest report label %q", report.Label)
		}
		if report.Path != expected.path || report.Tool != expected.tool || report.GitRevision != "abc123" || report.GitModified {
			t.Fatalf("manifest report = %+v, want path/tool/revision/clean evidence", report)
		}
		if report.SHA256 != expected.hash {
			t.Fatalf("manifest report hash for %s = %q, want %q", report.Label, report.SHA256, expected.hash)
		}
	}
}

func TestRunVerifiesBundleManifest(t *testing.T) {
	dir := t.TempDir()
	preflight := writeCleanBundleEvidenceReport(t, dir, "preflight")
	gateway := writeCleanBundleEvidenceReport(t, dir, "gateway")
	kafka := writeCleanBundleEvidenceReport(t, dir, "kafka")
	manifestPath := filepath.Join(dir, "bundle-manifest.json")

	result := runBundleForTest(t, []string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "gateway,kafka",
		"-remote-staging",
	})
	requireReportCheckExit(t, result, 0)

	result = runBundleForTest(t, []string{
		"-verify-manifest", manifestPath,
		"-targets", "gateway,kafka",
		"-remote-staging",
	})
	requireReportCheckExit(t, result, 0)
	requireReportCheckOutputContains(t, "stdout", result.Stdout, "internet-scale bundle manifest verified at "+manifestPath)
}

func TestRunRejectsManifestHashMismatch(t *testing.T) {
	dir := t.TempDir()
	gateway := writeCleanBundleEvidenceReport(t, dir, "gateway")
	kafka := writeCleanBundleEvidenceReport(t, dir, "kafka")
	manifestPath := filepath.Join(dir, "bundle-manifest.json")

	result := runBundleForTest(t, []string{
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "gateway,kafka",
	})
	requireReportCheckExit(t, result, 0)
	writeReportCheckJSONFile(t, gateway, runevidence.ReportEnvelope{Evidence: bundleEvidence("gateway", "abc123")}, true)

	result = runBundleForTest(t, []string{
		"-verify-manifest", manifestPath,
		"-targets", "gateway,kafka",
	})
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "reports[0].sha256", "want")
}

func TestRunRejectsManifestScopeMismatch(t *testing.T) {
	dir := t.TempDir()
	gateway := writeCleanBundleEvidenceReport(t, dir, "gateway")
	kafka := writeCleanBundleEvidenceReport(t, dir, "kafka")
	manifestPath := filepath.Join(dir, "bundle-manifest.json")

	result := runBundleForTest(t, []string{
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "gateway,kafka",
	})
	requireReportCheckExit(t, result, 0)

	result = runBundleForTest(t, []string{
		"-verify-manifest", manifestPath,
		"-targets", "gateway,nats",
	})
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "targets", "gateway,nats")
}

func TestRunRejectsMixedGitRevisions(t *testing.T) {
	dir := t.TempDir()
	gateway := writeCleanBundleEvidenceReport(t, dir, "gateway")
	nats := writeBundleEvidenceReport(t, dir, "nats.json", bundleEvidence("nats", "def456"))

	result := runBundleForTest(t, []string{
		"-gateway-report", gateway,
		"-nats-report", nats,
	})
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr, "nats.evidence.gitRevision", "abc123", "def456")
}

func TestRunRejectsWrongToolOrDirtyEvidence(t *testing.T) {
	dir := t.TempDir()
	preflight := writeBundleEvidenceReport(t, dir, "preflight.json", runevidence.Evidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", runevidence.Evidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
		GitModified: true,
	})

	result := runBundleForTest(t, []string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
	})
	requireReportCheckExit(t, result, 3)
	requireReportCheckOutputContains(t, "stderr", result.Stderr,
		"preflight.evidence.tool",
		"budgie-internet-scale-preflight",
		"gateway.evidence.gitModified",
	)
}

func writeBundleEvidenceReport(t *testing.T, dir, name string, evidence runevidence.Evidence) string {
	t.Helper()
	return writeReportCheckJSONInDir(t, dir, name, runevidence.ReportEnvelope{Evidence: evidence}, false)
}

func writeCleanBundleEvidenceReport(t *testing.T, dir, label string) string {
	t.Helper()
	return writeBundleEvidenceReport(t, dir, label+".json", bundleEvidence(label, "abc123"))
}

func bundleEvidence(label, revision string) runevidence.Evidence {
	tool := ""
	switch label {
	case "preflight":
		tool = "budgie-internet-scale-preflight"
	case "gateway":
		tool = "budgie-gateway-loadgen"
	case "nats", "kafka":
		tool = "budgie-commandlog-loadgen"
	}
	return runevidence.Evidence{Tool: tool, GitRevision: revision}
}
