package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/juncoflockleader/budgie-bbs/internal/core"
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

func TestRebuildPostViewMarksPollPosts(t *testing.T) {
	var m model
	m.vp = viewport.New(80, 20)
	m.posts = []core.Post{
		{ID: "pst_1", Author: "alice", CreatedSeq: 1, Body: "Thread body"},
		{ID: "pst_2", Author: "bob", CreatedSeq: 2, Body: "Reply body"},
	}
	m.postPolls = map[string]*core.Poll{
		"pst_1": {ID: "pol_1"},
	}

	m.rebuildPostView()
	rendered := m.vp.View()
	if !strings.Contains(rendered, "[poll]") {
		t.Fatalf("expected rendered post to include poll marker, got: %q", rendered)
	}
}

func TestRebuildPostViewNoPollMarkerForNoPollPost(t *testing.T) {
	var m model
	m.vp = viewport.New(80, 20)
	m.posts = []core.Post{
		{ID: "pst_1", Author: "alice", CreatedSeq: 1, Body: "Plain body"},
	}
	m.postPolls = map[string]*core.Poll{}

	m.rebuildPostView()
	rendered := m.vp.View()
	if strings.Contains(rendered, "[poll]") {
		t.Fatalf("did not expect poll marker for poll-less post, got: %q", rendered)
	}
}
