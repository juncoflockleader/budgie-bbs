package projections

// Board is the projection of a board.
type Board struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
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

// Post is the projection of a post.
type Post struct {
	ID            string `json:"id"`
	Thread        string `json:"thread"`
	Author        string `json:"author"`
	AuthorID      string `json:"authorId,omitempty"`
	Body          string `json:"body"`
	ContentType   string `json:"contentType"`
	ReplyTo       string `json:"replyTo,omitempty"`
	Version       int    `json:"version"`
	Redacted      bool   `json:"redacted"`
	ReactionCount int    `json:"ReactionCount"`
	CreatedSeq    int64  `json:"createdSeq"`
	UpdatedSeq    int64  `json:"updatedSeq"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
}

// User is the projection of an account.
type User struct {
	ID       string
	Name     string
	Role     string // "user" | "trusted" | "moderator" | "admin"
	Password string // bcrypt hash, never sent to clients
	Created  int64
}

type UserProfile struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Role              string   `json:"role"`
	DisplayName       string   `json:"displayName"`
	Bio               string   `json:"bio"`
	Avatar            string   `json:"avatar"`
	Created           int64    `json:"created"`
	LastSeen          int64    `json:"lastSeen"`
	PostsCreated      int      `json:"postsCreated"`
	ReactionsReceived int      `json:"reactionsReceived"`
	TrustLevel        int      `json:"trustLevel"`
	Pubkeys           []string `json:"pubkeys"`
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
	Kind     string `json:"kind"` // "mention" | "reply" | "watched"
	ThreadID string `json:"threadId"`
	PostID   string `json:"postId"`
	Actor    string `json:"actor"`
	Read     bool   `json:"read"`
	TS       int64  `json:"ts"`
}

// TrustLevelInfo holds computed activity stats and trust level for a user.
type TrustLevelInfo struct {
	PostsCreated  int `json:"postsCreated"`
	DaysVisited   int `json:"daysVisited"`
	ReactionsRecv int `json:"reactionsReceived"`
	TrustLevel    int `json:"trustLevel"`
}

// IsMod reports if a user is a moderator or admin.
func (u *User) IsMod() bool { return u.Role == "moderator" || u.Role == "admin" }

// IsAdmin reports if a user is an admin.
func (u *User) IsAdmin() bool { return u.Role == "admin" }
