package proto

import "sort"

// EventKind identifies which event type the server emitted.
type EventKind string

const (
	// Durable events — carry seq, persist permanently, replayable.
	EvtThreadNew                       EventKind = "thread.new"
	EvtPostAppended                    EventKind = "post.appended"
	EvtPostAttachmentAdded             EventKind = "post.attachment_added"
	EvtPostEdited                      EventKind = "post.edited"
	EvtPostFlagsSet                    EventKind = "post.flags_set"
	EvtPostRedacted                    EventKind = "post.redacted"
	EvtPostRestored                    EventKind = "post.restored"
	EvtPostDeletionCleared             EventKind = "post.deletion_cleared"
	EvtThreadTitleSet                  EventKind = "thread.title_set"
	EvtThreadLocked                    EventKind = "thread.locked"
	EvtThreadMoved                     EventKind = "thread.moved"
	EvtUserSanctioned                  EventKind = "user.sanctioned"
	EvtUserSanctionCleared             EventKind = "user.sanction_cleared"
	EvtContentFilterSet                EventKind = "content_filter.set"
	EvtBoardAutomodRuleSet             EventKind = "board.automod_rule_set"
	EvtBoardAutomodRuleDeleted         EventKind = "board.automod_rule_deleted"
	EvtBoardAutomodTriggered           EventKind = "board.automod_triggered"
	EvtRoleGranted                     EventKind = "role.granted"
	EvtRoleRevoked                     EventKind = "role.revoked"
	EvtBoardCreated                    EventKind = "board.created"
	EvtBoardSettingsSet                EventKind = "board.settings_set"
	EvtBoardMemberRequirementsSet      EventKind = "board.member_requirements_set"
	EvtBoardModeratorSet               EventKind = "board.moderator_set"
	EvtBoardMemberSet                  EventKind = "board.member_set"
	EvtBoardMemberApplicationSubmitted EventKind = "board.member_application_submitted"
	EvtBoardMemberApplicationReviewed  EventKind = "board.member_application_reviewed"
	EvtBoardRecommendedSet             EventKind = "board.recommended_set"
	EvtBoardFavoriteSet                EventKind = "board.favorite_set"
	EvtFavoriteFolderCreated           EventKind = "favorite_folder.created"
	EvtFavoriteFolderUpdated           EventKind = "favorite_folder.updated"
	EvtFavoriteFolderDeleted           EventKind = "favorite_folder.deleted"
	EvtFavoriteTreeImported            EventKind = "favorite_tree.imported"
	EvtBoardZapSet                     EventKind = "board.zap_set"
	EvtPostPurged                      EventKind = "post.purged" // GDPR hard-delete; body removed from projection
	EvtMailSent                        EventKind = "mail.sent"
	EvtMailAttachmentAdded             EventKind = "mail.attachment_added"
	EvtMailGroupSet                    EventKind = "mail.group_set"
	EvtMailGroupDeleted                EventKind = "mail.group_deleted"
	EvtMailCopyUpdated                 EventKind = "mail.copy_updated"
	EvtDirectMessageSent               EventKind = "direct_message.sent"
	EvtDirectMessageRead               EventKind = "direct_message.read"
	EvtDirectMessageDeleted            EventKind = "direct_message.deleted"
	EvtDirectMessageSettingsSet        EventKind = "direct_message.settings_set"
	EvtUserRelationshipSet             EventKind = "user.relationship_set"
	EvtUserBlessed                     EventKind = "user.blessed"
	EvtDigestEntryUpserted             EventKind = "digest.entry_upserted"
	EvtDigestEntryUpdated              EventKind = "digest.entry_updated"
	EvtDigestEntryBodySet              EventKind = "digest.entry_body_set"
	EvtDigestEntryRemoved              EventKind = "digest.entry_removed"
	EvtDigestDirectorySet              EventKind = "digest.directory_set"
	EvtDigestPathMoved                 EventKind = "digest.path_moved"
	EvtDigestPathCopied                EventKind = "digest.path_copied"
	EvtDigestPathDeleted               EventKind = "digest.path_deleted"
	EvtCommunityStatsSnapshotRecorded  EventKind = "community_stats.snapshot_recorded"
	EvtCounterCheckpointed             EventKind = "counter.checkpointed"

	// M8 — Notifications
	EvtMentioned           EventKind = "user.mentioned" // durable: @username in post body
	EvtNotificationCreated EventKind = "notification.created"

	// M9 — Trust levels
	EvtTrustLevelChanged EventKind = "user.trust_level_changed"

	// Modern forum moderation review queue
	EvtPostFlagged    EventKind = "post.flagged"
	EvtReviewResolved EventKind = "review.resolved"

	// Ephemeral events — carry eseq, best-effort, prunable.
	EvtChatLine       EventKind = "chat.line"
	EvtPresenceUpdate EventKind = "presence.update"
	EvtMUDRoom        EventKind = "mud.room"
	EvtMUDView        EventKind = "mud.view"
	EvtUserJoined     EventKind = "user.joined"
	EvtUserLeft       EventKind = "user.left"
	EvtPostReacted    EventKind = "post.reacted"
	EvtPostUnreacted  EventKind = "post.unreacted"
	EvtPollVoted      EventKind = "poll.voted"

	// M14 — Node spy (ephemeral): SSH session lifecycle.
	EvtNodeConnected    EventKind = "node.connected"
	EvtNodeDisconnected EventKind = "node.disconnected"
	// EvtNodeMessage is delivered only to the target node's TUI session.
	EvtNodeMessage EventKind = "node.message"
)

// Event is a fact emitted by the server.
type Event struct {
	ID      string    `json:"id,omitempty"` // durable event id; internal/replay metadata
	Kind    EventKind `json:"event"`
	Seq     int64     `json:"seq,omitempty"`  // durable events
	ESeq    int64     `json:"eseq,omitempty"` // ephemeral events
	Payload any       `json:"payload"`
	TS      int64     `json:"ts"`

	// Partition metadata is the durable write-ordering scope for this event.
	// Seq remains the global compatibility cursor; PartitionOffset is local to
	// PartitionKind/PartitionKey.
	PartitionKind   string `json:"partitionKind,omitempty"`
	PartitionKey    string `json:"partitionKey,omitempty"`
	PartitionOffset int64  `json:"partitionOffset,omitempty"`

	// Scopes tags this event for pub/sub routing; not sent over the wire.
	Scopes []string `json:"-"`
}

// IsDurable reports whether this is a permanent log event.
func (e *Event) IsDurable() bool {
	switch e.Kind {
	case EvtThreadNew, EvtPostAppended, EvtPostAttachmentAdded, EvtPostEdited, EvtPostRedacted,
		EvtPostFlagsSet, EvtPostRestored, EvtPostDeletionCleared, EvtPostPurged, EvtThreadTitleSet, EvtThreadLocked, EvtThreadMoved,
		EvtUserSanctioned, EvtUserSanctionCleared, EvtContentFilterSet, EvtBoardAutomodRuleSet, EvtBoardAutomodRuleDeleted, EvtBoardAutomodTriggered, EvtRoleGranted, EvtRoleRevoked, EvtBoardCreated, EvtBoardSettingsSet, EvtBoardMemberRequirementsSet, EvtBoardModeratorSet, EvtBoardMemberSet, EvtBoardMemberApplicationSubmitted, EvtBoardMemberApplicationReviewed,
		EvtBoardRecommendedSet, EvtBoardFavoriteSet, EvtFavoriteFolderCreated, EvtFavoriteFolderUpdated, EvtFavoriteFolderDeleted, EvtFavoriteTreeImported, EvtBoardZapSet,
		EvtMailSent, EvtMailAttachmentAdded, EvtMailGroupSet, EvtMailGroupDeleted, EvtMailCopyUpdated,
		EvtDirectMessageSent, EvtDirectMessageRead, EvtDirectMessageDeleted, EvtDirectMessageSettingsSet,
		EvtUserRelationshipSet,
		EvtUserBlessed, EvtDigestEntryUpserted, EvtDigestEntryUpdated, EvtDigestEntryBodySet, EvtDigestEntryRemoved,
		EvtDigestDirectorySet, EvtDigestPathMoved, EvtDigestPathCopied, EvtDigestPathDeleted,
		EvtCommunityStatsSnapshotRecorded, EvtCounterCheckpointed, EvtMentioned, EvtNotificationCreated, EvtTrustLevelChanged, EvtPostFlagged, EvtReviewResolved:
		return true
	}
	return false
}

// SortEventsByReplayOrder orders durable events by compatibility seq, with
// deterministic partition metadata tie-breakers for partition-aware batches.
func SortEventsByReplayOrder(events []*Event) {
	sort.Slice(events, func(i, j int) bool {
		if events[i].Seq == events[j].Seq {
			if events[i].PartitionKind == events[j].PartitionKind {
				if events[i].PartitionKey == events[j].PartitionKey {
					return events[i].PartitionOffset < events[j].PartitionOffset
				}
				return events[i].PartitionKey < events[j].PartitionKey
			}
			return events[i].PartitionKind < events[j].PartitionKind
		}
		return events[i].Seq < events[j].Seq
	})
}

// Durable event payloads.

type ThreadNewPayload struct {
	ID       string `json:"id"`
	Board    string `json:"board"`
	Author   string `json:"author"`
	AuthorID string `json:"authorId,omitempty"`
	Title    string `json:"title"`
	TS       int64  `json:"ts"`
}

type PostAppendedPayload struct {
	ID                  string              `json:"id"`
	Thread              string              `json:"thread"`
	Author              string              `json:"author"`
	AuthorID            string              `json:"authorId,omitempty"`
	Body                string              `json:"body"`
	RawBody             string              `json:"rawBody,omitempty"`
	PostCommitBody      *string             `json:"postCommitBody,omitempty"`
	PostCommitActorID   string              `json:"postCommitActorId,omitempty"`
	PostCommitActorName string              `json:"postCommitActorName,omitempty"`
	Signature           string              `json:"signature,omitempty"`
	ContentType         string              `json:"contentType"`
	ReplyTo             string              `json:"replyTo,omitempty"`
	TeX                 bool                `json:"tex,omitempty"`
	MailBack            bool                `json:"mailBack,omitempty"`
	SourcePost          string              `json:"sourcePost,omitempty"`
	SourceThread        string              `json:"sourceThread,omitempty"`
	SourceBoard         string              `json:"sourceBoard,omitempty"`
	SourceAuthor        string              `json:"sourceAuthor,omitempty"`
	SourceAuthorID      string              `json:"sourceAuthorId,omitempty"`
	SourceTitle         string              `json:"sourceTitle,omitempty"`
	Attachments         []AttachmentPayload `json:"attachments,omitempty"`
	TS                  int64               `json:"ts"`
}

type PostAttachmentAddedPayload struct {
	ID           string `json:"id"`
	Post         string `json:"post"`
	Thread       string `json:"thread"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	AuthorID     string `json:"authorId,omitempty"`
	StagedBlobID string `json:"stagedBlobId,omitempty"`
	TS           int64  `json:"ts"`
}

type CommunityStatsSnapshotRecordedPayload struct {
	Day                 string `json:"day"`
	SnapshotAt          int64  `json:"snapshotAt"`
	TotalUsers          int    `json:"totalUsers"`
	TotalBoards         int    `json:"totalBoards"`
	TotalThreads        int    `json:"totalThreads"`
	TotalPosts          int    `json:"totalPosts"`
	TotalReactions      int    `json:"totalReactions"`
	TotalMail           int    `json:"totalMail"`
	TotalDirectMessages int    `json:"totalDirectMessages"`
	TotalLogins         int    `json:"totalLogins"`
	TotalLogouts        int    `json:"totalLogouts"`
	TotalWebLogins      int    `json:"totalWebLogins"`
	TotalWebLogouts     int    `json:"totalWebLogouts"`
	TotalGuestLogins    int    `json:"totalGuestLogins"`
	TotalGuestLogouts   int    `json:"totalGuestLogouts"`
	TotalOnlineSeconds  int64  `json:"totalOnlineSeconds"`
	OnlineUsers         int    `json:"onlineUsers"`
	OnlineGuests        int    `json:"onlineGuests"`
	MaxOnlineUsers      int    `json:"maxOnlineUsers"`
	MaxOnlineAt         int64  `json:"maxOnlineAt"`
	MaxOnlineGuests     int    `json:"maxOnlineGuests"`
	MaxOnlineGuestsAt   int64  `json:"maxOnlineGuestsAt"`
	HeadSeq             int64  `json:"headSeq"`
}

type CounterCheckpointPayload struct {
	Complete        bool                              `json:"complete,omitempty"`
	SourceHeadSeq   int64                             `json:"sourceHeadSeq"`
	PostReactions   []PostReactionCounterCheckpoint   `json:"postReactions,omitempty"`
	PollOptionVotes []PollOptionVoteCounterCheckpoint `json:"pollOptionVotes,omitempty"`
	TS              int64                             `json:"ts"`
}

type PostReactionCounterCheckpoint struct {
	PostID string `json:"postId"`
	Count  int    `json:"count"`
}

type PollOptionVoteCounterCheckpoint struct {
	PollID   string `json:"pollId"`
	OptionID string `json:"optionId"`
	Count    int    `json:"count"`
}

type PostEditedPayload struct {
	ID      string `json:"id"`
	Thread  string `json:"thread"`
	NewBody string `json:"newBody"`
	Version int    `json:"version"`
	TS      int64  `json:"ts"`
}

type PostFlagsSetPayload struct {
	ID          string `json:"id"`
	Thread      string `json:"thread"`
	Marked      bool   `json:"marked"`
	Recommended bool   `json:"recommended"`
	NoReply     bool   `json:"noReply"`
	TeX         bool   `json:"tex"`
	MailBack    bool   `json:"mailBack"`
	By          string `json:"by"`
	TS          int64  `json:"ts"`
}

type PostRedactedPayload struct {
	ID           string `json:"id"`
	Thread       string `json:"thread"`
	By           string `json:"by"`
	Reason       string `json:"reason,omitempty"`
	DeletionKind string `json:"deletionKind,omitempty"`
	TS           int64  `json:"ts"`
}

type PostRestoredPayload struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
	By     string `json:"by"`
	TS     int64  `json:"ts"`
}

type PostDeletionClearedPayload struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
	Board  string `json:"board"`
	Kind   string `json:"kind"`
	By     string `json:"by"`
	TS     int64  `json:"ts"`
}

type PostPurgedPayload struct {
	ID     string `json:"id"`
	Thread string `json:"thread"`
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
	TS     int64  `json:"ts"`
}

type ThreadTitleSetPayload struct {
	Thread string `json:"thread"`
	Title  string `json:"title"`
	By     string `json:"by"`
	TS     int64  `json:"ts"`
}

type ThreadLockedPayload struct {
	Thread string `json:"thread"`
	Locked bool   `json:"locked"`
	By     string `json:"by"`
	TS     int64  `json:"ts"`
}

type ThreadMovedPayload struct {
	Thread    string `json:"thread"`
	FromBoard string `json:"fromBoard"`
	ToBoard   string `json:"toBoard"`
	By        string `json:"by"`
	TS        int64  `json:"ts"`
}

type UserSanctionedPayload struct {
	User        string `json:"user"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope,omitempty"`
	DurationSec int64  `json:"durationSec,omitempty"`
	By          string `json:"by"`
	Reason      string `json:"reason,omitempty"`
	TS          int64  `json:"ts"`
}

type UserSanctionClearedPayload struct {
	User   string `json:"user"`
	Kind   string `json:"kind,omitempty"`
	Scope  string `json:"scope,omitempty"`
	By     string `json:"by"`
	Reason string `json:"reason,omitempty"`
	TS     int64  `json:"ts"`
}

type ContentFilterSetPayload struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Scope   string `json:"scope,omitempty"`
	Active  bool   `json:"active"`
	By      string `json:"by"`
	TS      int64  `json:"ts"`
}

type BoardAutomodRuleSetPayload struct {
	ID          string `json:"id"`
	Board       string `json:"board"`
	Enabled     bool   `json:"enabled"`
	Priority    int    `json:"priority"`
	MatchType   string `json:"matchType"`
	Pattern     string `json:"pattern,omitempty"`
	Threshold   int    `json:"threshold,omitempty"`
	WindowSec   int    `json:"windowSec,omitempty"`
	Action      string `json:"action"`
	DurationSec int64  `json:"durationSec,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Note        string `json:"note,omitempty"`
	By          string `json:"by"`
	TS          int64  `json:"ts"`
}

type BoardAutomodRuleDeletedPayload struct {
	ID    string `json:"id"`
	Board string `json:"board"`
	By    string `json:"by"`
	TS    int64  `json:"ts"`
}

// BoardAutomodTriggeredPayload audits a fired automod rule.
type BoardAutomodTriggeredPayload struct {
	ID         string `json:"id"`
	Board      string `json:"board"`
	RuleID     string `json:"ruleId"`
	MatchType  string `json:"matchType"`
	Action     string `json:"action"`
	TargetUser string `json:"targetUser"`
	PostID     string `json:"postId,omitempty"`
	ThreadID   string `json:"threadId,omitempty"`
	Reason     string `json:"reason,omitempty"`
	TS         int64  `json:"ts"`
}

type RoleGrantedPayload struct {
	User string `json:"user"`
	Role string `json:"role"`
	By   string `json:"by"`
	TS   int64  `json:"ts"`
}

type RoleRevokedPayload struct {
	User string `json:"user"`
	Role string `json:"role"`
	By   string `json:"by"`
	TS   int64  `json:"ts"`
}

func RoleChangePayload(kind EventKind, user, role, by string, ts int64) any {
	switch kind {
	case EvtRoleGranted:
		return &RoleGrantedPayload{User: user, Role: role, By: by, TS: ts}
	case EvtRoleRevoked:
		return &RoleRevokedPayload{User: user, Role: role, By: by, TS: ts}
	default:
		return nil
	}
}

type BoardCreatedPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    string `json:"parentId,omitempty"`
	Position    int    `json:"position,omitempty"`
	By          string `json:"by"`
	TS          int64  `json:"ts"`
}

type BoardSettingsSetPayload struct {
	Board              string `json:"board"`
	AnonymousAllowed   bool   `json:"anonymousAllowed"`
	ReadOnly           bool   `json:"readOnly"`
	NoReply            bool   `json:"noReply"`
	AttachmentsAllowed bool   `json:"attachmentsAllowed"`
	MailInAllowed      bool   `json:"mailInAllowed"`
	RelayEnabled       bool   `json:"relayEnabled"`
	MemberReadMode     bool   `json:"memberReadMode"`
	MemberPostMode     bool   `json:"memberPostMode"`
	StatsExcluded      bool   `json:"statsExcluded"`
	ZapAllowed         bool   `json:"zapAllowed"`
	GuestAccess        string `json:"guestAccess"`
	By                 string `json:"by,omitempty"`
	TS                 int64  `json:"ts"`
}

type BoardMemberRequirementsSetPayload struct {
	Board                     string `json:"board"`
	MinLoginCount             int    `json:"minLoginCount"`
	MinPostCount              int    `json:"minPostCount"`
	MinTrustLevel             int    `json:"minTrustLevel"`
	MinScore                  int    `json:"minScore"`
	MinBoardPostCount         int    `json:"minBoardPostCount"`
	MinBoardOriginalPostCount int    `json:"minBoardOriginalPostCount"`
	MinBoardDigestCount       int    `json:"minBoardDigestCount"`
	MinBoardMarkCount         int    `json:"minBoardMarkCount"`
	MaxMembers                int    `json:"maxMembers"`
	ApprovalMode              string `json:"approvalMode"`
	By                        string `json:"by,omitempty"`
	TS                        int64  `json:"ts"`
}

type BoardModeratorSetPayload struct {
	Board     string `json:"board"`
	User      string `json:"user"`
	Moderator bool   `json:"moderator"`
	Position  int    `json:"position"`
	By        string `json:"by,omitempty"`
	TS        int64  `json:"ts"`
}

type BoardMemberSetPayload struct {
	Board               string `json:"board"`
	User                string `json:"user"`
	Member              bool   `json:"member"`
	Title               string `json:"title,omitempty"`
	Position            int    `json:"position"`
	CanManageMembers    bool   `json:"canManageMembers,omitempty"`
	CanCurate           bool   `json:"canCurate,omitempty"`
	CanModeratePosts    bool   `json:"canModeratePosts,omitempty"`
	CanModerateThreads  bool   `json:"canModerateThreads,omitempty"`
	CanAnnounce         bool   `json:"canAnnounce,omitempty"`
	CanManagePolls      bool   `json:"canManagePolls,omitempty"`
	CanSetBoardSettings bool   `json:"canSetBoardSettings,omitempty"`
	By                  string `json:"by,omitempty"`
	TS                  int64  `json:"ts"`
}

type BoardMemberApplicationSubmittedPayload struct {
	ID    string `json:"id"`
	Board string `json:"board"`
	User  string `json:"user"`
	Note  string `json:"note,omitempty"`
	TS    int64  `json:"ts"`
}

type BoardMemberApplicationReviewedPayload struct {
	Application string `json:"application"`
	Board       string `json:"board"`
	User        string `json:"user"`
	Status      string `json:"status"`
	Title       string `json:"title,omitempty"`
	Reviewer    string `json:"reviewer"`
	ReviewNote  string `json:"reviewNote,omitempty"`
	TS          int64  `json:"ts"`
}

type BoardZapSetPayload struct {
	UserID string `json:"userId"`
	Board  string `json:"board"`
	Zapped bool   `json:"zapped"`
	TS     int64  `json:"ts"`
}

type BoardFavoriteSetPayload struct {
	UserID   string `json:"userId"`
	Board    string `json:"board"`
	Favorite bool   `json:"favorite"`
	FolderID string `json:"folderId,omitempty"`
	Position *int   `json:"position,omitempty"`
	TS       int64  `json:"ts"`
}

type BoardRecommendedSetPayload struct {
	Board       string `json:"board"`
	Recommended bool   `json:"recommended"`
	Note        string `json:"note,omitempty"`
	Position    int    `json:"position,omitempty"`
	CuratedBy   string `json:"curatedBy,omitempty"`
	TS          int64  `json:"ts"`
}

type FavoriteFolderCreatedPayload struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	TS       int64  `json:"ts"`
}

type FavoriteFolderUpdatedPayload struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Position int    `json:"position"`
	TS       int64  `json:"ts"`
}

type FavoriteFolderDeletedPayload struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	ParentID string `json:"parentId,omitempty"`
	TS       int64  `json:"ts"`
}

type FavoriteTreeImportedFolderPayload struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type FavoriteTreeImportedBoardPayload struct {
	ID       string `json:"id"`
	FolderID string `json:"folderId,omitempty"`
	Position int    `json:"position"`
}

type FavoriteTreeImportedPayload struct {
	UserID  string                              `json:"userId"`
	Folders []FavoriteTreeImportedFolderPayload `json:"folders,omitempty"`
	Boards  []FavoriteTreeImportedBoardPayload  `json:"boards,omitempty"`
	Replace bool                                `json:"replace"`
	TS      int64                               `json:"ts"`
}

type MailSentPayload struct {
	ID          string              `json:"id"`
	FromUserID  string              `json:"fromUserId"`
	From        string              `json:"from"`
	ToUserIDs   []string            `json:"toUserIds"`
	To          []string            `json:"to"`
	Subject     string              `json:"subject"`
	Body        string              `json:"body"`
	ParentID    string              `json:"parentId,omitempty"`
	SaveSent    bool                `json:"saveSent"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
	TS          int64               `json:"ts"`
}

func NewMailSentPayload(id, fromUserID, fromName string, toUserIDs, toNames []string, subject, body, parentID string, saveSent bool, attachments []AttachmentPayload, ts int64) *MailSentPayload {
	return &MailSentPayload{
		ID:          id,
		FromUserID:  fromUserID,
		From:        fromName,
		ToUserIDs:   toUserIDs,
		To:          toNames,
		Subject:     subject,
		Body:        body,
		ParentID:    parentID,
		SaveSent:    saveSent,
		Attachments: attachments,
		TS:          ts,
	}
}

type MailAttachmentAddedPayload struct {
	ID           string `json:"id"`
	Mail         string `json:"mail"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	AuthorID     string `json:"authorId,omitempty"`
	Author       string `json:"author,omitempty"`
	StagedBlobID string `json:"stagedBlobId,omitempty"`
	TS           int64  `json:"ts"`
}

type MailGroupSetPayload struct {
	ID        string   `json:"id"`
	OwnerID   string   `json:"ownerId"`
	Name      string   `json:"name"`
	MemberIDs []string `json:"memberIds,omitempty"`
	TS        int64    `json:"ts"`
}

type MailGroupDeletedPayload struct {
	ID      string `json:"id"`
	OwnerID string `json:"ownerId"`
	TS      int64  `json:"ts"`
}

type MailCopyUpdatedPayload struct {
	Mail    string  `json:"mail"`
	UserID  string  `json:"userId"`
	Mailbox *string `json:"mailbox,omitempty"`
	Read    *bool   `json:"read,omitempty"`
	Kept    *bool   `json:"kept,omitempty"`
	TS      int64   `json:"ts"`
}

type DirectMessageSentPayload struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	FromUserID     string `json:"fromUserId"`
	From           string `json:"from"`
	ToUserID       string `json:"toUserId"`
	To             string `json:"to"`
	Body           string `json:"body"`
	TS             int64  `json:"ts"`
}

func NewDirectMessageSentPayload(id, fromUserID, fromName, toUserID, toName, body string, ts int64) *DirectMessageSentPayload {
	return &DirectMessageSentPayload{
		ID:             id,
		ConversationID: DirectConversationID(fromUserID, toUserID),
		FromUserID:     fromUserID,
		From:           fromName,
		ToUserID:       toUserID,
		To:             toName,
		Body:           body,
		TS:             ts,
	}
}

type DirectMessageReadPayload struct {
	MessageID string `json:"messageId"`
	UserID    string `json:"userId"`
	ReadAt    int64  `json:"readAt"`
	TS        int64  `json:"ts"`
}

type DirectMessageDeletedPayload struct {
	MessageID        string `json:"messageId"`
	UserID           string `json:"userId"`
	SenderDeleted    bool   `json:"senderDeleted"`
	RecipientDeleted bool   `json:"recipientDeleted"`
	TS               int64  `json:"ts"`
}

type DirectMessageSettingsSetPayload struct {
	UserID string `json:"userId"`
	Policy string `json:"policy"`
	TS     int64  `json:"ts"`
}

type UserRelationshipSetPayload struct {
	UserID       string `json:"userId"`
	TargetUserID string `json:"targetUserId"`
	Kind         string `json:"kind"`
	Active       bool   `json:"active"`
	Note         string `json:"note,omitempty"`
	TS           int64  `json:"ts"`
}

type UserBlessedPayload struct {
	ID         string `json:"id"`
	FromUserID string `json:"fromUserId"`
	From       string `json:"from"`
	ToUserID   string `json:"toUserId"`
	To         string `json:"to"`
	Message    string `json:"message,omitempty"`
	TS         int64  `json:"ts"`
}

type DigestEntryUpsertedPayload struct {
	ID         string `json:"id"`
	Board      string `json:"board"`
	TargetKind string `json:"targetKind"`
	TargetID   string `json:"targetId"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Path       string `json:"path,omitempty"`
	Note       string `json:"note,omitempty"`
	CreatedBy  string `json:"createdBy,omitempty"`
	TS         int64  `json:"ts"`
}

type DigestEntryUpdatedPayload struct {
	ID         string `json:"id"`
	Board      string `json:"board"`
	TargetKind string `json:"targetKind,omitempty"`
	TargetID   string `json:"targetId,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Title      string `json:"title"`
	Path       string `json:"path,omitempty"`
	Note       string `json:"note,omitempty"`
	By         string `json:"by,omitempty"`
	TS         int64  `json:"ts"`
}

type DigestEntryBodySetPayload struct {
	ID     string `json:"id"`
	Board  string `json:"board"`
	Kind   string `json:"kind,omitempty"`
	Body   string `json:"body,omitempty"`
	Edited bool   `json:"edited,omitempty"`
	By     string `json:"by,omitempty"`
	TS     int64  `json:"ts"`
}

type DigestEntryRemovedPayload struct {
	ID    string `json:"id"`
	Board string `json:"board"`
	Kind  string `json:"kind,omitempty"`
	By    string `json:"by,omitempty"`
	TS    int64  `json:"ts"`
}

type DigestDirectorySetPayload struct {
	ID        string `json:"id"`
	Board     string `json:"board"`
	Kind      string `json:"kind"`
	Path      string `json:"path"`
	CreatedBy string `json:"createdBy,omitempty"`
	TS        int64  `json:"ts"`
}

type DigestPathMovedPayload struct {
	Board    string `json:"board"`
	Kind     string `json:"kind"`
	FromPath string `json:"fromPath"`
	ToPath   string `json:"toPath"`
	Count    int    `json:"count,omitempty"`
	By       string `json:"by,omitempty"`
	TS       int64  `json:"ts"`
}

type DigestPathCopiedPayload struct {
	Board        string   `json:"board"`
	Kind         string   `json:"kind"`
	FromPath     string   `json:"fromPath"`
	ToPath       string   `json:"toPath"`
	EntryIDs     []string `json:"entryIds,omitempty"`
	DirectoryIDs []string `json:"directoryIds,omitempty"`
	Count        int      `json:"count,omitempty"`
	CreatedBy    string   `json:"createdBy,omitempty"`
	TS           int64    `json:"ts"`
}

type DigestPathDeletedPayload struct {
	Board string `json:"board"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
	Count int    `json:"count,omitempty"`
	By    string `json:"by,omitempty"`
	TS    int64  `json:"ts"`
}

// M10 — Reaction payloads.

type PostReactedPayload struct {
	PostID        string `json:"postId"`
	Thread        string `json:"thread"`
	User          string `json:"user"`
	Emoji         string `json:"emoji"`
	ReactionCount int    `json:"reactionCount"`
	TS            int64  `json:"ts"`
}

type PostUnreactedPayload struct {
	PostID        string `json:"postId"`
	Thread        string `json:"thread"`
	User          string `json:"user"`
	Emoji         string `json:"emoji"`
	ReactionCount int    `json:"reactionCount"`
	TS            int64  `json:"ts"`
}

// M11 — Poll payload.

type PollVotedPayload struct {
	Poll   string `json:"poll"`
	Option string `json:"option"`
	User   string `json:"user"`
	TS     int64  `json:"ts"`
}

// M8 — Notification payload (durable mention event).

type MentionedPayload struct {
	User     string `json:"user"` // mentioned user ID
	By       string `json:"by"`   // author username
	PostID   string `json:"postId"`
	ThreadID string `json:"threadId"`
	TS       int64  `json:"ts"`
}

type NotificationCreatedPayload struct {
	ID       string `json:"id"`
	UserID   string `json:"userId"`
	Kind     string `json:"kind"`
	ThreadID string `json:"threadId,omitempty"`
	PostID   string `json:"postId,omitempty"`
	Actor    string `json:"actor"`
	TS       int64  `json:"ts"`
}

// M9 — Trust level payload.

type TrustLevelChangedPayload struct {
	User     string `json:"user"`
	OldLevel int    `json:"oldLevel"`
	NewLevel int    `json:"newLevel"`
	TS       int64  `json:"ts"`
}

type PostFlaggedPayload struct {
	ReviewID string `json:"reviewId"`
	Kind     string `json:"kind,omitempty"`
	PostID   string `json:"postId"`
	Thread   string `json:"thread"`
	Reporter string `json:"reporter"`
	Reason   string `json:"reason,omitempty"`
	TS       int64  `json:"ts"`
}

type ReviewResolvedPayload struct {
	ReviewID   string `json:"reviewId"`
	Resolution string `json:"resolution"`
	By         string `json:"by"`
	TS         int64  `json:"ts"`
}

// Ephemeral event payloads.

type ChatLinePayload struct {
	ID   string `json:"id"`
	Room string `json:"room"`
	User string `json:"user"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

// MUDRoomEventPayload is a live, room-scoped MUD event delivered to everyone in
// the room. Kind is one of: say, emote, enter, leave, pose, tell. Text is the
// fully-rendered line (e.g. "alice says, \"hi\"" or "bob arrives from the south").
type MUDRoomEventPayload struct {
	Room  string `json:"room"`
	Kind  string `json:"kind"`
	Actor string `json:"actor"`
	Text  string `json:"text"`
	TS    int64  `json:"ts"`
}

// MUDViewPayload is delivered privately to the acting player (scope
// mud:user:<id>) in response to their own command. Room refreshes the room
// panel (look / after moving); Lines are direct feedback (help, who, errors);
// Left signals the player has left the world so the client closes the view.
type MUDViewPayload struct {
	Room  *MUDRoomView `json:"room,omitempty"`
	Lines []string     `json:"lines,omitempty"`
	Left  bool         `json:"left,omitempty"`
	TS    int64        `json:"ts"`
}

type PresenceUpdatePayload struct {
	User      string `json:"user"`
	UserID    string `json:"userId,omitempty"`
	SessionID string `json:"sessionId,omitempty"`
	Status    string `json:"status"`
	Mode      string `json:"mode,omitempty"`
	Board     string `json:"board,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Location  string `json:"location,omitempty"`
	FromHost  string `json:"fromHost,omitempty"`
	TS        int64  `json:"ts"`
}

type UserJoinedPayload struct {
	User string `json:"user"`
	TS   int64  `json:"ts"`
}

type UserLeftPayload struct {
	User string `json:"user"`
	TS   int64  `json:"ts"`
}

// M14 — Node spy payloads.

type NodeConnectedPayload struct {
	NodeID   string `json:"nodeId"`
	User     string `json:"user"`
	RemoteIP string `json:"remoteIp,omitempty"`
	TS       int64  `json:"ts"`
}

type NodeDisconnectedPayload struct {
	NodeID string `json:"nodeId"`
	User   string `json:"user"`
	TS     int64  `json:"ts"`
}

// NodeMessagePayload carries a sysop-to-node message injected into the target
// session's status bar. Delivered only via the in-process channel; never
// stored or replayed.
type NodeMessagePayload struct {
	NodeID string `json:"nodeId"`
	From   string `json:"from"`
	Text   string `json:"text"`
	TS     int64  `json:"ts"`
}
