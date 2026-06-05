package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

// page identifies which TUI screen is active.
type page int

const (
	pageBoardList page = iota
	pageThreadList
	pageThread
	pagePoll
	pageCompose
	pageChat
	pageSearch
	pageNotifications
)

// msg types for the bubbletea update cycle.
type (
	eventMsg     struct{ evt *proto.Event }
	errMsg       struct{ err error }
	boardsMsg    struct{ boards []core.Board }
	threadsMsg   struct{ threads []core.Thread }
	postsMsg     struct{ posts []core.Post }
	postPollsMsg struct {
		thread string
		polls  map[string]*core.Poll
		err    error
	}
	pollMsg struct {
		postID string
		poll   *core.Poll
		open   bool
		err    error
	}
	searchMsg        struct{ posts []core.Post }
	notificationsMsg struct {
		notifications []core.Notification
		unread        int
	}
	notificationStatusMsg struct{ unread int }
	disconnectMsg         struct{}
	postSubmittedMsg      struct{ thread string }
	threadSubmittedMsg    struct {
		board  string
		thread string
	}
	pollPermissionMsg struct {
		canCreatePoll bool
		err           error
	}
)

// model is the root bubbletea model.
type model struct {
	c     *core.Core
	actor *core.User
	sub   *core.Subscription

	page      page
	pageStack []page // navigation history for back/esc
	width     int
	height    int
	statusMsg string

	currentBoard  string
	currentThread string

	// Component state.
	list        list.Model
	vp          viewport.Model
	compose     textarea.Model
	titleInput  textinput.Model
	searchQuery string

	// Compose mode: true = creating new thread, false = replying
	composingNewThread bool

	// In-memory state.
	boards        []core.Board
	threads       []core.Thread
	posts         []core.Post
	postReactions map[string]bool
	selectedPost  int
	postPolls     map[string]*core.Poll
	currentPoll   string // postID currently shown in pagePoll
	chat          []chatLine
	canCreatePoll bool
	trustLoaded   bool
	// Notifications tracked in the current actor session.
	notifications []core.Notification
	unreadNotifs  int
	supportsANSI  bool
}

type chatLine struct {
	user string
	text string
	ts   int64
}

// Styles.
var (
	styleTitle    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleHeader   = lipgloss.NewStyle().Background(lipgloss.Color("236")).Foreground(lipgloss.Color("252")).Padding(0, 1)
	styleStatus   = lipgloss.NewStyle().Foreground(lipgloss.Color("215"))
	stylePostSep  = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).SetString(strings.Repeat("─", 80))
	styleAuthor   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("32"))
	styleRedacted = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
	styleChatUser = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("178"))
)

func (m *model) styled(style lipgloss.Style, value string) string {
	if m == nil || m.supportsANSI {
		return style.Render(value)
	}
	return value
}

func (m *model) postSepLine() string {
	if m == nil || m.supportsANSI {
		return stylePostSep.String()
	}
	return strings.Repeat("─", 80)
}

func newModel(c *core.Core, actor *core.User, width, height int, supportsANSI bool) model {
	scopes := []string{"board:general", "chat:lobby", "presence:global"}
	sub := c.Subscribe(scopes)

	ti := textinput.New()
	ti.Placeholder = "Thread title…"
	ti.CharLimit = 200

	m := model{
		c:             c,
		actor:         actor,
		sub:           sub,
		page:          pageBoardList,
		width:         width,
		height:        height,
		titleInput:    ti,
		selectedPost:  -1,
		postReactions: make(map[string]bool),
		postPolls:     make(map[string]*core.Poll),
		supportsANSI:  supportsANSI,
	}

	m.list = list.New(nil, list.NewDefaultDelegate(), width, height-3)
	m.list.SetShowHelp(false)

	m.vp = viewport.New(width, height-4)
	m.vp.Style = lipgloss.NewStyle()

	m.compose = textarea.New()
	m.compose.Placeholder = "Write your post… (Ctrl+S to submit, Esc to cancel)"
	m.compose.SetWidth(width - 4)
	m.compose.SetHeight(height / 3)

	return m
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
	return tea.Batch(
		m.fetchBoards(),
		m.fetchNotificationStatus(),
		m.fetchPollPermission(),
		m.awaitEvent(),
	)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-3)
		m.vp.Width = msg.Width
		m.vp.Height = msg.Height - 4
		m.compose.SetWidth(msg.Width - 4)
		m.compose.SetHeight(msg.Height / 3)
		m.titleInput.Width = msg.Width - 4

	case boardsMsg:
		m.boards = msg.boards
		m.rebuildList()

	case threadsMsg:
		m.threads = msg.threads
		m.rebuildList()

	case postsMsg:
		m.posts = msg.posts
		if err := m.hydrateReactionState(); err != nil {
			m.statusMsg = "error: " + err.Error()
			return m, nil
		}
		m.postPolls = make(map[string]*core.Poll)
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
			m.statusMsg = "error: " + msg.err.Error()
			return m, nil
		}
		if msg.thread != m.currentThread {
			return m, nil
		}
		m.postPolls = msg.polls
		if m.postPolls == nil {
			m.postPolls = make(map[string]*core.Poll)
		}
		m.rebuildPostView()

	case pollMsg:
		if msg.err != nil {
			m.statusMsg = "error: " + msg.err.Error()
			return m, nil
		}
		if msg.poll == nil {
			m.statusMsg = "selected post has no active poll"
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

	case eventMsg:
		cmds = append(cmds, m.awaitEvent()) // re-arm listener
		cmds = append(cmds, m.handleEvent(msg.evt)...)

	case disconnectMsg:
		m.statusMsg = "disconnected"

	case postSubmittedMsg:
		m.finishCompose()
		if m.page == pageCompose {
			m.popPage()
		}
		if msg.thread != "" {
			m.currentThread = msg.thread
			cmds = append(cmds, m.fetchPosts(msg.thread))
		}
		m.statusMsg = "post submitted"
		cmds = append(cmds, m.fetchNotificationStatus())

	case threadSubmittedMsg:
		m.finishCompose()
		if m.page == pageCompose {
			m.popPage()
		}
		if msg.board != "" {
			m.currentBoard = msg.board
			cmds = append(cmds, m.fetchThreads(msg.board))
		}
		if msg.thread != "" {
			m.currentThread = msg.thread
			m.posts = nil
			m.pushPage(pageThread)
			cmds = append(cmds, m.fetchPosts(msg.thread), m.resubscribeThread(msg.thread))
		}
		m.statusMsg = "thread submitted"
		cmds = append(cmds, m.fetchNotificationStatus())

	case errMsg:
		m.statusMsg = "error: " + msg.err.Error()

	case pollPermissionMsg:
		if msg.err != nil {
			m.statusMsg = "error: " + msg.err.Error()
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
		cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

		// Delegate to the component that was active when the key arrived.
		switch activePage {
		case pageBoardList, pageThreadList, pageNotifications:
			var c tea.Cmd
			m.list, c = m.list.Update(msg)
			cmds = append(cmds, c)
		case pageThread, pagePoll, pageChat, pageSearch:
			var c tea.Cmd
			m.vp, c = m.vp.Update(msg)
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
		}
		return m, tea.Batch(cmds...)
	}

	// Non-key messages also need component updates.
	switch m.page {
	case pageBoardList, pageThreadList:
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
	}

	return m, tea.Batch(cmds...)
}

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	key := keyString(msg)
	switch m.page {
	case pageBoardList:
		switch key {
		case "enter", " ", "right":
			if b := m.selectedBoard(); b != nil {
				m.currentBoard = b.ID
				m.pushPage(pageThreadList)
				m.threads = nil
				m.rebuildList()
				return m.fetchThreads(b.ID)
			}
		case "c":
			m.pushPage(pageChat)
			m.rebuildChatView()
		case "N":
			m.pushPage(pageNotifications)
			m.notifications = nil
			m.rebuildList()
			return m.fetchNotifications()
		case "/":
			m.pushPage(pageSearch)
			m.searchQuery = ""
			m.vp.SetContent(m.styled(styleDim, "Type your query and press Enter to search…"))
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageThreadList:
		switch key {
		case "enter", " ", "right":
			if t := m.selectedThread(); t != nil {
				m.currentThread = t.ID
				m.pushPage(pageThread)
				m.posts = nil
				return tea.Batch(
					m.fetchPosts(t.ID),
					m.resubscribeThread(t.ID),
				)
			}
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
			m.vp.SetContent(m.styled(styleDim, "Type your query and press Enter to search…"))
		case "esc", "left":
			m.popPage()
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageThread:
		switch key {
		case "k", "up":
			m.moveSelectedPost(-1)
		case "j", "down":
			m.moveSelectedPost(1)
		case "n":
			m.composingNewThread = false
			m.compose.Reset()
			m.compose.Focus()
			m.pushPage(pageCompose)
		case "r":
			postID := m.selectedPostID()
			if postID == "" {
				m.statusMsg = "no post to react"
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
				m.statusMsg = "no post selected"
				return nil
			}
			return m.openPoll(postID)
		case "P":
			postID := m.selectedPostID()
			if postID == "" {
				m.statusMsg = "no post selected"
				return nil
			}
			return m.openPoll(postID)
		case "esc", "left":
			m.popPage()
			return m.fetchThreads(m.currentBoard)
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pagePoll:
		switch key {
		case "esc", "left":
			m.popPage()
		case "1", "2", "3", "4", "5", "6", "7", "8", "9":
			idx := int(key[0]-'0') - 1
			poll := m.currentPollData()
			if poll == nil {
				m.statusMsg = "poll not found"
				return nil
			}
			expired := poll.ExpiresAt != 0 && poll.ExpiresAt < time.Now().UnixMilli()
			if expired || poll.Voted != "" {
				m.statusMsg = "poll already voted or closed"
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
					m.statusMsg = "polls require trust level 2+"
				} else {
					m.statusMsg = "checking poll permission…"
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = "focus body first"
				return nil
			}
			m.insertPollTemplate()
			return nil
		case "ctrl+e":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = "polls require trust level 2+"
				} else {
					m.statusMsg = "checking poll permission…"
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = "focus body first"
				return nil
			}
			m.insertPollTemplateWithExpires("1h")
			return nil
		case "ctrl+d":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = "polls require trust level 2+"
				} else {
					m.statusMsg = "checking poll permission…"
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = "focus body first"
				return nil
			}
			m.insertPollTemplateWithExpires("1d")
			return nil
		case "ctrl+w":
			if !m.canCreatePoll {
				if m.trustLoaded {
					m.statusMsg = "polls require trust level 2+"
				} else {
					m.statusMsg = "checking poll permission…"
				}
				return nil
			}
			if m.composingNewThread && m.titleInput.Focused() {
				m.statusMsg = "focus body first"
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
						m.statusMsg = "title is required"
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
		case "esc", "left":
			m.popPage()
		case "q", "ctrl+c":
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
				m.vp.SetContent(m.styled(styleTitle, "Search: ") + m.searchQuery + "▌")
			}
		case "esc", "left":
			m.popPage()
		default:
			rawKey := msg.String()
			if len(rawKey) == 1 {
				m.searchQuery += rawKey
				m.vp.SetContent(m.styled(styleTitle, "Search: ") + m.searchQuery + "▌")
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
				m.statusMsg = "failed to mark notification read"
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
			m.statusMsg = "notification marked as read"
		case "a":
			if err := m.c.MarkAllNotificationsRead(m.actor.ID); err != nil {
				m.statusMsg = "failed to mark notifications read"
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
				m.statusMsg = "failed to delete notification"
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
			m.statusMsg = "notification deleted"
		case "x":
			if err := m.c.DeleteReadNotifications(m.actor.ID); err != nil {
				m.statusMsg = "failed to clear read notifications"
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
			m.statusMsg = "read notifications cleared"
		case "c":
			if err := m.c.DeleteAllNotifications(m.actor.ID); err != nil {
				m.statusMsg = "failed to clear notifications"
				return func() tea.Msg { return errMsg{err} }
			}
			m.notifications = nil
			m.unreadNotifs = 0
			m.rebuildList()
			m.statusMsg = "notifications cleared"
		case "esc", "left":
			m.popPage()
			m.rebuildList()
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
			m.posts = append(m.posts, core.Post{
				ID:            p.ID,
				Thread:        p.Thread,
				Author:        p.Author,
				AuthorID:      p.AuthorID,
				Body:          p.Body,
				ContentType:   p.ContentType,
				ReplyTo:       p.ReplyTo,
				ReactionCount: 0,
				CreatedSeq:    evt.Seq,
				UpdatedSeq:    evt.Seq,
				CreatedAt:     p.TS,
				UpdatedAt:     p.TS,
			})
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

	case proto.EvtThreadNew:
		if m.page == pageThreadList {
			cmds = append(cmds, m.fetchThreads(m.currentBoard))
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
			action := "locked"
			if !p.Locked {
				action = "unlocked"
			}
			m.statusMsg = fmt.Sprintf("thread %s by %s", action, p.By)
		}
		cmds = append(cmds, m.fetchNotificationStatus())
	}

	if cmds == nil {
		cmds = []tea.Cmd{m.fetchNotificationStatus()}
	}
	return cmds
}

func (m model) View() string {
	headerLabel := fmt.Sprintf(" BudgieBBS | %s | %s ", m.actor.Name, pageName(m.page))
	if m.unreadNotifs > 0 {
		headerLabel += fmt.Sprintf(" ● %d unread", m.unreadNotifs)
	}
	header := m.styled(styleHeader, headerLabel)
	status := m.styled(styleDim, m.statusMsg)

	var body string
	switch m.page {
	case pageBoardList:
		body = m.list.View() + "\n" + m.styled(styleDim, "enter/→=open board  c=chat  N=notifications  /=search  q=quit")
	case pageThreadList:
		body = m.list.View() + "\n" + m.styled(styleDim, "enter/→=open thread  n=new thread  /=search  esc/←=back  q=quit")
	case pageNotifications:
		body = m.list.View() + "\n" + m.styled(styleDim, "enter/→=mark read  a=mark all read  d=delete  x=clear read  c=clear all  esc/←=back")
	case pageThread:
		help := "n=reply  r=react  p=poll  ↑/↓=select  esc/←=back  q=quit"
		if m.actor.IsMod() {
			help = "n=reply  L=lock/unlock  r=react  p=poll  ↑/↓=select  esc/←=back  q=quit"
		}
		body = m.vp.View() + "\n" + m.styled(styleDim, help)
	case pagePoll:
		poll := m.currentPollData()
		if poll == nil {
			body = m.styled(styleDim, "No poll loaded")
		} else {
			help := "1-9 vote · esc/←=back"
			if poll.ExpiresAt > 0 && poll.ExpiresAt < time.Now().UnixMilli() {
				help = "poll closed · esc/←=back"
			}
			if poll.Voted != "" {
				help = "already voted · " + help
			}
			total := 0
			for _, opt := range poll.Options {
				total += opt.VoteCount
			}
			var b strings.Builder
			b.WriteString(m.styled(styleTitle, "Poll") + "\n\n")
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
			expires := "open"
			if poll.ExpiresAt > 0 {
				if poll.ExpiresAt < time.Now().UnixMilli() {
					expires = "closed"
				} else {
					expires = "closes " + time.UnixMilli(poll.ExpiresAt).Format("2006-01-02 15:04")
				}
			}
			b.WriteString("\n" + m.styled(styleDim, fmt.Sprintf("%d vote%s · %s", total, func() string {
				if total == 1 {
					return ""
				}
				return "s"
			}(), expires)) + "\n")
			body = b.String() + "\n" + m.styled(styleDim, help)
		}
		if body == "" {
			body = m.styled(styleDim, "No poll loaded")
		}
	case pageCompose:
		if m.composingNewThread {
			titleSection := m.styled(styleTitle, "New thread") + "\n\n" +
				m.styled(styleDim, "Title: ") + m.titleInput.View() + "\n\n"
			if m.titleInput.Focused() {
				body = titleSection + m.styled(styleDim, "Enter/Tab=next field  Esc=cancel")
			} else {
				body = titleSection + m.compose.View() + "\n" +
					m.styled(styleDim, composeHelpLine(m.canCreatePoll, m.trustLoaded))
			}
		} else {
			body = m.styled(styleTitle, "New reply") + "\n\n" + m.compose.View() +
				"\n" + m.styled(styleDim, composeHelpLine(m.canCreatePoll, m.trustLoaded))
		}
	case pageChat:
		body = m.vp.View() + "\n" + m.styled(styleDim, "esc/←=back")
	case pageSearch:
		body = m.vp.View() + "\n" + m.styled(styleDim, "type query  enter=search  esc/←=back")
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, body, status)
}

// --- List helpers ---

type boardItem struct{ b core.Board }

func (i boardItem) Title() string       { return i.b.Name }
func (i boardItem) Description() string { return i.b.Description }
func (i boardItem) FilterValue() string { return i.b.Name }

type threadItem struct{ t core.Thread }

func (i threadItem) Title() string { return i.t.Title }
func (i threadItem) Description() string {
	return fmt.Sprintf("by %s · %d posts", i.t.Author, i.t.PostCount)
}
func (i threadItem) FilterValue() string { return i.t.Title }

type notificationItem struct{ n core.Notification }

func notificationKindLabel(kind string) string {
	switch kind {
	case "mention":
		return "@ mention"
	case "reply":
		return "↩ reply"
	case "watched":
		return "👁 watched"
	default:
		return kind
	}
}

func (i notificationItem) Title() string {
	if i.n.Read {
		return fmt.Sprintf("%s in %s", notificationKindLabel(i.n.Kind), i.n.ThreadID)
	}
	return "● " + fmt.Sprintf("%s in %s", notificationKindLabel(i.n.Kind), i.n.ThreadID)
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

func (m *model) rebuildList() {
	switch m.page {
	case pageBoardList:
		items := make([]list.Item, len(m.boards))
		for i, b := range m.boards {
			items[i] = boardItem{b}
		}
		m.list.SetItems(items)
		m.list.Title = "Boards"
	case pageThreadList:
		items := make([]list.Item, len(m.threads))
		for i, t := range m.threads {
			items[i] = threadItem{t}
		}
		m.list.SetItems(items)
		m.list.Title = m.currentBoard
	case pageNotifications:
		items := make([]list.Item, len(m.notifications))
		for i, n := range m.notifications {
			items[i] = notificationItem{n}
		}
		m.list.SetItems(items)
		m.list.Title = "Notifications"
	}
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

func (m *model) selectedNotification() *core.Notification {
	sel, ok := m.list.SelectedItem().(notificationItem)
	if !ok {
		return nil
	}
	return &sel.n
}

func (m *model) selectedPostID() string {
	if m.selectedPost < 0 || m.selectedPost >= len(m.posts) {
		return ""
	}
	return m.posts[m.selectedPost].ID
}

func (m *model) currentPollData() *core.Poll {
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

// --- Post/chat view rendering ---

func (m *model) rebuildPostView() {
	var b strings.Builder
	for i, p := range m.posts {
		marker := "  "
		if i == m.selectedPost {
			marker = m.styled(styleDim, "→ ")
		}
		pollTag := ""
		if _, ok := m.postPolls[p.ID]; ok {
			pollTag = m.styled(styleDim, "  [poll]")
		}
		createdAt := p.CreatedAt
		if createdAt == 0 {
			createdAt = p.CreatedSeq
		}
		ts := fmt.Sprintf("#%d", p.CreatedSeq)
		if createdAt > 1_000_000_000_000 {
			ts = time.UnixMilli(createdAt).Format("2006-01-02 15:04")
		}
		author := m.styled(styleAuthor, p.Author)
		reactions := ""
		if p.ReactionCount > 0 {
			reactions = fmt.Sprintf("  ♥ %d", p.ReactionCount)
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  #%d%s%s", marker, author, m.styled(styleDim, ts), i+1, reactions, pollTag))
		if p.Version > 1 {
			b.WriteString(m.styled(styleDim, " (edited)"))
		}
		b.WriteString("\n")
		if p.Redacted {
			b.WriteString(m.styled(styleRedacted, "[removed by moderator]"))
		} else {
			b.WriteString(m.renderMarkup(p.Body))
		}
		b.WriteString("\n" + m.postSepLine() + "\n")
	}
	m.vp.SetContent(b.String())
}

func (m *model) rebuildChatView() {
	var b strings.Builder
	b.WriteString(m.styled(styleTitle, "Live Chat — #lobby") + "\n\n")
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
	boldStyle := lipgloss.NewStyle().Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	codeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

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

func (m model) fetchBoards() tea.Cmd {
	return func() tea.Msg {
		boards, err := m.c.ListBoards()
		if err != nil {
			return errMsg{err}
		}
		return boardsMsg{boards}
	}
}

func (m model) fetchThreads(board string) tea.Cmd {
	return func() tea.Msg {
		threads, err := m.c.ListThreads(board, 50, 0)
		if err != nil {
			return errMsg{err}
		}
		return threadsMsg{threads}
	}
}

func (m model) fetchPosts(thread string) tea.Cmd {
	return func() tea.Msg {
		posts, err := m.c.ListPosts(thread, 100, 0)
		if err != nil {
			return errMsg{err}
		}
		return postsMsg{posts}
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
			return postPollsMsg{thread: thread, polls: map[string]*core.Poll{}}
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

func composeHelpLine(canCreatePoll, trustLoaded bool) string {
	base := "Ctrl+S=submit  Esc=cancel"
	if canCreatePoll {
		return base + "  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll"
	}
	if trustLoaded {
		return base + "  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll (trust level 2+ required)"
	}
	return base + "  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll (checking permission…)"
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

func (m *model) rebuildSearchView(posts []core.Post) {
	var b strings.Builder
	b.WriteString(m.styled(styleTitle, fmt.Sprintf("Search results (%d)", len(posts))) + "\n\n")
	if len(posts) == 0 {
		b.WriteString(m.styled(styleDim, "No results found."))
	}
	for _, p := range posts {
		author := m.styled(styleAuthor, p.Author)
		b.WriteString(fmt.Sprintf("%s in thread %s\n", author, m.styled(styleDim, p.Thread)))
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
		return nil
	}
}

func pageName(p page) string {
	switch p {
	case pageBoardList:
		return "Boards"
	case pageThreadList:
		return "Threads"
	case pageThread:
		return "Thread"
	case pagePoll:
		return "Poll"
	case pageCompose:
		return "Compose"
	case pageChat:
		return "Chat"
	case pageSearch:
		return "Search"
	case pageNotifications:
		return "Notifications"
	}
	return ""
}
