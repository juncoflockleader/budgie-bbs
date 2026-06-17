package tui

import (
	"testing"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/totp"
)

func TestTwoFactorGate(t *testing.T) {
	c := newTestCore(t)
	user, err := c.RegisterUser("alice", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	// Enroll TOTP so the gate can verify a code.
	secret, _, err := c.BeginTOTPEnrollment(user.ID, user.Name)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := totp.CodeAtTime(secret, time.Now().Unix())
	if err := c.ConfirmTOTPEnrollment(user.ID, code); err != nil {
		t.Fatal(err)
	}

	// A 2FA-required session opens on the gate, not the main menu.
	m := newModel(c, user, 80, 24, false, localeEN, "", nil, nil, "", false, true, nil)
	if m.page != pageTwoFactorGate {
		t.Fatalf("expected the session to start on the 2FA gate, got page %v", m.page)
	}

	// A wrong code keeps the gate up.
	m.gateInput.SetValue("000000")
	m.handleTwoFactorGateKey("enter")
	if m.page != pageTwoFactorGate || m.gateErr == "" {
		t.Fatalf("wrong code should keep the gate up with an error; page=%v err=%q", m.page, m.gateErr)
	}

	// A correct code drops the gate and enters the BBS.
	good, _ := totp.CodeAtTime(secret, time.Now().Unix())
	m.gateInput.SetValue(good)
	m.handleTwoFactorGateKey("enter")
	if m.page != pageMainMenu {
		t.Fatalf("correct code should enter the main menu, got page %v", m.page)
	}
}
