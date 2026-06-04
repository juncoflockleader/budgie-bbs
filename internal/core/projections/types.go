package projections

const MaxUserSignatures = 8
const MaxUserPersonalFiles = 16

// Board is the projection of a board.
type Board struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	AnonymousAllowed   bool   `json:"anonymousAllowed"`
	ReadOnly           bool   `json:"readOnly"`
	NoReply            bool   `json:"noReply"`
	AttachmentsAllowed bool   `json:"attachmentsAllowed"`
	MailInAllowed      bool   `json:"mailInAllowed"`
	RelayEnabled       bool   `json:"relayEnabled"`
	MemberReadMode     bool   `json:"memberReadMode"`
	MemberPostMode     bool   `json:"memberPostMode"`
	ModeratorCount     int    `json:"moderatorCount"`
}

type BoardSummary struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Favorite           bool   `json:"favorite"`
	UnreadThreads      int    `json:"unreadThreads"`
	UnreadPosts        int    `json:"unreadPosts"`
	LastSeq            int64  `json:"lastSeq"`
	ReadSeq            int64  `json:"readSeq"`
	AnonymousAllowed   bool   `json:"anonymousAllowed"`
	ReadOnly           bool   `json:"readOnly"`
	NoReply            bool   `json:"noReply"`
	AttachmentsAllowed bool   `json:"attachmentsAllowed"`
	MailInAllowed      bool   `json:"mailInAllowed"`
	RelayEnabled       bool   `json:"relayEnabled"`
	MemberReadMode     bool   `json:"memberReadMode"`
	MemberPostMode     bool   `json:"memberPostMode"`
	ModeratorCount     int    `json:"moderatorCount"`
}

type CommunityStats struct {
	TotalUsers          int   `json:"totalUsers"`
	TotalBoards         int   `json:"totalBoards"`
	TotalThreads        int   `json:"totalThreads"`
	TotalPosts          int   `json:"totalPosts"`
	TotalReactions      int   `json:"totalReactions"`
	TotalMail           int   `json:"totalMail"`
	TotalDirectMessages int   `json:"totalDirectMessages"`
	OnlineUsers         int   `json:"onlineUsers"`
	MaxOnlineUsers      int   `json:"maxOnlineUsers"`
	MaxOnlineAt         int64 `json:"maxOnlineAt"`
	HeadSeq             int64 `json:"headSeq"`
}

type CommunityStatHistory struct {
	Day                 string `json:"day"`
	SnapshotAt          int64  `json:"snapshotAt"`
	TotalUsers          int    `json:"totalUsers"`
	TotalBoards         int    `json:"totalBoards"`
	TotalThreads        int    `json:"totalThreads"`
	TotalPosts          int    `json:"totalPosts"`
	TotalReactions      int    `json:"totalReactions"`
	TotalMail           int    `json:"totalMail"`
	TotalDirectMessages int    `json:"totalDirectMessages"`
	OnlineUsers         int    `json:"onlineUsers"`
	MaxOnlineUsers      int    `json:"maxOnlineUsers"`
	MaxOnlineAt         int64  `json:"maxOnlineAt"`
	HeadSeq             int64  `json:"headSeq"`
}

type ContentFilter struct {
	ID        string `json:"id"`
	Pattern   string `json:"pattern"`
	Scope     string `json:"scope"`
	Active    bool   `json:"active"`
	CreatedBy string `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type BoardRanking struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ThreadCount    int    `json:"threadCount"`
	PostCount      int    `json:"postCount"`
	LastSeq        int64  `json:"lastSeq"`
	LastPostAt     int64  `json:"lastPostAt"`
	ModeratorCount int    `json:"moderatorCount"`
}

type ThreadRanking struct {
	ID            string `json:"id"`
	Board         string `json:"board"`
	BoardName     string `json:"boardName"`
	Title         string `json:"title"`
	Author        string `json:"author"`
	AuthorID      string `json:"authorId,omitempty"`
	PostCount     int    `json:"postCount"`
	ReactionCount int    `json:"reactionCount"`
	Score         int    `json:"score"`
	LastSeq       int64  `json:"lastSeq"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

type ReplyRanking struct {
	PostID    string `json:"postId"`
	ThreadID  string `json:"threadId"`
	Board     string `json:"board"`
	BoardName string `json:"boardName"`
	Title     string `json:"title"`
	Author    string `json:"author"`
	AuthorID  string `json:"authorId,omitempty"`
	Excerpt   string `json:"excerpt"`
	Seq       int64  `json:"seq"`
	CreatedAt int64  `json:"createdAt"`
}

type UserRanking struct {
	UserID            string `json:"userId"`
	Name              string `json:"name"`
	Role              string `json:"role"`
	PostsCreated      int    `json:"postsCreated"`
	ReactionsReceived int    `json:"reactionsReceived"`
	LoginCount        int    `json:"loginCount"`
	TrustLevel        int    `json:"trustLevel"`
}

type Blessing struct {
	ID         string `json:"id"`
	FromUserID string `json:"fromUserId"`
	FromName   string `json:"fromName"`
	ToUserID   string `json:"toUserId"`
	ToName     string `json:"toName"`
	Message    string `json:"message,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	Seq        int64  `json:"seq"`
}

type BlessingRanking struct {
	UserID        string `json:"userId"`
	Name          string `json:"name"`
	BlessingCount int    `json:"blessingCount"`
	LastBlessedAt int64  `json:"lastBlessedAt"`
}

type ArchiveRanking struct {
	BoardID       string `json:"boardId"`
	BoardName     string `json:"boardName"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	EntryCount    int    `json:"entryCount"`
	EditedCount   int    `json:"editedCount"`
	LastUpdatedAt int64  `json:"lastUpdatedAt"`
}

type FavoriteFolder struct {
	ID        string `json:"id"`
	ParentID  string `json:"parentId,omitempty"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type FavoriteBoardEntry struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	FolderID      string `json:"folderId,omitempty"`
	Position      int    `json:"position"`
	UnreadThreads int    `json:"unreadThreads"`
	UnreadPosts   int    `json:"unreadPosts"`
	LastSeq       int64  `json:"lastSeq"`
	ReadSeq       int64  `json:"readSeq"`
}

type FavoriteTree struct {
	Folders []FavoriteFolder     `json:"folders"`
	Boards  []FavoriteBoardEntry `json:"boards"`
}

type BoardSettings struct {
	BoardID            string `json:"boardId"`
	AnonymousAllowed   bool   `json:"anonymousAllowed"`
	ReadOnly           bool   `json:"readOnly"`
	NoReply            bool   `json:"noReply"`
	AttachmentsAllowed bool   `json:"attachmentsAllowed"`
	MailInAllowed      bool   `json:"mailInAllowed"`
	RelayEnabled       bool   `json:"relayEnabled"`
	MemberReadMode     bool   `json:"memberReadMode"`
	MemberPostMode     bool   `json:"memberPostMode"`
	UpdatedAt          int64  `json:"updatedAt"`
}

type BoardSettingsPatch struct {
	AnonymousAllowed   *bool
	ReadOnly           *bool
	NoReply            *bool
	AttachmentsAllowed *bool
	MailInAllowed      *bool
	RelayEnabled       *bool
	MemberReadMode     *bool
	MemberPostMode     *bool
}

type BoardModerator struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Position  int    `json:"position"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type BoardMember struct {
	UserID              string `json:"userId"`
	Name                string `json:"name"`
	Title               string `json:"title,omitempty"`
	Position            int    `json:"position"`
	CanManageMembers    bool   `json:"canManageMembers"`
	CanCurate           bool   `json:"canCurate"`
	CanModeratePosts    bool   `json:"canModeratePosts"`
	CanModerateThreads  bool   `json:"canModerateThreads"`
	CanAnnounce         bool   `json:"canAnnounce"`
	CanManagePolls      bool   `json:"canManagePolls"`
	CanSetBoardSettings bool   `json:"canSetBoardSettings"`
	CreatedAt           int64  `json:"createdAt"`
	UpdatedAt           int64  `json:"updatedAt"`
}

type BoardMemberPatch struct {
	Title               string
	Position            *int
	CanManageMembers    *bool
	CanCurate           *bool
	CanModeratePosts    *bool
	CanModerateThreads  *bool
	CanAnnounce         *bool
	CanManagePolls      *bool
	CanSetBoardSettings *bool
}

type BoardMemberApplication struct {
	ID           string `json:"id"`
	BoardID      string `json:"boardId"`
	UserID       string `json:"userId"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	Note         string `json:"note,omitempty"`
	Title        string `json:"title,omitempty"`
	ReviewerID   string `json:"reviewerId,omitempty"`
	ReviewerName string `json:"reviewerName,omitempty"`
	ReviewNote   string `json:"reviewNote,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
	UpdatedAt    int64  `json:"updatedAt"`
	ReviewedAt   int64  `json:"reviewedAt,omitempty"`
}

type BoardMemberRequirements struct {
	BoardID                   string `json:"boardId"`
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
	UpdatedAt                 int64  `json:"updatedAt"`
}

type BoardMemberRequirementsPatch struct {
	MinLoginCount             *int
	MinPostCount              *int
	MinTrustLevel             *int
	MinScore                  *int
	MinBoardPostCount         *int
	MinBoardOriginalPostCount *int
	MinBoardDigestCount       *int
	MinBoardMarkCount         *int
	MaxMembers                *int
	ApprovalMode              *string
}

type BoardInfo struct {
	Board        Board                   `json:"board"`
	Settings     BoardSettings           `json:"settings"`
	Requirements BoardMemberRequirements `json:"requirements"`
	Moderators   []BoardModerator        `json:"moderators"`
	Members      []BoardMember           `json:"members"`
}

type DigestEntry struct {
	ID            string `json:"id"`
	BoardID       string `json:"boardId"`
	BoardName     string `json:"boardName,omitempty"`
	TargetKind    string `json:"targetKind"`
	TargetID      string `json:"targetId"`
	Kind          string `json:"kind"`
	Title         string `json:"title"`
	Path          string `json:"path,omitempty"`
	Note          string `json:"note,omitempty"`
	BodyEdited    bool   `json:"bodyEdited,omitempty"`
	CreatedBy     string `json:"createdBy"`
	CreatedByName string `json:"createdByName"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	ThreadID      string `json:"threadId"`
	PostID        string `json:"postId,omitempty"`
	Author        string `json:"author,omitempty"`
	Excerpt       string `json:"excerpt,omitempty"`
}

type DigestPathNode struct {
	Path       string `json:"path"`
	Name       string `json:"name"`
	ParentPath string `json:"parentPath"`
	Kind       string `json:"kind,omitempty"`
	EntryCount int    `json:"entryCount"`
	ChildCount int    `json:"childCount"`
	Explicit   bool   `json:"explicit,omitempty"`
}

type DigestExport struct {
	Entry DigestEntry `json:"entry"`
	Body  string      `json:"body"`
}

type MailItem struct {
	ID          string           `json:"id"`
	FromUserID  string           `json:"fromUserId"`
	FromName    string           `json:"fromName"`
	ToUserIDs   []string         `json:"toUserIds"`
	ToNames     []string         `json:"toNames"`
	Subject     string           `json:"subject"`
	Body        string           `json:"body,omitempty"`
	Excerpt     string           `json:"excerpt,omitempty"`
	ParentID    string           `json:"parentId,omitempty"`
	Mailbox     string           `json:"mailbox"`
	Role        string           `json:"role"`
	Read        bool             `json:"read"`
	Kept        bool             `json:"kept"`
	Attachments []MailAttachment `json:"attachments,omitempty"`
	CreatedAt   int64            `json:"createdAt"`
	UpdatedAt   int64            `json:"updatedAt"`
	Seq         int64            `json:"seq"`
}

type MailUsage struct {
	UserID         string `json:"userId"`
	UsedBytes      int64  `json:"usedBytes"`
	QuotaBytes     int64  `json:"quotaBytes"`
	RemainingBytes int64  `json:"remainingBytes"`
}

type RelayDelivery struct {
	ID         string `json:"id"`
	BoardID    string `json:"boardId"`
	ThreadID   string `json:"threadId"`
	PostID     string `json:"postId"`
	AuthorID   string `json:"authorId,omitempty"`
	AuthorName string `json:"authorName"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Status     string `json:"status"`
	LastError  string `json:"lastError,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
	Seq        int64  `json:"seq"`
}

type MailAttachment struct {
	ID          string `json:"id"`
	MailID      string `json:"mailId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	URL         string `json:"url,omitempty"`
	Stored      bool   `json:"stored"`
	CreatedBy   string `json:"createdBy,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

type MailGroupMember struct {
	UserID   string `json:"userId"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type MailGroup struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Members   []MailGroupMember `json:"members"`
	BuiltIn   bool              `json:"builtIn,omitempty"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
}

type DirectMessage struct {
	ID             string `json:"id"`
	ConversationID string `json:"conversationId"`
	FromUserID     string `json:"fromUserId"`
	FromName       string `json:"fromName"`
	ToUserID       string `json:"toUserId"`
	ToName         string `json:"toName"`
	OtherUserID    string `json:"otherUserId"`
	OtherName      string `json:"otherName"`
	Body           string `json:"body"`
	Read           bool   `json:"read"`
	Mine           bool   `json:"mine"`
	CreatedAt      int64  `json:"createdAt"`
	Seq            int64  `json:"seq"`
}

type DirectMessageConversation struct {
	UserID        string `json:"userId"`
	Name          string `json:"name"`
	LastMessageID string `json:"lastMessageId"`
	LastBody      string `json:"lastBody"`
	LastFromName  string `json:"lastFromName"`
	LastAt        int64  `json:"lastAt"`
	UnreadCount   int    `json:"unreadCount"`
}

type DirectMessageSettings struct {
	UserID    string `json:"userId"`
	Policy    string `json:"policy"`
	UpdatedAt int64  `json:"updatedAt"`
}

type SocialUser struct {
	UserID        string `json:"userId"`
	SessionID     string `json:"sessionId,omitempty"`
	Name          string `json:"name"`
	DisplayName   string `json:"displayName"`
	Role          string `json:"role"`
	Note          string `json:"note,omitempty"`
	Kind          string `json:"kind"`
	Mutual        bool   `json:"mutual"`
	Ignored       bool   `json:"ignored"`
	Status        string `json:"status,omitempty"`
	Mode          string `json:"mode,omitempty"`
	BoardID       string `json:"boardId,omitempty"`
	BoardName     string `json:"boardName,omitempty"`
	ThreadID      string `json:"threadId,omitempty"`
	LocationLabel string `json:"locationLabel,omitempty"`
	FromHost      string `json:"fromHost,omitempty"`
	LastSeen      int64  `json:"lastSeen"`
	IdleSeconds   int64  `json:"idleSeconds"`
	Online        bool   `json:"online"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// Category is a board category projection.
type Category struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ParentID    string `json:"parentId,omitempty"`
	Position    int    `json:"position"`
	Visibility  string `json:"visibility"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

// Thread is the projection of a thread.
type Thread struct {
	ID        string `json:"id"`
	Board     string `json:"board"`
	Author    string `json:"author"`
	AuthorID  string `json:"authorId,omitempty"`
	Title     string `json:"title"`
	Locked    bool   `json:"locked"`
	PostCount int    `json:"postCount"`
	LastSeq   int64  `json:"lastSeq"`
	CreatedTS int64  `json:"createdTs"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type ThreadSummary struct {
	ID                string `json:"id"`
	Board             string `json:"board"`
	BoardName         string `json:"boardName,omitempty"`
	Author            string `json:"author"`
	AuthorID          string `json:"authorId,omitempty"`
	Title             string `json:"title"`
	Locked            bool   `json:"locked"`
	PostCount         int    `json:"postCount"`
	LastSeq           int64  `json:"lastSeq"`
	CreatedTS         int64  `json:"createdTs"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
	ReadSeq           int64  `json:"readSeq"`
	UnreadPosts       int    `json:"unreadPosts"`
	FirstUnreadPostID string `json:"firstUnreadPostId,omitempty"`
}

// Post is the projection of a post.
type Post struct {
	ID             string           `json:"id"`
	Thread         string           `json:"thread"`
	Board          string           `json:"board,omitempty"`
	BoardName      string           `json:"boardName,omitempty"`
	ThreadTitle    string           `json:"threadTitle,omitempty"`
	Author         string           `json:"author"`
	AuthorID       string           `json:"authorId,omitempty"`
	Body           string           `json:"body"`
	Signature      string           `json:"signature,omitempty"`
	ContentType    string           `json:"contentType"`
	ReplyTo        string           `json:"replyTo,omitempty"`
	ReplyDepth     int              `json:"replyDepth,omitempty"`
	Version        int              `json:"version"`
	Redacted       bool             `json:"redacted"`
	Marked         bool             `json:"marked"`
	Recommended    bool             `json:"recommended"`
	NoReply        bool             `json:"noReply"`
	TeX            bool             `json:"tex"`
	MailBack       bool             `json:"mailBack"`
	SourcePost     string           `json:"sourcePost,omitempty"`
	SourceThread   string           `json:"sourceThread,omitempty"`
	SourceBoard    string           `json:"sourceBoard,omitempty"`
	SourceAuthor   string           `json:"sourceAuthor,omitempty"`
	SourceAuthorID string           `json:"sourceAuthorId,omitempty"`
	SourceTitle    string           `json:"sourceTitle,omitempty"`
	ReactionCount  int              `json:"ReactionCount"`
	Attachments    []PostAttachment `json:"attachments,omitempty"`
	CreatedSeq     int64            `json:"createdSeq"`
	UpdatedSeq     int64            `json:"updatedSeq"`
	CreatedAt      int64            `json:"createdAt"`
	UpdatedAt      int64            `json:"updatedAt"`
}

type PostAttachment struct {
	ID          string `json:"id"`
	PostID      string `json:"postId"`
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	SizeBytes   int64  `json:"sizeBytes,omitempty"`
	URL         string `json:"url,omitempty"`
	Stored      bool   `json:"stored"`
	CreatedBy   string `json:"createdBy,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
}

// User is the projection of an account.
type User struct {
	ID                 string
	Name               string
	Role               string // "user" | "trusted" | "moderator" | "admin"
	Password           string // bcrypt hash, never sent to clients
	Created            int64
	RegistrationStatus string
	ReviewedAt         int64
	ReviewedBy         string
	ReviewReason       string
	DeactivatedAt      int64
	DeactivatedBy      string
	DeactivatedReason  string
}

type AccountRegistrationSettings struct {
	RequireApproval bool  `json:"requireApproval"`
	UpdatedAt       int64 `json:"updatedAt"`
}

type AccountRegistration struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	Created        int64  `json:"created"`
	ReviewedAt     int64  `json:"reviewedAt"`
	ReviewedBy     string `json:"reviewedBy"`
	ReviewedByName string `json:"reviewedByName,omitempty"`
	ReviewReason   string `json:"reviewReason"`
}

type PasswordRecoveryRequest struct {
	ID             string `json:"id"`
	UserID         string `json:"userId"`
	UserName       string `json:"userName"`
	Status         string `json:"status"`
	SubmittedName  string `json:"submittedName"`
	SubmittedEmail string `json:"submittedEmail"`
	Note           string `json:"note"`
	ReviewerID     string `json:"reviewerId"`
	ReviewerName   string `json:"reviewerName,omitempty"`
	ReviewNote     string `json:"reviewNote"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

type UserProfile struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Role              string   `json:"role"`
	DisplayName       string   `json:"displayName"`
	Bio               string   `json:"bio"`
	Avatar            string   `json:"avatar"`
	Signature         string   `json:"signature"`
	Plan              string   `json:"plan"`
	Homepage          string   `json:"homepage"`
	Created           int64    `json:"created"`
	LastSeen          int64    `json:"lastSeen"`
	PostsCreated      int      `json:"postsCreated"`
	ReactionsReceived int      `json:"reactionsReceived"`
	TrustLevel        int      `json:"trustLevel"`
	Pubkeys           []string `json:"pubkeys"`
}

type UserPrivateProfile struct {
	UserID            string `json:"userId"`
	RealName          string `json:"realName"`
	RealEmail         string `json:"realEmail"`
	RegistrationEmail string `json:"registrationEmail"`
	Address           string `json:"address"`
	Phone             string `json:"phone"`
	Mobile            string `json:"mobile"`
	Birthday          string `json:"birthday"`
	School            string `json:"school"`
	ContactNote       string `json:"contactNote"`
	UpdatedAt         int64  `json:"updatedAt"`
}

type UserPersonalFile struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Body      string `json:"body"`
	Public    bool   `json:"public"`
	UpdatedAt int64  `json:"updatedAt"`
}

type UserSignature struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Label     string `json:"label"`
	Body      string `json:"body"`
	Position  int    `json:"position"`
	Active    bool   `json:"active"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type UserSignatureSettings struct {
	UserID              string `json:"userId"`
	SelectedSignatureID string `json:"selectedSignatureId"`
	RandomEnabled       bool   `json:"randomEnabled"`
	UpdatedAt           int64  `json:"updatedAt"`
}

type UserSignatureBundle struct {
	Signatures []UserSignature       `json:"signatures"`
	Settings   UserSignatureSettings `json:"settings"`
	MaxCount   int                   `json:"maxCount"`
}

type UserSignatureRecount struct {
	UserID              string `json:"userId"`
	Count               int    `json:"count"`
	ActiveCount         int    `json:"activeCount"`
	SelectedSignatureID string `json:"selectedSignatureId"`
	RandomEnabled       bool   `json:"randomEnabled"`
	CurrentSignature    string `json:"currentSignature"`
	UpdatedAt           int64  `json:"updatedAt"`
}

type UserLoginACLRule struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Pattern   string `json:"pattern"`
	Note      string `json:"note"`
	Position  int    `json:"position"`
	Active    bool   `json:"active"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type UserLoginACLSettings struct {
	UserID    string `json:"userId"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt int64  `json:"updatedAt"`
}

type UserLoginACLBundle struct {
	Rules    []UserLoginACLRule   `json:"rules"`
	Settings UserLoginACLSettings `json:"settings"`
	Host     string               `json:"host,omitempty"`
	Allowed  bool                 `json:"allowed"`
}

type ModerationReview struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	TargetID   string `json:"targetId"`
	TargetKind string `json:"targetKind"`
	Reporter   string `json:"reporter"`
	Reason     string `json:"reason"`
	Resolution string `json:"resolution,omitempty"`
	Actor      string `json:"actor,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

type UserSanction struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Kind      string `json:"kind"`
	Scope     string `json:"scope"`
	ExpiresAt int64  `json:"expiresAt"`
	By        string `json:"by"`
	Reason    string `json:"reason"`
	Seq       int64  `json:"seq"`
}

// Poll is the API projection of a poll.
type Poll struct {
	ID        string       `json:"id"`
	PostID    string       `json:"postId"`
	Question  string       `json:"question,omitempty"`
	ExpiresAt int64        `json:"expiresAt,omitempty"`
	TS        int64        `json:"ts"`
	Options   []PollOption `json:"options"`
	Voted     string       `json:"voted,omitempty"` // option_id the current user voted for
}

type PollOption struct {
	ID        string `json:"id"`
	Text      string `json:"text"`
	VoteCount int    `json:"voteCount"`
}

// Notification is the API projection of a notification.
type Notification struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "mention" | "reply" | "watched" | "login"
	ThreadID string `json:"threadId"`
	PostID   string `json:"postId"`
	Actor    string `json:"actor"`
	Read     bool   `json:"read"`
	TS       int64  `json:"ts"`
}

// TrustLevelInfo holds computed activity stats and trust level for a user.
type TrustLevelInfo struct {
	LoginCount    int `json:"loginCount"`
	PostsCreated  int `json:"postsCreated"`
	DaysVisited   int `json:"daysVisited"`
	ReactionsRecv int `json:"reactionsReceived"`
	TrustLevel    int `json:"trustLevel"`
}

// IsMod reports if a user is a moderator or admin.
func (u *User) IsMod() bool { return u.Role == "moderator" || u.Role == "admin" }

// IsAdmin reports if a user is an admin.
func (u *User) IsAdmin() bool { return u.Role == "admin" }
