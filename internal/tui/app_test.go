package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/muesli/termenv"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	if strings.Count(rendered, postSepText) < 2 || !strings.Contains(rendered, postSignatureSepText) {
		t.Fatalf("expected full-width post dividers and short signature divider, got: %q", rendered)
	}
}

func TestPostSeparatorsDifferentiateFloors(t *testing.T) {
	boundaryColor := fmt.Sprint(stylePostSep.GetForeground())
	innerColor := fmt.Sprint(stylePostInnerSep.GetForeground())
	signatureColor := fmt.Sprint(stylePostSignatureSep.GetForeground())
	if boundaryColor == innerColor || boundaryColor == signatureColor || innerColor == signatureColor {
		t.Fatalf("expected post boundary, inner, and signature separators to use distinct colors")
	}

	plain := model{supportsANSI: false}
	if plain.postBoundaryLine() != plain.postInnerSepLine() {
		t.Fatalf("expected plain terminal post boundary and inner separators to fall back to identical text lines")
	}
	if lipgloss.Width(plain.postSignatureSepLine()) >= lipgloss.Width(plain.postBoundaryLine()) {
		t.Fatalf("expected signature separator to be shorter than full post separators")
	}
}

func TestRebuildPostViewShowsTitleAuthorTimeAndMetadata(t *testing.T) {
	created := time.Date(2026, time.June, 5, 9, 30, 0, 0, time.Local).UnixMilli()
	updated := time.Date(2026, time.June, 5, 10, 15, 0, 0, time.Local).UnixMilli()
	var m model
	m.vp = viewport.New(120, 20)
	m.width = 120
	m.currentBoard = "tech"
	m.boards = []core.Board{{ID: "tech", Name: "Tech Board"}}
	m.threads = []core.Thread{{ID: "thr_1", Title: "Welcome thread"}}
	m.authorNames = map[string]string{"alice": "Alicia"}
	m.posts = []core.Post{
		{
			ID:            "pst_1",
			Thread:        "thr_1",
			Author:        "bob",
			Body:          "OP body",
			ContentType:   "markup",
			Version:       2,
			ReactionCount: 3,
			Attachments:   []core.PostAttachment{{Filename: "notes.txt"}},
			CreatedSeq:    41,
			UpdatedSeq:    47,
			CreatedAt:     created,
			UpdatedAt:     updated,
			Marked:        true,
			Recommended:   true,
			NoReply:       true,
			TeX:           true,
			MailBack:      true,
			SourceTitle:   "Source thread",
		},
		{
			ID:          "pst_2",
			Thread:      "thr_1",
			Author:      "alice",
			Body:        "Reply body",
			ContentType: "reply-markup",
			ReplyTo:     "pst_1",
			CreatedSeq:  42,
			CreatedAt:   created,
		},
	}
	m.postPolls = map[string]*core.Poll{"pst_1": {ID: "poll_1"}}
	m.selectedPost = 1

	m.rebuildPostView()
	rendered := m.vp.View()
	expected := []string{
		"Title: Welcome thread",
		"Author: bob",
		"Time: 2026-06-05 09:30",
		"Seq: #41",
		"Updated: #47",
		"Version: v2",
		"Edited: 2026-06-05 10:15",
		"Type: markup",
		"♥ 3",
		"[poll]",
		"Attachments: 1",
		"Marked",
		"Recommended",
		"No reply",
		"TeX",
		"Mail back",
		"Source: Source thread",
	}
	for _, want := range expected {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected rendered post metadata to contain %q, got: %q", want, rendered)
		}
	}
	if !strings.Contains(rendered, "[1]   Author: alice (Alicia)  Time: 2026-06-05 09:30") {
		t.Fatalf("expected reply header to show only author and time, got: %q", rendered)
	}
	unexpectedReplyMetadata := []string{"[1]   Title:", "Type: reply-markup", "Reply: OP"}
	for _, unwanted := range unexpectedReplyMetadata {
		if strings.Contains(rendered, unwanted) {
			t.Fatalf("did not expect reply header metadata %q, got: %q", unwanted, rendered)
		}
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
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	view := m.View()
	if !strings.Contains(view, "Main Menu") || !strings.Contains(view, "Boards") || !strings.Contains(view, "Live Chat") {
		t.Fatalf("expected main menu view, got: %q", view)
	}
	lines := strings.Split(view, "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "BudgieBBS | alice | Main Menu") {
		t.Fatalf("expected main menu title in the top header, got: %q", view)
	}
	if strings.Count(view, "Main Menu") != 1 {
		t.Fatalf("expected main menu title to render only once, got: %q", view)
	}
	foundProfile := false
	foundOnline := false
	foundExit := false
	for _, item := range m.list.Items() {
		menuItem, ok := item.(mainMenuItem)
		if ok && menuItem.title == m.tr(msgTitleProfile) {
			foundProfile = true
		}
		if ok && menuItem.title == m.tr(msgTitleOnlineUsers) {
			foundOnline = true
		}
		if ok && menuItem.title == m.tr(msgPageExit) {
			foundExit = true
		}
	}
	if !foundProfile {
		t.Fatalf("expected main menu items to include Profile, got %#v", m.list.Items())
	}
	if !foundOnline {
		t.Fatalf("expected main menu items to include Online, got %#v", m.list.Items())
	}
	if !foundExit {
		t.Fatalf("expected main menu items to include Exit, got %#v", m.list.Items())
	}
	if !strings.Contains(view, "1-7=jump") {
		t.Fatalf("expected main menu help to include the Exit shortcut range, got: %q", view)
	}
	if strings.Contains(view, "ready") {
		t.Fatalf("did not expect default ready status line to consume space, got: %q", view)
	}
	if m.list.ShowTitle() {
		t.Fatal("expected main menu list title to be hidden because the top header owns the title")
	}
	if !strings.Contains(view, "A server-hosted campus BBS over SSH") {
		t.Fatalf("expected main menu content to keep the BBS tagline, got: %q", view)
	}
}

func TestMainMenuExitItemQuits(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()
	m.list.Select(len(m.list.Items()) - 1)

	cmd := m.handleKey(keyMsg("enter"))
	if cmd == nil {
		t.Fatal("expected Exit menu item to return a quit command")
	}
	if got := cmd(); got != tea.Quit() {
		t.Fatalf("expected Exit command to quit, got %#v", got)
	}
}

func TestStatusMessageRendersInTopHeader(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		statusMsg:    "profile saved",
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	view := m.View()
	if !strings.Contains(view, "BudgieBBS | alice | Main Menu | profile saved") {
		t.Fatalf("expected status message in top header, got: %q", view)
	}
}

func TestMainMenuProfileShortcutOpensProfileSettings(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("5"))
	if m.page != pageProfile {
		t.Fatalf("expected profile shortcut to open profile page, got %s", m.pageName(m.page))
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

func TestSSHRendererAppliesSessionColorProfile(t *testing.T) {
	c := newTestCore(t)
	actor, err := c.RegisterUser("colortester", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}
	// A renderer pinned to a 256-color profile stands in for an SSH session whose
	// client supports color, independent of the (TTY-less) test process. This is
	// what the real handler does via bubbletea.MakeRenderer(sess). Without the
	// per-session renderer, package-level styles would use the global renderer
	// (no color under a headless daemon) and emit no escapes.
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI256)
	m := newModel(c, actor, 80, 24, true, localeEN, "", nil, nil, "", false, false, r)

	if out := m.fullWidth(styleHeader, "BudgieBBS"); !strings.Contains(out, "\x1b[") {
		t.Fatalf("header should carry ANSI color from the session renderer, got %q", out)
	}
	if out := m.styled(styleAuthor, "alice"); !strings.Contains(out, "\x1b[") {
		t.Fatalf("styled() should carry ANSI color from the session renderer, got %q", out)
	}
	if line := m.postBoundaryLine(); !strings.Contains(line, "\x1b[") {
		t.Fatalf("post separator should carry ANSI color from the session renderer, got %q", line)
	}
}

func TestProfileSignatureEditSavesThroughCore(t *testing.T) {
	c := newTestCore(t)
	actor, err := c.RegisterUser("alice", "correct-horse-battery-staple")
	if err != nil {
		t.Fatal(err)
	}

	m := newModel(c, actor, 80, 24, false, localeEN, "", nil, nil, "", false, false, nil)
	m.page = pageProfile
	profile, err := c.UserProfileByName(actor.Name)
	if err != nil {
		t.Fatal(err)
	}
	m.profile = profile
	m.rebuildList()
	m.list.Select(len(m.profileFields()) - 1)
	m.openProfileField()
	if m.page != pageProfileEdit || !m.profileField.multiline {
		t.Fatalf("expected signature edit page, got page=%s field=%+v", m.pageName(m.page), m.profileField)
	}

	m.profileEditor.SetValue("sig line")
	cmd := m.submitProfileField()
	if cmd == nil {
		t.Fatal("expected save command")
	}
	updated, _ := m.Update(cmd())
	got := updated.(model)
	if got.page != pageProfile {
		t.Fatalf("expected save to return to profile page, got %s", got.pageName(got.page))
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
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("6"))
	if m.page != pageOnline {
		t.Fatalf("expected online shortcut to open online page, got %s", m.pageName(m.page))
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
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
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

func TestBoardHeaderShowsBoardTitleInBoardAndThreadViews(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageThreadList,
		currentBoard: "tech",
		list:         list.New(nil, list.NewDefaultDelegate(), 80, 20),
		boards: []core.Board{{
			ID:   "tech",
			Name: "Tech Board",
		}},
		supportsANSI: false,
	}
	m.rebuildList()

	threadListView := m.View()
	threadListLines := strings.Split(threadListView, "\n")
	if len(threadListLines) == 0 || !strings.Contains(threadListLines[0], "Tech Board") {
		t.Fatalf("expected thread list top header to show board title, got: %q", threadListView)
	}
	if strings.Count(threadListView, "Tech Board") != 1 {
		t.Fatalf("expected board title to render only once in thread list, got: %q", threadListView)
	}

	m.page = pageThread
	m.vp = viewport.New(80, 10)
	m.posts = []core.Post{{ID: "pst_1", Author: "alice", Body: "post content"}}
	m.postPolls = map[string]*core.Poll{}
	m.rebuildPostView()
	threadView := m.View()
	threadLines := strings.Split(threadView, "\n")
	if len(threadLines) == 0 || !strings.Contains(threadLines[0], "Tech Board") {
		t.Fatalf("expected reading top header to show board title, got: %q", threadView)
	}
	if strings.Count(threadView, "Tech Board") != 1 {
		t.Fatalf("expected board title to render only once in reading view, got: %q", threadView)
	}
}

func TestTopHeaderMergesSectionTitle(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageNotifications,
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	view := m.View()
	lines := strings.Split(view, "\n")
	if len(lines) == 0 || !strings.Contains(lines[0], "BudgieBBS | alice | Notifications") {
		t.Fatalf("expected notifications title in top header, got: %q", view)
	}
	if len(lines) > 1 && strings.TrimSpace(lines[1]) == "Notifications" {
		t.Fatalf("did not expect a second standalone notifications header, got: %q", view)
	}
}

func TestViewHeightFitsTerminal(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageMainMenu,
		width:        48,
		height:       10,
		statusMsg:    strings.Repeat("saved ", 20),
		list:         list.New(nil, newBBSListDelegate(nil), 48, 30),
		supportsANSI: true,
	}
	m.rebuildList()

	if got := lipgloss.Height(m.View()); got > m.height {
		t.Fatalf("expected main menu view height <= %d, got %d", m.height, got)
	}

	m.page = pageThread
	m.currentBoard = "tech"
	m.boards = []core.Board{{ID: "tech", Name: "Tech Board"}}
	m.vp = viewport.New(48, 30)
	m.vp.SetContent(strings.Repeat("post line\n", 40))
	if got := lipgloss.Height(m.View()); got > m.height {
		t.Fatalf("expected thread view height <= %d, got %d", m.height, got)
	}
}

func TestChatViewKeepsInputWithinTerminalHeight(t *testing.T) {
	ci := textinput.New()
	ci.Prompt = "> "
	ci.Focus()
	ci.SetValue("hello")
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageChat,
		width:        48,
		height:       9,
		vp:           viewport.New(48, 30),
		chatInput:    ci,
		supportsANSI: true,
	}
	m.vp.SetContent(strings.Repeat("chat line\n", 40))

	view := m.View()
	if got := lipgloss.Height(view); got > m.height {
		t.Fatalf("expected chat view height <= %d, got %d", m.height, got)
	}
	if !strings.Contains(view, "> hello") {
		t.Fatalf("expected chat input to remain visible, got: %q", view)
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
	if !strings.Contains(rendered, "0051  Next page") {
		t.Fatalf("expected thread list to use page offset in serials, got: %q", rendered)
	}
	fullView := m.View()
	if !strings.Contains(fullView, "general") {
		t.Fatalf("expected thread list view to include board header, got: %q", fullView)
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

func TestThreadReaderUpDownOpenAdjacentThreads(t *testing.T) {
	m := model{
		actor:         &core.User{Name: "alice"},
		page:          pageThread,
		currentBoard:  "general",
		currentThread: "thr_2",
		list:          list.New(nil, list.NewDefaultDelegate(), 80, 20),
		vp:            viewport.New(80, 20),
		postPolls:     map[string]*core.Poll{"pst_1": {ID: "poll_1"}},
		selectedPost:  0,
		threads: []core.Thread{
			{ID: "thr_1", Title: "First"},
			{ID: "thr_2", Title: "Second"},
			{ID: "thr_3", Title: "Third"},
		},
		supportsANSI: false,
	}

	cmd := m.handleKey(keyMsg("up"))
	if cmd == nil {
		t.Fatal("expected up in thread reader to load the previous thread")
	}
	if m.currentThread != "thr_1" {
		t.Fatalf("expected up to switch to previous thread, got %q", m.currentThread)
	}
	if m.selectedPost != -1 || len(m.posts) != 0 || len(m.postPolls) != 0 {
		t.Fatalf("expected adjacent thread navigation to clear old post state")
	}
	if !strings.Contains(m.vp.View(), "Loading thread") {
		t.Fatalf("expected loading placeholder while adjacent thread loads, got %q", m.vp.View())
	}

	cmd = m.handleKey(keyMsg("down"))
	if cmd == nil {
		t.Fatal("expected down in thread reader to load the next thread")
	}
	if m.currentThread != "thr_2" {
		t.Fatalf("expected down to switch to next thread, got %q", m.currentThread)
	}
}

func TestThreadReaderUpDownBoundaries(t *testing.T) {
	m := model{
		actor:         &core.User{Name: "alice"},
		page:          pageThread,
		currentThread: "thr_1",
		list:          list.New(nil, list.NewDefaultDelegate(), 80, 20),
		vp:            viewport.New(80, 20),
		threads: []core.Thread{
			{ID: "thr_1", Title: "First"},
		},
	}

	if cmd := m.handleKey(keyMsg("up")); cmd != nil {
		t.Fatal("did not expect up at first thread to fetch")
	}
	if m.statusMsg != "already at first thread" {
		t.Fatalf("expected first-thread boundary status, got %q", m.statusMsg)
	}
	if cmd := m.handleKey(keyMsg("down")); cmd != nil {
		t.Fatal("did not expect down at last thread to fetch")
	}
	if m.statusMsg != "already at last thread" {
		t.Fatalf("expected last-thread boundary status, got %q", m.statusMsg)
	}
}

func TestThreadReaderKeepsDedicatedScrollKeys(t *testing.T) {
	m := model{
		actor:         &core.User{Name: "alice"},
		page:          pageThread,
		currentThread: "thr_1",
		width:         80,
		height:        12,
		list:          list.New(nil, list.NewDefaultDelegate(), 80, 20),
		vp:            viewport.New(80, 4),
		threads: []core.Thread{
			{ID: "thr_1", Title: "First"},
			{ID: "thr_2", Title: "Second"},
		},
		supportsANSI: false,
	}
	m.vp.SetContent(strings.Join([]string{
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
		"line 06",
		"line 07",
		"line 08",
		"line 09",
	}, "\n"))

	updated, _ := m.Update(keyMsg("pgdown"))
	got := updated.(model)
	if got.currentThread != "thr_1" {
		t.Fatalf("expected pgdown to scroll without changing thread, got %q", got.currentThread)
	}
	if got.vp.YOffset == 0 {
		t.Fatalf("expected pgdown to scroll the thread body")
	}

	updated, _ = got.Update(keyMsg("home"))
	got = updated.(model)
	if got.vp.YOffset != 0 {
		t.Fatalf("expected home to return to top, got offset %d", got.vp.YOffset)
	}

	updated, _ = got.Update(keyMsg("ctrl+down"))
	got = updated.(model)
	if got.vp.YOffset != 1 {
		t.Fatalf("expected ctrl+down to scroll by one line, got offset %d", got.vp.YOffset)
	}

	updated, _ = got.Update(keyMsg("end"))
	got = updated.(model)
	if got.currentThread != "thr_1" {
		t.Fatalf("expected end to scroll without changing thread, got %q", got.currentThread)
	}
	if got.vp.YOffset <= 1 {
		t.Fatalf("expected end to move near the bottom, got offset %d", got.vp.YOffset)
	}

	if view := got.View(); !strings.Contains(view, "Ctrl+↑/↓=line") || !strings.Contains(view, "Space/PgDn,b/PgUp=page") {
		t.Fatalf("expected thread reader help to show scroll alternatives, got %q", view)
	}
}

func TestLeftArrowFromBoardListReturnsToMainMenu(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageBoardList,
		list:         list.New(nil, newBBSListDelegate(nil), 80, 20),
		supportsANSI: false,
	}
	m.rebuildList()

	_ = m.handleKey(keyMsg("left"))
	if m.page != pageMainMenu {
		t.Fatalf("expected left arrow from board list to return to main menu, got %s", m.pageName(m.page))
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
		t.Fatalf("expected right arrow on board list to enter thread list, got %s", m.pageName(m.page))
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
		t.Fatalf("expected right arrow on thread list to enter thread, got %s", m.pageName(m.page))
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
		t.Fatalf("expected post submission to return to thread page, got %s", got.pageName(got.page))
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
		list:         list.New(nil, newBBSListDelegate(nil), 80, sectionContentHeightFor(20)),
		currentBoard: "general",
	}
	m.titleInput.SetValue("Title")
	m.compose.SetValue("Body")

	updated, _ := m.Update(threadSubmittedMsg{board: "general", thread: "thr_1"})
	got := updated.(model)
	if got.page != pageThread {
		t.Fatalf("expected thread submission to open the new thread page, got %s", got.pageName(got.page))
	}
	if got.currentThread != "thr_1" {
		t.Fatalf("expected current thread to be set, got %q", got.currentThread)
	}
	if got.statusMsg != "thread submitted" {
		t.Fatalf("expected submitted status, got %q", got.statusMsg)
	}
}

func TestThreadSubmittedPendingStaysOnThreadList(t *testing.T) {
	m := model{
		actor:        &core.User{Name: "alice"},
		page:         pageCompose,
		pageStack:    []page{pageThreadList},
		vp:           viewport.New(80, 20),
		compose:      textarea.New(),
		titleInput:   textinput.New(),
		list:         list.New(nil, newBBSListDelegate(nil), 80, sectionContentHeightFor(20)),
		currentBoard: "general",
	}
	m.titleInput.SetValue("Title")
	m.compose.SetValue("Body")

	updated, _ := m.Update(threadSubmittedMsg{board: "general", thread: "cmd_receipt_1", queued: true})
	got := updated.(model)
	if got.page != pageThreadList {
		t.Fatalf("expected queued thread submission to return to thread list, got %s", got.pageName(got.page))
	}
	if got.currentThread == "cmd_receipt_1" {
		t.Fatalf("expected queued command receipt not to become current thread")
	}
	if got.statusMsg != "command queued" {
		t.Fatalf("expected queued status, got %q", got.statusMsg)
	}
}

func TestSubmitNewThreadPendingAckDoesNotUseReceiptAsThread(t *testing.T) {
	commandLog := core.NewBrokerCommandLog(core.NewMemoryBrokerCommandLogClient())
	c, err := core.New(t.TempDir()+"/budgie.db", core.WithAuthoritativeCommandLog(commandLog))
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	defer c.DB.Close()

	m := model{
		c:            c,
		actor:        &core.User{ID: "alice", Name: "alice"},
		currentBoard: "general",
		compose:      textarea.New(),
		titleInput:   textinput.New(),
	}
	m.titleInput.SetValue("Queued title")
	m.compose.SetValue("Queued body")

	msg := m.submitNewThread()()
	got, ok := msg.(threadSubmittedMsg)
	if !ok {
		t.Fatalf("expected threadSubmittedMsg, got %T: %#v", msg, msg)
	}
	if !got.queued {
		t.Fatalf("expected authoritative command-log reply to be queued: %#v", got)
	}
	if got.thread != "" {
		t.Fatalf("expected queued command receipt not to be treated as a thread id, got %q", got.thread)
	}
	if got.board != "general" {
		t.Fatalf("expected board to be preserved, got %q", got.board)
	}
}

func TestPendingCommandMessagesUseQueuedStatus(t *testing.T) {
	m := model{
		vp:         viewport.New(80, 20),
		compose:    textarea.New(),
		titleInput: textinput.New(),
		list:       list.New(nil, newBBSListDelegate(nil), 80, sectionContentHeightFor(20)),
	}

	updated, _ := m.Update(postSubmittedMsg{thread: "thr_1", queued: true})
	if got := updated.(model).statusMsg; got != "command queued" {
		t.Fatalf("expected queued post status, got %q", got)
	}

	updated, _ = m.Update(chatSentMsg{queued: true})
	if got := updated.(model).statusMsg; got != "command queued" {
		t.Fatalf("expected queued chat status, got %q", got)
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
	if value == "up" {
		return tea.KeyMsg{Type: tea.KeyUp}
	}
	if value == "down" {
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	if value == "pgup" {
		return tea.KeyMsg{Type: tea.KeyPgUp}
	}
	if value == "pgdown" {
		return tea.KeyMsg{Type: tea.KeyPgDown}
	}
	if value == "home" {
		return tea.KeyMsg{Type: tea.KeyHome}
	}
	if value == "end" {
		return tea.KeyMsg{Type: tea.KeyEnd}
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
