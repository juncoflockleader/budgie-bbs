package tui

import (
	"context"
	"encoding/json"
	"fmt"
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
	foundProfile := false
	foundOnline := false
	for _, item := range m.list.Items() {
		menuItem, ok := item.(mainMenuItem)
		if ok && menuItem.title == "Profile" {
			foundProfile = true
		}
		if ok && menuItem.title == "Online" {
			foundOnline = true
		}
	}
	if !foundProfile {
		t.Fatalf("expected main menu items to include Profile, got %#v", m.list.Items())
	}
	if !foundOnline {
		t.Fatalf("expected main menu items to include Online, got %#v", m.list.Items())
	}
}

func TestMainMenuProfileShortcutOpensProfileSettings(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		list:         list.New(nil, newBBSListDelegate(), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("5"))
	if m.page != pageProfile {
		t.Fatalf("expected profile shortcut to open profile page, got %s", pageName(m.page))
	}
	if len(m.list.Items()) == 0 {
		t.Fatalf("expected profile field items")
	}
	if _, ok := m.list.Items()[0].(profileFieldItem); !ok {
		t.Fatalf("expected profile field items, got %#v", m.list.Items())
	}
	view := m.View()
	if !strings.Contains(view, "My Profile") || !strings.Contains(view, "Display name") {
		t.Fatalf("expected profile settings view, got: %q", view)
	}
}

func TestProfileSignatureEditSavesThroughCore(t *testing.T) {
	c := newTestCore(t)
	actor, err := c.RegisterUser("alice", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}

	m := newModel(c, actor, 80, 24, false)
	m.page = pageProfile
	profile, err := c.UserProfileByName(actor.Name)
	if err != nil {
		t.Fatal(err)
	}
	m.profile = profile
	m.rebuildList()
	m.list.Select(len(profileFields()) - 1)
	m.openProfileField()
	if m.page != pageProfileEdit || !m.profileField.multiline {
		t.Fatalf("expected signature edit page, got page=%s field=%+v", pageName(m.page), m.profileField)
	}

	m.profileEditor.SetValue("sig line")
	cmd := m.submitProfileField()
	if cmd == nil {
		t.Fatal("expected save command")
	}
	updated, _ := m.Update(cmd())
	got := updated.(model)
	if got.page != pageProfile {
		t.Fatalf("expected save to return to profile page, got %s", pageName(got.page))
	}
	if got.statusMsg != "profile saved" {
		t.Fatalf("expected saved status, got %q", got.statusMsg)
	}
	saved, err := c.UserProfileByName(actor.Name)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Signature != "sig line" {
		t.Fatalf("expected signature to save, got %q", saved.Signature)
	}
}

func TestMainMenuOnlineShortcutOpensOnlineUsers(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		list:         list.New(nil, newBBSListDelegate(), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("6"))
	if m.page != pageOnline {
		t.Fatalf("expected online shortcut to open online page, got %s", pageName(m.page))
	}
	view := m.View()
	if !strings.Contains(view, "Who is Online") || !strings.Contains(view, "Visible sessions") {
		t.Fatalf("expected online users view, got: %q", view)
	}
}

func TestOnlineUsersPageShowsPresenceRows(t *testing.T) {
	c := newTestCore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	alice, err := c.RegisterUser("alice", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := c.RegisterUser("bob", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(proto.SetPresencePayload{Status: "active", Mode: "reading", Board: "general", Location: "General board"})
	if err != nil {
		t.Fatal(err)
	}
	reply := c.ExecCmd(context.Background(), bob, proto.CmdSetPresence, raw, "")
	if reply.Err != nil {
		t.Fatal(fmt.Errorf("%s", reply.Err.Message))
	}

	users, err := c.ListOnlineUsers(alice.ID, "", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	m := model{
		actor:        alice,
		page:         pageOnline,
		list:         list.New(nil, newBBSListDelegate(), 80, 20),
		onlineUsers:  users,
		supportsANSI: false,
	}
	m.rebuildList()

	view := m.View()
	if !strings.Contains(view, "bob") || !strings.Contains(view, "reading") || !strings.Contains(view, "General board") {
		t.Fatalf("expected online users view to show bob's presence, got: %q", view)
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
	if !strings.Contains(threadView, "enter/→=open thread") || !strings.Contains(threadView, "n=new thread") ||
		!strings.Contains(threadView, "r=refresh") || !strings.Contains(threadView, "Ctrl+↑/↓=page") {
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

func TestThreadListSerialsUsePageOffset(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageThreadList,
		currentBoard: "general",
		threadOffset: threadPageSize,
		list:         list.New(nil, list.NewDefaultDelegate(), 80, 20),
		threads: []core.Thread{
			{ID: "thr_51", Title: "Next page", Author: "alice", PostCount: 2},
		},
	}
	m.rebuildList()

	rendered := m.list.View()
	if !strings.Contains(rendered, "0051  Next page") || !strings.Contains(rendered, "general [51-51]") {
		t.Fatalf("expected thread list to use page offset in serials and title, got: %q", rendered)
	}
}

func TestThreadListRefreshAndPaginationKeys(t *testing.T) {
	threads := make([]core.Thread, threadPageSize)
	for i := range threads {
		threads[i] = core.Thread{ID: fmt.Sprintf("thr_%d", i+1), Title: fmt.Sprintf("Thread %d", i+1)}
	}
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageThreadList,
		currentBoard: "general",
		list:         list.New(nil, list.NewDefaultDelegate(), 80, 20),
		threads:      threads,
	}

	if cmd := m.handleKey(keyMsg("r")); cmd == nil {
		t.Fatal("expected r to refresh current thread page")
	}
	if m.statusMsg != "refreshing threads" {
		t.Fatalf("expected refresh status, got %q", m.statusMsg)
	}

	if cmd := m.handleKey(keyMsg("ctrl+down")); cmd == nil {
		t.Fatal("expected ctrl+down to fetch next thread page")
	}
	if !strings.Contains(m.statusMsg, "51-100") {
		t.Fatalf("expected next-page loading status, got %q", m.statusMsg)
	}

	m.threadOffset = threadPageSize
	if cmd := m.handleKey(keyMsg("ctrl+up")); cmd == nil {
		t.Fatal("expected ctrl+up to fetch previous thread page")
	}
	if !strings.Contains(m.statusMsg, "1-50") {
		t.Fatalf("expected previous-page loading status, got %q", m.statusMsg)
	}

	m.threadOffset = 0
	if cmd := m.handleKey(keyMsg("ctrl+up")); cmd != nil {
		t.Fatal("did not expect ctrl+up to fetch before the first page")
	}
	if m.statusMsg != "already at first page" {
		t.Fatalf("expected first-page guard status, got %q", m.statusMsg)
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

func TestNewThreadTitleEnterFocusesBodyWithoutLeadingNewline(t *testing.T) {
	m := model{
		actor:              &core.User{Name: "alice"},
		page:               pageCompose,
		vp:                 viewport.New(80, 20),
		compose:            textarea.New(),
		titleInput:         textinput.New(),
		composingNewThread: true,
		supportsANSI:       false,
	}
	m.titleInput.SetValue("Title")
	m.titleInput.Focus()
	m.compose.Placeholder = "Write your post"
	m.compose.Blur()

	updated, _ := m.Update(keyMsg("enter"))
	got := updated.(model)
	if got.titleInput.Focused() {
		t.Fatalf("expected title input to blur after enter")
	}
	if !got.compose.Focused() {
		t.Fatalf("expected body input to focus after enter")
	}
	if got.compose.Value() != "" {
		t.Fatalf("expected title enter not to insert body text, got %q", got.compose.Value())
	}
	view := got.View()
	if !strings.Contains(view, "Title:") || !strings.Contains(view, "Write your post") {
		t.Fatalf("expected new-thread compose to show title and body together, got %q", view)
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
	if value == "ctrl+up" {
		return tea.KeyMsg{Type: tea.KeyCtrlUp}
	}
	if value == "ctrl+down" {
		return tea.KeyMsg{Type: tea.KeyCtrlDown}
	}
	if value == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(value)}
}
