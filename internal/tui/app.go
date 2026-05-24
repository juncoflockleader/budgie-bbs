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
	pageCompose
	pageChat
	pageSearch
)

// msg types for the bubbletea update cycle.
type (
	eventMsg      struct{ evt *proto.Event }
	errMsg        struct{ err error }
	boardsMsg     struct{ boards []core.Board }
	threadsMsg    struct{ threads []core.Thread }
	postsMsg      struct{ posts []core.Post }
	searchMsg     struct{ posts []core.Post }
	disconnectMsg struct{}
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
	boards  []core.Board
	threads []core.Thread
	posts   []core.Post
	chat    []chatLine
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

func newModel(c *core.Core, actor *core.User, width, height int) model {
	scopes := []string{"board:general", "chat:lobby", "presence:global"}
	sub := c.Subscribe(scopes)

	ti := textinput.New()
	ti.Placeholder = "Thread title…"
	ti.CharLimit = 200

	m := model{
		c:          c,
		actor:      actor,
		sub:        sub,
		page:       pageBoardList,
		width:      width,
		height:     height,
		titleInput: ti,
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
		m.rebuildPostView()

	case searchMsg:
		m.rebuildSearchView(msg.posts)

	case eventMsg:
		cmds = append(cmds, m.awaitEvent()) // re-arm listener
		cmds = append(cmds, m.handleEvent(msg.evt)...)

	case disconnectMsg:
		m.statusMsg = "disconnected"

	case errMsg:
		m.statusMsg = "error: " + msg.err.Error()

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
		case pageBoardList, pageThreadList:
			var c tea.Cmd
			m.list, c = m.list.Update(msg)
			cmds = append(cmds, c)
		case pageThread, pageChat, pageSearch:
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
	case pageThread, pageChat, pageSearch:
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

func (m *model) handleKey(msg tea.KeyMsg) tea.Cmd {
	switch m.page {
	case pageBoardList:
		switch msg.String() {
		case "enter", " ":
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
		case "/":
			m.pushPage(pageSearch)
			m.searchQuery = ""
			m.vp.SetContent(styleDim.Render("Type your query and press Enter to search…"))
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageThreadList:
		switch msg.String() {
		case "enter", " ":
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
			m.vp.SetContent(styleDim.Render("Type your query and press Enter to search…"))
		case "esc", "left":
			m.popPage()
			m.rebuildList()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageThread:
		switch msg.String() {
		case "n":
			m.composingNewThread = false
			m.compose.Reset()
			m.compose.Focus()
			m.pushPage(pageCompose)
		case "L":
			if m.actor.IsMod() {
				return m.toggleThreadLock()
			}
		case "esc", "left":
			m.popPage()
			return m.fetchThreads(m.currentBoard)
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageCompose:
		switch msg.String() {
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
		switch msg.String() {
		case "esc", "left":
			m.popPage()
		case "q", "ctrl+c":
			m.c.Unsubscribe(m.sub)
			return tea.Quit
		}

	case pageSearch:
		switch msg.String() {
		case "enter":
			if m.searchQuery != "" {
				return m.runSearch(m.searchQuery)
			}
		case "backspace":
			if len(m.searchQuery) > 0 {
				m.searchQuery = m.searchQuery[:len(m.searchQuery)-1]
				m.vp.SetContent(styleTitle.Render("Search: ") + m.searchQuery + "▌")
			}
		case "esc", "left":
			m.popPage()
		default:
			if len(msg.String()) == 1 {
				m.searchQuery += msg.String()
				m.vp.SetContent(styleTitle.Render("Search: ") + m.searchQuery + "▌")
			}
		}
	}
	return nil
}

func (m *model) handleEvent(evt *proto.Event) []tea.Cmd {
	switch evt.Kind {
	case proto.EvtPostAppended:
		p, ok := evt.Payload.(*proto.PostAppendedPayload)
		if !ok {
			return nil
		}
		if p.Thread == m.currentThread {
			m.posts = append(m.posts, core.Post{
				ID:          p.ID,
				Thread:      p.Thread,
				Author:      p.Author,
				Body:        p.Body,
				ContentType: p.ContentType,
				ReplyTo:     p.ReplyTo,
				CreatedSeq:  evt.Seq,
				UpdatedSeq:  evt.Seq,
			})
			m.rebuildPostView()
			m.vp.GotoBottom()
		}
		for i, t := range m.threads {
			if t.ID == p.Thread {
				m.threads[i].PostCount++
				m.threads[i].LastSeq = evt.Seq
			}
		}

	case proto.EvtChatLine:
		p, ok := evt.Payload.(*proto.ChatLinePayload)
		if !ok {
			return nil
		}
		m.chat = append(m.chat, chatLine{user: p.User, text: p.Text, ts: p.TS})
		if len(m.chat) > 200 {
			m.chat = m.chat[len(m.chat)-200:]
		}
		if m.page == pageChat {
			m.rebuildChatView()
			m.vp.GotoBottom()
		}

	case proto.EvtThreadNew:
		if m.page == pageThreadList {
			return []tea.Cmd{m.fetchThreads(m.currentBoard)}
		}

	case proto.EvtPostEdited:
		p, ok := evt.Payload.(*proto.PostEditedPayload)
		if !ok {
			return nil
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

	case proto.EvtPostRedacted:
		p, ok := evt.Payload.(*proto.PostRedactedPayload)
		if !ok {
			return nil
		}
		for i, post := range m.posts {
			if post.ID == p.ID {
				m.posts[i].Redacted = true
			}
		}
		if m.page == pageThread {
			m.rebuildPostView()
		}

	case proto.EvtThreadLocked:
		p, ok := evt.Payload.(*proto.ThreadLockedPayload)
		if !ok {
			return nil
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
	}
	return nil
}

func (m model) View() string {
	header := styleHeader.Render(
		fmt.Sprintf(" BudgieBBS | %s | %s ", m.actor.Name, pageName(m.page)),
	)
	status := styleDim.Render(m.statusMsg)

	var body string
	switch m.page {
	case pageBoardList:
		body = m.list.View()
	case pageThreadList:
		body = m.list.View()
	case pageThread:
		help := "n=reply  esc/←=back  q=quit"
		if m.actor.IsMod() {
			help = "n=reply  L=lock/unlock  esc/←=back  q=quit"
		}
		body = m.vp.View() + "\n" + styleDim.Render(help)
	case pageCompose:
		if m.composingNewThread {
			titleSection := styleTitle.Render("New thread") + "\n\n" +
				styleDim.Render("Title: ") + m.titleInput.View() + "\n\n"
			if m.titleInput.Focused() {
				body = titleSection + styleDim.Render("Enter/Tab=next field  Esc=cancel")
			} else {
				body = titleSection + m.compose.View() + "\n" +
					styleDim.Render("Ctrl+S=submit  Esc=cancel")
			}
		} else {
			body = styleTitle.Render("New reply") + "\n\n" + m.compose.View() +
				"\n" + styleDim.Render("Ctrl+S=submit  Esc=cancel")
		}
	case pageChat:
		body = m.vp.View() + "\n" + styleDim.Render("esc/←=back")
	case pageSearch:
		body = m.vp.View() + "\n" + styleDim.Render("type query  enter=search  esc/←=back")
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

// --- Post/chat view rendering ---

func (m *model) rebuildPostView() {
	var b strings.Builder
	for i, p := range m.posts {
		ts := time.UnixMilli(p.CreatedSeq).Format("2006-01-02 15:04")
		author := styleAuthor.Render(p.Author)
		b.WriteString(fmt.Sprintf("%s  %s  #%d", author, styleDim.Render(ts), i+1))
		if p.Version > 1 {
			b.WriteString(styleDim.Render(" (edited)"))
		}
		b.WriteString("\n")
		if p.Redacted {
			b.WriteString(styleRedacted.Render("[removed by moderator]"))
		} else {
			b.WriteString(renderMarkup(p.Body))
		}
		b.WriteString("\n" + stylePostSep.String() + "\n")
	}
	m.vp.SetContent(b.String())
}

func (m *model) rebuildChatView() {
	var b strings.Builder
	b.WriteString(styleTitle.Render("Live Chat — #lobby") + "\n\n")
	for _, line := range m.chat {
		ts := time.UnixMilli(line.ts).Format("15:04")
		b.WriteString(fmt.Sprintf("%s %s %s\n",
			styleDim.Render(ts),
			styleChatUser.Render(line.user+":"),
			line.text,
		))
	}
	m.vp.SetContent(b.String())
}

// renderMarkup converts the constrained Markdown subset to ANSI-friendly text.
func renderMarkup(body string) string {
	boldStyle := lipgloss.NewStyle().Bold(true)
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	codeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	lines := strings.Split(body, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "> ") {
			out = append(out, dimStyle.Render("│ "+line[2:]))
			continue
		}
		line = replaceInline(line, "**", func(s string) string { return boldStyle.Render(s) })
		line = replaceInline(line, "`", func(s string) string { return codeStyle.Render(s) })
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

func (m model) resubscribeThread(threadID string) tea.Cmd {
	return func() tea.Msg {
		m.c.Bus.AddScopes(m.sub, []string{"thread:" + threadID})
		return nil
	}
}

func (m model) submitPost(body string) tea.Cmd {
	return func() tea.Msg {
		p := proto.AppendPostPayload{Thread: m.currentThread, Body: body}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdAppendPost, raw, "")
		if reply.Err != nil {
			return errMsg{fmt.Errorf("%s", reply.Err.Message)}
		}
		return nil
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
		p := proto.CreateThreadPayload{Board: board, Title: title, Body: body}
		raw, err := json.Marshal(p)
		if err != nil {
			return errMsg{err}
		}
		reply := m.c.ExecCmd(context.Background(), m.actor, proto.CmdCreateThread, raw, "")
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

func (m *model) rebuildSearchView(posts []core.Post) {
	var b strings.Builder
	b.WriteString(styleTitle.Render(fmt.Sprintf("Search results (%d)", len(posts))) + "\n\n")
	if len(posts) == 0 {
		b.WriteString(styleDim.Render("No results found."))
	}
	for _, p := range posts {
		author := styleAuthor.Render(p.Author)
		b.WriteString(fmt.Sprintf("%s in thread %s\n", author, styleDim.Render(p.Thread)))
		b.WriteString(renderMarkup(p.Body))
		b.WriteString("\n" + stylePostSep.String() + "\n")
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
	case pageCompose:
		return "Compose"
	case pageChat:
		return "Chat"
	case pageSearch:
		return "Search"
	}
	return ""
}
