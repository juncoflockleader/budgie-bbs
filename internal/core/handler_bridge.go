package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/handler"
)

type Handler = handler.Handler
type Reply = handler.Reply

type pollBlock struct {
	question  string
	options   []string
	expiresAt int64
}

func newHandler(db *sql.DB, bus Bus) *Handler {
	handler.SetRuntime(handler.Runtime{
		CheckProcessed:               checkProcessed,
		QQueryRow:                    qQueryRow,
		ActiveSanction:               activeSanction,
		MatchContentFilter:           matchContentFilter,
		NowMS:                        nowMS,
		NewID:                        newID,
		AppendEvent:                  appendEvent,
		GetThread:                    getThread,
		GetPost:                      getPost,
		GetUserTx:                    getUserTx,
		GetThreadTx:                  getThreadTx,
		GetPostTx:                    getPostTx,
		GetPollWithVotes:             getPollWithVotes,
		InsertThread:                 insertThread,
		InsertPost:                   insertPost,
		BumpThread:                   bumpThread,
		InsertPoll:                   insertPoll,
		InsertPollOption:             insertPollOption,
		InsertPostAttachment:         insertPostAttachment,
		InsertMailAttachment:         insertMailAttachment,
		InsertRelayDelivery:          insertRelayDelivery,
		UpsertReaction:               upsertReaction,
		ReactionCountTx:              reactionCountTx,
		DeleteReaction:               deleteReaction,
		UserReacted:                  userReacted,
		MarkPostRedacted:             markPostRedacted,
		MarkPostRestored:             markPostRestored,
		MarkPostPurged:               markPostPurged,
		SetPostFlags:                 setPostFlags,
		SetThreadLocked:              setThreadLocked,
		MoveThreadBoard:              moveThreadBoard,
		SetUserRole:                  setUserRole,
		InsertBoard:                  insertBoard,
		GetDigestExport:              getDigestExport,
		InsertMailMessage:            insertMailMessage,
		InsertMailCopy:               insertMailCopy,
		InsertNotification:           insertNotification,
		UpdateMailCopy:               updateMailCopy,
		TrashMailCopy:                trashMailCopy,
		SetMailGroup:                 setMailGroup,
		DeleteMailGroup:              deleteMailGroup,
		GetMailGroupID:               getMailGroupID,
		ListMailGroupMembers:         listMailGroupMembers,
		ListFriendUserIDs:            listFriendUserIDs,
		ListLoginWatchers:            listLoginWatchers,
		InsertDirectMessage:          insertDirectMessage,
		InsertBlessing:               insertBlessing,
		MarkDirectMessageRead:        markDirectMessageRead,
		DeleteDirectMessage:          deleteDirectMessage,
		GetDirectMessageSettings:     getDirectMessageSettings,
		SetDirectMessageSettings:     setDirectMessageSettings,
		SetUserRelationship:          setUserRelationship,
		SetUserPresence:              setUserPresence,
		UserIgnores:                  userIgnores,
		InsertModerationReview:       insertModerationReview,
		UpsertContentFilter:          upsertContentFilter,
		ResolveModerationReview:      resolveModerationReview,
		CastVote:                     castVote,
		SetThreadPref:                setThreadPref,
		SetBoardFavorite:             setBoardFavorite,
		CreateFavoriteFolder:         createFavoriteFolder,
		UpdateFavoriteFolder:         updateFavoriteFolder,
		DeleteFavoriteFolder:         deleteFavoriteFolder,
		MoveBoardFavorite:            moveBoardFavorite,
		ImportFavoriteTree:           importFavoriteTree,
		GetBoardSettings:             getBoardSettings,
		SetBoardSettings:             setBoardSettings,
		GetBoardMemberRequirements:   getBoardMemberRequirements,
		SetBoardMemberRequirements:   setBoardMemberRequirements,
		SetBoardModerator:            setBoardModerator,
		SetBoardMember:               setBoardMember,
		InsertBoardMemberApplication: insertBoardMemberApplication,
		ReviewBoardMemberApplication: reviewBoardMemberApplication,
		UpsertDigestEntry:            upsertDigestEntry,
		RemoveDigestEntry:            removeDigestEntry,
		UpdateDigestEntry:            updateDigestEntry,
		SetDigestEntryBody:           setDigestEntryBody,
		UpsertDigestDirectory:        upsertDigestDirectory,
		CountDigestPathEntries:       countDigestPathEntries,
		CountDigestPathDirectories:   countDigestPathDirectories,
		MoveDigestPath:               moveDigestPath,
		CopyDigestPath:               copyDigestPath,
		DeleteDigestPath:             deleteDigestPath,
		MarkBoardRead:                markBoardRead,
		RestoreBoardRead:             restoreBoardRead,
		MarkFavoriteFolderRead:       markFavoriteFolderRead,
		RestoreFavoriteFolderRead:    restoreFavoriteFolderRead,
		MarkThreadRead:               markThreadRead,
		RestoreThreadRead:            restoreThreadRead,
		MarkPostRead:                 markPostRead,
		FtsInsertPost:                ftsInsertPost,
		FtsUpdatePost:                ftsUpdatePost,
		FtsDeletePost:                ftsDeletePost,
		RecordProcessed:              recordProcessed,
		RecordReactionReceived:       recordReactionReceived,
		RecordReactionRemoved:        recordReactionRemoved,
		UserTrustLevel:               userTrustLevel,
		UpdatePostBody:               updatePostBody,
		InsertSanction:               insertSanction,
		ClearUserSanctions:           clearUserSanctions,
		EnqueueOutboxJob:             enqueueOutboxJob,
	})
	return handler.New(db, bus)
}

func parseMentions(body string) []string {
	return handler.ParseMentions(body)
}

func parsePollExpires(raw string) (int64, error) {
	return handler.ParsePollExpires(raw)
}

func extractPoll(body string) (*pollBlock, string) {
	pb, cleanBody := handler.ParsePoll(body)
	if pb == nil {
		return nil, cleanBody
	}
	return &pollBlock{
		question:  pb.Question,
		options:   pb.Options,
		expiresAt: pb.ExpiresAt,
	}, cleanBody
}

func getUserTx(tx *sql.Tx, id string) (*User, error) {
	u := &User{}
	err := qQueryRow(tx, `SELECT id, name, role, password, created,
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

func getThreadTx(tx *sql.Tx, id string) (*Thread, error) {
	t := &Thread{}
	var locked int
	err := qQueryRow(tx,
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

func getPostTx(tx *sql.Tx, id string) (*Post, error) {
	p := &Post{}
	var redacted, marked, recommended, noReply int
	err := qQueryRow(tx,
		`SELECT id, thread, author, COALESCE(author_id,''), body, COALESCE(signature,''), content_type, COALESCE(reply_to,''), version,
		        redacted, marked, recommended, no_reply,
		        COALESCE(source_post,''), COALESCE(source_thread,''), COALESCE(source_board,''),
		        COALESCE(source_author,''), COALESCE(source_author_id,''), COALESCE(source_title,''),
		        created_seq, updated_seq, created_at, updated_at
		   FROM posts WHERE id=?`, id,
	).Scan(
		&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType, &p.ReplyTo, &p.Version,
		&redacted, &marked, &recommended, &noReply,
		&p.SourcePost, &p.SourceThread, &p.SourceBoard, &p.SourceAuthor, &p.SourceAuthorID, &p.SourceTitle,
		&p.CreatedSeq, &p.UpdatedSeq, &p.CreatedAt, &p.UpdatedAt,
	)
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
	p.Marked = marked != 0
	p.Recommended = recommended != 0
	p.NoReply = noReply != 0
	return p, nil
}
