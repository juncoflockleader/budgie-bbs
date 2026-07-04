package proto

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"strings"
	"time"
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

// MaxPostBodyLength bounds user-authored post bodies. Command handlers and
// native command-log decisions share this limit so replayed commands validate
// the same way as direct SQL-backed commands.
const MaxPostBodyLength = 20000

const MaxPostSignatureLength = 500

const MaxQuotedReplyLines = 24
const MaxQuotedReplyBytes = 2400
const MaxPostAuthorMailExcerptLength = 1200

// MaxThreadTitleLength bounds thread-like titles that become post/thread
// projection data, including thread title updates and system notices.
const MaxThreadTitleLength = 160
const MaxSystemNoticeSourceLength = 160
const MaxPostAttachments = 8
const MaxMailAttachments = 8
const MaxAttachmentFilenameLength = 160
const MaxAttachmentContentTypeLength = 120
const MaxAttachmentURLLength = 500
const MaxModerationReasonLength = 500
const MaxBoardNoteLength = 500
const MaxSlugLength = 64
const MaxContentFilterPatternLength = 120
const MaxUserRelationshipNoteLength = 160
const MaxBlessingMessageLength = 500
const MaxMailboxLength = 64
const MaxMailGroupNameLength = 80
const MaxMailGroupMembers = 200
const MaxCommandRangeItems = 100
const MaxBoardMemberTitleLength = 80
const MaxFavoriteFolderNameLength = 80
const MaxFavoriteImportFolders = 200
const MaxFavoriteImportBoards = 500
const MaxDigestPathLength = 120
const MaxAutomodPatternLength = 500
const MaxAutomodNoteLength = 1000
const DefaultContentFilterScope = "global"

const postBodyLengthValidationMessage = "body must be 20000 characters or less"
const createThreadRequiredValidationMessage = "board, title, and body are required"
const appendPostRequiredValidationMessage = "thread and body are required"
const repostPostRequiredValidationMessage = "post and board are required"
const createBoardRequiredValidationMessage = "id and name are required"
const boardOwnParentValidationMessage = "board cannot be its own parent"
const threadRequiredValidationMessage = "thread is required"
const boardRequiredValidationMessage = "board is required"
const boardAndUserRequiredValidationMessage = "board and user are required"
const folderRequiredValidationMessage = "folder is required"
const destinationBoardRequiredValidationMessage = "destination board is required"
const threadTitleRequiredValidationMessage = "title is required"
const threadTitleLengthValidationMessage = "title must be 160 characters or less"
const systemNoticeBoardValidationMessage = "notice board must be notepad, GiveupNotice, or bbsnet"
const systemNoticeSourceLengthValidationMessage = "source must be 160 characters or less"
const postAttachmentCountValidationMessage = "a post can have at most 8 attachments"
const mailAttachmentCountValidationMessage = "mail can have at most 8 attachments"
const attachmentFilenameRequiredValidationMessage = "attachment filename is required"
const attachmentFilenameLengthValidationMessage = "attachment filename must be 160 characters or less"
const attachmentContentTypeLengthValidationMessage = "attachment content type must be 120 characters or less"
const attachmentURLLengthValidationMessage = "attachment URL must be 500 characters or less"
const attachmentSizeValidationMessage = "attachment size cannot be negative"
const anonymousPostingDisabledMessage = "anonymous posting is not enabled for this board"
const moderationReasonLengthValidationMessage = "reason must be 500 characters or less"
const boardRecommendationNoteLengthValidationMessage = "recommendation note must be 500 characters or less"
const positionNegativeValidationMessage = "position cannot be negative"
const boardMembershipApplicationNoteLengthValidationMessage = "application note must be 500 characters or less"
const boardMembershipReviewNoteLengthValidationMessage = "review note must be 500 characters or less"
const boardMembershipReviewRequiredValidationMessage = "application and status are required"
const contentFilterPatternRequiredValidationMessage = "pattern is required"
const contentFilterPatternLengthValidationMessage = "pattern must be 120 characters or less"
const boardAndIDRequiredValidationMessage = "board and id are required"
const userRequiredValidationMessage = "user is required"
const sanctionKindValidationMessage = `kind must be "mute" or "ban"`
const clearSanctionKindValidationMessage = `kind must be "mute", "ban", or empty`
const userRelationshipKindValidationMessage = `kind must be "friend" or "ignore"`
const userRelationshipNoteLengthValidationMessage = "note is too long"
const blessingMessageLengthValidationMessage = "blessing message must be 500 characters or less"
const directMessagePolicyValidationMessage = `policy must be "all", "friends", or "none"`
const directMessageRecipientAndBodyValidationMessage = "to and body are required"
const mailboxRequiredValidationMessage = "mailbox is required"
const mailboxLengthValidationMessage = "mailbox is too long"
const mailboxCharactersValidationMessage = "mailbox may contain only lowercase letters, numbers, hyphens, and underscores"
const mailGroupNameRequiredValidationMessage = "name is required"
const mailGroupNameLengthValidationMessage = "mail group name is too long"
const mailGroupMemberCountValidationMessage = "mail group may contain at most 200 members"
const mailGroupRequiredValidationMessage = "group is required"
const mailRequiredValidationMessage = "mail is required"
const pollRequiredValidationMessage = "poll is required"
const postRequiredValidationMessage = "post is required"
const editPostRequiredValidationMessage = "post and body are required"
const attachPostRequiredValidationMessage = "post and filename are required"
const postFlagRequiredValidationMessage = "at least one article flag is required"
const reviewAndResolutionRequiredValidationMessage = "review and resolution are required"
const bodyRequiredValidationMessage = "body is required"
const entryRequiredValidationMessage = "entry is required"
const messageRequiredValidationMessage = "message is required"
const updateMailOperationRequiredValidationMessage = "mailbox, read, or kept is required"
const postRangeRequiredValidationMessage = "posts are required"
const postRangeLengthValidationMessage = "post range can include at most 100 items"
const postRangeEmptyIDValidationMessage = "post id cannot be empty"
const mailRangeRequiredValidationMessage = "mail is required"
const mailRangeLengthValidationMessage = "mail range can include at most 100 items"
const mailRangeEmptyIDValidationMessage = "mail id cannot be empty"
const slugIDValidationMessage = "id must be lowercase alphanumeric, hyphens, or underscores (max 64 chars)"
const contentFilterIDValidationMessage = "filter id must be lowercase alphanumeric, hyphens, or underscores"
const boardMemberTitleLengthValidationMessage = "member title must be 80 characters or less"
const boardMemberPositionNegativeValidationMessage = "member position cannot be negative"
const boardMemberApprovalModeValidationMessage = `approvalMode must be "manual" or "auto"`
const boardMemberApplicationStatusValidationMessage = `status must be "approved", "rejected", or "blacklisted"`
const favoriteFolderNameRequiredValidationMessage = "name is required"
const favoriteFolderNameLengthValidationMessage = "folder name must be 80 characters or less"
const favoriteImportFolderLimitValidationMessage = "favorite import supports at most 200 folders"
const favoriteImportBoardLimitValidationMessage = "favorite import supports at most 500 boards"
const favoriteBoardIDRequiredValidationMessage = "favorite board is missing id"
const favoriteFolderImportCycleValidationMessage = "favorite folder import contains a cycle"
const statsSnapshotDateValidationMessage = "date must be YYYY-MM-DD"
const digestKindValidationMessage = `kind must be "digest", "archive", "recommended", "pinned", or "announcement"`
const sourcePathRequiredValidationMessage = "source path is required"
const automodPatternLengthValidationMessage = "pattern must be 500 characters or less"
const automodNoteLengthValidationMessage = "note must be 1000 characters or less"

// ValidatePostBodyLength returns a validation message when body exceeds the
// shared post-body cap, or "" when the length is accepted.
func ValidatePostBodyLength(body string) string {
	if len(body) > MaxPostBodyLength {
		return postBodyLengthValidationMessage
	}
	return ""
}

// ValidateThreadTitle returns a validation message when title is blank after
// trimming or exceeds the shared title cap, or "" when it is accepted.
func ValidateThreadTitle(title string) string {
	_, msg := normalizeRequiredLengthCappedString(title, threadTitleRequiredValidationMessage, MaxThreadTitleLength, threadTitleLengthValidationMessage)
	return msg
}

func normalizeRequiredString(value, requiredMessage string) (string, string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return value, requiredMessage
	}
	return value, ""
}

func normalizeRequiredFields(requiredMessage string, fields ...*string) (msg string) {
	for _, field := range fields {
		*field = strings.TrimSpace(*field)
		if *field == "" && msg == "" {
			msg = requiredMessage
		}
	}
	return msg
}

func normalizeLengthCappedString(value string, max int, lengthMessage string) (string, string) {
	value = strings.TrimSpace(value)
	if len(value) > max {
		return value, lengthMessage
	}
	return value, ""
}

func normalizeRequiredLengthCappedString(value, requiredMessage string, max int, lengthMessage string) (string, string) {
	value, msg := normalizeRequiredString(value, requiredMessage)
	if msg != "" {
		return value, msg
	}
	if len(value) > max {
		return value, lengthMessage
	}
	return value, ""
}

// NormalizePostContentType canonicalizes article content types. Only the exact
// ansi-art token opts into ANSI rendering; all other values render as markup.
func NormalizePostContentType(contentType string) string {
	if contentType == "ansi-art" {
		return "ansi-art"
	}
	return "markup"
}

// ResolvePostAuthorIdentity returns the author identity stored on posts after
// applying anonymous-posting policy.
func ResolvePostAuthorIdentity(actorName, actorID string, anonymous bool, anonymousAllowed bool, canModerateBoard bool) (string, string, string) {
	if !anonymous {
		return actorName, actorID, ""
	}
	if !anonymousAllowed && !canModerateBoard {
		return "", "", anonymousPostingDisabledMessage
	}
	return "Anonymous", "", ""
}

// NormalizePostSignature trims a stored user signature and caps the snapshot
// length persisted on posts.
func NormalizePostSignature(signature string) string {
	signature = strings.TrimSpace(signature)
	if len(signature) > MaxPostSignatureLength {
		signature = signature[:MaxPostSignatureLength]
	}
	return signature
}

// FormatQuotedReplyPrefix returns the display-only quote block prepended to a
// directed reply when the author requests quoting.
func FormatQuotedReplyPrefix(author, body string) string {
	author = strings.TrimSpace(author)
	if author == "" {
		author = "Unknown"
	}
	body = strings.TrimSpace(body)
	if body == "" {
		body = "[empty article]"
	}
	lines := strings.Split(body, "\n")
	var b strings.Builder
	fmt.Fprintf(&b, "> %s wrote:\n", author)
	for i, line := range lines {
		if i >= MaxQuotedReplyLines || b.Len()+len(line)+8 > MaxQuotedReplyBytes {
			b.WriteString("> ...\n")
			break
		}
		line = strings.TrimRight(line, "\r")
		if line == "" {
			b.WriteString(">\n")
			continue
		}
		fmt.Fprintf(&b, "> %s\n", line)
	}
	b.WriteString("\n")
	return b.String()
}

// FormatRepostBody returns the body used for a reposted source article.
func FormatRepostBody(sourceBoard, sourceThreadTitle, sourceAuthor, sourcePostID, sourceBody string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"Reposted from %s / %s\nOriginal author: %s\nOriginal post: %s\n\n%s",
		sourceBoard,
		sourceThreadTitle,
		sourceAuthor,
		sourcePostID,
		sourceBody,
	))
}

// FormatArticleMailBackBody returns the private mail body generated when an
// article author requests mail-back replies.
func FormatArticleMailBackBody(boardID, threadTitle, originalPostID, replyPostID, replyAuthor, replyBody string) string {
	return strings.TrimSpace(fmt.Sprintf(
		"Article reply mail-back\n\nBoard: %s\nThread: %s\nOriginal post: %s\nReply post: %s\nReply author: %s\n\n%s",
		boardID,
		threadTitle,
		originalPostID,
		replyPostID,
		replyAuthor,
		replyBody,
	))
}

// FormatSystemNoticeBody returns the public generated body for a system notice.
func FormatSystemNoticeBody(board SystemNoticeBoard, title, noticeBody, source, actorName string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	fmt.Fprintf(&b, "- Notice board: %s\n", board.Name)
	fmt.Fprintf(&b, "- Actor: %s\n", actorName)
	if source != "" {
		fmt.Fprintf(&b, "- Source: %s\n", source)
	}
	b.WriteString("\n")
	b.WriteString(noticeBody)
	if !strings.HasSuffix(noticeBody, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\nGenerated public system notice.\n")
	return b.String()
}

// FormatBlessingSystemBody returns the public generated body for a blessing.
func FormatBlessingSystemBody(fromName, toName, message string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Blessing for %s\n\n", toName)
	fmt.Fprintf(&b, "- From: %s\n", fromName)
	fmt.Fprintf(&b, "- To: %s\n\n", toName)
	if strings.TrimSpace(message) == "" {
		b.WriteString("A public blessing was sent.\n")
	} else {
		b.WriteString(strings.TrimSpace(message))
		if !strings.HasSuffix(message, "\n") {
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nGenerated public blessing record.\n")
	return b.String()
}

const (
	BlessingSystemBoardID          = "Blessing"
	BlessingSystemBoardName        = "Blessing"
	BlessingSystemBoardDescription = "Generated blessing rituals and rankings"
)

// BlessingSystemPostIDs returns the generated thread/post ids used for public
// blessing mirrors.
func BlessingSystemPostIDs(blessingID string) (threadID, postID string) {
	return "blessing_thr_" + blessingID, "blessing_pst_" + blessingID
}

// BlessingSystemTitle returns the generated title for a public blessing mirror.
func BlessingSystemTitle(fromName, toName string) string {
	return "Blessing: " + fromName + " -> " + toName
}

// FormatPostAuthorMailBody returns the private mail body used when contacting
// an article author from article context.
func FormatPostAuthorMailBody(boardID, threadTitle string, postSeq int64, postID, articleAuthor, senderName, note, articleBody string) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(note))
	b.WriteString("\n\n---\n")
	b.WriteString("Sent from article reading context.\n")
	fmt.Fprintf(&b, "Board: %s\n", boardID)
	fmt.Fprintf(&b, "Thread: %s\n", threadTitle)
	fmt.Fprintf(&b, "Post: #%d (%s)\n", postSeq, postID)
	fmt.Fprintf(&b, "Article author: %s\n", articleAuthor)
	fmt.Fprintf(&b, "Mail author: %s\n\n", senderName)
	b.WriteString("Article excerpt:\n")
	b.WriteString(ArticleMailExcerpt(articleBody, MaxPostAuthorMailExcerptLength))
	return strings.TrimSpace(b.String())
}

func ArticleMailExcerpt(body string, max int) string {
	body = strings.TrimSpace(body)
	runes := []rune(body)
	if max <= 0 || len(runes) <= max {
		return body
	}
	return strings.TrimSpace(string(runes[:max])) + "..."
}

// FormatSysmailSystemBody returns the restricted generated body for admin
// mail-all broadcasts mirrored to the sysmail board.
const (
	SysmailSystemBoardID          = "sysmail"
	SysmailSystemBoardDescription = "Generated restricted sysop mail log"
)

func FormatSysmailSystemBody(mailID, subject, mailBody, actorName string, recipientCount int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Sysop mail: %s\n\n", subject)
	fmt.Fprintf(&b, "- Mail: %s\n", mailID)
	fmt.Fprintf(&b, "- From: %s\n", actorName)
	if recipientCount == 1 {
		b.WriteString("- Recipients: 1 user\n")
	} else {
		fmt.Fprintf(&b, "- Recipients: %d users\n", recipientCount)
	}
	b.WriteString("- Source: admin mail-all broadcast\n\n")
	b.WriteString(mailBody)
	if !strings.HasSuffix(mailBody, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString("\nGenerated restricted sysop mail record.\n")
	return b.String()
}

// NormalizeSyssecuritySystemTitle trims a generated syssecurity title and
// returns the default title used for generic security notices.
const (
	SyssecuritySystemBoardID          = "syssecurity"
	SyssecuritySystemBoardDescription = "Generated security and administration audit log"
)

func NormalizeSyssecuritySystemTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Security notice"
	}
	return title
}

// FormatSyssecuritySystemBody returns the restricted generated body for
// security and administration audit log posts.
func FormatSyssecuritySystemBody(title string, lines []string) string {
	title = NormalizeSyssecuritySystemTitle(title)
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", line)
	}
	b.WriteString("\nGenerated security notices omit private notes and article content.\n")
	return b.String()
}

// FormatSanctionSystemBody returns the public generated body for board-posting
// sanction mirrors.
const (
	RegistrySystemBoardID          = "Registry"
	RegistrySystemBoardDescription = "Generated board registration approvals"

	RejectRegistrySystemBoardID          = "reject_registry"
	RejectRegistrySystemBoardDescription = "Generated rejected board registrations"
)

func BoardRegistrationSystemPlan(status, applicationID string) (boardID, description, threadID, postID string, ok bool) {
	if status == "approved" {
		boardID, description = RegistrySystemBoardID, RegistrySystemBoardDescription
	} else if status == "rejected" || status == "blacklisted" {
		boardID, description = RejectRegistrySystemBoardID, RejectRegistrySystemBoardDescription
	} else {
		return "", "", "", "", false
	}
	return boardID, description, "registry_" + status + "_thr_" + applicationID, "registry_" + status + "_pst_" + applicationID, true
}

func BoardRegistrationSystemContent(status, applicationID, sourceBoardName, sourceBoardID, applicantName, reviewerName string) (string, string) {
	title := "Board registration " + status + " " + applicationID
	return title, fmt.Sprintf("# %s\n\n- Application: %s\n- Status: %s\n- Board: %s (%s)\n- Applicant: %s\n- Reviewer: %s\n\nApplication and review notes are kept in the board member manager queue.\n",
		title, applicationID, status, sourceBoardName, sourceBoardID, applicantName, reviewerName)
}

const (
	DenyPostSystemBoardID          = "denypost"
	DenyPostSystemBoardDescription = "Generated board posting deny records"

	UndenyPostSystemBoardID          = "undenypost"
	UndenyPostSystemBoardDescription = "Generated board posting restore records"
)

func FormatSanctionSystemBody(action, targetName, sourceBoardName, sourceBoardID, kind, actorName, reason string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", action)
	fmt.Fprintf(&b, "- Action: %s\n", strings.ToLower(action))
	fmt.Fprintf(&b, "- User: %s\n", targetName)
	fmt.Fprintf(&b, "- Board: %s (%s)\n", sourceBoardName, sourceBoardID)
	if strings.TrimSpace(kind) == "" {
		fmt.Fprintf(&b, "- Kind: all\n")
	} else {
		fmt.Fprintf(&b, "- Kind: %s\n", strings.TrimSpace(kind))
	}
	fmt.Fprintf(&b, "- Actor: %s\n", actorName)
	if strings.TrimSpace(reason) != "" {
		fmt.Fprintf(&b, "- Reason: %s\n", strings.TrimSpace(reason))
	}
	b.WriteString("\nGenerated public board-posting sanction record. Private moderation notes and article bodies are not mirrored.\n")
	return b.String()
}

// FormatForwardMailBody returns the body for a forwarded private mail message.
func FormatForwardMailBody(note, fromName string, toNames []string, subject string, attachmentNames []string, body string) string {
	return formatPrivateMailBody(note, "----- Forwarded mail -----", fromName, toNames, subject, attachmentNames, body)
}

// FormatMailBoardBody returns the body used when posting a private mail message
// to a board or existing thread.
func FormatMailBoardBody(note, fromName string, toNames []string, subject string, attachmentNames []string, body string) string {
	return formatPrivateMailBody(note, "Posted from private mail.", fromName, toNames, subject, attachmentNames, body)
}

func formatPrivateMailBody(note, heading, fromName string, toNames []string, subject string, attachmentNames []string, body string) string {
	var b strings.Builder
	if note = strings.TrimSpace(note); note != "" {
		b.WriteString(note)
		b.WriteString("\n\n")
	}
	b.WriteString(heading)
	b.WriteByte('\n')
	fmt.Fprintf(&b, "From: %s\n", fromName)
	if len(toNames) > 0 {
		fmt.Fprintf(&b, "To: %s\n", strings.Join(toNames, ", "))
	}
	fmt.Fprintf(&b, "Subject: %s\n", subject)
	if len(attachmentNames) > 0 {
		fmt.Fprintf(&b, "Attachments: %s\n", strings.Join(attachmentNames, ", "))
	}
	b.WriteString("\n")
	b.WriteString(strings.TrimSpace(body))
	return b.String()
}

// FormatContentFilterReviewBody returns the generated public review body for a
// content-filter-triggered moderation review.
func FormatContentFilterReviewBody(title, reviewID, filterID, filterScope, boardID, threadID, postID, publicAuthor string) string {
	return fmt.Sprintf("# %s\n\n- Review: %s\n- Status: opened\n- Filter: %s\n- Filter scope: %s\n- Board: %s\n- Thread: %s\n- Post: %s\n- Public author: %s\n\nSensitive filter pattern and article body are kept out of this generated record.\n",
		title,
		reviewID,
		filterID,
		filterScope,
		boardID,
		threadID,
		postID,
		publicAuthor,
	)
}

const (
	ContentFilterSystemBoardID          = "Filter"
	ContentFilterSystemBoardName        = "Filter"
	ContentFilterSystemBoardDescription = "Generated content filter review log"
)

// ContentFilterReviewPostIDs returns the generated thread/post ids used for
// public content-filter review mirrors.
func ContentFilterReviewPostIDs(reviewID string) (threadID, postID string) {
	return "filter_thr_" + reviewID, "filter_pst_" + reviewID
}

// ContentFilterReviewTitle returns the generated title for a content-filter
// review mirror post.
func ContentFilterReviewTitle(reviewID string) string {
	return "Content filter review " + reviewID
}

const (
	ModerationSystemBoardID          = "0moderation"
	ModerationSystemBoardName        = "0Moderation"
	ModerationSystemBoardDescription = "Generated moderation audit log"
	ModerationLogFlag                = "flag"
	ModerationLogResolve             = "resolve"
)

// ModerationSystemPostIDs returns the generated thread/post ids used for the
// public moderation audit mirror.
func ModerationSystemPostIDs(action, reviewID string) (threadID, postID string) {
	return "mod_" + action + "_thr_" + reviewID, "mod_" + action + "_pst_" + reviewID
}

// ModerationSystemTitle returns the generated title for a moderation audit
// mirror post.
func ModerationSystemTitle(action, reviewID string) string {
	if action == ModerationLogResolve {
		return "Moderation resolved " + reviewID
	}
	return "Moderation flag " + reviewID
}

// FormatModerationSystemBody returns the generated public moderation audit
// mirror body.
func FormatModerationSystemBody(action, reviewID, boardID, threadID, postID, actorName string) string {
	statusLine := "opened"
	if action == ModerationLogResolve {
		statusLine = "resolved"
	}
	title := ModerationSystemTitle(action, reviewID)
	return fmt.Sprintf("# %s\n\n- Review: %s\n- Status: %s\n- Board: %s\n- Thread: %s\n- Post: %s\n- Actor: %s\n\nSensitive report and resolution text is kept in the moderator review queue.\n",
		title, reviewID, statusLine, boardID, threadID, postID, actorName)
}

// PollResultOption is a display-ready option row for a generated poll result.
type PollResultOption struct {
	Text  string
	Votes int
}

// FormatPollResultBody returns the generated public result body for a poll.
func FormatPollResultBody(sourceThreadTitle, sourceBoard, question string, options []PollResultOption) string {
	total := 0
	for _, option := range options {
		total += option.Votes
	}
	question = strings.TrimSpace(question)
	if question == "" {
		question = "Untitled poll"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Poll result: %s\n\n", question)
	fmt.Fprintf(&b, "- Source thread: %s\n", sourceThreadTitle)
	fmt.Fprintf(&b, "- Source board: %s\n", sourceBoard)
	fmt.Fprintf(&b, "- Total votes: %d\n\n", total)
	for i, option := range options {
		percent := 0
		if total > 0 {
			percent = option.Votes * 100 / total
		}
		fmt.Fprintf(&b, "%d. %s: %d vote(s), %d%%\n", i+1, option.Text, option.Votes, percent)
	}
	b.WriteString("\nGenerated public poll result.\n")
	return b.String()
}

const (
	VoteSystemBoardID          = "vote"
	VoteSystemBoardName        = "vote"
	VoteSystemBoardDescription = "Generated poll results"
)

// PollResultPostIDs returns the generated thread/post ids used for public poll
// result mirrors.
func PollResultPostIDs(pollID string) (threadID, postID string) {
	return "vote_poll_" + pollID, "vote_poll_post_" + pollID
}

// PollResultTitle returns the generated title for a poll result mirror post.
func PollResultTitle(question string) string {
	question = strings.TrimSpace(question)
	if question == "" {
		return "Poll result"
	}
	return "Poll result: " + question
}

// NormalizeCreateThreadPayload trims routing/title fields and validates the
// required thread creation fields plus the shared post-body cap.
func NormalizeCreateThreadPayload(p CreateThreadPayload) (CreateThreadPayload, string) {
	if msg := normalizeRequiredFields(createThreadRequiredValidationMessage, &p.Board, &p.Title); msg != "" || p.Body == "" {
		return p, createThreadRequiredValidationMessage
	}
	if msg := ValidatePostBodyLength(p.Body); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeAppendPostPayload trims post routing fields and validates the
// required reply fields plus the shared post-body cap.
func NormalizeAppendPostPayload(p AppendPostPayload) (AppendPostPayload, string) {
	p.ReplyTo = strings.TrimSpace(p.ReplyTo)
	if msg := normalizeRequiredFields(appendPostRequiredValidationMessage, &p.Thread); msg != "" || p.Body == "" {
		return p, appendPostRequiredValidationMessage
	}
	if msg := ValidatePostBodyLength(p.Body); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizePostBoardMailPayload trims mail-to-board routing/content fields and
// validates that the posted body is present. Board validation stays with
// callers because a thread target can supply the board.
func NormalizePostBoardMailPayload(p PostBoardMailPayload) (PostBoardMailPayload, string) {
	p.Board = strings.TrimSpace(p.Board)
	p.Thread = strings.TrimSpace(p.Thread)
	p.Subject = strings.TrimSpace(p.Subject)
	p.Body = strings.TrimSpace(p.Body)
	p.ContentType = strings.TrimSpace(p.ContentType)
	if p.Body == "" {
		return p, bodyRequiredValidationMessage
	}
	return p, ""
}

// NormalizeRepostPostPayload trims repost routing fields and validates the
// source post plus destination board requirement.
func NormalizeRepostPostPayload(p RepostPostPayload) (RepostPostPayload, string) {
	p.Title = strings.TrimSpace(p.Title)
	if msg := normalizeRequiredFields(repostPostRequiredValidationMessage, &p.Post, &p.Board); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeEditPostPayload trims the target post id and validates the required
// edit-post fields while preserving the raw replacement body.
func NormalizeEditPostPayload(p EditPostPayload) (EditPostPayload, string) {
	if msg := normalizeRequiredFields(editPostRequiredValidationMessage, &p.Post); msg != "" || p.Body == "" {
		return p, editPostRequiredValidationMessage
	}
	return p, ""
}

// NormalizeSetPostFlagPayload trims the target post and validates that at least
// one article flag is being changed.
func NormalizeSetPostFlagPayload(p SetPostFlagPayload) (SetPostFlagPayload, string) {
	p.Post = strings.TrimSpace(p.Post)
	if p.Post == "" {
		return p, postRequiredValidationMessage
	}
	if p.Marked == nil && p.Recommended == nil && p.NoReply == nil && p.TeX == nil && p.MailBack == nil {
		return p, postFlagRequiredValidationMessage
	}
	return p, ""
}

// NormalizeFlagPostPayload trims the target post for moderation flag commands.
// Reason text is left untouched so reporter-authored text is preserved.
func NormalizeFlagPostPayload(p FlagPostPayload) (FlagPostPayload, string) {
	msg := normalizeRequiredFields(postRequiredValidationMessage, &p.Post)
	return p, msg
}

// NormalizeResolveReviewPayload trims the moderation review target and
// resolution text used to close the review.
func NormalizeResolveReviewPayload(p ResolveReviewPayload) (ResolveReviewPayload, string) {
	if msg := normalizeRequiredFields(reviewAndResolutionRequiredValidationMessage, &p.Review, &p.Resolution); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizePublishPollResultPayload trims the target poll id and validates
// that it is present.
func NormalizePublishPollResultPayload(p PublishPollResultPayload) (PublishPollResultPayload, string) {
	msg := normalizeRequiredFields(pollRequiredValidationMessage, &p.Poll)
	return p, msg
}

// NormalizeRedactPostPayload trims the target post id and validates that it is
// present. The reason text remains caller-controlled content.
func NormalizeRedactPostPayload(p RedactPostPayload) (RedactPostPayload, string) {
	msg := normalizeRequiredFields(postRequiredValidationMessage, &p.Post)
	return p, msg
}

// NormalizeRestorePostPayload trims the target post id and validates that it is
// present.
func NormalizeRestorePostPayload(p RestorePostPayload) (RestorePostPayload, string) {
	msg := normalizeRequiredFields(postRequiredValidationMessage, &p.Post)
	return p, msg
}

// NormalizePurgePostPayload trims the target post id and validates that it is
// present. The purge reason remains caller-controlled content.
func NormalizePurgePostPayload(p PurgePostPayload) (PurgePostPayload, string) {
	msg := normalizeRequiredFields(postRequiredValidationMessage, &p.Post)
	return p, msg
}

// NormalizeRedactPostRangePayload trims the target board and normalizes the
// post-id range while preserving the moderator reason text.
func NormalizeRedactPostRangePayload(p RedactPostRangePayload) (RedactPostRangePayload, string) {
	p.Board = strings.TrimSpace(p.Board)
	if p.Board == "" {
		return p, boardRequiredValidationMessage
	}
	posts, msg := NormalizePostRangeIDs(p.Posts)
	if msg != "" {
		return p, msg
	}
	p.Posts = posts
	return p, ""
}

// NormalizeRestorePostRangePayload trims the target board and normalizes the
// post-id range for restore operations.
func NormalizeRestorePostRangePayload(p RestorePostRangePayload) (RestorePostRangePayload, string) {
	p.Board = strings.TrimSpace(p.Board)
	if p.Board == "" {
		return p, boardRequiredValidationMessage
	}
	posts, msg := NormalizePostRangeIDs(p.Posts)
	if msg != "" {
		return p, msg
	}
	p.Posts = posts
	return p, ""
}

// NormalizeClearBoardJunkPayload trims the target board. Posts remain
// caller-resolved because an empty list means "all junk" for this command.
func NormalizeClearBoardJunkPayload(p ClearBoardJunkPayload) (ClearBoardJunkPayload, string) {
	msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board)
	return p, msg
}

// NormalizeSetThreadTitlePayload trims the target thread and title, then
// validates the shared thread-title command rules.
func NormalizeSetThreadTitlePayload(p SetThreadTitlePayload) (SetThreadTitlePayload, string) {
	p.Thread = strings.TrimSpace(p.Thread)
	p.Title = strings.TrimSpace(p.Title)
	if p.Thread == "" {
		return p, threadRequiredValidationMessage
	}
	if msg := ValidateThreadTitle(p.Title); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeLockThreadPayload trims the target thread and validates that it is
// present.
func NormalizeLockThreadPayload(p LockThreadPayload) (LockThreadPayload, string) {
	msg := normalizeRequiredFields(threadRequiredValidationMessage, &p.Thread)
	return p, msg
}

// NormalizeMoveThreadPayload trims the source thread and destination board,
// then validates the shared move-thread command requirements.
func NormalizeMoveThreadPayload(p MoveThreadPayload) (MoveThreadPayload, string) {
	p.Thread = strings.TrimSpace(p.Thread)
	p.ToBoard = strings.TrimSpace(p.ToBoard)
	if p.Thread == "" {
		return p, threadRequiredValidationMessage
	}
	if p.ToBoard == "" {
		return p, destinationBoardRequiredValidationMessage
	}
	return p, ""
}

// ValidateSystemNoticeSource returns a validation message when source exceeds
// the shared source cap, or "" when it is accepted.
func ValidateSystemNoticeSource(source string) string {
	_, msg := normalizeLengthCappedString(source, MaxSystemNoticeSourceLength, systemNoticeSourceLengthValidationMessage)
	return msg
}

// SystemNoticeBoard is a normalized generated public notice board descriptor.
type SystemNoticeBoard struct {
	ID          string
	Name        string
	Description string
}

// NormalizeSystemNoticeBoard canonicalizes the generated public notice board
// aliases shared by direct handlers and native command-log decisions.
func NormalizeSystemNoticeBoard(raw string) (SystemNoticeBoard, string) {
	board := strings.TrimSpace(raw)
	if board == "" || strings.EqualFold(board, "notepad") {
		return SystemNoticeBoard{ID: "notepad", Name: "notepad", Description: "Generated public system notes"}, ""
	}
	switch strings.ToLower(board) {
	case "giveupnotice", "giveup_notice":
		return SystemNoticeBoard{ID: "GiveupNotice", Name: "GiveupNotice", Description: "Generated give-up-net notices"}, ""
	case "bbsnet":
		return SystemNoticeBoard{ID: "bbsnet", Name: "bbsnet", Description: "Generated site-hop and network notices"}, ""
	default:
		return SystemNoticeBoard{}, systemNoticeBoardValidationMessage
	}
}

// NormalizePublishSystemNoticePayload trims and validates the generated public
// notice payload, returning the canonical destination board descriptor.
func NormalizePublishSystemNoticePayload(p PublishSystemNoticePayload) (PublishSystemNoticePayload, SystemNoticeBoard, string) {
	p.Board = strings.TrimSpace(p.Board)
	board, msg := NormalizeSystemNoticeBoard(p.Board)
	if msg != "" {
		return p, SystemNoticeBoard{}, msg
	}
	p.Title = strings.TrimSpace(p.Title)
	if msg := ValidateThreadTitle(p.Title); msg != "" {
		return p, SystemNoticeBoard{}, msg
	}
	p.Body = strings.TrimSpace(p.Body)
	if p.Body == "" {
		return p, SystemNoticeBoard{}, bodyRequiredValidationMessage
	}
	if msg := ValidatePostBodyLength(p.Body); msg != "" {
		return p, SystemNoticeBoard{}, msg
	}
	p.Source = strings.TrimSpace(p.Source)
	if msg := ValidateSystemNoticeSource(p.Source); msg != "" {
		return p, SystemNoticeBoard{}, msg
	}
	return p, board, ""
}

// ValidatePostAttachmentCount returns a validation message when count exceeds
// the shared post attachment cap, or "" when it is accepted.
func ValidatePostAttachmentCount(count int) string {
	if count > MaxPostAttachments {
		return postAttachmentCountValidationMessage
	}
	return ""
}

// ValidateMailAttachmentCount returns a validation message when count exceeds
// the shared mail attachment cap, or "" when it is accepted.
func ValidateMailAttachmentCount(count int) string {
	if count > MaxMailAttachments {
		return mailAttachmentCountValidationMessage
	}
	return ""
}

// ValidSlug reports whether s is a lowercase alphanumeric, hyphen, or
// underscore identifier within the shared command slug length cap.
func ValidSlug(s string) bool {
	if len(s) == 0 || len(s) > MaxSlugLength {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// ValidateSlugID returns a validation message when id is not a shared command
// slug identifier, or "" when it is accepted.
func ValidateSlugID(id string) string {
	if !ValidSlug(id) {
		return slugIDValidationMessage
	}
	return ""
}

// ValidateContentFilterID returns a validation message when id is not a shared
// content-filter slug identifier, or "" when it is accepted.
func ValidateContentFilterID(id string) string {
	if !ValidSlug(id) {
		return contentFilterIDValidationMessage
	}
	return ""
}

// NormalizeCreateBoardPayload trims and validates pure create-board payload
// fields. Parent existence and conflict checks stay with storage-backed callers.
func NormalizeCreateBoardPayload(p CreateBoardPayload) (CreateBoardPayload, string) {
	p.ParentID = strings.TrimSpace(p.ParentID)
	if msg := normalizeRequiredFields(createBoardRequiredValidationMessage, &p.ID, &p.Name); msg != "" {
		return p, msg
	}
	if msg := ValidateSlugID(p.ID); msg != "" {
		return p, msg
	}
	if p.ParentID == p.ID {
		return p, boardOwnParentValidationMessage
	}
	if p.Position != nil && *p.Position < 0 {
		return p, positionNegativeValidationMessage
	}
	return p, ""
}

// NormalizeContentFilterPayload trims fields and fills the default global
// scope used by setContentFilter commands.
func NormalizeContentFilterPayload(p SetContentFilterPayload) SetContentFilterPayload {
	p.ID = strings.TrimSpace(p.ID)
	p.Pattern = strings.TrimSpace(p.Pattern)
	p.Scope = strings.TrimSpace(p.Scope)
	if p.Scope == "" {
		p.Scope = DefaultContentFilterScope
	}
	return p
}

// ValidateContentFilterPattern returns a validation message when pattern is
// blank or exceeds the shared content-filter length cap.
func ValidateContentFilterPattern(pattern string) string {
	_, msg := normalizeRequiredLengthCappedString(pattern, contentFilterPatternRequiredValidationMessage, MaxContentFilterPatternLength, contentFilterPatternLengthValidationMessage)
	return msg
}

// NormalizeUserRelationshipKind canonicalizes user relationship aliases used
// by setUserRelationship commands.
func NormalizeUserRelationshipKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "friend", "friends", "follow", "following":
		return "friend"
	case "ignore", "ignored", "badlist":
		return "ignore"
	default:
		return ""
	}
}

// NormalizeUserRelationshipNote trims a relationship note and returns a
// validation message when it exceeds the shared relationship note cap.
func NormalizeUserRelationshipNote(note string) (string, string) {
	return normalizeLengthCappedString(note, MaxUserRelationshipNoteLength, userRelationshipNoteLengthValidationMessage)
}

// NormalizeSetUserRelationshipPayload trims the target user, canonicalizes the
// relationship kind, and validates the shared relationship note cap.
func NormalizeSetUserRelationshipPayload(p SetUserRelationshipPayload) (SetUserRelationshipPayload, string) {
	if msg := normalizeRequiredFields(userRequiredValidationMessage, &p.User); msg != "" {
		return p, msg
	}
	p.Kind = NormalizeUserRelationshipKind(p.Kind)
	if p.Kind == "" {
		return p, userRelationshipKindValidationMessage
	}
	var msg string
	p.Note, msg = NormalizeUserRelationshipNote(p.Note)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeSetLoginWatchPayload trims the target user for login-watch
// commands and validates that it is present.
func NormalizeSetLoginWatchPayload(p SetLoginWatchPayload) (SetLoginWatchPayload, string) {
	msg := normalizeRequiredFields(userRequiredValidationMessage, &p.User)
	return p, msg
}

// HiddenPresenceStatus reports whether a user presence status should be hidden
// from public online lists.
func HiddenPresenceStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "offline" || status == "invisible"
}

// CloakedPresenceStatus reports whether a user presence status is a privileged
// cloak status.
func CloakedPresenceStatus(status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	return status == "cloak" || status == "cloaked"
}

// TypingPresenceStatus reports whether a user presence status is the transient
// typing state, which is broadcast but not persisted.
func TypingPresenceStatus(status string) bool {
	return strings.EqualFold(strings.TrimSpace(status), "typing")
}

// VisiblePresenceStatus reports whether a user presence status counts as
// visible/online for user presence and login-watch decisions.
func VisiblePresenceStatus(status string) bool {
	return strings.TrimSpace(status) != "" && !HiddenPresenceStatus(status) && !CloakedPresenceStatus(status)
}

// NormalizeBlessingMessage trims a blessing message and returns a validation
// message when it exceeds the shared blessing message cap.
func NormalizeBlessingMessage(message string) (string, string) {
	return normalizeLengthCappedString(message, MaxBlessingMessageLength, blessingMessageLengthValidationMessage)
}

// NormalizeBlessUserPayload trims the target user and blessing message, and
// validates their shared command-level constraints.
func NormalizeBlessUserPayload(p BlessUserPayload) (BlessUserPayload, string) {
	if msg := normalizeRequiredFields(userRequiredValidationMessage, &p.User); msg != "" {
		return p, msg
	}
	var msg string
	p.Message, msg = NormalizeBlessingMessage(p.Message)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeSetBoardFavoritePayload trims board/folder identifiers and validates
// that the target board is present.
func NormalizeSetBoardFavoritePayload(p SetBoardFavoritePayload) (SetBoardFavoritePayload, string) {
	p.FolderID = strings.TrimSpace(p.FolderID)
	if msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeSetBoardZapPayload trims the target board and validates that it is
// present.
func NormalizeSetBoardZapPayload(p SetBoardZapPayload) (SetBoardZapPayload, string) {
	msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board)
	return p, msg
}

// NormalizeSetBoardSettingsPayload trims the target board and validates that
// it is present. Individual settings keep their nil/explicit values unchanged.
func NormalizeSetBoardSettingsPayload(p SetBoardSettingsPayload) (SetBoardSettingsPayload, string) {
	msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board)
	return p, msg
}

// NormalizeBoardGuestAccess canonicalizes a board guest-access value to the
// stored/event representation: "" (default), "hidden", or "public".
func NormalizeBoardGuestAccess(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "hidden":
		return "hidden"
	case "public":
		return "public"
	default:
		return ""
	}
}

// BoardSettingsAuditLines returns display-ready audit lines for explicitly set
// board settings fields.
func BoardSettingsAuditLines(p SetBoardSettingsPayload) []string {
	fields := []struct {
		name  string
		value *bool
	}{
		{"anonymousAllowed", p.AnonymousAllowed},
		{"readOnly", p.ReadOnly},
		{"noReply", p.NoReply},
		{"attachmentsAllowed", p.AttachmentsAllowed},
		{"mailInAllowed", p.MailInAllowed},
		{"relayEnabled", p.RelayEnabled},
		{"memberReadMode", p.MemberReadMode},
		{"memberPostMode", p.MemberPostMode},
		{"statsExcluded", p.StatsExcluded},
		{"zapAllowed", p.ZapAllowed},
	}
	out := []string{}
	for _, field := range fields {
		if field.value == nil {
			continue
		}
		out = append(out, fmt.Sprintf("%s: %t", field.name, *field.value))
	}
	if p.GuestAccess != nil {
		v := NormalizeBoardGuestAccess(*p.GuestAccess)
		if v == "" {
			v = "default"
		}
		out = append(out, "guestAccess: "+v)
	}
	return out
}

// NormalizeCreateFavoriteFolderPayload trims folder identifiers and validates
// the required favorite-folder name.
func NormalizeCreateFavoriteFolderPayload(p CreateFavoriteFolderPayload) (CreateFavoriteFolderPayload, string) {
	p.ParentID = strings.TrimSpace(p.ParentID)
	var msg string
	p.Name, msg = NormalizeFavoriteFolderName(p.Name, true)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeUpdateFavoriteFolderPayload trims folder identifiers and validates
// the optional favorite-folder name.
func NormalizeUpdateFavoriteFolderPayload(p UpdateFavoriteFolderPayload) (UpdateFavoriteFolderPayload, string) {
	p.Folder = strings.TrimSpace(p.Folder)
	if p.Folder == "" {
		return p, folderRequiredValidationMessage
	}
	var msg string
	p.Name, msg = NormalizeFavoriteFolderName(p.Name, false)
	if msg != "" {
		return p, msg
	}
	if p.ParentID != nil {
		parentID := strings.TrimSpace(*p.ParentID)
		p.ParentID = &parentID
	}
	return p, ""
}

// NormalizeDeleteFavoriteFolderPayload trims the target folder and validates
// that it is present.
func NormalizeDeleteFavoriteFolderPayload(p DeleteFavoriteFolderPayload) (DeleteFavoriteFolderPayload, string) {
	msg := normalizeRequiredFields(folderRequiredValidationMessage, &p.Folder)
	return p, msg
}

// NormalizeMoveBoardFavoritePayload trims board/folder identifiers and
// validates that the target board is present.
func NormalizeMoveBoardFavoritePayload(p MoveBoardFavoritePayload) (MoveBoardFavoritePayload, string) {
	p.FolderID = strings.TrimSpace(p.FolderID)
	if msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeImportFavoriteTreePayload trims and validates the command-level
// favorite-tree import structure. Board existence stays with storage-backed
// callers.
func NormalizeImportFavoriteTreePayload(p ImportFavoriteTreePayload) (ImportFavoriteTreePayload, string) {
	if len(p.Folders) > MaxFavoriteImportFolders {
		return p, favoriteImportFolderLimitValidationMessage
	}
	if len(p.Boards) > MaxFavoriteImportBoards {
		return p, favoriteImportBoardLimitValidationMessage
	}
	sourceFolderIDs := map[string]bool{}
	for i := range p.Folders {
		folder := &p.Folders[i]
		folder.ID = strings.TrimSpace(folder.ID)
		folder.ParentID = strings.TrimSpace(folder.ParentID)
		folder.Name = strings.TrimSpace(folder.Name)
		if folder.ID == "" {
			return p, fmt.Sprintf("folder %d is missing id", i+1)
		}
		if sourceFolderIDs[folder.ID] {
			return p, fmt.Sprintf("duplicate folder id %q", folder.ID)
		}
		if folder.Name == "" {
			return p, fmt.Sprintf("folder %q is missing name", folder.ID)
		}
		if len(folder.Name) > MaxFavoriteFolderNameLength {
			return p, fmt.Sprintf("folder %q name must be 80 characters or less", folder.ID)
		}
		sourceFolderIDs[folder.ID] = true
	}
	for _, folder := range p.Folders {
		if folder.ParentID != "" && !sourceFolderIDs[folder.ParentID] {
			return p, fmt.Sprintf("folder %q references missing parent %q", folder.ID, folder.ParentID)
		}
	}
	ordered := map[string]bool{}
	remaining := append([]ImportFavoriteFolderPayload(nil), p.Folders...)
	for len(remaining) > 0 {
		progressed := false
		next := remaining[:0]
		for _, folder := range remaining {
			if folder.ParentID != "" && !ordered[folder.ParentID] {
				next = append(next, folder)
				continue
			}
			ordered[folder.ID] = true
			progressed = true
		}
		if !progressed {
			return p, favoriteFolderImportCycleValidationMessage
		}
		remaining = next
	}
	for i := range p.Boards {
		board := &p.Boards[i]
		board.ID = strings.TrimSpace(board.ID)
		board.FolderID = strings.TrimSpace(board.FolderID)
		if board.ID == "" {
			return p, favoriteBoardIDRequiredValidationMessage
		}
		if board.FolderID != "" && !sourceFolderIDs[board.FolderID] {
			return p, fmt.Sprintf("board %q references missing folder %q", board.ID, board.FolderID)
		}
	}
	return p, ""
}

// NormalizeDirectMessagePolicy canonicalizes direct-message visibility aliases
// and returns a validation message when the policy is not supported.
func NormalizeDirectMessagePolicy(policy string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "", "all", "everyone":
		return "all", ""
	case "friend", "friends", "friends-only", "friend_only":
		return "friends", ""
	case "none", "off", "disabled", "block":
		return "none", ""
	default:
		return "", directMessagePolicyValidationMessage
	}
}

// NormalizeSendDirectMessagePayload trims direct-message recipient and body,
// returning a validation message when either required field is blank.
func NormalizeSendDirectMessagePayload(p SendDirectMessagePayload) (SendDirectMessagePayload, string) {
	if msg := normalizeRequiredFields(directMessageRecipientAndBodyValidationMessage, &p.To, &p.Body); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeDirectMessageTarget trims a direct-message id and validates that it
// is present.
func NormalizeDirectMessageTarget(message string) (string, string) {
	return normalizeRequiredString(message, messageRequiredValidationMessage)
}

// DirectConversationID returns the canonical two-user direct-message
// conversation id, independent of sender/recipient order.
func DirectConversationID(a, b string) string {
	if b < a {
		return b + ":" + a
	}
	return a + ":" + b
}

// DirectMessageEventScopes returns the account scopes shared by direct-message
// send/read/delete events.
func DirectMessageEventScopes(fromUserID, toUserID string) []string {
	scopes := []string{"account:" + fromUserID}
	if toUserID != fromUserID {
		scopes = append(scopes, "account:"+toUserID)
	}
	return scopes
}

// NormalizeMailbox canonicalizes mailbox aliases and validates mailbox names.
func NormalizeMailbox(mailbox string) (string, string) {
	mailbox = strings.TrimSpace(strings.ToLower(mailbox))
	switch mailbox {
	case "deleted", "delete":
		mailbox = "trash"
	}
	var msg string
	mailbox, msg = normalizeRequiredLengthCappedString(mailbox, mailboxRequiredValidationMessage, MaxMailboxLength, mailboxLengthValidationMessage)
	if msg != "" {
		return "", msg
	}
	for _, r := range mailbox {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return "", mailboxCharactersValidationMessage
	}
	return mailbox, ""
}

// NormalizeMailGroupPayload trims mail-group identifiers and validates the
// shared group name and member-count caps.
func NormalizeMailGroupPayload(p SetMailGroupPayload) (SetMailGroupPayload, string) {
	p.Group = strings.TrimSpace(p.Group)
	var msg string
	p.Name, msg = normalizeRequiredLengthCappedString(p.Name, mailGroupNameRequiredValidationMessage, MaxMailGroupNameLength, mailGroupNameLengthValidationMessage)
	if msg != "" {
		return p, msg
	}
	if len(p.Members) > MaxMailGroupMembers {
		return p, mailGroupMemberCountValidationMessage
	}
	return p, ""
}

// NormalizeDeleteMailGroupPayload trims the target mail-group reference and
// validates that it is present.
func NormalizeDeleteMailGroupPayload(p DeleteMailGroupPayload) (DeleteMailGroupPayload, string) {
	msg := normalizeRequiredFields(mailGroupRequiredValidationMessage, &p.Group)
	return p, msg
}

// IsFriendMailGroupRef reports whether a mail-group reference means the
// sender's friend list.
func IsFriendMailGroupRef(ref string) bool {
	switch strings.ToLower(strings.TrimSpace(ref)) {
	case "friend", "friends", "@friends", "friend-list", "friends-list":
		return true
	default:
		return false
	}
}

// NormalizeUpdateMailPayload trims the target mail id, normalizes mailbox
// aliases, and validates that the command has at least one mutation.
func NormalizeUpdateMailPayload(p UpdateMailPayload) (UpdateMailPayload, string) {
	if msg := normalizeRequiredFields(mailRequiredValidationMessage, &p.Mail); msg != "" {
		return p, msg
	}
	if p.Mailbox != nil {
		mailbox, msg := NormalizeMailbox(*p.Mailbox)
		if msg != "" {
			return p, msg
		}
		p.Mailbox = &mailbox
	}
	if p.Mailbox == nil && p.Read == nil && p.Kept == nil {
		return p, updateMailOperationRequiredValidationMessage
	}
	return p, ""
}

// NormalizeDeleteMailPayload trims the target mail id and validates that it is
// present before delete/trash operations.
func NormalizeDeleteMailPayload(p DeleteMailPayload) (DeleteMailPayload, string) {
	msg := normalizeRequiredFields(mailRequiredValidationMessage, &p.Mail)
	return p, msg
}

// NormalizeForwardMailPayload trims forward-mail identifiers and free-text
// fields, and validates that the source mail id is present.
func NormalizeForwardMailPayload(p ForwardMailPayload) (ForwardMailPayload, string) {
	p.Subject = strings.TrimSpace(p.Subject)
	p.Note = strings.TrimSpace(p.Note)
	if msg := normalizeRequiredFields(mailRequiredValidationMessage, &p.Mail); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeForwardMailSubject returns the explicit subject when supplied, or a
// forwarded subject derived from the source mail.
func NormalizeForwardMailSubject(subject, sourceSubject string) string {
	subject = strings.TrimSpace(subject)
	if subject != "" {
		return subject
	}
	sourceSubject = strings.TrimSpace(sourceSubject)
	if sourceSubject == "" {
		return "Fwd: (no subject)"
	}
	if strings.HasPrefix(strings.ToLower(sourceSubject), "fwd:") {
		return sourceSubject
	}
	return "Fwd: " + sourceSubject
}

// NormalizePostMailToBoardPayload trims identifiers and text fields used when
// posting private mail into a board thread. It returns separate validation
// messages so callers can preserve source-mail lookup ordering.
func NormalizePostMailToBoardPayload(p PostMailToBoardPayload) (PostMailToBoardPayload, string, string) {
	p.Mail = strings.TrimSpace(p.Mail)
	p.Board = strings.TrimSpace(p.Board)
	p.Thread = strings.TrimSpace(p.Thread)
	p.Subject = strings.TrimSpace(p.Subject)
	p.Note = strings.TrimSpace(p.Note)
	mailMsg := ""
	if p.Mail == "" {
		mailMsg = mailRequiredValidationMessage
	}
	targetMsg := ""
	if p.Board == "" && p.Thread == "" {
		targetMsg = boardRequiredValidationMessage
	}
	return p, mailMsg, targetMsg
}

// PostMailToBoardTitle returns the thread title for a posted private mail,
// falling back to the source mail subject and finally a stable placeholder.
func PostMailToBoardTitle(subject, sourceSubject string) string {
	subject = strings.TrimSpace(subject)
	if subject != "" {
		return subject
	}
	sourceSubject = strings.TrimSpace(sourceSubject)
	if sourceSubject != "" {
		return sourceSubject
	}
	return "(no subject)"
}

// NormalizeMailPostAuthorPayload trims fields used to send private mail from
// article-reading context and validates the required post/body fields.
func NormalizeMailPostAuthorPayload(p MailPostAuthorPayload) (MailPostAuthorPayload, string) {
	p.Post = strings.TrimSpace(p.Post)
	p.Subject = strings.TrimSpace(p.Subject)
	p.Body = strings.TrimSpace(p.Body)
	if p.Post == "" {
		return p, postRequiredValidationMessage
	}
	if p.Body == "" {
		return p, bodyRequiredValidationMessage
	}
	return p, ""
}

// MailPostAuthorSubject returns the explicit mail subject or the default reply
// subject derived from the source thread title.
func MailPostAuthorSubject(subject, threadTitle string) string {
	subject = strings.TrimSpace(subject)
	if subject != "" {
		return subject
	}
	return "Re: " + threadTitle
}

// NormalizeSendDigestEntryMailPayload trims digest-entry mail identifiers and
// text fields, and validates that the source digest entry id is present.
func NormalizeSendDigestEntryMailPayload(p SendDigestEntryMailPayload) (SendDigestEntryMailPayload, string) {
	p.Subject = strings.TrimSpace(p.Subject)
	p.Note = strings.TrimSpace(p.Note)
	if msg := normalizeRequiredFields(entryRequiredValidationMessage, &p.Entry); msg != "" {
		return p, msg
	}
	return p, ""
}

// DigestEntryMailSubject returns the explicit mail subject or the default
// archive subject derived from the digest entry title.
func DigestEntryMailSubject(subject, entryTitle string) string {
	subject = strings.TrimSpace(subject)
	if subject != "" {
		return subject
	}
	return "Archive: " + entryTitle
}

// NormalizeSendMailContentPayload trims send-mail content fields and validates
// the required body. Recipient expansion and authorization stay with callers.
func NormalizeSendMailContentPayload(p SendMailPayload) (SendMailPayload, string) {
	p.Body = strings.TrimSpace(p.Body)
	p.Subject = strings.TrimSpace(p.Subject)
	p.ReplyTo = strings.TrimSpace(p.ReplyTo)
	if p.Body == "" {
		return p, bodyRequiredValidationMessage
	}
	if p.Subject == "" {
		p.Subject = "(no subject)"
	}
	return p, ""
}

// NormalizePostRangeIDs trims, deduplicates, and validates post IDs in a range
// command while preserving first-seen order.
func NormalizePostRangeIDs(input []string) ([]string, string) {
	return normalizeCommandRangeIDs(input, postRangeRequiredValidationMessage, postRangeLengthValidationMessage, postRangeEmptyIDValidationMessage)
}

// NormalizeMailRangeIDs trims, deduplicates, and validates mail IDs in a range
// command while preserving first-seen order.
func NormalizeMailRangeIDs(input []string) ([]string, string) {
	return normalizeCommandRangeIDs(input, mailRangeRequiredValidationMessage, mailRangeLengthValidationMessage, mailRangeEmptyIDValidationMessage)
}

func normalizeCommandRangeIDs(input []string, requiredMessage, lengthMessage, emptyIDMessage string) ([]string, string) {
	if len(input) == 0 {
		return nil, requiredMessage
	}
	if len(input) > MaxCommandRangeItems {
		return nil, lengthMessage
	}
	out := make([]string, 0, len(input))
	seen := map[string]bool{}
	for _, raw := range input {
		id := strings.TrimSpace(raw)
		if id == "" {
			return nil, emptyIDMessage
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, ""
}

// NormalizeBoardMemberTitle trims a board member title and returns a validation
// message when it exceeds the shared board member title cap.
func NormalizeBoardMemberTitle(title string) (string, string) {
	return normalizeLengthCappedString(title, MaxBoardMemberTitleLength, boardMemberTitleLengthValidationMessage)
}

// NormalizeSetBoardModeratorPayload trims board/user references and validates
// that both are present.
func NormalizeSetBoardModeratorPayload(p SetBoardModeratorPayload) (SetBoardModeratorPayload, string) {
	if msg := normalizeRequiredFields(boardAndUserRequiredValidationMessage, &p.Board, &p.User); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeSetBoardMemberPayload trims board/user references and validates the
// shared member title and position constraints.
func NormalizeSetBoardMemberPayload(p SetBoardMemberPayload) (SetBoardMemberPayload, string) {
	if msg := normalizeRequiredFields(boardAndUserRequiredValidationMessage, &p.Board, &p.User); msg != "" {
		return p, msg
	}
	var msg string
	p.Title, msg = NormalizeBoardMemberTitle(p.Title)
	if msg != "" {
		return p, msg
	}
	if p.Position != nil && *p.Position < 0 {
		return p, boardMemberPositionNegativeValidationMessage
	}
	return p, ""
}

// SetBoardMemberPermissionsChanged reports whether a board-member command
// explicitly changes delegated board permissions.
func SetBoardMemberPermissionsChanged(p SetBoardMemberPayload) bool {
	return p.CanManageMembers != nil ||
		p.CanCurate != nil ||
		p.CanModeratePosts != nil ||
		p.CanModerateThreads != nil ||
		p.CanAnnounce != nil ||
		p.CanManagePolls != nil ||
		p.CanSetBoardSettings != nil
}

type CommandPolicyFailure struct {
	Code    string
	Message string
}

func forbiddenPolicyFailure(message string) *CommandPolicyFailure {
	return &CommandPolicyFailure{Code: ErrForbidden, Message: message}
}

const (
	BoardMemberManagerPermissionMessage   = "board member manager permission required"
	BoardModeratorChangeMemberPermissions = "board moderator role required to change member permissions"
	BoardModeratorManageBoardModerators   = "board moderator role required to manage board moderators"
	BoardModeratorManageDelegatedMembers  = "board moderator role required to manage delegated board members"
	BoardModeratorReviewOwnApplication    = "board moderator role required to review your own application"
	BoardModeratorBlacklistMembership     = "board moderator role required to blacklist membership applications"
	AutomodGlobalSanctionPermission       = "only admins can create global-sanction rules"
	AutomodThreadModerationPermission     = "thread moderation permission required"
	AutomodPostModerationPermission       = "post moderation permission required"
)

func CheckBoardMemberManagerPermission(canModerateBoard, canManageMembers bool) *CommandPolicyFailure {
	if !canModerateBoard && !canManageMembers {
		return forbiddenPolicyFailure(BoardMemberManagerPermissionMessage)
	}
	return nil
}

func CheckSetBoardMemberPermissionChange(p SetBoardMemberPayload, canModerateBoard bool) *CommandPolicyFailure {
	if canModerateBoard {
		return nil
	}
	if SetBoardMemberPermissionsChanged(p) {
		return forbiddenPolicyFailure(BoardModeratorChangeMemberPermissions)
	}
	return nil
}

func CheckSetBoardMemberTargetPermission(canModerateBoard, targetIsModerator, targetHasDelegatedPermissions bool) *CommandPolicyFailure {
	if canModerateBoard {
		return nil
	}
	if targetIsModerator {
		return forbiddenPolicyFailure(BoardModeratorManageBoardModerators)
	}
	if targetHasDelegatedPermissions {
		return forbiddenPolicyFailure(BoardModeratorManageDelegatedMembers)
	}
	return nil
}

func CheckReviewBoardMembershipPermission(canModerateBoard, canManageMembers bool, actorID, applicationUserID, status string) *CommandPolicyFailure {
	if failure := CheckBoardMemberManagerPermission(canModerateBoard, canManageMembers); failure != nil {
		return failure
	}
	if !canModerateBoard && actorID == applicationUserID {
		return forbiddenPolicyFailure(BoardModeratorReviewOwnApplication)
	}
	if !canModerateBoard && status == "blacklisted" {
		return forbiddenPolicyFailure(BoardModeratorBlacklistMembership)
	}
	return nil
}

type AutomodPermissionRequirements struct{ Admin, ThreadModeration, PostModeration bool }

func AutomodActionPermissionRequirements(actions []string) AutomodPermissionRequirements {
	var out AutomodPermissionRequirements
	for _, action := range actions {
		switch action {
		case "global_mute":
			out.Admin = true
		case "lock_thread":
			out.ThreadModeration = true
		default:
			out.PostModeration = true
		}
	}
	return out
}

func CheckAutomodActionPermissions(req AutomodPermissionRequirements, isAdmin, canModerateThreads, canModeratePosts bool) *CommandPolicyFailure {
	if req.Admin && !isAdmin {
		return forbiddenPolicyFailure(AutomodGlobalSanctionPermission)
	}
	if req.ThreadModeration && !canModerateThreads {
		return forbiddenPolicyFailure(AutomodThreadModerationPermission)
	}
	if req.PostModeration && !canModeratePosts {
		return forbiddenPolicyFailure(AutomodPostModerationPermission)
	}
	return nil
}

// NormalizeBoardMemberApprovalMode canonicalizes board member approval mode
// aliases.
func NormalizeBoardMemberApprovalMode(mode string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "manual":
		return "manual", ""
	case "auto", "automatic":
		return "auto", ""
	default:
		return "", boardMemberApprovalModeValidationMessage
	}
}

const BoardMembershipAutoApprovalNote = "auto-approved by board membership rules"

// NormalizeSetBoardMemberRequirementsPayload trims the target board,
// validates numeric requirement fields, and canonicalizes approval mode.
func NormalizeSetBoardMemberRequirementsPayload(p SetBoardMemberRequirementsPayload) (SetBoardMemberRequirementsPayload, string) {
	if msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board); msg != "" {
		return p, msg
	}
	for _, field := range []struct {
		name  string
		value *int
	}{
		{"minLoginCount", p.MinLoginCount},
		{"minPostCount", p.MinPostCount},
		{"minTrustLevel", p.MinTrustLevel},
		{"minScore", p.MinScore},
		{"minBoardPostCount", p.MinBoardPostCount},
		{"minBoardOriginalPostCount", p.MinBoardOriginalPostCount},
		{"minBoardDigestCount", p.MinBoardDigestCount},
		{"minBoardMarkCount", p.MinBoardMarkCount},
		{"maxMembers", p.MaxMembers},
	} {
		if field.value != nil && *field.value < 0 {
			return p, field.name + " must be non-negative"
		}
	}
	if p.ApprovalMode != nil {
		mode, msg := NormalizeBoardMemberApprovalMode(*p.ApprovalMode)
		if msg != "" {
			return p, msg
		}
		p.ApprovalMode = &mode
	}
	return p, ""
}

// NormalizeBoardMemberApplicationStatus canonicalizes membership application
// review status aliases.
func NormalizeBoardMemberApplicationStatus(status string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approved", "approve":
		return "approved", ""
	case "rejected", "reject":
		return "rejected", ""
	case "blacklisted", "blacklist":
		return "blacklisted", ""
	default:
		return "", boardMemberApplicationStatusValidationMessage
	}
}

// NormalizeFavoriteFolderName trims a favorite-folder name and validates the
// shared length cap. When required is true, blank names are rejected.
func NormalizeFavoriteFolderName(name string, required bool) (string, string) {
	if required {
		return normalizeRequiredLengthCappedString(name, favoriteFolderNameRequiredValidationMessage, MaxFavoriteFolderNameLength, favoriteFolderNameLengthValidationMessage)
	}
	return normalizeLengthCappedString(name, MaxFavoriteFolderNameLength, favoriteFolderNameLengthValidationMessage)
}

// NormalizeStatsSnapshotDate normalizes a stats snapshot date into display and
// ID forms, defaulting blank input to the UTC day of ts.
func NormalizeStatsSnapshotDate(raw string, ts int64) (dateLabel, dateID, msg string) {
	raw = strings.TrimSpace(raw)
	var day time.Time
	var err error
	if raw == "" {
		day = time.UnixMilli(ts).UTC()
	} else {
		day, err = time.Parse("2006-01-02", raw)
		if err != nil {
			return "", "", statsSnapshotDateValidationMessage
		}
	}
	return day.Format("2006-01-02"), day.Format("20060102"), ""
}

// NormalizeDigestKind canonicalizes digest curation kind aliases.
func NormalizeDigestKind(kind string) (string, string) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		kind = "digest"
	}
	switch kind {
	case "digest", "archive", "recommended", "pinned", "announcement":
		return kind, ""
	default:
		return "", digestKindValidationMessage
	}
}

// NormalizeDigestPath trims outer whitespace/slashes and caps stored digest
// paths at the shared path length.
func NormalizeDigestPath(path string) string {
	path = strings.TrimSpace(path)
	path = strings.Trim(path, "/")
	if len(path) > MaxDigestPathLength {
		return path[:MaxDigestPathLength]
	}
	return path
}

// NormalizeDigestPathMutationBoard trims and validates the board targeted by a
// digest path mutation.
func NormalizeDigestPathMutationBoard(board string) (string, string) {
	return normalizeRequiredString(board, boardRequiredValidationMessage)
}

// NormalizeDigestPathMutationKind canonicalizes the digest kind used by path
// mutations. Blank kind targets the archive tree for compatibility.
func NormalizeDigestPathMutationKind(kind string) (string, string) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		kind = "archive"
	}
	return NormalizeDigestKind(kind)
}

// NormalizeDigestPathMutationSourcePath canonicalizes a required source path.
func NormalizeDigestPathMutationSourcePath(path string) (string, string) {
	return normalizeRequiredString(NormalizeDigestPath(path), sourcePathRequiredValidationMessage)
}

// NormalizeDigestPathMutationPaths canonicalizes a required source path and an
// optional destination path for digest path move/copy commands.
func NormalizeDigestPathMutationPaths(fromPath, toPath string) (string, string, string) {
	fromPath, msg := NormalizeDigestPathMutationSourcePath(fromPath)
	if msg != "" {
		return "", "", msg
	}
	return fromPath, NormalizeDigestPath(toPath), ""
}

// NormalizeDigestCurationFields canonicalizes the digest kind and trims shared
// digest curation text fields. Title defaulting depends on the loaded target.
func NormalizeDigestCurationFields(kind, title, path, note string) (normalizedKind, normalizedTitle, normalizedPath, normalizedNote, msg string) {
	normalizedKind, msg = NormalizeDigestKind(kind)
	if msg != "" {
		return "", "", "", "", msg
	}
	return normalizedKind, strings.TrimSpace(title), NormalizeDigestPath(path), strings.TrimSpace(note), ""
}

// NormalizeRemoveDigestEntryPayload trims the target digest entry and validates
// that it is present.
func NormalizeRemoveDigestEntryPayload(p RemoveDigestEntryPayload) (RemoveDigestEntryPayload, string) {
	msg := normalizeRequiredFields(entryRequiredValidationMessage, &p.Entry)
	return p, msg
}

// NormalizeUpdateDigestEntryTargetPayload trims the target digest entry and
// validates that it is present before callers load current state.
func NormalizeUpdateDigestEntryTargetPayload(p UpdateDigestEntryPayload) (UpdateDigestEntryPayload, string) {
	msg := normalizeRequiredFields(entryRequiredValidationMessage, &p.Entry)
	return p, msg
}

// NormalizeUpdateDigestEntryPayload trims the target entry and optional update
// fields. Callers merge nil fields with the currently loaded digest entry.
func NormalizeUpdateDigestEntryPayload(p UpdateDigestEntryPayload) (UpdateDigestEntryPayload, string) {
	p, msg := NormalizeUpdateDigestEntryTargetPayload(p)
	if msg != "" {
		return p, msg
	}
	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" {
			return p, threadTitleRequiredValidationMessage
		}
		p.Title = &title
	}
	if p.Path != nil {
		path := NormalizeDigestPath(*p.Path)
		p.Path = &path
	}
	if p.Note != nil {
		note := strings.TrimSpace(*p.Note)
		p.Note = &note
	}
	return p, ""
}

// NormalizeSetDigestEntryBodyPayload trims the target digest entry and
// validates that it is present. Body content is intentionally left untouched.
func NormalizeSetDigestEntryBodyPayload(p SetDigestEntryBodyPayload) (SetDigestEntryBodyPayload, string) {
	msg := normalizeRequiredFields(entryRequiredValidationMessage, &p.Entry)
	return p, msg
}

// DigestCurationPermissionMessage returns the user-facing permission error for
// a normalized digest curation kind.
func DigestCurationPermissionMessage(kind string) string {
	if kind == "announcement" {
		return "board announcement permission required"
	}
	return "board curator permission required"
}

// DigestEventScopes returns the event scopes shared by digest mutations.
func DigestEventScopes(boardID string) []string {
	return []string{"board:" + boardID, "digest:" + boardID, "digest:global"}
}

// NormalizeModerationReason trims a moderator-supplied reason and returns a
// validation message when it exceeds the shared moderation reason cap.
func NormalizeModerationReason(reason string) (string, string) {
	return normalizeLengthCappedString(reason, MaxModerationReasonLength, moderationReasonLengthValidationMessage)
}

// NormalizeSanctionUserPayload trims user sanction command fields, normalizes
// the optional scope default, and validates command-local fields.
func NormalizeSanctionUserPayload(p SanctionUserPayload) (SanctionUserPayload, string) {
	var msg string
	p.Reason, msg = NormalizeModerationReason(p.Reason)
	if msg != "" {
		return p, msg
	}
	if msg := normalizeRequiredFields(userRequiredValidationMessage, &p.User); msg != "" {
		return p, msg
	}
	p.Kind = strings.TrimSpace(p.Kind)
	if p.Kind != "mute" && p.Kind != "ban" {
		return p, sanctionKindValidationMessage
	}
	p.Scope = strings.TrimSpace(p.Scope)
	if p.Scope == "" {
		p.Scope = "global"
	}
	return p, ""
}

// NormalizeClearUserSanctionPayload trims clear-sanction command fields,
// normalizes the optional scope default, and validates command-local fields.
func NormalizeClearUserSanctionPayload(p ClearUserSanctionPayload) (ClearUserSanctionPayload, string) {
	if msg := normalizeRequiredFields(userRequiredValidationMessage, &p.User); msg != "" {
		return p, msg
	}
	p.Kind = strings.TrimSpace(p.Kind)
	if p.Kind != "" && p.Kind != "mute" && p.Kind != "ban" {
		return p, clearSanctionKindValidationMessage
	}
	p.Scope = strings.TrimSpace(p.Scope)
	if p.Scope == "" {
		p.Scope = "global"
	}
	var msg string
	p.Reason, msg = NormalizeModerationReason(p.Reason)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeBoardRecommendationNote trims a board recommendation note and
// returns a validation message when it exceeds the shared board note cap.
func NormalizeBoardRecommendationNote(note string) (string, string) {
	return normalizeBoardNote(note, boardRecommendationNoteLengthValidationMessage)
}

// NormalizeSetRecommendedBoardPayload trims the board and note fields and
// validates the shared recommended-board payload constraints.
func NormalizeSetRecommendedBoardPayload(p SetRecommendedBoardPayload) (SetRecommendedBoardPayload, string) {
	if msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board); msg != "" {
		return p, msg
	}
	if p.Position != nil && *p.Position < 0 {
		return p, positionNegativeValidationMessage
	}
	var msg string
	p.Note, msg = NormalizeBoardRecommendationNote(p.Note)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeApplyBoardMembershipPayload trims board membership application
// fields and validates shared payload constraints.
func NormalizeApplyBoardMembershipPayload(p ApplyBoardMembershipPayload) (ApplyBoardMembershipPayload, string) {
	if msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board); msg != "" {
		return p, msg
	}
	var msg string
	p.Note, msg = NormalizeBoardMembershipApplicationNote(p.Note)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeLeaveBoardMembershipPayload trims the target board and validates
// that it is present.
func NormalizeLeaveBoardMembershipPayload(p LeaveBoardMembershipPayload) (LeaveBoardMembershipPayload, string) {
	msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board)
	return p, msg
}

// NormalizeMarkBoardReadPayload trims the target board and validates that it
// is present.
func NormalizeMarkBoardReadPayload(p MarkBoardReadPayload) (MarkBoardReadPayload, string) {
	msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board)
	return p, msg
}

// NormalizeRestoreBoardReadPayload trims the target board and validates that
// it is present.
func NormalizeRestoreBoardReadPayload(p RestoreBoardReadPayload) (RestoreBoardReadPayload, string) {
	msg := normalizeRequiredFields(boardRequiredValidationMessage, &p.Board)
	return p, msg
}

// NormalizeMarkThreadReadPayload trims the target thread and validates that it
// is present.
func NormalizeMarkThreadReadPayload(p MarkThreadReadPayload) (MarkThreadReadPayload, string) {
	msg := normalizeRequiredFields(threadRequiredValidationMessage, &p.Thread)
	return p, msg
}

// NormalizeRestoreThreadReadPayload trims the target thread and validates that
// it is present.
func NormalizeRestoreThreadReadPayload(p RestoreThreadReadPayload) (RestoreThreadReadPayload, string) {
	msg := normalizeRequiredFields(threadRequiredValidationMessage, &p.Thread)
	return p, msg
}

// NormalizeMarkPostReadPayload trims the target post and validates that it is
// present.
func NormalizeMarkPostReadPayload(p MarkPostReadPayload) (MarkPostReadPayload, string) {
	msg := normalizeRequiredFields(postRequiredValidationMessage, &p.Post)
	return p, msg
}

// NormalizeCuratePostTargetPayload trims the target post and validates that it
// is present. Digest curation fields are normalized after the target is loaded.
func NormalizeCuratePostTargetPayload(p CuratePostPayload) (CuratePostPayload, string) {
	msg := normalizeRequiredFields(postRequiredValidationMessage, &p.Post)
	return p, msg
}

// NormalizeCurateThreadTargetPayload trims the target thread and validates
// that it is present. Digest curation fields are normalized after lookup.
func NormalizeCurateThreadTargetPayload(p CurateThreadPayload) (CurateThreadPayload, string) {
	msg := normalizeRequiredFields(threadRequiredValidationMessage, &p.Thread)
	return p, msg
}

// NormalizeReviewBoardMembershipTargetPayload trims and validates the review
// target application and canonical review status.
func NormalizeReviewBoardMembershipTargetPayload(p ReviewBoardMembershipPayload) (ReviewBoardMembershipPayload, string) {
	if msg := normalizeRequiredFields(boardMembershipReviewRequiredValidationMessage, &p.Application, &p.Status); msg != "" {
		return p, msg
	}
	status, msg := NormalizeBoardMemberApplicationStatus(p.Status)
	if msg != "" {
		return p, msg
	}
	p.Status = status
	return p, ""
}

// NormalizeReviewBoardMembershipContent trims and validates reviewer-supplied
// title and note fields. Callers keep permission and state checks nearby.
func NormalizeReviewBoardMembershipContent(p ReviewBoardMembershipPayload) (ReviewBoardMembershipPayload, string) {
	var msg string
	p.Title, msg = NormalizeBoardMemberTitle(p.Title)
	if msg != "" {
		return p, msg
	}
	p.Note, msg = NormalizeBoardMembershipReviewNote(p.Note)
	if msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeBoardMembershipApplicationNote trims a board membership application
// note and returns a validation message when it exceeds the shared board note cap.
func NormalizeBoardMembershipApplicationNote(note string) (string, string) {
	return normalizeBoardNote(note, boardMembershipApplicationNoteLengthValidationMessage)
}

// NormalizeBoardMembershipReviewNote trims a board membership review note and
// returns a validation message when it exceeds the shared board note cap.
func NormalizeBoardMembershipReviewNote(note string) (string, string) {
	return normalizeBoardNote(note, boardMembershipReviewNoteLengthValidationMessage)
}

func normalizeBoardNote(note, lengthMessage string) (string, string) {
	return normalizeLengthCappedString(note, MaxBoardNoteLength, lengthMessage)
}

// NormalizeAttachmentPayload trims attachment metadata and returns a validation
// message when the metadata is not accepted.
func NormalizeAttachmentPayload(input AttachmentPayload) (AttachmentPayload, string) {
	out := input
	filenameMsg, contentTypeMsg, urlMsg := "", "", ""
	out.Filename, filenameMsg = normalizeRequiredLengthCappedString(input.Filename, attachmentFilenameRequiredValidationMessage, MaxAttachmentFilenameLength, attachmentFilenameLengthValidationMessage)
	out.ContentType, contentTypeMsg = normalizeLengthCappedString(input.ContentType, MaxAttachmentContentTypeLength, attachmentContentTypeLengthValidationMessage)
	out.URL, urlMsg = normalizeLengthCappedString(input.URL, MaxAttachmentURLLength, attachmentURLLengthValidationMessage)
	if filenameMsg != "" {
		return out, filenameMsg
	}
	if contentTypeMsg != "" {
		return out, contentTypeMsg
	}
	if urlMsg != "" {
		return out, urlMsg
	}
	if out.SizeBytes < 0 {
		return out, attachmentSizeValidationMessage
	}
	return out, ""
}

// NormalizePostAttachments validates and trims inline post attachment metadata.
// Client-supplied IDs are cleared so command handlers can assign canonical IDs.
func NormalizePostAttachments(input []AttachmentPayload) ([]AttachmentPayload, string) {
	return normalizeAttachmentPayloads(input, ValidatePostAttachmentCount)
}

// NormalizeMailAttachments validates and trims inline mail attachment metadata.
// Client-supplied IDs are cleared so command handlers can assign canonical IDs.
func NormalizeMailAttachments(input []AttachmentPayload) ([]AttachmentPayload, string) {
	return normalizeAttachmentPayloads(input, ValidateMailAttachmentCount)
}

// WithAttachmentIDs copies normalized attachment payloads and assigns canonical
// IDs from the supplied generator.
func WithAttachmentIDs(input []AttachmentPayload, idFor func(int) string) []AttachmentPayload {
	if len(input) == 0 {
		return nil
	}
	out := make([]AttachmentPayload, len(input))
	for i, item := range input {
		item.ID = idFor(i)
		out[i] = item
	}
	return out
}

// MailMessageSize returns the quota size of a mail message body plus
// attachments, clamped to zero if malformed negative attachment sizes underflow.
func MailMessageSize(subject, body string, attachments []AttachmentPayload) int64 {
	size := int64(len(subject) + len(body))
	for _, item := range attachments {
		size += item.SizeBytes
	}
	if size < 0 {
		return 0
	}
	return size
}

func normalizeAttachmentPayloads(input []AttachmentPayload, validateCount func(int) string) ([]AttachmentPayload, string) {
	if len(input) == 0 {
		return nil, ""
	}
	if msg := validateCount(len(input)); msg != "" {
		return nil, msg
	}
	out := make([]AttachmentPayload, 0, len(input))
	for _, item := range input {
		item, msg := NormalizeAttachmentPayload(item)
		if msg != "" {
			return nil, msg
		}
		item.ID = ""
		out = append(out, item)
	}
	return out, ""
}

// NormalizeAttachMailPayload trims attach-mail identifiers and validates the
// embedded attachment metadata.
func NormalizeAttachMailPayload(p AttachMailPayload) (AttachMailPayload, string) {
	p.ID = strings.TrimSpace(p.ID)
	p.StagedBlobID = strings.TrimSpace(p.StagedBlobID)
	if msg := normalizeRequiredFields(mailRequiredValidationMessage, &p.Mail); msg != "" {
		return p, msg
	}
	filename, contentType, msg := normalizeAttachedFileMetadata(p.Filename, p.ContentType, p.SizeBytes)
	if msg != "" {
		return p, msg
	}
	p.Filename = filename
	p.ContentType = contentType
	return p, ""
}

// NormalizeAttachPostPayload trims attach-post identifiers and validates the
// embedded attachment metadata.
func NormalizeAttachPostPayload(p AttachPostPayload) (AttachPostPayload, string) {
	p.ID = strings.TrimSpace(p.ID)
	p.Post = strings.TrimSpace(p.Post)
	p.StagedBlobID = strings.TrimSpace(p.StagedBlobID)
	if p.Post == "" || strings.TrimSpace(p.Filename) == "" {
		return p, attachPostRequiredValidationMessage
	}
	filename, contentType, msg := normalizeAttachedFileMetadata(p.Filename, p.ContentType, p.SizeBytes)
	if msg != "" {
		return p, msg
	}
	p.Filename = filename
	p.ContentType = contentType
	return p, ""
}

func normalizeAttachedFileMetadata(filename, contentType string, sizeBytes int64) (string, string, string) {
	attachment, msg := NormalizeAttachmentPayload(AttachmentPayload{Filename: filename, ContentType: contentType, SizeBytes: sizeBytes})
	return attachment.Filename, attachment.ContentType, msg
}

type CreateBoardPayload struct {
	ID          string `json:"id"` // URL-safe slug
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ParentID    string `json:"parentId,omitempty"`
	Position    *int   `json:"position,omitempty"`
}

type SetBoardSettingsPayload struct {
	Board              string  `json:"board"`
	AnonymousAllowed   *bool   `json:"anonymousAllowed,omitempty"`
	ReadOnly           *bool   `json:"readOnly,omitempty"`
	NoReply            *bool   `json:"noReply,omitempty"`
	AttachmentsAllowed *bool   `json:"attachmentsAllowed,omitempty"`
	MailInAllowed      *bool   `json:"mailInAllowed,omitempty"`
	RelayEnabled       *bool   `json:"relayEnabled,omitempty"`
	MemberReadMode     *bool   `json:"memberReadMode,omitempty"`
	MemberPostMode     *bool   `json:"memberPostMode,omitempty"`
	StatsExcluded      *bool   `json:"statsExcluded,omitempty"`
	ZapAllowed         *bool   `json:"zapAllowed,omitempty"`
	GuestAccess        *string `json:"guestAccess,omitempty"`
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

// NormalizeDeleteBoardAutomodRulePayload trims fields for board automod rule
// delete commands and validates the target rule identity.
func NormalizeDeleteBoardAutomodRulePayload(p DeleteBoardAutomodRulePayload) (DeleteBoardAutomodRulePayload, string) {
	if msg := normalizeRequiredFields(boardAndIDRequiredValidationMessage, &p.ID, &p.Board); msg != "" {
		return p, msg
	}
	return p, ""
}

// NormalizeAutomodRulePayload trims fields and canonicalizes the comma-separated
// action list used by board automod rule commands.
func NormalizeAutomodRulePayload(p SetBoardAutomodRulePayload) (SetBoardAutomodRulePayload, []string) {
	out := p
	out.ID = strings.TrimSpace(p.ID)
	out.Board = strings.TrimSpace(p.Board)
	out.MatchType = strings.TrimSpace(p.MatchType)
	out.Pattern = strings.TrimSpace(p.Pattern)
	actions := ParseAutomodActions(p.Action)
	out.Action = strings.Join(actions, ",")
	out.Reason, _ = NormalizeModerationReason(p.Reason)
	out.Note = strings.TrimSpace(p.Note)
	return out, actions
}

// NormalizeSetBoardAutomodRulePayload trims and canonicalizes a board automod
// rule command payload, then validates the command target board.
func NormalizeSetBoardAutomodRulePayload(p SetBoardAutomodRulePayload) (SetBoardAutomodRulePayload, []string, string) {
	p, actions := NormalizeAutomodRulePayload(p)
	if p.Board == "" {
		return p, actions, boardRequiredValidationMessage
	}
	return p, actions, ""
}

// ValidateAutomodRule checks rule fields and returns a validation message, or ""
// if the rule is valid. Shared by the command handler and the native
// command-log decider so both paths validate identically.
func ValidateAutomodRule(p SetBoardAutomodRulePayload) string {
	p, actions := NormalizeAutomodRulePayload(p)
	matchType := p.MatchType
	if !AutomodMatchTypes[matchType] {
		return "unknown match type"
	}
	if len(actions) == 0 {
		return "at least one action is required"
	}
	for _, a := range actions {
		if !AutomodActions[a] {
			return "unknown action: " + a
		}
	}
	pattern := p.Pattern
	switch matchType {
	case "keyword", "regex":
		if pattern == "" {
			return "pattern is required for keyword/regex rules"
		}
		if _, msg := normalizeLengthCappedString(pattern, MaxAutomodPatternLength, automodPatternLengthValidationMessage); msg != "" {
			return msg
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
	if _, msg := NormalizeModerationReason(p.Reason); msg != "" {
		return msg
	}
	if _, msg := normalizeLengthCappedString(p.Note, MaxAutomodNoteLength, automodNoteLengthValidationMessage); msg != "" {
		return msg
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
