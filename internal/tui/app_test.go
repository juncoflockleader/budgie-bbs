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
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
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

func TestRebuildPostViewUsesOPAndReplyOrdinals(t *testing.T) {
	var m model
	m.vp = viewport.New(80, 20)
	m.posts = []core.Post{
		{ID: "pst_1", Author: "alice", CreatedSeq: 1, Body: "First"},
		{ID: "pst_2", Author: "bob", CreatedSeq: 2, Body: "Second"},
	}
	m.postPolls = map[string]*core.Poll{}
	m.selectedPost = 1

	m.rebuildPostView()
	rendered := m.vp.View()
	if !strings.Contains(rendered, "OP") || !strings.Contains(rendered, "[1]") {
		t.Fatalf("expected rendered posts to include OP and reply ordinals, got: %q", rendered)
	}
	if strings.Contains(rendered, "0001") || strings.Contains(rendered, "0002") {
		t.Fatalf("did not expect old zero-padded post serials, got: %q", rendered)
	}
}

func TestRebuildPostViewSeparatesMetadataBodyAndSignature(t *testing.T) {
	var m model
	m.vp = viewport.New(80, 20)
	m.posts = []core.Post{
		{ID: "pst_1", Author: "alice", CreatedSeq: 1, Body: "Post body", Signature: "sig line"},
	}
	m.postPolls = map[string]*core.Poll{}

	m.rebuildPostView()
	rendered := m.vp.View()
	if !strings.Contains(rendered, "Post body") || !strings.Contains(rendered, "sig line") {
		t.Fatalf("expected rendered post body and signature, got: %q", rendered)
	}
	if strings.Count(rendered, strings.Repeat("─", 80)) < 3 {
		t.Fatalf("expected metadata/body/signature dividers, got: %q", rendered)
	}
}

func TestHandlePostAppendedKeepsSignature(t *testing.T) {
	var m model
	m.vp = viewport.New(80, 20)
	m.currentThread = "thr_1"
	m.postReactions = map[string]bool{}
	m.postPolls = map[string]*core.Poll{}
	m.handleEvent(&proto.Event{
		Kind: proto.EvtPostAppended,
		Seq:  7,
		Payload: &proto.PostAppendedPayload{
			ID:        "pst_1",
			Thread:    "thr_1",
			Author:    "alice",
			Body:      "Post body",
			Signature: "sig line",
		},
	})

	if len(m.posts) != 1 || m.posts[0].Signature != "sig line" {
		t.Fatalf("expected appended post to retain signature, got: %#v", m.posts)
	}
}

func TestInitialTUIViewStartsAtMainMenu(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		list:         list.New(nil, newBBSListDelegate(), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	view := m.View()
	if !strings.Contains(view, "Main Menu") || !strings.Contains(view, "Boards") || !strings.Contains(view, "Live Chat") {
		t.Fatalf("expected main menu view, got: %q", view)
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

func TestThreadListPrefixesThreadSerials(t *testing.T) {
	m := model{
		actor: &core.User{Name: "alice"},
		page:  pageThreadList,
		list:  list.New(nil, list.NewDefaultDelegate(), 80, 20),
		threads: []core.Thread{
			{ID: "thr_1", Title: "Welcome", Author: "alice", PostCount: 2},
			{ID: "thr_2", Title: "Second", Author: "bob", PostCount: 1},
		},
	}
	m.rebuildList()

	rendered := m.list.View()
	if !strings.Contains(rendered, "0001  Welcome") || !strings.Contains(rendered, "0002  Second") {
		t.Fatalf("expected thread list to include serial numbers, got: %q", rendered)
	}
}

func TestLeftArrowFromBoardListReturnsToMainMenu(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageBoardList,
		list:         list.New(nil, newBBSListDelegate(), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("left"))
	if m.page != pageMainMenu {
		t.Fatalf("expected left arrow from board list to return to main menu, got %s", pageName(m.page))
	}
	if _, ok := m.list.Items()[0].(mainMenuItem); !ok {
		t.Fatalf("expected main menu items after returning from board list, got %#v", m.list.Items())
	}
}

func TestChatPageAcceptsTextInput(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageChat,
		vp:           viewport.New(80, 20),
		chatInput:    textinput.New(),
		supportsANSI: false,
	}
	m.chatInput.Focus()
	m.rebuildChatView()

	updated, _ := m.Update(keyMsg("h"))
	got := updated.(model)
	if got.chatInput.Value() != "h" {
		t.Fatalf("expected chat input to accept typed text, got %q", got.chatInput.Value())
	}
	if view := got.View(); !strings.Contains(view, "enter=send") {
		t.Fatalf("expected chat view to show send help, got: %q", view)
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
	if value == "left" {
		return tea.KeyMsg{Type: tea.KeyLeft}
	}
	if value == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
