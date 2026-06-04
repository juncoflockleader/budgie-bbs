package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
)

func TestInsertPollTemplate(t *testing.T) {
	var m model
	m.compose = textarea.New()
	m.compose.SetValue("")
	m.insertPollTemplate()

	got := m.compose.Value()
	want := "[poll]\nQuestion\nOption 1\nOption 2\n[/poll]"
	if got != want {
		t.Fatalf("expected blank compose template %q, got %q", want, got)
	}

	m.compose.SetValue("before")
	m.insertPollTemplate()
	want = "before\n\n[poll]\nQuestion\nOption 1\nOption 2\n[/poll]"
	if got = m.compose.Value(); got != want {
		t.Fatalf("expected appended template with blank line %q, got %q", want, got)
	}

	m.compose.SetValue("before\n")
	m.insertPollTemplate()
	want = "before\n\n[poll]\nQuestion\nOption 1\nOption 2\n[/poll]"
	if got = m.compose.Value(); got != want {
		t.Fatalf("expected appended template after newline %q, got %q", want, got)
	}
}

func TestInsertPollTemplateWithExpires(t *testing.T) {
	var m model
	m.compose = textarea.New()
	m.compose.SetValue("draft")
	m.insertPollTemplateWithExpires("1d")

	got := m.compose.Value()
	want := "draft\n\n[poll expires=1d]\nQuestion\nOption 1\nOption 2\n[/poll]"
	if got != want {
		t.Fatalf("expected template with expiry %q, got %q", want, got)
	}

	m.compose.SetValue("")
	m.insertPollTemplateWithExpires("")
	want = "[poll]\nQuestion\nOption 1\nOption 2\n[/poll]"
	if got = m.compose.Value(); got != want {
		t.Fatalf("expected fallback to default template %q, got %q", want, got)
	}
}
