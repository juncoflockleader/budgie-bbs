package proto

import (
	"regexp"
	"regexp/syntax"
	"strings"
)

// maxAutomodRegexProgramSize bounds the compiled NFA instruction count of a
// user-supplied automod regex. RE2 has no catastrophic backtracking, but large
// bounded repetitions (e.g. `a{1000}` repeated) expand into huge programs whose
// match time, though linear, can still burn seconds of CPU per post. Bounding
// the program size keeps any single rule cheap to evaluate. A normal pattern
// compiles to well under this; `a{1000}`-style blowups are rejected.
const maxAutomodRegexProgramSize = 1000

// AutomodRegexWithinComplexityLimit reports whether a regex compiles to a
// program small enough to evaluate cheaply (see maxAutomodRegexProgramSize).
func AutomodRegexWithinComplexityLimit(pattern string) bool {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return false
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return false
	}
	return len(prog.Inst) <= maxAutomodRegexProgramSize
}

// CommandName identifies which command a client is sending.
type CommandName string

const (
	CmdCreateThread               CommandName = "createThread"
	CmdAppendPost                 CommandName = "appendPost"
	CmdRepostPost                 CommandName = "repostPost"
	CmdPostBoardMail              CommandName = "postBoardMail"
	CmdAttachPost                 CommandName = "attachPost"
	CmdEditPost                   CommandName = "editPost"
	CmdSetPostFlag                CommandName = "setPostFlag"
	CmdRedactPost                 CommandName = "redactPost"
	CmdRestorePost                CommandName = "restorePost"
	CmdRedactPostRange            CommandName = "redactPostRange"
	CmdRestorePostRange           CommandName = "restorePostRange"
	CmdClearBoardJunk             CommandName = "clearBoardJunk"
	CmdSetThreadTitle             CommandName = "setThreadTitle"
	CmdLockThread                 CommandName = "lockThread"
	CmdMoveThread                 CommandName = "moveThread"
	CmdSanctionUser               CommandName = "sanctionUser"
	CmdClearUserSanction          CommandName = "clearUserSanction"
	CmdSetContentFilter           CommandName = "setContentFilter"
	CmdGrantRole                  CommandName = "grantRole"
	CmdRevokeRole                 CommandName = "revokeRole"
	CmdPublishStatsSnapshot       CommandName = "publishStatsSnapshot"
	CmdPublishSystemNotice        CommandName = "publishSystemNotice"
	CmdSendChatLine               CommandName = "sendChatLine"
	CmdSetPresence                CommandName = "setPresence"
	CmdMUDCommand                 CommandName = "mudCommand"
	CmdCreateBoard                CommandName = "createBoard"
	CmdSetBoardSettings           CommandName = "setBoardSettings"
	CmdSetBoardModerator          CommandName = "setBoardModerator"
	CmdSetBoardMember             CommandName = "setBoardMember"
	CmdSetBoardMemberRequirements CommandName = "setBoardMemberRequirements"
	CmdSetRecommendedBoard        CommandName = "setRecommendedBoard"
	CmdApplyBoardMembership       CommandName = "applyBoardMembership"
	CmdReviewBoardMembership      CommandName = "reviewBoardMembership"
	CmdLeaveBoardMembership       CommandName = "leaveBoardMembership"
	CmdCuratePost                 CommandName = "curatePost"
	CmdCurateThread               CommandName = "curateThread"
	CmdRemoveDigestEntry          CommandName = "removeDigestEntry"
	CmdUpdateDigestEntry          CommandName = "updateDigestEntry"
	CmdSetDigestEntryBody         CommandName = "setDigestEntryBody"
	CmdCreateDigestDirectory      CommandName = "createDigestDirectory"
	CmdMoveDigestPath             CommandName = "moveDigestPath"
	CmdCopyDigestPath             CommandName = "copyDigestPath"
	CmdDeleteDigestPath           CommandName = "deleteDigestPath"
	CmdSendDigestEntryMail        CommandName = "sendDigestEntryMail"
	CmdMailPostAuthor             CommandName = "mailPostAuthor"
	CmdSendMail                   CommandName = "sendMail"
	CmdForwardMail                CommandName = "forwardMail"
	CmdPostMailToBoard            CommandName = "postMailToBoard"
	CmdSetMailGroup               CommandName = "setMailGroup"
	CmdDeleteMailGroup            CommandName = "deleteMailGroup"
	CmdAttachMail                 CommandName = "attachMail"
	CmdUpdateMail                 CommandName = "updateMail"
	CmdDeleteMail                 CommandName = "deleteMail"
	CmdDeleteMailRange            CommandName = "deleteMailRange"
	CmdSendDirectMessage          CommandName = "sendDirectMessage"
	CmdSetDirectMessageSettings   CommandName = "setDirectMessageSettings"
	CmdMarkDirectMessageRead      CommandName = "markDirectMessageRead"
	CmdDeleteDirectMessage        CommandName = "deleteDirectMessage"
	CmdSetUserRelationship        CommandName = "setUserRelationship"
	CmdSetLoginWatch              CommandName = "setLoginWatch"
	CmdBlessUser                  CommandName = "blessUser"
	CmdSetBoardFavorite           CommandName = "setBoardFavorite"
	CmdSetBoardZap                CommandName = "setBoardZap"
	CmdCreateFavoriteFolder       CommandName = "createFavoriteFolder"
	CmdUpdateFavoriteFolder       CommandName = "updateFavoriteFolder"
	CmdDeleteFavoriteFolder       CommandName = "deleteFavoriteFolder"
	CmdMoveBoardFavorite          CommandName = "moveBoardFavorite"
	CmdImportFavoriteTree         CommandName = "importFavoriteTree"
	CmdMarkBoardRead              CommandName = "markBoardRead"
	CmdRestoreBoardRead           CommandName = "restoreBoardRead"
	CmdMarkFavoriteFolderRead     CommandName = "markFavoriteFolderRead"
	CmdRestoreFavoriteFolderRead  CommandName = "restoreFavoriteFolderRead"
	CmdMarkThreadRead             CommandName = "markThreadRead"
	CmdRestoreThreadRead          CommandName = "restoreThreadRead"
	CmdMarkPostRead               CommandName = "markPostRead"
	CmdSubscribe                  CommandName = "subscribe"
	CmdUnsubscribe                CommandName = "unsubscribe"
	CmdPurgePost                  CommandName = "purgePost" // admin-only physical delete of post body (GDPR)

	// M10 — Reactions
	CmdReactPost   CommandName = "reactPost"
	CmdUnreactPost CommandName = "unreactPost"

	// M11 — Polls
	CmdVotePoll          CommandName = "votePoll"
	CmdPublishPollResult CommandName = "publishPollResult"

	// M8 — Notifications
	CmdSetThreadPref CommandName = "setThreadPref"

	// Modern forum moderation
	CmdFlagPost      CommandName = "flagPost"
	CmdResolveReview CommandName = "resolveReview"

	// Board automod rules
	CmdSetBoardAutomodRule    CommandName = "setBoardAutomodRule"
	CmdDeleteBoardAutomodRule CommandName = "deleteBoardAutomodRule"
)

type CreateBoardPayload struct {
	ID          string `json:"id"` // URL-safe slug
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	Position    *int   `json:"position,omitempty"`
}

type SetBoardSettingsPayload struct {
	Board              string `json:"board"`
	AnonymousAllowed   *bool  `json:"anonymousAllowed,omitempty"`
	ReadOnly           *bool  `json:"readOnly,omitempty"`
	NoReply            *bool  `json:"noReply,omitempty"`
	AttachmentsAllowed *bool  `json:"attachmentsAllowed,omitempty"`
	MailInAllowed      *bool  `json:"mailInAllowed,omitempty"`
	RelayEnabled       *bool  `json:"relayEnabled,omitempty"`
	MemberReadMode     *bool  `json:"memberReadMode,omitempty"`
	MemberPostMode     *bool  `json:"memberPostMode,omitempty"`
	StatsExcluded      *bool  `json:"statsExcluded,omitempty"`
	ZapAllowed         *bool  `json:"zapAllowed,omitempty"`
}

type SetBoardModeratorPayload struct {
	Board     string `json:"board"`
	User      string `json:"user"`
	Moderator bool   `json:"moderator"`
	Position  *int   `json:"position,omitempty"`
}

type SetBoardMemberPayload struct {
	Board               string `json:"board"`
	User                string `json:"user"`
	Member              bool   `json:"member"`
	Title               string `json:"title,omitempty"`
	Position            *int   `json:"position,omitempty"`
	CanManageMembers    *bool  `json:"canManageMembers,omitempty"`
	CanCurate           *bool  `json:"canCurate,omitempty"`
	CanModeratePosts    *bool  `json:"canModeratePosts,omitempty"`
	CanModerateThreads  *bool  `json:"canModerateThreads,omitempty"`
	CanAnnounce         *bool  `json:"canAnnounce,omitempty"`
	CanManagePolls      *bool  `json:"canManagePolls,omitempty"`
	CanSetBoardSettings *bool  `json:"canSetBoardSettings,omitempty"`
}

type SetBoardMemberRequirementsPayload struct {
	Board                     string  `json:"board"`
	MinLoginCount             *int    `json:"minLoginCount,omitempty"`
	MinPostCount              *int    `json:"minPostCount,omitempty"`
	MinTrustLevel             *int    `json:"minTrustLevel,omitempty"`
	MinScore                  *int    `json:"minScore,omitempty"`
	MinBoardPostCount         *int    `json:"minBoardPostCount,omitempty"`
	MinBoardOriginalPostCount *int    `json:"minBoardOriginalPostCount,omitempty"`
	MinBoardDigestCount       *int    `json:"minBoardDigestCount,omitempty"`
	MinBoardMarkCount         *int    `json:"minBoardMarkCount,omitempty"`
	MaxMembers                *int    `json:"maxMembers,omitempty"`
	ApprovalMode              *string `json:"approvalMode,omitempty"`
}

type SetRecommendedBoardPayload struct {
	Board       string `json:"board"`
	Recommended bool   `json:"recommended"`
	Note        string `json:"note,omitempty"`
	Position    *int   `json:"position,omitempty"`
}

type SetContentFilterPayload struct {
	ID      string `json:"id,omitempty"`
	Pattern string `json:"pattern"`
	Scope   string `json:"scope,omitempty"` // board id or "global"
	Active  *bool  `json:"active,omitempty"`
}

// SetBoardAutomodRulePayload creates or updates a board automod rule. Empty ID
// creates a new rule.
type SetBoardAutomodRulePayload struct {
	ID          string `json:"id,omitempty"`
	Board       string `json:"board"`
	Enabled     *bool  `json:"enabled,omitempty"`
	Priority    int    `json:"priority,omitempty"`
	MatchType   string `json:"matchType"`             // keyword|regex|repeated_text|link_count|account_age|rate_threshold
	Pattern     string `json:"pattern,omitempty"`     // keyword/regex text
	Threshold   int    `json:"threshold,omitempty"`   // repeated/link/age/rate count
	WindowSec   int    `json:"windowSec,omitempty"`   // rate_threshold window
	Action      string `json:"action"`                // manual_review|redact|lock_thread|board_mute|board_ban|global_mute
	DurationSec int64  `json:"durationSec,omitempty"` // for mute/ban actions, 0 = permanent
	Reason      string `json:"reason,omitempty"`      // public/audit reason
	Note        string `json:"note,omitempty"`        // private moderator note
}

// DeleteBoardAutomodRulePayload removes a board automod rule.
type DeleteBoardAutomodRulePayload struct {
	ID    string `json:"id"`
	Board string `json:"board"`
}

// AutomodMatchTypes and AutomodActions are the valid automod rule enums.
var AutomodMatchTypes = map[string]bool{
	"keyword": true, "regex": true, "repeated_text": true,
	"link_count": true, "account_age": true, "rate_threshold": true,
}

var AutomodActions = map[string]bool{
	"manual_review": true, "redact": true, "lock_thread": true,
	"board_mute": true, "board_ban": true, "global_mute": true,
}

// ParseAutomodActions splits a rule's action field into individual actions. A
// rule may carry several comma-separated actions (e.g. "redact,board_ban"),
// applied together in priority order when the rule matches.
func ParseAutomodActions(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ValidateAutomodRule checks rule fields and returns a validation message, or ""
// if the rule is valid. Shared by the command handler and the native
// command-log decider so both paths validate identically.
func ValidateAutomodRule(p SetBoardAutomodRulePayload) string {
	matchType := strings.TrimSpace(p.MatchType)
	if !AutomodMatchTypes[matchType] {
		return "unknown match type"
	}
	actions := ParseAutomodActions(p.Action)
	if len(actions) == 0 {
		return "at least one action is required"
	}
	for _, a := range actions {
		if !AutomodActions[a] {
			return "unknown action: " + a
		}
	}
	pattern := strings.TrimSpace(p.Pattern)
	switch matchType {
	case "keyword", "regex":
		if pattern == "" {
			return "pattern is required for keyword/regex rules"
		}
		if len(pattern) > 500 {
			return "pattern must be 500 characters or less"
		}
		if matchType == "regex" {
			if _, err := regexp.Compile(pattern); err != nil {
				return "invalid regular expression"
			}
			if !AutomodRegexWithinComplexityLimit(pattern) {
				return "regular expression is too complex"
			}
		}
	case "repeated_text":
		if p.Threshold < 2 {
			return "repeated_text threshold must be at least 2"
		}
	case "link_count", "account_age":
		if p.Threshold < 1 {
			return "threshold must be at least 1"
		}
	case "rate_threshold":
		if p.Threshold < 1 || p.WindowSec < 1 {
			return "rate_threshold needs a positive threshold and window"
		}
	}
	if len(strings.TrimSpace(p.Reason)) > 500 {
		return "reason must be 500 characters or less"
	}
	if len(strings.TrimSpace(p.Note)) > 1000 {
		return "note must be 1000 characters or less"
	}
	if p.DurationSec < 0 {
		return "duration cannot be negative"
	}
	return ""
}

type ApplyBoardMembershipPayload struct {
	Board string `json:"board"`
	Note  string `json:"note,omitempty"`
}

type ReviewBoardMembershipPayload struct {
	Application string `json:"application"`
	Status      string `json:"status"`
	Title       string `json:"title,omitempty"`
	Note        string `json:"note,omitempty"`
}

type LeaveBoardMembershipPayload struct {
	Board string `json:"board"`
}

type CuratePostPayload struct {
	Post  string `json:"post"`
	Kind  string `json:"kind,omitempty"`
	Title string `json:"title,omitempty"`
	Path  string `json:"path,omitempty"`
	Note  string `json:"note,omitempty"`
}

type CurateThreadPayload struct {
	Thread string `json:"thread"`
	Kind   string `json:"kind,omitempty"`
	Title  string `json:"title,omitempty"`
	Path   string `json:"path,omitempty"`
	Note   string `json:"note,omitempty"`
}

type RemoveDigestEntryPayload struct {
	Entry string `json:"entry"`
}

type UpdateDigestEntryPayload struct {
	Entry string  `json:"entry"`
	Title *string `json:"title,omitempty"`
	Path  *string `json:"path,omitempty"`
	Note  *string `json:"note,omitempty"`
}

type SetDigestEntryBodyPayload struct {
	Entry string `json:"entry"`
	Body  string `json:"body,omitempty"`
	Reset bool   `json:"reset,omitempty"`
}

type CreateDigestDirectoryPayload struct {
	Board string `json:"board"`
	Kind  string `json:"kind,omitempty"`
	Path  string `json:"path"`
}

type MoveDigestPathPayload struct {
	Board    string `json:"board"`
	Kind     string `json:"kind,omitempty"`
	FromPath string `json:"fromPath"`
	ToPath   string `json:"toPath"`
}

type CopyDigestPathPayload struct {
	Board    string `json:"board"`
	Kind     string `json:"kind,omitempty"`
	FromPath string `json:"fromPath"`
	ToPath   string `json:"toPath"`
}

type DeleteDigestPathPayload struct {
	Board string `json:"board"`
	Kind  string `json:"kind,omitempty"`
	Path  string `json:"path"`
}

type SendDigestEntryMailPayload struct {
	Entry     string   `json:"entry"`
	To        []string `json:"to"`
	ToGroups  []string `json:"toGroups,omitempty"`
	ToFriends bool     `json:"toFriends,omitempty"`
	ToAll     bool     `json:"toAll,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Note      string   `json:"note,omitempty"`
	SaveSent  *bool    `json:"saveSent,omitempty"`
}

type MailPostAuthorPayload struct {
	Post     string `json:"post"`
	Subject  string `json:"subject,omitempty"`
	Body     string `json:"body"`
	SaveSent *bool  `json:"saveSent,omitempty"`
}

type SendMailPayload struct {
	To          []string            `json:"to"`
	ToGroups    []string            `json:"toGroups,omitempty"`
	ToFriends   bool                `json:"toFriends,omitempty"`
	ToAll       bool                `json:"toAll,omitempty"`
	Subject     string              `json:"subject"`
	Body        string              `json:"body"`
	ReplyTo     string              `json:"replyTo,omitempty"`
	SaveSent    *bool               `json:"saveSent,omitempty"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
}

type ForwardMailPayload struct {
	Mail      string   `json:"mail"`
	To        []string `json:"to"`
	ToGroups  []string `json:"toGroups,omitempty"`
	ToFriends bool     `json:"toFriends,omitempty"`
	ToAll     bool     `json:"toAll,omitempty"`
	Subject   string   `json:"subject,omitempty"`
	Note      string   `json:"note,omitempty"`
	SaveSent  *bool    `json:"saveSent,omitempty"`
}

type PostMailToBoardPayload struct {
	Mail    string `json:"mail"`
	Board   string `json:"board,omitempty"`
	Thread  string `json:"thread,omitempty"`
	Subject string `json:"subject,omitempty"`
	Note    string `json:"note,omitempty"`
}

type SetMailGroupPayload struct {
	Group   string   `json:"group,omitempty"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type DeleteMailGroupPayload struct {
	Group string `json:"group"`
}

type AttachMailPayload struct {
	ID           string `json:"id,omitempty"`
	Mail         string `json:"mail"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	StagedBlobID string `json:"stagedBlobId,omitempty"`
}

type UpdateMailPayload struct {
	Mailbox *string `json:"mailbox,omitempty"`
	Mail    string  `json:"mail"`
	Read    *bool   `json:"read,omitempty"`
	Kept    *bool   `json:"kept,omitempty"`
}

type DeleteMailPayload struct {
	Mail string `json:"mail"`
}

type DeleteMailRangePayload struct {
	Mail []string `json:"mail"`
}

type SendDirectMessagePayload struct {
	To   string `json:"to"`
	Body string `json:"body"`
}

type SetDirectMessageSettingsPayload struct {
	Policy string `json:"policy"`
}

type MarkDirectMessageReadPayload struct {
	Message string `json:"message"`
}

type DeleteDirectMessagePayload struct {
	Message string `json:"message"`
}

type SetUserRelationshipPayload struct {
	User   string `json:"user"`
	Kind   string `json:"kind"`
	Active bool   `json:"active"`
	Note   string `json:"note,omitempty"`
}

type SetLoginWatchPayload struct {
	User   string `json:"user"`
	Active bool   `json:"active"`
}

type BlessUserPayload struct {
	User    string `json:"user"`
	Message string `json:"message,omitempty"`
}

type SetBoardFavoritePayload struct {
	Board    string `json:"board"`
	Favorite bool   `json:"favorite"`
	FolderID string `json:"folderId,omitempty"`
	Position *int   `json:"position,omitempty"`
}

type SetBoardZapPayload struct {
	Board  string `json:"board"`
	Zapped bool   `json:"zapped"`
}

type CreateFavoriteFolderPayload struct {
	Name     string `json:"name"`
	ParentID string `json:"parentId,omitempty"`
	Position *int   `json:"position,omitempty"`
}

type UpdateFavoriteFolderPayload struct {
	Folder   string  `json:"folder"`
	Name     string  `json:"name,omitempty"`
	ParentID *string `json:"parentId,omitempty"`
	Position *int    `json:"position,omitempty"`
}

type DeleteFavoriteFolderPayload struct {
	Folder string `json:"folder"`
}

type MoveBoardFavoritePayload struct {
	Board    string `json:"board"`
	FolderID string `json:"folderId,omitempty"`
	Position *int   `json:"position,omitempty"`
}

type ImportFavoriteFolderPayload struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId,omitempty"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type ImportFavoriteBoardPayload struct {
	ID       string `json:"id"`
	FolderID string `json:"folderId,omitempty"`
	Position int    `json:"position"`
}

type ImportFavoriteTreePayload struct {
	Folders []ImportFavoriteFolderPayload `json:"folders"`
	Boards  []ImportFavoriteBoardPayload  `json:"boards"`
	Replace *bool                         `json:"replace,omitempty"`
}

type MarkBoardReadPayload struct {
	Board string `json:"board"`
}

type RestoreBoardReadPayload struct {
	Board string `json:"board"`
}

type MarkFavoriteFolderReadPayload struct {
	Folder string `json:"folder,omitempty"`
}

type RestoreFavoriteFolderReadPayload struct {
	Folder string `json:"folder,omitempty"`
}

type MarkThreadReadPayload struct {
	Thread string `json:"thread"`
}

type RestoreThreadReadPayload struct {
	Thread string `json:"thread"`
}

type MarkPostReadPayload struct {
	Post string `json:"post"`
}

type AttachmentPayload struct {
	ID          string `json:"id,omitempty"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	URL         string `json:"url,omitempty"`
}

type CreateThreadPayload struct {
	Board       string              `json:"board"`
	Title       string              `json:"title"`
	Body        string              `json:"body"`
	ContentType string              `json:"contentType,omitempty"`
	Anonymous   bool                `json:"anonymous,omitempty"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
}

type AppendPostPayload struct {
	Thread      string              `json:"thread"`
	Body        string              `json:"body"`
	ReplyTo     string              `json:"replyTo,omitempty"`
	QuotePost   bool                `json:"quotePost,omitempty"`
	ContentType string              `json:"contentType,omitempty"`
	Anonymous   bool                `json:"anonymous,omitempty"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
}

type RepostPostPayload struct {
	Post  string `json:"post"`
	Board string `json:"board"`
	Title string `json:"title,omitempty"`
}

type PostBoardMailPayload struct {
	Board       string              `json:"board"`
	Thread      string              `json:"thread,omitempty"`
	Subject     string              `json:"subject,omitempty"`
	Body        string              `json:"body"`
	ContentType string              `json:"contentType,omitempty"`
	Attachments []AttachmentPayload `json:"attachments,omitempty"`
}

type AttachPostPayload struct {
	ID           string `json:"id,omitempty"`
	Post         string `json:"post"`
	Filename     string `json:"filename"`
	ContentType  string `json:"contentType,omitempty"`
	SizeBytes    int64  `json:"sizeBytes,omitempty"`
	StagedBlobID string `json:"stagedBlobId,omitempty"`
}

type EditPostPayload struct {
	Post string `json:"post"`
	Body string `json:"body"`
}

type SetPostFlagPayload struct {
	Post        string `json:"post"`
	Marked      *bool  `json:"marked,omitempty"`
	Recommended *bool  `json:"recommended,omitempty"`
	NoReply     *bool  `json:"noReply,omitempty"`
	TeX         *bool  `json:"tex,omitempty"`
	MailBack    *bool  `json:"mailBack,omitempty"`
}

type RedactPostPayload struct {
	Post   string `json:"post"`
	Reason string `json:"reason,omitempty"`
}

type RestorePostPayload struct {
	Post string `json:"post"`
}

type RedactPostRangePayload struct {
	Board  string   `json:"board"`
	Posts  []string `json:"posts"`
	Reason string   `json:"reason,omitempty"`
}

type RestorePostRangePayload struct {
	Board string   `json:"board"`
	Posts []string `json:"posts"`
}

type ClearBoardJunkPayload struct {
	Board string   `json:"board"`
	Posts []string `json:"posts,omitempty"`
}

type SetThreadTitlePayload struct {
	Thread string `json:"thread"`
	Title  string `json:"title"`
}

type LockThreadPayload struct {
	Thread string `json:"thread"`
	Locked bool   `json:"locked"`
}

type MoveThreadPayload struct {
	Thread  string `json:"thread"`
	ToBoard string `json:"toBoard"`
}

type SanctionUserPayload struct {
	User        string `json:"user"`
	Kind        string `json:"kind"`            // "mute" | "ban"
	Scope       string `json:"scope,omitempty"` // board id or "global"
	DurationSec int64  `json:"durationSec,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

type ClearUserSanctionPayload struct {
	User   string `json:"user"`
	Kind   string `json:"kind,omitempty"`  // empty clears both mute and ban
	Scope  string `json:"scope,omitempty"` // board id or "global"
	Reason string `json:"reason,omitempty"`
}

type GrantRolePayload struct {
	User string `json:"user"`
	Role string `json:"role"`
}

type RevokeRolePayload struct {
	User string `json:"user"`
	Role string `json:"role"`
}

type SendChatLinePayload struct {
	Room string `json:"room"`
	Text string `json:"text"`
}

// MUDCommandPayload carries a raw MUD command line (e.g. "look", "north",
// "say hi", "who"). The server parses the verb; this keeps the wire format
// stable as new verbs are added.
type MUDCommandPayload struct {
	Line string `json:"line"`
}

type PublishStatsSnapshotPayload struct {
	Date string `json:"date,omitempty"` // YYYY-MM-DD, defaults to current UTC day
}

type PublishSystemNoticePayload struct {
	Board  string `json:"board,omitempty"` // KBS-style notice board, defaults to notepad
	Title  string `json:"title"`
	Body   string `json:"body"`
	Source string `json:"source,omitempty"`
}

type SetPresencePayload struct {
	Status    string `json:"status"`
	SessionID string `json:"sessionId,omitempty"`
	Mode      string `json:"mode,omitempty"`
	Board     string `json:"board,omitempty"`
	Thread    string `json:"thread,omitempty"`
	Location  string `json:"location,omitempty"`
	FromHost  string `json:"fromHost,omitempty"`
}

type PurgePostPayload struct {
	Post   string `json:"post"`
	Reason string `json:"reason,omitempty"`
}

// M10 — Reactions
type ReactPostPayload struct {
	Post  string `json:"post"`
	Emoji string `json:"emoji,omitempty"` // defaults to "heart"
}

// M11 — Polls
type VotePollPayload struct {
	Poll   string `json:"poll"`
	Option string `json:"option"`
}

type PublishPollResultPayload struct {
	Poll string `json:"poll"`
}

// M8 — Thread watch preference
type SetThreadPrefPayload struct {
	Thread string `json:"thread"`
	Level  string `json:"level"` // "watch" | "normal" | "mute"
}

type FlagPostPayload struct {
	Post   string `json:"post"`
	Reason string `json:"reason,omitempty"`
}

type ResolveReviewPayload struct {
	Review     string `json:"review"`
	Resolution string `json:"resolution"`
}

type SubscribePayload struct {
	Scopes []string `json:"scopes"`
}

type UnsubscribePayload struct {
	Scopes []string `json:"scopes"`
}
