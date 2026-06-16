package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleTwoFactorGateKey drives the mandatory in-app 2FA challenge shown to a
// connected user who must pass two-factor before reaching the BBS.
func (m *model) handleTwoFactorGateKey(key string) tea.Cmd {
	switch key {
	case "esc", "ctrl+c":
		return m.quit()
	case "tab":
		if m.gateMethod == "backup" {
			m.gateMethod = "totp"
			m.gateInput.Placeholder = "123456"
		} else {
			m.gateMethod = "backup"
			m.gateInput.Placeholder = "abcd-efgh"
		}
		m.gateInput.Reset()
		m.gateErr = ""
		return nil
	case "enter":
		code := strings.TrimSpace(m.gateInput.Value())
		if code == "" {
			return nil
		}
		var err error
		if m.gateMethod == "backup" {
			err = m.c.VerifyBackupCode(m.actor.ID, code)
		} else {
			err = m.c.VerifyTOTP(m.actor.ID, code)
		}
		if err != nil {
			m.gateErr = "Invalid code — try again."
			m.gateInput.Reset()
			return nil
		}
		// Passed: drop the gate and enter the BBS.
		m.gateErr = ""
		m.gateInput.Blur()
		m.page = pageMainMenu
		m.rebuildList()
		return nil
	}
	return nil
}

func (m model) renderTwoFactorGate() string {
	var b strings.Builder
	b.WriteString(m.styled(styleTitle, "Two-factor verification") + "\n\n")
	if m.gateMethod == "backup" {
		b.WriteString("Enter one of your backup codes:\n\n")
	} else {
		b.WriteString("Enter the 6-digit code from your authenticator app:\n\n")
	}
	b.WriteString(m.gateInput.View())
	if m.gateErr != "" {
		b.WriteString("\n\n" + m.styled(styleDim, m.gateErr))
	}
	alt := "use a backup code"
	if m.gateMethod == "backup" {
		alt = "use your authenticator"
	}
	return m.renderSection("", b.String(), "enter=verify · tab="+alt+" · esc=disconnect")
}
