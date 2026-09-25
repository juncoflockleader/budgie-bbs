package runevidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectForToolTrimsBudgetAndHashesFile(t *testing.T) {
	path := writeEvidenceTestFile(t, "budget.json", []byte(`{"ok":true}`))

	evidence := CollectForTool("budgie-tool", " "+path+" ")
	requireString(t, "budget file", evidence.BudgetFile, path)
	requireString(t, "budget sha", evidence.BudgetSHA256, "4062edaf750fb8074e7e83e0c9028c94e32468a8b6f1614774328ef045150f93")
	requireString(t, "tool", evidence.Tool, "budgie-tool")
}

func TestCollectForToolRecordsTool(t *testing.T) {
	evidence := CollectForTool(" budgie-tool ", "")
	requireString(t, "tool", evidence.Tool, "budgie-tool")
}

func TestSHA256Helpers(t *testing.T) {
	data := []byte(`{"ok":true}`)
	want := "4062edaf750fb8074e7e83e0c9028c94e32468a8b6f1614774328ef045150f93"
	requireString(t, "BytesSHA256", BytesSHA256(data), want)
	path := writeEvidenceTestFile(t, "budget.json", data)
	got, err := ReadFileSHA256(" " + path + " ")
	if err != nil {
		t.Fatalf("ReadFileSHA256: %v", err)
	}
	requireString(t, "ReadFileSHA256", got, want)
	if _, err := ReadFileSHA256(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatalf("ReadFileSHA256 missing file succeeded, want error")
	}
}

func TestReportEvidenceEnvelope(t *testing.T) {
	data := []byte(`{"tool":"ignored","evidence":{"tool":"budgie-tool","gitRevision":"abc123"},"newerField":true}`)
	evidence, err := DecodeReportEvidence(data)
	if err != nil {
		t.Fatalf("DecodeReportEvidence: %v", err)
	}
	if evidence.Tool != "budgie-tool" || evidence.GitRevision != "abc123" {
		t.Fatalf("evidence = %+v, want nested report evidence", evidence)
	}

	path := writeEvidenceTestFile(t, "report.json", data)
	read, err := ReadReportEvidence(" " + path + " ")
	if err != nil {
		t.Fatalf("ReadReportEvidence: %v", err)
	}
	if read != evidence {
		t.Fatalf("read evidence = %+v, want %+v", read, evidence)
	}

	if _, err := DecodeReportEvidence([]byte(`{"evidence":{}} {}`)); err == nil || !strings.Contains(err.Error(), "unexpected trailing JSON") {
		t.Fatalf("trailing JSON err = %v, want trailing-data error", err)
	}
}

func TestNormalizeBudgetPath(t *testing.T) {
	requireString(t, "normalized path", normalizeBudgetPath(" ops/../ops/budget.json "), "ops/budget.json")
}

func requireString(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %q, want %q", name, got, want)
	}
}

func writeEvidenceTestFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestValidateReportEvidence(t *testing.T) {
	policy := ReportEvidencePolicy{
		Tool:               "budgie-loadgen",
		RequiredBudgetFile: "ops/budget.json",
		ReportName:         "load report",
	}
	if violations := ValidateReportEvidence(Evidence{
		Tool:         "budgie-loadgen",
		BudgetFile:   "ops/../ops/budget.json",
		BudgetSHA256: strings.Repeat("a", 64),
		GitRevision:  "abc123",
	}, policy); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	violations := ValidateReportEvidence(Evidence{
		Tool:        "other",
		BudgetFile:  "ops/other.json",
		GitModified: true,
	}, policy)
	requireEvidenceViolationFields(t, violations, "load report", "tool", "budgetFile", "budgetSha256", "gitRevision", "gitModified")
}

func TestValidateToolGitEvidence(t *testing.T) {
	if violations := ValidateToolGitEvidence(Evidence{
		Tool:        "budgie-preflight",
		GitRevision: "abc123",
	}, "budgie-preflight", "preflight report"); len(violations) != 0 {
		t.Fatalf("violations = %+v, want none", violations)
	}

	violations := ValidateToolGitEvidence(Evidence{
		Tool:        "other",
		GitModified: true,
	}, "budgie-preflight", "preflight report")
	requireEvidenceViolationFields(t, violations, "preflight report", "tool", "gitRevision", "gitModified")
	if violations[0].Want != "budgie-preflight" {
		t.Fatalf("tool want = %v, want budgie-preflight", violations[0].Want)
	}
}

func requireEvidenceViolationFields(t *testing.T, violations []ReportEvidenceViolation, reportName string, fields ...string) {
	t.Helper()
	if len(violations) != len(fields) {
		t.Fatalf("violations = %+v, want %d", violations, len(fields))
	}
	for i, field := range fields {
		if violations[i].Field != field {
			t.Fatalf("violation[%d].Field = %q, want %q", i, violations[i].Field, field)
		}
		if !strings.Contains(violations[i].Message, reportName) {
			t.Fatalf("violation[%d].Message = %q, want report name", i, violations[i].Message)
		}
	}
}

func TestFormatReportEvidenceViolations(t *testing.T) {
	got := FormatReportEvidenceViolations("evidence.", []ReportEvidenceViolation{
		{Field: "tool", Value: "other", Want: "budgie-tool", Message: "wrong tool"},
	})
	want := []string{"evidence.tool: wrong tool (value=other want=budgie-tool)"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("formatted violations = %+v, want %+v", got, want)
	}
}

func TestSanitizeEndpointRedactsSecrets(t *testing.T) {
	got := SanitizeEndpoint("host=postgres.internal port=5432 dbname=budgie user=budgie password=secret", "postgres")
	requireString(t, "keyword dsn", got, "postgres://postgres.internal:5432/budgie")
	got = SanitizeEndpoint("postgres://user:secret@postgres.internal:5432/budgie?sslmode=require#frag", "postgres")
	requireString(t, "url dsn", got, "postgres://postgres.internal:5432/budgie")
}

func TestSanitizeKafkaBrokers(t *testing.T) {
	got := SanitizeKafkaBrokers("user:secret@redpanda-a.internal:9092?token=secret, redpanda-b.internal:9092")
	want := []string{"kafka://redpanda-a.internal:9092", "kafka://redpanda-b.internal:9092"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("brokers = %+v, want %+v", got, want)
	}
}

func TestSanitizedEndpointEvidenceViolation(t *testing.T) {
	if got := SanitizedEndpointEvidenceViolation("postgres://postgres.internal:5432/budgie"); got != "" {
		t.Fatalf("sanitized endpoint violation = %q, want none", got)
	}
	for _, endpoint := range []string{
		"postgres.internal:5432",
		"postgres://user:secret@postgres.internal:5432/budgie",
		"postgres://postgres.internal:5432/budgie?sslmode=require",
		"postgres://postgres.internal:5432/budgie#frag",
	} {
		if got := SanitizedEndpointEvidenceViolation(endpoint); got == "" {
			t.Fatalf("endpoint %q had no violation", endpoint)
		}
	}
}

func TestEndpointSensitivityChecks(t *testing.T) {
	if EndpointLooksSensitive("postgres://postgres.internal:5432/budgie") {
		t.Fatal("plain endpoint should not look sensitive")
	}
	for _, endpoint := range []string{
		"postgres://user:secret@postgres.internal:5432/budgie",
		"postgres://postgres.internal:5432/budgie?token=secret",
		"host=postgres.internal password=secret",
	} {
		if !EndpointLooksSensitive(endpoint) {
			t.Fatalf("endpoint %q should look sensitive", endpoint)
		}
	}
	requireString(t, "broker URL", KafkaBrokerEndpointURL("redpanda.internal:9092"), "kafka://redpanda.internal:9092")
}

func TestEndpointHostLocalChecks(t *testing.T) {
	for _, endpoint := range []string{
		"postgres://localhost:5432/budgie",
		"localhost:5432",
		"host=127.0.0.1 port=5432 dbname=budgie",
		"[::1]:9092",
	} {
		if !EndpointHostIsLocal(endpoint) {
			t.Fatalf("%q should be local", endpoint)
		}
	}
	if EndpointHostIsLocal("postgres://postgres.internal:5432/budgie") {
		t.Fatal("postgres.internal should not be local")
	}
	if !KafkaBrokerListHasLocal("redpanda.internal:9092,127.0.0.1:9092") {
		t.Fatal("broker list with loopback should be local")
	}
}
