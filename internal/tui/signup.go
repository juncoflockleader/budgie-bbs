package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// signupStep enumerates the steps of the SSH self-registration wizard.
type signupStep int

const (
	signupUsername signupStep = iota
	signupPassword
	signupEmail
	signupRealName
	signupAffiliation
	signupNote
	signupPolicy
	signupResult
)

// isInput reports whether the step collects free text via the signup input.
func (s signupStep) isInput() bool {
	switch s {
	case signupUsername, signupPassword, signupEmail, signupRealName, signupAffiliation, signupNote:
		return true
	}
	return false
}

// optional reports whether a text step may be left blank.
func (s signupStep) optional() bool {
	switch s {
	case signupRealName, signupAffiliation, signupNote:
		return true
	}
	return false
}

// signupState holds the in-progress SSH registration wizard.
type signupState struct {
	steps []signupStep
	idx   int
	step  signupStep

	username, password, email   string
	realName, affiliation, note string
	policyText, policyVersion   string

	errMsg string
	result string // non-empty => terminal result screen
}

// signupResultMsg carries the outcome of an account-creation attempt.
type signupResultMsg struct {
	err      error
	username string
	status   string // "approved" | "pending" | "verify"
}

// canRegister reports whether the current (guest) session may self-register.
func (m model) canRegister() bool {
	return m.allowRegistration && m.actor != nil && m.actor.ID == "guest"
}

func (m *model) enterSignup() tea.Cmd {
	markdown, version := m.c.PrivacyPolicy()
	s := &signupState{policyText: markdown, policyVersion: version}
	s.steps = []signupStep{signupUsername, signupPassword}
	if m.c.EmailVerificationEnabled() {
		s.steps = append(s.steps, signupEmail)
	}
	s.steps = append(s.steps, signupRealName, signupAffiliation, signupNote)
	if m.c.PrivacyPolicyRequired() {
		s.steps = append(s.steps, signupPolicy)
	}
	s.idx = 0
	s.step = s.steps[0]
	m.signup = s
	m.pushPage(pageSignup)
	m.configureSignupInput()
	return textinput.Blink
}

// configureSignupInput prepares the input (or policy viewport) for the current step.
func (m *model) configureSignupInput() {
	s := m.signup
	if s.step == signupPolicy {
		m.signupInput.Blur()
		m.signupVP.Width = m.width
		m.signupVP.Height = m.sectionContentHeight() - 4
		if m.signupVP.Height < 3 {
			m.signupVP.Height = 3
		}
		m.signupVP.SetContent(s.policyText)
		m.signupVP.GotoTop()
		return
	}
	m.signupInput.Reset()
	m.signupInput.EchoMode = textinput.EchoNormal
	m.signupInput.CharLimit = 200
	switch s.step {
	case signupUsername:
		m.signupInput.Placeholder = "username"
	case signupPassword:
		m.signupInput.Placeholder = "password (min 8 chars)"
		m.signupInput.EchoMode = textinput.EchoPassword
	case signupEmail:
		m.signupInput.Placeholder = "you@example.com"
	case signupRealName:
		m.signupInput.Placeholder = "real name (optional)"
	case signupAffiliation:
		m.signupInput.Placeholder = "school or affiliation (optional)"
	case signupNote:
		m.signupInput.Placeholder = "reason for joining (optional)"
		m.signupInput.CharLimit = 1000
	}
	m.signupInput.Focus()
}

func (m *model) handleSignupKey(key string) tea.Cmd {
	s := m.signup
	if s == nil {
		m.exitSignup()
		return nil
	}
	switch s.step {
	case signupResult:
		m.exitSignup()
		return nil
	case signupPolicy:
		switch key {
		case "y", "Y":
			return m.signupSubmit()
		case "n", "N", "esc":
			m.exitSignup()
		}
		return nil
	default:
		switch key {
		case "esc":
			m.exitSignup()
			return nil
		case "enter":
			return m.signupAdvance()
		}
	}
	return nil
}

// signupAdvance validates the current field and moves to the next step (or submits).
func (m *model) signupAdvance() tea.Cmd {
	s := m.signup
	val := strings.TrimSpace(m.signupInput.Value())
	switch s.step {
	case signupUsername:
		if val == "" {
			s.errMsg = "Username is required."
			return nil
		}
		s.username = val
	case signupPassword:
		if len(val) < 8 {
			s.errMsg = "Password must be at least 8 characters."
			return nil
		}
		s.password = val
	case signupEmail:
		if !strings.Contains(val, "@") {
			s.errMsg = "A valid email address is required."
			return nil
		}
		s.email = val
	case signupRealName:
		s.realName = val
	case signupAffiliation:
		s.affiliation = val
	case signupNote:
		s.note = val
	}
	s.errMsg = ""
	s.idx++
	if s.idx >= len(s.steps) {
		return m.signupSubmit()
	}
	s.step = s.steps[s.idx]
	m.configureSignupInput()
	if s.step == signupPolicy {
		return nil
	}
	return textinput.Blink
}

// signupSubmit creates the account asynchronously, mirroring the HTTP handler.
func (m *model) signupSubmit() tea.Cmd {
	s := m.signup
	c := m.c
	username, password, email := s.username, s.password, s.email
	realName, affiliation, note := s.realName, s.affiliation, s.note
	policyAccepted := c.PrivacyPolicyRequired()
	policyVersion := s.policyVersion
	emailVerif := c.EmailVerificationEnabled()
	return func() tea.Msg {
		u, err := c.RegisterUser(username, password)
		if err != nil {
			return signupResultMsg{err: err}
		}
		if err := c.SaveRegistrationIntake(u.ID, core.RegistrationIntake{
			RealName:       realName,
			Affiliation:    affiliation,
			Note:           note,
			PolicyAccepted: policyAccepted,
			PolicyVersion:  policyVersion,
		}); err != nil {
			return signupResultMsg{err: err}
		}
		status := "approved"
		if u.RegistrationStatus == "pending" {
			status = "pending"
		}
		if emailVerif {
			if err := c.StartEmailVerification(u.ID, email); err != nil {
				return signupResultMsg{err: err}
			}
			if status == "approved" {
				status = "verify"
			}
		}
		return signupResultMsg{username: u.Name, status: status}
	}
}

func (m *model) applySignupResult(msg signupResultMsg) {
	s := m.signup
	if s == nil {
		return
	}
	s.step = signupResult
	if msg.err != nil {
		s.result = "Could not create account: " + msg.err.Error()
		return
	}
	switch msg.status {
	case "pending":
		s.result = "Account '" + msg.username + "' created and is awaiting administrator approval.\n" +
			"Reconnect over SSH once it is approved."
	case "verify":
		s.result = "Account '" + msg.username + "' created.\n" +
			"Check your email to verify it, then reconnect over SSH to log in."
	default:
		s.result = "Account '" + msg.username + "' created.\n" +
			"Reconnect over SSH as '" + msg.username + "' to log in."
	}
}

func (m *model) exitSignup() {
	m.signup = nil
	m.signupInput.Blur()
	if !m.popPage() {
		m.page = pageMainMenu
	}
	m.rebuildList()
}

func (m model) renderSignup() string {
	s := m.signup
	title := m.styled(styleTitle, "Create account")
	if s == nil {
		return m.renderSection("", title, "esc=cancel")
	}
	switch s.step {
	case signupResult:
		return m.renderSection("", title+"\n\n"+s.result, "press any key to return")
	case signupPolicy:
		body := title + "\n\n" + m.styled(styleTitle, "Privacy policy") + "\n\n" + m.signupVP.View()
		return m.renderSection("", body, "↑/↓ scroll · y=accept · n=cancel")
	}
	var b strings.Builder
	b.WriteString(title + "\n\n")
	b.WriteString(m.signupStepPrompt(s.step) + "\n\n")
	b.WriteString(m.signupInput.View())
	if s.errMsg != "" {
		b.WriteString("\n\n" + m.styled(styleDim, s.errMsg))
	}
	hint := "enter=continue · esc=cancel"
	if s.step.optional() {
		hint = "enter=continue (blank to skip) · esc=cancel"
	}
	return m.renderSection("", b.String(), hint)
}

func (m model) signupStepPrompt(step signupStep) string {
	switch step {
	case signupUsername:
		return "Choose a username:"
	case signupPassword:
		return "Choose a password (at least 8 characters):"
	case signupEmail:
		return "Email address (required for verification):"
	case signupRealName:
		return "Real name (optional):"
	case signupAffiliation:
		return "School or affiliation (optional):"
	case signupNote:
		return "Reason for joining (optional):"
	}
	return ""
}
