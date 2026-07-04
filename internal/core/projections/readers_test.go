package projections

import (
	"database/sql"
	"maps"
	"slices"
	"testing"

	_ "modernc.org/sqlite"
)

func openSQLiteTestDB(t *testing.T) *sql.DB {
	t.Helper()
	oldFlavor := currentSQLFlavor
	SetSQLFlavor(sqliteFlavor)
	t.Cleanup(func() { SetSQLFlavor(oldFlavor) })

	db, err := sql.Open("sqlite", ":memory:")
	requireNoError(t, "open sqlite", err)
	t.Cleanup(func() { db.Close() })
	return db
}

func execSQL(t *testing.T, db *sql.DB, stmt string, args ...any) {
	t.Helper()
	if _, err := db.Exec(stmt, args...); err != nil {
		t.Fatalf("exec %q: %v", stmt, err)
	}
}

func execSQLs(t *testing.T, db *sql.DB, stmts ...string) {
	t.Helper()
	for _, stmt := range stmts {
		execSQL(t, db, stmt)
	}
}

func requireResult[T comparable](t *testing.T, name string, got T, err error, want T) {
	t.Helper()
	if err != nil || got != want {
		t.Fatalf("%s = %v, %v; want %v, nil", name, got, err, want)
	}
}

func requireLookupResult[T comparable](t *testing.T, name string, got T, found bool, err error, want T, wantFound bool) {
	t.Helper()
	if err != nil || found != wantFound || got != want {
		t.Fatalf("%s = %#v, %v, %v; want %#v, %v, nil", name, got, found, err, want, wantFound)
	}
}

func requireNoError(t *testing.T, name string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func requireStringSlice(t *testing.T, name string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

func TestLookupReaders(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE boards (id TEXT, name TEXT)`,
		`CREATE TABLE categories (id TEXT, parent_id TEXT, position INTEGER)`,
		`CREATE TABLE threads (id TEXT, last_seq INTEGER)`,
		`CREATE TABLE posts (id TEXT)`,
		`CREATE TABLE polls (id TEXT, post_id TEXT)`,
		`CREATE TABLE board_moderators (board_id TEXT, user_id TEXT, position INTEGER)`,
		`CREATE TABLE board_moderator_terms (board_id TEXT, user_id TEXT, started_at INTEGER, ended_at INTEGER, removed_by TEXT, position INTEGER)`,
		`CREATE TABLE recommended_boards (board_id TEXT, position INTEGER)`,
		`CREATE TABLE board_members (
			board_id TEXT,
			user_id TEXT,
			position INTEGER,
			can_manage_members INTEGER,
			can_curate INTEGER,
			can_moderate_posts INTEGER,
			can_moderate_threads INTEGER,
			can_announce INTEGER,
			can_manage_polls INTEGER,
			can_set_board_settings INTEGER
		)`,
		`INSERT INTO boards (id, name) VALUES ('general', 'General')`,
		`INSERT INTO threads (id, last_seq) VALUES ('thread_1', 42)`,
		`INSERT INTO polls (id, post_id) VALUES ('poll_1', 'post_1')`,
		`INSERT INTO board_moderators (board_id, user_id, position) VALUES ('general', 'mod_1', 1), ('general', 'mod_2', 3)`,
		`INSERT INTO board_moderator_terms (board_id, user_id, started_at, ended_at, removed_by, position) VALUES ('general', 'mod_old', 100, 123, 'admin_1', 8)`,
		`INSERT INTO recommended_boards (board_id, position) VALUES ('general', 10), ('tech', 20)`,
		`INSERT INTO board_members (
			board_id, user_id, position, can_manage_members, can_curate, can_moderate_posts,
			can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings
		) VALUES ('general', 'member_1', 2, 0, 1, 0, 0, 0, 0, 0)`,
		`INSERT INTO board_members (
			board_id, user_id, position, can_manage_members, can_curate, can_moderate_posts,
			can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings
		) VALUES ('general', 'member_2', 5, 0, 0, 0, 0, 0, 0, 0)`,
		`INSERT INTO board_members (
			board_id, user_id, position, can_manage_members, can_curate, can_moderate_posts,
			can_moderate_threads, can_announce, can_manage_polls, can_set_board_settings
		) VALUES ('general', 'member_3', 6, 0, 0, 0, 1, 0, 0, 0)`,
		`INSERT INTO categories (id, parent_id, position) VALUES ('root', '', 0), ('general', 'root', 3), ('tech', 'root', 4)`,
		`INSERT INTO posts (id) VALUES ('post_1')`,
	)

	exists, err := BoardExists(db, "general")
	requireResult(t, "BoardExists", exists, err, true)
	exists, err = BoardExists(db, "missing")
	requireResult(t, "BoardExists missing", exists, err, false)
	exists, err = CategoryExists(db, "root")
	requireResult(t, "CategoryExists", exists, err, true)
	exists, err = CategoryExists(db, "missing")
	requireResult(t, "CategoryExists missing", exists, err, false)
	position, found, err := CategoryPosition(db, "general")
	requireLookupResult(t, "CategoryPosition", position, found, err, 3, true)
	position, found, err = CategoryPosition(db, "missing")
	requireLookupResult(t, "CategoryPosition missing", position, found, err, 0, false)
	position, err = NextCategoryPosition(db, "root")
	requireResult(t, "NextCategoryPosition", position, err, 5)
	position, err = NextCategoryPosition(db, "missing-parent")
	requireResult(t, "NextCategoryPosition empty", position, err, 0)
	name, found, err := BoardName(db, "general")
	requireLookupResult(t, "BoardName", name, found, err, "General", true)
	name, found, err = BoardName(db, "missing")
	requireLookupResult(t, "BoardName missing", name, found, err, "", false)

	exists, err = ThreadExists(db, "thread_1")
	requireResult(t, "ThreadExists", exists, err, true)
	exists, err = ThreadExists(db, "missing")
	requireResult(t, "ThreadExists missing", exists, err, false)
	exists, err = PostExists(db, "post_1")
	requireResult(t, "PostExists", exists, err, true)
	exists, err = PostExists(db, "missing")
	requireResult(t, "PostExists missing", exists, err, false)
	seq, found, err := ThreadLastSeq(db, "thread_1")
	requireLookupResult(t, "ThreadLastSeq", seq, found, err, int64(42), true)
	seq, found, err = ThreadLastSeq(db, "missing")
	requireLookupResult(t, "ThreadLastSeq missing", seq, found, err, int64(0), false)

	pollID, found, err := PollIDForPost(db, "post_1")
	requireLookupResult(t, "PollIDForPost", pollID, found, err, "poll_1", true)
	pollID, found, err = PollIDForPost(db, "missing")
	requireLookupResult(t, "PollIDForPost missing", pollID, found, err, "", false)

	exists, err = BoardModeratorExists(db, "general", "mod_1")
	requireResult(t, "BoardModeratorExists", exists, err, true)
	exists, err = BoardModeratorExists(db, "general", "member_1")
	requireResult(t, "BoardModeratorExists missing", exists, err, false)
	position, found, err = BoardModeratorPosition(db, "general", "mod_1")
	requireLookupResult(t, "BoardModeratorPosition", position, found, err, 1, true)
	position, found, err = BoardModeratorPosition(db, "general", "missing")
	requireLookupResult(t, "BoardModeratorPosition missing", position, found, err, 0, false)
	position, err = NextBoardModeratorPosition(db, "general")
	requireResult(t, "NextBoardModeratorPosition", position, err, 4)
	position, err = NextBoardModeratorPosition(db, "empty")
	requireResult(t, "NextBoardModeratorPosition empty", position, err, 0)
	requestedPosition, negativePosition := 7, -1
	for _, tc := range []struct {
		name      string
		userID    string
		moderator bool
		requested *int
		want      int
	}{
		{"requested", "mod_1", true, &requestedPosition, 7}, {"negative requested", "mod_1", true, &negativePosition, 0},
		{"existing appoint", "mod_1", true, nil, 1}, {"next appoint", "new_mod", true, nil, 4},
		{"active removal", "mod_2", false, nil, 3}, {"finalized removal", "mod_old", false, nil, 8},
	} {
		position, err = BoardModeratorEventPosition(db, "general", tc.userID, "admin_1", tc.moderator, tc.requested, 123)
		requireResult(t, "BoardModeratorEventPosition "+tc.name, position, err, tc.want)
	}
	position, found, err = RecommendedBoardPosition(db, "general")
	requireLookupResult(t, "RecommendedBoardPosition", position, found, err, 10, true)
	position, found, err = RecommendedBoardPosition(db, "missing")
	requireLookupResult(t, "RecommendedBoardPosition missing", position, found, err, 0, false)
	position, err = NextRecommendedBoardPosition(db)
	requireResult(t, "NextRecommendedBoardPosition", position, err, 30)
	exists, err = BoardMemberExists(db, "general", "member_1")
	requireResult(t, "BoardMemberExists", exists, err, true)
	exists, err = BoardMemberExists(db, "general", "missing")
	requireResult(t, "BoardMemberExists missing", exists, err, false)
	allowed, err := BoardMemberPermission(db, "general", "member_1", "can_curate")
	requireResult(t, "BoardMemberPermission can_curate", allowed, err, true)
	allowed, err = BoardMemberPermission(db, "general", "member_1", "can_announce")
	requireResult(t, "BoardMemberPermission can_announce", allowed, err, false)
	allowed, err = BoardMemberPermission(db, "general", "member_1", "not_a_permission")
	requireResult(t, "BoardMemberPermission invalid", allowed, err, false)
	allowed, err = BoardMemberHasDelegatedPermissions(db, "general", "member_1")
	requireResult(t, "BoardMemberHasDelegatedPermissions", allowed, err, true)
	allowed, err = BoardMemberHasDelegatedPermissions(db, "general", "member_2")
	requireResult(t, "BoardMemberHasDelegatedPermissions empty", allowed, err, false)
	if board, thread, err := BoardThreadModerationPermissions(db, "general", "mod_1"); err != nil || !board || !thread {
		t.Fatalf("BoardThreadModerationPermissions moderator = %v, %v, %v; want true, true, nil", board, thread, err)
	}
	if board, thread, err := BoardThreadModerationPermissions(db, "general", "member_3"); err != nil || board || !thread {
		t.Fatalf("BoardThreadModerationPermissions thread member = %v, %v, %v; want false, true, nil", board, thread, err)
	}
	if board, thread, err := BoardThreadModerationPermissions(db, "general", "member_2"); err != nil || board || thread {
		t.Fatalf("BoardThreadModerationPermissions plain member = %v, %v, %v; want false, false, nil", board, thread, err)
	}
	canAnnounce := true
	finalMember, err := BoardMemberFinalState(db, "general", "member_1", true, BoardMemberPatch{
		Title:       "  Curator  ",
		CanAnnounce: &canAnnounce,
	})
	requireNoError(t, "BoardMemberFinalState existing", err)
	if finalMember.Title != "Curator" || finalMember.Position != 2 || !finalMember.CanCurate || !finalMember.CanAnnounce {
		t.Fatalf("BoardMemberFinalState existing = %#v; want title, preserved position/curate, patched announce", finalMember)
	}
	newMember, err := BoardMemberFinalState(db, "general", "member_new", true, BoardMemberPatch{})
	requireNoError(t, "BoardMemberFinalState new", err)
	if newMember.UserID != "member_new" || newMember.Position != 7 {
		t.Fatalf("BoardMemberFinalState new = %#v; want next position 7", newMember)
	}
	inactiveMember, err := BoardMemberFinalState(db, "general", "member_1", false, BoardMemberPatch{})
	requireNoError(t, "BoardMemberFinalState inactive", err)
	if inactiveMember.UserID != "member_1" || inactiveMember.Position != 0 || inactiveMember.CanCurate {
		t.Fatalf("BoardMemberFinalState inactive = %#v; want only user id", inactiveMember)
	}
}

func TestBoardMembershipAdmissionStatsAndCheck(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE board_members (board_id TEXT, user_id TEXT)`,
		`CREATE TABLE user_activity (user_id TEXT, login_count INTEGER, trust_level INTEGER)`,
		`CREATE TABLE threads (id TEXT, board TEXT, author_id TEXT)`,
		`CREATE TABLE posts (id TEXT, thread TEXT, author_id TEXT, redacted INTEGER)`,
		`CREATE TABLE digest_entries (board_id TEXT, target_kind TEXT, target_id TEXT)`,
		`INSERT INTO board_members (board_id, user_id) VALUES ('general', 'u1'), ('general', 'u2')`,
		`INSERT INTO user_activity (user_id, login_count, trust_level) VALUES ('u1', 3, 2)`,
		`INSERT INTO threads (id, board, author_id) VALUES ('th1', 'general', 'u1'), ('th2', 'general', 'u2')`,
		`INSERT INTO posts (id, thread, author_id, redacted) VALUES ('p1', 'th1', 'u1', 0), ('p2', 'th2', 'u1', 0), ('p3', 'th1', 'u1', 1)`,
		`INSERT INTO digest_entries (board_id, target_kind, target_id) VALUES ('general', 'post', 'p1'), ('general', 'thread', 'th1')`,
	)

	requirements := &BoardMemberRequirements{
		MinLoginCount:             2,
		MinPostCount:              2,
		MinTrustLevel:             2,
		MinScore:                  4,
		MinBoardPostCount:         2,
		MinBoardOriginalPostCount: 1,
		MinBoardDigestCount:       2,
		MinBoardMarkCount:         3,
		MaxMembers:                2,
	}
	stats, err := BoardMembershipAdmissionStatsForUser(db, "general", "u1", requirements)
	requireNoError(t, "BoardMembershipAdmissionStatsForUser", err)
	stats.ReactionScore = 4
	stats.BoardMarkCount = 3
	if failure := CheckBoardMembershipAdmission(requirements, stats); failure != nil {
		t.Fatalf("CheckBoardMembershipAdmission = %#v; want nil", failure)
	}

	stats, err = BoardMembershipAdmissionStatsForUser(db, "general", "u3", &BoardMemberRequirements{MaxMembers: 2})
	requireNoError(t, "BoardMembershipAdmissionStatsForUser full", err)
	failure := CheckBoardMembershipAdmission(&BoardMemberRequirements{MaxMembers: 2}, stats)
	if failure == nil || failure.Code != "conflict" || failure.Message != "board membership is full" {
		t.Fatalf("full-board failure = %#v; want conflict/full", failure)
	}

	failure = CheckBoardMembershipAdmission(&BoardMemberRequirements{MinScore: 5}, BoardMembershipAdmissionStats{ReactionScore: 4})
	if failure == nil || failure.Code != "forbidden" || failure.Message != "minimum score is 5" {
		t.Fatalf("score failure = %#v; want forbidden/minimum score", failure)
	}

	counter := testReactionCounter{counts: map[string]int{"p1": 2, "p2": 3, "p3": 100}}
	score, err := UserReactionScore(db, counter, "u1")
	requireResult(t, "UserReactionScore", score, err, 5)
	boardMarks, err := UserBoardMarkCount(db, counter, "general", "u1")
	requireResult(t, "UserBoardMarkCount", boardMarks, err, 5)
}

type testReactionCounter struct {
	counts map[string]int
}

func (c testReactionCounter) ReactionCount(postID string) (int, error) {
	return c.counts[postID], nil
}

func TestBoardJunkPostIDs(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQL(t, db, `CREATE TABLE post_deletions (post_id TEXT, thread_id TEXT, board_id TEXT, kind TEXT, deleted_at INTEGER, seq INTEGER)`)
	for _, item := range []struct {
		postID    string
		threadID  string
		boardID   string
		kind      string
		deletedAt int64
		seq       int64
	}{
		{postID: "old-junk", threadID: "thread-old", boardID: "general", kind: "junk", deletedAt: 10, seq: 2},
		{postID: "new-junk", threadID: "thread-new", boardID: "general", kind: "junk", deletedAt: 20, seq: 1},
		{postID: "same-time-newer-seq", threadID: "thread-same", boardID: "general", kind: "junk", deletedAt: 20, seq: 3},
		{postID: "recycled", threadID: "thread-recycle", boardID: "general", kind: "recycle", deletedAt: 30, seq: 1},
		{postID: "other-board", threadID: "thread-other", boardID: "tech", kind: "junk", deletedAt: 40, seq: 1},
	} {
		execSQL(t, db, `INSERT INTO post_deletions (post_id, thread_id, board_id, kind, deleted_at, seq) VALUES (?, ?, ?, ?, ?, ?)`,
			item.postID, item.threadID, item.boardID, item.kind, item.deletedAt, item.seq)
	}

	requested, msg, err := BoardJunkPostIDs(db, "general", []string{" post-1 ", "post-1", "post-2"})
	if err != nil || msg != "" {
		t.Fatalf("BoardJunkPostIDs requested err = %v, msg = %q", err, msg)
	}
	requireStringSlice(t, "BoardJunkPostIDs requested", requested, []string{"post-1", "post-2"})

	if _, msg, err := BoardJunkPostIDs(db, "general", []string{" "}); err != nil || msg == "" {
		t.Fatalf("BoardJunkPostIDs invalid requested err = %v, msg = %q, want validation msg", err, msg)
	}

	listed, msg, err := BoardJunkPostIDs(db, "general", nil)
	if err != nil || msg != "" {
		t.Fatalf("BoardJunkPostIDs listed err = %v, msg = %q", err, msg)
	}
	requireStringSlice(t, "BoardJunkPostIDs listed", listed, []string{"same-time-newer-seq", "new-junk", "old-junk"})

	threadID, ok, err := BoardJunkPostThreadID(db, "new-junk", "general")
	requireLookupResult(t, "BoardJunkPostThreadID", threadID, ok, err, "thread-new", true)
	threadID, ok, err = BoardJunkPostThreadID(db, "recycled", "general")
	requireLookupResult(t, "BoardJunkPostThreadID recycled", threadID, ok, err, "", false)
}

func TestFavoriteFolderReaders(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQL(t, db, `CREATE TABLE favorite_folders (id TEXT, user_id TEXT, parent_id TEXT, name TEXT, position INTEGER)`)
	for _, folder := range []struct {
		id       string
		userID   string
		parentID string
		name     string
		position int
	}{
		{id: "root", userID: "alice", parentID: "", name: "Root", position: 2},
		{id: "sibling", userID: "alice", parentID: "", name: "Sibling", position: 4},
		{id: "child", userID: "alice", parentID: "root", name: "Child", position: 0},
		{id: "grandchild", userID: "alice", parentID: "child", name: "Grandchild", position: 1},
		{id: "bob-root", userID: "bob", parentID: "", name: "Bob", position: 0},
	} {
		execSQL(t, db, `INSERT INTO favorite_folders (id, user_id, parent_id, name, position) VALUES (?, ?, ?, ?, ?)`,
			folder.id, folder.userID, folder.parentID, folder.name, folder.position)
	}

	exists, err := FavoriteFolderExists(db, "alice", " ")
	requireResult(t, "FavoriteFolderExists blank", exists, err, true)
	exists, err = FavoriteFolderExists(db, "alice", "root")
	requireResult(t, "FavoriteFolderExists root", exists, err, true)
	exists, err = FavoriteFolderExists(db, "alice", "bob-root")
	requireResult(t, "FavoriteFolderExists other user", exists, err, false)

	state, found, err := FavoriteFolderStateForUser(db, "alice", "child")
	requireLookupResult(t, "FavoriteFolderStateForUser", state, found, err, FavoriteFolderState{ParentID: "root", Name: "Child"}, true)
	state, found, err = FavoriteFolderStateForUser(db, "alice", "missing")
	requireLookupResult(t, "FavoriteFolderStateForUser missing", state, found, err, FavoriteFolderState{}, false)

	contains, err := FavoriteFolderContains(db, "alice", "root", "grandchild")
	requireResult(t, "FavoriteFolderContains root/grandchild", contains, err, true)
	contains, err = FavoriteFolderContains(db, "alice", "grandchild", "root")
	requireResult(t, "FavoriteFolderContains grandchild/root", contains, err, false)
	contains, err = FavoriteFolderContains(db, "alice", "root", "missing")
	requireResult(t, "FavoriteFolderContains missing", contains, err, false)

	position, err := FavoriteFolderTargetPosition(db, "alice", "", nil)
	requireResult(t, "FavoriteFolderTargetPosition root", position, err, 5)
	position, err = FavoriteFolderTargetPosition(db, "alice", "root", nil)
	requireResult(t, "FavoriteFolderTargetPosition child", position, err, 1)
	negative := -4
	position, err = FavoriteFolderTargetPosition(db, "alice", "", &negative)
	requireResult(t, "FavoriteFolderTargetPosition negative", position, err, 0)
	requested := 8
	position, err = FavoriteFolderTargetPosition(db, "alice", "", &requested)
	requireResult(t, "FavoriteFolderTargetPosition requested", position, err, 8)
}

func TestThreadRootReaders(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE posts (
			id TEXT, thread TEXT, created_seq INTEGER, no_reply INTEGER, mail_back INTEGER,
			author TEXT DEFAULT '', author_id TEXT DEFAULT '', body TEXT DEFAULT '', signature TEXT DEFAULT '',
			content_type TEXT DEFAULT 'markup', reply_to TEXT DEFAULT '', version INTEGER DEFAULT 0,
			redacted INTEGER DEFAULT 0, updated_seq INTEGER DEFAULT 0, created_at INTEGER DEFAULT 0,
			updated_at INTEGER DEFAULT 0, marked INTEGER DEFAULT 0, recommended INTEGER DEFAULT 0,
			tex INTEGER DEFAULT 0, source_post TEXT DEFAULT '', source_thread TEXT DEFAULT '',
			source_board TEXT DEFAULT '', source_author TEXT DEFAULT '', source_author_id TEXT DEFAULT '',
			source_title TEXT DEFAULT ''
		)`,
		`CREATE TABLE post_reaction_count_shards (post_id TEXT, count_value INTEGER)`,
		`CREATE TABLE post_reactions (post_id TEXT, user_id TEXT)`,
		`CREATE TABLE post_attachments (id TEXT, post_id TEXT, filename TEXT, content_type TEXT, size_bytes INTEGER, url TEXT, created_by TEXT, created_at INTEGER)`,
		`CREATE TABLE attachment_blobs (attachment_id TEXT)`,
	)
	for _, post := range []struct {
		id         string
		threadID   string
		createdSeq int64
		noReply    int
		mailBack   int
	}{
		{id: "reply", threadID: "thread_1", createdSeq: 20, noReply: 0, mailBack: 0},
		{id: "root", threadID: "thread_1", createdSeq: 10, noReply: 1, mailBack: 1},
		{id: "other-root", threadID: "thread_2", createdSeq: 5, noReply: 0, mailBack: 1},
	} {
		execSQL(t, db, `INSERT INTO posts (id, thread, created_seq, no_reply, mail_back) VALUES (?, ?, ?, ?, ?)`,
			post.id, post.threadID, post.createdSeq, post.noReply, post.mailBack)
	}

	guards, err := ThreadRootReplyGuardsForThread(db, "thread_1")
	requireResult(t, "ThreadRootReplyGuardsForThread", guards, err, ThreadRootReplyGuards{NoReply: true, MailBack: true})
	postID, found, err := ThreadRootPostID(db, "thread_1")
	requireLookupResult(t, "ThreadRootPostID", postID, found, err, "root", true)
	rootPost, err := ThreadRootPost(db, "thread_1")
	requireNoError(t, "ThreadRootPost", err)
	if rootPost == nil || rootPost.ID != "root" || !rootPost.NoReply || !rootPost.MailBack {
		t.Fatalf("ThreadRootPost = %#v, want root with no-reply and mail-back", rootPost)
	}
	guards, err = ThreadRootReplyGuardsForThread(db, "missing")
	requireResult(t, "ThreadRootReplyGuardsForThread missing", guards, err, ThreadRootReplyGuards{})
	postID, found, err = ThreadRootPostID(db, "missing")
	requireLookupResult(t, "ThreadRootPostID missing", postID, found, err, "", false)
	rootPost, err = ThreadRootPost(db, "missing")
	if err != nil || rootPost != nil {
		t.Fatalf("ThreadRootPost missing = %#v, %v; want nil, nil", rootPost, err)
	}
}

func TestPromotedAttachmentBlobMatches(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE attachment_blobs (attachment_id TEXT, content_type TEXT, size_bytes INTEGER)`,
		`CREATE TABLE mail_attachment_blobs (attachment_id TEXT, content_type TEXT, size_bytes INTEGER)`,
		`CREATE TABLE post_attachments (post_id TEXT)`,
		`CREATE TABLE mail_attachments (message_id TEXT)`,
		`CREATE TABLE attachment_blob_staging (id TEXT, kind TEXT, size_bytes INTEGER)`,
	)
	execSQL(t, db, `INSERT INTO attachment_blobs (attachment_id, content_type, size_bytes) VALUES (?, ?, ?)`, "att_1", "text/plain", int64(12))
	execSQL(t, db, `INSERT INTO mail_attachment_blobs (attachment_id, content_type, size_bytes) VALUES (?, ?, ?)`, "matt_1", "image/png", int64(20))
	for _, postID := range []string{"post_1", "post_1", "post_2"} {
		execSQL(t, db, `INSERT INTO post_attachments (post_id) VALUES (?)`, postID)
	}
	for _, mailID := range []string{"mail_1", "mail_1", "mail_1", "mail_2"} {
		execSQL(t, db, `INSERT INTO mail_attachments (message_id) VALUES (?)`, mailID)
	}
	execSQL(t, db, `INSERT INTO attachment_blob_staging (id, kind, size_bytes) VALUES (?, ?, ?)`, "stage_1", StagedBlobPostAttachment, int64(33))

	ok, err := PromotedAttachmentBlobMatches(db, StagedBlobPostAttachment, "att_1", 12, " text/plain ")
	requireResult(t, "PromotedAttachmentBlobMatches post", ok, err, true)
	ok, err = PromotedAttachmentBlobMatches(db, StagedBlobPostAttachment, "att_1", -1, "")
	requireResult(t, "PromotedAttachmentBlobMatches wildcard", ok, err, true)
	ok, err = PromotedAttachmentBlobMatches(db, StagedBlobPostAttachment, "att_1", 13, "text/plain")
	requireResult(t, "PromotedAttachmentBlobMatches size mismatch", ok, err, false)
	ok, err = PromotedAttachmentBlobMatches(db, StagedBlobMailAttachment, "matt_1", 20, "text/plain")
	requireResult(t, "PromotedAttachmentBlobMatches content mismatch", ok, err, false)
	ok, err = PromotedAttachmentBlobMatches(db, StagedBlobMailAttachment, "missing", 20, "image/png")
	requireResult(t, "PromotedAttachmentBlobMatches missing", ok, err, false)
	if ok, err := PromotedAttachmentBlobMatches(db, "avatar", "att_1", 12, "text/plain"); err == nil || ok {
		t.Fatalf("PromotedAttachmentBlobMatches unknown = %v, %v; want false, error", ok, err)
	}
	count, err := PostAttachmentCount(db, "post_1")
	requireResult(t, "PostAttachmentCount", count, err, 2)
	count, err = MailAttachmentCount(db, "mail_1")
	requireResult(t, "MailAttachmentCount", count, err, 3)
	info, found, err := GetStagedAttachmentBlobInfo(db, " stage_1 ")
	requireLookupResult(t, "GetStagedAttachmentBlobInfo", info, found, err, StagedAttachmentBlobInfo{Kind: StagedBlobPostAttachment, SizeBytes: 33}, true)
	info, found, err = GetStagedAttachmentBlobInfo(db, "missing")
	requireLookupResult(t, "GetStagedAttachmentBlobInfo missing", info, found, err, StagedAttachmentBlobInfo{}, false)
}

func TestUserRelationshipAndBlessingExists(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE user_relationships (user_id TEXT, target_user_id TEXT, kind TEXT)`,
		`CREATE TABLE blessings (from_user_id TEXT, to_user_id TEXT)`,
		`CREATE TABLE direct_message_settings (user_id TEXT, policy TEXT)`,
	)
	execSQL(t, db, `INSERT INTO user_relationships (user_id, target_user_id, kind) VALUES (?, ?, ?)`, "alice", "bob", "friend")
	execSQL(t, db, `INSERT INTO blessings (from_user_id, to_user_id) VALUES (?, ?)`, "alice", "carol")
	for _, item := range []struct {
		userID string
		policy string
	}{
		{userID: "alice", policy: "friends"},
		{userID: "carol", policy: "none"},
		{userID: "dana", policy: "surprise"},
	} {
		execSQL(t, db, `INSERT INTO direct_message_settings (user_id, policy) VALUES (?, ?)`, item.userID, item.policy)
	}

	ok, err := UserRelationshipExists(db, "alice", "bob", "friend")
	requireResult(t, "UserRelationshipExists friend", ok, err, true)
	ok, err = UserRelationshipExists(db, "alice", "bob", "ignore")
	requireResult(t, "UserRelationshipExists wrong kind", ok, err, false)
	ok, err = BlessingExists(db, "alice", "carol")
	requireResult(t, "BlessingExists", ok, err, true)
	ok, err = BlessingExists(db, "carol", "alice")
	requireResult(t, "BlessingExists reversed", ok, err, false)
	ok, err = DirectMessageAllowed(db, "missing", "bob")
	requireResult(t, "DirectMessageAllowed default", ok, err, true)
	ok, err = DirectMessageAllowed(db, "carol", "bob")
	requireResult(t, "DirectMessageAllowed none", ok, err, false)
	ok, err = DirectMessageAllowed(db, "alice", "bob")
	requireResult(t, "DirectMessageAllowed friend", ok, err, true)
	ok, err = DirectMessageAllowed(db, "alice", "carol")
	requireResult(t, "DirectMessageAllowed non-friend", ok, err, false)
	ok, err = DirectMessageAllowed(db, "dana", "bob")
	requireResult(t, "DirectMessageAllowed unknown policy", ok, err, true)
}

func TestDirectMessageTargetReaders(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQL(t, db, `CREATE TABLE direct_messages (id TEXT, from_user_id TEXT, to_user_id TEXT, read_at INTEGER, sender_deleted INTEGER, recipient_deleted INTEGER)`)
	execSQL(t, db, `INSERT INTO direct_messages (id, from_user_id, to_user_id, read_at, sender_deleted, recipient_deleted) VALUES (?, ?, ?, ?, ?, ?)`,
		"dm_1", "alice", "bob", int64(42), 0, 1)

	state, found, err := DirectMessageTarget(db, "dm_1")
	requireLookupResult(t, "DirectMessageTarget", state, found, err, DirectMessageState{FromUserID: "alice", ToUserID: "bob", ReadAt: 42, RecipientDeleted: true}, true)
	state, found, err = DirectMessageTarget(db, "missing")
	requireLookupResult(t, "DirectMessageTarget missing", state, found, err, DirectMessageState{}, false)
}

func TestFindUserRef(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQL(t, db, `CREATE TABLE users (id TEXT, name TEXT, role TEXT, password TEXT, created INTEGER)`)
	for _, user := range []User{
		{ID: "usr_alice", Name: "alice", Role: "user", Password: "pw1", Created: 10},
		{ID: "alice", Name: "other", Role: "mod", Password: "pw2", Created: 20},
		{ID: "usr_bob", Name: "bob", Role: "user", Password: "pw3", Created: 30},
		{ID: "usr_owner", Name: "owner", Role: "user", Password: "pw4", Created: 40},
	} {
		execSQL(t, db, `INSERT INTO users (id, name, role, password, created) VALUES (?, ?, ?, ?, ?)`,
			user.ID, user.Name, user.Role, user.Password, user.Created)
	}

	if user, err := FindUserRef(db, " "); err != nil || user != nil {
		t.Fatalf("FindUserRef blank = %#v, %v; want nil, nil", user, err)
	}
	byName, err := FindUserRef(db, " usr_alice ")
	requireNoError(t, "FindUserRef by id", err)
	if byName == nil || byName.ID != "usr_alice" || byName.Name != "alice" {
		t.Fatalf("FindUserRef by id = %#v, want usr_alice", byName)
	}
	preferredID, err := FindUserRef(db, "alice")
	requireNoError(t, "FindUserRef id/name collision", err)
	if preferredID == nil || preferredID.ID != "alice" || preferredID.Name != "other" {
		t.Fatalf("FindUserRef id/name collision = %#v, want id match", preferredID)
	}
	if missing, err := FindUserRef(db, "missing"); err != nil || missing != nil {
		t.Fatalf("FindUserRef missing = %#v, %v; want nil, nil", missing, err)
	}
	memberIDs, missingRef, includesOwner, err := ResolveMailGroupMemberIDs(db, []string{"bob", "usr_bob", " alice "}, "usr_owner")
	if err != nil || missingRef != "" || includesOwner {
		t.Fatalf("ResolveMailGroupMemberIDs err = %v, missingRef = %q, includesOwner = %v", err, missingRef, includesOwner)
	}
	requireStringSlice(t, "ResolveMailGroupMemberIDs", memberIDs, []string{"usr_bob", "alice"})
	if _, missingRef, includesOwner, err := ResolveMailGroupMemberIDs(db, []string{" missing "}, "usr_owner"); err != nil || missingRef != "missing" || includesOwner {
		t.Fatalf("ResolveMailGroupMemberIDs missing = %q, %v, %v; want missing, false, nil", missingRef, includesOwner, err)
	}
	if _, missingRef, includesOwner, err := ResolveMailGroupMemberIDs(db, []string{"owner"}, "usr_owner"); err != nil || missingRef != "" || !includesOwner {
		t.Fatalf("ResolveMailGroupMemberIDs owner = %q, %v, %v; want empty, true, nil", missingRef, includesOwner, err)
	}
	allRecipients, err := ListMailAllRecipientIDs(db, "usr_owner")
	requireNoError(t, "ListMailAllRecipientIDs", err)
	requireStringSlice(t, "ListMailAllRecipientIDs", allRecipients, []string{"usr_alice", "usr_bob", "alice"})
}

func TestCurrentPostSignature(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE user_signature_settings (user_id TEXT, selected_signature_id TEXT, random_enabled INTEGER)`,
		`CREATE TABLE user_signatures (id TEXT, user_id TEXT, body TEXT, position INTEGER, active INTEGER, updated_at INTEGER)`,
		`CREATE TABLE user_profiles (user_id TEXT, signature TEXT)`,
	)
	execSQL(t, db, `INSERT INTO user_profiles (user_id, signature) VALUES (?, ?)`, "profile_user", " profile signature ")
	execSQL(t, db, `INSERT INTO user_profiles (user_id, signature) VALUES (?, ?)`, "selected_user", " profile fallback ")
	execSQL(t, db, `INSERT INTO user_signature_settings (user_id, selected_signature_id, random_enabled) VALUES (?, ?, ?)`, "selected_user", "sig_selected", 0)
	execSQL(t, db, `INSERT INTO user_signatures (id, user_id, body, position, active, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "sig_bank", "selected_user", " bank signature ", 0, 1, 20)
	execSQL(t, db, `INSERT INTO user_signatures (id, user_id, body, position, active, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "sig_selected", "selected_user", " selected signature ", 10, 1, 10)
	execSQL(t, db, `INSERT INTO user_profiles (user_id, signature) VALUES (?, ?)`, "bank_user", " profile fallback ")
	execSQL(t, db, `INSERT INTO user_signature_settings (user_id, selected_signature_id, random_enabled) VALUES (?, ?, ?)`, "bank_user", "sig_blank", 0)
	execSQL(t, db, `INSERT INTO user_signatures (id, user_id, body, position, active, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "sig_blank", "bank_user", "   ", 0, 1, 10)
	execSQL(t, db, `INSERT INTO user_signatures (id, user_id, body, position, active, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "sig_bank_only", "bank_user", " bank only ", 1, 1, 20)
	execSQL(t, db, `INSERT INTO user_signature_settings (user_id, selected_signature_id, random_enabled) VALUES (?, ?, ?)`, "random_user", "", 1)
	execSQL(t, db, `INSERT INTO user_signatures (id, user_id, body, position, active, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "sig_random_a", "random_user", " random a ", 0, 1, 10)
	execSQL(t, db, `INSERT INTO user_signatures (id, user_id, body, position, active, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, "sig_random_b", "random_user", " random b ", 1, 1, 20)

	signature, err := CurrentPostSignature(db, " ", nil)
	requireResult(t, "CurrentPostSignature blank", signature, err, "")
	signature, err = CurrentPostSignature(db, "profile_user", nil)
	requireResult(t, "CurrentPostSignature profile fallback", signature, err, "profile signature")
	signature, err = CurrentPostSignature(db, "selected_user", nil)
	requireResult(t, "CurrentPostSignature selected", signature, err, "selected signature")
	signature, err = CurrentPostSignature(db, "bank_user", nil)
	requireResult(t, "CurrentPostSignature bank fallback", signature, err, "bank only")
	signature, err = CurrentPostSignature(db, "random_user", func(int) int { return 3 })
	requireResult(t, "CurrentPostSignature random offset", signature, err, "random b")
	signature, err = CurrentPostSignature(db, "missing", nil)
	requireResult(t, "CurrentPostSignature missing", signature, err, "")
}

func TestMailGroupIDReaders(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE mail_groups (id TEXT, user_id TEXT, name TEXT)`,
		`CREATE TABLE mail_group_deletions (event_id TEXT, group_id TEXT)`,
	)
	for _, group := range []struct {
		id     string
		userID string
		name   string
	}{
		{id: "grp_friends", userID: "alice", name: "friends"},
		{id: "friends", userID: "alice", name: "other"},
		{id: "grp_bob", userID: "bob", name: "friends"},
	} {
		execSQL(t, db, `INSERT INTO mail_groups (id, user_id, name) VALUES (?, ?, ?)`, group.id, group.userID, group.name)
	}
	execSQL(t, db, `INSERT INTO mail_group_deletions (event_id, group_id) VALUES (?, ?)`, "evt_delete", "grp_friends")

	id, err := GetMailGroupID(db, "alice", "grp_friends")
	requireResult(t, "GetMailGroupID by id", id, err, "grp_friends")
	id, err = GetMailGroupID(db, "alice", "other")
	requireResult(t, "GetMailGroupID by name", id, err, "friends")
	id, err = GetMailGroupID(db, "alice", "missing")
	requireResult(t, "GetMailGroupID missing", id, err, "")
	id, err = MailGroupIDByName(db, "alice", " friends ")
	requireResult(t, "MailGroupIDByName", id, err, "grp_friends")
	id, err = MailGroupIDByName(db, "bob", "friends")
	requireResult(t, "MailGroupIDByName other owner", id, err, "grp_bob")
	id, found, err := MailGroupDeletion(db, "evt_delete")
	requireLookupResult(t, "MailGroupDeletion", id, found, err, "grp_friends", true)
	id, found, err = MailGroupDeletion(db, "evt_missing")
	requireLookupResult(t, "MailGroupDeletion missing", id, found, err, "", false)
}

func TestMailCopyReaders(t *testing.T) {
	db := openSQLiteTestDB(t)

	execSQLs(t, db,
		`CREATE TABLE mail_messages (id TEXT, from_user_id TEXT, subject TEXT, body TEXT)`,
		`CREATE TABLE mail_copies (message_id TEXT, user_id TEXT, role TEXT, mailbox TEXT)`,
		`CREATE TABLE mail_attachments (message_id TEXT, size_bytes INTEGER)`,
	)
	execSQL(t, db, `INSERT INTO mail_messages (id, from_user_id, subject, body) VALUES (?, ?, ?, ?)`,
		"mail-1", "alice", "hello", "body!")
	execSQL(t, db, `INSERT INTO mail_attachments (message_id, size_bytes) VALUES (?, ?)`, "mail-1", 7)
	for _, item := range []struct {
		userID  string
		role    string
		mailbox string
	}{
		{userID: "alice", role: "sender", mailbox: "sent"},
		{userID: "bob", role: "recipient", mailbox: "inbox"},
		{userID: "bob", role: "cc", mailbox: "inbox"},
		{userID: "carol", role: "recipient", mailbox: "trash"},
	} {
		execSQL(t, db, `INSERT INTO mail_copies (message_id, user_id, role, mailbox) VALUES (?, ?, ?, ?)`,
			"mail-1", item.userID, item.role, item.mailbox)
	}

	ok, err := UserHasMailCopy(db, "bob", "mail-1")
	requireResult(t, "UserHasMailCopy bob", ok, err, true)
	ok, err = UserHasMailCopy(db, "dana", "mail-1")
	requireResult(t, "UserHasMailCopy dana", ok, err, false)
	target, found, err := GetMailCopyUpdateTarget(db, "bob", "mail-1")
	requireLookupResult(t, "GetMailCopyUpdateTarget bob", target, found, err, MailCopyUpdateTarget{FromUserID: "alice", TrashedCopies: 0}, true)
	target, found, err = GetMailCopyUpdateTarget(db, "carol", "mail-1")
	requireLookupResult(t, "GetMailCopyUpdateTarget carol", target, found, err, MailCopyUpdateTarget{FromUserID: "alice", TrashedCopies: 1}, true)
	target, found, err = GetMailCopyUpdateTarget(db, "dana", "mail-1")
	requireLookupResult(t, "GetMailCopyUpdateTarget missing", target, found, err, MailCopyUpdateTarget{}, false)
	senderID, ok, err := MailSenderID(db, "mail-1")
	requireLookupResult(t, "MailSenderID", senderID, ok, err, "alice", true)
	senderID, ok, err = MailSenderID(db, "missing")
	requireLookupResult(t, "MailSenderID missing", senderID, ok, err, "", false)
	storedSize, err := MailStoredSize(db, "mail-1")
	requireNoError(t, "MailStoredSize", err)
	const mailSize = int64(len("hello") + len("body!") + 7)
	if storedSize != mailSize {
		t.Fatalf("MailStoredSize = %d, want %d", storedSize, mailSize)
	}
	storedSize, err = MailStoredSize(db, "missing")
	requireResult(t, "MailStoredSize missing", storedSize, err, int64(0))

	counts, err := ActiveMailCopyCounts(db, "mail-1")
	requireNoError(t, "ActiveMailCopyCounts", err)
	if want := map[string]int{"alice": 1, "bob": 2}; !maps.Equal(counts, want) {
		t.Fatalf("ActiveMailCopyCounts = %#v, want %#v", counts, want)
	}

	scopes, err := MailAccountScopes(db, "mail-1", "alice")
	requireNoError(t, "MailAccountScopes", err)
	if len(scopes) != 3 || scopes[0] != "account:alice" {
		t.Fatalf("MailAccountScopes = %#v, want actor first plus bob/carol", scopes)
	}
	for _, scope := range []string{"account:alice", "account:bob", "account:carol"} {
		if !slices.Contains(scopes, scope) {
			t.Fatalf("MailAccountScopes = %#v, missing %s", scopes, scope)
		}
	}
	trashed, err := TrashedMailCopyCount(db, "carol", "mail-1")
	requireResult(t, "TrashedMailCopyCount carol", trashed, err, 1)
	trashed, err = TrashedMailCopyCount(db, "bob", "mail-1")
	requireResult(t, "TrashedMailCopyCount bob", trashed, err, 0)

	used, err := MailUsedBytes(db, "bob")
	requireNoError(t, "MailUsedBytes", err)
	if want := mailSize * 2; used != want {
		t.Fatalf("MailUsedBytes bob = %d, want %d", used, want)
	}
	ok, err = MailQuotaAllows(db, "bob", DefaultMailQuotaBytes-used)
	requireResult(t, "MailQuotaAllows at limit", ok, err, true)
	ok, err = MailQuotaAllows(db, "bob", DefaultMailQuotaBytes-used+1)
	requireResult(t, "MailQuotaAllows over limit", ok, err, false)
	ok, err = MailQuotaAllows(db, "", DefaultMailQuotaBytes+1)
	requireResult(t, "MailQuotaAllows blank user", ok, err, true)
}
