package core_test

import (
	"strings"
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/totp"
)

func TestTwoFactorTOTPEnrollAndVerify(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	u := registerAndGetUser(t, c, "alice", "pw")

	if st, _ := c.TwoFactorStatus(u.ID); st.Enrolled() {
		t.Fatal("should not be enrolled initially")
	}
	secret, uri, err := c.BeginTOTPEnrollment(u.ID, u.Name)
	if err != nil || secret == "" || uri == "" {
		t.Fatalf("begin enrollment: %v", err)
	}
	// A wrong code must not activate the pending secret.
	if err := c.ConfirmTOTPEnrollment(u.ID, "000000"); err == nil {
		t.Fatal("expected invalid-code error")
	}
	if st, _ := c.TwoFactorStatus(u.ID); st.TOTPEnrolled {
		t.Fatal("must not be enrolled after a wrong code")
	}
	// The right code activates it.
	code, _ := totp.CodeAtTime(secret, time.Now().Unix())
	if err := c.ConfirmTOTPEnrollment(u.ID, code); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if st, _ := c.TwoFactorStatus(u.ID); !st.TOTPEnrolled {
		t.Fatal("should be TOTP enrolled")
	}
	// Verification at login.
	code, _ = totp.CodeAtTime(secret, time.Now().Unix())
	if err := c.VerifyTOTP(u.ID, code); err != nil {
		t.Fatalf("verify good code: %v", err)
	}
	if err := c.VerifyTOTP(u.ID, "111111"); err == nil {
		t.Fatal("bad code should fail verification")
	}
	if err := c.DisableTOTP(u.ID); err != nil {
		t.Fatal(err)
	}
	if st, _ := c.TwoFactorStatus(u.ID); st.TOTPEnrolled {
		t.Fatal("should be disabled")
	}
}

func TestVerifyTOTPRejectsReplay(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	u := registerAndGetUser(t, c, "alice", "pw")

	secret, _, err := c.BeginTOTPEnrollment(u.ID, u.Name)
	if err != nil {
		t.Fatalf("begin enrollment: %v", err)
	}
	confirmCode, _ := totp.CodeAtTime(secret, time.Now().Unix())
	if err := c.ConfirmTOTPEnrollment(u.ID, confirmCode); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	code, _ := totp.CodeAtTime(secret, time.Now().Unix())
	// First use of a fresh code succeeds.
	if err := c.VerifyTOTP(u.ID, code); err != nil {
		t.Fatalf("first verify should succeed: %v", err)
	}
	// Replaying the very same code must be rejected — a code is single-use per
	// time step (RFC 6238 §5.2).
	if err := c.VerifyTOTP(u.ID, code); err == nil {
		t.Fatal("a replayed TOTP code must be rejected")
	}
}

func TestTwoFactorBackupCodes(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	u := registerAndGetUser(t, c, "alice", "pw")

	// Backup codes require an enrolled method.
	if _, err := c.GenerateBackupCodes(u.ID); err == nil {
		t.Fatal("expected error generating backup codes before enrollment")
	}
	secret, _, err := c.BeginTOTPEnrollment(u.ID, u.Name)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.CodeAtTime(secret, time.Now().Unix())
	if err := c.ConfirmTOTPEnrollment(u.ID, code); err != nil {
		t.Fatal(err)
	}

	codes, err := c.GenerateBackupCodes(u.ID)
	if err != nil || len(codes) != 10 {
		t.Fatalf("generate backup codes: %v (%d)", err, len(codes))
	}
	if got := c.BackupCodesRemaining(u.ID); got != 10 {
		t.Fatalf("remaining = %d, want 10", got)
	}
	// A code works once, then is consumed.
	if err := c.VerifyBackupCode(u.ID, codes[0]); err != nil {
		t.Fatalf("verify backup code: %v", err)
	}
	if got := c.BackupCodesRemaining(u.ID); got != 9 {
		t.Fatalf("remaining after one use = %d, want 9", got)
	}
	if err := c.VerifyBackupCode(u.ID, codes[0]); err == nil {
		t.Fatal("a backup code must be single-use")
	}
	if err := c.VerifyBackupCode(u.ID, "wrong-code"); err == nil {
		t.Fatal("a wrong backup code must fail")
	}
	// Codes accept normalized input (case/dashes ignored).
	if err := c.VerifyBackupCode(u.ID, strings.ToUpper(strings.ReplaceAll(codes[1], "-", " "))); err != nil {
		t.Fatalf("normalized backup code should verify: %v", err)
	}
	// Regenerating replaces the old set.
	fresh, _ := c.GenerateBackupCodes(u.ID)
	if got := c.BackupCodesRemaining(u.ID); got != 10 {
		t.Fatalf("remaining after regen = %d, want 10", got)
	}
	if err := c.VerifyBackupCode(u.ID, codes[2]); err == nil {
		t.Fatal("old backup codes must be invalidated on regeneration")
	}
	if err := c.VerifyBackupCode(u.ID, fresh[0]); err != nil {
		t.Fatalf("new backup code should verify: %v", err)
	}
	// Disabling the last 2FA method clears backup codes.
	if err := c.DisableTOTP(u.ID); err != nil {
		t.Fatal(err)
	}
	if got := c.BackupCodesRemaining(u.ID); got != 0 {
		t.Fatalf("backup codes should be cleared when unenrolled, got %d", got)
	}
}

func TestTwoFactorEnforcement(t *testing.T) {
	c, cancel := newTestCore(t)
	defer cancel()
	admin := registerAndGetUser(t, c, "admin", "pw") // first user => admin
	bob := registerAndGetUser(t, c, "bob", "pw")     // ordinary user

	if req, _ := c.TwoFactorRequiredForLogin(admin.ID, admin.Role); req {
		t.Fatal("2FA must be off by default")
	}
	if _, err := c.SetSecuritySettings(true); err != nil {
		t.Fatal(err)
	}
	// Enforcement on but admin not enrolled: not challenged, but nudged.
	if req, _ := c.TwoFactorRequiredForLogin(admin.ID, admin.Role); req {
		t.Fatal("un-enrolled staff cannot be challenged")
	}
	if should, _ := c.StaffShouldEnroll2FA(admin.ID, admin.Role); !should {
		t.Fatal("un-enrolled staff should be nudged to enroll")
	}
	// After enrolling, the admin must be challenged.
	secret, _, _ := c.BeginTOTPEnrollment(admin.ID, admin.Name)
	code, _ := totp.CodeAtTime(secret, time.Now().Unix())
	if err := c.ConfirmTOTPEnrollment(admin.ID, code); err != nil {
		t.Fatal(err)
	}
	if req, _ := c.TwoFactorRequiredForLogin(admin.ID, admin.Role); !req {
		t.Fatal("enrolled staff must be challenged when enforcement is on")
	}
	// Ordinary users are never challenged.
	if req, _ := c.TwoFactorRequiredForLogin(bob.ID, bob.Role); req {
		t.Fatal("non-staff must never be challenged")
	}
}
