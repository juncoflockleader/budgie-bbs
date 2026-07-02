package core

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"sync"
)

const memoryPresenceCoalesceWindowMS int64 = 30 * 1000

// MemoryPresenceStore is a test-only non-SQL PresenceStore fixture. It keeps
// high-churn presence sessions in memory while reading durable user/profile
// metadata from SQL for roster responses. Production backends are sql and
// nats-kv (see -presence-store).
type MemoryPresenceStore struct {
	mu                sync.Mutex
	db                *sql.DB
	sessions          map[memoryPresenceKey]memoryPresenceSession
	guestSessions     map[string]memoryGuestPresenceSession
	totalGuestLogins  int
	totalGuestLogouts int
}

type memoryPresenceKey struct {
	userID    string
	sessionID string
}

type memoryPresenceSession struct {
	UserID        string
	SessionID     string
	Status        string
	Mode          string
	BoardID       string
	ThreadID      string
	LocationLabel string
	FromHost      string
	LastSeen      int64
	UpdatedAt     int64
}

type memoryGuestPresenceSession struct {
	SessionID     string
	Status        string
	LocationLabel string
	FromHost      string
	LastSeen      int64
	UpdatedAt     int64
}

func NewMemoryPresenceStore() *MemoryPresenceStore {
	return &MemoryPresenceStore{
		sessions:      map[memoryPresenceKey]memoryPresenceSession{},
		guestSessions: map[string]memoryGuestPresenceSession{},
	}
}

func (s *MemoryPresenceStore) BindPresenceStoreDB(db *sql.DB) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
}

func (s *MemoryPresenceStore) SetUserPresence(userID, sessionID, status, mode, boardID, threadID, locationLabel, fromHost string, ts int64) error {
	if s == nil {
		return sql.ErrConnDone
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return fmt.Errorf("presence user id required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		sessionID = "default"
	}
	status = strings.TrimSpace(status)
	mode = strings.TrimSpace(mode)
	boardID = strings.TrimSpace(boardID)
	threadID = strings.TrimSpace(threadID)
	locationLabel = strings.TrimSpace(locationLabel)
	fromHost = strings.TrimSpace(fromHost)
	if ts <= 0 {
		ts = nowMS()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	key := memoryPresenceKey{userID: userID, sessionID: sessionID}
	previous, hadPrevious := s.sessions[key]
	if hadPrevious &&
		memoryPresenceStatusCountsOnline(status) &&
		previous.Status == status &&
		previous.Mode == mode &&
		previous.BoardID == boardID &&
		previous.ThreadID == threadID &&
		previous.LocationLabel == locationLabel &&
		previous.FromHost == fromHost &&
		memoryPresenceWithinCoalesceWindow(previous.LastSeen, ts) {
		return nil
	}
	s.sessions[key] = memoryPresenceSession{
		UserID:        userID,
		SessionID:     sessionID,
		Status:        status,
		Mode:          mode,
		BoardID:       boardID,
		ThreadID:      threadID,
		LocationLabel: locationLabel,
		FromHost:      fromHost,
		LastSeen:      ts,
		UpdatedAt:     ts,
	}
	return nil
}

func (s *MemoryPresenceStore) SetGuestPresence(sessionID, status, locationLabel, fromHost string, ts int64) error {
	if s == nil {
		return sql.ErrConnDone
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("guest session id required")
	}
	if len(sessionID) > 120 {
		sessionID = sessionID[:120]
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}
	if len(status) > 40 {
		status = status[:40]
	}
	locationLabel = strings.TrimSpace(locationLabel)
	if len(locationLabel) > 120 {
		locationLabel = locationLabel[:120]
	}
	fromHost = strings.TrimSpace(fromHost)
	if len(fromHost) > 120 {
		fromHost = fromHost[:120]
	}
	if ts <= 0 {
		ts = nowMS()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	previous, hadPrevious := s.guestSessions[sessionID]
	wasOnline := hadPrevious && memoryGuestPresenceStatusCountsOnline(previous.Status)
	isOnline := memoryGuestPresenceStatusCountsOnline(status)
	if hadPrevious &&
		isOnline &&
		previous.Status == status &&
		previous.LocationLabel == locationLabel &&
		previous.FromHost == fromHost &&
		memoryPresenceWithinCoalesceWindow(previous.LastSeen, ts) {
		return nil
	}
	if !wasOnline && isOnline {
		s.totalGuestLogins++
	} else if wasOnline && !isOnline {
		s.totalGuestLogouts++
	}
	s.guestSessions[sessionID] = memoryGuestPresenceSession{
		SessionID:     sessionID,
		Status:        status,
		LocationLabel: locationLabel,
		FromHost:      fromHost,
		LastSeen:      ts,
		UpdatedAt:     ts,
	}
	return nil
}

func (s *MemoryPresenceStore) ListOnlineUsers(viewerID, boardID string, limit, offset int) ([]SocialUser, error) {
	if s == nil {
		return nil, sql.ErrConnDone
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	boardID = strings.TrimSpace(boardID)
	cutoff := nowMS() - 5*60*1000
	viewerCanSeeCloak := s.viewerCanSeeCloak(viewerID)

	s.mu.Lock()
	sessions := make([]memoryPresenceSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		if session.LastSeen < cutoff {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(session.Status))
		if status == "" || status == "offline" || status == "invisible" {
			continue
		}
		if (status == "cloak" || status == "cloaked") && !viewerCanSeeCloak {
			continue
		}
		if boardID != "" && session.BoardID != boardID {
			continue
		}
		sessions = append(sessions, session)
	}
	s.mu.Unlock()

	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].LastSeen != sessions[j].LastSeen {
			return sessions[i].LastSeen > sessions[j].LastSeen
		}
		if sessions[i].UserID != sessions[j].UserID {
			return sessions[i].UserID < sessions[j].UserID
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	if offset >= len(sessions) {
		return []SocialUser{}, nil
	}
	end := offset + limit
	if end > len(sessions) {
		end = len(sessions)
	}

	now := nowMS()
	out := make([]SocialUser, 0, end-offset)
	for _, session := range sessions[offset:end] {
		user := SocialUser{
			UserID:        session.UserID,
			Name:          session.UserID,
			DisplayName:   session.UserID,
			SessionID:     session.SessionID,
			Kind:          "online",
			UpdatedAt:     session.UpdatedAt,
			Status:        session.Status,
			LastSeen:      session.LastSeen,
			Mode:          session.Mode,
			BoardID:       session.BoardID,
			ThreadID:      session.ThreadID,
			LocationLabel: session.LocationLabel,
			FromHost:      session.FromHost,
			Online:        true,
		}
		if user.LastSeen > 0 {
			user.IdleSeconds = (now - user.LastSeen) / 1000
		}
		keep, err := s.decorateOnlineUser(viewerID, &user)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, user)
		}
	}
	return out, nil
}

func (s *MemoryPresenceStore) ListChatOnlineUsers(viewerID, roomID string, limit, offset int) ([]SocialUser, error) {
	if s == nil {
		return nil, sql.ErrConnDone
	}
	roomID = strings.TrimSpace(roomID)
	if roomID == "" {
		roomID = "lobby"
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	sessions := s.chatSessions(viewerID, roomID)
	if offset >= len(sessions) {
		return []SocialUser{}, nil
	}
	end := offset + limit
	if end > len(sessions) {
		end = len(sessions)
	}
	now := nowMS()
	out := make([]SocialUser, 0, end-offset)
	for _, session := range sessions[offset:end] {
		user := SocialUser{
			UserID:        session.UserID,
			Name:          session.UserID,
			DisplayName:   session.UserID,
			SessionID:     session.SessionID,
			Kind:          "chat",
			UpdatedAt:     session.UpdatedAt,
			Status:        session.Status,
			LastSeen:      session.LastSeen,
			Mode:          session.Mode,
			BoardID:       session.BoardID,
			ThreadID:      session.ThreadID,
			LocationLabel: session.LocationLabel,
			FromHost:      session.FromHost,
			Online:        true,
		}
		if user.LastSeen > 0 {
			user.IdleSeconds = (now - user.LastSeen) / 1000
		}
		keep, err := s.decorateOnlineUser(viewerID, &user)
		if err != nil {
			return nil, err
		}
		if keep {
			out = append(out, user)
		}
	}
	return out, nil
}

func (s *MemoryPresenceStore) ChatOnlineCounts() (map[string]int, error) {
	if s == nil {
		return nil, sql.ErrConnDone
	}
	cutoff := nowMS() - 5*60*1000
	s.mu.Lock()
	defer s.mu.Unlock()
	byRoom := map[string]map[string]bool{}
	for _, session := range s.sessions {
		if session.LastSeen < cutoff || strings.ToLower(strings.TrimSpace(session.Mode)) != "chat" || strings.TrimSpace(session.LocationLabel) == "" || !memoryPresenceStatusCountsOnline(session.Status) {
			continue
		}
		users := byRoom[session.LocationLabel]
		if users == nil {
			users = map[string]bool{}
			byRoom[session.LocationLabel] = users
		}
		users[session.UserID] = true
	}
	counts := map[string]int{}
	for roomID, users := range byRoom {
		counts[roomID] = len(users)
	}
	return counts, nil
}

func (s *MemoryPresenceStore) chatSessions(viewerID, roomID string) []memoryPresenceSession {
	cutoff := nowMS() - 5*60*1000
	viewerCanSeeCloak := s.viewerCanSeeCloak(viewerID)
	s.mu.Lock()
	defer s.mu.Unlock()
	sessions := make([]memoryPresenceSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		if session.LastSeen < cutoff ||
			strings.ToLower(strings.TrimSpace(session.Mode)) != "chat" ||
			session.LocationLabel != roomID {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(session.Status))
		if status == "" || status == "offline" || status == "invisible" {
			continue
		}
		if (status == "cloak" || status == "cloaked") && !viewerCanSeeCloak {
			continue
		}
		sessions = append(sessions, session)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		if sessions[i].LastSeen != sessions[j].LastSeen {
			return sessions[i].LastSeen > sessions[j].LastSeen
		}
		if sessions[i].UserID != sessions[j].UserID {
			return sessions[i].UserID < sessions[j].UserID
		}
		return sessions[i].SessionID < sessions[j].SessionID
	})
	return sessions
}

func (s *MemoryPresenceStore) Stats() (PresenceStats, error) {
	if s == nil {
		return PresenceStats{}, sql.ErrConnDone
	}
	cutoff := nowMS() - 5*60*1000
	s.mu.Lock()
	defer s.mu.Unlock()
	onlineUsers := map[string]bool{}
	for _, session := range s.sessions {
		if session.LastSeen >= cutoff && memoryPresenceStatusCountsOnline(session.Status) {
			onlineUsers[session.UserID] = true
		}
	}
	onlineGuests := 0
	for _, session := range s.guestSessions {
		if session.LastSeen >= cutoff && memoryGuestPresenceStatusCountsOnline(session.Status) {
			onlineGuests++
		}
	}
	return PresenceStats{
		OnlineUsers:       len(onlineUsers),
		OnlineGuests:      onlineGuests,
		TotalGuestLogins:  s.totalGuestLogins,
		TotalGuestLogouts: s.totalGuestLogouts,
	}, nil
}

func (s *MemoryPresenceStore) viewerCanSeeCloak(viewerID string) bool {
	if s == nil || s.db == nil || strings.TrimSpace(viewerID) == "" {
		return false
	}
	var role string
	if err := qQueryRow(s.db, `SELECT role FROM users WHERE id=?`, viewerID).Scan(&role); err != nil {
		return false
	}
	role = strings.ToLower(strings.TrimSpace(role))
	return role == "moderator" || role == "admin"
}

func (s *MemoryPresenceStore) decorateOnlineUser(viewerID string, user *SocialUser) (bool, error) {
	if s == nil || s.db == nil || user == nil {
		return true, nil
	}
	err := qQueryRow(s.db,
		`SELECT u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name)
		   FROM users u
		   LEFT JOIN user_profiles up ON up.user_id = u.id
		  WHERE u.id=?`,
		user.UserID,
	).Scan(&user.Name, &user.Role, &user.DisplayName)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if user.BoardID != "" {
		err = qQueryRow(s.db, `SELECT name FROM boards WHERE id=?`, user.BoardID).Scan(&user.BoardName)
		if err != nil && err != sql.ErrNoRows {
			return false, err
		}
	}
	if strings.TrimSpace(viewerID) == "" {
		return true, nil
	}
	var mutual, ignored int
	err = qQueryRow(s.db,
		`SELECT
		     CASE WHEN EXISTS (
		       SELECT 1 FROM user_relationships mine
		        WHERE mine.user_id = ? AND mine.target_user_id = ? AND mine.kind='friend'
		     ) AND EXISTS (
		       SELECT 1 FROM user_relationships back
		        WHERE back.user_id = ? AND back.target_user_id = ? AND back.kind='friend'
		     ) THEN 1 ELSE 0 END,
		     CASE WHEN EXISTS (
		       SELECT 1 FROM user_relationships ig
		        WHERE ig.user_id = ? AND ig.target_user_id = ? AND ig.kind='ignore'
		     ) THEN 1 ELSE 0 END`,
		viewerID,
		user.UserID,
		user.UserID,
		viewerID,
		viewerID,
		user.UserID,
	).Scan(&mutual, &ignored)
	if err != nil {
		return false, err
	}
	user.Mutual = mutual != 0
	user.Ignored = ignored != 0
	return true, nil
}

func memoryPresenceStatusCountsOnline(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "invisible", "cloak", "cloaked":
		return false
	default:
		return true
	}
}

func memoryGuestPresenceStatusCountsOnline(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "offline", "inactive":
		return false
	default:
		return true
	}
}

func memoryPresenceWithinCoalesceWindow(previousLastSeen, ts int64) bool {
	if previousLastSeen <= 0 {
		return false
	}
	return ts <= previousLastSeen || ts-previousLastSeen < memoryPresenceCoalesceWindowMS
}
