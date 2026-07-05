// Package nntp exposes Budgie boards through a small NNTP-compatible gateway.
// It intentionally routes writes through core commands so old-school clients do
// not bypass forum permissions, sanctions, idempotency, or moderation.
package nntp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
	"github.com/juncoflockleader/budgie-bbs/internal/proto"
)

type Server struct {
	core   *core.Core
	addr   string
	domain string
	prefix string
}

func New(c *core.Core, addr, domain, prefix string) *Server {
	if domain == "" {
		domain = "budgie.local"
	}
	if prefix == "" {
		prefix = "budgie"
	}
	return &Server{core: c, addr: addr, domain: domain, prefix: prefix}
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	defer ln.Close()

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	slog.Info("NNTP server listening", "addr", s.addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handleConn(ctx, conn)
	}
}

type session struct {
	s           *Server
	conn        net.Conn
	r           *bufio.Reader
	w           *bufio.Writer
	pendingUser string
	actor       *projections.User
	group       string
	articles    []article
}

type article struct {
	Number int
	Thread projections.Thread
	Post   projections.Post
}

const (
	// nntpIdleTimeout closes connections that stall mid-read (Slowloris).
	nntpIdleTimeout = 5 * time.Minute
	// nntpMaxLineBytes bounds a single protocol line so an unterminated line
	// cannot grow the read buffer without limit.
	nntpMaxLineBytes = 64 << 10
	// nntpMaxArticleBytes bounds a POSTed article's accumulated size.
	nntpMaxArticleBytes = 2 << 20
)

var errNNTPLineTooLong = fmt.Errorf("nntp: line too long")

// readLine reads one CRLF-terminated line with an idle deadline and a hard
// length bound (ReadSlice, unlike ReadString, will not grow the buffer past its
// size — it returns ErrBufferFull, which we surface as a fatal line-too-long).
func (s *session) readLine() (string, error) {
	if s.conn != nil {
		_ = s.conn.SetReadDeadline(time.Now().Add(nntpIdleTimeout))
	}
	line, err := s.r.ReadSlice('\n')
	if err == bufio.ErrBufferFull {
		return "", errNNTPLineTooLong
	}
	return string(line), err
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	se := &session{
		s:    s,
		conn: conn,
		r:    bufio.NewReaderSize(conn, nntpMaxLineBytes),
		w:    bufio.NewWriter(conn),
	}
	se.writeLine("200 BudgieBBS NNTP gateway ready")
	for {
		line, err := se.readLine()
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		if !se.handleLine(ctx, line) {
			return
		}
	}
}

func (s *session) handleLine(ctx context.Context, line string) bool {
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return true
	}
	cmd := strings.ToUpper(parts[0])
	arg := strings.TrimSpace(strings.TrimPrefix(line, parts[0]))
	switch cmd {
	case "CAPABILITIES":
		s.writeLine("101 Capability list follows")
		s.writeLine("VERSION 2")
		s.writeLine("READER")
		s.writeLine("POST")
		s.writeLine("NEWNEWS")
		s.writeLine("AUTHINFO USER")
		s.writeLine(".")
	case "QUIT":
		s.writeLine("205 closing connection")
		return false
	case "MODE":
		s.writeLine("200 reader mode")
	case "AUTHINFO":
		s.handleAuth(arg)
	case "LIST":
		s.handleList()
	case "GROUP":
		s.handleGroup(arg)
	case "ARTICLE":
		s.handleArticle(arg, "article")
	case "HEAD":
		s.handleArticle(arg, "head")
	case "BODY":
		s.handleArticle(arg, "body")
	case "LISTGROUP":
		s.handleGroup(arg)
	case "OVER", "XOVER":
		s.handleOverview()
	case "NEWNEWS":
		s.handleNewNews(arg)
	case "POST":
		s.handlePost(ctx)
	default:
		s.writeLine("500 command not recognized")
	}
	return true
}

func (s *session) handleAuth(arg string) {
	parts := strings.Fields(arg)
	if len(parts) < 2 {
		s.writeLine("501 syntax: AUTHINFO USER name / AUTHINFO PASS password")
		return
	}
	switch strings.ToUpper(parts[0]) {
	case "USER":
		s.pendingUser = parts[1]
		s.writeLine("381 password required")
	case "PASS":
		if s.pendingUser == "" {
			s.writeLine("482 send AUTHINFO USER first")
			return
		}
		user, err := s.s.core.AuthenticateUser(s.pendingUser, parts[1])
		if err != nil {
			s.writeLine("481 authentication rejected")
			return
		}
		s.actor = user
		s.writeLine("281 authentication accepted")
	default:
		s.writeLine("501 unknown AUTHINFO mode")
	}
}

// canRead reports whether this session's actor may read the given board. It
// enforces the same member-read-mode access control as the HTTP transport so
// NNTP cannot expose private/member-only boards.
func (s *session) canRead(boardID string) bool {
	ok, err := s.s.core.ActorCanReadBoardID(s.actor, boardID)
	return err == nil && ok
}

func (s *session) handleList() {
	boards, err := s.s.core.ListBoards()
	if err != nil {
		s.writeLine("503 backend error")
		return
	}
	s.writeLine("215 list of newsgroups follows")
	for _, b := range boards {
		if !s.canRead(b.ID) {
			continue
		}
		group := s.boardToGroup(b.ID)
		count := s.articleCount(b.ID)
		high := count
		low := 1
		if count == 0 {
			low = 0
		}
		s.writeLine(fmt.Sprintf("%s %d %d y", group, high, low))
	}
	s.writeLine(".")
}

func (s *session) handleGroup(arg string) {
	board := s.groupToBoard(strings.TrimSpace(arg))
	if board == "" {
		s.writeLine("411 no such newsgroup")
		return
	}
	// Member-read-mode boards are not selectable by non-members — this gates
	// ARTICLE/HEAD/BODY/OVER too, since they operate on the selected group.
	if !s.canRead(board) {
		s.writeLine("411 no such newsgroup")
		return
	}
	articles, err := s.loadArticles(board)
	if err != nil {
		s.writeLine("503 backend error")
		return
	}
	s.group = board
	s.articles = articles
	low := 1
	if len(articles) == 0 {
		low = 0
	}
	s.writeLine(fmt.Sprintf("211 %d %d %d %s", len(articles), low, len(articles), s.boardToGroup(board)))
}

func (s *session) handleArticle(arg, mode string) {
	if s.group == "" {
		s.writeLine("412 no newsgroup selected")
		return
	}
	n := 1
	if strings.TrimSpace(arg) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(arg))
		if err != nil {
			s.writeLine("423 no such article number")
			return
		}
		n = parsed
	}
	if n < 1 || n > len(s.articles) {
		s.writeLine("423 no such article number")
		return
	}
	a := s.articles[n-1]
	code := "220"
	if mode == "head" {
		code = "221"
	} else if mode == "body" {
		code = "222"
	}
	s.writeLine(fmt.Sprintf("%s %d <%s> article follows", code, n, s.messageID(a.Post.ID)))
	if mode != "body" {
		s.writeHeaders(a)
	}
	if mode == "article" {
		s.writeLine("")
	}
	if mode != "head" {
		for _, line := range strings.Split(a.Post.Body, "\n") {
			if strings.HasPrefix(line, ".") {
				line = "." + line
			}
			s.writeLine(line)
		}
	}
	s.writeLine(".")
}

func (s *session) handleOverview() {
	if s.group == "" {
		s.writeLine("412 no newsgroup selected")
		return
	}
	s.writeLine("224 overview follows")
	for _, a := range s.articles {
		subject := a.Thread.Title
		from := a.Post.Author
		date := fmt.Sprintf("%s", nntpDate(a.Post.CreatedAt))
		msgID := "<" + s.messageID(a.Post.ID) + ">"
		s.writeLine(fmt.Sprintf("%d\t%s\t%s\t%s\t%s\t\t%d\t%d", a.Number, subject, from, date, msgID, len(a.Post.Body), 1))
	}
	s.writeLine(".")
}

// handleNewNews implements the NEWNEWS command (RFC 3977 §7.4).
// Syntax: NEWNEWS wildmat date time [GMT]
// wildmat is a newsgroup pattern (we accept * or a specific group).
// date/time are in YYYYMMDD HHMMSS format.
// Returns message-IDs of articles created at or after the given time.
func (s *session) handleNewNews(arg string) {
	parts := strings.Fields(arg)
	if len(parts) < 3 {
		s.writeLine("501 syntax: NEWNEWS wildmat date time [GMT]")
		return
	}
	wildmat := parts[0] // e.g. "*" or "budgie.general"
	dateStr := parts[1] // YYYYMMDD
	timeStr := parts[2] // HHMMSS
	asGMT := len(parts) >= 4 && strings.EqualFold(parts[3], "GMT")

	// Parse the date and time.
	layout := "20060102 150405"
	loc := time.Local
	if asGMT {
		loc = time.UTC
	}
	t, err := time.ParseInLocation(layout, dateStr+" "+timeStr, loc)
	if err != nil {
		s.writeLine("501 invalid date/time format; use YYYYMMDD HHMMSS")
		return
	}
	sinceMS := t.UnixMilli()

	// Enumerate boards matching the wildmat pattern.
	boards, err := s.s.core.ListBoards()
	if err != nil {
		s.writeLine("500 internal error")
		return
	}

	s.writeLine("230 list of new articles follows")
	for _, b := range boards {
		if !s.canRead(b.ID) {
			continue
		}
		group := s.boardToGroup(b.ID)
		if !nntpWildmatMatch(wildmat, group) {
			continue
		}
		articles, err := s.loadArticles(b.ID)
		if err != nil {
			continue
		}
		for _, a := range articles {
			if a.Post.CreatedAt >= sinceMS {
				s.writeLine("<" + s.messageID(a.Post.ID) + ">")
			}
		}
	}
	s.writeLine(".")
}

// nntpWildmatMatch returns true when the subject matches the NNTP wildmat
// pattern. Supports only '*' (match everything) and literal equality.
// A full wildmat implementation would handle '?' and character classes;
// this covers the common client cases.
func nntpWildmatMatch(pattern, subject string) bool {
	if pattern == "*" {
		return true
	}
	// Handle simple prefix wildcard: "budgie.*"
	if strings.HasSuffix(pattern, ".*") {
		prefix := strings.TrimSuffix(pattern, ".*")
		return strings.HasPrefix(subject, prefix+".")
	}
	return pattern == subject
}

func (s *session) handlePost(ctx context.Context) {
	if s.actor == nil {
		s.writeLine("480 authentication required")
		return
	}
	if s.group == "" {
		s.writeLine("412 no newsgroup selected")
		return
	}
	s.writeLine("340 send article; end with <CR-LF>.<CR-LF>")
	headers, body, err := s.readPostedArticle()
	if err != nil {
		s.writeLine("441 posting failed")
		return
	}
	subject := strings.TrimSpace(headers["subject"])
	if subject == "" {
		subject = "Untitled NNTP post"
	}

	parentID := firstPostFromMessageIDHeader(headers["references"])
	if parentID == "" {
		parentID = firstPostFromMessageIDHeader(headers["in-reply-to"])
	}

	payload, cmd := s.appendOrCreatePayload(parentID, subject, body)
	reply := s.s.core.ExecCmd(ctx, s.actor, cmd, payload, fmt.Sprintf("nntp_%d", time.Now().UnixNano()))
	if reply.Err != nil {
		s.writeLine("441 " + reply.Err.Message)
		return
	}
	s.writeLine("240 article received")
}

func (s *session) appendOrCreatePayload(parentID, subject, body string) ([]byte, proto.CommandName) {
	if parentID == "" {
		payload, _ := json.Marshal(proto.CreateThreadPayload{
			Board: s.group,
			Title: subject,
			Body:  body,
		})
		return payload, proto.CmdCreateThread
	}

	parent, err := s.s.core.GetPost(parentID)
	if err != nil || parent == nil {
		payload, _ := json.Marshal(proto.CreateThreadPayload{
			Board: s.group,
			Title: subject,
			Body:  body,
		})
		return payload, proto.CmdCreateThread
	}

	thread, err := s.s.core.GetThread(parent.Thread)
	if err != nil || thread == nil {
		payload, _ := json.Marshal(proto.CreateThreadPayload{
			Board: s.group,
			Title: subject,
			Body:  body,
		})
		return payload, proto.CmdCreateThread
	}

	if thread.Board != s.group {
		// Reference is for a post outside the selected group.
		payload, _ := json.Marshal(proto.CreateThreadPayload{
			Board: s.group,
			Title: subject,
			Body:  body,
		})
		return payload, proto.CmdCreateThread
	}

	payload, _ := json.Marshal(proto.AppendPostPayload{
		Thread:  thread.ID,
		Body:    body,
		ReplyTo: parent.ID,
	})
	return payload, proto.CmdAppendPost
}

func (s *session) readPostedArticle() (map[string]string, string, error) {
	headers := map[string]string{}
	var body []string
	inBody := false
	total := 0
	for {
		line, err := s.readLine()
		if err != nil {
			return nil, "", err
		}
		total += len(line)
		if total > nntpMaxArticleBytes {
			return nil, "", fmt.Errorf("nntp: article too large")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "." {
			break
		}
		if strings.HasPrefix(line, "..") {
			line = strings.TrimPrefix(line, ".")
		}
		if !inBody {
			if line == "" {
				inBody = true
				continue
			}
			k, v, ok := strings.Cut(line, ":")
			if ok {
				headers[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
			}
			continue
		}
		body = append(body, line)
	}
	return headers, strings.Join(body, "\n"), nil
}

func (s *session) loadArticles(board string) ([]article, error) {
	threads, err := s.s.core.ListThreads(board, 200, 0)
	if err != nil {
		return nil, err
	}
	var out []article
	for _, t := range threads {
		posts, err := s.s.core.ListPosts(t.ID, 500, 0)
		if err != nil {
			return nil, err
		}
		for _, p := range posts {
			if p.Redacted {
				continue
			}
			out = append(out, article{Number: len(out) + 1, Thread: t, Post: p})
		}
	}
	return out, nil
}

func (s *session) articleCount(board string) int {
	articles, err := s.loadArticles(board)
	if err != nil {
		return 0
	}
	return len(articles)
}

func (s *session) writeHeaders(a article) {
	s.writeLine("Subject: " + a.Thread.Title)
	s.writeLine("From: " + a.Post.Author)
	s.writeLine("Newsgroups: " + s.boardToGroup(a.Thread.Board))
	s.writeLine("Message-ID: <" + s.messageID(a.Post.ID) + ">")
	s.writeLine("Date: " + nntpDate(a.Post.CreatedAt))
}

func firstPostFromMessageIDHeader(raw string) string {
	for _, token := range strings.Fields(raw) {
		t := strings.TrimSpace(token)
		t = strings.TrimPrefix(t, "<")
		t = strings.TrimSuffix(t, ">")
		if at := strings.Index(t, "@"); at >= 0 {
			t = t[:at]
		}
		if t != "" {
			return t
		}
	}
	return ""
}

func nntpDate(ts int64) string {
	if ts <= 0 {
		return time.Unix(0, 0).Format(time.RFC1123Z)
	}
	return time.UnixMilli(ts).Format(time.RFC1123Z)
}

func (s *session) groupToBoard(group string) string {
	prefix := s.s.prefix + "."
	if !strings.HasPrefix(group, prefix) {
		return ""
	}
	return strings.TrimPrefix(group, prefix)
}

func (s *session) boardToGroup(board string) string {
	return s.s.prefix + "." + board
}

func (s *session) messageID(postID string) string {
	return postID + "@" + s.s.domain
}

func (s *session) writeLine(line string) {
	_, _ = s.w.WriteString(line + "\r\n")
	_ = s.w.Flush()
}
