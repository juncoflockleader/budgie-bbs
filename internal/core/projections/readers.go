package projections

import (
	"database/sql"
	"net/netip"
	"sort"
	"strings"
	"time"
)

const DefaultMailQuotaBytes int64 = 10 << 20
const generatedSystemBoardSQLList = "'0announce','0moderation','BBSLists','Blessing','Filter','Goodbye','GiveupNotice','Recommend','Registry','bbsnet','denypost','newcomers','notepad','reject_registry','sysmail','syssecurity','undenypost','vote'"

// --- Readers ---

func GetBoard(db *sql.DB, id string) (*Board, error) {
	b := &Board{}
	var anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int
	err := QQueryRow(db,
		`SELECT b.id, b.name, b.description,
		        COALESCE(s.anonymous_allowed, 0), COALESCE(s.read_only, 0), COALESCE(s.no_reply, 0),
		        COALESCE(s.attachments_allowed, 0), COALESCE(s.mail_in_allowed, 0), COALESCE(s.relay_enabled, 0),
		        COALESCE(s.member_read_mode, 0), COALESCE(s.member_post_mode, 0), COALESCE(s.stats_excluded, 0),
		        COALESCE(s.zap_allowed, 1),
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0)
		   FROM boards b
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE b.id=?`,
		id,
	).Scan(&b.ID, &b.Name, &b.Description, &anonymousAllowed, &readOnly, &noReply, &attachmentsAllowed, &mailInAllowed, &relayEnabled, &memberReadMode, &memberPostMode, &statsExcluded, &zapAllowed, &b.ModeratorCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	applyBoardPolicyFlags(b, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed)
	return b, nil
}

func ListBoards(db *sql.DB) ([]Board, error) {
	rows, err := QQuery(db,
		`SELECT b.id, b.name, b.description,
		        COALESCE(s.anonymous_allowed, 0), COALESCE(s.read_only, 0), COALESCE(s.no_reply, 0),
		        COALESCE(s.attachments_allowed, 0), COALESCE(s.mail_in_allowed, 0), COALESCE(s.relay_enabled, 0),
		        COALESCE(s.member_read_mode, 0), COALESCE(s.member_post_mode, 0), COALESCE(s.stats_excluded, 0),
		        COALESCE(s.zap_allowed, 1),
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0)
		   FROM boards b
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  ORDER BY b.name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []Board
	for rows.Next() {
		var b Board
		var anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &anonymousAllowed, &readOnly, &noReply, &attachmentsAllowed, &mailInAllowed, &relayEnabled, &memberReadMode, &memberPostMode, &statsExcluded, &zapAllowed, &b.ModeratorCount); err != nil {
			return nil, err
		}
		applyBoardPolicyFlags(&b, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed)
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func GetCommunityStats(db *sql.DB) (*CommunityStats, error) {
	stats, err := getCommunityStatsCurrent(db)
	if err != nil {
		return nil, err
	}
	if err := applyCommunityStatsMaxOnline(db, stats); err != nil {
		return nil, err
	}
	return stats, nil
}

func getCommunityStatsCurrent(db *sql.DB) (*CommunityStats, error) {
	cutoff := NowMS() - 5*60*1000
	stats := &CommunityStats{}
	err := QQueryRow(db,
		`SELECT
		    (SELECT COUNT(*) FROM users),
		    (SELECT COUNT(*) FROM boards WHERE id NOT IN (`+generatedSystemBoardSQLList+`)),
		    (SELECT COUNT(*) FROM threads WHERE board NOT IN (`+generatedSystemBoardSQLList+`)),
		    (SELECT COUNT(*)
		       FROM posts p
		       JOIN threads t ON t.id=p.thread
		      WHERE p.redacted=0 AND t.board NOT IN (`+generatedSystemBoardSQLList+`)),
		    (SELECT COUNT(*)
		       FROM post_reactions pr
		       JOIN posts p ON p.id=pr.post_id
		       JOIN threads t ON t.id=p.thread
		      WHERE t.board NOT IN (`+generatedSystemBoardSQLList+`)),
		    (SELECT COUNT(*) FROM mail_messages),
		    (SELECT COUNT(*) FROM direct_messages),
		    COALESCE((SELECT SUM(login_count) FROM user_activity), 0),
		    COALESCE((SELECT SUM(total_online_seconds) FROM user_activity), 0),
		    (SELECT COUNT(DISTINCT user_id) FROM user_presence_sessions WHERE last_seen >= ? AND LOWER(status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')),
		    (SELECT COUNT(*) FROM guest_presence_sessions WHERE last_seen >= ? AND LOWER(status) NOT IN ('offline', 'inactive')),
		    COALESCE((SELECT MAX(seq) FROM events), 0)`,
		cutoff,
		cutoff,
	).Scan(
		&stats.TotalUsers,
		&stats.TotalBoards,
		&stats.TotalThreads,
		&stats.TotalPosts,
		&stats.TotalReactions,
		&stats.TotalMail,
		&stats.TotalDirectMessages,
		&stats.TotalLogins,
		&stats.TotalOnlineSeconds,
		&stats.OnlineUsers,
		&stats.OnlineGuests,
		&stats.HeadSeq,
	)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func applyCommunityStatsMaxOnline(db *sql.DB, stats *CommunityStats) error {
	var maxOnline int
	var maxAt int64
	err := QQueryRow(db,
		`SELECT max_online_users, max_online_at
		   FROM community_stat_history
		  ORDER BY max_online_users DESC, max_online_at DESC
		  LIMIT 1`,
	).Scan(&maxOnline, &maxAt)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	stats.MaxOnlineUsers = maxOnline
	stats.MaxOnlineAt = maxAt
	if stats.OnlineUsers > stats.MaxOnlineUsers {
		stats.MaxOnlineUsers = stats.OnlineUsers
		stats.MaxOnlineAt = NowMS()
	}
	var maxGuests int
	var maxGuestsAt int64
	err = QQueryRow(db,
		`SELECT max_online_guests, max_online_guests_at
		   FROM community_stat_history
		  ORDER BY max_online_guests DESC, max_online_guests_at DESC
		  LIMIT 1`,
	).Scan(&maxGuests, &maxGuestsAt)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	stats.MaxOnlineGuests = maxGuests
	stats.MaxOnlineGuestsAt = maxGuestsAt
	if stats.OnlineGuests > stats.MaxOnlineGuests {
		stats.MaxOnlineGuests = stats.OnlineGuests
		stats.MaxOnlineGuestsAt = NowMS()
	}
	return nil
}

func ListCommunityStatHistory(db *sql.DB, limit, offset int) ([]CommunityStatHistory, error) {
	if limit <= 0 || limit > 365 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	fetchLimit := limit + 1
	rows, err := QQuery(db,
		`SELECT day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		        total_reactions, total_mail, total_direct_messages, total_logins, total_online_seconds, online_users,
		        online_guests, max_online_users, max_online_at, max_online_guests,
		        max_online_guests_at, head_seq
		   FROM community_stat_history
		  ORDER BY day DESC
		  LIMIT ? OFFSET ?`,
		fetchLimit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommunityStatHistory{}
	for rows.Next() {
		var h CommunityStatHistory
		if err := rows.Scan(&h.Day, &h.SnapshotAt, &h.TotalUsers, &h.TotalBoards, &h.TotalThreads, &h.TotalPosts, &h.TotalReactions, &h.TotalMail, &h.TotalDirectMessages, &h.TotalLogins, &h.TotalOnlineSeconds, &h.OnlineUsers, &h.OnlineGuests, &h.MaxOnlineUsers, &h.MaxOnlineAt, &h.MaxOnlineGuests, &h.MaxOnlineGuestsAt, &h.HeadSeq); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if i+1 >= len(out) {
			continue
		}
		out[i].applyDeltaFrom(out[i+1])
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func ListCommunityStatHistoryRange(db *sql.DB, startDay, endDay string) ([]CommunityStatHistory, error) {
	rows, err := QQuery(db,
		`SELECT day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		        total_reactions, total_mail, total_direct_messages, total_logins, total_online_seconds, online_users,
		        online_guests, max_online_users, max_online_at, max_online_guests,
		        max_online_guests_at, head_seq
		   FROM community_stat_history
		  WHERE day >= ? AND day <= ?
		  ORDER BY day DESC`,
		startDay, endDay,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CommunityStatHistory{}
	for rows.Next() {
		var h CommunityStatHistory
		if err := rows.Scan(&h.Day, &h.SnapshotAt, &h.TotalUsers, &h.TotalBoards, &h.TotalThreads, &h.TotalPosts, &h.TotalReactions, &h.TotalMail, &h.TotalDirectMessages, &h.TotalLogins, &h.TotalOnlineSeconds, &h.OnlineUsers, &h.OnlineGuests, &h.MaxOnlineUsers, &h.MaxOnlineAt, &h.MaxOnlineGuests, &h.MaxOnlineGuestsAt, &h.HeadSeq); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var previous CommunityStatHistory
	hasPrevious := false
	err = QQueryRow(db,
		`SELECT day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		        total_reactions, total_mail, total_direct_messages, total_logins, total_online_seconds, online_users,
		        online_guests, max_online_users, max_online_at, max_online_guests,
		        max_online_guests_at, head_seq
		   FROM community_stat_history
		  WHERE day < ?
		  ORDER BY day DESC
		  LIMIT 1`,
		startDay,
	).Scan(&previous.Day, &previous.SnapshotAt, &previous.TotalUsers, &previous.TotalBoards, &previous.TotalThreads, &previous.TotalPosts, &previous.TotalReactions, &previous.TotalMail, &previous.TotalDirectMessages, &previous.TotalLogins, &previous.TotalOnlineSeconds, &previous.OnlineUsers, &previous.OnlineGuests, &previous.MaxOnlineUsers, &previous.MaxOnlineAt, &previous.MaxOnlineGuests, &previous.MaxOnlineGuestsAt, &previous.HeadSeq)
	if err == nil {
		hasPrevious = true
	} else if err != sql.ErrNoRows {
		return nil, err
	}
	for i := range out {
		if i+1 < len(out) {
			out[i].applyDeltaFrom(out[i+1])
			continue
		}
		if hasPrevious {
			out[i].applyDeltaFrom(previous)
		}
	}
	return out, nil
}

func ListLoginHourlyStats(db *sql.DB, day string) ([]LoginHourlyStat, error) {
	out := make([]LoginHourlyStat, 24)
	for i := range out {
		out[i] = LoginHourlyStat{Day: day, Hour: i}
	}
	rows, err := QQuery(db,
		`SELECT hour, login_count, updated_at
		   FROM login_hourly_stats
		  WHERE day=?
		  ORDER BY hour ASC`,
		day,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hour int
		var count int
		var updatedAt int64
		if err := rows.Scan(&hour, &count, &updatedAt); err != nil {
			return nil, err
		}
		if hour < 0 || hour >= len(out) {
			continue
		}
		out[hour].LoginCount = count
		out[hour].UpdatedAt = updatedAt
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (h *CommunityStatHistory) applyDeltaFrom(previous CommunityStatHistory) {
	h.DeltaUsers = h.TotalUsers - previous.TotalUsers
	h.DeltaBoards = h.TotalBoards - previous.TotalBoards
	h.DeltaThreads = h.TotalThreads - previous.TotalThreads
	h.DeltaPosts = h.TotalPosts - previous.TotalPosts
	h.DeltaReactions = h.TotalReactions - previous.TotalReactions
	h.DeltaMail = h.TotalMail - previous.TotalMail
	h.DeltaDirectMessages = h.TotalDirectMessages - previous.TotalDirectMessages
	h.DeltaLogins = h.TotalLogins - previous.TotalLogins
	h.DeltaOnlineSeconds = h.TotalOnlineSeconds - previous.TotalOnlineSeconds
	h.DeltaGuests = h.OnlineGuests - previous.OnlineGuests
}

func ListBoardRankings(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]BoardRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	onlineCutoff := NowMS() - 5*60*1000
	rows, err := QQuery(db,
		`SELECT b.id, b.name, b.description,
		        COUNT(DISTINCT t.id) AS thread_count,
		        COUNT(p.id) AS post_count,
		        COALESCE(MAX(t.last_seq), 0) AS last_seq,
		        COALESCE(MAX(p.created_at), 0) AS last_post_at,
		        COALESCE((SELECT COUNT(DISTINCT ups.user_id)
		                    FROM user_presence_sessions ups
		                   WHERE ups.board_id=b.id
		                     AND ups.last_seen >= ?
		                     AND LOWER(ups.status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')), 0) AS online_users,
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0) AS moderator_count
		   FROM boards b
		   LEFT JOIN board_settings s ON s.board_id=b.id
		   LEFT JOIN threads t ON t.board=b.id
		   LEFT JOIN posts p ON p.thread=t.id AND p.redacted=0
		  WHERE b.id NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(s.stats_excluded, 0)=0
		    AND (
		      COALESCE(s.member_read_mode, 0)=0
		      OR ?=1
		      OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		      OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		    )
		  GROUP BY b.id, b.name, b.description
		  ORDER BY post_count DESC, thread_count DESC, last_seq DESC, b.name
		  LIMIT ? OFFSET ?`,
		onlineCutoff, boolInt(includePrivate), viewerID, viewerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BoardRanking{}
	for rows.Next() {
		var rank BoardRanking
		if err := rows.Scan(&rank.ID, &rank.Name, &rank.Description, &rank.ThreadCount, &rank.PostCount, &rank.LastSeq, &rank.LastPostAt, &rank.OnlineUsers, &rank.ModeratorCount); err != nil {
			return nil, err
		}
		out = append(out, rank)
	}
	return out, rows.Err()
}

func ListRecommendedBoards(db *sql.DB, limit, offset int) ([]RecommendedBoard, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	onlineCutoff := NowMS() - 5*60*1000
	rows, err := QQuery(db,
		`SELECT rb.board_id, b.name, b.description, rb.note, rb.position,
		        rb.curated_by, COALESCE(u.name, ''), rb.created_at, rb.updated_at,
		        COALESCE((SELECT COUNT(*) FROM threads t WHERE t.board=b.id), 0) AS thread_count,
		        COALESCE((SELECT COUNT(*)
		                    FROM posts p
		                    JOIN threads t ON t.id=p.thread
		                   WHERE t.board=b.id AND p.redacted=0), 0) AS post_count,
		        COALESCE((SELECT COUNT(DISTINCT ups.user_id)
		                    FROM user_presence_sessions ups
		                   WHERE ups.board_id=b.id
		                     AND ups.last_seen >= ?
		                     AND LOWER(ups.status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')), 0) AS online_users,
		        COALESCE((SELECT MAX(t.last_seq) FROM threads t WHERE t.board=b.id), 0) AS last_seq,
		        COALESCE((SELECT MAX(p.created_at)
		                    FROM posts p
		                    JOIN threads t ON t.id=p.thread
		                   WHERE t.board=b.id AND p.redacted=0), 0) AS last_post_at,
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0) AS moderator_count
		   FROM recommended_boards rb
		   JOIN boards b ON b.id=rb.board_id
		   LEFT JOIN users u ON u.id=rb.curated_by
		   LEFT JOIN categories c ON c.id=b.id
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE b.id NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(c.visibility, 'public')='public'
		    AND COALESCE(s.member_read_mode, 0)=0
		    AND COALESCE(s.stats_excluded, 0)=0
		  ORDER BY rb.position ASC, rb.updated_at DESC, LOWER(b.name), LOWER(rb.board_id)
		  LIMIT ? OFFSET ?`,
		onlineCutoff, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecommendedBoard{}
	for rows.Next() {
		var board RecommendedBoard
		if err := rows.Scan(
			&board.ID,
			&board.Name,
			&board.Description,
			&board.Note,
			&board.Position,
			&board.CuratedBy,
			&board.CuratedByName,
			&board.RecommendedAt,
			&board.UpdatedAt,
			&board.ThreadCount,
			&board.PostCount,
			&board.OnlineUsers,
			&board.LastSeq,
			&board.LastPostAt,
			&board.ModeratorCount,
		); err != nil {
			return nil, err
		}
		out = append(out, board)
	}
	return out, rows.Err()
}

func ListRecentPublicBoards(db *sql.DB, startAt, endAt int64, limit int) ([]BoardSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := QQuery(db,
		`SELECT b.id, b.name, b.description,
		        COALESCE((SELECT COUNT(*) FROM threads t WHERE t.board=b.id), 0) AS thread_count,
		        COALESCE((SELECT COUNT(*)
		                    FROM posts p
		                    JOIN threads t ON t.id=p.thread
		                   WHERE t.board=b.id AND p.redacted=0), 0) AS post_count,
		        COALESCE((SELECT MAX(t.last_seq) FROM threads t WHERE t.board=b.id), 0) AS last_seq,
		        COALESCE(c.created_at, 0) AS created_at,
		        COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=b.id), 0) AS moderator_count
		   FROM boards b
		   JOIN categories c ON c.id=b.id
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE b.id NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(c.created_at, 0) >= ?
		    AND COALESCE(c.created_at, 0) <= ?
		    AND COALESCE(c.visibility, 'public')='public'
		    AND COALESCE(s.member_read_mode, 0)=0
		    AND COALESCE(s.stats_excluded, 0)=0
		  ORDER BY c.created_at DESC, LOWER(b.name), LOWER(b.id)
		  LIMIT ?`,
		startAt, endAt, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BoardSummary{}
	for rows.Next() {
		var board BoardSummary
		if err := rows.Scan(&board.ID, &board.Name, &board.Description, &board.ThreadCount, &board.PostCount, &board.LastSeq, &board.CreatedAt, &board.ModeratorCount); err != nil {
			return nil, err
		}
		board.NewBoard = true
		out = append(out, board)
	}
	return out, rows.Err()
}

func ListThreadRankings(db *sql.DB, viewerID string, includePrivate bool, boardID string, limit, offset int) ([]ThreadRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT t.id, t.board, b.name, t.title, t.author, COALESCE(t.author_id, ''),
		        COUNT(DISTINCT p.id) AS post_count,
		        COUNT(DISTINCT COALESCE(NULLIF(p.author_id, ''), p.author)) AS participant_count,
		        COUNT(pr.post_id) AS reaction_count,
		        t.last_seq, t.created_at, t.updated_at
		   FROM threads t
		   JOIN boards b ON b.id=t.board
		   LEFT JOIN board_settings s ON s.board_id=b.id
		   LEFT JOIN posts p ON p.thread=t.id AND p.redacted=0
		   LEFT JOIN post_reactions pr ON pr.post_id=p.id
		  WHERE (? = '' OR t.board = ?)
		    AND t.board NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(s.stats_excluded, 0)=0
		    AND (
		      COALESCE(s.member_read_mode, 0)=0
		      OR ?=1
		      OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		      OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		    )
		  GROUP BY t.id, t.board, b.name, t.title, t.author, t.author_id, t.last_seq, t.created_at, t.updated_at
		  ORDER BY t.last_seq DESC`,
		boardID, boardID, boolInt(includePrivate), viewerID, viewerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThreadRanking{}
	now := time.Now().UnixMilli()
	for rows.Next() {
		var rank ThreadRanking
		if err := rows.Scan(&rank.ID, &rank.Board, &rank.BoardName, &rank.Title, &rank.Author, &rank.AuthorID, &rank.PostCount, &rank.ParticipantCount, &rank.ReactionCount, &rank.LastSeq, &rank.CreatedAt, &rank.UpdatedAt); err != nil {
			return nil, err
		}
		rank.Score = hotThreadScore(rank.PostCount, rank.ParticipantCount, rank.ReactionCount, rank.UpdatedAt, now)
		out = append(out, rank)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].LastSeq != out[j].LastSeq {
			return out[i].LastSeq > out[j].LastSeq
		}
		return out[i].Title < out[j].Title
	})
	if offset >= len(out) {
		return []ThreadRanking{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func ListThreadRankingsRange(db *sql.DB, viewerID string, includePrivate bool, boardID string, startAt, endAt int64, limit, offset int) ([]ThreadRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if startAt <= 0 || endAt <= 0 || endAt < startAt {
		return []ThreadRanking{}, nil
	}
	rows, err := QQuery(db,
		`SELECT t.id, t.board, b.name, t.title, t.author, COALESCE(t.author_id, ''),
		        COUNT(DISTINCT p.id) AS post_count,
		        COUNT(DISTINCT COALESCE(NULLIF(p.author_id, ''), p.author)) AS participant_count,
		        COUNT(pr.post_id) AS reaction_count,
		        COALESCE(MAX(p.created_seq), 0) AS last_seq,
		        t.created_at,
		        COALESCE(MAX(p.created_at), t.updated_at) AS period_last_activity
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		   JOIN boards b ON b.id=t.board
		   LEFT JOIN board_settings s ON s.board_id=b.id
		   LEFT JOIN post_reactions pr ON pr.post_id=p.id
		  WHERE (? = '' OR t.board = ?)
		    AND p.redacted=0
		    AND p.created_at >= ?
		    AND p.created_at <= ?
		    AND t.board NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(s.stats_excluded, 0)=0
		    AND (
		      COALESCE(s.member_read_mode, 0)=0
		      OR ?=1
		      OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		      OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		    )
		  GROUP BY t.id, t.board, b.name, t.title, t.author, t.author_id, t.created_at, t.updated_at`,
		boardID, boardID, startAt, endAt, boolInt(includePrivate), viewerID, viewerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThreadRanking{}
	for rows.Next() {
		var rank ThreadRanking
		if err := rows.Scan(&rank.ID, &rank.Board, &rank.BoardName, &rank.Title, &rank.Author, &rank.AuthorID, &rank.PostCount, &rank.ParticipantCount, &rank.ReactionCount, &rank.LastSeq, &rank.CreatedAt, &rank.UpdatedAt); err != nil {
			return nil, err
		}
		rank.Score = periodThreadScore(rank.PostCount, rank.ParticipantCount, rank.ReactionCount)
		out = append(out, rank)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if out[i].LastSeq != out[j].LastSeq {
			return out[i].LastSeq > out[j].LastSeq
		}
		return out[i].Title < out[j].Title
	})
	if offset >= len(out) {
		return []ThreadRanking{}, nil
	}
	end := offset + limit
	if end > len(out) {
		end = len(out)
	}
	return out[offset:end], nil
}

func periodThreadScore(postCount, participantCount, reactionCount int) int {
	return postCount*100 + participantCount*150 + reactionCount*200
}

func hotThreadScore(postCount, participantCount, reactionCount int, updatedAt, now int64) int {
	base := int64(postCount*100 + participantCount*150 + reactionCount*200)
	if base <= 0 {
		return 0
	}
	if updatedAt <= 0 || now <= updatedAt {
		return int(base)
	}
	ageHours := (now - updatedAt) / int64(time.Hour/time.Millisecond)
	const halfLifeHours int64 = 48
	score := base * halfLifeHours / (halfLifeHours + ageHours)
	if score < 1 {
		return 1
	}
	return int(score)
}

func ListReplyRankings(db *sql.DB, viewerID string, includePrivate bool, limit, offset int) ([]ReplyRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT p.id, t.id, t.board, b.name, t.title, p.author, COALESCE(p.author_id, ''),
		        COALESCE(p.body, '') AS body,
		        p.created_seq, p.created_at
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		   JOIN boards b ON b.id=t.board
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE p.redacted=0
		    AND p.created_seq > (SELECT MIN(root.created_seq) FROM posts root WHERE root.thread=t.id)
		    AND t.board NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(s.stats_excluded, 0)=0
		    AND (
		      COALESCE(s.member_read_mode, 0)=0
		      OR ?=1
		      OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		      OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		    )
		  ORDER BY p.created_seq DESC
		  LIMIT ? OFFSET ?`,
		boolInt(includePrivate), viewerID, viewerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ReplyRanking{}
	for rows.Next() {
		var rank ReplyRanking
		var body string
		if err := rows.Scan(&rank.PostID, &rank.ThreadID, &rank.Board, &rank.BoardName, &rank.Title, &rank.Author, &rank.AuthorID, &body, &rank.Seq, &rank.CreatedAt); err != nil {
			return nil, err
		}
		rank.Excerpt = replyRankingExcerpt(body)
		out = append(out, rank)
	}
	return out, rows.Err()
}

func replyRankingExcerpt(body string) string {
	excerpt := strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(body))
	runes := []rune(excerpt)
	if len(runes) > 180 {
		return string(runes[:180])
	}
	return excerpt
}

func ListUserRankings(db *sql.DB, limit, offset int) ([]UserRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT u.id, u.name, u.role,
		        COUNT(DISTINCT p.id) AS posts_created,
		        COUNT(pr.post_id) AS reactions_received,
		        COALESCE(ua.login_count, 0),
		        COALESCE(ua.total_online_seconds, 0),
		        COALESCE(ua.trust_level, 0)
		   FROM users u
		   LEFT JOIN user_activity ua ON ua.user_id=u.id
		   LEFT JOIN posts p ON p.author_id=u.id AND p.redacted=0
		        AND NOT EXISTS (
		          SELECT 1
		            FROM threads pt
		            LEFT JOIN board_settings ps ON ps.board_id=pt.board
		           WHERE pt.id=p.thread
		             AND (pt.board IN (`+generatedSystemBoardSQLList+`) OR COALESCE(ps.stats_excluded, 0)!=0)
		        )
		   LEFT JOIN post_reactions pr ON pr.post_id=p.id
		  GROUP BY u.id, u.name, u.role, ua.login_count, ua.total_online_seconds, ua.trust_level
		  ORDER BY posts_created DESC,
		           reactions_received DESC,
		           COALESCE(ua.login_count, 0) DESC,
		           COALESCE(ua.total_online_seconds, 0) DESC,
		           u.name
		  LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRanking{}
	for rows.Next() {
		var rank UserRanking
		if err := rows.Scan(&rank.UserID, &rank.Name, &rank.Role, &rank.PostsCreated, &rank.ReactionsReceived, &rank.LoginCount, &rank.TotalOnlineSeconds, &rank.TrustLevel); err != nil {
			return nil, err
		}
		out = append(out, rank)
	}
	return out, rows.Err()
}

func GetPublicUserPostActivity(db *sql.DB, userID string) (postCount int, lastPostAt int64, err error) {
	err = QQueryRow(db,
		`SELECT COUNT(DISTINCT p.id) AS posts_created,
		        COALESCE(MAX(p.created_at), 0) AS last_post_at
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		   LEFT JOIN board_settings s ON s.board_id=t.board
		  WHERE p.author_id=?
		    AND p.redacted=0
		    AND t.board NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(s.stats_excluded, 0)=0
		    AND COALESCE(s.member_read_mode, 0)=0`,
		userID,
	).Scan(&postCount, &lastPostAt)
	return postCount, lastPostAt, err
}

func ListBlessingRankings(db *sql.DB, limit, offset int) ([]BlessingRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT u.id, u.name, COUNT(b.id) AS blessing_count, COALESCE(MAX(b.created_at), 0) AS last_blessed_at
		   FROM blessings b
		   JOIN users u ON u.id=b.to_user_id
		  GROUP BY u.id, u.name
		  ORDER BY blessing_count DESC, last_blessed_at DESC, u.name
		  LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlessingRanking{}
	for rows.Next() {
		var rank BlessingRanking
		if err := rows.Scan(&rank.UserID, &rank.Name, &rank.BlessingCount, &rank.LastBlessedAt); err != nil {
			return nil, err
		}
		out = append(out, rank)
	}
	return out, rows.Err()
}

func ListBlessingRankingsRange(db *sql.DB, startAt, endAt int64, limit, offset int) ([]BlessingRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if startAt <= 0 || endAt <= 0 || endAt < startAt {
		return []BlessingRanking{}, nil
	}
	rows, err := QQuery(db,
		`SELECT u.id, u.name, COUNT(b.id) AS blessing_count, COALESCE(MAX(b.created_at), 0) AS last_blessed_at
		   FROM blessings b
		   JOIN users u ON u.id=b.to_user_id
		  WHERE b.created_at >= ?
		    AND b.created_at <= ?
		  GROUP BY u.id, u.name
		  ORDER BY blessing_count DESC, last_blessed_at DESC, u.name
		  LIMIT ? OFFSET ?`,
		startAt, endAt, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlessingRanking{}
	for rows.Next() {
		var rank BlessingRanking
		if err := rows.Scan(&rank.UserID, &rank.Name, &rank.BlessingCount, &rank.LastBlessedAt); err != nil {
			return nil, err
		}
		out = append(out, rank)
	}
	return out, rows.Err()
}

func ListBlessings(db *sql.DB, limit, offset int) ([]Blessing, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT b.id, b.from_user_id, COALESCE(f.name,''), b.to_user_id, COALESCE(t.name,''),
		        b.message, b.created_at, b.seq
		   FROM blessings b
		   LEFT JOIN users f ON f.id=b.from_user_id
		   LEFT JOIN users t ON t.id=b.to_user_id
		  ORDER BY b.created_at DESC, b.seq DESC
		  LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Blessing{}
	for rows.Next() {
		var blessing Blessing
		if err := rows.Scan(&blessing.ID, &blessing.FromUserID, &blessing.FromName, &blessing.ToUserID, &blessing.ToName, &blessing.Message, &blessing.CreatedAt, &blessing.Seq); err != nil {
			return nil, err
		}
		out = append(out, blessing)
	}
	return out, rows.Err()
}

func ListBlessingsRange(db *sql.DB, startAt, endAt int64, limit, offset int) ([]Blessing, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if startAt <= 0 || endAt <= 0 || endAt < startAt {
		return []Blessing{}, nil
	}
	rows, err := QQuery(db,
		`SELECT b.id, b.from_user_id, COALESCE(f.name,''), b.to_user_id, COALESCE(t.name,''),
		        b.message, b.created_at, b.seq
		   FROM blessings b
		   LEFT JOIN users f ON f.id=b.from_user_id
		   LEFT JOIN users t ON t.id=b.to_user_id
		  WHERE b.created_at >= ?
		    AND b.created_at <= ?
		  ORDER BY b.created_at DESC, b.seq DESC
		  LIMIT ? OFFSET ?`,
		startAt, endAt, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Blessing{}
	for rows.Next() {
		var blessing Blessing
		if err := rows.Scan(&blessing.ID, &blessing.FromUserID, &blessing.FromName, &blessing.ToUserID, &blessing.ToName, &blessing.Message, &blessing.CreatedAt, &blessing.Seq); err != nil {
			return nil, err
		}
		out = append(out, blessing)
	}
	return out, rows.Err()
}

func ListArchiveRankings(db *sql.DB, viewerID string, includePrivate bool, kind string, limit, offset int) ([]ArchiveRanking, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	kind = strings.TrimSpace(kind)
	rows, err := QQuery(db,
		`SELECT d.board_id, b.name, d.kind, d.path,
		        COUNT(*) AS entry_count,
		        COALESCE(SUM(CASE WHEN d.body_edited != 0 THEN 1 ELSE 0 END), 0) AS edited_count,
		        COALESCE(MAX(d.updated_at), 0) AS last_updated_at
		   FROM digest_entries d
		   JOIN boards b ON b.id=d.board_id
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE (? = '' OR d.kind = ?)
		    AND COALESCE(s.stats_excluded, 0)=0
		    AND (
		      COALESCE(s.member_read_mode, 0)=0
		      OR ?=1
		      OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		      OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		    )
		  GROUP BY d.board_id, b.name, d.kind, d.path
		  ORDER BY entry_count DESC, edited_count DESC, last_updated_at DESC, b.name, d.path
		  LIMIT ? OFFSET ?`,
		kind, kind, boolInt(includePrivate), viewerID, viewerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ArchiveRanking{}
	for rows.Next() {
		var rank ArchiveRanking
		if err := rows.Scan(&rank.BoardID, &rank.BoardName, &rank.Kind, &rank.Path, &rank.EntryCount, &rank.EditedCount, &rank.LastUpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, rank)
	}
	return out, rows.Err()
}

func GetBoardSettings(db *sql.DB, boardID string) (*BoardSettings, error) {
	var exists int
	if err := QQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	settings := &BoardSettings{BoardID: boardID, ZapAllowed: true}
	var anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int
	err := QQueryRow(db,
		`SELECT COALESCE(anonymous_allowed, 0), COALESCE(read_only, 0), COALESCE(no_reply, 0),
		        COALESCE(attachments_allowed, 0), COALESCE(mail_in_allowed, 0), COALESCE(relay_enabled, 0),
		        COALESCE(member_read_mode, 0), COALESCE(member_post_mode, 0), COALESCE(stats_excluded, 0),
		        COALESCE(zap_allowed, 1), COALESCE(updated_at, 0)
		   FROM board_settings WHERE board_id=?`,
		boardID,
	).Scan(&anonymousAllowed, &readOnly, &noReply, &attachmentsAllowed, &mailInAllowed, &relayEnabled, &memberReadMode, &memberPostMode, &statsExcluded, &zapAllowed, &settings.UpdatedAt)
	if err == sql.ErrNoRows {
		return settings, nil
	}
	if err != nil {
		return nil, err
	}
	applySettingsFlags(settings, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed)
	return settings, nil
}

func GetBoardMemberRequirements(db *sql.DB, boardID string) (*BoardMemberRequirements, error) {
	var exists int
	if err := QQueryRow(db, `SELECT 1 FROM boards WHERE id=?`, boardID).Scan(&exists); err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	req := &BoardMemberRequirements{BoardID: boardID, ApprovalMode: "manual"}
	err := QQueryRow(db,
		`SELECT COALESCE(min_login_count, 0), COALESCE(min_post_count, 0),
		        COALESCE(min_trust_level, 0), COALESCE(min_score, 0),
		        COALESCE(min_board_post_count, 0), COALESCE(min_board_original_post_count, 0),
		        COALESCE(min_board_digest_count, 0), COALESCE(min_board_mark_count, 0),
		        COALESCE(max_members, 0),
		        COALESCE(approval_mode, 'manual'), COALESCE(updated_at, 0)
		   FROM board_member_requirements WHERE board_id=?`,
		boardID,
	).Scan(&req.MinLoginCount, &req.MinPostCount, &req.MinTrustLevel, &req.MinScore, &req.MinBoardPostCount, &req.MinBoardOriginalPostCount, &req.MinBoardDigestCount, &req.MinBoardMarkCount, &req.MaxMembers, &req.ApprovalMode, &req.UpdatedAt)
	if err == sql.ErrNoRows {
		return req, nil
	}
	if err != nil {
		return nil, err
	}
	req.ApprovalMode = strings.ToLower(strings.TrimSpace(req.ApprovalMode))
	if req.ApprovalMode == "" {
		req.ApprovalMode = "manual"
	}
	return req, nil
}

func ListBoardModerators(db *sql.DB, boardID string) ([]BoardModerator, error) {
	rows, err := QQuery(db,
		`SELECT bm.user_id, u.name, bm.position, bm.created_at, bm.updated_at
		   FROM board_moderators bm
		   JOIN users u ON u.id = bm.user_id
		  WHERE bm.board_id=?
		  ORDER BY bm.position, u.name`,
		boardID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	mods := []BoardModerator{}
	for rows.Next() {
		var m BoardModerator
		if err := rows.Scan(&m.UserID, &m.Name, &m.Position, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, err
		}
		mods = append(mods, m)
	}
	return mods, rows.Err()
}

func GetBoardInfo(db *sql.DB, boardID string) (*BoardInfo, error) {
	board, err := GetBoard(db, boardID)
	if err != nil || board == nil {
		return nil, err
	}
	settings, err := GetBoardSettings(db, boardID)
	if err != nil {
		return nil, err
	}
	requirements, err := GetBoardMemberRequirements(db, boardID)
	if err != nil {
		return nil, err
	}
	moderators, err := ListBoardModerators(db, boardID)
	if err != nil {
		return nil, err
	}
	members, err := ListBoardMembers(db, boardID)
	if err != nil {
		return nil, err
	}
	return &BoardInfo{Board: *board, Settings: *settings, Requirements: *requirements, Moderators: moderators, Members: members}, nil
}

func ListBoardMembers(db *sql.DB, boardID string) ([]BoardMember, error) {
	rows, err := QQuery(db,
		`SELECT bm.user_id, u.name, bm.title, COALESCE(bm.position, 0),
		        COALESCE(bm.can_manage_members, 0), COALESCE(bm.can_curate, 0),
		        COALESCE(bm.can_moderate_posts, 0), COALESCE(bm.can_moderate_threads, 0),
		        COALESCE(bm.can_announce, 0), COALESCE(bm.can_manage_polls, 0),
		        COALESCE(bm.can_set_board_settings, 0),
		        bm.created_at, bm.updated_at
		   FROM board_members bm
		   JOIN users u ON u.id = bm.user_id
		  WHERE bm.board_id=?
		  ORDER BY COALESCE(bm.position, 0), u.name`,
		boardID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BoardMember{}
	for rows.Next() {
		var m BoardMember
		var canManageMembers, canCurate, canModeratePosts, canModerateThreads, canAnnounce, canManagePolls, canSetBoardSettings int
		if err := rows.Scan(
			&m.UserID,
			&m.Name,
			&m.Title,
			&m.Position,
			&canManageMembers,
			&canCurate,
			&canModeratePosts,
			&canModerateThreads,
			&canAnnounce,
			&canManagePolls,
			&canSetBoardSettings,
			&m.CreatedAt,
			&m.UpdatedAt,
		); err != nil {
			return nil, err
		}
		m.CanManageMembers = canManageMembers != 0
		m.CanCurate = canCurate != 0
		m.CanModeratePosts = canModeratePosts != 0
		m.CanModerateThreads = canModerateThreads != 0
		m.CanAnnounce = canAnnounce != 0
		m.CanManagePolls = canManagePolls != 0
		m.CanSetBoardSettings = canSetBoardSettings != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

func UserIsBoardMember(db *sql.DB, boardID, userID string) (bool, error) {
	var found int
	err := QQueryRow(db, `SELECT 1 FROM board_members WHERE board_id=? AND user_id=?`, boardID, userID).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func LatestBoardMemberApplicationStatus(db *sql.DB, boardID, userID string) (string, error) {
	var status string
	err := QQueryRow(db,
		`SELECT status FROM board_member_applications
		  WHERE board_id=? AND user_id=?
		    AND status IN ('pending', 'blacklisted')
		  ORDER BY CASE status WHEN 'pending' THEN 0 ELSE 1 END, updated_at DESC, created_at DESC LIMIT 1`,
		boardID, userID,
	).Scan(&status)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return status, err
}

func GetBoardMemberApplication(db *sql.DB, applicationID string) (*BoardMemberApplication, error) {
	app := &BoardMemberApplication{}
	err := QQueryRow(db,
		`SELECT a.id, a.board_id, a.user_id, u.name, a.status, a.note, a.title,
		        a.reviewer_id, COALESCE(r.name, ''), a.review_note, a.created_at, a.updated_at, a.reviewed_at
		   FROM board_member_applications a
		   JOIN users u ON u.id = a.user_id
		   LEFT JOIN users r ON r.id = a.reviewer_id
		  WHERE a.id=?`,
		applicationID,
	).Scan(&app.ID, &app.BoardID, &app.UserID, &app.Name, &app.Status, &app.Note, &app.Title, &app.ReviewerID, &app.ReviewerName, &app.ReviewNote, &app.CreatedAt, &app.UpdatedAt, &app.ReviewedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return app, err
}

func ListBoardMemberApplications(db *sql.DB, boardID, status, userID string, limit, offset int) ([]BoardMemberApplication, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT a.id, a.board_id, a.user_id, u.name, a.status, a.note, a.title,
		        a.reviewer_id, COALESCE(r.name, ''), a.review_note, a.created_at, a.updated_at, a.reviewed_at
		   FROM board_member_applications a
		   JOIN users u ON u.id = a.user_id
		   LEFT JOIN users r ON r.id = a.reviewer_id
		  WHERE a.board_id=?
		    AND (? = '' OR a.status = ?)
		    AND (? = '' OR a.user_id = ?)
		  ORDER BY a.updated_at DESC, a.created_at DESC
		  LIMIT ? OFFSET ?`,
		boardID, status, status, userID, userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BoardMemberApplication{}
	for rows.Next() {
		var app BoardMemberApplication
		if err := rows.Scan(&app.ID, &app.BoardID, &app.UserID, &app.Name, &app.Status, &app.Note, &app.Title, &app.ReviewerID, &app.ReviewerName, &app.ReviewNote, &app.CreatedAt, &app.UpdatedAt, &app.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, app)
	}
	return out, rows.Err()
}

func ListDigestEntries(db *sql.DB, boardID, kind, path string, limit, offset int) ([]DigestEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT d.id, d.board_id, b.name, d.target_kind, d.target_id, d.kind, d.title, d.path, d.note, d.body_edited,
		        d.created_by, COALESCE(u.name, ''), d.created_at, d.updated_at,
		        CASE WHEN d.target_kind='thread' THEN tt.id ELSE pt.id END AS thread_id,
		        CASE WHEN d.target_kind='post' THEN p.id ELSE '' END AS post_id,
		        CASE WHEN d.target_kind='thread' THEN tt.author ELSE p.author END AS author,
		        CASE
		          WHEN d.body_edited != 0 THEN SUBSTR(d.body, 1, 180)
		          WHEN d.target_kind='post' THEN SUBSTR(p.body, 1, 180)
		          ELSE tt.title
		        END AS excerpt
		   FROM digest_entries d
		   JOIN boards b ON b.id = d.board_id
		   LEFT JOIN users u ON u.id = d.created_by
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id = d.target_id
		   LEFT JOIN threads pt ON pt.id = p.thread
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id = d.target_id
		  WHERE d.board_id = ?
		    AND (? = '' OR d.kind = ?)
		    AND (? = '' OR d.path = ?)
		  ORDER BY
		    CASE d.kind WHEN 'pinned' THEN 0 WHEN 'recommended' THEN 1 WHEN 'digest' THEN 2 ELSE 3 END,
		    d.updated_at DESC,
		    d.title
		  LIMIT ? OFFSET ?`,
		boardID, kind, kind, path, path, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanDigestEntryRows(rows)
}

func ListDigestPathTree(db *sql.DB, boardID, kind string) ([]DigestPathNode, error) {
	treeKind := strings.TrimSpace(kind)
	rows, err := QQuery(db,
		`SELECT COALESCE(path, ''), COUNT(*)
		   FROM digest_entries
		  WHERE board_id = ?
		    AND (? = '' OR kind = ?)
		  GROUP BY COALESCE(path, '')
		  ORDER BY COALESCE(path, '')`,
		boardID, kind, kind,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	nodes := map[string]*DigestPathNode{}
	childSets := map[string]map[string]struct{}{}
	ensureNode := func(path, nodeKind string) *DigestPathNode {
		path = strings.Trim(strings.TrimSpace(path), "/")
		if path == "" {
			if nodes[path] == nil {
				nodes[path] = &DigestPathNode{Path: "", Name: "/", Kind: nodeKind}
			}
			if nodes[path].Kind == "" {
				nodes[path].Kind = nodeKind
			}
			return nodes[path]
		}
		if nodes[path] == nil {
			name := path
			parent := ""
			if idx := strings.LastIndex(path, "/"); idx >= 0 {
				parent = path[:idx]
				name = path[idx+1:]
			}
			nodes[path] = &DigestPathNode{Path: path, Name: name, ParentPath: parent, Kind: nodeKind}
		}
		if nodes[path].Kind == "" {
			nodes[path].Kind = nodeKind
		}
		return nodes[path]
	}
	addChild := func(parent, child string) {
		if childSets[parent] == nil {
			childSets[parent] = map[string]struct{}{}
		}
		childSets[parent][child] = struct{}{}
	}
	addPath := func(rawPath string, count int, explicit bool) {
		path := strings.Trim(strings.TrimSpace(rawPath), "/")
		ensureNode("", treeKind)
		if path == "" {
			node := ensureNode("", treeKind)
			node.EntryCount += count
			if explicit {
				node.Explicit = true
			}
			return
		}
		current := ""
		for _, part := range strings.Split(path, "/") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			parent := current
			if current == "" {
				current = part
			} else {
				current += "/" + part
			}
			ensureNode(current, treeKind)
			addChild(parent, current)
		}
		node := ensureNode(path, treeKind)
		node.EntryCount += count
		if explicit {
			node.Explicit = true
		}
	}

	hasRows := false
	for rows.Next() {
		var rawPath string
		var count int
		if err := rows.Scan(&rawPath, &count); err != nil {
			return nil, err
		}
		hasRows = true
		addPath(rawPath, count, false)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	dirRows, err := QQuery(db,
		`SELECT COALESCE(path, '')
		   FROM digest_directories
		  WHERE board_id = ?
		    AND (? = '' OR kind = ?)
		  ORDER BY COALESCE(path, '')`,
		boardID, kind, kind,
	)
	if err != nil {
		return nil, err
	}
	defer dirRows.Close()
	for dirRows.Next() {
		var rawPath string
		if err := dirRows.Scan(&rawPath); err != nil {
			return nil, err
		}
		hasRows = true
		addPath(rawPath, 0, true)
	}
	if err := dirRows.Err(); err != nil {
		return nil, err
	}
	if !hasRows {
		return []DigestPathNode{}, nil
	}
	for parent, children := range childSets {
		if node := nodes[parent]; node != nil {
			node.ChildCount = len(children)
		}
	}
	paths := make([]string, 0, len(nodes))
	for path := range nodes {
		paths = append(paths, path)
	}
	sort.Slice(paths, func(i, j int) bool {
		if paths[i] == "" {
			return true
		}
		if paths[j] == "" {
			return false
		}
		return paths[i] < paths[j]
	})
	out := make([]DigestPathNode, 0, len(paths))
	for _, path := range paths {
		out = append(out, *nodes[path])
	}
	return out, nil
}

func ListSiteDigestEntries(db *sql.DB, viewerID string, includePrivate bool, kind, path string, limit, offset int) ([]DigestEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT d.id, d.board_id, b.name, d.target_kind, d.target_id, d.kind, d.title, d.path, d.note, d.body_edited,
		        d.created_by, COALESCE(u.name, ''), d.created_at, d.updated_at,
		        CASE WHEN d.target_kind='thread' THEN tt.id ELSE pt.id END AS thread_id,
		        CASE WHEN d.target_kind='post' THEN p.id ELSE '' END AS post_id,
		        CASE WHEN d.target_kind='thread' THEN tt.author ELSE p.author END AS author,
		        CASE
		          WHEN d.body_edited != 0 THEN SUBSTR(d.body, 1, 180)
		          WHEN d.target_kind='post' THEN SUBSTR(p.body, 1, 180)
		          ELSE tt.title
		        END AS excerpt
		   FROM digest_entries d
		   JOIN boards b ON b.id = d.board_id
		   LEFT JOIN board_settings s ON s.board_id = b.id
		   LEFT JOIN users u ON u.id = d.created_by
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id = d.target_id
		   LEFT JOIN threads pt ON pt.id = p.thread
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id = d.target_id
		  WHERE (? = '' OR d.kind = ?)
		    AND (? = '' OR d.path = ?)
		    AND (
		          COALESCE(s.member_read_mode, 0)=0
		          OR ?=1
		          OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		          OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		        )
		  ORDER BY
		    CASE d.kind WHEN 'announcement' THEN 0 WHEN 'pinned' THEN 1 WHEN 'recommended' THEN 2 WHEN 'digest' THEN 3 ELSE 4 END,
		    d.updated_at DESC,
		    b.name,
		    d.title
		  LIMIT ? OFFSET ?`,
		kind, kind, path, path, boolInt(includePrivate), viewerID, viewerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanDigestEntryRows(rows)
}

func ListPublicRecommendedDigestEntries(db *sql.DB, limit, offset int) ([]DigestEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT d.id, d.board_id, b.name, d.target_kind, d.target_id, d.kind, d.title, d.path, d.note, d.body_edited,
		        d.created_by, COALESCE(u.name, ''), d.created_at, d.updated_at,
		        CASE WHEN d.target_kind='thread' THEN tt.id ELSE pt.id END AS thread_id,
		        CASE WHEN d.target_kind='post' THEN p.id ELSE '' END AS post_id,
		        CASE WHEN d.target_kind='thread' THEN tt.author ELSE p.author END AS author,
		        CASE
		          WHEN d.body_edited != 0 THEN SUBSTR(d.body, 1, 180)
		          WHEN d.target_kind='post' THEN SUBSTR(p.body, 1, 180)
		          ELSE tt.title
		        END AS excerpt
		   FROM digest_entries d
		   JOIN boards b ON b.id = d.board_id
		   JOIN categories c ON c.id = b.id
		   LEFT JOIN board_settings s ON s.board_id = b.id
		   LEFT JOIN users u ON u.id = d.created_by
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id = d.target_id
		   LEFT JOIN threads pt ON pt.id = p.thread
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id = d.target_id
		  WHERE d.kind = 'recommended'
		    AND b.id NOT IN (`+generatedSystemBoardSQLList+`)
		    AND COALESCE(c.visibility, 'public')='public'
		    AND COALESCE(s.member_read_mode, 0)=0
		    AND COALESCE(s.stats_excluded, 0)=0
		  ORDER BY d.updated_at DESC, b.name, d.title
		  LIMIT ? OFFSET ?`,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanDigestEntryRows(rows)
}

func SearchDigestEntries(db *sql.DB, viewerID string, includePrivate bool, boardID, kind, path, query string, limit, offset int) ([]DigestEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	pattern := "%" + escapeLike(strings.ToLower(strings.TrimSpace(query))) + "%"
	rows, err := QQuery(db,
		`SELECT d.id, d.board_id, b.name, d.target_kind, d.target_id, d.kind, d.title, d.path, d.note, d.body_edited,
		        d.created_by, COALESCE(u.name, ''), d.created_at, d.updated_at,
		        CASE WHEN d.target_kind='thread' THEN tt.id ELSE pt.id END AS thread_id,
		        CASE WHEN d.target_kind='post' THEN p.id ELSE '' END AS post_id,
		        CASE WHEN d.target_kind='thread' THEN tt.author ELSE p.author END AS author,
		        CASE
		          WHEN d.body_edited != 0 THEN SUBSTR(d.body, 1, 180)
		          WHEN d.target_kind='post' THEN SUBSTR(p.body, 1, 180)
		          ELSE tt.title
		        END AS excerpt
		   FROM digest_entries d
		   JOIN boards b ON b.id = d.board_id
		   LEFT JOIN board_settings s ON s.board_id = b.id
		   LEFT JOIN users u ON u.id = d.created_by
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id = d.target_id
		   LEFT JOIN threads pt ON pt.id = p.thread
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id = d.target_id
		  WHERE (? = '' OR d.board_id = ?)
		    AND (? = '' OR d.kind = ?)
		    AND (? = '' OR d.path = ?)
		    AND (
		          COALESCE(s.member_read_mode, 0)=0
		          OR ?=1
		          OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		          OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		        )
		    AND (
		          LOWER(COALESCE(d.title, '')) LIKE ? ESCAPE '\'
		          OR LOWER(COALESCE(d.path, '')) LIKE ? ESCAPE '\'
		          OR LOWER(COALESCE(d.note, '')) LIKE ? ESCAPE '\'
		          OR LOWER(CASE
		                WHEN d.body_edited != 0 THEN COALESCE(d.body, '')
		                WHEN d.target_kind='post' THEN COALESCE(p.body, '')
		                ELSE COALESCE(tt.title, '')
		              END) LIKE ? ESCAPE '\'
		          OR LOWER(CASE WHEN d.target_kind='thread' THEN COALESCE(tt.author, '') ELSE COALESCE(p.author, '') END) LIKE ? ESCAPE '\'
		        )
		  ORDER BY
		    CASE
		      WHEN LOWER(COALESCE(d.title, '')) LIKE ? ESCAPE '\' THEN 0
		      WHEN LOWER(COALESCE(d.path, '')) LIKE ? ESCAPE '\' THEN 1
		      ELSE 2
		    END,
		    CASE d.kind WHEN 'announcement' THEN 0 WHEN 'archive' THEN 1 WHEN 'pinned' THEN 2 WHEN 'recommended' THEN 3 WHEN 'digest' THEN 4 ELSE 5 END,
		    d.updated_at DESC,
		    b.name,
		    d.title
		  LIMIT ? OFFSET ?`,
		boardID, boardID, kind, kind, path, path, boolInt(includePrivate), viewerID, viewerID,
		pattern, pattern, pattern, pattern, pattern, pattern, pattern, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanDigestEntryRows(rows)
}

func GetDigestExport(db *sql.DB, entryID string) (*DigestExport, error) {
	var e DigestEntry
	var bodyEdited int
	var editedBody string
	var body string
	err := QQueryRow(db,
		`SELECT d.id, d.board_id, b.name, d.target_kind, d.target_id, d.kind, d.title, d.path, d.note, d.body_edited,
		        d.created_by, COALESCE(u.name, ''), d.created_at, d.updated_at,
		        CASE WHEN d.target_kind='thread' THEN tt.id ELSE pt.id END AS thread_id,
		        CASE WHEN d.target_kind='post' THEN p.id ELSE '' END AS post_id,
		        CASE WHEN d.target_kind='thread' THEN tt.author ELSE p.author END AS author,
		        CASE
		          WHEN d.body_edited != 0 THEN SUBSTR(d.body, 1, 180)
		          WHEN d.target_kind='post' THEN SUBSTR(p.body, 1, 180)
		          ELSE tt.title
		        END AS excerpt,
		        d.body,
		        CASE WHEN d.target_kind='post' THEN COALESCE(p.body, '') ELSE '' END AS body
		   FROM digest_entries d
		   JOIN boards b ON b.id = d.board_id
		   LEFT JOIN users u ON u.id = d.created_by
		   LEFT JOIN posts p ON d.target_kind='post' AND p.id = d.target_id
		   LEFT JOIN threads pt ON pt.id = p.thread
		   LEFT JOIN threads tt ON d.target_kind='thread' AND tt.id = d.target_id
		  WHERE d.id=?`,
		entryID,
	).Scan(&e.ID, &e.BoardID, &e.BoardName, &e.TargetKind, &e.TargetID, &e.Kind, &e.Title, &e.Path, &e.Note, &bodyEdited, &e.CreatedBy, &e.CreatedByName, &e.CreatedAt, &e.UpdatedAt, &e.ThreadID, &e.PostID, &e.Author, &e.Excerpt, &editedBody, &body)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	e.BodyEdited = bodyEdited != 0
	if e.BodyEdited {
		body = editedBody
		return &DigestExport{Entry: e, Body: body}, nil
	}
	if e.TargetKind == "thread" && e.ThreadID != "" {
		threadBody, err := digestThreadTranscript(db, e.ThreadID)
		if err != nil {
			return nil, err
		}
		body = threadBody
	}
	return &DigestExport{Entry: e, Body: body}, nil
}

func digestThreadTranscript(db *sql.DB, threadID string) (string, error) {
	rows, err := QQuery(db,
		`SELECT author, body
		   FROM posts
		  WHERE thread=? AND redacted=0
		  ORDER BY created_seq`,
		threadID,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var author, body string
		if err := rows.Scan(&author, &body); err != nil {
			return "", err
		}
		if b.Len() > 0 {
			b.WriteString("\n\n---\n\n")
		}
		b.WriteString("From: ")
		b.WriteString(author)
		b.WriteString("\n\n")
		b.WriteString(body)
	}
	return b.String(), rows.Err()
}

func FormatDigestExportText(export *DigestExport) string {
	if export == nil {
		return ""
	}
	e := export.Entry
	var b strings.Builder
	b.WriteString(e.Title)
	b.WriteString("\n")
	b.WriteString(strings.Repeat("=", len(e.Title)))
	b.WriteString("\n\n")
	b.WriteString("Board: ")
	if e.BoardName != "" {
		b.WriteString(e.BoardName)
		b.WriteString(" (")
		b.WriteString(e.BoardID)
		b.WriteString(")")
	} else {
		b.WriteString(e.BoardID)
	}
	b.WriteString("\nKind: ")
	b.WriteString(e.Kind)
	if e.Path != "" {
		b.WriteString("\nPath: ")
		b.WriteString(e.Path)
	}
	if e.Author != "" {
		b.WriteString("\nAuthor: ")
		b.WriteString(e.Author)
	}
	if e.Note != "" {
		b.WriteString("\nNote: ")
		b.WriteString(e.Note)
	}
	b.WriteString("\n\n")
	b.WriteString(strings.TrimSpace(export.Body))
	b.WriteString("\n")
	return b.String()
}

func scanDigestEntryRows(rows *sql.Rows) ([]DigestEntry, error) {
	defer rows.Close()
	entries := []DigestEntry{}
	for rows.Next() {
		var e DigestEntry
		var bodyEdited int
		if err := rows.Scan(&e.ID, &e.BoardID, &e.BoardName, &e.TargetKind, &e.TargetID, &e.Kind, &e.Title, &e.Path, &e.Note, &bodyEdited, &e.CreatedBy, &e.CreatedByName, &e.CreatedAt, &e.UpdatedAt, &e.ThreadID, &e.PostID, &e.Author, &e.Excerpt); err != nil {
			return nil, err
		}
		e.BodyEdited = bodyEdited != 0
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func ListMail(db *sql.DB, userID, mailbox string, limit, offset int, unreadOnly bool) ([]MailItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if strings.TrimSpace(mailbox) == "" {
		mailbox = "inbox"
	}
	q := `SELECT m.id, m.from_user_id, COALESCE(u.name, ''), m.subject, m.body, m.parent_id,
	             c.mailbox, c.role, c.read, c.kept, m.created_at, c.updated_at, m.seq
	        FROM mail_copies c
	        JOIN mail_messages m ON m.id = c.message_id
	        LEFT JOIN users u ON u.id = m.from_user_id
	       WHERE c.user_id=? AND c.mailbox=?`
	args := []any{userID, mailbox}
	if unreadOnly {
		q += ` AND c.read=0`
	}
	q += ` ORDER BY c.updated_at DESC, m.created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := QQuery(db, q, args...)
	if err != nil {
		return nil, err
	}
	return scanMailRows(db, rows)
}

func ListMailThread(db *sql.DB, userID, messageID string, limit, offset int) ([]MailItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`WITH RECURSIVE ancestors(id, parent_id, depth) AS (
		      SELECT m.id, m.parent_id, 0
		        FROM mail_messages m
		        JOIN mail_copies c ON c.message_id=m.id
		       WHERE c.user_id=? AND m.id=?
		      UNION ALL
		      SELECT parent.id, parent.parent_id, ancestors.depth+1
		        FROM mail_messages parent
		        JOIN ancestors ON ancestors.parent_id=parent.id
		       WHERE ancestors.depth < 100
		    ),
		    root(id) AS (
		      SELECT id FROM ancestors ORDER BY depth DESC LIMIT 1
		    ),
		    thread_ids(id, depth) AS (
		      SELECT id, 0 FROM root
		      UNION ALL
		      SELECT child.id, thread_ids.depth+1
		        FROM mail_messages child
		        JOIN thread_ids ON child.parent_id=thread_ids.id
		       WHERE thread_ids.depth < 100
		    )
		  SELECT m.id, m.from_user_id, COALESCE(u.name, ''), m.subject, m.body, m.parent_id,
		         c.mailbox, c.role, c.read, c.kept, m.created_at, c.updated_at, m.seq
		    FROM thread_ids
		    JOIN mail_messages m ON m.id=thread_ids.id
		    JOIN mail_copies c ON c.message_id=m.id AND c.user_id=?
		    LEFT JOIN users u ON u.id=m.from_user_id
		   WHERE NOT EXISTS (
		         SELECT 1 FROM mail_copies preferred
		          WHERE preferred.message_id=c.message_id
		            AND preferred.user_id=c.user_id
		            AND preferred.role='recipient'
		            AND c.role<>'recipient'
		   )
		   ORDER BY m.created_at, m.seq, m.id
		   LIMIT ? OFFSET ?`,
		userID, messageID, userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanMailRows(db, rows)
}

func ListMailByAuthor(db *sql.DB, userID, messageID string, limit, offset int) ([]MailItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`WITH selected(from_user_id) AS (
		      SELECT m.from_user_id
		        FROM mail_messages m
		        JOIN mail_copies c ON c.message_id=m.id
		       WHERE c.user_id=? AND m.id=?
		       LIMIT 1
		    )
		  SELECT m.id, m.from_user_id, COALESCE(u.name, ''), m.subject, m.body, m.parent_id,
		         c.mailbox, c.role, c.read, c.kept, m.created_at, c.updated_at, m.seq
		    FROM selected
		    JOIN mail_messages m ON m.from_user_id=selected.from_user_id
		    JOIN mail_copies c ON c.message_id=m.id AND c.user_id=?
		    LEFT JOIN users u ON u.id=m.from_user_id
		   WHERE NOT EXISTS (
		         SELECT 1 FROM mail_copies preferred
		          WHERE preferred.message_id=c.message_id
		            AND preferred.user_id=c.user_id
		            AND preferred.role='recipient'
		            AND c.role<>'recipient'
		   )
		   ORDER BY m.created_at DESC, m.seq DESC, m.id DESC
		   LIMIT ? OFFSET ?`,
		userID, messageID, userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanMailRows(db, rows)
}

func scanMailRows(db *sql.DB, rows *sql.Rows) ([]MailItem, error) {
	items := []MailItem{}
	for rows.Next() {
		var item MailItem
		var read, kept int
		if err := rows.Scan(&item.ID, &item.FromUserID, &item.FromName, &item.Subject, &item.Body, &item.ParentID, &item.Mailbox, &item.Role, &read, &kept, &item.CreatedAt, &item.UpdatedAt, &item.Seq); err != nil {
			return nil, err
		}
		item.Read = read != 0
		item.Kept = kept != 0
		item.Excerpt = excerpt(item.Body, 180)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range items {
		if err := hydrateMailRecipients(db, &items[i]); err != nil {
			return nil, err
		}
		if err := hydrateMailAttachments(db, &items[i]); err != nil {
			return nil, err
		}
	}
	return items, nil
}

func GetMail(db *sql.DB, userID, messageID string) (*MailItem, error) {
	item := &MailItem{}
	var read, kept int
	err := QQueryRow(db,
		`SELECT m.id, m.from_user_id, COALESCE(u.name, ''), m.subject, m.body, m.parent_id,
		        c.mailbox, c.role, c.read, c.kept, m.created_at, c.updated_at, m.seq
		   FROM mail_copies c
		   JOIN mail_messages m ON m.id = c.message_id
		   LEFT JOIN users u ON u.id = m.from_user_id
		  WHERE c.user_id=? AND c.message_id=?
		  ORDER BY CASE c.role WHEN 'recipient' THEN 0 ELSE 1 END
		  LIMIT 1`,
		userID, messageID,
	).Scan(&item.ID, &item.FromUserID, &item.FromName, &item.Subject, &item.Body, &item.ParentID, &item.Mailbox, &item.Role, &read, &kept, &item.CreatedAt, &item.UpdatedAt, &item.Seq)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	item.Read = read != 0
	item.Kept = kept != 0
	item.Excerpt = excerpt(item.Body, 180)
	if err := hydrateMailRecipients(db, item); err != nil {
		return nil, err
	}
	if err := hydrateMailAttachments(db, item); err != nil {
		return nil, err
	}
	return item, nil
}

func CountUnreadMail(db *sql.DB, userID string) (int, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM mail_copies WHERE user_id=? AND mailbox='inbox' AND role='recipient' AND read=0`,
		userID,
	).Scan(&n)
	return n, err
}

func GetMailUsage(db *sql.DB, userID string) (*MailUsage, error) {
	used, err := MailUsedBytes(db, userID)
	if err != nil {
		return nil, err
	}
	return mailUsageFromUsed(userID, used), nil
}

func MailUsedBytes(db *sql.DB, userID string) (int64, error) {
	var used sql.NullInt64
	err := QQueryRow(db,
		`SELECT COALESCE(SUM(LENGTH(m.subject) + LENGTH(m.body) +
		        COALESCE((SELECT SUM(size_bytes) FROM mail_attachments a WHERE a.message_id=m.id), 0)), 0)
		   FROM mail_copies c
		   JOIN mail_messages m ON m.id = c.message_id
		  WHERE c.user_id=? AND c.mailbox <> 'trash'`,
		userID,
	).Scan(&used)
	if err != nil {
		return 0, err
	}
	return used.Int64, nil
}

func mailUsageFromUsed(userID string, used int64) *MailUsage {
	remaining := DefaultMailQuotaBytes - used
	if remaining < 0 {
		remaining = 0
	}
	return &MailUsage{
		UserID:         userID,
		UsedBytes:      used,
		QuotaBytes:     DefaultMailQuotaBytes,
		RemainingBytes: remaining,
	}
}

func ListRelayDeliveries(db *sql.DB, status string, limit, offset int) ([]RelayDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	status = strings.TrimSpace(strings.ToLower(status))
	query := `SELECT id, board_id, thread_id, post_id, author_id, author_name, title, body,
	                 status, last_error, created_at, updated_at, seq
	            FROM relay_deliveries`
	args := []any{}
	if status != "" {
		query += ` WHERE status=?`
		args = append(args, status)
	}
	query += ` ORDER BY seq, created_at, id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := QQuery(db, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RelayDelivery{}
	for rows.Next() {
		var item RelayDelivery
		if err := rows.Scan(
			&item.ID,
			&item.BoardID,
			&item.ThreadID,
			&item.PostID,
			&item.AuthorID,
			&item.AuthorName,
			&item.Title,
			&item.Body,
			&item.Status,
			&item.LastError,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.Seq,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func ListMailAttachments(db *sql.DB, mailID string) ([]MailAttachment, error) {
	rows, err := QQuery(db,
		`SELECT id, message_id, filename, content_type, size_bytes, url,
		        EXISTS(SELECT 1 FROM mail_attachment_blobs b WHERE b.attachment_id=mail_attachments.id),
		        created_by, created_at
		   FROM mail_attachments WHERE message_id=? ORDER BY created_at, id`,
		mailID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []MailAttachment{}
	for rows.Next() {
		var att MailAttachment
		var stored int
		if err := rows.Scan(&att.ID, &att.MailID, &att.Filename, &att.ContentType, &att.SizeBytes, &att.URL, &stored, &att.CreatedBy, &att.CreatedAt); err != nil {
			return nil, err
		}
		att.Stored = stored != 0
		out = append(out, att)
	}
	return out, rows.Err()
}

func GetMailAttachment(db *sql.DB, attachmentID string) (*MailAttachment, error) {
	att := &MailAttachment{}
	var stored int
	err := QQueryRow(db,
		`SELECT id, message_id, filename, content_type, size_bytes, url,
		        EXISTS(SELECT 1 FROM mail_attachment_blobs b WHERE b.attachment_id=mail_attachments.id),
		        created_by, created_at
		   FROM mail_attachments WHERE id=?`,
		attachmentID,
	).Scan(&att.ID, &att.MailID, &att.Filename, &att.ContentType, &att.SizeBytes, &att.URL, &stored, &att.CreatedBy, &att.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	att.Stored = stored != 0
	return att, nil
}

func GetMailAttachmentBlob(db *sql.DB, attachmentID string) ([]byte, string, error) {
	var data []byte
	var contentType string
	err := QQueryRow(db, `SELECT data, content_type FROM mail_attachment_blobs WHERE attachment_id=?`, attachmentID).Scan(&data, &contentType)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	return data, contentType, err
}

func hydrateMailAttachments(db *sql.DB, item *MailItem) error {
	attachments, err := ListMailAttachments(db, item.ID)
	if err != nil {
		return err
	}
	item.Attachments = attachments
	return nil
}

func ListMailGroups(db *sql.DB, ownerID string) ([]MailGroup, error) {
	rows, err := QQuery(db,
		`SELECT id, name, created_at, updated_at
		   FROM mail_groups
		  WHERE user_id=?
		  ORDER BY name`,
		ownerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := []MailGroup{}
	for rows.Next() {
		var g MailGroup
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range groups {
		members, err := ListMailGroupMembers(db, ownerID, groups[i].ID)
		if err != nil {
			return nil, err
		}
		groups[i].Members = members
	}
	return groups, nil
}

func ListMailGroupMembers(db *sql.DB, ownerID, groupRef string) ([]MailGroupMember, error) {
	rows, err := QQuery(db,
		`SELECT gm.user_id, u.name, gm.position
		   FROM mail_groups g
		   JOIN mail_group_members gm ON gm.group_id = g.id
		   JOIN users u ON u.id = gm.user_id
		  WHERE g.user_id=? AND (g.id=? OR g.name=?)
		  ORDER BY gm.position, u.name`,
		ownerID, groupRef, groupRef,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	members := []MailGroupMember{}
	for rows.Next() {
		var m MailGroupMember
		if err := rows.Scan(&m.UserID, &m.Name, &m.Position); err != nil {
			return nil, err
		}
		members = append(members, m)
	}
	return members, rows.Err()
}

func GetMailGroupID(db *sql.DB, ownerID, groupRef string) (string, error) {
	var id string
	err := QQueryRow(db,
		`SELECT id FROM mail_groups WHERE user_id=? AND (id=? OR name=?) LIMIT 1`,
		ownerID, groupRef, groupRef,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

func ListFriendUserIDs(db *sql.DB, ownerID string) ([]string, error) {
	rows, err := QQuery(db,
		`SELECT target_user_id FROM user_relationships WHERE user_id=? AND kind='friend' ORDER BY updated_at DESC`,
		ownerID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func ListDirectMessageConversations(db *sql.DB, userID string, limit, offset int) ([]DirectMessageConversation, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT d.id, d.from_user_id, COALESCE(f.name, ''), d.to_user_id, COALESCE(t.name, ''),
		        d.body, d.created_at
		   FROM direct_messages d
		   LEFT JOIN users f ON f.id = d.from_user_id
		   LEFT JOIN users t ON t.id = d.to_user_id
		  WHERE (d.from_user_id=? AND d.sender_deleted=0)
		     OR (d.to_user_id=? AND d.recipient_deleted=0)
		  ORDER BY d.created_at DESC
		  LIMIT 1000`,
		userID, userID,
	)
	if err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	skipped := 0
	out := []DirectMessageConversation{}
	for rows.Next() {
		var id, fromID, fromName, toID, toName, body string
		var createdAt int64
		if err := rows.Scan(&id, &fromID, &fromName, &toID, &toName, &body, &createdAt); err != nil {
			return nil, err
		}
		otherID, otherName := fromID, fromName
		if fromID == userID {
			otherID, otherName = toID, toName
		}
		if seen[otherID] {
			continue
		}
		seen[otherID] = true
		if skipped < offset {
			skipped++
			continue
		}
		out = append(out, DirectMessageConversation{
			UserID:        otherID,
			Name:          otherName,
			LastMessageID: id,
			LastBody:      excerpt(body, 160),
			LastFromName:  fromName,
			LastAt:        createdAt,
		})
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range out {
		unread, err := countUnreadDirectMessagesWithUser(db, userID, out[i].UserID)
		if err != nil {
			return nil, err
		}
		out[i].UnreadCount = unread
	}
	return out, nil
}

func ListDirectMessages(db *sql.DB, userID, otherUserID string, limit, offset int) ([]DirectMessage, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT d.id, d.conversation_id, d.from_user_id, COALESCE(f.name, ''),
		        d.to_user_id, COALESCE(t.name, ''), d.body, d.read_at, d.created_at, d.seq
		   FROM direct_messages d
		   LEFT JOIN users f ON f.id = d.from_user_id
		   LEFT JOIN users t ON t.id = d.to_user_id
		  WHERE ((d.from_user_id=? AND d.to_user_id=? AND d.sender_deleted=0)
		      OR (d.from_user_id=? AND d.to_user_id=? AND d.recipient_deleted=0))
		  ORDER BY d.created_at ASC
		  LIMIT ? OFFSET ?`,
		userID, otherUserID, otherUserID, userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DirectMessage{}
	for rows.Next() {
		var m DirectMessage
		var readAt int64
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.FromUserID, &m.FromName, &m.ToUserID, &m.ToName, &m.Body, &readAt, &m.CreatedAt, &m.Seq); err != nil {
			return nil, err
		}
		m.Mine = m.FromUserID == userID
		m.Read = m.Mine || readAt != 0
		m.OtherUserID, m.OtherName = m.FromUserID, m.FromName
		if m.Mine {
			m.OtherUserID, m.OtherName = m.ToUserID, m.ToName
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func CountUnreadDirectMessages(db *sql.DB, userID string) (int, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM direct_messages
		  WHERE to_user_id=? AND read_at=0 AND recipient_deleted=0`,
		userID,
	).Scan(&n)
	return n, err
}

func GetDirectMessageSettings(db *sql.DB, userID string) (*DirectMessageSettings, error) {
	settings := &DirectMessageSettings{UserID: userID, Policy: "all"}
	err := QQueryRow(db,
		`SELECT policy, updated_at FROM direct_message_settings WHERE user_id=?`,
		userID,
	).Scan(&settings.Policy, &settings.UpdatedAt)
	if err == sql.ErrNoRows {
		return settings, nil
	}
	return settings, err
}

func ListSocialUsers(db *sql.DB, userID, list string, onlineOnly bool) ([]SocialUser, error) {
	cutoff := NowMS() - 5*60*1000
	var rows *sql.Rows
	var err error
	switch list {
	case "friend", "friends":
		q := `SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
		             '', r.note, 'friend', r.created_at, r.updated_at,
		             COALESCE(p.status, ''), COALESCE(p.last_seen, 0),
		             COALESCE(p.mode, ''), COALESCE(p.board_id, ''), COALESCE(pb.name, ''),
		             COALESCE(p.thread_id, ''), COALESCE(p.location_label, ''), COALESCE(p.from_host, ''),
		             CASE WHEN EXISTS (
		               SELECT 1 FROM user_relationships back
		                WHERE back.user_id = u.id AND back.target_user_id = ? AND back.kind='friend'
		             ) THEN 1 ELSE 0 END AS mutual,
		             CASE WHEN EXISTS (
		               SELECT 1 FROM user_relationships ig
		                WHERE ig.user_id = ? AND ig.target_user_id = u.id AND ig.kind='ignore'
		             ) THEN 1 ELSE 0 END AS ignored
		        FROM user_relationships r
		        JOIN users u ON u.id = r.target_user_id
		        LEFT JOIN user_profiles up ON up.user_id = u.id
		        LEFT JOIN user_presence p ON p.user_id = u.id
		        LEFT JOIN boards pb ON pb.id = p.board_id
		       WHERE r.user_id=? AND r.kind='friend'`
		args := []any{userID, userID, userID}
		if onlineOnly {
			q += ` AND COALESCE(p.last_seen,0) >= ? AND LOWER(COALESCE(p.status,'')) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')`
			args = append(args, cutoff)
		}
		q += ` ORDER BY CASE WHEN COALESCE(p.last_seen,0) >= ? AND LOWER(COALESCE(p.status,'')) NOT IN ('offline', 'invisible', 'cloak', 'cloaked') THEN 0 ELSE 1 END, u.name`
		args = append(args, cutoff)
		rows, err = QQuery(db, q, args...)
	case "fan", "fans":
		q := `SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
		             '', '', 'fan', r.created_at, r.updated_at,
		             COALESCE(p.status, ''), COALESCE(p.last_seen, 0),
		             COALESCE(p.mode, ''), COALESCE(p.board_id, ''), COALESCE(pb.name, ''),
		             COALESCE(p.thread_id, ''), COALESCE(p.location_label, ''), COALESCE(p.from_host, ''),
		             CASE WHEN EXISTS (
		               SELECT 1 FROM user_relationships mine
		                WHERE mine.user_id = ? AND mine.target_user_id = u.id AND mine.kind='friend'
		             ) THEN 1 ELSE 0 END AS mutual,
		             CASE WHEN EXISTS (
		               SELECT 1 FROM user_relationships ig
		                WHERE ig.user_id = ? AND ig.target_user_id = u.id AND ig.kind='ignore'
		             ) THEN 1 ELSE 0 END AS ignored
		        FROM user_relationships r
		        JOIN users u ON u.id = r.user_id
		        LEFT JOIN user_profiles up ON up.user_id = u.id
		        LEFT JOIN user_presence p ON p.user_id = u.id
		        LEFT JOIN boards pb ON pb.id = p.board_id
		       WHERE r.target_user_id=? AND r.kind='friend'`
		args := []any{userID, userID, userID}
		if onlineOnly {
			q += ` AND COALESCE(p.last_seen,0) >= ? AND LOWER(COALESCE(p.status,'')) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')`
			args = append(args, cutoff)
		}
		q += ` ORDER BY CASE WHEN COALESCE(p.last_seen,0) >= ? AND LOWER(COALESCE(p.status,'')) NOT IN ('offline', 'invisible', 'cloak', 'cloaked') THEN 0 ELSE 1 END, u.name`
		args = append(args, cutoff)
		rows, err = QQuery(db, q, args...)
	case "ignore", "ignores":
		q := `SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
		             '', r.note, 'ignore', r.created_at, r.updated_at,
		             COALESCE(p.status, ''), COALESCE(p.last_seen, 0),
		             COALESCE(p.mode, ''), COALESCE(p.board_id, ''), COALESCE(pb.name, ''),
		             COALESCE(p.thread_id, ''), COALESCE(p.location_label, ''), COALESCE(p.from_host, ''),
		             0 AS mutual,
		             1 AS ignored
		        FROM user_relationships r
		        JOIN users u ON u.id = r.target_user_id
		        LEFT JOIN user_profiles up ON up.user_id = u.id
		        LEFT JOIN user_presence p ON p.user_id = u.id
		        LEFT JOIN boards pb ON pb.id = p.board_id
		       WHERE r.user_id=? AND r.kind='ignore'`
		args := []any{userID}
		if onlineOnly {
			q += ` AND COALESCE(p.last_seen,0) >= ? AND LOWER(COALESCE(p.status,'')) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')`
			args = append(args, cutoff)
		}
		q += ` ORDER BY u.name`
		rows, err = QQuery(db, q, args...)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SocialUser{}
	for rows.Next() {
		var u SocialUser
		var mutual, ignored int
		if err := rows.Scan(&u.UserID, &u.Name, &u.Role, &u.DisplayName, &u.SessionID, &u.Note, &u.Kind, &u.CreatedAt, &u.UpdatedAt, &u.Status, &u.LastSeen, &u.Mode, &u.BoardID, &u.BoardName, &u.ThreadID, &u.LocationLabel, &u.FromHost, &mutual, &ignored); err != nil {
			return nil, err
		}
		u.Mutual = mutual != 0
		u.Ignored = ignored != 0
		u.Online = u.LastSeen >= cutoff && visibleOnlineStatus(u.Status)
		if u.Online && u.LastSeen > 0 {
			u.IdleSeconds = (NowMS() - u.LastSeen) / 1000
		}
		if !u.Online && hiddenOnlineStatus(u.Status) {
			u.Status = ""
			u.Mode = ""
			u.BoardID = ""
			u.BoardName = ""
			u.ThreadID = ""
			u.LocationLabel = ""
			u.FromHost = ""
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func ListOnlineUsers(db *sql.DB, viewerID, boardID string, limit, offset int) ([]SocialUser, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	cutoff := NowMS() - 5*60*1000
	q := `SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
	             p.session_id, '', 'online', 0, COALESCE(p.updated_at, 0),
	             COALESCE(p.status, ''), COALESCE(p.last_seen, 0),
	             COALESCE(p.mode, ''), COALESCE(p.board_id, ''), COALESCE(pb.name, ''),
	             COALESCE(p.thread_id, ''), COALESCE(p.location_label, ''), COALESCE(p.from_host, ''),
	             CASE WHEN EXISTS (
	               SELECT 1 FROM user_relationships mine
	                WHERE mine.user_id = ? AND mine.target_user_id = u.id AND mine.kind='friend'
	             ) AND EXISTS (
	               SELECT 1 FROM user_relationships back
	                WHERE back.user_id = u.id AND back.target_user_id = ? AND back.kind='friend'
	             ) THEN 1 ELSE 0 END AS mutual,
	             CASE WHEN EXISTS (
	               SELECT 1 FROM user_relationships ig
	                WHERE ig.user_id = ? AND ig.target_user_id = u.id AND ig.kind='ignore'
	             ) THEN 1 ELSE 0 END AS ignored
	        FROM user_presence_sessions p
	        JOIN users u ON u.id = p.user_id
	        LEFT JOIN user_profiles up ON up.user_id = u.id
	        LEFT JOIN boards pb ON pb.id = p.board_id
	       WHERE p.last_seen >= ? AND LOWER(p.status) NOT IN ('offline', 'invisible')
	         AND (LOWER(p.status) NOT IN ('cloak', 'cloaked')
	           OR EXISTS (
	             SELECT 1 FROM users viewer
	              WHERE viewer.id = ? AND viewer.role IN ('moderator', 'admin')
	           ))
	         AND (? = '' OR p.board_id = ?)
	       ORDER BY p.last_seen DESC, u.name
	       LIMIT ? OFFSET ?`
	rows, err := QQuery(db, q, viewerID, viewerID, viewerID, cutoff, viewerID, boardID, boardID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SocialUser{}
	now := NowMS()
	for rows.Next() {
		var u SocialUser
		var mutual, ignored int
		if err := rows.Scan(&u.UserID, &u.Name, &u.Role, &u.DisplayName, &u.SessionID, &u.Note, &u.Kind, &u.CreatedAt, &u.UpdatedAt, &u.Status, &u.LastSeen, &u.Mode, &u.BoardID, &u.BoardName, &u.ThreadID, &u.LocationLabel, &u.FromHost, &mutual, &ignored); err != nil {
			return nil, err
		}
		u.Mutual = mutual != 0
		u.Ignored = ignored != 0
		u.Online = true
		if u.LastSeen > 0 {
			u.IdleSeconds = (now - u.LastSeen) / 1000
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func visibleOnlineStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status != "" && !hiddenOnlineStatus(status)
}

func hiddenOnlineStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "offline" || status == "invisible" || status == "cloak" || status == "cloaked"
}

func UserIgnores(db *sql.DB, userID, targetUserID string) (bool, error) {
	var found int
	err := QQueryRow(db,
		`SELECT 1 FROM user_relationships WHERE user_id=? AND target_user_id=? AND kind='ignore' LIMIT 1`,
		userID, targetUserID,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func hydrateMailRecipients(db *sql.DB, item *MailItem) error {
	rows, err := QQuery(db,
		`SELECT c.user_id, COALESCE(u.name, '')
		   FROM mail_copies c
		   LEFT JOIN users u ON u.id = c.user_id
		  WHERE c.message_id=? AND c.role='recipient'
		  ORDER BY u.name`,
		item.ID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	item.ToUserIDs = []string{}
	item.ToNames = []string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return err
		}
		item.ToUserIDs = append(item.ToUserIDs, id)
		item.ToNames = append(item.ToNames, name)
	}
	return rows.Err()
}

func countUnreadDirectMessagesWithUser(db *sql.DB, userID, otherUserID string) (int, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM direct_messages
		  WHERE from_user_id=? AND to_user_id=? AND read_at=0 AND recipient_deleted=0`,
		otherUserID, userID,
	).Scan(&n)
	return n, err
}

func excerpt(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= max {
		return s
	}
	return strings.TrimSpace(s[:max]) + "..."
}

func ListFavoriteBoards(db *sql.DB, userID string) ([]Board, error) {
	rows, err := QQuery(db,
		`SELECT b.id, b.name, b.description
		 FROM board_favorites f
		 JOIN boards b ON b.id = f.board_id
		 WHERE f.user_id = ?
		 ORDER BY f.folder_id, f.position, b.name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []Board
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ID, &b.Name, &b.Description); err != nil {
			return nil, err
		}
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func ListFavoriteTree(db *sql.DB, userID string) (*FavoriteTree, error) {
	folderRows, err := QQuery(db,
		`SELECT id, parent_id, name, position, created_at, updated_at
		 FROM favorite_folders
		 WHERE user_id = ?
		 ORDER BY parent_id, position, name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer folderRows.Close()
	tree := &FavoriteTree{
		Folders: []FavoriteFolder{},
		Boards:  []FavoriteBoardEntry{},
	}
	for folderRows.Next() {
		var f FavoriteFolder
		if err := folderRows.Scan(&f.ID, &f.ParentID, &f.Name, &f.Position, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		tree.Folders = append(tree.Folders, f)
	}
	if err := folderRows.Err(); err != nil {
		return nil, err
	}

	boardRows, err := QQuery(db,
		`SELECT b.id, b.name, b.description, f.folder_id, f.position,
		        COALESCE((SELECT COUNT(*) FROM threads t WHERE t.board = b.id AND t.last_seq > COALESCE(m.last_seq, 0)), 0) AS unread_threads,
		        COALESCE((SELECT COUNT(*)
		                  FROM posts p
		                  JOIN threads t ON t.id = p.thread
		                  WHERE t.board = b.id
		                    AND p.created_seq > COALESCE(m.last_seq, 0)
		                    AND p.redacted = 0), 0) AS unread_posts,
		        COALESCE((SELECT MAX(t.last_seq) FROM threads t WHERE t.board = b.id), 0) AS last_seq,
		        COALESCE(m.last_seq, 0) AS read_seq
		   FROM board_favorites f
		   JOIN boards b ON b.id = f.board_id
		   LEFT JOIN board_read_markers m ON m.board_id = b.id AND m.user_id = f.user_id
		  WHERE f.user_id = ?
		  ORDER BY f.folder_id, f.position, b.name`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer boardRows.Close()
	for boardRows.Next() {
		var b FavoriteBoardEntry
		if err := boardRows.Scan(&b.ID, &b.Name, &b.Description, &b.FolderID, &b.Position, &b.UnreadThreads, &b.UnreadPosts, &b.LastSeq, &b.ReadSeq); err != nil {
			return nil, err
		}
		tree.Boards = append(tree.Boards, b)
	}
	return tree, boardRows.Err()
}

func ListBoardSummaries(db *sql.DB, userID string, unreadOnly bool, opts ...BoardSummaryOptions) ([]BoardSummary, error) {
	opt := BoardSummaryOptions{NewDays: 30}
	if len(opts) > 0 {
		opt = opts[0]
		if opt.NewDays <= 0 {
			opt.NewDays = 30
		}
	}
	search := strings.TrimSpace(strings.ToLower(opt.Search))
	searchLike := "%" + search + "%"
	unreadFilter := 0
	if unreadOnly {
		unreadFilter = 1
	}
	newFilter := 0
	if opt.NewOnly {
		newFilter = 1
	}
	newCutoff := NowMS() - int64(opt.NewDays)*24*60*60*1000
	onlineCutoff := NowMS() - 5*60*1000
	orderBy := "favorite DESC, LOWER(name), LOWER(id)"
	switch strings.ToLower(strings.TrimSpace(opt.Sort)) {
	case "name", "":
		orderBy = "favorite DESC, LOWER(name), LOWER(id)"
	case "new", "newest":
		orderBy = "created_at DESC, favorite DESC, LOWER(name), LOWER(id)"
	case "online":
		orderBy = "online_users DESC, favorite DESC, LOWER(name), LOWER(id)"
	case "posts", "articles":
		orderBy = "post_count DESC, thread_count DESC, favorite DESC, LOWER(name), LOWER(id)"
	case "threads":
		orderBy = "thread_count DESC, post_count DESC, favorite DESC, LOWER(name), LOWER(id)"
	case "activity", "recent":
		orderBy = "last_seq DESC, favorite DESC, LOWER(name), LOWER(id)"
	case "unread":
		orderBy = "unread_posts DESC, unread_threads DESC, favorite DESC, LOWER(name), LOWER(id)"
	}
	rows, err := QQuery(db,
		`WITH board_state AS (
		    SELECT b.id, b.name, b.description,
		           CASE WHEN f.board_id IS NULL THEN 0 ELSE 1 END AS favorite,
		           COALESCE(m.last_seq, 0) AS read_seq,
		           COALESCE(c.created_at, 0) AS created_at,
		           CASE WHEN COALESCE(c.created_at, 0) > 0 AND COALESCE(c.created_at, 0) >= ? THEN 1 ELSE 0 END AS new_board,
		           COALESCE((SELECT MAX(t.last_seq) FROM threads t WHERE t.board = b.id), 0) AS last_seq,
		           COALESCE((SELECT COUNT(*) FROM threads t WHERE t.board = b.id), 0) AS thread_count,
		           COALESCE((SELECT COUNT(*)
		                     FROM posts p
		                     JOIN threads t ON t.id = p.thread
		                     WHERE t.board = b.id
		                       AND p.redacted = 0), 0) AS post_count,
		           COALESCE((SELECT COUNT(DISTINCT ups.user_id)
		                     FROM user_presence_sessions ups
		                     WHERE ups.board_id = b.id
		                       AND ups.last_seen >= ?
		                       AND LOWER(ups.status) NOT IN ('offline', 'invisible', 'cloak', 'cloaked')), 0) AS online_users,
		           CASE WHEN bz.board_id IS NOT NULL AND COALESCE(s.zap_allowed, 1) != 0 THEN 1 ELSE 0 END AS zapped,
		           COALESCE((SELECT COUNT(*) FROM threads t WHERE t.board = b.id AND t.last_seq > COALESCE(m.last_seq, 0)), 0) AS unread_threads,
		           COALESCE((SELECT COUNT(*)
		                     FROM posts p
		                     JOIN threads t ON t.id = p.thread
		                     WHERE t.board = b.id
		                       AND p.created_seq > COALESCE(m.last_seq, 0)
		                       AND p.redacted = 0), 0) AS unread_posts
		      FROM boards b
		      LEFT JOIN categories c ON c.id = b.id
		      LEFT JOIN board_favorites f ON f.board_id = b.id AND f.user_id = ?
		      LEFT JOIN board_zaps bz ON bz.board_id = b.id AND bz.user_id = ?
		      LEFT JOIN board_read_markers m ON m.board_id = b.id AND m.user_id = ?
		      LEFT JOIN board_settings s ON s.board_id = b.id
		)
		SELECT id, name, description, favorite, unread_threads, unread_posts,
		       thread_count, post_count, online_users, last_seq, read_seq, created_at, new_board, zapped,
		       COALESCE((SELECT anonymous_allowed FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT read_only FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT no_reply FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT attachments_allowed FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT mail_in_allowed FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT relay_enabled FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT member_read_mode FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT member_post_mode FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT stats_excluded FROM board_settings WHERE board_id=id), 0),
		       COALESCE((SELECT zap_allowed FROM board_settings WHERE board_id=id), 1),
		       COALESCE((SELECT COUNT(*) FROM board_moderators bm WHERE bm.board_id=id), 0)
		  FROM board_state
		 WHERE (? = 0 OR (unread_posts > 0 AND zapped = 0))
		   AND (? = '' OR LOWER(id) LIKE ? OR LOWER(name) LIKE ? OR LOWER(description) LIKE ?)
		   AND (? = 0 OR new_board = 1)
		 ORDER BY `+orderBy,
		newCutoff, onlineCutoff, userID, userID, userID, unreadFilter, search, searchLike, searchLike, searchLike, newFilter,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var boards []BoardSummary
	for rows.Next() {
		var b BoardSummary
		var favorite int
		var newBoard int
		var zapped int
		var anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int
		if err := rows.Scan(&b.ID, &b.Name, &b.Description, &favorite, &b.UnreadThreads, &b.UnreadPosts, &b.ThreadCount, &b.PostCount, &b.OnlineUsers, &b.LastSeq, &b.ReadSeq, &b.CreatedAt, &newBoard, &zapped, &anonymousAllowed, &readOnly, &noReply, &attachmentsAllowed, &mailInAllowed, &relayEnabled, &memberReadMode, &memberPostMode, &statsExcluded, &zapAllowed, &b.ModeratorCount); err != nil {
			return nil, err
		}
		b.Favorite = favorite != 0
		b.NewBoard = newBoard != 0
		b.Zapped = zapped != 0
		applyBoardSummaryPolicyFlags(&b, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed)
		boards = append(boards, b)
	}
	return boards, rows.Err()
}

func ListCategories(db *sql.DB) ([]Category, error) {
	rows, err := QQuery(db,
		`SELECT id, name, description, parent_id, position, visibility, created_at, updated_at
		 FROM categories ORDER BY position, name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var categories []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.ParentID, &c.Position, &c.Visibility, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		categories = append(categories, c)
	}
	return categories, rows.Err()
}

func ListThreads(db *sql.DB, boardID string, limit, offset int) ([]Thread, error) {
	rows, err := QQuery(db,
		`SELECT id, board, author, COALESCE(author_id,''), title, locked, post_count, last_seq, created_ts, created_at, updated_at
		 FROM threads WHERE board=? ORDER BY last_seq DESC LIMIT ? OFFSET ?`,
		boardID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var threads []Thread
	for rows.Next() {
		var t Thread
		var locked int
		if err := rows.Scan(&t.ID, &t.Board, &t.Author, &t.AuthorID, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		if t.CreatedAt == 0 {
			t.CreatedAt = t.CreatedTS
		}
		if t.UpdatedAt == 0 {
			t.UpdatedAt = t.CreatedAt
		}
		t.Locked = locked != 0
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

func ListThreadSummaries(db *sql.DB, userID, boardID string, limit, offset int, unreadOnly bool) ([]ThreadSummary, error) {
	return ListThreadSummariesFiltered(db, userID, boardID, "", "", limit, offset, unreadOnly)
}

func ListThreadSummariesFiltered(db *sql.DB, userID, boardID, titleQuery, authorQuery string, limit, offset int, unreadOnly bool) ([]ThreadSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	titleSearch := strings.TrimSpace(strings.ToLower(titleQuery))
	authorSearch := strings.TrimSpace(strings.ToLower(authorQuery))
	titleLike := "%" + escapeLike(titleSearch) + "%"
	authorLike := "%" + escapeLike(authorSearch) + "%"
	unreadFilter := 0
	if unreadOnly {
		unreadFilter = 1
	}
	rows, err := QQuery(db,
		`WITH thread_state AS (
		    SELECT t.id, t.board, b.name AS board_name, t.author, COALESCE(t.author_id,'') AS author_id, t.title, t.locked, t.post_count,
		           t.last_seq, t.created_ts, t.created_at, t.updated_at,
		           CASE
		             WHEN COALESCE(trm.last_seq, 0) > COALESCE(brm.last_seq, 0) THEN COALESCE(trm.last_seq, 0)
		             ELSE COALESCE(brm.last_seq, 0)
		           END AS read_seq
		      FROM threads t
		      JOIN boards b ON b.id = t.board
		      LEFT JOIN board_read_markers brm ON brm.board_id = t.board AND brm.user_id = ?
		      LEFT JOIN thread_read_markers trm ON trm.thread_id = t.id AND trm.user_id = ?
		     WHERE t.board = ?
		       AND (? = '' OR LOWER(COALESCE(t.title, '')) LIKE ? ESCAPE '\')
		       AND (? = '' OR LOWER(COALESCE(t.author, '')) LIKE ? ESCAPE '\' OR LOWER(COALESCE(t.author_id, '')) LIKE ? ESCAPE '\')
		)
		SELECT id, board, board_name, author, author_id, title, locked, post_count, last_seq, created_ts, created_at, updated_at, read_seq,
		       unread_posts,
		       first_unread_post
		  FROM (
		    SELECT thread_state.*,
		           COALESCE((SELECT COUNT(*) FROM posts p WHERE p.thread = thread_state.id AND p.created_seq > read_seq AND p.redacted = 0), 0) AS unread_posts,
		           COALESCE((SELECT p.id FROM posts p WHERE p.thread = thread_state.id AND p.created_seq > read_seq AND p.redacted = 0 ORDER BY p.created_seq LIMIT 1), '') AS first_unread_post
		      FROM thread_state
		  ) summary
		 WHERE ? = 0 OR unread_posts > 0
		 ORDER BY last_seq DESC LIMIT ? OFFSET ?`,
		userID, userID, boardID, titleSearch, titleLike, authorSearch, authorLike, authorLike, unreadFilter, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var threads []ThreadSummary
	for rows.Next() {
		var t ThreadSummary
		var locked int
		if err := rows.Scan(&t.ID, &t.Board, &t.BoardName, &t.Author, &t.AuthorID, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS, &t.CreatedAt, &t.UpdatedAt, &t.ReadSeq, &t.UnreadPosts, &t.FirstUnreadPostID); err != nil {
			return nil, err
		}
		if t.CreatedAt == 0 {
			t.CreatedAt = t.CreatedTS
		}
		if t.UpdatedAt == 0 {
			t.UpdatedAt = t.CreatedAt
		}
		t.Locked = locked != 0
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

func ListUnreadThreadSummaries(db *sql.DB, userID string, includePrivate bool, favoritesOnly bool, folderID string, limit, offset int) ([]ThreadSummary, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if folderID != "" {
		favoritesOnly = true
	}
	rows, err := QQuery(db,
		`WITH RECURSIVE folder_scope(id) AS (
		    SELECT ? WHERE ? <> ''
		    UNION ALL
		    SELECT child.id
		      FROM favorite_folders child
		      JOIN folder_scope parent ON parent.id = child.parent_id
		     WHERE child.user_id = ?
		),
		allowed_boards AS (
		    SELECT b.id
		      FROM boards b
		      LEFT JOIN board_settings s ON s.board_id = b.id
		      LEFT JOIN board_zaps bz ON bz.board_id = b.id AND bz.user_id = ?
		     WHERE (
		              COALESCE(s.member_read_mode, 0)=0
		              OR ?=1
		              OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		              OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		           )
		       AND (bz.board_id IS NULL OR COALESCE(s.zap_allowed, 1)=0)
		       AND (
		              ?=0
		              OR EXISTS (
		                SELECT 1 FROM board_favorites f
		                 WHERE f.user_id=?
		                   AND f.board_id=b.id
		                   AND (?='' OR f.folder_id IN (SELECT id FROM folder_scope))
		              )
		           )
		),
		thread_state AS (
		    SELECT t.id, t.board, b.name AS board_name, t.author, COALESCE(t.author_id,'') AS author_id, t.title, t.locked, t.post_count,
		           t.last_seq, t.created_ts, t.created_at, t.updated_at,
		           CASE
		             WHEN COALESCE(trm.last_seq, 0) > COALESCE(brm.last_seq, 0) THEN COALESCE(trm.last_seq, 0)
		             ELSE COALESCE(brm.last_seq, 0)
		           END AS read_seq
		      FROM threads t
		      JOIN allowed_boards ab ON ab.id = t.board
		      JOIN boards b ON b.id = t.board
		      LEFT JOIN board_read_markers brm ON brm.board_id = t.board AND brm.user_id = ?
		      LEFT JOIN thread_read_markers trm ON trm.thread_id = t.id AND trm.user_id = ?
		)
		SELECT id, board, board_name, author, author_id, title, locked, post_count, last_seq, created_ts, created_at, updated_at, read_seq,
		       unread_posts,
		       first_unread_post
		  FROM (
		    SELECT thread_state.*,
		           COALESCE((SELECT COUNT(*) FROM posts p WHERE p.thread = thread_state.id AND p.created_seq > read_seq AND p.redacted = 0), 0) AS unread_posts,
		           COALESCE((SELECT p.id FROM posts p WHERE p.thread = thread_state.id AND p.created_seq > read_seq AND p.redacted = 0 ORDER BY p.created_seq LIMIT 1), '') AS first_unread_post
		      FROM thread_state
		  ) summary
		 WHERE unread_posts > 0
		 ORDER BY last_seq DESC LIMIT ? OFFSET ?`,
		folderID, folderID, userID,
		userID, boolInt(includePrivate), userID, userID,
		boolInt(favoritesOnly), userID, folderID,
		userID, userID,
		limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var threads []ThreadSummary
	for rows.Next() {
		var t ThreadSummary
		var locked int
		if err := rows.Scan(&t.ID, &t.Board, &t.BoardName, &t.Author, &t.AuthorID, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS, &t.CreatedAt, &t.UpdatedAt, &t.ReadSeq, &t.UnreadPosts, &t.FirstUnreadPostID); err != nil {
			return nil, err
		}
		if t.CreatedAt == 0 {
			t.CreatedAt = t.CreatedTS
		}
		if t.UpdatedAt == 0 {
			t.UpdatedAt = t.CreatedAt
		}
		t.Locked = locked != 0
		threads = append(threads, t)
	}
	return threads, rows.Err()
}

func GetThread(db *sql.DB, id string) (*Thread, error) {
	t := &Thread{}
	var locked int
	err := QQueryRow(db,
		`SELECT id, board, author, COALESCE(author_id,''), title, locked, post_count, last_seq, created_ts, created_at, updated_at FROM threads WHERE id=?`, id,
	).Scan(&t.ID, &t.Board, &t.Author, &t.AuthorID, &t.Title, &locked, &t.PostCount, &t.LastSeq, &t.CreatedTS, &t.CreatedAt, &t.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if t.CreatedAt == 0 {
		t.CreatedAt = t.CreatedTS
	}
	if t.UpdatedAt == 0 {
		t.UpdatedAt = t.CreatedAt
	}
	t.Locked = locked != 0
	return t, nil
}

func ListPosts(db *sql.DB, threadID string, limit, offset int) ([]Post, error) {
	rows, err := QQuery(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, COALESCE(signature,''), content_type, COALESCE(reply_to,''), version, redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=posts.id), 0),
		        created_seq, updated_seq, created_at, updated_at
		 FROM posts WHERE thread=? ORDER BY created_seq LIMIT ? OFFSET ?`,
		threadID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	return scanSearchPostRows(db, rows)
}

func SearchReadablePosts(db *sql.DB, viewerID string, includePrivate bool, query, boardID string, limit int) ([]Post, error) {
	var rows *sql.Rows
	var err error
	if boardID != "" {
		rows, err = QQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		 FROM posts_fts f
		 JOIN posts p ON p.id = f.post_id
		 JOIN threads t ON t.id = p.thread
		 LEFT JOIN board_settings s ON s.board_id = t.board
		 WHERE f.board_id=? AND posts_fts MATCH ? AND p.redacted=0
		   AND (
		     COALESCE(s.member_read_mode, 0)=0
		     OR ?=1
		     OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=t.board AND bm.user_id=?)
		     OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=t.board AND m.user_id=?)
		   )
		 ORDER BY rank LIMIT ?`,
			boardID, query, boolInt(includePrivate), viewerID, viewerID, limit,
		)
	} else {
		rows, err = QQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		 FROM posts_fts f
		 JOIN posts p ON p.id = f.post_id
		 JOIN threads t ON t.id = p.thread
		 LEFT JOIN board_settings s ON s.board_id = t.board
		 WHERE posts_fts MATCH ? AND p.redacted=0
		   AND (
		     COALESCE(s.member_read_mode, 0)=0
		     OR ?=1
		     OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=t.board AND bm.user_id=?)
		     OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=t.board AND m.user_id=?)
		   )
		 ORDER BY rank LIMIT ?`,
			query, boolInt(includePrivate), viewerID, viewerID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	return scanSearchPostRows(db, rows)
}

func scanSearchPostRows(db *sql.DB, rows *sql.Rows) ([]Post, error) {
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachPostAttachments(db, posts)
}

func ListReplyTreePosts(db *sql.DB, rootPostID string, limit, offset int) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`WITH RECURSIVE reply_tree(id, depth) AS (
		    SELECT id, 0 FROM posts WHERE id=?
		    UNION ALL
		    SELECT child.id, reply_tree.depth + 1
		      FROM posts child
		      JOIN reply_tree ON child.reply_to=reply_tree.id
		)
		SELECT p.id, p.thread, t.board, b.name AS board_name, t.title AS thread_title,
		       p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		       COALESCE(p.reply_to,''), reply_tree.depth, p.version, p.redacted,
		       COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		       p.created_seq, p.updated_seq, p.created_at, p.updated_at
		  FROM reply_tree
		  JOIN posts p ON p.id=reply_tree.id
		  JOIN threads t ON t.id=p.thread
		  JOIN boards b ON b.id=t.board
		 ORDER BY reply_tree.depth, p.created_seq
		 LIMIT ? OFFSET ?`,
		rootPostID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Board, &p.BoardName, &p.ThreadTitle, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType,
			&p.ReplyTo, &p.ReplyDepth, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachPostAttachments(db, posts)
}

func ListPostAttachments(db *sql.DB, postID string) ([]PostAttachment, error) {
	rows, err := QQuery(db,
		`SELECT id, post_id, filename, content_type, size_bytes, url,
		        EXISTS(SELECT 1 FROM attachment_blobs b WHERE b.attachment_id=post_attachments.id),
		        created_by, created_at
		 FROM post_attachments WHERE post_id=? ORDER BY created_at, id`,
		postID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PostAttachment{}
	for rows.Next() {
		var att PostAttachment
		var stored int
		if err := rows.Scan(&att.ID, &att.PostID, &att.Filename, &att.ContentType, &att.SizeBytes, &att.URL, &stored, &att.CreatedBy, &att.CreatedAt); err != nil {
			return nil, err
		}
		att.Stored = stored != 0
		out = append(out, att)
	}
	return out, rows.Err()
}

func GetPostAttachment(db *sql.DB, attachmentID string) (*PostAttachment, error) {
	att := &PostAttachment{}
	var stored int
	err := QQueryRow(db,
		`SELECT id, post_id, filename, content_type, size_bytes, url,
		        EXISTS(SELECT 1 FROM attachment_blobs b WHERE b.attachment_id=post_attachments.id),
		        created_by, created_at
		   FROM post_attachments WHERE id=?`,
		attachmentID,
	).Scan(&att.ID, &att.PostID, &att.Filename, &att.ContentType, &att.SizeBytes, &att.URL, &stored, &att.CreatedBy, &att.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	att.Stored = stored != 0
	return att, nil
}

func GetAttachmentBlob(db *sql.DB, attachmentID string) ([]byte, string, error) {
	var data []byte
	var contentType string
	err := QQueryRow(db, `SELECT data, content_type FROM attachment_blobs WHERE attachment_id=?`, attachmentID).Scan(&data, &contentType)
	if err == sql.ErrNoRows {
		return nil, "", nil
	}
	return data, contentType, err
}

func attachPostAttachments(db *sql.DB, posts []Post) ([]Post, error) {
	for i := range posts {
		attachments, err := ListPostAttachments(db, posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].Attachments = attachments
		if err := hydratePostMetadata(db, &posts[i]); err != nil {
			return nil, err
		}
	}
	return posts, nil
}

func hydratePostMetadata(db *sql.DB, post *Post) error {
	var marked, recommended, noReply, tex, mailBack int
	err := QQueryRow(db, `SELECT marked, recommended, no_reply, tex, mail_back,
	        COALESCE(source_post,''), COALESCE(source_thread,''), COALESCE(source_board,''),
	        COALESCE(source_author,''), COALESCE(source_author_id,''), COALESCE(source_title,'')
	    FROM posts WHERE id=?`, post.ID).Scan(
		&marked, &recommended, &noReply, &tex, &mailBack,
		&post.SourcePost, &post.SourceThread, &post.SourceBoard,
		&post.SourceAuthor, &post.SourceAuthorID, &post.SourceTitle,
	)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	post.Marked = marked != 0
	post.Recommended = recommended != 0
	post.NoReply = noReply != 0
	post.TeX = tex != 0
	post.MailBack = mailBack != 0
	return nil
}

func ListPostsByAuthor(db *sql.DB, name string, limit, offset int) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT p.id, p.thread, t.board, b.name AS board_name, t.title AS thread_title,
		        p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		   JOIN boards b ON b.id=t.board
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE p.author=? AND p.redacted=0
		    AND COALESCE(s.member_read_mode, 0)=0
		  ORDER BY p.created_seq DESC LIMIT ? OFFSET ?`,
		name, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Board, &p.BoardName, &p.ThreadTitle, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachPostAttachments(db, posts)
}

func ListReadablePostsByAuthor(db *sql.DB, viewerID string, includePrivate bool, name string, limit, offset int) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT p.id, p.thread, t.board, b.name AS board_name, t.title AS thread_title,
		        p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		   FROM posts p
		   JOIN threads t ON t.id=p.thread
		   JOIN boards b ON b.id=t.board
		   LEFT JOIN board_settings s ON s.board_id=b.id
		  WHERE p.author=? AND p.redacted=0
		    AND (
		           COALESCE(s.member_read_mode, 0)=0
		           OR ?=1
		           OR EXISTS (SELECT 1 FROM board_moderators bm WHERE bm.board_id=b.id AND bm.user_id=?)
		           OR EXISTS (SELECT 1 FROM board_members m WHERE m.board_id=b.id AND m.user_id=?)
		        )
		  ORDER BY p.created_seq DESC LIMIT ? OFFSET ?`,
		name, boolInt(includePrivate), viewerID, viewerID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Board, &p.BoardName, &p.ThreadTitle, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachPostAttachments(db, posts)
}

func ListResidentBoardPosts(db *sql.DB, userID string, limit, offset int) ([]Post, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT p.id, p.thread, t.board, b.name AS board_name, t.title AS thread_title,
		        p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		   FROM board_members bm
		   JOIN boards b ON b.id=bm.board_id
		   JOIN threads t ON t.board=bm.board_id
		   JOIN posts p ON p.thread=t.id
		  WHERE bm.user_id=?
		    AND p.redacted=0
		  ORDER BY p.created_seq DESC
		  LIMIT ? OFFSET ?`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Board, &p.BoardName, &p.ThreadTitle, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attachPostAttachments(db, posts)
}

func ListBoardDeletedPosts(db *sql.DB, boardID, kind string, limit, offset int) ([]PostDeletion, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	if offset < 0 {
		offset = 0
	}
	kind = strings.TrimSpace(strings.ToLower(kind))
	if kind != "recycle" && kind != "junk" {
		kind = ""
	}
	rows, err := QQuery(db,
		`SELECT d.post_id, d.thread_id, d.board_id, b.name, t.title,
		        COALESCE(d.deleted_by_id, ''), COALESCE(d.deleted_by_name, ''), COALESCE(d.reason, ''),
		        d.kind, d.deleted_at, d.seq,
		        p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		   FROM post_deletions d
		   JOIN posts p ON p.id=d.post_id
		   JOIN threads t ON t.id=d.thread_id
		   JOIN boards b ON b.id=d.board_id
		  WHERE d.board_id=?
		    AND p.redacted=1
		    AND (?='' OR d.kind=?)
		  ORDER BY d.deleted_at DESC, d.seq DESC
		  LIMIT ? OFFSET ?`,
		boardID, kind, kind, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PostDeletion{}
	for rows.Next() {
		var d PostDeletion
		var redacted int
		if err := rows.Scan(
			&d.PostID, &d.ThreadID, &d.BoardID, &d.BoardName, &d.ThreadTitle,
			&d.DeletedByID, &d.DeletedByName, &d.Reason, &d.Kind, &d.DeletedAt, &d.Seq,
			&d.Post.Author, &d.Post.AuthorID, &d.Post.Body, &d.Post.Signature, &d.Post.ContentType,
			&d.Post.ReplyTo, &d.Post.Version, &redacted, &d.Post.ReactionCount,
			&d.Post.CreatedSeq, &d.Post.UpdatedSeq, &d.Post.CreatedAt, &d.Post.UpdatedAt,
		); err != nil {
			return nil, err
		}
		d.Post.ID = d.PostID
		d.Post.Thread = d.ThreadID
		d.Post.Board = d.BoardID
		d.Post.BoardName = d.BoardName
		d.Post.ThreadTitle = d.ThreadTitle
		d.Post.Redacted = redacted != 0
		if d.Post.CreatedAt == 0 {
			d.Post.CreatedAt = d.Post.CreatedSeq
		}
		if d.Post.UpdatedAt == 0 {
			d.Post.UpdatedAt = d.Post.CreatedAt
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		attachments, err := ListPostAttachments(db, out[i].PostID)
		if err != nil {
			return nil, err
		}
		out[i].Post.Attachments = attachments
		if err := hydratePostMetadata(db, &out[i].Post); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func applyBoardPolicyFlags(b *Board, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int) {
	b.AnonymousAllowed = anonymousAllowed != 0
	b.ReadOnly = readOnly != 0
	b.NoReply = noReply != 0
	b.AttachmentsAllowed = attachmentsAllowed != 0
	b.MailInAllowed = mailInAllowed != 0
	b.RelayEnabled = relayEnabled != 0
	b.MemberReadMode = memberReadMode != 0
	b.MemberPostMode = memberPostMode != 0
	b.StatsExcluded = statsExcluded != 0
	b.ZapAllowed = zapAllowed != 0
}

func applyBoardSummaryPolicyFlags(b *BoardSummary, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int) {
	b.AnonymousAllowed = anonymousAllowed != 0
	b.ReadOnly = readOnly != 0
	b.NoReply = noReply != 0
	b.AttachmentsAllowed = attachmentsAllowed != 0
	b.MailInAllowed = mailInAllowed != 0
	b.RelayEnabled = relayEnabled != 0
	b.MemberReadMode = memberReadMode != 0
	b.MemberPostMode = memberPostMode != 0
	b.StatsExcluded = statsExcluded != 0
	b.ZapAllowed = zapAllowed != 0
	if !b.ZapAllowed {
		b.Zapped = false
	}
}

func applySettingsFlags(s *BoardSettings, anonymousAllowed, readOnly, noReply, attachmentsAllowed, mailInAllowed, relayEnabled, memberReadMode, memberPostMode, statsExcluded, zapAllowed int) {
	s.AnonymousAllowed = anonymousAllowed != 0
	s.ReadOnly = readOnly != 0
	s.NoReply = noReply != 0
	s.AttachmentsAllowed = attachmentsAllowed != 0
	s.MailInAllowed = mailInAllowed != 0
	s.RelayEnabled = relayEnabled != 0
	s.MemberReadMode = memberReadMode != 0
	s.MemberPostMode = memberPostMode != 0
	s.StatsExcluded = statsExcluded != 0
	s.ZapAllowed = zapAllowed != 0
}

func GetPost(db *sql.DB, id string) (*Post, error) {
	p := &Post{}
	var redacted int
	err := QQueryRow(db,
		`SELECT id, thread, author, COALESCE(author_id,''), body, COALESCE(signature,''), content_type, COALESCE(reply_to,''), version, redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=posts.id), 0),
		        created_seq, updated_seq, created_at, updated_at FROM posts WHERE id=?`, id,
	).Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType, &p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.CreatedAt == 0 {
		p.CreatedAt = p.CreatedSeq
	}
	if p.UpdatedAt == 0 {
		p.UpdatedAt = p.CreatedAt
	}
	p.Redacted = redacted != 0
	attachments, err := ListPostAttachments(db, p.ID)
	if err != nil {
		return nil, err
	}
	p.Attachments = attachments
	if err := hydratePostMetadata(db, p); err != nil {
		return nil, err
	}
	return p, nil
}

func GetUserByID(db *sql.DB, id string) (*User, error) {
	u := &User{}
	err := QQueryRow(db, `SELECT id, name, role, password, created,
	        COALESCE(NULLIF(registration_status,''), 'approved'), COALESCE(reviewed_at,0), COALESCE(reviewed_by,''), COALESCE(review_reason,''),
	        COALESCE(deactivated_at,0), COALESCE(deactivated_by,''), COALESCE(deactivated_reason,'')
	    FROM users WHERE id=?`, id).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created,
			&u.RegistrationStatus, &u.ReviewedAt, &u.ReviewedBy, &u.ReviewReason,
			&u.DeactivatedAt, &u.DeactivatedBy, &u.DeactivatedReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func GetUserByName(db *sql.DB, name string) (*User, error) {
	u := &User{}
	err := QQueryRow(db, `SELECT id, name, role, password, created,
	        COALESCE(NULLIF(registration_status,''), 'approved'), COALESCE(reviewed_at,0), COALESCE(reviewed_by,''), COALESCE(review_reason,''),
	        COALESCE(deactivated_at,0), COALESCE(deactivated_by,''), COALESCE(deactivated_reason,'')
	    FROM users WHERE name=?`, name).
		Scan(&u.ID, &u.Name, &u.Role, &u.Password, &u.Created,
			&u.RegistrationStatus, &u.ReviewedAt, &u.ReviewedBy, &u.ReviewReason,
			&u.DeactivatedAt, &u.DeactivatedBy, &u.DeactivatedReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func GetAccountRegistrationSettings(db *sql.DB) (*AccountRegistrationSettings, error) {
	out := &AccountRegistrationSettings{}
	var requireApproval int
	err := QQueryRow(db,
		`SELECT require_approval, updated_at
		   FROM account_registration_settings
		  WHERE id='default'`,
	).Scan(&requireApproval, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	out.RequireApproval = requireApproval != 0
	return out, nil
}

func ListAccountRegistrations(db *sql.DB, status string, limit, offset int) ([]AccountRegistration, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT u.id, u.name, u.role, COALESCE(NULLIF(u.registration_status,''), 'approved'),
		        u.created, COALESCE(u.reviewed_at,0), COALESCE(u.reviewed_by,''), COALESCE(r.name,''), COALESCE(u.review_reason,'')
		   FROM users u
		   LEFT JOIN users r ON r.id = u.reviewed_by
		  WHERE (?='all' OR COALESCE(NULLIF(u.registration_status,''), 'approved')=?)
		  ORDER BY CASE COALESCE(NULLIF(u.registration_status,''), 'approved') WHEN 'pending' THEN 0 ELSE 1 END,
		           u.created DESC, u.name
		  LIMIT ? OFFSET ?`,
		status, status, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountRegistration{}
	for rows.Next() {
		var row AccountRegistration
		if err := rows.Scan(&row.ID, &row.Name, &row.Role, &row.Status, &row.Created, &row.ReviewedAt, &row.ReviewedBy, &row.ReviewedByName, &row.ReviewReason); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func GetAccountRegistrationByID(db *sql.DB, userID string) (*AccountRegistration, error) {
	row := &AccountRegistration{}
	err := QQueryRow(db,
		`SELECT u.id, u.name, u.role, COALESCE(NULLIF(u.registration_status,''), 'approved'),
		        u.created, COALESCE(u.reviewed_at,0), COALESCE(u.reviewed_by,''), COALESCE(r.name,''), COALESCE(u.review_reason,'')
		   FROM users u
		   LEFT JOIN users r ON r.id = u.reviewed_by
		  WHERE u.id=?`,
		userID,
	).Scan(&row.ID, &row.Name, &row.Role, &row.Status, &row.Created, &row.ReviewedAt, &row.ReviewedBy, &row.ReviewedByName, &row.ReviewReason)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func ListPasswordRecoveryRequests(db *sql.DB, status string, limit, offset int) ([]PasswordRecoveryRequest, error) {
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := QQuery(db,
		`SELECT r.id, r.user_id, u.name, r.status, r.submitted_name, r.submitted_email, r.note,
		        r.reviewer_id, COALESCE(rv.name,''), r.review_note, r.created_at, r.updated_at
		   FROM password_recovery_requests r
		   JOIN users u ON u.id = r.user_id
		   LEFT JOIN users rv ON rv.id = r.reviewer_id
		  WHERE (?='all' OR r.status=?)
		  ORDER BY CASE r.status WHEN 'pending' THEN 0 ELSE 1 END, r.updated_at DESC, r.created_at DESC
		  LIMIT ? OFFSET ?`,
		status, status, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []PasswordRecoveryRequest{}
	for rows.Next() {
		var row PasswordRecoveryRequest
		if err := rows.Scan(&row.ID, &row.UserID, &row.UserName, &row.Status, &row.SubmittedName, &row.SubmittedEmail, &row.Note, &row.ReviewerID, &row.ReviewerName, &row.ReviewNote, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func GetPasswordRecoveryRequest(db *sql.DB, id string) (*PasswordRecoveryRequest, error) {
	row := &PasswordRecoveryRequest{}
	err := QQueryRow(db,
		`SELECT r.id, r.user_id, u.name, r.status, r.submitted_name, r.submitted_email, r.note,
		        r.reviewer_id, COALESCE(rv.name,''), r.review_note, r.created_at, r.updated_at
		   FROM password_recovery_requests r
		   JOIN users u ON u.id = r.user_id
		   LEFT JOIN users rv ON rv.id = r.reviewer_id
		  WHERE r.id=?`,
		id,
	).Scan(&row.ID, &row.UserID, &row.UserName, &row.Status, &row.SubmittedName, &row.SubmittedEmail, &row.Note, &row.ReviewerID, &row.ReviewerName, &row.ReviewNote, &row.CreatedAt, &row.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func GetUserByPubkey(db *sql.DB, pubkey string) (*User, error) {
	var userID string
	err := QQueryRow(db, `SELECT user_id FROM auth_pubkeys WHERE pubkey=?`, pubkey).Scan(&userID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return GetUserByID(db, userID)
}

func CountUsers(db *sql.DB) (int, error) {
	var n int
	err := QQueryRow(db, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func GetUserProfileByName(db *sql.DB, name string) (*UserProfile, error) {
	p := &UserProfile{}
	var lastVisitDay string
	err := QQueryRow(db,
		`SELECT u.id, u.name, u.role, COALESCE(NULLIF(up.display_name,''), u.name),
		        COALESCE(up.title,''), COALESCE(up.bio,''), COALESCE(up.avatar,''), COALESCE(up.signature,''),
		        COALESCE(up.plan,''), COALESCE(up.homepage,''), u.created,
		        COALESCE(ua.posts_created,0), COALESCE(ua.reactions_recv,0), COALESCE(ua.trust_level,0),
		        COALESCE(ua.last_visit_day,'')
		 FROM users u
		 LEFT JOIN user_profiles up ON up.user_id = u.id
		 LEFT JOIN user_activity ua ON ua.user_id = u.id
		 WHERE u.name=?`,
		name,
	).Scan(&p.ID, &p.Name, &p.Role, &p.DisplayName, &p.Title, &p.Bio, &p.Avatar, &p.Signature,
		&p.Plan, &p.Homepage, &p.Created,
		&p.PostsCreated, &p.ReactionsReceived, &p.TrustLevel, &lastVisitDay)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastVisitDay != "" {
		if lastSeen, err := time.Parse("2006-01-02", lastVisitDay); err == nil {
			p.LastSeen = lastSeen.UnixMilli()
		}
	}
	pubkeys, err := ListPubkeyTitlesByUserName(db, p.Name)
	if err != nil {
		return nil, err
	}
	p.Pubkeys = pubkeys
	return p, err
}

func GetUserPrivateProfile(db *sql.DB, userID string) (*UserPrivateProfile, error) {
	p := &UserPrivateProfile{UserID: userID}
	err := QQueryRow(db,
		`SELECT user_id, real_name, real_email, registration_email, address, phone, mobile,
		        birthday, school, contact_note, updated_at
		   FROM user_private_profiles
		  WHERE user_id=?`,
		userID,
	).Scan(&p.UserID, &p.RealName, &p.RealEmail, &p.RegistrationEmail, &p.Address, &p.Phone, &p.Mobile,
		&p.Birthday, &p.School, &p.ContactNote, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return p, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func ListUserPersonalFiles(db *sql.DB, userID string, includePrivate bool) ([]UserPersonalFile, error) {
	rows, err := QQuery(db,
		`SELECT user_id, name, body, public, updated_at
		   FROM user_personal_files
		  WHERE user_id=? AND (?=1 OR public=1)
		  ORDER BY name`,
		userID, boolInt(includePrivate),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserPersonalFile{}
	for rows.Next() {
		var file UserPersonalFile
		var public int
		if err := rows.Scan(&file.UserID, &file.Name, &file.Body, &public, &file.UpdatedAt); err != nil {
			return nil, err
		}
		file.Public = public != 0
		out = append(out, file)
	}
	return out, rows.Err()
}

func GetUserPersonalFile(db *sql.DB, userID, name string, includePrivate bool) (*UserPersonalFile, error) {
	file := &UserPersonalFile{}
	var public int
	err := QQueryRow(db,
		`SELECT user_id, name, body, public, updated_at
		   FROM user_personal_files
		  WHERE user_id=? AND name=? AND (?=1 OR public=1)`,
		userID, strings.TrimSpace(name), boolInt(includePrivate),
	).Scan(&file.UserID, &file.Name, &file.Body, &public, &file.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	file.Public = public != 0
	return file, nil
}

func GetUserSignature(db *sql.DB, userID, id string) (*UserSignature, error) {
	sig := &UserSignature{}
	var active int
	err := QQueryRow(db,
		`SELECT id, user_id, label, body, position, active, created_at, updated_at
		   FROM user_signatures
		  WHERE user_id=? AND id=?`,
		userID, id,
	).Scan(&sig.ID, &sig.UserID, &sig.Label, &sig.Body, &sig.Position, &active, &sig.CreatedAt, &sig.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	sig.Active = active != 0
	return sig, nil
}

func ListUserSignatures(db *sql.DB, userID string) (*UserSignatureBundle, error) {
	bundle := &UserSignatureBundle{
		Settings: UserSignatureSettings{UserID: userID},
		MaxCount: MaxUserSignatures,
	}
	rows, err := QQuery(db,
		`SELECT id, user_id, label, body, position, active, created_at, updated_at
		   FROM user_signatures
		  WHERE user_id=?
		  ORDER BY position, updated_at, id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sig UserSignature
		var active int
		if err := rows.Scan(&sig.ID, &sig.UserID, &sig.Label, &sig.Body, &sig.Position, &active, &sig.CreatedAt, &sig.UpdatedAt); err != nil {
			return nil, err
		}
		sig.Active = active != 0
		bundle.Signatures = append(bundle.Signatures, sig)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var randomEnabled int
	err = QQueryRow(db,
		`SELECT user_id, selected_signature_id, random_enabled, updated_at
		   FROM user_signature_settings
		  WHERE user_id=?`,
		userID,
	).Scan(&bundle.Settings.UserID, &bundle.Settings.SelectedSignatureID, &randomEnabled, &bundle.Settings.UpdatedAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	bundle.Settings.RandomEnabled = randomEnabled != 0
	return bundle, nil
}

func GetUserLoginACLRule(db *sql.DB, userID, id string) (*UserLoginACLRule, error) {
	rule := &UserLoginACLRule{}
	var active int
	err := QQueryRow(db,
		`SELECT id, user_id, pattern, note, position, active, created_at, updated_at
		   FROM user_login_acl_rules
		  WHERE user_id=? AND id=?`,
		userID, id,
	).Scan(&rule.ID, &rule.UserID, &rule.Pattern, &rule.Note, &rule.Position, &active, &rule.CreatedAt, &rule.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	rule.Active = active != 0
	return rule, nil
}

func ListUserLoginACL(db *sql.DB, userID, host string) (*UserLoginACLBundle, error) {
	bundle := &UserLoginACLBundle{
		Settings: UserLoginACLSettings{UserID: userID},
		Host:     strings.TrimSpace(host),
		Allowed:  true,
	}
	rows, err := QQuery(db,
		`SELECT id, user_id, pattern, note, position, active, created_at, updated_at
		   FROM user_login_acl_rules
		  WHERE user_id=?
		  ORDER BY position, updated_at, id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rule UserLoginACLRule
		var active int
		if err := rows.Scan(&rule.ID, &rule.UserID, &rule.Pattern, &rule.Note, &rule.Position, &active, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		rule.Active = active != 0
		bundle.Rules = append(bundle.Rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var enabled int
	err = QQueryRow(db,
		`SELECT user_id, enabled, updated_at
		   FROM user_login_acl_settings
		  WHERE user_id=?`,
		userID,
	).Scan(&bundle.Settings.UserID, &enabled, &bundle.Settings.UpdatedAt)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	bundle.Settings.Enabled = enabled != 0
	bundle.Allowed = LoginACLAllows(bundle, host)
	return bundle, nil
}

func LoginACLAllows(bundle *UserLoginACLBundle, host string) bool {
	if bundle == nil || !bundle.Settings.Enabled {
		return true
	}
	host = normalizeLoginHost(host)
	if host == "" {
		return false
	}
	for _, rule := range bundle.Rules {
		if !rule.Active {
			continue
		}
		if loginACLPatternMatches(host, rule.Pattern) {
			return true
		}
	}
	return false
}

func normalizeLoginHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.String()
	}
	if addrPort, err := netip.ParseAddrPort(host); err == nil {
		return addrPort.Addr().String()
	}
	return strings.ToLower(host)
}

func loginACLPatternMatches(host, pattern string) bool {
	host = normalizeLoginHost(host)
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if host == "" || pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if prefix, err := netip.ParsePrefix(pattern); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return prefix.Contains(addr)
		}
	}
	if strings.Contains(pattern, "*") {
		parts := strings.Split(pattern, "*")
		if len(parts) == 2 && parts[1] == "" {
			return strings.HasPrefix(host, parts[0])
		}
		cursor := 0
		for i, part := range parts {
			if part == "" {
				continue
			}
			idx := strings.Index(host[cursor:], part)
			if idx < 0 {
				return false
			}
			if i == 0 && idx != 0 {
				return false
			}
			cursor += idx + len(part)
		}
		last := parts[len(parts)-1]
		return last == "" || strings.HasSuffix(host, last)
	}
	return host == normalizeLoginHost(pattern)
}

func ListPubkeyTitlesByUserName(db *sql.DB, username string) ([]string, error) {
	rows, err := QQuery(db,
		`SELECT pubkey FROM auth_pubkeys ap
		 JOIN users u ON u.id = ap.user_id
		 WHERE u.name = ?
		 ORDER BY pubkey`,
		username,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := make([]string, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		keys = append(keys, ExtractPubkeyTitle(raw))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}

func ExtractPubkeyTitle(raw string) string {
	parts := strings.Fields(raw)
	if len(parts) >= 3 {
		title := strings.Join(parts[2:], " ")
		if title != "" {
			return title
		}
	}
	if raw == "" {
		return "SSH key"
	}
	if len(parts) >= 1 {
		return parts[0] + " key"
	}
	return "SSH key"
}

func ListModerationReviews(db *sql.DB, status string, limit, offset int) ([]ModerationReview, error) {
	q := `SELECT id, kind, status, target_id, target_kind, reporter, reason, resolution, actor, created_at, updated_at
	      FROM moderation_reviews`
	var args []any
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := QQuery(db, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModerationReview
	for rows.Next() {
		var r ModerationReview
		if err := rows.Scan(&r.ID, &r.Kind, &r.Status, &r.TargetID, &r.TargetKind, &r.Reporter, &r.Reason, &r.Resolution, &r.Actor, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func ListContentFilters(db *sql.DB, scope string, includeInactive bool, limit, offset int) ([]ContentFilter, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	q := `SELECT id, pattern, scope, active, created_by, created_at, updated_at FROM content_filters`
	var args []any
	var wheres []string
	scope = strings.TrimSpace(scope)
	if scope != "" {
		wheres = append(wheres, `scope=?`)
		args = append(args, scope)
	}
	if !includeInactive {
		wheres = append(wheres, `active=1`)
	}
	if len(wheres) > 0 {
		q += ` WHERE ` + strings.Join(wheres, ` AND `)
	}
	q += ` ORDER BY updated_at DESC, id LIMIT ? OFFSET ?`
	args = append(args, limit, offset)

	rows, err := QQuery(db, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContentFilter
	for rows.Next() {
		filter, err := scanContentFilter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, filter)
	}
	return out, rows.Err()
}

func MatchContentFilter(db *sql.DB, boardID, text string) (*ContentFilter, error) {
	needle := strings.ToLower(strings.TrimSpace(text))
	if needle == "" {
		return nil, nil
	}
	rows, err := QQuery(db,
		`SELECT id, pattern, scope, active, created_by, created_at, updated_at
		   FROM content_filters
		  WHERE active=1 AND (scope='global' OR scope=?)
		  ORDER BY CASE WHEN scope=? THEN 0 ELSE 1 END, updated_at DESC, id`,
		boardID, boardID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		filter, err := scanContentFilter(rows)
		if err != nil {
			return nil, err
		}
		pattern := strings.ToLower(strings.TrimSpace(filter.Pattern))
		if pattern != "" && strings.Contains(needle, pattern) {
			return &filter, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

type contentFilterScanner interface {
	Scan(dest ...any) error
}

func scanContentFilter(row contentFilterScanner) (ContentFilter, error) {
	var f ContentFilter
	var active int
	err := row.Scan(&f.ID, &f.Pattern, &f.Scope, &active, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
	f.Active = active != 0
	return f, err
}

func ListUserSanctions(db *sql.DB, userID string, limit, offset int) ([]UserSanction, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	rows, err := QQuery(db,
		`SELECT id, user_id, kind, scope, expires_at, by, reason, seq
	       FROM user_sanctions
	      WHERE user_id = ?
	      ORDER BY seq DESC LIMIT ? OFFSET ?`,
		userID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UserSanction
	for rows.Next() {
		var s UserSanction
		if err := rows.Scan(&s.ID, &s.UserID, &s.Kind, &s.Scope, &s.ExpiresAt, &s.By, &s.Reason, &s.Seq); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func SearchPosts(db *sql.DB, query, boardID string, limit int) ([]Post, error) {
	var rows *sql.Rows
	var err error
	if boardID != "" {
		rows, err = QQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		 FROM posts_fts f
		 JOIN posts p ON p.id = f.post_id
		 WHERE f.board_id=? AND posts_fts MATCH ? AND p.redacted=0
		 ORDER BY rank LIMIT ?`,
			boardID, query, limit,
		)
	} else {
		rows, err = QQuery(db,
			`SELECT p.id, p.thread, p.author, COALESCE(p.author_id,''), p.body, COALESCE(p.signature,''), p.content_type,
		        COALESCE(p.reply_to,''), p.version, p.redacted,
		        COALESCE((SELECT COUNT(*) FROM post_reactions WHERE post_id=p.id), 0),
		        p.created_seq, p.updated_seq, p.created_at, p.updated_at
		 FROM posts_fts f
		 JOIN posts p ON p.id = f.post_id
		 WHERE posts_fts MATCH ? AND p.redacted=0
		 ORDER BY rank LIMIT ?`,
			query, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var posts []Post
	for rows.Next() {
		var p Post
		var redacted int
		if err := rows.Scan(&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType,
			&p.ReplyTo, &p.Version, &redacted, &p.ReactionCount, &p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		if p.CreatedAt == 0 {
			p.CreatedAt = p.CreatedSeq
		}
		if p.UpdatedAt == 0 {
			p.UpdatedAt = p.CreatedAt
		}
		p.Redacted = redacted != 0
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return attachPostAttachments(db, posts)
}

func ReactionCount(db *sql.DB, postID string) (int, error) {
	var n int
	err := QQueryRow(db, `SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func ReactionCountTx(tx *sql.Tx, postID string) (int, error) {
	var n int
	err := QQueryRow(tx, `SELECT COUNT(*) FROM post_reactions WHERE post_id=?`, postID).Scan(&n)
	return n, err
}

func UserReacted(db *sql.DB, postID, userID string) (bool, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM post_reactions WHERE post_id=? AND user_id=?`, postID, userID,
	).Scan(&n)
	return n > 0, err
}

func GetPollByPostID(db *sql.DB, postID string) (*Poll, error) {
	p := &Poll{}
	err := QQueryRow(db,
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE post_id=?`, postID,
	).Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

func GetPollWithVotes(db *sql.DB, pollID, viewerUserID string) (*Poll, error) {
	p := &Poll{}
	err := QQueryRow(db,
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE id=?`, pollID,
	).Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// Load options with counts.
	rows, err := QQuery(db,
		`SELECT po.id, po.text,
		        (SELECT COUNT(*) FROM poll_votes pv WHERE pv.option_id=po.id) AS cnt
		 FROM poll_options po WHERE po.poll_id=? ORDER BY po.position`, pollID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var opt PollOption
		if err := rows.Scan(&opt.ID, &opt.Text, &opt.VoteCount); err != nil {
			return nil, err
		}
		p.Options = append(p.Options, opt)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Check if viewer voted.
	if viewerUserID != "" {
		var votedOptionID string
		err := QQueryRow(db,
			`SELECT option_id FROM poll_votes WHERE poll_id=? AND user_id=?`, pollID, viewerUserID,
		).Scan(&votedOptionID)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		p.Voted = votedOptionID
	}
	return p, nil
}

func PollsForPosts(db *sql.DB, postIDs []string, viewerUserID string) (map[string]*Poll, error) {
	if len(postIDs) == 0 {
		return nil, nil
	}
	args := make([]interface{}, len(postIDs))
	for i, id := range postIDs {
		args[i] = id
	}
	placeholder := strings.Repeat("?,", len(postIDs))
	placeholder = placeholder[:len(placeholder)-1] // trim trailing comma
	rows, err := QQuery(db,
		`SELECT id, post_id, question, expires_at, ts FROM polls WHERE post_id IN (`+placeholder+`)`,
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	polls := map[string]*Poll{}
	for rows.Next() {
		p := &Poll{}
		if err := rows.Scan(&p.ID, &p.PostID, &p.Question, &p.ExpiresAt, &p.TS); err != nil {
			return nil, err
		}
		polls[p.PostID] = p
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Load options and votes for each poll found.
	for _, p := range polls {
		full, err := GetPollWithVotes(db, p.ID, viewerUserID)
		if err != nil {
			return nil, err
		}
		if full != nil {
			polls[p.PostID] = full
		}
	}
	return polls, nil
}

func ActiveSanction(db *sql.DB, userID, scope string) (string, bool) {
	now := NowMS()
	var kind string
	var err error
	if scope != "" {
		err = QQueryRow(db,
			`SELECT kind FROM user_sanctions
			 WHERE user_id=? AND (scope=? OR scope='global')
			   AND (expires_at=0 OR expires_at>?)
			 ORDER BY CASE kind WHEN 'ban' THEN 0 ELSE 1 END LIMIT 1`,
			userID, scope, now,
		).Scan(&kind)
	} else {
		err = QQueryRow(db,
			`SELECT kind FROM user_sanctions
			 WHERE user_id=? AND scope='global'
			   AND (expires_at=0 OR expires_at>?)
			 ORDER BY CASE kind WHEN 'ban' THEN 0 ELSE 1 END LIMIT 1`,
			userID, now,
		).Scan(&kind)
	}
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		return "", false
	}
	return kind, true
}

func ListLoginWatchers(db *sql.DB, targetUserID string) ([]string, error) {
	rows, err := QQuery(db,
		`SELECT r.user_id
		   FROM user_relationships r
		  WHERE r.target_user_id=?
		    AND r.kind='login_watch'
		    AND EXISTS (
		          SELECT 1 FROM user_relationships f
		           WHERE f.user_id=r.user_id
		             AND f.target_user_id=r.target_user_id
		             AND f.kind='friend'
		        )
		    AND NOT EXISTS (
		          SELECT 1 FROM user_relationships ig
		           WHERE ig.user_id=r.user_id
		             AND ig.target_user_id=r.target_user_id
		             AND ig.kind='ignore'
		        )
		    AND NOT EXISTS (
		          SELECT 1 FROM user_relationships tig
		           WHERE tig.user_id=r.target_user_id
		             AND tig.target_user_id=r.user_id
		             AND tig.kind='ignore'
		        )
		  ORDER BY r.updated_at, r.user_id`,
		targetUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		out = append(out, userID)
	}
	return out, rows.Err()
}

func ListNotifications(db *sql.DB, userID string, limit, offset int, unreadOnly bool) ([]Notification, error) {
	q := `SELECT id, kind, thread_id, post_id, actor, read, ts
	      FROM notifications WHERE user_id=?`
	if unreadOnly {
		q += ` AND read=0`
	}
	q += ` ORDER BY ts DESC LIMIT ? OFFSET ?`
	rows, err := QQuery(db, q, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		var n Notification
		var read int
		if err := rows.Scan(&n.ID, &n.Kind, &n.ThreadID, &n.PostID, &n.Actor, &read, &n.TS); err != nil {
			return nil, err
		}
		n.Read = read != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

func CountUnreadNotifications(db *sql.DB, userID string) (int, error) {
	var n int
	err := QQueryRow(db,
		`SELECT COUNT(*) FROM notifications WHERE user_id=? AND read=0`, userID,
	).Scan(&n)
	return n, err
}

func WatchersOfThread(db sqlLike, threadID, excludeUserID string) ([]string, error) {
	rows, err := QQuery(db,
		`SELECT user_id FROM thread_prefs WHERE thread_id=? AND level='watch' AND user_id!=?`,
		threadID, excludeUserID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func TrustInfo(db *sql.DB, userID string) (*TrustLevelInfo, error) {
	_ = EnsureActivity(db, userID)
	t := &TrustLevelInfo{}
	err := QQueryRow(db,
		`SELECT login_count, posts_created, days_visited, reactions_recv, total_online_seconds, trust_level
		 FROM user_activity WHERE user_id=?`, userID,
	).Scan(&t.LoginCount, &t.PostsCreated, &t.DaysVisited, &t.ReactionsRecv, &t.TotalOnlineSeconds, &t.TrustLevel)
	if err == sql.ErrNoRows {
		return t, nil
	}
	return t, err
}

func UserTrustLevel(db *sql.DB, userID string) (int, error) {
	var level int
	err := QQueryRow(db, `SELECT trust_level FROM user_activity WHERE user_id=?`, userID).Scan(&level)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return level, nil
}
