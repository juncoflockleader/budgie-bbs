package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRunAcceptsConsistentBundleEvidence(t *testing.T) {
	dir := t.TempDir()
	preflight := writeBundleEvidenceReport(t, dir, "preflight.json", reportEvidence{
		Tool:        "budgie-internet-scale-preflight",
		GitRevision: "abc123",
	})
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})
	kafka := writeBundleEvidenceReport(t, dir, "kafka.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
		"-kafka-report", kafka,
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "internet-scale report bundle has consistent evidence metadata") {
		t.Fatalf("stdout missing success line:\n%s", stdout.String())
	}
}

func TestRunWritesBundleManifest(t *testing.T) {
	dir := t.TempDir()
	preflight := writeBundleEvidenceReport(t, dir, "preflight.json", reportEvidence{
		Tool:        "budgie-internet-scale-preflight",
		GitRevision: "abc123",
	})
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})
	kafka := writeBundleEvidenceReport(t, dir, "kafka.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})
	manifestPath := filepath.Join(dir, "nested", "bundle-manifest.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "kafka,gateway",
		"-remote-staging",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "archived internet-scale bundle manifest at "+manifestPath) {
		t.Fatalf("stdout missing manifest path:\n%s", stdout.String())
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest bundleManifest
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
	preflight := writeBundleEvidenceReport(t, dir, "preflight.json", reportEvidence{
		Tool:        "budgie-internet-scale-preflight",
		GitRevision: "abc123",
	})
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})
	kafka := writeBundleEvidenceReport(t, dir, "kafka.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})
	manifestPath := filepath.Join(dir, "bundle-manifest.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "gateway,kafka",
		"-remote-staging",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("write manifest exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"-verify-manifest", manifestPath,
		"-targets", "gateway,kafka",
		"-remote-staging",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("verify manifest exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "internet-scale bundle manifest verified at "+manifestPath) {
		t.Fatalf("stdout missing verify success:\n%s", stdout.String())
	}
}

func TestRunRejectsManifestHashMismatch(t *testing.T) {
	dir := t.TempDir()
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})
	kafka := writeBundleEvidenceReport(t, dir, "kafka.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})
	manifestPath := filepath.Join(dir, "bundle-manifest.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "gateway,kafka",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("write manifest exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}
	overwriteIndentedBundleEvidenceReport(t, gateway, reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"-verify-manifest", manifestPath,
		"-targets", "gateway,kafka",
	}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("verify manifest exit = %d, want violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "reports[0].sha256") ||
		!strings.Contains(stderr.String(), "want") {
		t.Fatalf("stderr missing hash mismatch:\n%s", stderr.String())
	}
}

func TestRunRejectsManifestScopeMismatch(t *testing.T) {
	dir := t.TempDir()
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})
	kafka := writeBundleEvidenceReport(t, dir, "kafka.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})
	manifestPath := filepath.Join(dir, "bundle-manifest.json")

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-report", gateway,
		"-kafka-report", kafka,
		"-manifest-file", manifestPath,
		"-targets", "gateway,kafka",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("write manifest exit = %d, want success\nstderr:\n%s", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = run([]string{
		"-verify-manifest", manifestPath,
		"-targets", "gateway,nats",
	}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("verify manifest exit = %d, want violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "targets") ||
		!strings.Contains(stderr.String(), "gateway,nats") {
		t.Fatalf("stderr missing target mismatch:\n%s", stderr.String())
	}
}

func TestRunRejectsMixedGitRevisions(t *testing.T) {
	dir := t.TempDir()
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
	})
	nats := writeBundleEvidenceReport(t, dir, "nats.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "def456",
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-gateway-report", gateway,
		"-nats-report", nats,
	}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, want violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "nats.evidence.gitRevision") ||
		!strings.Contains(stderr.String(), "abc123") ||
		!strings.Contains(stderr.String(), "def456") {
		t.Fatalf("stderr missing revision mismatch:\n%s", stderr.String())
	}
}

func TestRunRejectsWrongToolOrDirtyEvidence(t *testing.T) {
	dir := t.TempDir()
	preflight := writeBundleEvidenceReport(t, dir, "preflight.json", reportEvidence{
		Tool:        "budgie-commandlog-loadgen",
		GitRevision: "abc123",
	})
	gateway := writeBundleEvidenceReport(t, dir, "gateway.json", reportEvidence{
		Tool:        "budgie-gateway-loadgen",
		GitRevision: "abc123",
		GitModified: true,
	})

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"-preflight-report", preflight,
		"-gateway-report", gateway,
	}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("run exit = %d, want violation\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	for _, token := range []string{
		"preflight.evidence.tool",
		"budgie-internet-scale-preflight",
		"gateway.evidence.gitModified",
	} {
		if !strings.Contains(stderr.String(), token) {
			t.Fatalf("stderr missing %q:\n%s", token, stderr.String())
		}
	}
}

func writeBundleEvidenceReport(t *testing.T, dir, name string, evidence reportEvidence) string {
	t.Helper()
	data, err := json.Marshal(evidenceEnvelope{Evidence: evidence})
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return path
}

func overwriteIndentedBundleEvidenceReport(t *testing.T, path string, evidence reportEvidence) {
	t.Helper()
	data, err := json.MarshalIndent(evidenceEnvelope{Evidence: evidence}, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("rewrite report: %v", err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
