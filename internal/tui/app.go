package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/commandexec"
	"github.com/juncoflockleader/budgie-bbs/internal/core/doormodel"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/core/sitemodel"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// page identifies which TUI screen is active.
type page int

const (
	pageMainMenu page = iota
	pageBoardList
	pageThreadList
	pageThread
	pagePoll
	pageCompose
	pageChat
	pageSearch
	pageNotifications
	pageProfile
	pageProfileEdit
	pageOnline
	pageNodeSpy
	pageDoorsMenu
	pageSignup
	pageTwoFactorGate
	pageMUD
)

const threadPageSize = 50

const sectionChromeLines = 4 // global header, panel borders, help line

// msg types for the bubbletea update cycle.
type (
	eventMsg   struct{ evt *proto.Event }
	errMsg     struct{ err error }
	boardsMsg  struct{ boards []core.Board }
	threadsMsg struct {
		board   string
		offset  int
		threads []core.Thread
	}
	postsMsg     struct{ posts []core.Post }
	postPollsMsg struct {
		thread string
		polls  map[string]*projections.Poll
		err    error
	}
	chatLinesMsg struct {
		lines []core.ChatLine
		err   error
	}
	pollMsg struct {
		postID string
		poll   *projections.Poll
		open   bool
		err    error
	}
	searchMsg        struct{ posts []core.Post }
	notificationsMsg struct {
		notifications []projections.Notification
		unread        int
	}
	notificationStatusMsg struct{ unread int }
	profileMsg            struct {
		profile *projections.UserProfile
		err     error
	}
	profileSavedMsg struct {
		profile *projections.UserProfile
		err     error
	}
	onlineUsersMsg struct {
		users []core.SocialUser
		err   error
	}
	presenceSetMsg   struct{ err error }
	disconnectMsg    struct{}
	sysopMsgMsg      struct{ msg string }
	nodeListMsg      struct{ nodes []core.NodeEntry }
	doorExitedMsg    struct{ err error }
	commandQueuedMsg struct{}
	postSubmittedMsg struct {
		thread string
		queued bool
	}
	chatSentMsg        struct{ queued bool }
	threadSubmittedMsg struct {
		board  string
		thread string
		queued bool
	}
	pollPermissionMsg struct {
		canCreatePoll bool
		err           error
	}
)

// model is the root bubbletea model.
type model struct {
	c      *core.Core
	actor  *core.User
	sub    *core.Subscription
	locale localeCode

	// M14: node registry handles for this SSH session.
	nodeID string        // empty in unit tests
	msgCh  <-chan string // receives sysop messages; nil if not registered

	page      page
	pageStack []page // navigation history for back/esc
	width     int
	height    int
	statusMsg string

	currentBoard  string
	currentThread string
	threadOffset  int

	// Component state.
	list          list.Model
	vp            viewport.Model
	compose       textarea.Model
	titleInput    textinput.Model
	chatInput     textinput.Model
	profileInput  textinput.Model
	profileEditor textarea.Model
	searchQuery   string

	// MUD ("The Town"): a shared multi-user text world. mudRoom is the current
	// room snapshot; mudLog is the scrolling activity feed; mudRoomScope tracks
	// the room scope currently subscribed (so it can be swapped on movement).
	mudInput     textinput.Model
	mudRoom      *proto.MUDRoomView
	mudLog       []string
	mudRoomScope string

	// Compose mode: true = creating new thread, false = replying
	composingNewThread bool

	// In-memory state.
	boards        []core.Board
	threads       []core.Thread
	posts         []core.Post
	postReactions map[string]bool
	selectedPost  int
	postPolls     map[string]*projections.Poll
	currentPoll   string // postID currently shown in pagePoll
	chat          []chatLine
	canCreatePoll bool
	trustLoaded   bool
	// Notifications tracked in the current actor session.
	notifications []projections.Notification
	unreadNotifs  int
	profile       *projections.UserProfile
	profileField  profileField
	onlineUsers   []core.SocialUser
	nodes         []core.NodeEntry       // M14: active SSH sessions for sysop panel
	doors         []doormodel.DoorConfig // M12: configured door games
	termName      string                 // M12: TERM env value forwarded to door processes
	authorNames   map[string]string
	supportsANSI  bool
	// r is the per-session lipgloss renderer. Styles are package-level templates
	// built with the global renderer (whose color profile reflects the *server*
	// process — no color under a headless daemon); rebinding each style to r via
	// rstyle makes it render with the SSH *session's* detected color profile.
	r *lipgloss.Renderer

	// appearance is the admin-configured site branding + main-menu layout,
	// snapshotted at login. nil falls back to built-in defaults.
	appearance *core.SiteAppearance

	// SSH self-registration (opt-in via -allow-ssh-registration).
	allowRegistration bool
	signup            *signupState
	signupInput       textinput.Model
	signupVP          viewport.Model

	// In-app 2FA gate: shown first when the connected user must pass 2FA.
	requires2FA bool
	gateInput   textinput.Model
	gateMethod  string // "totp" | "backup"
	gateErr     string
}

type chatLine struct {
	user string
	text string
	ts   int64
}

// Styles.
var (
	postSepText           = strings.Repeat("─", 80)
	postSignatureSepText  = strings.Repeat("─", 24)
	styleTitle            = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	styleDim              = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleHeader           = lipgloss.NewStyle().Background(lipgloss.Color("19")).Foreground(lipgloss.Color("229")).Bold(true).Padding(0, 1)
	styleHelp             = lipgloss.NewStyle().Background(lipgloss.Color("18")).Foreground(lipgloss.Color("159")).Padding(0, 1)
	stylePanel            = lipgloss.NewStyle().Border(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("26")).Padding(0, 1)
	stylePostSep          = lipgloss.NewStyle().Foreground(lipgloss.Color("24"))
	stylePostInnerSep     = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	stylePostSignatureSep = lipgloss.NewStyle().Foreground(lipgloss.Color("30"))
	styleAuthor           = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("32"))
	styleRedacted         = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	styleChatUser         = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("178"))
)

// rstyle rebinds a package-level style template to this session's renderer so it
// renders with the SSH client's color profile rather than the server process's.
func (m model) rstyle(style lipgloss.Style) lipgloss.Style {
	if m.r == nil {
		return style
	}
	return style.Renderer(m.r)
}

func (m *model) styled(style lipgloss.Style, value string) string {
	if m == nil {
		return style.Render(value)
	}
	if m.supportsANSI {
		return m.rstyle(style).Render(value)
	}
	return value
}

func (m *model) postSepLine() string {
	return m.postBoundaryLine()
}

func (m *model) postBoundaryLine() string {
	if m == nil {
		return stylePostSep.Render(postSepText)
	}
	if m.supportsANSI {
		return m.rstyle(stylePostSep).Render(postSepText)
	}
	return postSepText
}

func (m *model) postInnerSepLine() string {
	if m == nil {
		return stylePostInnerSep.Render(postSepText)
	}
	if m.supportsANSI {
		return m.rstyle(stylePostInnerSep).Render(postSepText)
	}
	return postSepText
}

func (m *model) postSignatureSepLine() string {
	if m == nil {
		return stylePostSignatureSep.Render(postSignatureSepText)
	}
	if m.supportsANSI {
		return m.rstyle(stylePostSignatureSep).Render(postSignatureSepText)
	}
	return postSignatureSepText
}

func newModel(c *core.Core, actor *core.User, width, height int, supportsANSI bool, locale localeCode, nodeID string, msgCh <-chan string, doors []doormodel.DoorConfig, termName string, allowRegistration bool, requires2FA bool, renderer *lipgloss.Renderer) model {
	if renderer == nil {
		// Unit tests and any non-SSH caller fall back to the global renderer.
		renderer = lipgloss.DefaultRenderer()
	}
	scopes := []string{"board:general", "chat:lobby", "presence:global"}
	if actor != nil && actor.IsAdmin() {
		scopes = append(scopes, "system:nodes")
	}
	sub := c.Subscribe(scopes)
	appearance, _ := c.SiteAppearance() // nil on error -> defaults at render time
	threadTitlePlaceholder := trLocale(locale, msgPlaceholderThreadTitle)
	chatMessagePlaceholder := trLocale(locale, msgPlaceholderChatMessage)
	composeBodyPlaceholder := trLocale(locale, msgPlaceholderComposeBody)

	ti := textinput.New()
	ti.Placeholder = threadTitlePlaceholder
	ti.CharLimit = 200

	ci := textinput.New()
	ci.Placeholder = chatMessagePlaceholder
	ci.Prompt = "> "
	ci.CharLimit = 1000
	ci.Width = width - 4

	mi := textinput.New()
	mi.Placeholder = "look, north, say hi, who, help…"
	mi.Prompt = "› "
	mi.CharLimit = 400
	mi.Width = width - 4

	pi := textinput.New()
	pi.CharLimit = 1000
	pi.Width = width - 4

	m := model{
		c:                 c,
		actor:             actor,
		sub:               sub,
		nodeID:            nodeID,
		msgCh:             msgCh,
		page:              pageMainMenu,
		width:             width,
		height:            height,
		titleInput:        ti,
		chatInput:         ci,
		mudInput:          mi,
		profileInput:      pi,
		selectedPost:      -1,
		postReactions:     make(map[string]bool),
		postPolls:         make(map[string]*projections.Poll),
		authorNames:       make(map[string]string),
		supportsANSI:      supportsANSI,
		r:                 renderer,
		appearance:        appearance,
		locale:            locale,
		doors:             doors,
		termName:          termName,
		allowRegistration: allowRegistration,
		requires2FA:       requires2FA,
		gateMethod:        "totp",
	}

	m.list = list.New(nil, newBBSListDelegate(renderer), width, sectionContentHeightFor(height))
	m.list.SetShowHelp(false)
	m.list.SetShowStatusBar(false)
	m.list.SetFilteringEnabled(false)
	m.list.SetShowTitle(false)

	m.vp = viewport.New(width, sectionContentHeightFor(height))
	m.vp.Style = lipgloss.NewStyle()

	si := textinput.New()
	si.CharLimit = 200
	si.Width = width - 4
	m.signupInput = si
	m.signupVP = viewport.New(width, sectionContentHeightFor(height))
	m.signupVP.Style = lipgloss.NewStyle()

	gi := textinput.New()
	gi.CharLimit = 32
	gi.Width = width - 4
	gi.Placeholder = "123456"
	m.gateInput = gi
	if requires2FA {
		m.page = pageTwoFactorGate
		m.gateInput.Focus()
	}

	m.compose = textarea.New()
	m.compose.Placeholder = composeBodyPlaceholder
	m.compose.SetWidth(width - 4)
	m.compose.SetHeight(height / 3)
	m.profileEditor = textarea.New()
	m.profileEditor.SetWidth(width - 4)
	m.profileEditor.SetHeight(height / 3)
	m.rebuildList()

	return m
}

func newBBSListDelegate(r *lipgloss.Renderer) list.DefaultDelegate {
	delegate := list.NewDefaultDelegate()
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.Foreground(lipgloss.Color("252"))
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.Foreground(lipgloss.Color("109"))
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("231")).
		Background(lipgloss.Color("25")).
		Bold(true).
		Reverse(true).
		Padding(0, 1)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("159")).
		Background(lipgloss.Color("24")).
		Reverse(true).
		Padding(0, 1)
	// Rebind every delegate style to the per-session renderer so list items keep
	// their colors under a headless daemon (whose global renderer is no-color).
	if r != nil {
		s := &delegate.Styles
		s.NormalTitle = s.NormalTitle.Renderer(r)
		s.NormalDesc = s.NormalDesc.Renderer(r)
		s.SelectedTitle = s.SelectedTitle.Renderer(r)
		s.SelectedDesc = s.SelectedDesc.Renderer(r)
		s.DimmedTitle = s.DimmedTitle.Renderer(r)
		s.DimmedDesc = s.DimmedDesc.Renderer(r)
		s.FilterMatch = s.FilterMatch.Renderer(r)
	}
	return delegate
}

// pushPage saves the current page on the stack and navigates to p.
func (m *model) pushPage(p page) {
	m.pageStack = append(m.pageStack, m.page)
	m.page = p
}

// popPage returns to the previous page. Returns false if already at root.
func (m *model) popPage() bool {
	if len(m.pageStack) == 0 {
		return false
	}
	n := len(m.pageStack)
	m.page = m.pageStack[n-1]
	m.pageStack = m.pageStack[:n-1]
	return true
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.fetchBoards(),
		m.fetchNotificationStatus(),
		m.fetchPollPermission(),
		m.setPresence("active", "tui", "", "", m.tr(msgTitleMainMenu)),
		m.awaitEvent(),
	}
	if m.msgCh != nil {
		cmds = append(cmds, m.awaitSysopMsg())
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, m.sectionContentHeight())
		m.vp.Width = msg.Width
		m.vp.Height = m.sectionContentHeight()
		m.compose.SetWidth(msg.Width - 4)
		m.compose.SetHeight(msg.Height / 3)
		m.titleInput.Width = msg.Width - 4
		m.chatInput.Width = msg.Width - 4
		m.mudInput.Width = msg.Width - 4
		m.profileInput.Width = msg.Width - 4
		m.profileEditor.SetWidth(msg.Width - 4)
		m.profileEditor.SetHeight(msg.Height / 3)
		m.signupInput.Width = msg.Width - 4
		m.signupVP.Width = msg.Width

	case boardsMsg:
		m.boards = msg.boards
		m.rebuildList()

	case threadsMsg:
		if msg.board != "" && m.currentBoard != "" && msg.board != m.currentBoard {
			return m, nil
		}
		pageChanged := msg.offset != m.threadOffset
		m.threadOffset = msg.offset
		m.threads = msg.threads
		m.rebuildList()
		if pageChanged {
			m.list.Select(0)
		}

	case postsMsg:
		m.posts = msg.posts
		m.hydratePostAuthorNames(m.posts)
		if err := m.hydrateReactionState(); err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": err.Error()})
			return m, nil
		}
		m.postPolls = make(map[string]*projections.Poll)
		if len(m.posts) == 0 {
			m.selectedPost = -1
		} else {
			m.selectedPost = len(m.posts) - 1
		}
		m.rebuildPostView()
		if len(m.posts) > 0 {
			cmds = append(cmds, m.fetchPollsForPosts(m.posts))
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case postPollsMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			return m, nil
		}
		if msg.thread != m.currentThread {
			return m, nil
		}
		m.postPolls = msg.polls
		if m.postPolls == nil {
			m.postPolls = make(map[string]*projections.Poll)
		}
		m.rebuildPostView()

	case chatLinesMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			return m, nil
		}
		m.chat = make([]chatLine, 0, len(msg.lines))
		for _, line := range msg.lines {
			ts := line.TS
			if ts == 0 {
				ts = line.CreatedAt
			}
			m.chat = append(m.chat, chatLine{user: line.User, text: line.Text, ts: ts})
		}
		m.rebuildChatView()
		m.vp.GotoBottom()

	case pollMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			return m, nil
		}
		if msg.poll == nil {
			m.statusMsg = m.tr(msgStatusPollNoActive)
			return m, nil
		}
		m.postPolls[msg.postID] = msg.poll
		if msg.open {
			m.currentPoll = msg.postID
			m.pushPage(pagePoll)
		}
		return m, nil

	case searchMsg:
		m.rebuildSearchView(msg.posts)

	case notificationsMsg:
		m.notifications = msg.notifications
		m.unreadNotifs = msg.unread
		if m.page == pageNotifications {
			m.rebuildList()
		}

	case notificationStatusMsg:
		m.unreadNotifs = msg.unread

	case profileMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			return m, nil
		}
		m.profile = msg.profile
		m.cacheProfileDisplayName(msg.profile)
		if m.page == pageProfile {
			m.rebuildList()
		}

	case profileSavedMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			return m, nil
		}
		m.profile = msg.profile
		m.cacheProfileDisplayName(msg.profile)
		m.statusMsg = m.tr(msgStatusProfileSaved)
		if m.page == pageProfileEdit {
			m.popPage()
		}
		if m.page == pageProfile {
			m.rebuildList()
		}

	case signupResultMsg:
		m.applySignupResult(msg)
		return m, nil

	case onlineUsersMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			return m, nil
		}
		m.onlineUsers = msg.users
		if m.page == pageOnline {
			m.rebuildList()
		}

	case presenceSetMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusPresence, map[string]string{"message": msg.err.Error()})
		}

	case eventMsg:
		cmds = append(cmds, m.awaitEvent()) // re-arm listener
		cmds = append(cmds, m.handleEvent(msg.evt)...)

	case disconnectMsg:
		m.statusMsg = m.tr(msgStatusDisconnected)

	case sysopMsgMsg:
		// Sysop message arrives from the node registry channel; show it in the
		// status bar and re-arm the listener.
		m.statusMsg = "[sysop] " + msg.msg
		if m.msgCh != nil {
			cmds = append(cmds, m.awaitSysopMsg())
		}

	case nodeListMsg:
		m.nodes = msg.nodes
		if m.page == pageNodeSpy {
			m.rebuildList()
		}

	case doorExitedMsg:
		if msg.err != nil {
			m.statusMsg = "door exited with error: " + msg.err.Error()
		} else {
			m.statusMsg = "door session ended"
		}
		// Return to the doors menu so user can launch another or go back.
		if m.page != pageDoorsMenu {
			m.pushPage(pageDoorsMenu)
			m.rebuildList()
		}

	case commandQueuedMsg:
		m.statusMsg = m.tr(msgStatusCommandQueued)

	case postSubmittedMsg:
		m.finishCompose()
		if m.page == pageCompose {
			m.popPage()
		}
		if msg.thread != "" && !msg.queued {
			m.currentThread = msg.thread
			cmds = append(cmds, m.fetchPosts(msg.thread))
		}
		if msg.queued {
			m.statusMsg = m.tr(msgStatusCommandQueued)
		} else {
			m.statusMsg = m.tr(msgStatusPostSubmitted)
			cmds = append(cmds, m.fetchNotificationStatus())
		}

	case chatSentMsg:
		if msg.queued {
			m.statusMsg = m.tr(msgStatusCommandQueued)
		} else {
			m.statusMsg = m.tr(msgStatusChatSent)
		}

	case threadSubmittedMsg:
		m.finishCompose()
		if m.page == pageCompose {
			m.popPage()
		}
		if msg.board != "" {
			m.currentBoard = msg.board
			m.threadOffset = 0
			if !msg.queued {
				cmds = append(cmds, m.fetchThreads(msg.board, m.threadOffset))
			}
		}
		if msg.thread != "" && !msg.queued {
			m.currentThread = msg.thread
			m.posts = nil
			m.pushPage(pageThread)
			cmds = append(cmds, m.fetchPosts(msg.thread), m.resubscribeThread(msg.thread))
		}
		if msg.queued {
			m.statusMsg = m.tr(msgStatusCommandQueued)
		} else {
			m.statusMsg = m.tr(msgStatusThreadSubmitted)
			cmds = append(cmds, m.fetchNotificationStatus())
		}

	case errMsg:
		m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})

	case pollPermissionMsg:
		if msg.err != nil {
			m.statusMsg = m.tr(msgStatusError, map[string]string{"message": msg.err.Error()})
			m.canCreatePoll = false
			m.trustLoaded = true
			return m, nil
		}
		m.canCreatePoll = msg.canCreatePoll
		m.trustLoaded = true

	case tea.KeyMsg:
		// Capture the page BEFORE handleKey might change it, so the component
		// dispatch below uses the correct component for this key event.
		activePage := m.page
		key := keyString(msg)
		titleFocusNavigation := activePage == pageCompose &&
			m.composingNewThread &&
			m.titleInput.Focused() &&
			(key == "enter" || key == "tab" || key == "ctrl+s")
		cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if titleFocusNavigation {
			return m, tea.Batch(cmds...)
		}

		// Delegate to the component that was active when the key arrived.
		switch activePage {
		case pageMainMenu, pageBoardList, pageThreadList, pageNotifications, pageProfile, pageOnline, pageNodeSpy, pageDoorsMenu:
			var c tea.Cmd
			m.list, c = m.list.Update(msg)
			cmds = append(cmds, c)
		case pageThread, pagePoll, pageSearch:
			var c tea.Cmd
			m.vp, c = m.vp.Update(msg)
			cmds = append(cmds, c)
		case pageChat:
			var inputCmd tea.Cmd
			m.chatInput, inputCmd = m.chatInput.Update(msg)
			cmds = append(cmds, inputCmd)
			var viewportCmd tea.Cmd
			m.vp, viewportCmd = m.vp.Update(msg)
			cmds = append(cmds, viewportCmd)
		case pageMUD:
			var inputCmd tea.Cmd
			m.mudInput, inputCmd = m.mudInput.Update(msg)
			cmds = append(cmds, inputCmd)
		case pageCompose:
			if m.composingNewThread && m.titleInput.Focused() {
				var c tea.Cmd
				m.titleInput, c = m.titleInput.Update(msg)
				cmds = append(cmds, c)
			} else {
				var c tea.Cmd
				m.compose, c = m.compose.Update(msg)
				cmds = append(cmds, c)
			}
		case pageProfileEdit:
			if m.profileField.multiline {
				var c tea.Cmd
				m.profileEditor, c = m.profileEditor.Update(msg)
				cmds = append(cmds, c)
			} else {
				var c tea.Cmd
				m.profileInput, c = m.profileInput.Update(msg)
				cmds = append(cmds, c)
			}
		case pageSignup:
			if m.signup != nil && m.signup.step == signupPolicy {
				var c tea.Cmd
				m.signupVP, c = m.signupVP.Update(msg)
				cmds = append(cmds, c)
			} else if m.signup != nil && m.signup.step.isInput() {
				var c tea.Cmd
				m.signupInput, c = m.signupInput.Update(msg)
				cmds = append(cmds, c)
			}
		case pageTwoFactorGate:
			var c tea.Cmd
			m.gateInput, c = m.gateInput.Update(msg)
			cmds = append(cmds, c)
		}
		return m, tea.Batch(cmds...)
	}

	// Non-key messages also need component updates.
	switch m.page {
	case pageMainMenu, pageBoardList, pageThreadList, pageProfile, pageOnline, pageNodeSpy, pageDoorsMenu:
		var c tea.Cmd
		m.list, c = m.list.Update(msg)
		cmds = append(cmds, c)
	case pageThread, pagePoll, pageChat, pageSearch:
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		cmds = append(cmds, c)
	case pageNotifications:
		var c tea.Cmd
		m.list, c = m.list.Update(msg)
		cmds = append(cmds, c)
	case pageCompose:
		if m.composingNewThread && m.titleInput.Focused() {
			var c tea.Cmd
			m.titleInput, c = m.titleInput.Update(msg)
			cmds = append(cmds, c)
		} else {
			var c tea.Cmd
			m.compose, c = m.compose.Update(msg)
			cmds = append(cmds, c)
		}
	case pageProfileEdit:
		if m.profileField.multiline {
			var c tea.Cmd
			m.profileEditor, c = m.profileEditor.Update(msg)
			cmds = append(cmds, c)
		} else {
			var c tea.Cmd
			m.profileInput, c = m.profileInput.Update(msg)
			cmds = append(cmds, c)
		}
	}

	// M14: keep the node registry location in sync after each update.
	if m.c != nil && m.nodeID != "" {
		m.c.Nodes.UpdateLocation(m.nodeID, m.pageName(m.page))
	}

	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := keyString(msg)
	// Global: cycle language without leaving the current page.
	if key == "ctrl+l" {
		m.cycleLocale()
		return nil
	}
	switch m.page {
	case pageMainMenu:
		switch key {
		case "enter", " ", "right":
			return m.openMainMenuSelection()
		case "1":
			m.page = pageBoardList
			m.rebuildList()
		case "2":
			return m.enterChat()
		case "3":
			m.pushPage(pageNotifications)
			m.notifications = nil
			m.rebuildList()
			return m.fetchNotifications()
		case "4", "/":
			m.pushPage(pageSearch)
			m.searchQuery = ""
			m.vp.SetContent(m.styled(styleDim, m.tr(msgPlaceholderSearchPrompt)))
		case "5", "p":
			return m.enterProfile()
		case "6", "o":
			return m.enterOnline()
		case "7":
			return m.enterMUD()
		case "8":
			return m.quit()
		case "c":
			if m.canRegister() {
				return m.enterSignup()
			}
		case "q", "ctrl+c":
			return m.quit()
		}

	case pageSignup:
		return m.handleSignupKey(key)

	case pageTwoFactorGate:
		return m.handleTwoFactorGateKey(key)

	case pageBoardList:
		switch key {
		case "enter", " ", "right":
			if b := m.selectedBoard(); b != nil {
				m.currentBoard = b.ID
				m.threadOffset = 0
				m.pushPage(pageThreadList)
				m.threads = nil
				m.rebuildList()
				return m.fetchThreads(b.ID, m.threadOffset)
			}
		case "c":
			return m.enterChat()
		case "N":
			m.pushPage(pageNotifications)
			m.notifications = nil
			m.rebuildList()
			return m.fetchNotifications()
		case "S":
			// M14: sysop node spy panel — admin only.
			if m.actor != nil && m.actor.IsAdmin() {
				return m.enterNodeSpy()
			}
		case "d":
			// M12: door games — only shown when doors are configured.
			if len(m.doors) > 0 {
				return m.enterDoorsMenu()
			}
		case "/":
			m.pushPage(pageSearch)
			m.searchQuery = ""
			m.vp.SetContent(m.styled(styleDim, m.tr(msgPlaceholderSearchPrompt)))
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageThreadList:
		switch key {
		case "enter", " ", "right":
			if t := m.selectedThread(); t != nil {
				m.pushPage(pageThread)
				return m.openThread(*t)
			}
		case "r":
			if m.currentBoard == "" {
				m.statusMsg = m.tr(msgStatusNoBoardSelected)
				return nil
			}
			m.statusMsg = m.tr(msgStatusRefreshing)
			return m.fetchThreads(m.currentBoard, m.threadOffset)
		case "ctrl+up":
			if m.currentBoard == "" {
				m.statusMsg = m.tr(msgStatusNoBoardSelected)
				return nil
			}
			if m.threadOffset == 0 {
				m.statusMsg = m.tr(msgStatusFirstPage)
				return nil
			}
			nextOffset := m.threadOffset - threadPageSize
			if nextOffset < 0 {
				nextOffset = 0
			}
			m.statusMsg = m.tr(msgStatusLoadingThreads, map[string]string{"from": fmt.Sprintf("%d", nextOffset+1), "to": fmt.Sprintf("%d", nextOffset+threadPageSize)})
			return m.fetchThreads(m.currentBoard, nextOffset)
		case "ctrl+down":
			if m.currentBoard == "" {
				m.statusMsg = m.tr(msgStatusNoBoardSelected)
				return nil
			}
			if len(m.threads) < threadPageSize {
				m.statusMsg = m.tr(msgStatusLastPage)
				return nil
			}
			nextOffset := m.threadOffset + threadPageSize
			m.statusMsg = m.tr(msgStatusLoadingThreads, map[string]string{"from": fmt.Sprintf("%d", nextOffset+1), "to": fmt.Sprintf("%d", nextOffset+threadPageSize)})
			return m.fetchThreads(m.currentBoard, nextOffset)
		case "n":
			m.composingNewThread = true
			m.titleInput.Reset()
			m.titleInput.Focus()
			m.compose.Reset()
			m.compose.Blur()
			m.pushPage(pageCompose)
		case "/":
			m.pushPage(pageSearch)
			m.searchQuery = ""
			m.vp.SetContent(m.styled(styleDim, m.tr(msgPlaceholderSearchPrompt)))
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageThread:
		switch key {
		case "k", "up":
			return m.navigateAdjacentThread(-1)
		case "j", "down":
			return m.navigateAdjacentThread(1)
		case "ctrl+up":
			m.vp.LineUp(1)
		case "ctrl+down":
			m.vp.LineDown(1)
		case "home":
			m.vp.GotoTop()
		case "end":
			m.vp.GotoBottom()
		case "n":
			m.composingNewThread = false
			m.compose.Reset()
			m.compose.Focus()
			m.pushPage(pageCompose)
		case "r":
			postID := m.selectedPostID()
			if postID == "" {
				m.statusMsg = m.tr(msgStatusNoPostToReact)
				return nil
			}
			if m.postReactions[postID] {
				return m.unreactPost(postID)
			}
			return m.reactPost(postID)
		case "L":
			if m.actor.IsMod() {
				return m.toggleThreadLock()
			}
		case "N":
			m.pushPage(pageNotifications)
			m.notifications = nil
			m.rebuildList()
			return m.fetchNotifications()
		case "p":
			postID := m.selectedPostID()
			if postID == "" {
				m.statusMsg = m.tr(msgStatusNoPostToMark)
				return nil
			}
			return m.openPoll(postID)
		case "P":
			postID := m.selectedPostID()
			if postID == "" {
				m.statusMsg = m.tr(msgStatusNoPostToMark)
				return nil
			}
			return m.openPoll(postID)
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
			return m.fetchThreads(m.currentBoard, m.threadOffset)
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pagePoll:
		switch key {
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(key[0]-'0') - 1
			poll := m.currentPollData()
			if poll == nil {
				m.statusMsg = m.tr(msgStatusChatNotFound)
				return nil
			}
			expired := poll.ExpiresAt != 0 && poll.ExpiresAt < time.Now().UnixMilli()
			if expired || poll.Voted != "" {
				status := m.tr(msgListOpen)
				if poll.Voted != "" {
					status = m.tr(msgStatusPollVotedOrClosed)
				} else {
					status = m.tr(msgStatusPollClosed)
				}
				if poll.Voted != "" {
					status = m.tr(msgStatusPollVotedOrClosed)
				}
				if expired {
					status = m.tr(msgStatusPollClosed)
				}
				m.statusMsg = m.tr(msgStatusPollVotedOrClosed, map[string]string{"status": status})
				return nil
			}
			if idx < 0 || idx >= len(poll.Options) {
				return nil
			}
			return m.votePoll(poll.ID, poll.Options[idx].ID, m.currentPoll)
		}

	case pageCompose:
		switch key {
		case "ctrl+p":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = m.tr(msgStatusPollNeedTrust)
				} else {
					m.statusMsg = m.tr(msgStatusPollChecking)
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = m.tr(msgStatusFocusBodyFirst)
				return nil
			}
			m.insertPollTemplate()
			return nil
		case "ctrl+e":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = m.tr(msgStatusPollNeedTrust)
				} else {
					m.statusMsg = m.tr(msgStatusPollChecking)
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = m.tr(msgStatusFocusBodyFirst)
				return nil
			}
			m.insertPollTemplateWithExpires("1h")
			return nil
		case "ctrl+d":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = m.tr(msgStatusPollNeedTrust)
				} else {
					m.statusMsg = m.tr(msgStatusPollChecking)
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = m.tr(msgStatusFocusBodyFirst)
				return nil
			}
			m.insertPollTemplateWithExpires("1d")
			return nil
		case "ctrl+w":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = m.tr(msgStatusPollNeedTrust)
				} else {
					m.statusMsg = m.tr(msgStatusPollChecking)
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = m.tr(msgStatusFocusBodyFirst)
				return nil
			}
			m.insertPollTemplateWithExpires("1w")
			return nil
		case "ctrl+s":
			if m.composingNewThread {
				// If title field still focused, move to body first.
				if m.titleInput.Focused() {
					title := strings.TrimSpace(m.titleInput.Value())
					if title == "" {
						m.statusMsg = m.tr(msgStatusTitleRequired)
						return nil
					}
					m.titleInput.Blur()
					m.compose.Focus()
					return nil
				}
				return m.submitNewThread()
			}
			body := strings.TrimSpace(m.compose.Value())
			if body == "" {
				return nil
			}
			return m.submitPost(body)
		case "tab", "enter":
			// In new-thread mode, tab/enter on the title field moves to body.
			if m.composingNewThread && m.titleInput.Focused() {
				title := strings.TrimSpace(m.titleInput.Value())
				if title == "" {
					return nil
				}
				m.titleInput.Blur()
				m.compose.Focus()
				return nil
			}
		case "esc":
			m.compose.Blur()
			m.titleInput.Blur()
			m.popPage()
		}

	case pageChat:
		switch key {
		case "enter":
			text := strings.TrimSpace(m.chatInput.Value())
			if text == "" {
				return nil
			}
			m.chatInput.Reset()
			m.statusMsg = m.tr(msgStatusSendingChat)
			return m.sendChatLine(text)
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageMUD:
		// Only intercept submit / leave / hard-quit; every other key (including
		// letters like 'q') must reach the command input.
		switch key {
		case "enter":
			line := strings.TrimSpace(m.mudInput.Value())
			if line == "" {
				return nil
			}
			m.mudInput.Reset()
			return m.sendMUDCommand(line)
		case "esc":
			cmd := m.leaveMUD()
			if !m.popPage() {
				m.page = pageMainMenu
			}
			return cmd
		case "ctrl+c":
			m.leaveMUD()
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageSearch:
		switch key {
		case "enter":
			if m.searchQuery != "" {
				return m.runSearch(m.searchQuery)
			}
		case "backspace":
			if len(m.searchQuery) > 0 {
				m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				m.vp.SetContent(m.styled(styleTitle, m.tr(msgPlaceholderSearchInput)) + m.searchQuery + "▌")
			}
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
		default:
			rawKey := msg.String()
			if len(rawKey) == 1 {
				m.searchQuery += rawKey
				m.vp.SetContent(m.styled(styleTitle, m.tr(msgPlaceholderSearchInput)) + m.searchQuery + "▌")
			}
		}
	case pageNotifications:
		switch key {
		case "enter", "right":
			selection := m.selectedNotification()
			if selection == nil || selection.Read {
				return nil
			}
			if err := m.c.MarkNotificationRead(selection.ID, m.actor.ID); err != nil {
				m.statusMsg = m.tr(msgStatusFailedRead)
				return func() tea.Msg { return errMsg{err} }
			}
			for i := range m.notifications {
				if m.notifications[i].ID == selection.ID {
					m.notifications[i].Read = true
					if m.unreadNotifs > 0 {
						m.unreadNotifs--
					}
					break
				}
			}
			m.rebuildList()
			m.statusMsg = m.tr(msgStatusNotificationMarked)
		case "a":
			if err := m.c.MarkAllNotificationsRead(m.actor.ID); err != nil {
				m.statusMsg = m.tr(msgStatusFailedReadAll)
				return func() tea.Msg { return errMsg{err} }
			}
			for i := range m.notifications {
				m.notifications[i].Read = true
			}
			m.unreadNotifs = 0
			m.rebuildList()
		case "d":
			selection := m.selectedNotification()
			if selection == nil {
				return nil
			}
			if err := m.c.DeleteNotification(selection.ID, m.actor.ID); err != nil {
				m.statusMsg = m.tr(msgStatusFailedDelete)
				return func() tea.Msg { return errMsg{err} }
			}
			next := m.notifications[:0]
			for _, n := range m.notifications {
				if n.ID == selection.ID {
					if !n.Read && m.unreadNotifs > 0 {
						m.unreadNotifs--
					}
					continue
				}
				next = append(next, n)
			}
			m.notifications = next
			m.rebuildList()
			m.statusMsg = m.tr(msgStatusDeleted)
		case "x":
			if err := m.c.DeleteReadNotifications(m.actor.ID); err != nil {
				m.statusMsg = m.tr(msgStatusFailedClearRead)
				return func() tea.Msg { return errMsg{err} }
			}
			next := m.notifications[:0]
			for _, n := range m.notifications {
				if !n.Read {
					next = append(next, n)
				}
			}
			m.notifications = next
			m.rebuildList()
			m.statusMsg = m.tr(msgStatusClearRead)
		case "c":
			if err := m.c.DeleteAllNotifications(m.actor.ID); err != nil {
				m.statusMsg = m.tr(msgStatusFailedClearAll)
				return func() tea.Msg { return errMsg{err} }
			}
			m.notifications = nil
			m.unreadNotifs = 0
			m.rebuildList()
			m.statusMsg = m.tr(msgStatusClearedAll)
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
			m.rebuildList()
		}

	case pageProfile:
		switch key {
		case "enter", " ", "right":
			m.openProfileField()
		case "r":
			return m.fetchProfile()
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageProfileEdit:
		switch key {
		case "ctrl+s":
			return m.submitProfileField()
		case "esc":
			m.profileInput.Blur()
			m.profileEditor.Blur()
			if !m.popPage() {
				m.page = pageProfile
			}
			if m.page == pageProfile {
				m.rebuildList()
			}
		}

	case pageOnline:
		switch key {
		case "r":
			return m.fetchOnlineUsers()
		case "enter", " ", "right":
			user := m.selectedOnlineUser()
			if user == nil {
				return nil
			}
			m.statusMsg = m.onlineUserStatus(*user)
		case "esc", "left":
			if !m.popPage() {
				m.page = pageMainMenu
			}
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageNodeSpy:
		switch key {
		case "r":
			return m.fetchNodes()
		case "k":
			// Kick selected node (admin only).
			if n := m.selectedNode(); n != nil && m.c != nil {
				if err := m.c.KickNode(n.NodeID); err != nil {
					m.statusMsg = "kick failed: " + err.Error()
				} else {
					m.statusMsg = "kicked node " + n.NodeID
					return m.fetchNodes()
				}
			}
		case "m":
			// Send message to selected node — prompt via status bar input.
			if n := m.selectedNode(); n != nil {
				m.statusMsg = "send message to " + n.Username + " (use /msg <text>)"
			}
		case "esc", "left":
			if !m.popPage() {
				m.page = pageBoardList
			}
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageDoorsMenu:
		switch key {
		case "enter", " ", "right":
			if d := m.selectedDoor(); d != nil {
				return m.launchDoor(*d)
			}
		case "esc", "left":
			if !m.popPage() {
				m.page = pageBoardList
			}
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}
	}
	return nil
}

func (m *model) handleEvent(evt *proto.Event) []tea.Cmd {
	var cmds []tea.Cmd

	switch evt.Kind {
	case proto.EvtPostAppended:
		p, ok := evt.Payload.(*proto.PostAppendedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		if p.Thread == m.currentThread {
			post := core.Post{
				ID:            p.ID,
				Thread:        p.Thread,
				Author:        p.Author,
				AuthorID:      p.AuthorID,
				Body:          p.Body,
				Signature:     p.Signature,
				ContentType:   p.ContentType,
				ReplyTo:       p.ReplyTo,
				ReactionCount: 0,
				CreatedSeq:    evt.Seq,
				UpdatedSeq:    evt.Seq,
				CreatedAt:     p.TS,
				UpdatedAt:     p.TS,
			}
			m.posts = append(m.posts, post)
			m.hydratePostAuthorNames([]core.Post{post})
			m.postReactions[p.ID] = false
			m.selectedPost = len(m.posts) - 1
			m.rebuildPostView()
			m.vp.GotoBottom()
			cmds = append(cmds, m.fetchPollsForPosts([]core.Post{{ID: p.ID}}))
		}
		for i, t := range m.threads {
			if t.ID == p.Thread {
				m.threads[i].PostCount++
				m.threads[i].LastSeq = evt.Seq
			}
		}
		cmds = append(cmds, m.fetchNotificationStatus())
		if m.page == pageNotifications {
			cmds = append(cmds, m.fetchNotifications())
		}

	case proto.EvtChatLine:
		p, ok := evt.Payload.(*proto.ChatLinePayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		m.chat = append(m.chat, chatLine{user: p.User, text: p.Text, ts: p.TS})
		if len(m.chat) > 200 {
			m.chat = m.chat[len(m.chat)-200:]
		}
		if m.page == pageChat {
			m.rebuildChatView()
			m.vp.GotoBottom()
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtMUDRoom:
		if p, ok := evt.Payload.(*proto.MUDRoomEventPayload); ok {
			m.mudAppend(p.Text)
		}

	case proto.EvtMUDView:
		if v, ok := evt.Payload.(*proto.MUDViewPayload); ok && !v.Left {
			if v.Room != nil {
				m.applyMUDRoom(v.Room)
			}
			for _, ln := range v.Lines {
				m.mudAppend(ln)
			}
		}

	case proto.EvtThreadNew:
		if m.page == pageThreadList {
			cmds = append(cmds, m.fetchThreads(m.currentBoard, m.threadOffset))
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtPostEdited:
		p, ok := evt.Payload.(*proto.PostEditedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		for i, post := range m.posts {
			if post.ID == p.ID {
				m.posts[i].Body = p.NewBody
				m.posts[i].Version = p.Version
				m.posts[i].UpdatedSeq = evt.Seq
			}
		}
		if m.page == pageThread {
			m.rebuildPostView()
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtPostRedacted:
		p, ok := evt.Payload.(*proto.PostRedactedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		for i, post := range m.posts {
			if post.ID == p.ID {
				m.posts[i].Redacted = true
			}
		}
		if m.page == pageThread {
			m.rebuildPostView()
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtPostReacted:
		p, ok := evt.Payload.(*proto.PostReactedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		for i, post := range m.posts {
			if post.ID == p.PostID {
				m.posts[i].ReactionCount = p.ReactionCount
			}
		}
		m.postReactions[p.PostID] = p.User == m.actor.Name
		if m.page == pageThread {
			m.rebuildPostView()
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtPostUnreacted:
		p, ok := evt.Payload.(*proto.PostUnreactedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		for i, post := range m.posts {
			if post.ID == p.PostID {
				m.posts[i].ReactionCount = p.ReactionCount
			}
		}
		m.postReactions[p.PostID] = p.User == m.actor.Name
		if m.page == pageThread {
			m.rebuildPostView()
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtPollVoted:
		p, ok := evt.Payload.(*proto.PollVotedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		if postID := m.pollPostIDForPoll(p.Poll); postID != "" {
			cmds = append(cmds, m.fetchPoll(postID, false))
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtThreadLocked:
		p, ok := evt.Payload.(*proto.ThreadLockedPayload)
		if !ok {
			return []tea.Cmd{m.fetchNotificationStatus()}
		}
		for i, t := range m.threads {
			if t.ID == p.Thread {
				m.threads[i].Locked = p.Locked
			}
		}
		if m.page == pageThread {
			action := m.tr(msgStatusLocked)
			if !p.Locked {
				action = m.tr(msgStatusUnlocked)
			}
			m.statusMsg = m.tr(msgStatusThreadAction, map[string]string{"action": action, "by": p.By})
		}
		cmds = append(cmds, m.fetchNotificationStatus())

	case proto.EvtPresenceUpdate:
		if m.page == pageOnline {
			cmds = append(cmds, m.fetchOnlineUsers())
		}

	case proto.EvtNodeConnected, proto.EvtNodeDisconnected:
		// Refresh the node list whenever the sysop panel is open.
		if m.page == pageNodeSpy {
			cmds = append(cmds, m.fetchNodes())
		}
	}

	if cmds == nil {
		cmds = []tea.Cmd{m.fetchNotificationStatus()}
	}
	return cmds
}

func (m model) View() string {
	headerLabel := m.tr(msgHeaderFormat, map[string]string{
		"app":   m.tr(msgHeaderAppName),
		"user":  m.actor.Name,
		"title": m.headerTitle(),
	})
	if m.unreadNotifs > 0 {
		headerLabel += m.tr(msgHeaderUnread, map[string]string{"count": fmt.Sprintf("%d", m.unreadNotifs)})
	}
	if strings.TrimSpace(m.statusMsg) != "" {
		headerLabel += m.tr(msgHeaderStatus, map[string]string{"status": strings.TrimSpace(m.statusMsg)})
	}
	header := m.fullWidth(styleHeader, headerLabel)

	var body string
	switch m.page {
	case pageMainMenu:
		mainHelp := m.tr(msgHelpMainMenu)
		if m.canRegister() {
			mainHelp += "  c=register"
		}
		body = m.renderSection(m.tr(msgTitleMainMenu), m.renderMainMenu(), mainHelp)
	case pageBoardList:
		boardHelp := m.tr(msgHelpBoardList)
		if len(m.doors) > 0 {
			boardHelp += "  d=doors"
		}
		if m.actor != nil && m.actor.IsAdmin() {
			boardHelp += "  S=node-spy"
		}
		body = m.renderSection(m.tr(msgTitleBoards), m.list.View(), boardHelp)
	case pageThreadList:
		body = m.renderSection(m.boardTitleOrFallback(), m.list.View(), m.tr(msgHelpThreadList))
	case pageNotifications:
		body = m.renderSection(m.tr(msgTitleNotifications), m.list.View(), m.tr(msgHelpNotifications))
	case pageProfile:
		body = m.renderSection(m.tr(msgTitleProfile), m.renderProfileSettings(), m.tr(msgHelpProfile))
	case pageProfileEdit:
		body = m.renderSection(m.profileEditorHeader(), m.renderProfileEditor(), m.tr(msgHelpProfileEdit))
	case pageOnline:
		body = m.renderSection(m.tr(msgTitleOnlineUsers), m.renderOnlineUsers(), m.tr(msgHelpOnline))
	case pageNodeSpy:
		body = m.renderSection("Node Spy", m.renderNodeSpy(), "[r]efresh  [k]ick  [esc]back")
	case pageDoorsMenu:
		body = m.renderSection("Doors", m.list.View(), "enter=launch  esc/←=back")
	case pageSignup:
		body = m.renderSignup()
	case pageTwoFactorGate:
		body = m.renderTwoFactorGate()
	case pageThread:
		help := m.tr(msgHelpThreadReader)
		if m.actor.IsMod() {
			help = m.tr(msgHelpThreadReaderMod)
		}
		body = m.renderSection(m.boardTitleOrFallback(), m.vp.View(), help)
	case pagePoll:
		poll := m.currentPollData()
		if poll == nil {
			body = m.styled(styleDim, m.tr(msgTitleNoPoll))
		} else {
			help := m.tr(msgHelpPoll)
			if poll.ExpiresAt > 0 && poll.ExpiresAt < time.Now().UnixMilli() {
				help = m.tr(msgHelpPollClosed)
			}
			if poll.Voted != "" {
				help = m.tr(msgHelpPollVoted)
			}
			total := 0
			for _, opt := range poll.Options {
				total += opt.VoteCount
			}
			var b strings.Builder
			if poll.Question != "" {
				b.WriteString(m.styled(styleTitle, poll.Question) + "\n\n")
			}
			for i, option := range poll.Options {
				if i >= 9 {
					break
				}
				pct := 0
				if total > 0 {
					pct = (option.VoteCount * 100) / total
				}
				voteState := ""
				if poll.Voted == option.ID {
					voteState = " ✓"
				}
				line := fmt.Sprintf("%d) %s%s", i+1, option.Text, voteState)
				if poll.Voted != "" || (poll.ExpiresAt > 0 && poll.ExpiresAt < time.Now().UnixMilli()) {
					line = fmt.Sprintf("%s - %d (%d%%)", line, option.VoteCount, pct)
				}
				b.WriteString(line + "\n")
			}
			expires := m.tr(msgListOpen)
			if poll.ExpiresAt > 0 {
				if poll.ExpiresAt < time.Now().UnixMilli() {
					expires = m.tr(msgListCloses)
				} else {
					expires = m.tr(msgListClosesAt, map[string]string{"time": time.UnixMilli(poll.ExpiresAt).Format("2006-01-02 15:04")})
				}
			}
			plural := m.tr(msgCommonVoteSuffix)
			if total != 1 {
				plural = m.tr(msgCommonVotePluralSuffix)
			}
			b.WriteString("\n" + m.styled(styleDim, m.tr(msgListReplyVoteVotes, map[string]string{
				"count":  fmt.Sprintf("%d", total),
				"plural": plural,
			})+" · "+expires) + "\n")
			body = m.renderSection(m.tr(msgTitlePoll), b.String(), help)
		}
		if body == "" {
			body = m.styled(styleDim, m.tr(msgTitleNoPoll))
		}
	case pageCompose:
		if m.composingNewThread {
			titleSection := m.styled(styleDim, m.tr(msgComposeTitlePrefix)) + m.titleInput.View() + "\n\n" +
				m.compose.View()
			if m.titleInput.Focused() {
				body = m.renderSection(m.tr(msgTitleNewThread), titleSection, m.tr(msgHelpNewThread))
			} else {
				body = m.renderSection(m.tr(msgTitleNewThread), titleSection, m.composeHelpLine())
			}
		} else {
			body = m.renderSection(m.tr(msgTitleNewReply), m.compose.View(), m.composeHelpLine())
		}
	case pageChat:
		body = m.renderSection(m.tr(msgTitleLiveChat), m.chatSectionContent(), m.tr(msgHelpChat))
	case pageMUD:
		body = m.renderSection("The Town", m.mudSectionContent(), "type a command · 'help' · esc=leave")
	case pageSearch:
		body = m.renderSection(m.tr(msgTitleSearch), m.vp.View(), m.tr(msgHelpSearch))
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body)
}

func (m model) renderMainMenu() string {
	layout := sitemodel.DefaultTUIMainMenuLayout()
	if m.appearance != nil && m.appearance.MainMenuLayout != nil && len(m.appearance.MainMenuLayout.Blocks) > 0 {
		layout = *m.appearance.MainMenuLayout
	}
	// Inner content width (panel border + padding) and the height budget for the
	// section. Art wider than the terminal is skipped so narrow clients degrade
	// gracefully instead of wrapping (which would overflow the section).
	innerWidth := m.panelWidth() - 4
	if innerWidth < 1 {
		innerWidth = m.panelWidth()
	}
	avail := m.sectionContentHeight()

	parts := make([]string, 0, len(layout.Blocks))
	menuPos := -1
	for _, blk := range layout.Blocks {
		switch strings.ToLower(strings.TrimSpace(blk.Type)) {
		case "menu":
			menuPos = len(parts)
			parts = append(parts, "") // placeholder; sized + filled below
		case "spacer":
			n := blk.Lines
			if n < 1 {
				n = 1
			}
			parts = append(parts, strings.Repeat("\n", n-1))
		case "text":
			text := blk.Text
			if strings.TrimSpace(text) == "" {
				text = m.mainMenuTagline()
			}
			text = truncateDisplayWidth(text, innerWidth)
			parts = append(parts, m.placeMenuBlock(m.colorizeBlock(text, blk.Color, true), blk.Align, innerWidth))
		case "art":
			art := strings.TrimRight(blk.Art, "\n")
			if s := strings.TrimSpace(blk.Stock); s != "" {
				art = sitemodel.StockTUIArt(s)
			}
			if strings.TrimSpace(art) == "" {
				continue
			}
			if innerWidth > 0 && lipgloss.Width(art) > innerWidth {
				continue // too wide for this terminal; degrade gracefully
			}
			parts = append(parts, m.placeMenuBlock(m.colorizeBlock(art, blk.Color, false), blk.Align, innerWidth))
		}
	}

	// Size the menu list to the height left over after the decorative blocks so
	// the whole composition fits the section content area.
	if menuPos >= 0 {
		decoLines := 0
		for i, p := range parts {
			if i != menuPos {
				decoLines += lipgloss.Height(p)
			}
		}
		menuHeight := avail - decoLines
		if menuHeight < 1 {
			menuHeight = 1
		}
		m.list.SetHeight(menuHeight)
		parts[menuPos] = m.list.View()
	}
	return strings.Join(parts, "\n")
}

// mainMenuTagline returns the admin tagline if set, else the built-in default.
func (m model) mainMenuTagline() string {
	if m.appearance != nil && strings.TrimSpace(m.appearance.Tagline) != "" {
		return strings.TrimSpace(m.appearance.Tagline)
	}
	return m.tr(msgTagline)
}

// colorizeBlock applies an optional per-block hex color (truecolor) to art/text,
// honoring the ANSI capability gate. When no color is given, text blocks render
// dim and art renders in the terminal's default foreground.
func (m model) colorizeBlock(content, hexColor string, dimWhenPlain bool) string {
	if !m.supportsANSI {
		return content
	}
	if c := strings.TrimSpace(hexColor); c != "" {
		return m.rstyle(lipgloss.NewStyle().Foreground(lipgloss.Color(c))).Render(content)
	}
	if dimWhenPlain {
		return m.styled(styleDim, content)
	}
	return content
}

// placeMenuBlock horizontally aligns a (possibly multi-line) block within width,
// preserving the block's internal shape.
func (m model) placeMenuBlock(content, align string, width int) string {
	if width <= 0 {
		return content
	}
	pos := lipgloss.Left
	switch strings.ToLower(strings.TrimSpace(align)) {
	case "center":
		pos = lipgloss.Center
	case "right":
		pos = lipgloss.Right
	}
	if pos == lipgloss.Left {
		return content
	}
	return lipgloss.PlaceHorizontal(width, pos, content)
}

func (m model) renderProfileSettings() string {
	var b strings.Builder
	name := ""
	role := ""
	if m.actor != nil {
		name = m.actor.Name
		role = m.actor.Role
	}
	if role == "" {
		role = "user"
	}
	displayName := m.profileFieldValue(profileField{key: "displayName"})
	if strings.TrimSpace(displayName) == "" {
		displayName = name
	}
	b.WriteString(fmt.Sprintf("%s %s\n", m.styled(styleDim, m.tr(msgLabelUser)), name))
	b.WriteString(fmt.Sprintf("%s %s\n", m.styled(styleDim, m.tr(msgLabelDisplay)), displayName))
	b.WriteString(fmt.Sprintf("%s %s", m.styled(styleDim, m.tr(msgLabelRole)), role))
	if m.profile != nil {
		b.WriteString(fmt.Sprintf("  %s %d  %s %d",
			m.styled(styleDim, m.tr(msgLabelTrust)), m.profile.TrustLevel,
			m.styled(styleDim, m.tr(msgLabelPosts)), m.profile.PostsCreated,
		))
	} else {
		b.WriteString("  " + m.styled(styleDim, m.tr(msgLabelLoading)))
	}
	b.WriteString("\n\n")
	b.WriteString(m.list.View())
	return b.String()
}

func (m model) renderProfileEditor() string {
	field := m.profileField
	if field.key == "" {
		return m.styled(styleDim, m.tr(msgLabelNoProfileField))
	}
	var b strings.Builder
	if field.desc != "" {
		b.WriteString(m.styled(styleDim, field.desc) + "\n")
	}
	b.WriteString("\n")
	if field.multiline {
		b.WriteString(m.profileEditor.View())
	} else {
		b.WriteString(m.profileInput.View())
	}
	return b.String()
}

func (m model) renderOnlineUsers() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %d\n\n", m.styled(styleDim, m.tr(msgLabelVisible)), len(m.onlineUsers)))
	if len(m.onlineUsers) == 0 {
		b.WriteString(m.styled(styleDim, m.tr(msgOnlineNoUsers)) + "\n")
		return b.String()
	}
	b.WriteString(m.list.View())
	return b.String()
}

func (m model) profileFieldValue(field profileField) string {
	if m.profile == nil {
		if field.key == "displayName" && m.actor != nil {
			return m.actor.Name
		}
		return ""
	}
	switch field.key {
	case "displayName":
		if strings.TrimSpace(m.profile.DisplayName) == "" && m.actor != nil {
			return m.actor.Name
		}
		return m.profile.DisplayName
	case "title":
		return m.profile.Title
	case "bio":
		return m.profile.Bio
	case "avatar":
		return m.profile.Avatar
	case "signature":
		return m.profile.Signature
	case "plan":
		return m.profile.Plan
	case "homepage":
		return m.profile.Homepage
	default:
		return ""
	}
}

func (m model) profileWithField(field profileField, value string) projections.UserProfile {
	var next projections.UserProfile
	if m.profile != nil {
		next = *m.profile
	}
	if m.actor != nil {
		next.ID = m.actor.ID
		next.Name = m.actor.Name
		next.Role = m.actor.Role
	}
	value = strings.TrimSpace(value)
	switch field.key {
	case "displayName":
		if value == "" && m.actor != nil {
			value = m.actor.Name
		}
		next.DisplayName = value
	case "title":
		next.Title = value
	case "bio":
		next.Bio = value
	case "avatar":
		next.Avatar = value
	case "signature":
		next.Signature = value
	case "plan":
		next.Plan = value
	case "homepage":
		next.Homepage = value
	}
	return next
}

func (m model) renderPanel(content string) string {
	content = limitLines(content, m.sectionContentHeight())
	if !m.supportsANSI {
		return content
	}
	return m.rstyle(stylePanel).Width(m.panelWidth() - 2).Render(content)
}

func (m model) renderSection(_ string, content, help string) string {
	return m.renderPanel(content) + "\n" + m.helpLine(help)
}

func (m model) helpLine(help string) string {
	return m.fullWidth(styleHelp, help)
}

func (m model) fullWidth(style lipgloss.Style, value string) string {
	if !m.supportsANSI {
		return value
	}
	width := m.panelWidth()
	return m.rstyle(style).Width(width).Render(truncateDisplayWidth(value, width-2))
}

func (m model) panelWidth() int {
	if m.width <= 4 {
		return 80
	}
	return m.width
}

func (m model) sectionContentHeight() int {
	return sectionContentHeightFor(m.height)
}

func sectionContentHeightFor(height int) int {
	if height <= 0 {
		height = 24
	}
	contentHeight := height - sectionChromeLines
	if contentHeight < 1 {
		return 1
	}
	return contentHeight
}

func limitLines(content string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	return strings.Join(lines[:maxLines], "\n")
}

func (m model) chatSectionContent() string {
	input := m.chatInput.View()
	historyLines := m.sectionContentHeight() - 2
	if historyLines < 1 {
		return input
	}
	return limitLines(m.vp.View(), historyLines) + "\n\n" + input
}

func (m model) boardTitleOrFallback() string {
	title := m.boardTitle()
	if title == "" {
		title = m.currentBoard
	}
	if title == "" {
		title = m.tr(msgTitleBoardFallback)
	}
	return title
}

func (m model) profileEditorHeader() string {
	if strings.TrimSpace(m.profileField.label) == "" {
		return m.tr(msgProfileSettingsTitle)
	}
	return m.tr(msgComposeTitlePrefix) + m.profileField.label
}

func (m model) headerTitle() string {
	switch m.page {
	case pageMainMenu:
		return m.tr(msgTitleMainMenu)
	case pageBoardList:
		return m.tr(msgTitleBoards)
	case pageThreadList, pageThread:
		return m.boardTitleOrFallback()
	case pageNotifications:
		return m.tr(msgTitleNotifications)
	case pageProfile:
		return m.tr(msgTitleProfile)
	case pageProfileEdit:
		return m.profileEditorHeader()
	case pageOnline:
		return m.tr(msgTitleOnlineUsers)
	case pagePoll:
		return m.tr(msgTitlePoll)
	case pageCompose:
		if m.composingNewThread {
			return m.tr(msgTitleNewThread)
		}
		return m.tr(msgTitleNewReply)
	case pageChat:
		return m.tr(msgTitleLiveChat)
	case pageMUD:
		return "The Town"
	case pageSearch:
		return m.tr(msgTitleSearch)
	default:
		return m.pageName(m.page)
	}
}

func truncateDisplayWidth(value string, maxWidth int) string {
	if maxWidth <= 0 || lipgloss.Width(value) <= maxWidth {
		return value
	}
	var b strings.Builder
	width := 0
	for _, r := range value {
		rw := lipgloss.Width(string(r))
		if width+rw > maxWidth {
			break
		}
		b.WriteRune(r)
		width += rw
	}
	return b.String()
}

func (m model) boardTitle() string {
	boardID := strings.TrimSpace(m.currentBoard)
	if boardID == "" {
		for _, post := range m.posts {
			if name := strings.TrimSpace(post.BoardName); name != "" {
				return name
			}
			if id := strings.TrimSpace(post.Board); id != "" {
				boardID = id
				break
			}
		}
	}
	if boardID == "" {
		return ""
	}
	for _, board := range m.boards {
		if board.ID == boardID {
			if name := strings.TrimSpace(board.Name); name != "" {
				return name
			}
			return board.ID
		}
	}
	return boardID
}

// --- List helpers ---

type mainMenuItem struct {
	key   string
	title string
	desc  string
}

func (i mainMenuItem) Title() string {
	return fmt.Sprintf("%s) %s", i.key, i.title)
}

func (i mainMenuItem) Description() string { return i.desc }

func (i mainMenuItem) FilterValue() string { return i.title }

type boardItem struct{ b core.Board }

func (i boardItem) Title() string       { return i.b.Name }
func (i boardItem) Description() string { return i.b.Description }
func (i boardItem) FilterValue() string { return i.b.Name }

type threadItem struct {
	index int
	t     core.Thread
	desc  string
}

func (i threadItem) Title() string {
	if i.index > 0 {
		return fmt.Sprintf("%04d  %s", i.index, i.t.Title)
	}
	return i.t.Title
}
func (i threadItem) Description() string {
	return i.desc
}
func (i threadItem) FilterValue() string { return i.t.Title }

type notificationItem struct {
	n     projections.Notification
	title string
}

func (m *model) notificationKindLabel(kind string) string {
	switch kind {
	case "mention":
		return m.tr(msgNotifMention)
	case "reply":
		return m.tr(msgNotifReply)
	case "watched":
		return m.tr(msgNotifWatched)
	default:
		return kind
	}
}

func (m *model) notificationItemTitle(n projections.Notification) string {
	label := m.notificationKindLabel(n.Kind)
	line := m.tr(msgCommonPostFormat, map[string]string{"label": label, "thread": n.ThreadID})
	if n.Read {
		return line
	}
	return "● " + line
}

func (i notificationItem) Title() string {
	return i.title
}

func (i notificationItem) Description() string {
	ts := ""
	if i.n.TS > 1_000_000_000_000 {
		ts = time.UnixMilli(i.n.TS).Format("2006-01-02 15:04")
	} else {
		ts = fmt.Sprintf("#%d", i.n.TS)
	}
	return fmt.Sprintf("%s • %s", i.n.Actor, ts)
}

func (i notificationItem) FilterValue() string {
	return i.n.Actor + " " + i.n.ThreadID
}

type profileField struct {
	key       string
	label     string
	desc      string
	multiline bool
}

type profileFieldItem struct {
	field profileField
	value string
}

func (i profileFieldItem) Title() string { return i.field.label }

func (i profileFieldItem) Description() string {
	value := strings.TrimSpace(i.value)
	if value == "" {
		return i.field.desc
	}
	value = strings.ReplaceAll(value, "\n", " / ")
	if len(value) > 96 {
		value = value[:96] + "…"
	}
	return value
}

func (i profileFieldItem) FilterValue() string { return i.field.label + " " + i.value }

func (m *model) profileFields() []profileField {
	return []profileField{
		{key: "displayName", label: m.tr(msgProfileDisplayName), desc: m.tr(msgProfileDisplayDesc)},
		{key: "title", label: m.tr(msgProfileTitle), desc: m.tr(msgProfileTitleDesc)},
		{key: "bio", label: m.tr(msgProfileBio), desc: m.tr(msgProfileBioDesc), multiline: true},
		{key: "avatar", label: m.tr(msgProfileAvatar), desc: m.tr(msgProfileAvatarDesc)},
		{key: "homepage", label: m.tr(msgProfileHomepage), desc: m.tr(msgProfileHomepageDesc)},
		{key: "plan", label: m.tr(msgProfilePlan), desc: m.tr(msgProfilePlanDesc), multiline: true},
		{key: "signature", label: m.tr(msgProfileSignature), desc: m.tr(msgProfileSignatureDesc), multiline: true},
	}
}

type onlineUserItem struct {
	u    core.SocialUser
	desc string
}

func (i onlineUserItem) Title() string {
	name := i.u.DisplayName
	if strings.TrimSpace(name) == "" {
		name = i.u.Name
	}
	if name != i.u.Name && i.u.Name != "" {
		name = fmt.Sprintf("%s (%s)", name, i.u.Name)
	}
	if i.u.Mutual {
		name += " ★"
	}
	return name
}

func (i onlineUserItem) Description() string { return i.desc }

func (i onlineUserItem) FilterValue() string {
	return i.u.Name + " " + i.u.DisplayName + " " + i.u.Status + " " + i.u.Mode + " " + i.u.BoardName + " " + i.u.LocationLabel
}

func (m *model) onlineUserStatus(user core.SocialUser) string {
	mode := strings.TrimSpace(user.Mode)
	if mode == "" {
		mode = strings.TrimSpace(user.Status)
	}
	if mode == "" {
		mode = m.tr(msgCommonOnline)
	}
	location := strings.TrimSpace(user.LocationLabel)
	if location == "" && strings.TrimSpace(user.BoardName) != "" {
		location = user.BoardName
	}
	if location == "" && strings.TrimSpace(user.BoardID) != "" {
		location = user.BoardID
	}
	if location == "" && strings.TrimSpace(user.ThreadID) != "" {
		location = m.tr(msgCommonStatusIn, map[string]string{"id": user.ThreadID})
	}
	parts := []string{mode}
	if location != "" {
		parts = append(parts, location)
	}
	if user.IdleSeconds > 0 {
		parts = append(parts, m.tr(msgOnlineModeIdleTemplate, map[string]string{"idle": formatIdle(user.IdleSeconds)}))
	}
	if user.Role != "" && user.Role != "user" {
		parts = append(parts, user.Role)
	}
	return strings.Join(parts, " · ")
}

func formatIdle(seconds int64) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	hours := minutes / 60
	if hours < 24 {
		return fmt.Sprintf("%dh%02dm", hours, minutes%60)
	}
	days := hours / 24
	return fmt.Sprintf("%dd%02dh", days, hours%24)
}

// --- M14 node spy helpers ---

// nodeItem is a list.Item representing a single active SSH session.
type nodeItem struct {
	n core.NodeEntry
}

func (i nodeItem) Title() string {
	return fmt.Sprintf("%s  %s", i.n.Username, i.n.RemoteIP)
}

func (i nodeItem) Description() string {
	elapsed := time.Since(i.n.LoginTime).Truncate(time.Second)
	loc := i.n.Location
	if loc == "" {
		loc = "?"
	}
	return fmt.Sprintf("%s  %s  node:%s", loc, elapsed, i.n.NodeID[:8])
}

func (i nodeItem) FilterValue() string {
	return i.n.Username + " " + i.n.RemoteIP + " " + i.n.Location
}

func (m model) renderNodeSpy() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("%s %d active node(s)\n\n", m.styled(styleDim, "›"), len(m.nodes)))
	if len(m.nodes) == 0 {
		b.WriteString(m.styled(styleDim, "no active SSH sessions") + "\n")
		return b.String()
	}
	b.WriteString(m.list.View())
	return b.String()
}

// --- M12 door games helpers ---

// doorItem is a list.Item representing a configured door game.
type doorItem struct {
	d doormodel.DoorConfig
}

func (i doorItem) Title() string {
	return i.d.Name
}

func (i doorItem) Description() string {
	if i.d.Description != "" {
		return i.d.Description
	}
	return i.d.Cmd
}

func (i doorItem) FilterValue() string {
	return i.d.ID + " " + i.d.Name
}

// enterDoorsMenu navigates to the M12 door games list.
func (m *model) enterDoorsMenu() tea.Cmd {
	m.pushPage(pageDoorsMenu)
	m.rebuildList()
	return nil
}

// selectedDoor returns the DoorConfig of the currently selected item, or nil.
func (m *model) selectedDoor() *doormodel.DoorConfig {
	sel, ok := m.list.SelectedItem().(doorItem)
	if !ok {
		return nil
	}
	return &sel.d
}

// launchDoor builds an exec.Cmd for the door and returns a tea.ExecProcess
// command that suspends the TUI, hands the terminal to the door binary, and
// resumes when the door exits.
func (m *model) launchDoor(d doormodel.DoorConfig) tea.Cmd {
	termName := m.termName
	if termName == "" {
		termName = "ansi"
	}

	cmd := exec.Command(d.Cmd, d.Args...)
	// Forward the terminal type and size so the door can render correctly.
	cmd.Env = append(cmd.Environ(),
		"TERM="+termName,
		fmt.Sprintf("COLUMNS=%d", m.width),
		fmt.Sprintf("LINES=%d", m.height),
	)

	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return doorExitedMsg{err: err}
	})
}

func (m *model) rebuildList() {
	m.list.SetShowTitle(false)
	switch m.page {
	case pageMainMenu:
		items := []list.Item{
			mainMenuItem{key: "1", title: m.tr(msgPageBoard), desc: m.tr(msgPageBoardsDesc)},
			mainMenuItem{key: "2", title: m.tr(msgTitleLiveChat), desc: m.tr(msgPageChatDesc)},
			mainMenuItem{key: "3", title: m.tr(msgTitleNotifications), desc: m.tr(msgPageNotificationsDesc)},
			mainMenuItem{key: "4", title: m.tr(msgTitleSearch), desc: m.tr(msgPageSearchDesc)},
			mainMenuItem{key: "5", title: m.tr(msgTitleProfile), desc: m.tr(msgPageProfileDesc)},
			mainMenuItem{key: "6", title: m.tr(msgTitleOnlineUsers), desc: m.tr(msgPageOnlineDesc)},
			mainMenuItem{key: "7", title: "The Town (MUD)", desc: "Wander the shared text world"},
			mainMenuItem{key: "8", title: m.tr(msgPageExit), desc: m.tr(msgPageExitDesc)},
		}
		if m.canRegister() {
			items = append(items, mainMenuItem{key: "c", title: "Create account", desc: "Register a new account"})
		}
		m.list.SetItems(items)
		m.list.Title = m.tr(msgTitleMainMenu)
	case pageBoardList:
		items := make([]list.Item, len(m.boards))
		for i, b := range m.boards {
			items[i] = boardItem{b}
		}
		m.list.SetItems(items)
		m.list.Title = m.tr(msgTitleBoards)
	case pageThreadList:
		items := make([]list.Item, len(m.threads))
		for i, t := range m.threads {
			author := strings.TrimSpace(t.Author)
			if author == "" {
				author = m.tr(msgCommonUnknown)
			}
			desc := m.tr(msgListByPosts, map[string]string{
				"author": author,
				"count":  fmt.Sprintf("%d", t.PostCount),
			})
			items[i] = threadItem{index: m.threadOffset + i + 1, t: t, desc: desc}
		}
		m.list.SetItems(items)
		if len(m.threads) == 0 {
			m.list.Title = fmt.Sprintf("%s [%s]", m.currentBoard, m.tr(msgMenuThreadListEmpty))
		} else {
			m.list.Title = fmt.Sprintf("%s [%d-%d]", m.currentBoard, m.threadOffset+1, m.threadOffset+len(m.threads))
		}
	case pageNotifications:
		items := make([]list.Item, len(m.notifications))
		for i, n := range m.notifications {
			items[i] = notificationItem{n, m.notificationItemTitle(n)}
		}
		m.list.SetItems(items)
		m.list.Title = m.tr(msgTitleNotifications)
	case pageProfile:
		fields := m.profileFields()
		items := make([]list.Item, len(fields))
		for i, field := range fields {
			items[i] = profileFieldItem{field: field, value: m.profileFieldValue(field)}
		}
		m.list.SetItems(items)
		m.list.Title = m.tr(msgProfileSettingsTitle)
	case pageOnline:
		items := make([]list.Item, len(m.onlineUsers))
		for i, user := range m.onlineUsers {
			items[i] = onlineUserItem{u: user, desc: m.onlineUserStatus(user)}
		}
		m.list.SetItems(items)
		m.list.Title = m.tr(msgPageOnline)

	case pageNodeSpy:
		items := make([]list.Item, len(m.nodes))
		for i, n := range m.nodes {
			items[i] = nodeItem{n: n}
		}
		m.list.SetItems(items)
		m.list.Title = "Node Spy"

	case pageDoorsMenu:
		items := make([]list.Item, len(m.doors))
		for i, d := range m.doors {
			items[i] = doorItem{d: d}
		}
		m.list.SetItems(items)
		m.list.Title = "Doors"
	}
}

func (m *model) cycleLocale() {
	switch m.locale {
	case localeEN:
		m.locale = localeZHCN
	case localeZHCN:
		m.locale = localeZHTW
	default:
		m.locale = localeEN
	}
	// Refresh input placeholders in the new locale.
	m.titleInput.Placeholder = m.tr(msgPlaceholderThreadTitle)
	m.chatInput.Placeholder = m.tr(msgPlaceholderChatMessage)
	m.compose.Placeholder = m.tr(msgPlaceholderComposeBody)
	// Rebuild all visible content.
	m.rebuildList()
	switch m.page {
	case pageThread:
		m.rebuildPostView()
	case pageChat:
		m.rebuildChatView()
	case pageSearch:
		m.rebuildSearchView(m.posts)
	}
	m.statusMsg = m.tr(msgStatusLocaleSwitch)
}

func (m *model) openMainMenuSelection() tea.Cmd {
	item, ok := m.list.SelectedItem().(mainMenuItem)
	if !ok {
		return nil
	}
	switch item.key {
	case "1":
		m.page = pageBoardList
		m.rebuildList()
	case "2":
		return m.enterChat()
	case "3":
		m.pushPage(pageNotifications)
		m.notifications = nil
		m.rebuildList()
		return m.fetchNotifications()
	case "4":
		m.pushPage(pageSearch)
		m.searchQuery = ""
		m.vp.SetContent(m.styled(styleDim, m.tr(msgPlaceholderSearchPrompt)))
	case "5":
		return m.enterProfile()
	case "6":
		return m.enterOnline()
	case "7":
		return m.enterMUD()
	case "8":
		return m.quit()
	case "c":
		if m.canRegister() {
			return m.enterSignup()
		}
	}
	return nil
}

func (m *model) quit() tea.Cmd {
	if m.c != nil && m.sub != nil {
		m.c.Unsubscribe(m.sub)
	}
	return tea.Quit
}

func (m *model) enterProfile() tea.Cmd {
	m.pushPage(pageProfile)
	m.rebuildList()
	return m.fetchProfile()
}

func (m *model) openProfileField() {
	item, ok := m.list.SelectedItem().(profileFieldItem)
	if !ok {
		return
	}
	if m.profile == nil {
		m.statusMsg = m.tr(msgStatusProfileLoading)
		return
	}
	m.profileField = item.field
	value := m.profileFieldValue(item.field)
	m.profileInput.Blur()
	m.profileEditor.Blur()
	if item.field.multiline {
		m.profileEditor.SetValue(value)
		m.profileEditor.Focus()
	} else {
		m.profileInput.Reset()
		m.profileInput.Placeholder = item.field.label
		m.profileInput.SetValue(value)
		m.profileInput.Focus()
	}
	m.pushPage(pageProfileEdit)
}

func (m *model) enterOnline() tea.Cmd {
	m.pushPage(pageOnline)
	m.onlineUsers = nil
	m.rebuildList()
	return tea.Batch(
		m.setPresence("active", "online", "", "", m.tr(msgTitleOnlineUsers)),
		m.fetchOnlineUsers(),
	)
}

// enterNodeSpy navigates to the M14 sysop node spy panel (admin only).
func (m *model) enterNodeSpy() tea.Cmd {
	m.pushPage(pageNodeSpy)
	m.nodes = nil
	m.rebuildList()
	return m.fetchNodes()
}

// selectedNode returns the NodeEntry of the currently selected list item, or nil.
func (m *model) selectedNode() *core.NodeEntry {
	sel, ok := m.list.SelectedItem().(nodeItem)
	if !ok {
		return nil
	}
	return &sel.n
}

func (m *model) enterChat() tea.Cmd {
	m.pushPage(pageChat)
	m.chatInput.Focus()
	m.rebuildChatView()
	return m.fetchChatLines("lobby")
}

// --- MUD ("The Town") -------------------------------------------------------

func (m *model) enterMUD() tea.Cmd {
	m.pushPage(pageMUD)
	m.mudInput.Focus()
	m.mudLog = nil
	m.mudRoom = nil
	m.mudRoomScope = ""
	if m.c != nil && m.sub != nil && m.actor != nil {
		m.c.Bus.AddScopes(m.sub, []string{"mud:user:" + m.actor.ID})
	}
	return m.sendMUDCommand("look")
}

func (m *model) leaveMUD() tea.Cmd {
	cmd := m.sendMUDCommand("quit")
	if m.c != nil && m.sub != nil && m.actor != nil {
		scopes := []string{"mud:user:" + m.actor.ID}
		if m.mudRoomScope != "" {
			scopes = append(scopes, m.mudRoomScope)
		}
		m.c.Bus.RemoveScopes(m.sub, scopes)
	}
	m.mudRoomScope = ""
	return cmd
}

func (m model) sendMUDCommand(line string) tea.Cmd {
	return func() tea.Msg {
		if m.c == nil {
			return nil
		}
		raw, _ := json.Marshal(proto.MUDCommandPayload{Line: line})
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdMUDCommand, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		return nil // effects (room view, room events) arrive over the event bus
	}
}

// applyMUDRoom swaps the subscribed room scope when the player changes rooms and
// records the new room snapshot.
func (m *model) applyMUDRoom(room *proto.MUDRoomView) {
	if room == nil {
		return
	}
	newScope := "mud:room:" + room.ID
	if newScope != m.mudRoomScope && m.c != nil && m.sub != nil {
		if m.mudRoomScope != "" {
			m.c.Bus.RemoveScopes(m.sub, []string{m.mudRoomScope})
		}
		m.c.Bus.AddScopes(m.sub, []string{newScope})
		m.mudRoomScope = newScope
	}
	m.mudRoom = room
}

func (m *model) mudAppend(line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	m.mudLog = append(m.mudLog, line)
	if len(m.mudLog) > 200 {
		m.mudLog = m.mudLog[len(m.mudLog)-200:]
	}
}

// mudSectionContent renders the room panel, the recent activity log, and the
// command input, sized to the section height.
func (m model) mudSectionContent() string {
	avail := m.sectionContentHeight()
	input := m.mudInput.View()

	var head strings.Builder
	if m.mudRoom != nil {
		head.WriteString(m.styled(styleTitle, m.mudRoom.Name) + "\n")
		for _, dl := range strings.Split(m.mudRoom.Desc, "\n") {
			head.WriteString(m.styled(styleDim, dl) + "\n")
		}
		if len(m.mudRoom.Exits) > 0 {
			head.WriteString(m.styled(stylePostSignatureSep, "Exits: "+strings.Join(m.mudRoom.Exits, ", ")) + "\n")
		}
		if len(m.mudRoom.Occupants) > 0 {
			head.WriteString(m.styled(styleAuthor, "Also here: "+strings.Join(m.mudRoom.Occupants, ", ")) + "\n")
		}
		head.WriteString("\n")
	}
	headStr := head.String()
	headH := 0
	if strings.TrimSpace(headStr) != "" {
		headH = lipgloss.Height(headStr)
	}
	logH := avail - headH - 1 // reserve a line for the input
	if logH < 1 {
		logH = 1
	}
	logLines := m.mudLog
	if len(logLines) > logH {
		logLines = logLines[len(logLines)-logH:]
	}
	return headStr + strings.Join(logLines, "\n") + "\n" + input
}

func (m *model) selectedBoard() *core.Board {
	sel, ok := m.list.SelectedItem().(boardItem)
	if !ok {
		return nil
	}
	return &sel.b
}

func (m *model) selectedThread() *core.Thread {
	sel, ok := m.list.SelectedItem().(threadItem)
	if !ok {
		return nil
	}
	return &sel.t
}

func (m *model) selectedNotification() *projections.Notification {
	sel, ok := m.list.SelectedItem().(notificationItem)
	if !ok {
		return nil
	}
	return &sel.n
}

func (m *model) selectedOnlineUser() *core.SocialUser {
	sel, ok := m.list.SelectedItem().(onlineUserItem)
	if !ok {
		return nil
	}
	return &sel.u
}

func (m *model) selectedPostID() string {
	if m.selectedPost < 0 || m.selectedPost >= len(m.posts) {
		return ""
	}
	return m.posts[m.selectedPost].ID
}

func (m *model) currentPollData() *projections.Poll {
	if m.currentPoll == "" {
		return nil
	}
	return m.postPolls[m.currentPoll]
}

func (m *model) pollPostIDForPoll(pollID string) string {
	for postID, poll := range m.postPolls {
		if poll != nil && poll.ID == pollID {
			return postID
		}
	}
	return ""
}

func (m *model) moveSelectedPost(delta int) {
	if len(m.posts) == 0 {
		m.selectedPost = -1
		return
	}
	m.selectedPost += delta
	if m.selectedPost < 0 {
		m.selectedPost = 0
	}
	if m.selectedPost >= len(m.posts) {
		m.selectedPost = len(m.posts) - 1
	}
	m.rebuildPostView()
}

func (m *model) navigateAdjacentThread(delta int) tea.Cmd {
	if delta == 0 {
		return nil
	}
	if len(m.threads) == 0 {
		m.statusMsg = m.tr(msgStatusThreadNotLoaded)
		return nil
	}
	currentIndex := -1
	for i, thread := range m.threads {
		if thread.ID == m.currentThread {
			currentIndex = i
			break
		}
	}
	if currentIndex < 0 {
		m.statusMsg = m.tr(msgStatusThreadNotInBoard)
		return nil
	}
	nextIndex := currentIndex + delta
	if nextIndex < 0 {
		m.statusMsg = m.tr(msgStatusThreadListFirst)
		return nil
	}
	if nextIndex >= len(m.threads) {
		m.statusMsg = m.tr(msgStatusThreadListLast)
		return nil
	}
	m.list.Select(nextIndex)
	return m.openThread(m.threads[nextIndex])
}

func (m *model) openThread(thread core.Thread) tea.Cmd {
	if strings.TrimSpace(thread.ID) == "" {
		m.statusMsg = m.tr(msgStatusChatNotFound)
		return nil
	}
	m.currentThread = thread.ID
	m.posts = nil
	m.postPolls = make(map[string]*projections.Poll)
	m.currentPoll = ""
	m.selectedPost = -1
	m.vp.SetContent(m.styled(styleDim, m.tr(msgStatusLoadingThread)))
	m.vp.GotoTop()
	return tea.Batch(
		m.fetchPosts(thread.ID),
		m.resubscribeThread(thread.ID),
	)
}

func (m *model) hydrateReactionState() error {
	for _, p := range m.posts {
		reacted, err := m.c.UserReacted(p.ID, m.actor.ID)
		if err != nil {
			return err
		}
		m.postReactions[p.ID] = reacted
	}
	return nil
}

func (m *model) hydratePostAuthorNames(posts []core.Post) {
	if m.authorNames == nil {
		m.authorNames = make(map[string]string)
	}
	if m.c == nil {
		return
	}
	for _, post := range posts {
		name := strings.TrimSpace(post.Author)
		if name == "" {
			continue
		}
		if _, ok := m.authorNames[name]; ok {
			continue
		}
		profile, err := m.c.UserProfileByName(name)
		if err != nil || profile == nil {
			m.authorNames[name] = ""
			continue
		}
		m.cacheProfileDisplayName(profile)
	}
}

func (m *model) cacheProfileDisplayName(profile *projections.UserProfile) {
	if profile == nil {
		return
	}
	if m.authorNames == nil {
		m.authorNames = make(map[string]string)
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		return
	}
	displayName := strings.TrimSpace(profile.DisplayName)
	if displayName == "" {
		displayName = name
	}
	m.authorNames[name] = displayName
}

// --- Post/chat view rendering ---

func (m *model) rebuildPostView() {
	var b strings.Builder
	ordinals := make(map[string]string, len(m.posts))
	for i, p := range m.posts {
		if p.ID != "" {
			ordinals[p.ID] = m.postOrdinal(i)
		}
	}
	for i, p := range m.posts {
		headerLines := m.postHeaderLines(i, p, ordinals)
		if len(headerLines) > 0 {
			b.WriteString(strings.Join(headerLines, "\n") + "\n")
		}
		b.WriteString(m.postInnerSepLine() + "\n")
		body := ""
		if p.Redacted {
			body = m.styled(styleRedacted, m.tr(msgPostRedacted))
		} else {
			body = m.renderMarkup(p.Body)
		}
		b.WriteString(body)
		if signature := strings.TrimSpace(p.Signature); signature != "" && !p.Redacted {
			b.WriteString("\n" + m.postSignatureSepLine() + "\n")
			b.WriteString(m.renderMarkup(signature))
		}
		b.WriteString("\n" + m.postBoundaryLine() + "\n")
	}
	m.vp.SetContent(b.String())
}

func (m model) postHeaderLines(index int, p core.Post, ordinals map[string]string) []string {
	ordinal := m.postOrdinal(index)
	authorLine := fmt.Sprintf(
		"%-4s  %s %s  %s %s",
		ordinal,
		m.styled(styleDim, m.tr(msgLabelAuthor)),
		m.postAuthorLabel(p),
		m.styled(styleDim, m.tr(msgLabelTime)),
		m.postTimeLabel(p),
	)
	if index != 0 {
		return []string{authorLine}
	}
	headerLines := []string{
		fmt.Sprintf("%-4s  %s %s", ordinal, m.styled(styleDim, m.tr(msgLabelTitle)), m.postTitle(p)),
		fmt.Sprintf(
			"      %s %s  %s %s",
			m.styled(styleDim, m.tr(msgLabelAuthor)),
			m.postAuthorLabel(p),
			m.styled(styleDim, m.tr(msgLabelTime)),
			m.postTimeLabel(p),
		),
	}
	if meta := m.postMetadata(p, ordinals); len(meta) > 0 {
		headerLines = append(headerLines, m.wrapPostMetadata(meta)...)
	}
	return headerLines
}

func (m model) postTitle(p core.Post) string {
	if title := strings.TrimSpace(p.ThreadTitle); title != "" {
		return title
	}
	threadID := p.Thread
	if threadID == "" {
		threadID = m.currentThread
	}
	for _, t := range m.threads {
		if t.ID == threadID {
			if title := strings.TrimSpace(t.Title); title != "" {
				return title
			}
			break
		}
	}
	if threadID != "" {
		return threadID
	}
	return m.tr(msgCommonUntitled)
}

func (m model) postAuthorLabel(p core.Post) string {
	name := strings.TrimSpace(p.Author)
	if name == "" {
		name = m.tr(msgCommonUnknown)
	}
	displayName := ""
	if m.authorNames != nil {
		displayName = strings.TrimSpace(m.authorNames[name])
	}
	if displayName == "" && m.actor != nil && m.actor.Name == name && m.profile != nil {
		displayName = strings.TrimSpace(m.profile.DisplayName)
	}
	styledName := m.styled(styleAuthor, name)
	if displayName != "" {
		return styledName + m.styled(styleDim, " ("+displayName+")")
	}
	return styledName
}

func (m model) postTimeLabel(p core.Post) string {
	if p.CreatedAt > 1_000_000_000_000 {
		return time.UnixMilli(p.CreatedAt).Format("2006-01-02 15:04")
	}
	if p.CreatedSeq > 0 {
		return m.tr(msgPostPrefixSeq, map[string]string{"seq": fmt.Sprintf("%d", p.CreatedSeq)})
	}
	return m.tr(msgCommonUnknown)
}

func (m model) postMetadata(p core.Post, ordinals map[string]string) []string {
	var meta []string
	if p.CreatedSeq > 0 {
		meta = append(meta, m.tr(msgPostPrefixSeq, map[string]string{"seq": fmt.Sprintf("%d", p.CreatedSeq)}))
	}
	if p.UpdatedSeq > 0 && p.UpdatedSeq != p.CreatedSeq {
		meta = append(meta, m.tr(msgPostPrefixUpdated, map[string]string{"seq": fmt.Sprintf("%d", p.UpdatedSeq)}))
	}
	if p.Version > 1 {
		meta = append(meta, m.tr(msgPostPrefixVersion, map[string]string{"version": fmt.Sprintf("%d", p.Version)}))
	}
	if p.UpdatedAt > 1_000_000_000_000 && p.CreatedAt > 1_000_000_000_000 && p.UpdatedAt != p.CreatedAt {
		meta = append(meta, m.tr(msgPostPrefixEdited, map[string]string{"time": time.UnixMilli(p.UpdatedAt).Format("2006-01-02 15:04")}))
	}
	if p.ReplyTo != "" {
		target := ordinals[p.ReplyTo]
		if target == "" {
			target = p.ReplyTo
		}
		meta = append(meta, m.tr(msgPostPrefixReply, map[string]string{"reply": target}))
	}
	if p.ContentType != "" {
		meta = append(meta, m.tr(msgPostPrefixType, map[string]string{"type": p.ContentType}))
	}
	if p.ReactionCount > 0 {
		meta = append(meta, m.tr(msgPostReaction, map[string]string{"count": fmt.Sprintf("%d", p.ReactionCount)}))
	}
	if _, ok := m.postPolls[p.ID]; ok {
		meta = append(meta, m.tr(msgPostPollTag))
	}
	if len(p.Attachments) > 0 {
		meta = append(meta, m.tr(msgPostAttachments, map[string]string{"count": fmt.Sprintf("%d", len(p.Attachments))}))
	}
	if p.Marked {
		meta = append(meta, m.tr(msgPostMarked))
	}
	if p.Recommended {
		meta = append(meta, m.tr(msgPostRecommended))
	}
	if p.NoReply {
		meta = append(meta, m.tr(msgPostNoReply))
	}
	if p.TeX {
		meta = append(meta, m.tr(msgPostTex))
	}
	if p.MailBack {
		meta = append(meta, m.tr(msgPostMailBack))
	}
	if p.Redacted {
		meta = append(meta, m.tr(msgPostRedacted))
	}
	if p.SourceTitle != "" {
		meta = append(meta, m.tr(msgPostSource, map[string]string{"source": p.SourceTitle}))
	} else if p.SourceThread != "" {
		meta = append(meta, m.tr(msgPostSource, map[string]string{"source": p.SourceThread}))
	}
	return meta
}

func (m model) wrapPostMetadata(meta []string) []string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	prefix := m.tr(msgPostMetaLinePrefix)
	continuation := m.tr(msgPostMetaContinuation)
	lines := []string{prefix}
	current := prefix
	for _, item := range meta {
		if item == "" {
			continue
		}
		next := item
		if current != prefix && current != continuation {
			next = "  " + next
		}
		if len(current)+len(next) > width && current != prefix && current != continuation {
			lines[len(lines)-1] = current
			current = continuation + item
			lines = append(lines, current)
			continue
		}
		current += next
	}
	if len(lines) == 0 {
		return nil
	}
	lines[len(lines)-1] = current
	return lines
}

func (m model) postOrdinal(index int) string {
	if index == 0 {
		return m.tr(msgPostOp)
	}
	return fmt.Sprintf("[%d]", index)
}

func (m model) blockLine(style lipgloss.Style, value string) string {
	if !m.supportsANSI {
		return value
	}
	width := m.width
	if width <= 4 {
		width = 80
	}
	return style.Width(width - 4).Render(value)
}

func (m *model) rebuildChatView() {
	var b strings.Builder
	if len(m.chat) == 0 {
		b.WriteString(m.styled(styleDim, m.tr(msgTitleNoMessages)) + "\n")
		m.vp.SetContent(b.String())
		return
	}
	for _, line := range m.chat {
		ts := time.UnixMilli(line.ts).Format("15:04")
		b.WriteString(fmt.Sprintf("%s %s %s\n",
			m.styled(styleDim, ts),
			m.styled(styleChatUser, line.user+":"),
			line.text,
		))
	}
	m.vp.SetContent(b.String())
}

// renderMarkup converts the constrained Markdown subset to ANSI-friendly text.
func (m *model) renderMarkup(body string) string {
	boldStyle := m.rstyle(lipgloss.NewStyle().Bold(true))
	dimStyle := m.rstyle(lipgloss.NewStyle().Foreground(lipgloss.Color("240")))
	codeStyle := m.rstyle(lipgloss.NewStyle().Foreground(lipgloss.Color("214")))

	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "> ") {
			out = append(out, m.styled(dimStyle, "│ "+line[2:]))
			continue
		}
		line = replaceInline(line, "**", func(s string) string { return m.styled(boldStyle, s) })
		line = replaceInline(line, "`", func(s string) string { return m.styled(codeStyle, s) })
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func replaceInline(s, delim string, render func(string) string) string {
	parts := strings.Split(s, delim)
	if len(parts) < 3 {
		return s
	}
	var b strings.Builder
	for i, p := range parts {
		if i%2 == 1 {
			b.WriteString(render(p))
		} else {
			b.WriteString(p)
		}
	}
	return b.String()
}

// --- Commands (bubbletea async operations) ---

func (m model) awaitEvent() tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-m.sub.Ch
		if !ok {
			return disconnectMsg{}
		}
		return eventMsg{evt}
	}
}

// awaitSysopMsg blocks until the node registry sends a sysop message to this
// session's message channel. Re-arm it in the Update handler after receipt.
func (m model) awaitSysopMsg() tea.Cmd {
	ch := m.msgCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg := <-ch
		return sysopMsgMsg{msg}
	}
}

// fetchNodes loads the live node list from the in-memory registry.
func (m model) fetchNodes() tea.Cmd {
	return func() tea.Msg {
		if m.c == nil {
			return nodeListMsg{}
		}
		return nodeListMsg{nodes: m.c.ListNodes()}
	}
}

// canReadBoard enforces the shared member-read-mode access control so the TUI
// cannot list or open private/member-only boards the actor isn't entitled to.
func (m model) canReadBoard(board string) bool {
	ok, err := m.c.ActorCanReadBoardID(m.actor, board)
	return err == nil && ok
}

func (m model) fetchBoards() tea.Cmd {
	return func() tea.Msg {
		boards, err := m.c.ListBoards()
		if err != nil {
			return errMsg{err}
		}
		readable := boards[:0]
		for _, b := range boards {
			if m.canReadBoard(b.ID) {
				readable = append(readable, b)
			}
		}
		return boardsMsg{readable}
	}
}

func (m model) fetchThreads(board string, offset int) tea.Cmd {
	return func() tea.Msg {
		if !m.canReadBoard(board) {
			return threadsMsg{board: board, offset: offset, threads: nil}
		}
		threads, err := m.c.ListThreads(board, threadPageSize, offset)
		if err != nil {
			return errMsg{err}
		}
		return threadsMsg{board: board, offset: offset, threads: threads}
	}
}

func (m model) fetchPosts(thread string) tea.Cmd {
	return func() tea.Msg {
		// Resolve the thread's board and enforce read access (defense in depth
		// in case a thread id is reached outside the board → thread navigation).
		if t, err := m.c.GetThread(thread); err == nil && t != nil && !m.canReadBoard(t.Board) {
			return postsMsg{nil}
		}
		posts, err := m.c.ListPosts(thread, 100, 0)
		if err != nil {
			return errMsg{err}
		}
		return postsMsg{posts}
	}
}

func (m model) fetchChatLines(room string) tea.Cmd {
	return func() tea.Msg {
		if m.c == nil {
			return chatLinesMsg{}
		}
		lines, err := m.c.ListChatLines(room, 50)
		if err != nil {
			return chatLinesMsg{err: err}
		}
		return chatLinesMsg{lines: lines}
	}
}

func (m model) fetchPollsForPosts(posts []core.Post) tea.Cmd {
	thread := m.currentThread
	postIDs := make([]string, 0, len(posts))
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
	}
	return func() tea.Msg {
		if len(postIDs) == 0 {
			return postPollsMsg{thread: thread, polls: map[string]*projections.Poll{}}
		}
		polls, err := m.c.PollsForPosts(postIDs, m.actor.ID)
		if err != nil {
			return postPollsMsg{thread: thread, err: err}
		}
		return postPollsMsg{thread: thread, polls: polls}
	}
}

func (m model) resubscribeThread(threadID string) tea.Cmd {
	return func() tea.Msg {
		m.c.Bus.AddScopes(m.sub, []string{"thread:" + threadID})
		return nil
	}
}

func isPendingCommandReply(reply commandexec.Reply) bool {
	return reply.Result != nil && reply.Result.Status == proto.AckStatusPending
}

func (m model) sendChatLine(text string) tea.Cmd {
	return func() tea.Msg {
		p := proto.SendChatLinePayload{Room: "lobby", Text: text}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdSendChatLine, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return chatSentMsg{queued: true}
		}
		return chatSentMsg{}
	}
}

func (m model) submitPost(body string) tea.Cmd {
	threadID := m.currentThread
	return func() tea.Msg {
		if err := validatePollMarkup(body); err != nil {
			return errMsg{fmt.Errorf("invalid poll markup: %w", err)}
		}
		p := proto.AppendPostPayload{Thread: m.currentThread, Body: body}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdAppendPost, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return postSubmittedMsg{thread: threadID, queued: true}
		}
		return postSubmittedMsg{thread: threadID}
	}
}

func (m model) submitNewThread() tea.Cmd {
	title := strings.TrimSpace(m.titleInput.Value())
	body := strings.TrimSpace(m.compose.Value())
	board := m.currentBoard
	return func() tea.Msg {
		if title == "" {
			return errMsg{fmt.Errorf("title is required")}
		}
		if body == "" {
			return errMsg{fmt.Errorf("body is required")}
		}
		if err := validatePollMarkup(body); err != nil {
			return errMsg{fmt.Errorf("invalid poll markup: %w", err)}
		}
		p := proto.CreateThreadPayload{Board: board, Title: title, Body: body}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdCreateThread, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return threadSubmittedMsg{board: board, queued: true}
		}
		threadID := ""
		if reply.Result != nil {
			threadID = reply.Result.ID
		}
		return threadSubmittedMsg{board: board, thread: threadID}
	}
}

func (m *model) finishCompose() {
	m.compose.Blur()
	m.compose.Reset()
	m.titleInput.Blur()
	m.titleInput.Reset()
}

func (m *model) insertPollTemplate() {
	template := "[poll]\nQuestion\nOption 1\nOption 2\n[/poll]"
	value := m.compose.Value()
	body := strings.TrimRight(value, "\n")
	if body == "" {
		m.compose.SetValue(template)
		return
	}

	if strings.HasSuffix(value, "\n") {
		m.compose.InsertString("\n" + template)
		return
	}
	m.compose.InsertString("\n\n" + template)
}

func (m *model) insertPollTemplateWithExpires(expiresAt string) {
	if expiresAt == "" {
		m.insertPollTemplate()
		return
	}
	template := "[poll expires=" + expiresAt + "]\nQuestion\nOption 1\nOption 2\n[/poll]"
	value := m.compose.Value()
	body := strings.TrimRight(value, "\n")
	if body == "" {
		m.compose.SetValue(template)
		return
	}

	if strings.HasSuffix(value, "\n") {
		m.compose.InsertString("\n" + template)
		return
	}
	m.compose.InsertString("\n\n" + template)
}

func (m *model) composeHelpLine() string {
	if m.canCreatePoll {
		if m.trustLoaded {
			return m.tr(msgComposeHelpPollChecked)
		}
		return m.tr(msgComposeHelpPollCheck)
	}
	if m.trustLoaded {
		return m.tr(msgComposeHelpPollTrust)
	}
	return m.tr(msgComposeHelpPollCheck)
}

func keyString(msg tea.KeyMsg) string {
	return normalizeKeyString(msg.String())
}

func normalizeKeyString(key string) string {
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "ctrl+") {
		return lower
	}
	return key
}

func (m model) fetchPollPermission() tea.Cmd {
	return func() tea.Msg {
		if m.actor.IsMod() {
			return pollPermissionMsg{canCreatePoll: true}
		}
		trust, err := m.c.TrustInfo(m.actor.ID)
		if err != nil {
			return pollPermissionMsg{err: err}
		}
		if trust == nil {
			return pollPermissionMsg{}
		}
		return pollPermissionMsg{canCreatePoll: trust.TrustLevel >= 2}
	}
}

func (m model) reactPost(postID string) tea.Cmd {
	return func() tea.Msg {
		p := proto.ReactPostPayload{Post: postID, Emoji: "heart"}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdReactPost, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return commandQueuedMsg{}
		}
		return nil
	}
}

func (m model) openPoll(postID string) tea.Cmd {
	return m.fetchPoll(postID, true)
}

func (m model) fetchPoll(postID string, open bool) tea.Cmd {
	return func() tea.Msg {
		pollMeta, err := m.c.GetPollByPostID(postID)
		if err != nil {
			return pollMsg{postID: postID, open: open, err: err}
		}
		if pollMeta == nil {
			return pollMsg{postID: postID, open: open}
		}
		poll, err := m.c.GetPoll(pollMeta.ID, m.actor.ID)
		if err != nil {
			return pollMsg{postID: postID, open: open, err: err}
		}
		if poll == nil {
			return pollMsg{postID: postID, open: open}
		}
		return pollMsg{postID: postID, poll: poll, open: open}
	}
}

func (m model) votePoll(pollID, optionID, postID string) tea.Cmd {
	return func() tea.Msg {
		p := proto.VotePollPayload{Poll: pollID, Option: optionID}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdVotePoll, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return commandQueuedMsg{}
		}
		// Reload to get latest counts and voter status.
		poll, err := m.c.GetPoll(pollID, m.actor.ID)
		if err != nil {
			return errMsg{err}
		}
		if poll == nil {
			return nil
		}
		return pollMsg{postID: postID, poll: poll}
	}
}

func (m model) unreactPost(postID string) tea.Cmd {
	return func() tea.Msg {
		p := proto.ReactPostPayload{Post: postID, Emoji: "heart"}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdUnreactPost, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return commandQueuedMsg{}
		}
		return nil
	}
}

func (m model) runSearch(query string) tea.Cmd {
	return func() tea.Msg {
		posts, err := m.c.SearchPosts(query, "", 30)
		if err != nil {
			return errMsg{err}
		}
		return searchMsg{posts}
	}
}

func (m model) fetchNotifications() tea.Cmd {
	return func() tea.Msg {
		notifs, err := m.c.ListNotifications(m.actor.ID, 50, 0, false)
		if err != nil {
			return errMsg{err}
		}
		unread, err := m.c.CountUnreadNotifications(m.actor.ID)
		if err != nil {
			return errMsg{err}
		}
		return notificationsMsg{notifications: notifs, unread: unread}
	}
}

func (m model) fetchNotificationStatus() tea.Cmd {
	return func() tea.Msg {
		unread, err := m.c.CountUnreadNotifications(m.actor.ID)
		if err != nil {
			return errMsg{err}
		}
		return notificationStatusMsg{unread: unread}
	}
}

func (m model) fetchProfile() tea.Cmd {
	return func() tea.Msg {
		if m.c == nil || m.actor == nil {
			return profileMsg{err: fmt.Errorf("profile unavailable")}
		}
		profile, err := m.c.UserProfileByName(m.actor.Name)
		if err != nil {
			return profileMsg{err: err}
		}
		return profileMsg{profile: profile}
	}
}

func (m model) fetchOnlineUsers() tea.Cmd {
	return func() tea.Msg {
		if m.c == nil || m.actor == nil {
			return onlineUsersMsg{err: fmt.Errorf("online list unavailable")}
		}
		users, err := m.c.ListOnlineUsers(m.actor.ID, "", 100, 0)
		if err != nil {
			return onlineUsersMsg{err: err}
		}
		return onlineUsersMsg{users: users}
	}
}

func (m model) setPresence(status, mode, board, thread, location string) tea.Cmd {
	return func() tea.Msg {
		if m.c == nil || m.actor == nil {
			return presenceSetMsg{}
		}
		p := proto.SetPresencePayload{
			Status:    status,
			SessionID: "tui",
			Mode:      mode,
			Board:     board,
			Thread:    thread,
			Location:  location,
		}
		raw, err := json.Marshal(p)
		if err != nil {
			return presenceSetMsg{err: err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdSetPresence, raw, "")
		if reply.Err != nil {
			return presenceSetMsg{err: fmt.Errorf("%s", reply.Err.Message)}
		}
		return presenceSetMsg{}
	}
}

func (m *model) submitProfileField() tea.Cmd {
	field := m.profileField
	if field.key == "" {
		return nil
	}
	value := m.profileInput.Value()
	if field.multiline {
		value = m.profileEditor.Value()
	}
	if field.key == "title" && len(strings.TrimSpace(value)) > 80 {
		m.statusMsg = m.tr(msgStatusTitleTooLong)
		return nil
	}
	next := m.profileWithField(field, value)
	return func() tea.Msg {
		if m.c == nil || m.actor == nil {
			return profileSavedMsg{err: fmt.Errorf("profile unavailable")}
		}
		if err := m.c.UpdateUserProfile(
			m.actor.ID,
			next.DisplayName,
			next.Title,
			next.Bio,
			next.Avatar,
			next.Signature,
			next.Plan,
			next.Homepage,
		); err != nil {
			return profileSavedMsg{err: err}
		}
		profile, err := m.c.UserProfileByName(m.actor.Name)
		if err != nil {
			return profileSavedMsg{err: err}
		}
		return profileSavedMsg{profile: profile}
	}
}

func (m *model) rebuildSearchView(posts []core.Post) {
	var b strings.Builder
	plural := ""
	if len(posts) != 1 {
		plural = "s"
	}
	b.WriteString(m.styled(styleDim, m.tr(msgSearchResults, map[string]string{
		"count":  fmt.Sprintf("%d", len(posts)),
		"plural": plural,
	})+"\n\n"))
	if len(posts) == 0 {
		if strings.TrimSpace(m.searchQuery) != "" {
			b.WriteString(m.styled(styleDim, m.tr(msgSearchNoResults, map[string]string{"query": m.searchQuery})))
		} else {
			b.WriteString(m.styled(styleDim, m.tr(msgSearchNoResultText)))
		}
		m.vp.SetContent(b.String())
		return
	}
	for _, p := range posts {
		author := m.styled(styleAuthor, p.Author)
		b.WriteString(m.tr(msgSearchAuthorsInThread, map[string]string{
			"author": author,
			"thread": m.styled(styleDim, p.Thread),
		}) + "\n")
		b.WriteString(m.renderMarkup(p.Body))
		b.WriteString("\n" + m.postSepLine() + "\n")
	}
	m.vp.SetContent(b.String())
	m.vp.GotoTop()
}

func (m model) toggleThreadLock() tea.Cmd {
	return func() tea.Msg {
		var locked bool
		for _, t := range m.threads {
			if t.ID == m.currentThread {
				locked = t.Locked
				break
			}
		}
		p := proto.LockThreadPayload{Thread: m.currentThread, Locked: !locked}
		raw, _ := json.Marshal(p)
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdLockThread, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("lock: %s", reply.Err.Message)}
		}
		if isPendingCommandReply(reply) {
			return commandQueuedMsg{}
		}
		return nil
	}
}

func (m model) pageName(p page) string {
	switch p {
	case pageMainMenu:
		return m.tr(msgTitleMainMenu)
	case pageBoardList:
		return m.tr(msgTitleBoards)
	case pageThreadList:
		return m.tr(msgCommonThread)
	case pageThread:
		return m.tr(msgCommonThread)
	case pagePoll:
		return m.tr(msgTitlePoll)
	case pageCompose:
		return m.tr(msgTitleNewReply)
	case pageChat:
		return m.tr(msgTitleLiveChat)
	case pageMUD:
		return "The Town"
	case pageSearch:
		return m.tr(msgTitleSearch)
	case pageNotifications:
		return m.tr(msgTitleNotifications)
	case pageProfile:
		return m.tr(msgTitleProfile)
	case pageProfileEdit:
		return m.tr(msgTitleProfileEdit)
	case pageOnline:
		return m.tr(msgTitleOnlineUsers)
	case pageNodeSpy:
		return "Node Spy"
	case pageDoorsMenu:
		return "Doors"
	case pageSignup:
		return "Create account"
	case pageTwoFactorGate:
		return "Two-factor"
	}
	return ""
}
