package core_test

import (
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
