package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
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

func TestBoardAndThreadListViewsShowKeyHints(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageBoardList,
		list:         list.New(nil, list.NewDefaultDelegate(), 80, 20),
		supportsANSI: false,
	}
	m.list.SetShowHelp(false)

	boardView := m.View()
	if !strings.Contains(boardView, "enter/→=open board") || !strings.Contains(boardView, "q=quit") {
		t.Fatalf("expected board list key hints, got: %q", boardView)
	}

	m.page = pageThreadList
	threadView := m.View()
	if !strings.Contains(threadView, "enter/→=open thread") || !strings.Contains(threadView, "n=new thread") {
		t.Fatalf("expected thread list key hints, got: %q", threadView)
	}
}

func TestRightArrowOpensBoardAndThreadListItems(t *testing.T) {
	m := model{
		actor: &core.User{Name: "alice"},
		page:  pageBoardList,
		list:  list.New(nil, list.NewDefaultDelegate(), 80, 20),
		boards: []core.Board{{
			ID:   "general",
			Name: "General",
		}},
		threads: []core.Thread{{
			ID:    "thr_1",
			Board: "general",
			Title: "Welcome",
		}},
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("right"))
	if m.page != pageThreadList {
		t.Fatalf("expected right arrow on board list to enter thread list, got %s", pageName(m.page))
	}
	if m.currentBoard != "general" {
		t.Fatalf("expected current board general, got %q", m.currentBoard)
	}

	m.threads = []core.Thread{{
		ID:    "thr_1",
		Board: "general",
		Title: "Welcome",
	}}
	m.rebuildList()
	_ = m.handleKey(keyMsg("right"))
	if m.page != pageThread {
		t.Fatalf("expected right arrow on thread list to enter thread, got %s", pageName(m.page))
	}
	if m.currentThread != "thr_1" {
		t.Fatalf("expected current thread thr_1, got %q", m.currentThread)
	}
}

func TestPostSubmittedReturnsFromComposeToThread(t *testing.T) {
	m := model{
		actor:         &core.User{Name: "alice"},
		page:          pageCompose,
		pageStack:     []page{pageThread},
		vp:            viewport.New(80, 20),
		compose:       textarea.New(),
		titleInput:    textinput.New(),
		currentThread: "thr_1",
	}
	m.compose.SetValue("draft body")

	updated, _ := m.Update(postSubmittedMsg{thread: "thr_1"})
	got := updated.(model)
	if got.page != pageThread {
		t.Fatalf("expected post submission to return to thread page, got %s", pageName(got.page))
	}
	if got.compose.Value() != "" {
		t.Fatalf("expected compose body to reset after submit, got %q", got.compose.Value())
	}
	if got.statusMsg != "post submitted" {
		t.Fatalf("expected submitted status, got %q", got.statusMsg)
	}
}

func TestThreadSubmittedReturnsFromComposeToNewThread(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageCompose,
		pageStack:    []page{pageThreadList},
		vp:           viewport.New(80, 20),
		compose:      textarea.New(),
		titleInput:   textinput.New(),
		currentBoard: "general",
	}
	m.titleInput.SetValue("Title")
	m.compose.SetValue("Body")

	updated, _ := m.Update(threadSubmittedMsg{board: "general", thread: "thr_1"})
	got := updated.(model)
	if got.page != pageThread {
		t.Fatalf("expected thread submission to open the new thread page, got %s", pageName(got.page))
	}
	if got.currentThread != "thr_1" {
		t.Fatalf("expected current thread to be set, got %q", got.currentThread)
	}
	if got.statusMsg != "thread submitted" {
		t.Fatalf("expected submitted status, got %q", got.statusMsg)
	}
}

func TestNormalizeKeyStringMakesControlKeysCaseInsensitive(t *testing.T) {
	if got := normalizeKeyString("ctrl+S"); got != "ctrl+s" {
		t.Fatalf("expected ctrl+S to normalize to ctrl+s, got %q", got)
	}
	if got := normalizeKeyString("N"); got != "N" {
		t.Fatalf("expected non-control key case to be preserved, got %q", got)
	}
}

func keyMsg(value string) tea.KeyMsg {
	if value == "right" {
		return tea.KeyMsg{Type: tea.KeyRight}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
