package accountmodel

import (
	"strings"
	"testing"
)

func TestTwoFactorStatusEnrolled(t *testing.T) {
	if (TwoFactorStatus{}).Enrolled() {
		t.Fatal("empty status should not be enrolled")
	}
	if !(TwoFactorStatus{TOTPEnrolled: true}).Enrolled() {
		t.Fatal("TOTP enrollment should count as enrolled")
	}
	if !(TwoFactorStatus{EmailEnrolled: true}).Enrolled() {
		t.Fatal("email enrollment should count as enrolled")
	}
}

func TestBackupCodeHelpers(t *testing.T) {
	code, err := RandomBackupCode()
	if err != nil {
		t.Fatalf("RandomBackupCode: %v", err)
	}
	if len(code) != 9 || code[4] != '-' {
		t.Fatalf("backup code format = %q", code)
	}
	if strings.ContainsAny(code, "01oil") {
		t.Fatalf("backup code contains ambiguous character: %q", code)
	}

	normalized := NormalizeBackupCode(" " + strings.ToUpper(strings.ReplaceAll(code, "-", " ")) + " ")
	if normalized != strings.ReplaceAll(code, "-", "") {
		t.Fatalf("normalized = %q for code %q", normalized, code)
	}
	if HashBackupCode(code) != HashBackupCode(normalized) {
		t.Fatal("hash should use normalized backup code")
	}
}

func TestRandomNumericCode(t *testing.T) {
	code, err := RandomNumericCode(6)
	if err != nil {
		t.Fatalf("RandomNumericCode: %v", err)
	}
	if len(code) != 6 {
		t.Fatalf("numeric code length = %d", len(code))
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("numeric code contains non-digit %q", r)
		}
	}
}

func TestEmail2FACodeMessage(t *testing.T) {
	subject, body := Email2FACodeMessage("123456")
	if subject != "Your BudgieBBS sign-in code" {
		t.Fatalf("subject = %q", subject)
	}
	if !strings.Contains(body, "123456") || !strings.Contains(body, "expires in 10 minutes") {
		t.Fatalf("unexpected body: %q", body)
	}
}
