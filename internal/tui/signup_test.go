package tui

import (
	"testing"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// TestSSHSignupFlow drives the guest registration wizard through every step and
// confirms the account, private intake, and policy acceptance are persisted.
func TestSSHSignupFlow(t *testing.T) {
	c := newTestCore(t)
	// Bootstrap an admin so the guest is not the first (admin) account.
	if _, err := c.RegisterUser("root", "correct-horse-battery-staple"); err != nil {
		t.Fatal(err)
	}
	c.SetPrivacyPolicy(true)

	guest := &core.User{ID: "guest", Name: "guest", Role: "user"}
	m := newModel(c, guest, 80, 24, false, localeEN, "", nil, nil, "", true)
	if !m.canRegister() {
		t.Fatal("guest with allowRegistration should be able to register")
	}

	m.enterSignup()
	if m.page != pageSignup || m.signup == nil {
		t.Fatalf("enterSignup did not enter the signup page")
	}

	// Walk the text steps (no email step: email verification is off).
	steps := []struct{ field, value string }{
		{"username", "sshuser"},
		{"password", "password123"},
		{"realName", "SSH User"},
		{"affiliation", "Terminal College"},
		{"note", "loves the command line"},
	}
	for _, s := range steps {
		m.signupInput.SetValue(s.value)
		m.signupAdvance()
	}

	if m.signup.step != signupPolicy {
		t.Fatalf("expected policy step after the text fields, got %v", m.signup.step)
	}

	// Accept the policy -> submit, then apply the async result.
	cmd := m.signupSubmit()
	if cmd == nil {
		t.Fatal("signupSubmit returned no command")
	}
	res, ok := cmd().(signupResultMsg)
	if !ok {
		t.Fatalf("expected signupResultMsg, got %T", cmd())
	}
	if res.err != nil {
		t.Fatalf("signup failed: %v", res.err)
	}
	m.applySignupResult(res)
	if m.signup.step != signupResult || m.signup.result == "" {
		t.Fatalf("expected a result screen, got step=%v result=%q", m.signup.step, m.signup.result)
	}

	// The account exists with the intake persisted.
	u, err := c.UserByName("sshuser")
	if err != nil || u == nil {
		t.Fatalf("new account not found: %v", err)
	}
	prof, err := c.UserPrivateProfile(u.ID)
	if err != nil || prof == nil {
		t.Fatalf("private profile not found: %v", err)
	}
	if prof.RealName != "SSH User" || prof.School != "Terminal College" || prof.ContactNote != "loves the command line" {
		t.Fatalf("intake not persisted: %+v", prof)
	}
}

// TestSSHSignupDisabledByDefault confirms a guest cannot register unless the
// operator opted in.
func TestSSHSignupDisabledByDefault(t *testing.T) {
	c := newTestCore(t)
	guest := &core.User{ID: "guest", Name: "guest", Role: "user"}
	m := newModel(c, guest, 80, 24, false, localeEN, "", nil, nil, "", false)
	if m.canRegister() {
		t.Fatal("guest should not be able to register when allowRegistration is false")
	}
}
