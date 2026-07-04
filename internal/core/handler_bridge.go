package core

import (
	"database/sql"

	"github.com/juncoflockleader/budgie-bbs/internal/core/counterstore"
	"github.com/juncoflockleader/budgie-bbs/internal/core/handler"
	"github.com/juncoflockleader/budgie-bbs/internal/core/projections"
)

type Handler = handler.Handler
type Reply = handler.Reply
type CommandPartition = handler.CommandPartition
type CounterStore = counterstore.Store
type CounterMutation = counterstore.Mutation
type CounterUserIdentity = counterstore.UserIdentity
type CounterReactionIdentity = counterstore.ReactionIdentity
type CounterPollVoteIdentity = counterstore.PollVoteIdentity
type PresenceStore = handler.PresenceStore
type PresenceStats = handler.PresenceStats
type ChatStore = handler.ChatStore

type pollBlock struct {
	question  string
	options   []string
	expiresAt int64
}

func newHandler(db *sql.DB, bus Bus, counterStore CounterStore, presenceStore PresenceStore, chatStore ChatStore) *Handler {
	if counterStore == nil {
		counterStore = sqlCounterStore{db: db}
	}
	if presenceStore == nil {
		presenceStore = sqlPresenceStore{db: db}
	}
	if chatStore == nil {
		chatStore = sqlChatStore{db: db}
	}
	handler.SetRuntime(handler.Runtime{
		CheckProcessed:               projections.CheckProcessed,
		QQueryRow:                    qQueryRow,
		ActiveSanction:               projections.ActiveSanction,
		MatchContentFilter:           projections.MatchContentFilter,
		NowMS:                        nowMS,
		NewID:                        newID,
		AppendEvent:                  appendEvent,
		GetThread:                    projections.GetThread,
		GetPost:                      projections.GetPost,
		GetMail:                      projections.GetMail,
		GetUserTx:                    getUserTx,
		GetThreadTx:                  getThreadTx,
		GetPostTx:                    getPostTx,
		GetPollWithVotes:             projections.GetPollWithVotes,
		InsertThread:                 projections.InsertThread,
		InsertPost:                   projections.InsertPost,
		BumpThread:                   projections.BumpThread,
		InsertPoll:                   projections.InsertPoll,
		InsertPollOption:             projections.InsertPollOption,
		InsertPostAttachment:         projections.InsertPostAttachment,
		InsertMailAttachment:         projections.InsertMailAttachment,
		PromoteStagedPostBlob:        promoteStagedPostAttachmentBlob,
		PromoteStagedMailBlob:        promoteStagedMailAttachmentBlob,
		InsertRelayDelivery:          projections.InsertRelayDelivery,
		CounterStore:                 counterStore,
		PresenceStore:                presenceStore,
		ChatStore:                    chatStore,
		MarkPostRedacted:             projections.MarkPostRedacted,
		MarkPostRestored:             projections.MarkPostRestored,
		RecordPostDeletion:           projections.RecordPostDeletion,
		ClearPostDeletion:            projections.ClearPostDeletion,
		MarkPostPurged:               projections.MarkPostPurged,
		SetPostFlags:                 projections.SetPostFlags,
		SetThreadLocked:              projections.SetThreadLocked,
		SetThreadTitle:               projections.SetThreadTitle,
		MoveThreadBoard:              projections.MoveThreadBoard,
		SetUserRole:                  projections.SetUserRole,
		InsertBoard:                  projections.InsertBoard,
		GetDigestExport:              projections.GetDigestExport,
		InsertMailMessage:            projections.InsertMailMessage,
		InsertMailCopy:               projections.InsertMailCopy,
		InsertNotification:           insertNotification,
		InsertNotificationTx:         insertNotificationTx,
		UpdateMailCopy:               projections.UpdateMailCopy,
		TrashMailCopy:                projections.TrashMailCopy,
		SetMailGroup:                 projections.SetMailGroup,
		DeleteMailGroup:              projections.DeleteMailGroup,
		GetMailGroupID:               getMailGroupID,
		ListMailGroupMembers:         projections.ListMailGroupMembers,
		ListFriendUserIDs:            projections.ListFriendUserIDs,
		ListLoginWatchers:            projections.ListLoginWatchers,
		InsertDirectMessage:          projections.InsertDirectMessage,
		InsertBlessing:               projections.InsertBlessing,
		MarkDirectMessageRead:        projections.MarkDirectMessageRead,
		DeleteDirectMessage:          projections.DeleteDirectMessage,
		GetDirectMessageSettings:     projections.GetDirectMessageSettings,
		SetDirectMessageSettings:     projections.SetDirectMessageSettings,
		SetUserRelationship:          projections.SetUserRelationship,
		SetUserPresence:              setUserPresence,
		UserIgnores:                  userIgnores,
		InsertModerationReview:       projections.InsertModerationReview,
		UpsertContentFilter:          projections.UpsertContentFilter,
		UpsertBoardAutomodRule:       upsertBoardAutomodRule,
		DeleteBoardAutomodRule:       projections.DeleteBoardAutomodRule,
		EvaluateBoardAutomod:         evaluateBoardAutomodForHandler,
		InsertAutomodAuditLog:        insertAutomodAuditLog,
		ResolveModerationReview:      projections.ResolveModerationReview,
		SetThreadPref:                projections.SetThreadPref,
		WatchersOfThreadTx:           watchersOfThreadTx,
		SetBoardFavorite:             projections.SetBoardFavorite,
		SetBoardZap:                  projections.SetBoardZap,
		CreateFavoriteFolder:         projections.CreateFavoriteFolder,
		UpdateFavoriteFolder:         projections.UpdateFavoriteFolder,
		DeleteFavoriteFolder:         projections.DeleteFavoriteFolder,
		MoveBoardFavorite:            projections.MoveBoardFavorite,
		ImportFavoriteTree:           importFavoriteTree,
		GetBoardSettings:             projections.GetBoardSettings,
		BoardAllowsPublicSystemPost:  projections.BoardAllowsPublicSystemPost,
		SetBoardSettings:             projections.SetBoardSettings,
		SetRecommendedBoard:          projections.SetRecommendedBoard,
		GetBoardMemberRequirements:   projections.GetBoardMemberRequirements,
		SetBoardMemberRequirements:   projections.SetBoardMemberRequirements,
		SetBoardModerator:            projections.SetBoardModerator,
		SetBoardMember:               projections.SetBoardMember,
		GetBoardMemberApplication:    projections.GetBoardMemberApplication,
		InsertBoardMemberApplication: projections.InsertBoardMemberApplication,
		ReviewBoardMemberApplication: projections.ReviewBoardMemberApplication,
		UpsertDigestEntry:            projections.UpsertDigestEntry,
		UpsertDigestEntryTx:          projections.UpsertDigestEntryTx,
		RemoveDigestEntry:            projections.RemoveDigestEntry,
		RemoveDigestEntryTx:          projections.RemoveDigestEntryTx,
		RemoveDigestEntryFinalTx:     projections.RemoveDigestEntryFinalTx,
		UpdateDigestEntry:            projections.UpdateDigestEntry,
		UpdateDigestEntryTx:          projections.UpdateDigestEntryTx,
		SetDigestEntryBody:           projections.SetDigestEntryBody,
		SetDigestEntryBodyTx:         projections.SetDigestEntryBodyTx,
		UpsertDigestDirectory:        projections.UpsertDigestDirectory,
		UpsertDigestDirectoryTx:      projections.UpsertDigestDirectoryTx,
		CountDigestPathEntries:       projections.CountDigestPathEntries,
		CountDigestPathDirectories:   projections.CountDigestPathDirectories,
		MoveDigestPath:               projections.MoveDigestPath,
		MoveDigestPathTx:             projections.MoveDigestPathTx,
		MoveDigestPathFinalTx:        projections.MoveDigestPathFinalTx,
		CopyDigestPath:               projections.CopyDigestPath,
		CopyDigestPathTx:             projections.CopyDigestPathTx,
		DeleteDigestPath:             projections.DeleteDigestPath,
		DeleteDigestPathTx:           projections.DeleteDigestPathTx,
		DeleteDigestPathFinalTx:      projections.DeleteDigestPathFinalTx,
		MarkBoardRead:                projections.MarkBoardRead,
		RestoreBoardRead:             projections.RestoreBoardRead,
		MarkFavoriteFolderRead:       projections.MarkFavoriteFolderRead,
		RestoreFavoriteFolderRead:    projections.RestoreFavoriteFolderRead,
		MarkThreadRead:               projections.MarkThreadRead,
		RestoreThreadRead:            projections.RestoreThreadRead,
		MarkPostRead:                 projections.MarkPostRead,
		FtsInsertPost:                commandFtsInsertPost,
		FtsUpdatePost:                commandFtsUpdatePost,
		FtsDeletePost:                commandFtsDeletePost,
		RecordProcessed:              projections.RecordProcessed,
		RecordReactionReceived:       projections.RecordReactionReceived,
		RecordReactionRemoved:        projections.RecordReactionRemoved,
		UserTrustLevel:               userTrustLevel,
		UpdatePostBody:               projections.UpdatePostBody,
		InsertSanction:               projections.InsertSanction,
		ClearUserSanctions:           projections.ClearUserSanctions,
		EnqueueOutboxJob:             enqueueOutboxJob,
		RecordCommunityStatSnapshot:  recordCommunityStatSnapshot,
		PGNotifyEphemeral:            pgNotifyEphemeralFn,
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
	var redacted, marked, recommended, noReply, tex, mailBack int
	err := qQueryRow(tx,
		`SELECT id, thread, author, COALESCE(author_id,''), body, COALESCE(signature,''), content_type, COALESCE(reply_to,''), version,
		        redacted, marked, recommended, no_reply, tex, mail_back,
		        COALESCE(source_post,''), COALESCE(source_thread,''), COALESCE(source_board,''),
		        COALESCE(source_author,''), COALESCE(source_author_id,''), COALESCE(source_title,''),
		        created_seq, updated_seq, created_at, updated_at
		   FROM posts WHERE id=?`, id,
	).Scan(
		&p.ID, &p.Thread, &p.Author, &p.AuthorID, &p.Body, &p.Signature, &p.ContentType, &p.ReplyTo, &p.Version,
		&redacted, &marked, &recommended, &noReply, &tex, &mailBack,
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
	p.TeX = tex != 0
	p.MailBack = mailBack != 0
	return p, nil
}
