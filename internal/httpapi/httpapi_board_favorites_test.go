package httpapi_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type boardsResponse struct {
	Boards []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
	} `json:"boards"`
}

type boardSummariesResponse struct {
	Boards []struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Description   string `json:"description"`
		Favorite      bool   `json:"favorite"`
		UnreadThreads int    `json:"unreadThreads"`
		UnreadPosts   int    `json:"unreadPosts"`
		LastSeq       int64  `json:"lastSeq"`
		ReadSeq       int64  `json:"readSeq"`
	} `json:"boards"`
}

type boardInfoResponse struct {
	Board struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		AnonymousAllowed bool   `json:"anonymousAllowed"`
		ReadOnly         bool   `json:"readOnly"`
		NoReply          bool   `json:"noReply"`
		ModeratorCount   int    `json:"moderatorCount"`
	} `json:"board"`
	Settings struct {
		AnonymousAllowed bool `json:"anonymousAllowed"`
		ReadOnly         bool `json:"readOnly"`
		NoReply          bool `json:"noReply"`
		MemberReadMode   bool `json:"memberReadMode"`
		MemberPostMode   bool `json:"memberPostMode"`
	} `json:"settings"`
	Requirements struct {
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
	} `json:"requirements"`
	Moderators []struct {
		UserID string `json:"userId"`
		Name   string `json:"name"`
	} `json:"moderators"`
	Members []struct {
		UserID              string `json:"userId"`
		Name                string `json:"name"`
		Title               string `json:"title"`
		Position            int    `json:"position"`
		CanManageMembers    bool   `json:"canManageMembers"`
		CanCurate           bool   `json:"canCurate"`
		CanModeratePosts    bool   `json:"canModeratePosts"`
		CanModerateThreads  bool   `json:"canModerateThreads"`
		CanAnnounce         bool   `json:"canAnnounce"`
		CanManagePolls      bool   `json:"canManagePolls"`
		CanSetBoardSettings bool   `json:"canSetBoardSettings"`
	} `json:"members"`
}

type favoriteFolderResponseItem struct {
	ID       string `json:"id"`
	ParentID string `json:"parentId"`
	Name     string `json:"name"`
	Position int    `json:"position"`
}

type favoriteBoardResponseItem struct {
	ID       string `json:"id"`
	FolderID string `json:"folderId"`
	Position int    `json:"position"`
}

type favoriteTreeResponse struct {
	Folders []favoriteFolderResponseItem `json:"folders"`
	Boards  []favoriteBoardResponseItem  `json:"boards"`
}

type categoriesResponse struct {
	Categories []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ParentID    string `json:"parentId"`
		Position    int    `json:"position"`
		Visibility  string `json:"visibility"`
	} `json:"categories"`
}

type communityStatsResponse struct {
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

type communityStatHistoryResponse struct {
	Days []struct {
		Day                 string `json:"day"`
		SnapshotAt          int64  `json:"snapshotAt"`
		TotalUsers          int    `json:"totalUsers"`
		TotalPosts          int    `json:"totalPosts"`
		OnlineUsers         int    `json:"onlineUsers"`
		MaxOnlineUsers      int    `json:"maxOnlineUsers"`
		MaxOnlineAt         int64  `json:"maxOnlineAt"`
		HeadSeq             int64  `json:"headSeq"`
		TotalBoards         int    `json:"totalBoards"`
		TotalThreads        int    `json:"totalThreads"`
		TotalReactions      int    `json:"totalReactions"`
		TotalMail           int    `json:"totalMail"`
		TotalDirectMessages int    `json:"totalDirectMessages"`
		DeltaUsers          int    `json:"deltaUsers"`
		DeltaBoards         int    `json:"deltaBoards"`
		DeltaThreads        int    `json:"deltaThreads"`
		DeltaPosts          int    `json:"deltaPosts"`
		DeltaReactions      int    `json:"deltaReactions"`
		DeltaMail           int    `json:"deltaMail"`
		DeltaDirectMessages int    `json:"deltaDirectMessages"`
	} `json:"days"`
}

type boardRankingsResponse struct {
	Boards []struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		ThreadCount int    `json:"threadCount"`
		PostCount   int    `json:"postCount"`
		LastSeq     int64  `json:"lastSeq"`
	} `json:"boards"`
}

type threadRankingsResponse struct {
	Threads []struct {
		ID            string `json:"id"`
		Board         string `json:"board"`
		BoardName     string `json:"boardName"`
		Title         string `json:"title"`
		PostCount     int    `json:"postCount"`
		ReactionCount int    `json:"reactionCount"`
		Score         int    `json:"score"`
	} `json:"threads"`
}

type replyRankingsResponse struct {
	Replies []struct {
		PostID    string `json:"postId"`
		ThreadID  string `json:"threadId"`
		Board     string `json:"board"`
		BoardName string `json:"boardName"`
		Title     string `json:"title"`
		Author    string `json:"author"`
		Excerpt   string `json:"excerpt"`
		Seq       int64  `json:"seq"`
		CreatedAt int64  `json:"createdAt"`
	} `json:"replies"`
}

type postsResponse struct {
	Posts []struct {
		ID           string `json:"id"`
		Thread       string `json:"thread"`
		Board        string `json:"board"`
		BoardName    string `json:"boardName"`
		ThreadTitle  string `json:"threadTitle"`
		Author       string `json:"author"`
		Body         string `json:"body"`
		ReplyDepth   int    `json:"replyDepth"`
		Marked       bool   `json:"marked"`
		Recommended  bool   `json:"recommended"`
		NoReply      bool   `json:"noReply"`
		TeX          bool   `json:"tex"`
		MailBack     bool   `json:"mailBack"`
		SourcePost   string `json:"sourcePost"`
		SourceThread string `json:"sourceThread"`
		SourceBoard  string `json:"sourceBoard"`
		SourceAuthor string `json:"sourceAuthor"`
		SourceTitle  string `json:"sourceTitle"`
	} `json:"posts"`
}

type userRankingsResponse struct {
	Users []struct {
		UserID            string `json:"userId"`
		Name              string `json:"name"`
		PostsCreated      int    `json:"postsCreated"`
		ReactionsReceived int    `json:"reactionsReceived"`
		LoginCount        int    `json:"loginCount"`
		TrustLevel        int    `json:"trustLevel"`
	} `json:"users"`
}

type blessingRankingsResponse struct {
	Blessings []struct {
		UserID        string `json:"userId"`
		Name          string `json:"name"`
		BlessingCount int    `json:"blessingCount"`
		LastBlessedAt int64  `json:"lastBlessedAt"`
	} `json:"blessings"`
}

type archiveRankingsResponse struct {
	Archives []struct {
		BoardID       string `json:"boardId"`
		BoardName     string `json:"boardName"`
		Kind          string `json:"kind"`
		Path          string `json:"path"`
		EntryCount    int    `json:"entryCount"`
		EditedCount   int    `json:"editedCount"`
		LastUpdatedAt int64  `json:"lastUpdatedAt"`
	} `json:"archives"`
}

type memberApplicationsResponse struct {
	Applications []struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Status string `json:"status"`
		Note   string `json:"note"`
		Title  string `json:"title"`
	} `json:"applications"`
}

type digestEntriesResponse struct {
	Entries []struct {
		ID         string `json:"id"`
		BoardID    string `json:"boardId"`
		BoardName  string `json:"boardName"`
		TargetKind string `json:"targetKind"`
		TargetID   string `json:"targetId"`
		Kind       string `json:"kind"`
		Title      string `json:"title"`
		Path       string `json:"path"`
		BodyEdited bool   `json:"bodyEdited"`
		ThreadID   string `json:"threadId"`
		PostID     string `json:"postId"`
		Author     string `json:"author"`
		Excerpt    string `json:"excerpt"`
	} `json:"entries"`
}

type digestPathTreeResponse struct {
	Nodes []struct {
		Path       string `json:"path"`
		Name       string `json:"name"`
		ParentPath string `json:"parentPath"`
		Kind       string `json:"kind"`
		EntryCount int    `json:"entryCount"`
		ChildCount int    `json:"childCount"`
		Explicit   bool   `json:"explicit"`
	} `json:"nodes"`
}

type mailListResponse struct {
	UnreadCount int `json:"unreadCount"`
	Mail        []struct {
		ID          string   `json:"id"`
		FromName    string   `json:"fromName"`
		ToNames     []string `json:"toNames"`
		Subject     string   `json:"subject"`
		ParentID    string   `json:"parentId"`
		Mailbox     string   `json:"mailbox"`
		Read        bool     `json:"read"`
		Kept        bool     `json:"kept"`
		Excerpt     string   `json:"excerpt"`
		Attachments []struct {
			ID       string `json:"id"`
			MailID   string `json:"mailId"`
			Filename string `json:"filename"`
			Stored   bool   `json:"stored"`
		} `json:"attachments"`
	} `json:"mail"`
}

type mailItemResponse struct {
	ID          string   `json:"id"`
	FromName    string   `json:"fromName"`
	ToNames     []string `json:"toNames"`
	Subject     string   `json:"subject"`
	Body        string   `json:"body"`
	Attachments []struct {
		ID       string `json:"id"`
		MailID   string `json:"mailId"`
		Filename string `json:"filename"`
		Stored   bool   `json:"stored"`
	} `json:"attachments"`
}

type mailUsageResponse struct {
	UserID         string `json:"userId"`
	UsedBytes      int64  `json:"usedBytes"`
	QuotaBytes     int64  `json:"quotaBytes"`
	RemainingBytes int64  `json:"remainingBytes"`
}

type relayDeliveriesResponse struct {
	Deliveries []struct {
		ID         string `json:"id"`
		BoardID    string `json:"boardId"`
		ThreadID   string `json:"threadId"`
		PostID     string `json:"postId"`
		AuthorName string `json:"authorName"`
		Title      string `json:"title"`
		Body       string `json:"body"`
		Status     string `json:"status"`
	} `json:"deliveries"`
}

type mailGroupsResponse struct {
	Groups []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		BuiltIn bool   `json:"builtIn"`
		Members []struct {
			UserID   string `json:"userId"`
			Name     string `json:"name"`
			Position int    `json:"position"`
		} `json:"members"`
	} `json:"groups"`
}

type directConversationsResponse struct {
	UnreadCount   int `json:"unreadCount"`
	Conversations []struct {
		UserID        string `json:"userId"`
		Name          string `json:"name"`
		LastMessageID string `json:"lastMessageId"`
		LastBody      string `json:"lastBody"`
		UnreadCount   int    `json:"unreadCount"`
	} `json:"conversations"`
}

type directMessagesResponse struct {
	Messages []struct {
		ID        string `json:"id"`
		FromName  string `json:"fromName"`
		ToName    string `json:"toName"`
		Body      string `json:"body"`
		Read      bool   `json:"read"`
		Mine      bool   `json:"mine"`
		OtherName string `json:"otherName"`
	} `json:"messages"`
}

type directMessageSettingsResponse struct {
	UserID string `json:"userId"`
	Policy string `json:"policy"`
}

type socialUsersResponse struct {
	Users []struct {
		UserID      string `json:"userId"`
		SessionID   string `json:"sessionId"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Note        string `json:"note"`
		Kind        string `json:"kind"`
		Mutual      bool   `json:"mutual"`
		Ignored     bool   `json:"ignored"`
		Status      string `json:"status"`
		Mode        string `json:"mode"`
		BoardID     string `json:"boardId"`
		BoardName   string `json:"boardName"`
		ThreadID    string `json:"threadId"`
		Location    string `json:"locationLabel"`
		FromHost    string `json:"fromHost"`
		Online      bool   `json:"online"`
		LastSeen    int64  `json:"lastSeen"`
	} `json:"users"`
}

type notificationsResponse struct {
	UnreadCount   int `json:"unreadCount"`
	Notifications []struct {
		ID       string `json:"id"`
		Kind     string `json:"kind"`
		ThreadID string `json:"threadId"`
		PostID   string `json:"postId"`
		Actor    string `json:"actor"`
		Read     bool   `json:"read"`
	} `json:"notifications"`
}

type threadSummariesResponse struct {
	Threads []struct {
		ID                string `json:"id"`
		Board             string `json:"board"`
		BoardName         string `json:"boardName"`
		Author            string `json:"author"`
		AuthorID          string `json:"authorId"`
		Title             string `json:"title"`
		UnreadPosts       int    `json:"unreadPosts"`
		FirstUnreadPostID string `json:"firstUnreadPostId"`
	} `json:"threads"`
}

func TestHTTPBoardFavoritesLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	token := registerUser(t, handler, "alice")

	favorites := boardsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites", token, nil, &favorites); status != http.StatusOK {
		t.Fatalf("list favorites status: %d", status)
	}
	if len(favorites.Boards) != 1 || favorites.Boards[0].ID != "general" {
		t.Fatalf("expected general default favorite for new user, got %+v", favorites.Boards)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/general/favorite", token, map[string]bool{
		"favorite": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("favorite board status: %d error=%+v", status, ack.Error)
	}
	if !ack.OK || ack.Result == nil || ack.Result.ID != "general" {
		t.Fatalf("unexpected favorite ack: %+v", ack)
	}

	favorites = boardsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites", token, nil, &favorites); status != http.StatusOK {
		t.Fatalf("list favorites after add status: %d", status)
	}
	if len(favorites.Boards) != 1 || favorites.Boards[0].ID != "general" {
		t.Fatalf("expected general favorite, got %+v", favorites.Boards)
	}

	ack = ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/boards/general/favorite", token, nil, &ack); status != http.StatusCreated {
		t.Fatalf("unfavorite board status: %d error=%+v", status, ack.Error)
	}

	favorites = boardsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites", token, nil, &favorites); status != http.StatusOK {
		t.Fatalf("list favorites after remove status: %d", status)
	}
	if len(favorites.Boards) != 0 {
		t.Fatalf("expected empty favorites after remove, got %+v", favorites.Boards)
	}
}

func TestHTTPFavoriteFoldersLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	createBoard := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "tech",
		"name":        "Tech",
		"description": "Technology",
	}, &createBoard); status != http.StatusCreated {
		t.Fatalf("create tech board status: %d error=%+v", status, createBoard.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "life",
		"name":        "Life",
		"description": "Life",
	}, &createBoard); status != http.StatusCreated {
		t.Fatalf("create life board status: %d error=%+v", status, createBoard.Error)
	}

	folderAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders", aliceToken, map[string]string{
		"name": "Work",
	}, &folderAck); status != http.StatusCreated {
		t.Fatalf("create folder status: %d error=%+v", status, folderAck.Error)
	}
	folderID := folderAck.Result.ID

	favAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/tech/favorite", aliceToken, map[string]any{
		"favorite": true,
		"folderId": folderID,
	}, &favAck); status != http.StatusCreated {
		t.Fatalf("favorite tech in folder status: %d error=%+v", status, favAck.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/life/favorite", aliceToken, map[string]any{
		"folderId": folderID,
		"position": 0,
	}, &favAck); status != http.StatusCreated {
		t.Fatalf("move life favorite status: %d error=%+v", status, favAck.Error)
	}

	tree := favoriteTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites/tree", aliceToken, nil, &tree); status != http.StatusOK {
		t.Fatalf("list favorite tree status: %d", status)
	}
	if len(tree.Folders) != 1 || tree.Folders[0].ID != folderID {
		t.Fatalf("expected favorite folder, got %+v", tree.Folders)
	}
	if len(tree.Boards) != 3 || tree.Boards[0].ID != "general" || tree.Boards[1].ID != "life" || tree.Boards[2].ID != "tech" {
		t.Fatalf("expected foldered favorites in manual order, got %+v", tree.Boards)
	}

	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/favorites/folders/"+folderID, aliceToken, map[string]string{
		"name": "Campus",
	}, &folderAck); status != http.StatusCreated {
		t.Fatalf("rename favorite folder status: %d error=%+v", status, folderAck.Error)
	}

	tree = favoriteTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites/tree", aliceToken, nil, &tree); status != http.StatusOK {
		t.Fatalf("list favorite tree after rename status: %d", status)
	}
	if len(tree.Folders) != 1 || tree.Folders[0].Name != "Campus" {
		t.Fatalf("expected renamed folder, got %+v", tree.Folders)
	}

	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/boards/favorites/folders/"+folderID, aliceToken, nil, &folderAck); status != http.StatusCreated {
		t.Fatalf("delete favorite folder status: %d error=%+v", status, folderAck.Error)
	}

	tree = favoriteTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites/tree", aliceToken, nil, &tree); status != http.StatusOK {
		t.Fatalf("list favorite tree after delete status: %d", status)
	}
	if len(tree.Folders) != 0 {
		t.Fatalf("expected folder deletion, got %+v", tree.Folders)
	}
	for _, board := range tree.Boards {
		if board.FolderID != "" {
			t.Fatalf("expected boards moved to root after folder deletion, got %+v", tree.Boards)
		}
	}
}

func TestHTTPBoardDirectoryHierarchy(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":       "orphan",
		"name":     "Orphan",
		"parentId": "missing",
	}, &ack); status != http.StatusNotFound {
		t.Fatalf("expected missing parent to return 404, got %d error=%+v", status, ack.Error)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]any{
		"id":          "clubs",
		"name":        "Clubs",
		"description": "Campus clubs",
		"parentId":    "general",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create clubs board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]any{
		"id":          "music",
		"name":        "Music",
		"description": "Music club",
		"parentId":    "clubs",
		"position":    0,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create music board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]any{
		"id":       "sports",
		"name":     "Sports",
		"parentId": "general",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create sports board status: %d error=%+v", status, ack.Error)
	}

	categories := categoriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/categories", adminToken, nil, &categories); status != http.StatusOK {
		t.Fatalf("list categories status: %d", status)
	}

	byID := map[string]struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ParentID    string `json:"parentId"`
		Position    int    `json:"position"`
		Visibility  string `json:"visibility"`
	}{}
	for _, category := range categories.Categories {
		byID[category.ID] = category
	}
	if byID["clubs"].ParentID != "general" || byID["clubs"].Position != 0 {
		t.Fatalf("expected clubs under general at position 0, got %+v", byID["clubs"])
	}
	if byID["music"].ParentID != "clubs" || byID["music"].Position != 0 {
		t.Fatalf("expected music under clubs at position 0, got %+v", byID["music"])
	}
	if byID["sports"].ParentID != "general" || byID["sports"].Position != 1 {
		t.Fatalf("expected sports under general at appended position, got %+v", byID["sports"])
	}
	if _, ok := byID["orphan"]; ok {
		t.Fatalf("rejected board should not be visible as a category, got %+v", byID["orphan"])
	}

	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/categories/sports", adminToken, map[string]any{
		"name":        "Athletics",
		"description": "Sports desk",
		"parentId":    "clubs",
		"visibility":  "staff",
	}, &categories); status != http.StatusOK {
		t.Fatalf("update sports category status: %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/categories/clubs", adminToken, map[string]any{
		"parentId": "music",
	}, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected category cycle rejection, got %d", status)
	}
	categories = categoriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/categories", adminToken, nil, &categories); status != http.StatusOK {
		t.Fatalf("list admin categories after update status: %d", status)
	}
	byID = map[string]struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		ParentID    string `json:"parentId"`
		Position    int    `json:"position"`
		Visibility  string `json:"visibility"`
	}{}
	for _, category := range categories.Categories {
		byID[category.ID] = category
	}
	if byID["sports"].Name != "Athletics" || byID["sports"].ParentID != "clubs" || byID["sports"].Visibility != "staff" {
		t.Fatalf("expected updated sports category, got %+v", byID["sports"])
	}
	aliceToken := registerUser(t, handler, "alice")
	categories = categoriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/categories", aliceToken, nil, &categories); status != http.StatusOK {
		t.Fatalf("list normal categories status: %d", status)
	}
	for _, category := range categories.Categories {
		if category.ID == "sports" {
			t.Fatalf("staff category should be hidden from normal user, got %+v", categories.Categories)
		}
	}
}

func TestHTTPFavoriteTreeImportExportAndFolderReadMarkers(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	ack := ackResponse{}
	for _, board := range []string{"tech", "life"} {
		if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
			"id":   board,
			"name": board,
		}, &ack); status != http.StatusCreated {
			t.Fatalf("create %s board status: %d error=%+v", board, status, ack.Error)
		}
	}

	work := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders", aliceToken, map[string]string{
		"name": "Work",
	}, &work); status != http.StatusCreated {
		t.Fatalf("create work folder status: %d error=%+v", status, work.Error)
	}
	child := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders", aliceToken, map[string]string{
		"name":     "Child",
		"parentId": work.Result.ID,
	}, &child); status != http.StatusCreated {
		t.Fatalf("create child folder status: %d error=%+v", status, child.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/tech/favorite", aliceToken, map[string]any{
		"favorite": true,
		"folderId": work.Result.ID,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("favorite tech status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/life/favorite", aliceToken, map[string]any{
		"favorite": true,
		"folderId": child.Result.ID,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("favorite life status: %d error=%+v", status, ack.Error)
	}

	tech := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/tech/threads", bobToken, map[string]string{
		"title": "Tech unread",
		"body":  "first",
	}, &tech); status != http.StatusCreated {
		t.Fatalf("create tech thread status: %d error=%+v", status, tech.Error)
	}
	life := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/life/threads", bobToken, map[string]string{
		"title": "Life unread",
		"body":  "first",
	}, &life); status != http.StatusCreated {
		t.Fatalf("create life thread status: %d error=%+v", status, life.Error)
	}

	unread := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread?favorites=1&folder="+work.Result.ID, aliceToken, nil, &unread); status != http.StatusOK {
		t.Fatalf("folder unread status: %d", status)
	}
	if !hasHTTPThread(unread, tech.Result.ID) || !hasHTTPThread(unread, life.Result.ID) {
		t.Fatalf("expected folder unread to include descendant favorite boards, got %+v", unread.Threads)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders/"+work.Result.ID+"/read", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("mark favorite folder read status: %d error=%+v", status, ack.Error)
	}
	unread = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread?favorites=1&folder="+work.Result.ID, aliceToken, nil, &unread); status != http.StatusOK {
		t.Fatalf("folder unread after mark status: %d", status)
	}
	if hasHTTPThread(unread, tech.Result.ID) || hasHTTPThread(unread, life.Result.ID) {
		t.Fatalf("expected folder mark-read to clear scoped unread, got %+v", unread.Threads)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders/"+work.Result.ID+"/read/restore", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("restore favorite folder read status: %d error=%+v", status, ack.Error)
	}
	unread = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread?favorites=1&folder="+work.Result.ID, aliceToken, nil, &unread); status != http.StatusOK {
		t.Fatalf("folder unread after restore status: %d", status)
	}
	if !hasHTTPThread(unread, tech.Result.ID) || !hasHTTPThread(unread, life.Result.ID) {
		t.Fatalf("expected folder restore to restore scoped unread, got %+v", unread.Threads)
	}

	exported := favoriteTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/favorites/export", aliceToken, nil, &exported); status != http.StatusOK {
		t.Fatalf("export favorite tree status: %d", status)
	}
	imported := favoriteTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/import", carolToken, map[string]any{
		"folders": exported.Folders,
		"boards":  exported.Boards,
		"replace": true,
	}, &imported); status != http.StatusCreated {
		t.Fatalf("import favorite tree status: %d", status)
	}
	importedWork := httpFavoriteFolderByName(imported, "Work")
	importedChild := httpFavoriteFolderByName(imported, "Child")
	if importedWork == nil || importedChild == nil {
		t.Fatalf("expected imported folders, got %+v", imported.Folders)
	}
	if importedChild.ParentID != importedWork.ID {
		t.Fatalf("expected imported child folder parent remapped to imported work folder, got %+v", imported.Folders)
	}
	if got := httpFavoriteFolderForBoard(imported, "tech"); got != importedWork.ID {
		t.Fatalf("expected imported tech favorite in work folder %q, got %q in %+v", importedWork.ID, got, imported.Boards)
	}
	if got := httpFavoriteFolderForBoard(imported, "life"); got != importedChild.ID {
		t.Fatalf("expected imported life favorite in child folder %q, got %q in %+v", importedChild.ID, got, imported.Boards)
	}
}

func TestHTTPCommunityRankingsAndStats(t *testing.T) {
	c, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	bobToken = loginUser(t, handler, "bob")

	ack := ackResponse{}
	for _, board := range []string{"tech", "life", "secret"} {
		if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
			"id":   board,
			"name": board,
		}, &ack); status != http.StatusCreated {
			t.Fatalf("create %s board status: %d error=%+v", board, status, ack.Error)
		}
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set secret member-read status: %d error=%+v", status, ack.Error)
	}

	hot := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/tech/threads", bobToken, map[string]string{
		"title": "Hot topic",
		"body":  "first",
	}, &hot); status != http.StatusCreated {
		t.Fatalf("create hot topic status: %d error=%+v", status, hot.Error)
	}
	hotReply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+hot.Result.ID+"/posts", bobToken, map[string]string{
		"body": "second",
	}, &hotReply); status != http.StatusCreated {
		t.Fatalf("reply hot topic status: %d error=%+v", status, hotReply.Error)
	}
	life := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/life/threads", aliceToken, map[string]string{
		"title": "Quiet topic",
		"body":  "first",
	}, &life); status != http.StatusCreated {
		t.Fatalf("create life topic status: %d error=%+v", status, life.Error)
	}
	secret := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", adminToken, map[string]string{
		"title": "Private topic",
		"body":  "hidden",
	}, &secret); status != http.StatusCreated {
		t.Fatalf("create secret topic status: %d error=%+v", status, secret.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+secret.Result.ID+"/posts", adminToken, map[string]string{
		"body": "classified reply",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("reply secret topic status: %d error=%+v", status, ack.Error)
	}

	hotPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+hot.Result.ID+"/posts", aliceToken, nil, &hotPosts); status != http.StatusOK {
		t.Fatalf("list hot posts status: %d", status)
	}
	if len(hotPosts.Posts) == 0 {
		t.Fatal("expected hot topic posts")
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+hotPosts.Posts[0].ID+"/react", aliceToken, map[string]string{
		"emoji": "+1",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("react hot post status: %d error=%+v", status, ack.Error)
	}
	archivePost := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+hotPosts.Posts[0].ID+"/digest", adminToken, map[string]string{
		"kind":  "archive",
		"title": "Hot archive post",
		"path":  "guide",
	}, &archivePost); status != http.StatusCreated {
		t.Fatalf("curate archive post status: %d error=%+v", status, archivePost.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/digest/"+archivePost.Result.ID+"/body", adminToken, map[string]string{
		"body": "Edited archive ranking copy",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set archive ranking body status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+hot.Result.ID+"/digest", adminToken, map[string]string{
		"kind":  "archive",
		"title": "Hot archive thread",
		"path":  "guide",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("curate archive thread status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+secret.Result.ID+"/digest", adminToken, map[string]string{
		"kind":  "archive",
		"title": "Private archive thread",
		"path":  "private",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("curate secret archive thread status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", aliceToken, map[string]string{
		"status": "active",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set presence status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"status": "active",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set bob presence status: %d error=%+v", status, ack.Error)
	}

	stats := communityStatsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/stats/community", aliceToken, nil, &stats); status != http.StatusOK {
		t.Fatalf("community stats status: %d", status)
	}
	if stats.TotalUsers != 3 || stats.TotalBoards != 4 || stats.TotalThreads != 3 || stats.TotalPosts != 5 || stats.TotalReactions != 1 || stats.OnlineUsers != 2 || stats.MaxOnlineUsers != 2 || stats.MaxOnlineAt == 0 || stats.HeadSeq == 0 {
		t.Fatalf("unexpected community stats: %+v", stats)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"status": "offline",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set bob offline presence status: %d error=%+v", status, ack.Error)
	}
	stats = communityStatsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/stats/community", aliceToken, nil, &stats); status != http.StatusOK {
		t.Fatalf("community stats after offline status: %d", status)
	}
	if stats.OnlineUsers != 1 || stats.MaxOnlineUsers != 2 || stats.MaxOnlineAt == 0 {
		t.Fatalf("expected max-online history to preserve peak after offline, got %+v", stats)
	}
	previousAt := time.Now().UTC().Add(-24 * time.Hour)
	if _, err := c.DB.Exec(`INSERT INTO community_stat_history (
		day, snapshot_at, total_users, total_boards, total_threads, total_posts,
		total_reactions, total_mail, total_direct_messages, online_users,
		max_online_users, max_online_at, head_seq
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		previousAt.Format("2006-01-02"), previousAt.UnixMilli(),
		2, 3, 1, 2, 0, 0, 0, 0, 1, previousAt.UnixMilli(), 1,
	); err != nil {
		t.Fatal(err)
	}
	history := communityStatHistoryResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/stats/community/history?limit=7", aliceToken, nil, &history); status != http.StatusOK {
		t.Fatalf("community stat history status: %d", status)
	}
	if len(history.Days) != 2 || history.Days[0].OnlineUsers != 1 || history.Days[0].MaxOnlineUsers != 2 || history.Days[0].MaxOnlineAt == 0 || history.Days[0].TotalPosts != 5 {
		t.Fatalf("expected daily stat history with preserved max-online peak, got %+v", history)
	}
	if history.Days[0].DeltaUsers != 1 || history.Days[0].DeltaBoards != 1 || history.Days[0].DeltaThreads != 2 || history.Days[0].DeltaPosts != 3 || history.Days[0].DeltaReactions != 1 || history.Days[0].DeltaMail != 0 || history.Days[0].DeltaDirectMessages != 0 {
		t.Fatalf("expected newest daily stat history row to include deltas, got %+v", history.Days[0])
	}
	if history.Days[1].DeltaUsers != 0 || history.Days[1].DeltaPosts != 0 || history.Days[1].DeltaReactions != 0 {
		t.Fatalf("expected oldest fetched daily stat history row to have zero deltas without an older comparison row, got %+v", history.Days[1])
	}

	boards := boardRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/boards", aliceToken, nil, &boards); status != http.StatusOK {
		t.Fatalf("board rankings status: %d", status)
	}
	if len(boards.Boards) == 0 || boards.Boards[0].ID != "tech" || boards.Boards[0].PostCount != 2 {
		t.Fatalf("expected tech to lead board rankings, got %+v", boards.Boards)
	}
	for _, board := range boards.Boards {
		if board.ID == "secret" {
			t.Fatalf("ordinary user should not see secret board ranking, got %+v", boards.Boards)
		}
	}

	adminBoards := boardRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/boards", adminToken, nil, &adminBoards); status != http.StatusOK {
		t.Fatalf("admin board rankings status: %d", status)
	}
	secretVisible := false
	for _, board := range adminBoards.Boards {
		if board.ID == "secret" {
			secretVisible = true
		}
	}
	if !secretVisible {
		t.Fatalf("admin should see secret board ranking, got %+v", adminBoards.Boards)
	}

	archives := archiveRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/archive?kind=archive", aliceToken, nil, &archives); status != http.StatusOK {
		t.Fatalf("archive rankings status: %d", status)
	}
	if len(archives.Archives) == 0 || archives.Archives[0].BoardID != "tech" || archives.Archives[0].Path != "guide" || archives.Archives[0].EntryCount != 2 || archives.Archives[0].EditedCount != 1 {
		t.Fatalf("expected tech guide to lead archive rankings, got %+v", archives.Archives)
	}
	for _, archive := range archives.Archives {
		if archive.BoardID == "secret" {
			t.Fatalf("ordinary user should not see secret archive ranking, got %+v", archives.Archives)
		}
	}
	adminArchives := archiveRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/archive?kind=archive", adminToken, nil, &adminArchives); status != http.StatusOK {
		t.Fatalf("admin archive rankings status: %d", status)
	}
	secretArchiveVisible := false
	for _, archive := range adminArchives.Archives {
		if archive.BoardID == "secret" && archive.Path == "private" {
			secretArchiveVisible = true
		}
	}
	if !secretArchiveVisible {
		t.Fatalf("admin should see secret archive ranking, got %+v", adminArchives.Archives)
	}

	threads := threadRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("thread rankings status: %d", status)
	}
	if len(threads.Threads) == 0 || threads.Threads[0].ID != hot.Result.ID || threads.Threads[0].ReactionCount != 1 {
		t.Fatalf("expected hot topic to lead thread rankings, got %+v", threads.Threads)
	}
	for _, thread := range threads.Threads {
		if thread.ID == secret.Result.ID {
			t.Fatalf("ordinary user should not see secret thread ranking, got %+v", threads.Threads)
		}
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/threads?board=secret", aliceToken, nil, &threads); status != http.StatusForbidden {
		t.Fatalf("expected direct secret thread ranking to be forbidden, got %d", status)
	}

	replies := replyRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/replies", aliceToken, nil, &replies); status != http.StatusOK {
		t.Fatalf("reply rankings status: %d", status)
	}
	if len(replies.Replies) != 1 || replies.Replies[0].PostID != hotReply.Result.ID || replies.Replies[0].ThreadID != hot.Result.ID || !strings.Contains(replies.Replies[0].Excerpt, "second") {
		t.Fatalf("expected latest public reply only, got %+v", replies.Replies)
	}
	adminReplies := replyRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/replies", adminToken, nil, &adminReplies); status != http.StatusOK {
		t.Fatalf("admin reply rankings status: %d", status)
	}
	if len(adminReplies.Replies) == 0 || adminReplies.Replies[0].ThreadID != secret.Result.ID || !strings.Contains(adminReplies.Replies[0].Excerpt, "classified reply") {
		t.Fatalf("expected admin latest reply to include private board, got %+v", adminReplies.Replies)
	}

	lifeThreads := threadRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/threads?board=life", aliceToken, nil, &lifeThreads); status != http.StatusOK {
		t.Fatalf("life thread rankings status: %d", status)
	}
	if len(lifeThreads.Threads) != 1 || lifeThreads.Threads[0].ID != life.Result.ID {
		t.Fatalf("expected life board scoped ranking, got %+v", lifeThreads.Threads)
	}

	users := userRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/users", aliceToken, nil, &users); status != http.StatusOK {
		t.Fatalf("user rankings status: %d", status)
	}
	if len(users.Users) == 0 || users.Users[0].Name != "bob" || users.Users[0].PostsCreated != 2 || users.Users[0].ReactionsReceived != 1 || users.Users[0].LoginCount != 2 {
		t.Fatalf("expected bob to lead user rankings, got %+v", users.Users)
	}

	forbiddenSnapshot := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/stats/community/snapshot", aliceToken, map[string]string{
		"date": "2026-06-04",
	}, &forbiddenSnapshot); status != http.StatusForbidden {
		t.Fatalf("expected non-admin stats snapshot publish forbidden, got %d error=%+v", status, forbiddenSnapshot.Error)
	}
	snapshot := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/stats/community/snapshot", adminToken, map[string]string{
		"date": "2026-06-04",
	}, &snapshot); status != http.StatusCreated {
		t.Fatalf("publish stats snapshot status: %d error=%+v", status, snapshot.Error)
	}
	if snapshot.Result == nil || snapshot.Result.ID == "" {
		t.Fatalf("expected generated stats snapshot thread id, got %+v", snapshot)
	}
	systemThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/BBSLists/threads", aliceToken, nil, &systemThreads); status != http.StatusOK {
		t.Fatalf("list BBSLists threads status: %d", status)
	}
	if len(systemThreads.Threads) != 1 || systemThreads.Threads[0].ID != snapshot.Result.ID || !strings.Contains(systemThreads.Threads[0].Title, "2026-06-04") {
		t.Fatalf("expected one generated stats thread, got %+v", systemThreads.Threads)
	}
	systemPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+snapshot.Result.ID+"/posts", aliceToken, nil, &systemPosts); status != http.StatusOK {
		t.Fatalf("list BBSLists stats posts status: %d", status)
	}
	if len(systemPosts.Posts) != 1 {
		t.Fatalf("expected one generated stats post, got %+v", systemPosts.Posts)
	}
	body := systemPosts.Posts[0].Body
	for _, want := range []string{"Total users: 3", "Total posts: 5", "Max online users: 2", "Recent daily history", "3 users (+1)", "5 posts (+3)", "1 reactions (+1)", "max 2 online", "Active boards", "(tech): 2 posts", "Hot threads", "Hot topic", "Latest replies", "second", "Top users", "bob", "Archive paths", "guide"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected stats snapshot body to contain %q, got:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"Secret", "Private topic", "classified reply", "private"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("expected stats snapshot body to hide private %q, got:\n%s", forbidden, body)
		}
	}
	again := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/stats/community/snapshot", adminToken, map[string]string{
		"date": "2026-06-04",
	}, &again); status != http.StatusCreated {
		t.Fatalf("repeat stats snapshot status: %d error=%+v", status, again.Error)
	}
	if again.Result == nil || again.Result.ID != snapshot.Result.ID {
		t.Fatalf("expected repeated snapshot publish to reuse thread %q, got %+v", snapshot.Result.ID, again)
	}
	systemThreads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/BBSLists/threads", aliceToken, nil, &systemThreads); status != http.StatusOK {
		t.Fatalf("list BBSLists threads after repeat status: %d", status)
	}
	if len(systemThreads.Threads) != 1 {
		t.Fatalf("expected repeated snapshot publish not to duplicate thread, got %+v", systemThreads.Threads)
	}
}

func TestHTTPPublishSystemNoticeCreatesPublicNoticeBoard(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	forbidden := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/notices", aliceToken, map[string]string{
		"title": "Campus notice",
		"body":  "Maintenance tonight",
	}, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected non-admin system notice publish forbidden, got %d error=%+v", status, forbidden.Error)
	}
	invalid := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/notices", adminToken, map[string]string{
		"board": "Filter",
		"title": "Filtered",
		"body":  "not a public notice board",
	}, &invalid); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected invalid notice board validation error, got %d error=%+v", status, invalid.Error)
	}

	notice := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/notices", adminToken, map[string]string{
		"title":  "Campus notice",
		"body":   "Maintenance tonight at 23:00.",
		"source": "operator broadcast",
	}, &notice); status != http.StatusCreated {
		t.Fatalf("publish system notice status: %d error=%+v", status, notice.Error)
	}
	if notice.Result == nil || notice.Result.ID == "" {
		t.Fatalf("expected generated notice thread id, got %+v", notice)
	}

	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/notepad/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list notepad threads status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].ID != notice.Result.ID || threads.Threads[0].Title != "Campus notice" {
		t.Fatalf("expected generated public notepad thread, got %+v", threads.Threads)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+notice.Result.ID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list notepad posts status: %d", status)
	}
	if len(posts.Posts) != 1 || posts.Posts[0].Author != "admin" {
		t.Fatalf("expected one admin-authored system notice post, got %+v", posts.Posts)
	}
	for _, want := range []string{"# Campus notice", "Notice board: notepad", "Actor: admin", "Source: operator broadcast", "Maintenance tonight at 23:00.", "Generated public system notice"} {
		if !strings.Contains(posts.Posts[0].Body, want) {
			t.Fatalf("expected notice body to contain %q, got:\n%s", want, posts.Posts[0].Body)
		}
	}
}

func TestHTTPBlessUserCreatesBlessingBoardAndRankings(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	self := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/alice/bless", aliceToken, map[string]string{
		"message": "self boost",
	}, &self); status != http.StatusUnprocessableEntity {
		t.Fatalf("expected self blessing validation error, got %d error=%+v", status, self.Error)
	}

	first := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/bob/bless", aliceToken, map[string]string{
		"message": "Good luck on finals.",
	}, &first); status != http.StatusCreated {
		t.Fatalf("bless bob status: %d error=%+v", status, first.Error)
	}
	if first.Result == nil || first.Result.ID == "" {
		t.Fatalf("expected blessing id, got %+v", first)
	}
	second := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/users/bob/bless", carolToken, map[string]string{}, &second); status != http.StatusCreated {
		t.Fatalf("second bless bob status: %d error=%+v", status, second.Error)
	}

	rankings := blessingRankingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/rankings/blessings", bobToken, nil, &rankings); status != http.StatusOK {
		t.Fatalf("blessing rankings status: %d", status)
	}
	if len(rankings.Blessings) == 0 || rankings.Blessings[0].Name != "bob" || rankings.Blessings[0].BlessingCount != 2 || rankings.Blessings[0].LastBlessedAt == 0 {
		t.Fatalf("expected bob to lead blessing rankings, got %+v", rankings.Blessings)
	}

	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/Blessing/threads", bobToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list Blessing threads status: %d", status)
	}
	if len(threads.Threads) != 2 || !strings.Contains(threads.Threads[1].Title, "alice -> bob") {
		t.Fatalf("expected generated Blessing threads, got %+v", threads.Threads)
	}
	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threads.Threads[1].ID+"/posts", bobToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list Blessing posts status: %d", status)
	}
	if len(posts.Posts) != 1 || !strings.Contains(posts.Posts[0].Body, "Good luck on finals.") || !strings.Contains(posts.Posts[0].Body, "Generated public blessing record") {
		t.Fatalf("expected generated blessing post, got %+v", posts.Posts)
	}
}

func TestHTTPSiteWideAndFavoriteFolderUnreadThreads(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	for _, board := range []string{"tech", "life", "secret"} {
		if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
			"id":   board,
			"name": board,
		}, &ack); status != http.StatusCreated {
			t.Fatalf("create %s board status: %d error=%+v", board, status, ack.Error)
		}
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set secret member-read status: %d error=%+v", status, ack.Error)
	}

	work := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders", aliceToken, map[string]string{
		"name": "Work",
	}, &work); status != http.StatusCreated {
		t.Fatalf("create work folder status: %d error=%+v", status, work.Error)
	}
	child := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/favorites/folders", aliceToken, map[string]string{
		"name":     "Child",
		"parentId": work.Result.ID,
	}, &child); status != http.StatusCreated {
		t.Fatalf("create child folder status: %d error=%+v", status, child.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/tech/favorite", aliceToken, map[string]any{
		"favorite": true,
		"folderId": work.Result.ID,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("favorite tech status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/life/favorite", aliceToken, map[string]any{
		"favorite": true,
		"folderId": child.Result.ID,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("favorite life status: %d error=%+v", status, ack.Error)
	}

	tech := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/tech/threads", bobToken, map[string]string{
		"title": "Tech unread",
		"body":  "first",
	}, &tech); status != http.StatusCreated {
		t.Fatalf("create tech thread status: %d error=%+v", status, tech.Error)
	}
	life := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/life/threads", bobToken, map[string]string{
		"title": "Life unread",
		"body":  "first",
	}, &life); status != http.StatusCreated {
		t.Fatalf("create life thread status: %d error=%+v", status, life.Error)
	}
	secret := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", adminToken, map[string]string{
		"title": "Secret unread",
		"body":  "hidden",
	}, &secret); status != http.StatusCreated {
		t.Fatalf("create secret thread status: %d error=%+v", status, secret.Error)
	}

	siteWide := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread", aliceToken, nil, &siteWide); status != http.StatusOK {
		t.Fatalf("site-wide unread status: %d", status)
	}
	if !hasHTTPThread(siteWide, tech.Result.ID) || !hasHTTPThread(siteWide, life.Result.ID) || hasHTTPThread(siteWide, secret.Result.ID) {
		t.Fatalf("expected site-wide visible unread threads only, got %+v", siteWide.Threads)
	}
	if got := httpBoardNameForThread(siteWide, tech.Result.ID); got != "tech" {
		t.Fatalf("expected board name on unread thread, got %q in %+v", got, siteWide.Threads)
	}

	folderUnread := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread?folder="+work.Result.ID, aliceToken, nil, &folderUnread); status != http.StatusOK {
		t.Fatalf("folder unread status: %d", status)
	}
	if !hasHTTPThread(folderUnread, tech.Result.ID) || !hasHTTPThread(folderUnread, life.Result.ID) {
		t.Fatalf("expected folder unread to include descendant favorite boards, got %+v", folderUnread.Threads)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+tech.Result.ID+"/read", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("mark tech thread read status: %d error=%+v", status, ack.Error)
	}
	folderUnread = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread?favorites=1&folder="+work.Result.ID, aliceToken, nil, &folderUnread); status != http.StatusOK {
		t.Fatalf("folder unread after mark status: %d", status)
	}
	if hasHTTPThread(folderUnread, tech.Result.ID) || !hasHTTPThread(folderUnread, life.Result.ID) {
		t.Fatalf("expected marking tech read to leave only life unread in folder traversal, got %+v", folderUnread.Threads)
	}

	adminUnread := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/unread", adminToken, nil, &adminUnread); status != http.StatusOK {
		t.Fatalf("admin unread status: %d", status)
	}
	if !hasHTTPThread(adminUnread, secret.Result.ID) {
		t.Fatalf("expected admin to see secret unread thread, got %+v", adminUnread.Threads)
	}
}

func TestHTTPReadableAuthorPostsRespectBoardReadPolicy(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	for _, board := range []string{"tech", "life", "secret"} {
		if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
			"id":   board,
			"name": board,
		}, &ack); status != http.StatusCreated {
			t.Fatalf("create %s board status: %d error=%+v", board, status, ack.Error)
		}
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set secret member-read status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/secret/members/bob", adminToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("add bob to secret board status: %d error=%+v", status, ack.Error)
	}

	tech := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/tech/threads", bobToken, map[string]string{
		"title": "Tech notes",
		"body":  "tech first",
	}, &tech); status != http.StatusCreated {
		t.Fatalf("create tech topic status: %d error=%+v", status, tech.Error)
	}
	life := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/life/threads", bobToken, map[string]string{
		"title": "Life notes",
		"body":  "life first",
	}, &life); status != http.StatusCreated {
		t.Fatalf("create life topic status: %d error=%+v", status, life.Error)
	}
	secret := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", bobToken, map[string]string{
		"title": "Secret notes",
		"body":  "secret first",
	}, &secret); status != http.StatusCreated {
		t.Fatalf("create secret topic status: %d error=%+v", status, secret.Error)
	}

	publicPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/users/bob/posts", "", nil, &publicPosts); status != http.StatusOK {
		t.Fatalf("public user posts status: %d", status)
	}
	if !hasHTTPPost(publicPosts, tech.Result.ID) || !hasHTTPPost(publicPosts, life.Result.ID) || hasHTTPPost(publicPosts, secret.Result.ID) {
		t.Fatalf("expected public user posts to hide member-read board posts, got %+v", publicPosts)
	}
	if got := httpPostThreadTitle(publicPosts, tech.Result.ID); got != "Tech notes" {
		t.Fatalf("expected public posts to include thread title, got %q in %+v", got, publicPosts)
	}

	alicePosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/authors/bob/posts", aliceToken, nil, &alicePosts); status != http.StatusOK {
		t.Fatalf("alice author posts status: %d", status)
	}
	if !hasHTTPPost(alicePosts, tech.Result.ID) || !hasHTTPPost(alicePosts, life.Result.ID) || hasHTTPPost(alicePosts, secret.Result.ID) {
		t.Fatalf("expected alice author posts to hide member-read board posts, got %+v", alicePosts)
	}

	adminPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/authors/bob/posts", adminToken, nil, &adminPosts); status != http.StatusOK {
		t.Fatalf("admin author posts status: %d", status)
	}
	if !hasHTTPPost(adminPosts, secret.Result.ID) {
		t.Fatalf("expected admin author posts to include member-read board posts, got %+v", adminPosts)
	}

	aliceSearch := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/search?q=first", aliceToken, nil, &aliceSearch); status != http.StatusOK {
		t.Fatalf("alice global search status: %d", status)
	}
	if !hasHTTPPost(aliceSearch, tech.Result.ID) || !hasHTTPPost(aliceSearch, life.Result.ID) || hasHTTPPost(aliceSearch, secret.Result.ID) {
		t.Fatalf("expected alice global search to hide member-read board posts, got %+v", aliceSearch)
	}
	bobSearch := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/search?q=first", bobToken, nil, &bobSearch); status != http.StatusOK {
		t.Fatalf("bob global search status: %d", status)
	}
	if !hasHTTPPost(bobSearch, secret.Result.ID) {
		t.Fatalf("expected board member search to include member-read board posts, got %+v", bobSearch)
	}
	adminSearch := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/search?q=first", adminToken, nil, &adminSearch); status != http.StatusOK {
		t.Fatalf("admin global search status: %d", status)
	}
	if !hasHTTPPost(adminSearch, secret.Result.ID) {
		t.Fatalf("expected admin search to include member-read board posts, got %+v", adminSearch)
	}
}

func TestHTTPPostReplyTreeAndReadPolicy(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "Reply tree",
		"body":  "root",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create reply-tree topic status: %d error=%+v", status, thread.Error)
	}
	rootPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, nil, &rootPosts); status != http.StatusOK {
		t.Fatalf("list root posts status: %d", status)
	}
	if len(rootPosts.Posts) != 1 {
		t.Fatalf("expected one root post, got %+v", rootPosts.Posts)
	}
	rootID := rootPosts.Posts[0].ID
	firstReply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, map[string]string{
		"replyTo": rootID,
		"body":    "first reply",
	}, &firstReply); status != http.StatusCreated {
		t.Fatalf("create first reply status: %d error=%+v", status, firstReply.Error)
	}
	secondReply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, map[string]string{
		"replyTo": rootID,
		"body":    "second reply",
	}, &secondReply); status != http.StatusCreated {
		t.Fatalf("create second reply status: %d error=%+v", status, secondReply.Error)
	}
	unrelated := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, map[string]string{
		"body": "not in reply tree",
	}, &unrelated); status != http.StatusCreated {
		t.Fatalf("create unrelated post status: %d error=%+v", status, unrelated.Error)
	}

	tree := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/posts/"+rootID+"/reply-tree", aliceToken, nil, &tree); status != http.StatusOK {
		t.Fatalf("reply tree status: %d", status)
	}
	if !hasHTTPPostID(tree, rootID) || !hasHTTPPostID(tree, firstReply.Result.ID) || !hasHTTPPostID(tree, secondReply.Result.ID) || hasHTTPPostID(tree, unrelated.Result.ID) {
		t.Fatalf("expected reply tree to include root and replies only, got %+v", tree.Posts)
	}
	if got := httpPostReplyDepth(tree, firstReply.Result.ID); got != 1 {
		t.Fatalf("expected reply depth 1, got %d in %+v", got, tree.Posts)
	}
	if got := httpPostThreadTitleForPost(tree, rootID); got != "Reply tree" {
		t.Fatalf("expected thread title on reply tree post, got %q in %+v", got, tree.Posts)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "secret",
		"name": "secret",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create secret board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set secret member-read status: %d error=%+v", status, ack.Error)
	}
	secret := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", adminToken, map[string]string{
		"title": "Secret reply tree",
		"body":  "hidden",
	}, &secret); status != http.StatusCreated {
		t.Fatalf("create secret topic status: %d error=%+v", status, secret.Error)
	}
	secretPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+secret.Result.ID+"/posts", adminToken, nil, &secretPosts); status != http.StatusOK {
		t.Fatalf("admin secret posts status: %d", status)
	}
	if len(secretPosts.Posts) != 1 {
		t.Fatalf("expected secret root post, got %+v", secretPosts.Posts)
	}
	forbidden := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/posts/"+secretPosts.Posts[0].ID+"/reply-tree", aliceToken, nil, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected alice reply-tree read on member-read board to be forbidden, got status %d response=%+v", status, forbidden)
	}
}

func hasHTTPThread(response threadSummariesResponse, threadID string) bool {
	for _, thread := range response.Threads {
		if thread.ID == threadID {
			return true
		}
	}
	return false
}

func httpBoardNameForThread(response threadSummariesResponse, threadID string) string {
	for _, thread := range response.Threads {
		if thread.ID == threadID {
			return thread.BoardName
		}
	}
	return ""
}

func hasHTTPPost(response postsResponse, threadID string) bool {
	for _, post := range response.Posts {
		if post.Thread == threadID {
			return true
		}
	}
	return false
}

func hasHTTPPostID(response postsResponse, postID string) bool {
	for _, post := range response.Posts {
		if post.ID == postID {
			return true
		}
	}
	return false
}

func httpPostThreadTitle(response postsResponse, threadID string) string {
	for _, post := range response.Posts {
		if post.Thread == threadID {
			return post.ThreadTitle
		}
	}
	return ""
}

func httpPostThreadTitleForPost(response postsResponse, postID string) string {
	for _, post := range response.Posts {
		if post.ID == postID {
			return post.ThreadTitle
		}
	}
	return ""
}

func httpPostReplyDepth(response postsResponse, postID string) int {
	for _, post := range response.Posts {
		if post.ID == postID {
			return post.ReplyDepth
		}
	}
	return -1
}

func httpFavoriteFolderByName(response favoriteTreeResponse, name string) *favoriteFolderResponseItem {
	for i := range response.Folders {
		if response.Folders[i].Name == name {
			return &response.Folders[i]
		}
	}
	return nil
}

func httpFavoriteFolderForBoard(response favoriteTreeResponse, boardID string) string {
	for _, board := range response.Boards {
		if board.ID == boardID {
			return board.FolderID
		}
	}
	return ""
}

func TestHTTPBoardSettingsAndModeratorsLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	createBoard := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "policy",
		"name":        "Policy",
		"description": "Policy board",
	}, &createBoard); status != http.StatusCreated {
		t.Fatalf("create policy board status: %d error=%+v", status, createBoard.Error)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/policy/settings", adminToken, map[string]bool{
		"readOnly": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set read-only status: %d error=%+v", status, ack.Error)
	}

	blocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/policy/threads", bobToken, map[string]string{
		"title": "blocked",
		"body":  "blocked",
	}, &blocked); status != http.StatusForbidden {
		t.Fatalf("expected read-only create to be forbidden, got %d error=%+v", status, blocked.Error)
	}

	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/policy/moderators/alice", adminToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("set board moderator status: %d error=%+v", status, ack.Error)
	}
	syssecurityThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/syssecurity/threads", bobToken, nil, &syssecurityThreads); status != http.StatusOK {
		t.Fatalf("list syssecurity threads status: %d", status)
	}
	if len(syssecurityThreads.Threads) != 2 || syssecurityThreads.Threads[0].Title != "Board moderator appointed: policy" {
		t.Fatalf("expected moderator appointment syssecurity thread, got %+v", syssecurityThreads.Threads)
	}
	syssecurityPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+syssecurityThreads.Threads[0].ID+"/posts", bobToken, nil, &syssecurityPosts); status != http.StatusOK {
		t.Fatalf("list syssecurity posts status: %d", status)
	}
	if len(syssecurityPosts.Posts) != 1 || !strings.Contains(syssecurityPosts.Posts[0].Body, "Action: board moderator appointed") || !strings.Contains(syssecurityPosts.Posts[0].Body, "User: alice") {
		t.Fatalf("expected moderator appointment syssecurity post, got %+v", syssecurityPosts.Posts)
	}
	settingsPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+syssecurityThreads.Threads[1].ID+"/posts", bobToken, nil, &settingsPosts); status != http.StatusOK {
		t.Fatalf("list syssecurity settings posts status: %d", status)
	}
	if len(settingsPosts.Posts) != 1 || !strings.Contains(settingsPosts.Posts[0].Body, "Action: board settings changed") || !strings.Contains(settingsPosts.Posts[0].Body, "readOnly: true") {
		t.Fatalf("expected board settings syssecurity post, got %+v", settingsPosts.Posts)
	}

	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/policy/settings", aliceToken, map[string]bool{
		"readOnly":         false,
		"noReply":          true,
		"anonymousAllowed": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("board moderator settings status: %d error=%+v", status, ack.Error)
	}

	info := boardInfoResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/policy", bobToken, nil, &info); status != http.StatusOK {
		t.Fatalf("get board info status: %d", status)
	}
	if !info.Settings.NoReply || !info.Settings.AnonymousAllowed || len(info.Moderators) != 1 || info.Moderators[0].Name != "alice" {
		t.Fatalf("expected board settings and moderator info, got %+v", info)
	}

	createThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/policy/threads", bobToken, map[string]any{
		"title":     "Anonymous topic",
		"body":      "hello",
		"anonymous": true,
	}, &createThread); status != http.StatusCreated {
		t.Fatalf("create anonymous thread status: %d error=%+v", status, createThread.Error)
	}

	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/policy/threads", bobToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list policy threads status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].Author != "Anonymous" || threads.Threads[0].AuthorID != "" {
		t.Fatalf("expected anonymous thread author, got %+v", threads.Threads)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+createThread.Result.ID+"/posts", bobToken, map[string]string{
		"body": "blocked reply",
	}, &blocked); status != http.StatusForbidden {
		t.Fatalf("expected no-reply post to be forbidden, got %d error=%+v", status, blocked.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+createThread.Result.ID+"/posts", aliceToken, map[string]string{
		"body": "moderator reply",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("expected board moderator reply, got %d error=%+v", status, ack.Error)
	}

	createMembersBoard := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "members",
		"name":        "Members",
		"description": "Members only",
	}, &createMembersBoard); status != http.StatusCreated {
		t.Fatalf("create members board status: %d error=%+v", status, createMembersBoard.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/members/settings", adminToken, map[string]bool{
		"memberReadMode": true,
		"memberPostMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set member policy status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/members", bobToken, nil, &info); status != http.StatusForbidden {
		t.Fatalf("expected non-member board info to be forbidden, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/members/members/alice", adminToken, map[string]string{
		"title": "alumna",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("add alice member status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/members", aliceToken, nil, &info); status != http.StatusOK {
		t.Fatalf("expected member board info, got %d", status)
	}
	if !info.Settings.MemberReadMode || !info.Settings.MemberPostMode || len(info.Members) != 1 || info.Members[0].Name != "alice" || info.Members[0].Title != "alumna" {
		t.Fatalf("expected member policy and alice member, got %+v", info)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/members/threads", bobToken, map[string]string{
		"title": "blocked",
		"body":  "blocked",
	}, &blocked); status != http.StatusForbidden {
		t.Fatalf("expected non-member thread create to be forbidden, got %d error=%+v", status, blocked.Error)
	}
	memberThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/members/threads", aliceToken, map[string]string{
		"title": "Member topic",
		"body":  "hello",
	}, &memberThread); status != http.StatusCreated {
		t.Fatalf("expected member thread create, got %d error=%+v", status, memberThread.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/members/threads", bobToken, nil, &threads); status != http.StatusForbidden {
		t.Fatalf("expected non-member threads list to be forbidden, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/members/members/bob", adminToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("add bob member status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/members/threads", bobToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("expected member threads list, got %d", status)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+memberThread.Result.ID+"/posts", bobToken, map[string]string{
		"body": "member reply",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("expected member reply, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/boards/members/members/bob", adminToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("remove bob member status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/members/threads", bobToken, nil, &threads); status != http.StatusForbidden {
		t.Fatalf("expected removed member threads list to be forbidden, got %d", status)
	}
}

func TestHTTPBoardMailInPosting(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")
	registerUser(t, handler, "alice")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "mailbox",
		"name": "Mailbox",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create mailbox board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/mailbox/mail-in", bobToken, map[string]string{
		"subject": "Mail thread",
		"body":    "posted from mail",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected disabled mail-in to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/mailbox/settings", adminToken, map[string]bool{
		"mailInAllowed": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("enable mail-in status: %d error=%+v", status, ack.Error)
	}
	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/mailbox/mail-in", bobToken, map[string]string{
		"subject": "Mail thread",
		"body":    "posted from mail",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("mail-in thread status: %d error=%+v", status, thread.Error)
	}
	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/mailbox/threads", bobToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list mailbox threads status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].ID != thread.Result.ID || threads.Threads[0].Title != "Mail thread" {
		t.Fatalf("expected mail-in thread, got %+v", threads.Threads)
	}
	reply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/mail-in", bobToken, map[string]string{
		"body": "mail reply",
	}, &reply); status != http.StatusCreated {
		t.Fatalf("mail-in reply status: %d error=%+v", status, reply.Error)
	}
	posts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", bobToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list mail-in posts status: %d", status)
	}
	if len(posts.Posts) != 2 || posts.Posts[1].ID != reply.Result.ID || posts.Posts[1].Thread != thread.Result.ID {
		t.Fatalf("expected mail-in reply post, got %+v", posts.Posts)
	}
}

func TestHTTPRelayDeliveries(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")
	registerUser(t, handler, "alice")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "relay",
		"name": "Relay",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create relay board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/relay/settings", adminToken, map[string]bool{
		"relayEnabled": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("enable relay status: %d error=%+v", status, ack.Error)
	}
	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/relay/threads", bobToken, map[string]string{
		"title": "Relay topic",
		"body":  "first relay body",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("create relay thread status: %d error=%+v", status, thread.Error)
	}
	reply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", bobToken, map[string]string{
		"body": "second relay body",
	}, &reply); status != http.StatusCreated {
		t.Fatalf("create relay reply status: %d error=%+v", status, reply.Error)
	}
	forbidden := relayDeliveriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/relay/deliveries?status=pending", bobToken, nil, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected non-admin relay list to be forbidden, got %d", status)
	}
	deliveries := relayDeliveriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/relay/deliveries?status=pending", adminToken, nil, &deliveries); status != http.StatusOK {
		t.Fatalf("list relay deliveries status: %d", status)
	}
	if len(deliveries.Deliveries) != 2 {
		t.Fatalf("expected two relay deliveries, got %+v", deliveries.Deliveries)
	}
	if deliveries.Deliveries[0].BoardID != "relay" || deliveries.Deliveries[0].ThreadID != thread.Result.ID || deliveries.Deliveries[0].Title != "Relay topic" || deliveries.Deliveries[0].Body != "first relay body" {
		t.Fatalf("unexpected first relay delivery: %+v", deliveries.Deliveries[0])
	}
	if deliveries.Deliveries[1].PostID != reply.Result.ID || deliveries.Deliveries[1].Body != "second relay body" || deliveries.Deliveries[1].Status != "pending" {
		t.Fatalf("unexpected reply relay delivery: %+v", deliveries.Deliveries[1])
	}
}

func TestHTTPPostAttachmentsLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	blocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]any{
		"title": "blocked file",
		"body":  "hello",
		"attachments": []map[string]any{{
			"filename": "blocked.zip",
		}},
	}, &blocked); status != http.StatusForbidden {
		t.Fatalf("expected disabled attachment post to be forbidden, got %d error=%+v", status, blocked.Error)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "files",
		"name":        "Files",
		"description": "File board",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create files board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/files/settings", adminToken, map[string]bool{
		"attachmentsAllowed": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("enable attachments status: %d error=%+v", status, ack.Error)
	}

	createThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/files/threads", aliceToken, map[string]any{
		"title": "manual",
		"body":  "read this",
		"attachments": []map[string]any{{
			"filename":    "manual.pdf",
			"contentType": "application/pdf",
			"sizeBytes":   4096,
			"url":         "https://example.test/manual.pdf",
		}},
	}, &createThread); status != http.StatusCreated {
		t.Fatalf("create attachment thread status: %d error=%+v", status, createThread.Error)
	}

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThread.Result.ID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list attachment posts status: %d", status)
	}
	if len(posts.Posts) != 1 || len(posts.Posts[0].Attachments) != 1 {
		t.Fatalf("expected one post attachment, got %+v", posts.Posts)
	}
	att := posts.Posts[0].Attachments[0]
	if att.ID == "" || att.Filename != "manual.pdf" || att.ContentType != "application/pdf" || att.SizeBytes != 4096 || att.URL == "" {
		t.Fatalf("expected attachment metadata round trip, got %+v", att)
	}

	upload := ackResponse{}
	if status := doMultipartFileRequest(t, handler, "/api/v1/posts/"+posts.Posts[0].ID+"/attachments", aliceToken, "archive.zip", []byte("download me"), &upload); status != http.StatusCreated {
		t.Fatalf("upload attachment status: %d error=%+v", status, upload.Error)
	}
	if upload.Result == nil || upload.Result.ID == "" {
		t.Fatalf("expected uploaded attachment id, got %+v", upload)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+createThread.Result.ID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts after upload status: %d", status)
	}
	if len(posts.Posts[0].Attachments) != 2 || !posts.Posts[0].Attachments[1].Stored || posts.Posts[0].Attachments[1].Filename != "archive.zip" {
		t.Fatalf("expected stored uploaded attachment, got %+v", posts.Posts[0].Attachments)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/attachments/"+upload.Result.ID, nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download attachment status: %d body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), []byte("download me")) {
		t.Fatalf("expected downloaded attachment bytes, got %q", rec.Body.String())
	}
}

func doMultipartFileRequest(t *testing.T, handler http.Handler, path, token, filename string, data []byte, out any) int {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if out != nil && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode multipart response: %v body=%s", err, rec.Body.String())
		}
	}
	return rec.Code
}

func TestHTTPBoardMemberApplicationsLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")
	registerUser(t, handler, "alice")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "club",
		"name":        "Club",
		"description": "Resident board",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create club board status: %d error=%+v", status, ack.Error)
	}

	apply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/member-applications", bobToken, map[string]string{
		"note": "I read this board daily.",
	}, &apply); status != http.StatusCreated {
		t.Fatalf("apply board membership status: %d error=%+v", status, apply.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/member-applications", bobToken, nil, &ack); status != http.StatusConflict {
		t.Fatalf("expected duplicate pending application conflict, got %d error=%+v", status, ack.Error)
	}

	apps := memberApplicationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/club/member-applications?status=pending", adminToken, nil, &apps); status != http.StatusOK {
		t.Fatalf("list pending applications status: %d", status)
	}
	if len(apps.Applications) != 1 || apps.Applications[0].ID != apply.Result.ID || apps.Applications[0].Name != "bob" || apps.Applications[0].Note == "" {
		t.Fatalf("expected bob pending application, got %+v", apps.Applications)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/board-member-applications/"+apply.Result.ID+"/review", bobToken, map[string]string{
		"status": "approved",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected applicant review to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/board-member-applications/"+apply.Result.ID+"/review", adminToken, map[string]string{
		"status": "approved",
		"title":  "resident",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("approve application status: %d error=%+v", status, ack.Error)
	}
	registryThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/Registry/threads", bobToken, nil, &registryThreads); status != http.StatusOK {
		t.Fatalf("list Registry threads status: %d", status)
	}
	if len(registryThreads.Threads) != 1 || registryThreads.Threads[0].ID != "registry_approved_thr_"+apply.Result.ID {
		t.Fatalf("expected approved registration system thread, got %+v", registryThreads.Threads)
	}
	registryPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+registryThreads.Threads[0].ID+"/posts", bobToken, nil, &registryPosts); status != http.StatusOK {
		t.Fatalf("list Registry posts status: %d", status)
	}
	if len(registryPosts.Posts) != 1 || !strings.Contains(registryPosts.Posts[0].Body, "Status: approved") || !strings.Contains(registryPosts.Posts[0].Body, "Applicant: bob") {
		t.Fatalf("expected approved registration log post, got %+v", registryPosts.Posts)
	}
	if strings.Contains(registryPosts.Posts[0].Body, "I read this board daily.") || strings.Contains(registryPosts.Posts[0].Body, "resident") {
		t.Fatalf("approved registration log leaked private note/title: %q", registryPosts.Posts[0].Body)
	}

	info := boardInfoResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/club", bobToken, nil, &info); status != http.StatusOK {
		t.Fatalf("get club info status: %d", status)
	}
	if len(info.Members) != 1 || info.Members[0].Name != "bob" || info.Members[0].Title != "resident" {
		t.Fatalf("expected approved bob member, got %+v", info.Members)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/alice", adminToken, map[string]any{
		"title":    "lead",
		"position": 0,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("add alice member status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/bob", adminToken, map[string]any{
		"title":    "resident",
		"position": 1,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("position bob member status: %d error=%+v", status, ack.Error)
	}
	info = boardInfoResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/club", bobToken, nil, &info); status != http.StatusOK {
		t.Fatalf("get ordered club info status: %d", status)
	}
	if len(info.Members) < 2 || info.Members[0].Name != "alice" || info.Members[0].Position != 0 || info.Members[1].Name != "bob" || info.Members[1].Position != 1 {
		t.Fatalf("expected ordered board members, got %+v", info.Members)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/members/leave", bobToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("leave membership status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/member-applications", bobToken, nil, &apply); status != http.StatusCreated {
		t.Fatalf("reapply after leave status: %d error=%+v", status, apply.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/board-member-applications/"+apply.Result.ID+"/review", adminToken, map[string]string{
		"status": "blacklisted",
		"note":   "not eligible",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("blacklist application status: %d error=%+v", status, ack.Error)
	}
	rejectThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/reject_registry/threads", bobToken, nil, &rejectThreads); status != http.StatusOK {
		t.Fatalf("list reject_registry threads status: %d", status)
	}
	if len(rejectThreads.Threads) != 1 || rejectThreads.Threads[0].ID != "registry_blacklisted_thr_"+apply.Result.ID {
		t.Fatalf("expected blacklisted registration system thread, got %+v", rejectThreads.Threads)
	}
	rejectPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+rejectThreads.Threads[0].ID+"/posts", bobToken, nil, &rejectPosts); status != http.StatusOK {
		t.Fatalf("list reject_registry posts status: %d", status)
	}
	if len(rejectPosts.Posts) != 1 || !strings.Contains(rejectPosts.Posts[0].Body, "Status: blacklisted") || strings.Contains(rejectPosts.Posts[0].Body, "not eligible") {
		t.Fatalf("expected sanitized blacklisted registration log, got %+v", rejectPosts.Posts)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected blacklisted application to be forbidden, got %d error=%+v", status, ack.Error)
	}
}

func TestHTTPDelegatedBoardMemberPermissions(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")
	_ = registerUser(t, handler, "dave")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "club",
		"name":        "Club",
		"description": "Resident board",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create club board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/alice", adminToken, map[string]any{
		"title":               "steward",
		"canManageMembers":    true,
		"canCurate":           true,
		"canModeratePosts":    true,
		"canModerateThreads":  true,
		"canManagePolls":      true,
		"canSetBoardSettings": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("grant delegated member permissions status: %d error=%+v", status, ack.Error)
	}

	info := boardInfoResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/club", aliceToken, nil, &info); status != http.StatusOK {
		t.Fatalf("get club info as alice status: %d", status)
	}
	if len(info.Members) != 1 ||
		!info.Members[0].CanManageMembers ||
		!info.Members[0].CanCurate ||
		!info.Members[0].CanModeratePosts ||
		!info.Members[0].CanModerateThreads ||
		!info.Members[0].CanManagePolls ||
		!info.Members[0].CanSetBoardSettings {
		t.Fatalf("expected alice delegated permissions, got %+v", info.Members)
	}

	apply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/member-applications", bobToken, nil, &apply); status != http.StatusCreated {
		t.Fatalf("bob apply status: %d error=%+v", status, apply.Error)
	}
	apps := memberApplicationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/club/member-applications?status=pending", aliceToken, nil, &apps); status != http.StatusOK {
		t.Fatalf("alice list applications status: %d", status)
	}
	if len(apps.Applications) != 1 || apps.Applications[0].Name != "bob" {
		t.Fatalf("expected alice to see bob pending application, got %+v", apps.Applications)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/board-member-applications/"+apply.Result.ID+"/review", aliceToken, map[string]string{
		"status": "approved",
		"title":  "resident",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice approve application status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/bob", aliceToken, map[string]any{
		"title":               "resident",
		"canCurate":           true,
		"canSetBoardSettings": true,
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected delegated manager cannot grant permissions, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/bob", aliceToken, map[string]any{
		"title":    "resident",
		"position": 1,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice manage ordinary member status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/member-applications", carolToken, nil, &apply); status != http.StatusCreated {
		t.Fatalf("carol apply status: %d error=%+v", status, apply.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/board-member-applications/"+apply.Result.ID+"/review", aliceToken, map[string]string{
		"status": "blacklisted",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected delegated manager cannot blacklist application, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/board-member-applications/"+apply.Result.ID+"/review", aliceToken, map[string]string{
		"status": "rejected",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice reject application status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/dave", adminToken, map[string]string{
		"title": "operator",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("add dave member status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/moderators/dave", adminToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("appoint dave board moderator status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/boards/club/members/dave", aliceToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected delegated manager cannot remove moderator member, got %d error=%+v", status, ack.Error)
	}

	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/club/threads", bobToken, map[string]string{
		"title": "local notes",
		"body":  "first post",
	}, &thread); status != http.StatusCreated {
		t.Fatalf("bob create thread status: %d error=%+v", status, thread.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/digest", bobToken, map[string]string{
		"kind": "digest",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected non-curator digest to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/digest", aliceToken, map[string]string{
		"kind":  "digest",
		"title": "local notes",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice curate thread status: %d error=%+v", status, ack.Error)
	}
	digest := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/club/digest", bobToken, nil, &digest); status != http.StatusOK {
		t.Fatalf("list digest status: %d", status)
	}
	if len(digest.Entries) != 1 || digest.Entries[0].Title != "local notes" {
		t.Fatalf("expected alice-curated digest entry, got %+v", digest.Entries)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/club/settings", bobToken, map[string]bool{
		"noReply": true,
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected ordinary member cannot edit board settings, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/club/settings", aliceToken, map[string]bool{
		"noReply": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice delegated settings update status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/lock", bobToken, map[string]bool{
		"locked": true,
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected ordinary member cannot lock thread, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/lock", aliceToken, map[string]bool{
		"locked": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice delegated thread lock status: %d error=%+v", status, ack.Error)
	}
	posts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	if len(posts.Posts) == 0 {
		t.Fatal("expected thread to have posts")
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/posts/"+posts.Posts[0].ID, aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("alice delegated post redact status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+posts.Posts[0].ID+"/restore", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected ordinary member cannot restore post, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+posts.Posts[0].ID+"/restore", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("alice delegated post restore status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/club/members/bob", adminToken, map[string]any{
		"title":       "resident",
		"canAnnounce": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("grant bob announcement permission status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/digest", bobToken, map[string]string{
		"kind": "archive",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected announcement-only member cannot archive, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/digest", bobToken, map[string]string{
		"kind":  "announcement",
		"title": "club notice",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("bob announcement curate status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/boards/club/members/bob", aliceToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected delegated manager cannot remove delegated member, got %d error=%+v", status, ack.Error)
	}
}

func TestHTTPPostArticleFlagsAndNoReply(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Article flags",
		"body":  "root post",
	}, &ack); status != http.StatusCreated || ack.Result == nil {
		t.Fatalf("create article flag thread status: %d error=%+v", status, ack.Error)
	}
	threadID := ack.Result.ID
	posts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list article flag posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected root post, got %+v", posts.Posts)
	}
	rootPostID := posts.Posts[0].ID

	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/posts/"+rootPostID+"/flags", bobToken, map[string]bool{
		"marked": true,
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected ordinary user cannot mark post, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/general/members/alice", adminToken, map[string]bool{
		"canCurate": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("grant alice curator status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/boards/general/members/bob", adminToken, map[string]bool{
		"canModerateThreads": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("grant bob thread manager status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/posts/"+rootPostID+"/flags", aliceToken, map[string]bool{
		"marked":      true,
		"recommended": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice mark/recommend status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/posts/"+rootPostID+"/flags", bobToken, map[string]bool{
		"noReply": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("bob no-reply status: %d error=%+v", status, ack.Error)
	}
	posts = postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", adminToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list flagged posts status: %d", status)
	}
	if len(posts.Posts) != 1 || !posts.Posts[0].Marked || !posts.Posts[0].Recommended || !posts.Posts[0].NoReply {
		t.Fatalf("expected article flags in post response, got %+v", posts.Posts)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", carolToken, map[string]string{
		"body": "ordinary reply",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected article no-reply to block ordinary reply, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", bobToken, map[string]string{
		"body": "thread manager reply",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("expected thread manager to bypass article no-reply, got %d error=%+v", status, ack.Error)
	}
}

func TestHTTPPostTeXAndMailBackFlags(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "TeX mail-back",
		"body":  "root post",
	}, &ack); status != http.StatusCreated || ack.Result == nil {
		t.Fatalf("create tex/mail-back thread status: %d error=%+v", status, ack.Error)
	}
	threadID := ack.Result.ID
	posts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list tex/mail-back posts status: %d", status)
	}
	if len(posts.Posts) != 1 {
		t.Fatalf("expected root post, got %+v", posts.Posts)
	}
	rootPostID := posts.Posts[0].ID

	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/posts/"+rootPostID+"/flags", bobToken, map[string]bool{
		"tex": true,
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected ordinary user cannot set author article metadata, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/posts/"+rootPostID+"/flags", aliceToken, map[string]bool{
		"tex":      true,
		"mailBack": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("alice set tex/mail-back status: %d error=%+v", status, ack.Error)
	}
	posts = postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list flagged tex/mail-back posts status: %d", status)
	}
	if len(posts.Posts) != 1 || !posts.Posts[0].TeX || !posts.Posts[0].MailBack {
		t.Fatalf("expected tex/mail-back flags in response, got %+v", posts.Posts)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", bobToken, map[string]string{
		"replyTo": rootPostID,
		"body":    "mail me back",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("bob reply with mail-back status: %d error=%+v", status, ack.Error)
	}
	aliceInbox := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", aliceToken, nil, &aliceInbox); status != http.StatusOK {
		t.Fatalf("list alice mail-back inbox status: %d", status)
	}
	if aliceInbox.UnreadCount != 1 || len(aliceInbox.Mail) != 1 || aliceInbox.Mail[0].FromName != "bob" || !strings.Contains(aliceInbox.Mail[0].Subject, "TeX mail-back") {
		t.Fatalf("expected article mail-back in alice inbox, got %+v", aliceInbox)
	}
	mail := mailItemResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/"+aliceInbox.Mail[0].ID, aliceToken, nil, &mail); status != http.StatusOK {
		t.Fatalf("get article mail-back status: %d", status)
	}
	if !strings.Contains(mail.Body, "mail me back") || !strings.Contains(mail.Body, rootPostID) {
		t.Fatalf("expected article mail-back body context, got %+v", mail)
	}
	bobSent := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail?mailbox=sent", bobToken, nil, &bobSent); status != http.StatusOK {
		t.Fatalf("list bob sent mail-back status: %d", status)
	}
	if len(bobSent.Mail) != 0 {
		t.Fatalf("expected automatic mail-back not to create sent copy, got %+v", bobSent)
	}
}

func TestHTTPRepostPostCreatesLineage(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "campus",
		"name":        "Campus",
		"description": "Shared campus notes",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create campus board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "Original article",
		"body":  "source body",
	}, &ack); status != http.StatusCreated || ack.Result == nil {
		t.Fatalf("create source thread status: %d error=%+v", status, ack.Error)
	}
	sourceThreadID := ack.Result.ID
	sourcePosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+sourceThreadID+"/posts", aliceToken, nil, &sourcePosts); status != http.StatusOK {
		t.Fatalf("list source posts status: %d", status)
	}
	if len(sourcePosts.Posts) != 1 {
		t.Fatalf("expected source root post, got %+v", sourcePosts.Posts)
	}
	sourcePostID := sourcePosts.Posts[0].ID

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+sourcePostID+"/repost", bobToken, map[string]string{
		"board": "campus",
		"title": "Shared original article",
	}, &ack); status != http.StatusCreated || ack.Result == nil {
		t.Fatalf("repost source article status: %d error=%+v", status, ack.Error)
	}
	repostThreadID := ack.Result.ID
	repostedPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+repostThreadID+"/posts", bobToken, nil, &repostedPosts); status != http.StatusOK {
		t.Fatalf("list repost posts status: %d", status)
	}
	if len(repostedPosts.Posts) != 1 {
		t.Fatalf("expected repost root post, got %+v", repostedPosts.Posts)
	}
	reposted := repostedPosts.Posts[0]
	if reposted.SourcePost != sourcePostID || reposted.SourceThread != sourceThreadID || reposted.SourceBoard != "general" || reposted.SourceAuthor != "alice" || reposted.SourceTitle != "Original article" {
		t.Fatalf("expected repost lineage in HTTP response, got %+v", reposted)
	}
	if !strings.Contains(reposted.Body, "source body") {
		t.Fatalf("expected repost body to include source body, got %q", reposted.Body)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "secret",
		"name":        "Secret",
		"description": "Members only",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create secret board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &ack); status != http.StatusCreated {
		t.Fatalf("enable secret member-read status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", adminToken, map[string]string{
		"title": "Private article",
		"body":  "hidden source",
	}, &ack); status != http.StatusCreated || ack.Result == nil {
		t.Fatalf("create private source thread status: %d error=%+v", status, ack.Error)
	}
	secretPosts := postsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+ack.Result.ID+"/posts", adminToken, nil, &secretPosts); status != http.StatusOK {
		t.Fatalf("list private source posts status: %d", status)
	}
	if len(secretPosts.Posts) != 1 {
		t.Fatalf("expected private source root post, got %+v", secretPosts.Posts)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+secretPosts.Posts[0].ID+"/repost", bobToken, map[string]string{
		"board": "campus",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected member-read source repost to be forbidden, got %d error=%+v", status, ack.Error)
	}
}

func TestHTTPBoardMemberRequirementsAdmission(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":          "selective",
		"name":        "Selective",
		"description": "Members by rule",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create selective board status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/selective/member-requirements", adminToken, map[string]any{
		"minLoginCount":             2,
		"minPostCount":              1,
		"minScore":                  1,
		"minBoardPostCount":         2,
		"minBoardOriginalPostCount": 1,
		"minBoardDigestCount":       1,
		"minBoardMarkCount":         1,
		"maxMembers":                1,
		"approvalMode":              "auto",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("set member requirements status: %d error=%+v", status, ack.Error)
	}

	info := boardInfoResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/selective", adminToken, nil, &info); status != http.StatusOK {
		t.Fatalf("get selective board info status: %d", status)
	}
	if info.Requirements.MinLoginCount != 2 ||
		info.Requirements.MinPostCount != 1 ||
		info.Requirements.MinScore != 1 ||
		info.Requirements.MinBoardPostCount != 2 ||
		info.Requirements.MinBoardOriginalPostCount != 1 ||
		info.Requirements.MinBoardDigestCount != 1 ||
		info.Requirements.MinBoardMarkCount != 1 ||
		info.Requirements.MaxMembers != 1 ||
		info.Requirements.ApprovalMode != "auto" {
		t.Fatalf("expected stored requirements, got %+v", info.Requirements)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected low-activity applicant to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", bobToken, map[string]string{
		"title": "activity",
		"body":  "first post",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create activity thread status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected one-login applicant to be forbidden, got %d error=%+v", status, ack.Error)
	}
	bobToken = loginUser(t, handler, "bob")
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected no-local-activity applicant to be forbidden, got %d error=%+v", status, ack.Error)
	}
	localThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/threads", bobToken, map[string]string{
		"title": "local activity",
		"body":  "first local post",
	}, &localThread); status != http.StatusCreated {
		t.Fatalf("create local activity thread status: %d error=%+v", status, localThread.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected one-local-post applicant to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+localThread.Result.ID+"/posts", bobToken, map[string]string{
		"body": "second local post",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("append local post status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected no-digest applicant to be forbidden, got %d error=%+v", status, ack.Error)
	}
	localPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+localThread.Result.ID+"/posts", bobToken, nil, &localPosts); status != http.StatusOK {
		t.Fatalf("list local posts status: %d", status)
	}
	if len(localPosts.Posts) == 0 {
		t.Fatal("expected local thread posts")
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+localPosts.Posts[0].ID+"/digest", adminToken, map[string]string{
		"kind":  "digest",
		"title": "Local digest credit",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("curate local post status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusForbidden {
		t.Fatalf("expected no-mark applicant to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+localPosts.Posts[0].ID+"/react", aliceToken, map[string]string{
		"emoji": "heart",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("react local post status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", bobToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("auto-apply membership status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/selective", bobToken, nil, &info); status != http.StatusOK {
		t.Fatalf("get selective board info as bob status: %d", status)
	}
	if len(info.Members) != 1 || info.Members[0].Name != "bob" {
		t.Fatalf("expected bob auto-member, got %+v", info.Members)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "activity 2",
		"body":  "first post",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("create alice activity thread status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/selective/member-applications", aliceToken, nil, &ack); status != http.StatusConflict {
		t.Fatalf("expected full board conflict, got %d error=%+v", status, ack.Error)
	}
}

func TestHTTPDigestCurationLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")

	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Curatable topic",
		"body":  "First post",
	}, &createAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, createAck.Error)
	}
	threadID := createAck.Result.ID

	replyAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", bobToken, map[string]string{
		"body": "Useful reply",
	}, &replyAck); status != http.StatusCreated {
		t.Fatalf("append reply status: %d error=%+v", status, replyAck.Error)
	}

	blocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+replyAck.Result.ID+"/digest", bobToken, map[string]string{
		"kind": "digest",
	}, &blocked); status != http.StatusForbidden {
		t.Fatalf("expected non-moderator curate to be forbidden, got %d error=%+v", status, blocked.Error)
	}

	postDigest := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+replyAck.Result.ID+"/digest", adminToken, map[string]string{
		"kind":  "digest",
		"title": "Useful reply",
		"path":  "faq",
	}, &postDigest); status != http.StatusCreated {
		t.Fatalf("curate post status: %d error=%+v", status, postDigest.Error)
	}
	threadDigest := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/digest", adminToken, map[string]string{
		"kind":  "recommended",
		"title": "Curatable topic",
	}, &threadDigest); status != http.StatusCreated {
		t.Fatalf("curate thread status: %d error=%+v", status, threadDigest.Error)
	}

	entries := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest", bobToken, nil, &entries); status != http.StatusOK {
		t.Fatalf("list digest status: %d", status)
	}
	if len(entries.Entries) != 2 || entries.Entries[0].ID != threadDigest.Result.ID || entries.Entries[1].ID != postDigest.Result.ID {
		t.Fatalf("expected thread and post digest entries, got %+v", entries.Entries)
	}
	recommendThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/Recommend/threads", bobToken, nil, &recommendThreads); status != http.StatusOK {
		t.Fatalf("list Recommend threads status: %d", status)
	}
	if len(recommendThreads.Threads) != 1 || recommendThreads.Threads[0].ID != "recommend_thr_"+threadDigest.Result.ID || recommendThreads.Threads[0].Title != "Curatable topic" {
		t.Fatalf("expected generated Recommend thread, got %+v", recommendThreads.Threads)
	}
	recommendPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+recommendThreads.Threads[0].ID+"/posts", bobToken, nil, &recommendPosts); status != http.StatusOK {
		t.Fatalf("list Recommend posts status: %d", status)
	}
	if len(recommendPosts.Posts) != 1 || !strings.Contains(recommendPosts.Posts[0].Body, "Kind: recommended") || !strings.Contains(recommendPosts.Posts[0].Body, "Curatable topic") {
		t.Fatalf("expected generated Recommend post, got %+v", recommendPosts.Posts)
	}

	filtered := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest?kind=digest&path=faq", bobToken, nil, &filtered); status != http.StatusOK {
		t.Fatalf("filtered digest status: %d", status)
	}
	if len(filtered.Entries) != 1 || filtered.Entries[0].PostID != replyAck.Result.ID || filtered.Entries[0].Path != "faq" {
		t.Fatalf("expected filtered post digest entry, got %+v", filtered.Entries)
	}

	archiveThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/digest", adminToken, map[string]string{
		"kind":  "archive",
		"title": "Archive root",
		"path":  "faq",
	}, &archiveThread); status != http.StatusCreated {
		t.Fatalf("curate archive thread status: %d error=%+v", status, archiveThread.Error)
	}
	archivePost := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+replyAck.Result.ID+"/digest", adminToken, map[string]string{
		"kind":  "archive",
		"title": "Archive child",
		"path":  "faq/howto",
	}, &archivePost); status != http.StatusCreated {
		t.Fatalf("curate archive post status: %d error=%+v", status, archivePost.Error)
	}
	directoryBlocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/digest/directories", bobToken, map[string]string{
		"kind": "archive",
		"path": "faq/empty",
	}, &directoryBlocked); status != http.StatusForbidden {
		t.Fatalf("expected non-curator directory create to be forbidden, got %d error=%+v", status, directoryBlocked.Error)
	}
	emptyDirectory := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/digest/directories", adminToken, map[string]string{
		"kind": "archive",
		"path": "faq/empty",
	}, &emptyDirectory); status != http.StatusCreated {
		t.Fatalf("create digest directory status: %d error=%+v", status, emptyDirectory.Error)
	}
	tree := digestPathTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest/tree?kind=archive", bobToken, nil, &tree); status != http.StatusOK {
		t.Fatalf("digest tree status: %d", status)
	}
	treeNodes := map[string]struct {
		EntryCount int
		ChildCount int
		ParentPath string
		Explicit   bool
	}{}
	for _, node := range tree.Nodes {
		treeNodes[node.Path] = struct {
			EntryCount int
			ChildCount int
			ParentPath string
			Explicit   bool
		}{EntryCount: node.EntryCount, ChildCount: node.ChildCount, ParentPath: node.ParentPath, Explicit: node.Explicit}
	}
	if treeNodes[""].ChildCount != 1 ||
		treeNodes["faq"].EntryCount != 1 ||
		treeNodes["faq"].ChildCount != 2 ||
		treeNodes["faq/howto"].EntryCount != 1 ||
		treeNodes["faq/howto"].ParentPath != "faq" ||
		!treeNodes["faq/empty"].Explicit ||
		treeNodes["faq/empty"].EntryCount != 0 {
		t.Fatalf("expected archive path tree, got %+v", tree.Nodes)
	}
	archiveSearch := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/digest/search?q=howto&kind=archive", bobToken, nil, &archiveSearch); status != http.StatusOK {
		t.Fatalf("archive search status: %d", status)
	}
	if len(archiveSearch.Entries) != 1 || archiveSearch.Entries[0].ID != archivePost.Result.ID || archiveSearch.Entries[0].Path != "faq/howto" {
		t.Fatalf("expected archive search to find nested archive entry, got %+v", archiveSearch.Entries)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/digest/"+archivePost.Result.ID+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+bobToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download digest entry status: %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "Archive child") || !strings.Contains(body, "Useful reply") {
		t.Fatalf("expected exported archive text, got %q", body)
	}
	updateBlocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/digest/"+archivePost.Result.ID, bobToken, map[string]string{
		"title": "Not yours",
	}, &updateBlocked); status != http.StatusForbidden {
		t.Fatalf("expected non-curator digest update to be forbidden, got %d error=%+v", status, updateBlocked.Error)
	}
	updateArchive := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/digest/"+archivePost.Result.ID, adminToken, map[string]string{
		"title": "Archive child edited",
		"path":  "faq/howto/edited",
		"note":  "Cleaned up for the archive",
	}, &updateArchive); status != http.StatusCreated {
		t.Fatalf("update digest entry status: %d error=%+v", status, updateArchive.Error)
	}
	movedArchive := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest?kind=archive&path=faq/howto/edited", bobToken, nil, &movedArchive); status != http.StatusOK {
		t.Fatalf("list moved archive status: %d", status)
	}
	if len(movedArchive.Entries) != 1 || movedArchive.Entries[0].ID != archivePost.Result.ID || movedArchive.Entries[0].Title != "Archive child edited" {
		t.Fatalf("expected moved archive entry, got %+v", movedArchive.Entries)
	}
	bodyEdit := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/digest/"+archivePost.Result.ID+"/body", adminToken, map[string]string{
		"body": "Edited archive body\nWith curator notes.",
	}, &bodyEdit); status != http.StatusCreated {
		t.Fatalf("set digest body status: %d error=%+v", status, bodyEdit.Error)
	}
	editedSearch := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/digest/search?q=curator%20notes&kind=archive", bobToken, nil, &editedSearch); status != http.StatusOK {
		t.Fatalf("search edited archive body status: %d", status)
	}
	if len(editedSearch.Entries) != 1 || editedSearch.Entries[0].ID != archivePost.Result.ID || !editedSearch.Entries[0].BodyEdited {
		t.Fatalf("expected edited archive body search result, got %+v", editedSearch.Entries)
	}
	archiveMail := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/digest/"+archivePost.Result.ID+"/mail", bobToken, map[string]any{
		"to":   []string{"admin"},
		"note": "Please keep this one.",
	}, &archiveMail); status != http.StatusCreated {
		t.Fatalf("mail digest entry status: %d error=%+v", status, archiveMail.Error)
	}
	adminInbox := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", adminToken, nil, &adminInbox); status != http.StatusOK {
		t.Fatalf("list admin mail after digest mail status: %d", status)
	}
	foundArchiveMail := false
	for _, item := range adminInbox.Mail {
		if item.ID == archiveMail.Result.ID {
			foundArchiveMail = strings.Contains(item.Subject, "Archive child edited")
		}
	}
	if !foundArchiveMail {
		t.Fatalf("expected mailed archive entry in admin inbox, got %+v", adminInbox.Mail)
	}
	archiveMailDetail := mailItemResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/"+archiveMail.Result.ID, adminToken, nil, &archiveMailDetail); status != http.StatusOK {
		t.Fatalf("get archive mail detail status: %d", status)
	}
	if !strings.Contains(archiveMailDetail.Body, "Please keep this one.") || !strings.Contains(archiveMailDetail.Body, "Edited archive body") {
		t.Fatalf("expected archive body in mailed digest entry, got %+v", archiveMailDetail)
	}
	resetBody := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/digest/"+archivePost.Result.ID+"/body", adminToken, nil, &resetBody); status != http.StatusCreated {
		t.Fatalf("reset digest body status: %d error=%+v", status, resetBody.Error)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/v1/digest/"+archivePost.Result.ID+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+bobToken)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download reset digest entry status: %d body=%s", rec.Code, rec.Body.String())
	}
	if body := rec.Body.String(); !strings.Contains(body, "Useful reply") || strings.Contains(body, "Edited archive body") {
		t.Fatalf("expected reset archive export to use source text, got %q", body)
	}
	copyBlocked := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/digest/paths/copy", bobToken, map[string]string{
		"kind":     "archive",
		"fromPath": "faq",
		"toPath":   "faq-copy",
	}, &copyBlocked); status != http.StatusForbidden {
		t.Fatalf("expected non-curator digest path copy to be forbidden, got %d error=%+v", status, copyBlocked.Error)
	}
	copyPath := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/digest/paths/copy", adminToken, map[string]string{
		"kind":     "archive",
		"fromPath": "faq",
		"toPath":   "faq-copy",
	}, &copyPath); status != http.StatusCreated {
		t.Fatalf("copy digest path status: %d error=%+v", status, copyPath.Error)
	}
	copiedPath := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest?kind=archive&path=faq-copy/howto/edited", bobToken, nil, &copiedPath); status != http.StatusOK {
		t.Fatalf("list copied digest path status: %d", status)
	}
	if len(copiedPath.Entries) != 1 || copiedPath.Entries[0].ID == archivePost.Result.ID || copiedPath.Entries[0].Title != "Archive child edited" {
		t.Fatalf("expected copied digest path entry, got %+v", copiedPath.Entries)
	}
	copiedTree := digestPathTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest/tree?kind=archive", bobToken, nil, &copiedTree); status != http.StatusOK {
		t.Fatalf("tree after copy digest path status: %d", status)
	}
	copiedEmpty := false
	for _, node := range copiedTree.Nodes {
		if node.Path == "faq-copy/empty" && node.Explicit && node.EntryCount == 0 {
			copiedEmpty = true
		}
	}
	if !copiedEmpty {
		t.Fatalf("expected copied empty digest directory, got %+v", copiedTree.Nodes)
	}
	movePath := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/digest/paths/move", adminToken, map[string]string{
		"kind":     "archive",
		"fromPath": "faq-copy",
		"toPath":   "faq-moved",
	}, &movePath); status != http.StatusCreated {
		t.Fatalf("move digest path status: %d error=%+v", status, movePath.Error)
	}
	movedPath := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest?kind=archive&path=faq-moved/howto/edited", bobToken, nil, &movedPath); status != http.StatusOK {
		t.Fatalf("list moved digest path status: %d", status)
	}
	if len(movedPath.Entries) != 1 || movedPath.Entries[0].Title != "Archive child edited" {
		t.Fatalf("expected moved digest path entry, got %+v", movedPath.Entries)
	}
	movedTree := digestPathTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest/tree?kind=archive", bobToken, nil, &movedTree); status != http.StatusOK {
		t.Fatalf("tree after move digest path status: %d", status)
	}
	movedEmpty := false
	for _, node := range movedTree.Nodes {
		if node.Path == "faq-moved/empty" && node.Explicit {
			movedEmpty = true
		}
	}
	if !movedEmpty {
		t.Fatalf("expected moved empty digest directory, got %+v", movedTree.Nodes)
	}
	deletePath := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/boards/general/digest/paths?kind=archive&path=faq-moved", adminToken, nil, &deletePath); status != http.StatusCreated {
		t.Fatalf("delete digest path status: %d error=%+v", status, deletePath.Error)
	}
	deletedPath := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest?kind=archive&path=faq-moved/howto/edited", bobToken, nil, &deletedPath); status != http.StatusOK {
		t.Fatalf("list deleted digest path status: %d", status)
	}
	if len(deletedPath.Entries) != 0 {
		t.Fatalf("expected deleted digest path subtree, got %+v", deletedPath.Entries)
	}
	deletedTree := digestPathTreeResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest/tree?kind=archive", bobToken, nil, &deletedTree); status != http.StatusOK {
		t.Fatalf("tree after delete digest path status: %d", status)
	}
	for _, node := range deletedTree.Nodes {
		if strings.HasPrefix(node.Path, "faq-moved") {
			t.Fatalf("expected deleted digest path subtree directories, got %+v", deletedTree.Nodes)
		}
	}

	removeAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/digest/"+postDigest.Result.ID, adminToken, nil, &removeAck); status != http.StatusCreated {
		t.Fatalf("remove digest status: %d error=%+v", status, removeAck.Error)
	}
	filtered = digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/digest?kind=digest&path=faq", bobToken, nil, &filtered); status != http.StatusOK {
		t.Fatalf("filtered digest after remove status: %d", status)
	}
	if len(filtered.Entries) != 0 {
		t.Fatalf("expected removed post digest entry, got %+v", filtered.Entries)
	}
}

func TestHTTPSiteAnnouncementsRespectBoardVisibility(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")

	publicThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Campus notice",
		"body":  "Public",
	}, &publicThread); status != http.StatusCreated {
		t.Fatalf("create public thread status: %d error=%+v", status, publicThread.Error)
	}
	publicDigest := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+publicThread.Result.ID+"/digest", adminToken, map[string]string{
		"kind":  "announcement",
		"title": "Public announcement",
	}, &publicDigest); status != http.StatusCreated {
		t.Fatalf("curate public announcement status: %d error=%+v", status, publicDigest.Error)
	}
	systemThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/0announce/threads", bobToken, nil, &systemThreads); status != http.StatusOK {
		t.Fatalf("list generated 0Announce threads status: %d", status)
	}
	if len(systemThreads.Threads) != 1 || systemThreads.Threads[0].Title != "Public announcement" || systemThreads.Threads[0].ID != "ann_thr_"+publicDigest.Result.ID {
		t.Fatalf("expected generated public 0Announce thread, got %+v", systemThreads.Threads)
	}

	createBoard := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards", adminToken, map[string]string{
		"id":   "secret",
		"name": "Secret",
	}, &createBoard); status != http.StatusCreated {
		t.Fatalf("create secret board status: %d error=%+v", status, createBoard.Error)
	}
	settingsAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/boards/secret/settings", adminToken, map[string]bool{
		"memberReadMode": true,
	}, &settingsAck); status != http.StatusCreated {
		t.Fatalf("set secret board settings status: %d error=%+v", status, settingsAck.Error)
	}
	secretThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/secret/threads", adminToken, map[string]string{
		"title": "Private notice",
		"body":  "Members only",
	}, &secretThread); status != http.StatusCreated {
		t.Fatalf("create secret thread status: %d error=%+v", status, secretThread.Error)
	}
	secretDigest := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+secretThread.Result.ID+"/digest", adminToken, map[string]string{
		"kind":  "announcement",
		"title": "Private announcement",
	}, &secretDigest); status != http.StatusCreated {
		t.Fatalf("curate secret announcement status: %d error=%+v", status, secretDigest.Error)
	}
	systemThreads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/0announce/threads", bobToken, nil, &systemThreads); status != http.StatusOK {
		t.Fatalf("list generated 0Announce threads after private announcement status: %d", status)
	}
	if len(systemThreads.Threads) != 1 {
		t.Fatalf("private announcement should not generate public 0Announce thread, got %+v", systemThreads.Threads)
	}

	bobAnnouncements := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/announcements", bobToken, nil, &bobAnnouncements); status != http.StatusOK {
		t.Fatalf("list bob announcements status: %d", status)
	}
	if len(bobAnnouncements.Entries) != 1 || bobAnnouncements.Entries[0].BoardID != "general" || bobAnnouncements.Entries[0].BoardName == "" {
		t.Fatalf("expected bob to see only public announcement, got %+v", bobAnnouncements.Entries)
	}

	adminAnnouncements := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/digest?kind=announcement", adminToken, nil, &adminAnnouncements); status != http.StatusOK {
		t.Fatalf("list admin announcements status: %d", status)
	}
	seen := map[string]bool{}
	for _, entry := range adminAnnouncements.Entries {
		seen[entry.BoardID] = true
	}
	if !seen["general"] || !seen["secret"] {
		t.Fatalf("expected admin to see public and secret announcements, got %+v", adminAnnouncements.Entries)
	}

	bobSearch := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/digest/search?q=announcement&kind=announcement", bobToken, nil, &bobSearch); status != http.StatusOK {
		t.Fatalf("bob announcement search status: %d", status)
	}
	if len(bobSearch.Entries) != 1 || bobSearch.Entries[0].BoardID != "general" {
		t.Fatalf("expected bob search to hide private announcement, got %+v", bobSearch.Entries)
	}
	adminSearch := digestEntriesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/digest/search?q=announcement&kind=announcement", adminToken, nil, &adminSearch); status != http.StatusOK {
		t.Fatalf("admin announcement search status: %d", status)
	}
	seen = map[string]bool{}
	for _, entry := range adminSearch.Entries {
		seen[entry.BoardID] = true
	}
	if !seen["general"] || !seen["secret"] {
		t.Fatalf("expected admin search to include public and secret announcements, got %+v", adminSearch.Entries)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/digest/"+secretDigest.Result.ID+"/download", nil)
	req.Header.Set("Authorization", "Bearer "+bobToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected private digest download to be forbidden, got %d body=%s", rec.Code, rec.Body.String())
	}
	blockedMail := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/digest/"+secretDigest.Result.ID+"/mail", bobToken, map[string]any{
		"to": []string{"bob"},
	}, &blockedMail); status != http.StatusForbidden {
		t.Fatalf("expected private digest mail to be forbidden, got %d error=%+v", status, blockedMail.Error)
	}
}

func TestHTTPModerationSystemBoardLogs(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	bobToken := registerUser(t, handler, "bob")

	publicThread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Public review target",
		"body":  "public body should stay out of logs",
	}, &publicThread); status != http.StatusCreated {
		t.Fatalf("create public thread status: %d error=%+v", status, publicThread.Error)
	}
	publicPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+publicThread.Result.ID+"/posts", bobToken, nil, &publicPosts); status != http.StatusOK {
		t.Fatalf("list public posts status: %d", status)
	}
	if len(publicPosts.Posts) == 0 {
		t.Fatalf("expected public thread starter post")
	}

	flag := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+publicPosts.Posts[0].ID+"/flag", bobToken, map[string]string{
		"reason": "sensitive report reason",
	}, &flag); status != http.StatusCreated {
		t.Fatalf("flag post status: %d error=%+v", status, flag.Error)
	}
	systemThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/0moderation/threads", bobToken, nil, &systemThreads); status != http.StatusOK {
		t.Fatalf("list generated 0Moderation threads status: %d", status)
	}
	if len(systemThreads.Threads) != 1 || systemThreads.Threads[0].ID != "mod_flag_thr_"+flag.Result.ID {
		t.Fatalf("expected generated moderation flag thread, got %+v", systemThreads.Threads)
	}
	flagPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+systemThreads.Threads[0].ID+"/posts", bobToken, nil, &flagPosts); status != http.StatusOK {
		t.Fatalf("list generated moderation flag posts status: %d", status)
	}
	if len(flagPosts.Posts) != 1 {
		t.Fatalf("expected one generated flag post, got %+v", flagPosts.Posts)
	}
	flagBody := flagPosts.Posts[0].Body
	for _, want := range []string{"Status: opened", "Board: general", "Actor: bob"} {
		if !strings.Contains(flagBody, want) {
			t.Fatalf("expected generated flag log to contain %q, got %q", want, flagBody)
		}
	}
	for _, secret := range []string{"sensitive report reason", "public body should stay out of logs"} {
		if strings.Contains(flagBody, secret) {
			t.Fatalf("generated flag log leaked %q: %q", secret, flagBody)
		}
	}

	resolve := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mod/reviewables/"+flag.Result.ID+"/resolve", adminToken, map[string]string{
		"resolution": "private moderator note",
	}, &resolve); status != http.StatusCreated {
		t.Fatalf("resolve review status: %d error=%+v", status, resolve.Error)
	}
	systemThreads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/0moderation/threads", bobToken, nil, &systemThreads); status != http.StatusOK {
		t.Fatalf("list generated 0Moderation threads after resolve status: %d", status)
	}
	if len(systemThreads.Threads) != 2 {
		t.Fatalf("expected flag and resolution moderation threads, got %+v", systemThreads.Threads)
	}
	resolvePosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/mod_resolve_thr_"+flag.Result.ID+"/posts", bobToken, nil, &resolvePosts); status != http.StatusOK {
		t.Fatalf("list generated moderation resolution posts status: %d", status)
	}
	if len(resolvePosts.Posts) != 1 || !strings.Contains(resolvePosts.Posts[0].Body, "Status: resolved") {
		t.Fatalf("expected generated resolution log post, got %+v", resolvePosts.Posts)
	}
	if strings.Contains(resolvePosts.Posts[0].Body, "private moderator note") {
		t.Fatalf("generated resolution log leaked moderator note: %q", resolvePosts.Posts[0].Body)
	}
}

func TestHTTPContentFilterCreatesFilterBoardRecord(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	forbidden := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/content-filters", bobToken, map[string]any{
		"id":      "filter_policy",
		"pattern": "classified",
	}, &forbidden); status != http.StatusForbidden {
		t.Fatalf("expected non-admin content filter create forbidden, got %d error=%+v", status, forbidden.Error)
	}
	filter := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/admin/content-filters", adminToken, map[string]any{
		"id":      "filter_policy",
		"pattern": "classified",
		"scope":   "global",
	}, &filter); status != http.StatusCreated || !filter.OK {
		t.Fatalf("create content filter status=%d error=%+v", status, filter.Error)
	}
	var filters struct {
		Filters []struct {
			ID      string `json:"id"`
			Pattern string `json:"pattern"`
			Scope   string `json:"scope"`
			Active  bool   `json:"active"`
		} `json:"filters"`
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/admin/content-filters?includeInactive=1", adminToken, nil, &filters); status != http.StatusOK {
		t.Fatalf("list content filters status: %d", status)
	}
	if len(filters.Filters) != 1 || filters.Filters[0].ID != "filter_policy" || !filters.Filters[0].Active {
		t.Fatalf("expected active content filter, got %+v", filters.Filters)
	}

	thread := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", aliceToken, map[string]string{
		"title": "Campus note",
		"body":  "this mentions a classified thing that should enter review",
	}, &thread); status != http.StatusCreated || !thread.OK || thread.Result == nil {
		t.Fatalf("create filtered thread status=%d error=%+v", status, thread.Error)
	}
	var reviews struct {
		Reviewables []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			TargetID string `json:"targetId"`
		} `json:"reviewables"`
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mod/reviewables", adminToken, nil, &reviews); status != http.StatusOK {
		t.Fatalf("list reviewables status: %d", status)
	}
	if len(reviews.Reviewables) != 1 || reviews.Reviewables[0].Kind != "content_filter" {
		t.Fatalf("expected content-filter review, got %+v", reviews.Reviewables)
	}

	filterThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/Filter/threads", bobToken, nil, &filterThreads); status != http.StatusOK {
		t.Fatalf("list Filter threads status: %d", status)
	}
	if len(filterThreads.Threads) != 1 || filterThreads.Threads[0].ID != "filter_thr_"+reviews.Reviewables[0].ID {
		t.Fatalf("expected generated Filter thread, got %+v", filterThreads.Threads)
	}
	filterPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+filterThreads.Threads[0].ID+"/posts", bobToken, nil, &filterPosts); status != http.StatusOK {
		t.Fatalf("list Filter posts status: %d", status)
	}
	if len(filterPosts.Posts) != 1 {
		t.Fatalf("expected generated Filter post, got %+v", filterPosts.Posts)
	}
	body := filterPosts.Posts[0].Body
	for _, want := range []string{"Status: opened", "Filter: filter_policy", "Board: general", "Public author: alice"} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected Filter body to contain %q, got:\n%s", want, body)
		}
	}
	for _, secret := range []string{"classified", "this mentions"} {
		if strings.Contains(body, secret) {
			t.Fatalf("generated Filter body leaked %q:\n%s", secret, body)
		}
	}

	inactive := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/admin/content-filters/filter_policy", adminToken, map[string]any{
		"pattern": "classified",
		"scope":   "global",
		"active":  false,
	}, &inactive); status != http.StatusCreated || !inactive.OK {
		t.Fatalf("disable content filter status=%d error=%+v", status, inactive.Error)
	}
	reply := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+thread.Result.ID+"/posts", aliceToken, map[string]string{
		"body": "classified appears again but the rule is off",
	}, &reply); status != http.StatusCreated || !reply.OK {
		t.Fatalf("append after disabling content filter status=%d error=%+v", status, reply.Error)
	}
	reviews = struct {
		Reviewables []struct {
			ID       string `json:"id"`
			Kind     string `json:"kind"`
			TargetID string `json:"targetId"`
		} `json:"reviewables"`
	}{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mod/reviewables", adminToken, nil, &reviews); status != http.StatusOK {
		t.Fatalf("list reviewables after disable status: %d", status)
	}
	if len(reviews.Reviewables) != 1 {
		t.Fatalf("expected disabled filter not to create more reviews, got %+v", reviews.Reviewables)
	}
}

func TestHTTPThreadReadMarkersLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Thread unread",
		"body":  "First post",
	}, &createAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, createAck.Error)
	}
	threadID := createAck.Result.ID

	posts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+threadID+"/posts", aliceToken, nil, &posts); status != http.StatusOK {
		t.Fatalf("list posts status: %d", status)
	}
	firstPostID := posts.Posts[0].ID

	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list thread summaries status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].UnreadPosts != 1 || threads.Threads[0].FirstUnreadPostID != firstPostID {
		t.Fatalf("expected first post unread, got %+v", threads.Threads)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/read", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("mark thread read status: %d error=%+v", status, ack.Error)
	}

	threads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list after mark-read status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].UnreadPosts != 0 || threads.Threads[0].FirstUnreadPostID != "" {
		t.Fatalf("expected no unread posts after mark-read, got %+v", threads.Threads)
	}

	replyAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": "Second post",
	}, &replyAck); status != http.StatusCreated {
		t.Fatalf("append post status: %d error=%+v", status, replyAck.Error)
	}

	threads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list after reply status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].UnreadPosts != 1 || threads.Threads[0].FirstUnreadPostID != replyAck.Result.ID {
		t.Fatalf("expected reply as first unread, got %+v", threads.Threads)
	}

	ack = ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/read/restore", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("restore thread read status: %d error=%+v", status, ack.Error)
	}

	threads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list after restore status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].UnreadPosts != 2 || threads.Threads[0].FirstUnreadPostID != firstPostID {
		t.Fatalf("expected restore to expose both posts, got %+v", threads.Threads)
	}
}

func TestHTTPListThreadsUnreadOnly(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	firstAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "First unread topic",
		"body":  "First post",
	}, &firstAck); status != http.StatusCreated {
		t.Fatalf("create first thread status: %d error=%+v", status, firstAck.Error)
	}

	secondAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Second unread topic",
		"body":  "Second post",
	}, &secondAck); status != http.StatusCreated {
		t.Fatalf("create second thread status: %d error=%+v", status, secondAck.Error)
	}

	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads?unread=1", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list unread threads status: %d", status)
	}
	if len(threads.Threads) != 2 {
		t.Fatalf("expected both threads unread, got %+v", threads.Threads)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+secondAck.Result.ID+"/read", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("mark second thread read status: %d error=%+v", status, ack.Error)
	}

	threads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads?unread=true", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list unread threads after mark-read status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].ID != firstAck.Result.ID {
		t.Fatalf("expected only first thread unread, got %+v", threads.Threads)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/read", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("mark board read status: %d error=%+v", status, ack.Error)
	}

	threads = threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads?unread=1", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list unread threads after board read status: %d", status)
	}
	if len(threads.Threads) != 0 {
		t.Fatalf("expected no unread threads after board marker, got %+v", threads.Threads)
	}
}

func TestHTTPMarkPostReadAdvancesThreadMarker(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Post read markers",
		"body":  "First post",
	}, &createAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, createAck.Error)
	}
	threadID := createAck.Result.ID

	secondAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": "Second post",
	}, &secondAck); status != http.StatusCreated {
		t.Fatalf("append second post status: %d error=%+v", status, secondAck.Error)
	}
	thirdAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/threads/"+threadID+"/posts", adminToken, map[string]string{
		"body": "Third post",
	}, &thirdAck); status != http.StatusCreated {
		t.Fatalf("append third post status: %d error=%+v", status, thirdAck.Error)
	}

	markAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/posts/"+secondAck.Result.ID+"/read", aliceToken, nil, &markAck); status != http.StatusCreated {
		t.Fatalf("mark post read status: %d error=%+v", status, markAck.Error)
	}

	threads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/threads", aliceToken, nil, &threads); status != http.StatusOK {
		t.Fatalf("list threads status: %d", status)
	}
	if len(threads.Threads) != 1 || threads.Threads[0].UnreadPosts != 1 || threads.Threads[0].FirstUnreadPostID != thirdAck.Result.ID {
		t.Fatalf("expected third post to be first unread, got %+v", threads.Threads)
	}
}

func TestHTTPPrivateMailAndDirectMessagesLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	mailAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"alice", "carol"},
		"subject": "Campus plans",
		"body":    "Meet in the lab at six.",
		"attachments": []map[string]any{{
			"filename":    "plan.txt",
			"contentType": "text/plain",
			"sizeBytes":   12,
			"url":         "https://example.edu/plan.txt",
		}},
	}, &mailAck); status != http.StatusCreated {
		t.Fatalf("send mail status: %d error=%+v", status, mailAck.Error)
	}

	aliceInbox := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", aliceToken, nil, &aliceInbox); status != http.StatusOK {
		t.Fatalf("list alice inbox status: %d", status)
	}
	if aliceInbox.UnreadCount != 1 || len(aliceInbox.Mail) != 1 || aliceInbox.Mail[0].FromName != "bob" || aliceInbox.Mail[0].Read {
		t.Fatalf("expected unread alice mail from bob, got %+v", aliceInbox)
	}
	if len(aliceInbox.Mail[0].ToNames) != 2 {
		t.Fatalf("expected multi-recipient mail, got %+v", aliceInbox.Mail[0].ToNames)
	}
	if len(aliceInbox.Mail[0].Attachments) != 1 || aliceInbox.Mail[0].Attachments[0].Filename != "plan.txt" || aliceInbox.Mail[0].Attachments[0].Stored {
		t.Fatalf("expected mail attachment metadata, got %+v", aliceInbox.Mail[0].Attachments)
	}
	aliceUsage := mailUsageResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/usage", aliceToken, nil, &aliceUsage); status != http.StatusOK {
		t.Fatalf("get alice mail usage status: %d", status)
	}
	if aliceUsage.UsedBytes <= 0 || aliceUsage.QuotaBytes <= aliceUsage.UsedBytes || aliceUsage.RemainingBytes != aliceUsage.QuotaBytes-aliceUsage.UsedBytes {
		t.Fatalf("expected alice mail usage with remaining quota, got %+v", aliceUsage)
	}

	uploadMail := ackResponse{}
	if status := doMultipartFileRequest(t, handler, "/api/v1/mail/"+mailAck.Result.ID+"/attachments", bobToken, "lab.zip", []byte("mail file"), &uploadMail); status != http.StatusCreated {
		t.Fatalf("upload mail attachment status: %d error=%+v", status, uploadMail.Error)
	}
	forbiddenMailUpload := ackResponse{}
	if status := doMultipartFileRequest(t, handler, "/api/v1/mail/"+mailAck.Result.ID+"/attachments", carolToken, "not-mine.txt", []byte("nope"), &forbiddenMailUpload); status != http.StatusForbidden {
		t.Fatalf("expected recipient mail upload to be forbidden, got %d error=%+v", status, forbiddenMailUpload.Error)
	}
	aliceMail := mailItemResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/"+mailAck.Result.ID, aliceToken, nil, &aliceMail); status != http.StatusOK {
		t.Fatalf("get alice mail detail status: %d", status)
	}
	if len(aliceMail.Attachments) != 2 || aliceMail.Attachments[1].ID != uploadMail.Result.ID || !aliceMail.Attachments[1].Stored {
		t.Fatalf("expected uploaded mail attachment in detail, got %+v", aliceMail.Attachments)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/mail/attachments/"+uploadMail.Result.ID, nil)
	req.Header.Set("Authorization", "Bearer "+aliceToken)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("download mail attachment status: %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "mail file" {
		t.Fatalf("expected downloaded mail attachment bytes, got %q", rec.Body.String())
	}

	patchAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/mail/"+mailAck.Result.ID, aliceToken, map[string]any{
		"read":    true,
		"kept":    true,
		"mailbox": "keep",
	}, &patchAck); status != http.StatusCreated {
		t.Fatalf("update alice mail status: %d error=%+v", status, patchAck.Error)
	}

	aliceKeep := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail?mailbox=keep", aliceToken, nil, &aliceKeep); status != http.StatusOK {
		t.Fatalf("list alice keep mailbox status: %d", status)
	}
	if aliceKeep.UnreadCount != 0 || len(aliceKeep.Mail) != 1 || !aliceKeep.Mail[0].Read || !aliceKeep.Mail[0].Kept {
		t.Fatalf("expected kept read mail, got %+v", aliceKeep)
	}

	deleteAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/mail/"+mailAck.Result.ID, carolToken, nil, &deleteAck); status != http.StatusCreated {
		t.Fatalf("delete carol mail status: %d error=%+v", status, deleteAck.Error)
	}
	carolTrash := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail?mailbox=trash", carolToken, nil, &carolTrash); status != http.StatusOK {
		t.Fatalf("list carol trash status: %d", status)
	}
	if len(carolTrash.Mail) != 1 || carolTrash.Mail[0].ID != mailAck.Result.ID {
		t.Fatalf("expected carol mail in trash, got %+v", carolTrash)
	}

	replyAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", aliceToken, map[string]any{
		"to":      []string{"bob"},
		"subject": "Re: Campus plans",
		"body":    "See you there.",
		"replyTo": mailAck.Result.ID,
	}, &replyAck); status != http.StatusCreated {
		t.Fatalf("send reply mail status: %d error=%+v", status, replyAck.Error)
	}
	bobInbox := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", bobToken, nil, &bobInbox); status != http.StatusOK {
		t.Fatalf("list bob inbox status: %d", status)
	}
	if len(bobInbox.Mail) != 1 || bobInbox.Mail[0].ID != replyAck.Result.ID || bobInbox.Mail[0].ParentID != mailAck.Result.ID {
		t.Fatalf("expected reply mail in bob inbox, got %+v", bobInbox)
	}
	forbiddenAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/mail/"+replyAck.Result.ID, carolToken, map[string]bool{"read": true}, &forbiddenAck); status != http.StatusNotFound {
		t.Fatalf("expected carol mail update to be hidden/not found, got %d %+v", status, forbiddenAck.Error)
	}

	relationshipAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/alice/friend", bobToken, nil, &relationshipAck); status != http.StatusCreated {
		t.Fatalf("bob friend alice status: %d error=%+v", status, relationshipAck.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/carol/friend", bobToken, nil, &relationshipAck); status != http.StatusCreated {
		t.Fatalf("bob friend carol status: %d error=%+v", status, relationshipAck.Error)
	}
	groupAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail/groups", bobToken, map[string]any{
		"name":    "lab",
		"members": []string{"alice", "carol"},
	}, &groupAck); status != http.StatusCreated {
		t.Fatalf("create mail group status: %d error=%+v", status, groupAck.Error)
	}
	groups := mailGroupsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail/groups", bobToken, nil, &groups); status != http.StatusOK {
		t.Fatalf("list mail groups status: %d", status)
	}
	if len(groups.Groups) != 2 || groups.Groups[0].ID != "friends" || !groups.Groups[0].BuiltIn || len(groups.Groups[0].Members) != 2 || groups.Groups[1].ID != groupAck.Result.ID || groups.Groups[1].Name != "lab" || len(groups.Groups[1].Members) != 2 {
		t.Fatalf("expected built-in friends group and two-member mail group, got %+v", groups.Groups)
	}
	groupMailAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"toGroups": []string{"lab", "friends"},
		"subject":  "Lab broadcast",
		"body":     "Bring your notebook.",
	}, &groupMailAck); status != http.StatusCreated {
		t.Fatalf("send group mail status: %d error=%+v", status, groupMailAck.Error)
	}
	aliceInbox = mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", aliceToken, nil, &aliceInbox); status != http.StatusOK {
		t.Fatalf("list alice inbox after group mail status: %d", status)
	}
	foundGroupMail := false
	for _, item := range aliceInbox.Mail {
		if item.ID == groupMailAck.Result.ID {
			foundGroupMail = len(item.ToNames) == 2
		}
	}
	if !foundGroupMail {
		t.Fatalf("expected deduplicated group/friends mail in alice inbox, got %+v", aliceInbox.Mail)
	}

	settingsAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/messages/settings", aliceToken, map[string]string{
		"policy": "friends",
	}, &settingsAck); status != http.StatusCreated {
		t.Fatalf("set message settings status: %d error=%+v", status, settingsAck.Error)
	}
	settings := directMessageSettingsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/messages/settings", aliceToken, nil, &settings); status != http.StatusOK {
		t.Fatalf("get message settings status: %d", status)
	}
	if settings.Policy != "friends" {
		t.Fatalf("expected friends-only message policy, got %+v", settings)
	}
	blockedDM := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/messages", bobToken, map[string]string{
		"to":   "alice",
		"body": "Blocked short ping",
	}, &blockedDM); status != http.StatusForbidden {
		t.Fatalf("expected friends-only direct message rejection, got %d %+v", status, blockedDM.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/friend", aliceToken, nil, &relationshipAck); status != http.StatusCreated {
		t.Fatalf("alice friend bob status: %d error=%+v", status, relationshipAck.Error)
	}

	dmAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/messages", bobToken, map[string]string{
		"to":   "alice",
		"body": "Short ping",
	}, &dmAck); status != http.StatusCreated {
		t.Fatalf("send direct message status: %d error=%+v", status, dmAck.Error)
	}
	aliceConvos := directConversationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/messages", aliceToken, nil, &aliceConvos); status != http.StatusOK {
		t.Fatalf("list alice conversations status: %d", status)
	}
	if aliceConvos.UnreadCount != 1 || len(aliceConvos.Conversations) != 1 || aliceConvos.Conversations[0].Name != "bob" || aliceConvos.Conversations[0].UnreadCount != 1 {
		t.Fatalf("expected unread bob conversation, got %+v", aliceConvos)
	}
	aliceMessages := directMessagesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/messages/bob", aliceToken, nil, &aliceMessages); status != http.StatusOK {
		t.Fatalf("list alice messages with bob status: %d", status)
	}
	if len(aliceMessages.Messages) != 1 || aliceMessages.Messages[0].ID != dmAck.Result.ID || aliceMessages.Messages[0].Mine || aliceMessages.Messages[0].Read {
		t.Fatalf("expected unread incoming dm, got %+v", aliceMessages)
	}
	readAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/messages/"+dmAck.Result.ID+"/read", aliceToken, nil, &readAck); status != http.StatusCreated {
		t.Fatalf("mark dm read status: %d error=%+v", status, readAck.Error)
	}
	aliceConvos = directConversationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/messages", aliceToken, nil, &aliceConvos); status != http.StatusOK {
		t.Fatalf("list alice conversations after read status: %d", status)
	}
	if aliceConvos.UnreadCount != 0 || aliceConvos.Conversations[0].UnreadCount != 0 {
		t.Fatalf("expected direct unread count cleared, got %+v", aliceConvos)
	}

	replyDM := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/messages", aliceToken, map[string]string{
		"to":   "bob",
		"body": "Short pong",
	}, &replyDM); status != http.StatusCreated {
		t.Fatalf("send direct reply status: %d error=%+v", status, replyDM.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/messages/"+replyDM.Result.ID, bobToken, nil, &deleteAck); status != http.StatusCreated {
		t.Fatalf("delete direct reply status: %d error=%+v", status, deleteAck.Error)
	}
	bobMessages := directMessagesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/messages/alice", bobToken, nil, &bobMessages); status != http.StatusOK {
		t.Fatalf("list bob messages with alice status: %d", status)
	}
	if len(bobMessages.Messages) != 1 || bobMessages.Messages[0].ID != dmAck.Result.ID {
		t.Fatalf("expected deleted reply hidden from bob, got %+v", bobMessages)
	}
	if status := doJSONRequest(t, handler, http.MethodPatch, "/api/v1/messages/settings", aliceToken, map[string]string{
		"policy": "none",
	}, &settingsAck); status != http.StatusCreated {
		t.Fatalf("set no-message settings status: %d error=%+v", status, settingsAck.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/messages", bobToken, map[string]string{
		"to":   "alice",
		"body": "Blocked again",
	}, &blockedDM); status != http.StatusForbidden {
		t.Fatalf("expected no-message direct message rejection, got %d %+v", status, blockedDM.Error)
	}
}

func TestHTTPSysopMailAll(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"toAll":   true,
		"subject": "Not sysop",
		"body":    "hello everyone",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected non-admin mail-all to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/admin/ignore", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("alice ignore admin status: %d error=%+v", status, ack.Error)
	}

	broadcast := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", adminToken, map[string]any{
		"toAll":   true,
		"subject": "Campus bulletin",
		"body":    "Maintenance at midnight.",
	}, &broadcast); status != http.StatusCreated {
		t.Fatalf("admin mail-all status: %d error=%+v", status, broadcast.Error)
	}
	for name, token := range map[string]string{"alice": aliceToken, "bob": bobToken, "carol": carolToken} {
		inbox := mailListResponse{}
		if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", token, nil, &inbox); status != http.StatusOK {
			t.Fatalf("list %s inbox status: %d", name, status)
		}
		if inbox.UnreadCount != 1 || len(inbox.Mail) != 1 || inbox.Mail[0].ID != broadcast.Result.ID || inbox.Mail[0].FromName != "admin" || inbox.Mail[0].Read {
			t.Fatalf("expected sysop broadcast in %s inbox, got %+v", name, inbox)
		}
		if len(inbox.Mail[0].ToNames) != 3 {
			t.Fatalf("expected mail-all to address three users, got %+v", inbox.Mail[0].ToNames)
		}
	}
	adminInbox := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail", adminToken, nil, &adminInbox); status != http.StatusOK {
		t.Fatalf("list admin inbox status: %d", status)
	}
	if len(adminInbox.Mail) != 0 {
		t.Fatalf("expected no admin inbox copy, got %+v", adminInbox)
	}
	adminSent := mailListResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/mail?mailbox=sent", adminToken, nil, &adminSent); status != http.StatusOK {
		t.Fatalf("list admin sent status: %d", status)
	}
	if len(adminSent.Mail) != 1 || adminSent.Mail[0].ID != broadcast.Result.ID || !adminSent.Mail[0].Read {
		t.Fatalf("expected sysop sent copy, got %+v", adminSent)
	}

	forbiddenThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/sysmail/threads", aliceToken, nil, &forbiddenThreads); status != http.StatusForbidden {
		t.Fatalf("expected sysmail thread list to be staff-only, got %d %+v", status, forbiddenThreads)
	}
	sysmailInfo := boardInfoResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/sysmail", adminToken, nil, &sysmailInfo); status != http.StatusOK {
		t.Fatalf("get sysmail board status: %d", status)
	}
	if sysmailInfo.Board.ID != "sysmail" || !sysmailInfo.Settings.ReadOnly || !sysmailInfo.Settings.NoReply || !sysmailInfo.Settings.MemberReadMode || !sysmailInfo.Settings.MemberPostMode {
		t.Fatalf("expected restricted sysmail settings, got %+v", sysmailInfo)
	}

	sysmailThreads := threadSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/sysmail/threads", adminToken, nil, &sysmailThreads); status != http.StatusOK {
		t.Fatalf("list sysmail threads status: %d", status)
	}
	expectedThreadID := "sysmail_thr_" + broadcast.Result.ID
	if len(sysmailThreads.Threads) != 1 || sysmailThreads.Threads[0].ID != expectedThreadID || sysmailThreads.Threads[0].Title != "Sysop mail: Campus bulletin" {
		t.Fatalf("expected generated sysmail thread, got %+v", sysmailThreads.Threads)
	}
	sysmailPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+expectedThreadID+"/posts", adminToken, nil, &sysmailPosts); status != http.StatusOK {
		t.Fatalf("list sysmail posts status: %d", status)
	}
	if len(sysmailPosts.Posts) != 1 || sysmailPosts.Posts[0].Author != "admin" {
		t.Fatalf("expected one admin-authored sysmail post, got %+v", sysmailPosts.Posts)
	}
	for _, want := range []string{"# Sysop mail: Campus bulletin", "From: admin", "Recipients: 3 users", "Source: admin mail-all broadcast", "Maintenance at midnight.", "Generated restricted sysop mail record"} {
		if !strings.Contains(sysmailPosts.Posts[0].Body, want) {
			t.Fatalf("expected sysmail body to contain %q, got:\n%s", want, sysmailPosts.Posts[0].Body)
		}
	}
	forbiddenPosts := listPostsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/threads/"+expectedThreadID+"/posts", bobToken, nil, &forbiddenPosts); status != http.StatusForbidden {
		t.Fatalf("expected sysmail posts to be staff-only, got %d %+v", status, forbiddenPosts)
	}
}

func TestHTTPSocialGraphAndIgnoreLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")
	carolToken := registerUser(t, handler, "carol")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/friend", aliceToken, map[string]string{
		"note": "lab partner",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("friend bob status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/alice/friend", bobToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("friend alice status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/carol/ignore", aliceToken, map[string]string{
		"note": "too noisy",
	}, &ack); status != http.StatusCreated {
		t.Fatalf("ignore carol status: %d error=%+v", status, ack.Error)
	}

	friends := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/friends", aliceToken, nil, &friends); status != http.StatusOK {
		t.Fatalf("list friends status: %d", status)
	}
	if len(friends.Users) != 1 || friends.Users[0].Name != "bob" || friends.Users[0].Note != "lab partner" || !friends.Users[0].Mutual {
		t.Fatalf("expected bob mutual friend, got %+v", friends.Users)
	}
	fans := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/fans", aliceToken, nil, &fans); status != http.StatusOK {
		t.Fatalf("list fans status: %d", status)
	}
	if len(fans.Users) != 1 || fans.Users[0].Name != "bob" || !fans.Users[0].Mutual {
		t.Fatalf("expected bob fan/mutual, got %+v", fans.Users)
	}
	ignores := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/ignores", aliceToken, nil, &ignores); status != http.StatusOK {
		t.Fatalf("list ignores status: %d", status)
	}
	if len(ignores.Users) != 1 || ignores.Users[0].Name != "carol" || !ignores.Users[0].Ignored {
		t.Fatalf("expected ignored carol, got %+v", ignores.Users)
	}

	online := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/online-friends", aliceToken, nil, &online); status != http.StatusOK {
		t.Fatalf("list online before presence status: %d", status)
	}
	if len(online.Users) != 0 {
		t.Fatalf("expected no online friends before presence, got %+v", online.Users)
	}
	loginWatch := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/login-watch", carolToken, nil, &loginWatch); status != http.StatusForbidden {
		t.Fatalf("expected non-friend login watch to be forbidden, got %d error=%+v", status, loginWatch.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/login-watch", aliceToken, nil, &loginWatch); status != http.StatusCreated {
		t.Fatalf("set login watch status: %d error=%+v", status, loginWatch.Error)
	}
	notifs := notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications before login status: %d", status)
	}
	if len(notifs.Notifications) != 0 || notifs.UnreadCount != 0 {
		t.Fatalf("expected login watch to wait while bob is offline, got %+v", notifs)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"status":   "reading:general",
		"mode":     "reading",
		"board":    "general",
		"location": "General",
		"fromHost": "web.test",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set presence status: %d error=%+v", status, ack.Error)
	}
	notifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications after login status: %d", status)
	}
	if notifs.UnreadCount != 1 || len(notifs.Notifications) != 1 || notifs.Notifications[0].Kind != "login" || notifs.Notifications[0].Actor != "bob" || notifs.Notifications[0].ThreadID != "" {
		t.Fatalf("expected one bob login notification, got %+v", notifs)
	}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/online-friends", aliceToken, nil, &online); status != http.StatusOK {
		t.Fatalf("list online friends status: %d", status)
	}
	if len(online.Users) != 1 || online.Users[0].Name != "bob" || !online.Users[0].Online || online.Users[0].Status != "reading:general" {
		t.Fatalf("expected bob online friend, got %+v", online.Users)
	}
	if online.Users[0].Mode != "reading" || online.Users[0].BoardID != "general" || online.Users[0].FromHost != "web.test" {
		t.Fatalf("expected rich online friend presence, got %+v", online.Users[0])
	}
	globalOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", aliceToken, nil, &globalOnline); status != http.StatusOK {
		t.Fatalf("list global online status: %d", status)
	}
	if len(globalOnline.Users) != 1 || globalOnline.Users[0].Name != "bob" || globalOnline.Users[0].BoardID != "general" {
		t.Fatalf("expected bob in global online list, got %+v", globalOnline.Users)
	}
	boardOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/online", aliceToken, nil, &boardOnline); status != http.StatusOK {
		t.Fatalf("list board online status: %d", status)
	}
	if len(boardOnline.Users) != 1 || boardOnline.Users[0].Name != "bob" || boardOnline.Users[0].Location != "General" {
		t.Fatalf("expected bob in board online list, got %+v", boardOnline.Users)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"status": "active",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set second presence status: %d error=%+v", status, ack.Error)
	}
	notifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications after repeated presence status: %d", status)
	}
	if len(notifs.Notifications) != 1 {
		t.Fatalf("expected login watch to clear after one notification, got %+v", notifs)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"status":   "invisible",
		"board":    "general",
		"location": "Hidden",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set invisible presence status: %d error=%+v", status, ack.Error)
	}
	online = socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/online-friends", aliceToken, nil, &online); status != http.StatusOK {
		t.Fatalf("list online friends after invisible status: %d", status)
	}
	if len(online.Users) != 0 {
		t.Fatalf("expected invisible bob hidden from online friends, got %+v", online.Users)
	}
	globalOnline = socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", aliceToken, nil, &globalOnline); status != http.StatusOK {
		t.Fatalf("list global online after invisible status: %d", status)
	}
	if len(globalOnline.Users) != 0 {
		t.Fatalf("expected invisible bob hidden from global online list, got %+v", globalOnline.Users)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/login-watch", aliceToken, nil, &loginWatch); status != http.StatusCreated {
		t.Fatalf("set login watch while invisible status: %d error=%+v", status, loginWatch.Error)
	}
	notifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications after invisible watch status: %d", status)
	}
	if len(notifs.Notifications) != 1 {
		t.Fatalf("expected invisible bob not to satisfy login watch immediately, got %+v", notifs)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"status": "active",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set visible presence status: %d error=%+v", status, ack.Error)
	}
	notifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications after visible presence status: %d", status)
	}
	if len(notifs.Notifications) != 2 || notifs.Notifications[0].Kind != "login" || notifs.Notifications[0].Actor != "bob" {
		t.Fatalf("expected visible bob to satisfy pending login watch, got %+v", notifs)
	}

	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/ignore", carolToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("carol ignore bob status: %d error=%+v", status, ack.Error)
	}
	blockedMail := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/mail", bobToken, map[string]any{
		"to":      []string{"carol"},
		"subject": "blocked",
		"body":    "hello",
	}, &blockedMail); status != http.StatusForbidden {
		t.Fatalf("expected blocked mail forbidden, got %d %+v", status, blockedMail.Error)
	}
	blockedMessage := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/messages", bobToken, map[string]string{
		"to":   "carol",
		"body": "hello",
	}, &blockedMessage); status != http.StatusForbidden {
		t.Fatalf("expected blocked direct message forbidden, got %d %+v", status, blockedMessage.Error)
	}

	if status := doJSONRequest(t, handler, http.MethodDelete, "/api/v1/users/bob/friend", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("delete friend status: %d error=%+v", status, ack.Error)
	}
	friends = socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/social/friends", aliceToken, nil, &friends); status != http.StatusOK {
		t.Fatalf("list friends after removal status: %d", status)
	}
	if len(friends.Users) != 0 {
		t.Fatalf("expected friend removal, got %+v", friends.Users)
	}
}

func TestHTTPPrivilegedCloakPresence(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", aliceToken, map[string]string{
		"status": "cloak",
	}, &ack); status != http.StatusForbidden {
		t.Fatalf("expected ordinary cloak to be forbidden, got %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/admin/friend", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("friend admin status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", adminToken, map[string]string{
		"status":   "cloaked",
		"mode":     "reading",
		"board":    "general",
		"location": "Control room",
		"fromHost": "ops.test",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set admin cloak presence status: %d error=%+v", status, ack.Error)
	}

	aliceOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", aliceToken, nil, &aliceOnline); status != http.StatusOK {
		t.Fatalf("list ordinary global online status: %d", status)
	}
	for _, user := range aliceOnline.Users {
		if user.Name == "admin" {
			t.Fatalf("expected ordinary user not to see cloaked admin globally, got %+v", user)
		}
	}
	adminOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", adminToken, nil, &adminOnline); status != http.StatusOK {
		t.Fatalf("list admin global online status: %d", status)
	}
	foundAdmin := false
	for _, user := range adminOnline.Users {
		if user.Name != "admin" {
			continue
		}
		foundAdmin = true
		if user.Status != "cloak" || user.BoardID != "general" || user.Location != "Control room" || user.FromHost != "ops.test" {
			t.Fatalf("expected admin to see own cloaked presence, got %+v", user)
		}
	}
	if !foundAdmin {
		t.Fatalf("expected admin to see own cloaked presence, got %+v", adminOnline.Users)
	}
	aliceBoardOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/online", aliceToken, nil, &aliceBoardOnline); status != http.StatusOK {
		t.Fatalf("list ordinary board online status: %d", status)
	}
	for _, user := range aliceBoardOnline.Users {
		if user.Name == "admin" {
			t.Fatalf("expected ordinary user not to see cloaked admin on board, got %+v", user)
		}
	}
	adminBoardOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/online", adminToken, nil, &adminBoardOnline); status != http.StatusOK {
		t.Fatalf("list admin board online status: %d", status)
	}
	foundAdmin = false
	for _, user := range adminBoardOnline.Users {
		if user.Name != "admin" {
			continue
		}
		foundAdmin = true
		if user.Status != "cloak" || user.Mode != "reading" {
			t.Fatalf("expected privileged board online list to include cloaked admin, got %+v", user)
		}
	}
	if !foundAdmin {
		t.Fatalf("expected privileged board online list to include cloaked admin, got %+v", adminBoardOnline.Users)
	}

	stats := communityStatsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/stats/community", aliceToken, nil, &stats); status != http.StatusOK {
		t.Fatalf("community stats status: %d", status)
	}
	if stats.OnlineUsers != 0 {
		t.Fatalf("expected cloaked presence excluded from public online count, got %+v", stats)
	}
	loginWatch := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/admin/login-watch", aliceToken, nil, &loginWatch); status != http.StatusCreated {
		t.Fatalf("set login watch while cloaked status: %d error=%+v", status, loginWatch.Error)
	}
	notifs := notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications while cloaked status: %d", status)
	}
	if len(notifs.Notifications) != 0 {
		t.Fatalf("expected cloak not to satisfy login watch immediately, got %+v", notifs)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", adminToken, map[string]string{
		"status": "active",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set visible admin presence status: %d error=%+v", status, ack.Error)
	}
	notifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications after visible presence status: %d", status)
	}
	if len(notifs.Notifications) != 1 || notifs.Notifications[0].Kind != "login" || notifs.Notifications[0].Actor != "admin" {
		t.Fatalf("expected visible admin presence to satisfy login watch, got %+v", notifs)
	}
}

func TestHTTPMultiSessionPresenceLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	aliceToken := registerUser(t, handler, "alice")
	bobToken := registerUser(t, handler, "bob")

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/friend", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("friend bob status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"sessionId": "web",
		"status":    "reading:general",
		"mode":      "reading",
		"board":     "general",
		"location":  "General",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set web presence status: %d error=%+v", status, ack.Error)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"sessionId": "ssh",
		"status":    "active",
		"mode":      "mail",
		"location":  "Mailbox",
		"fromHost":  "ssh.test",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set ssh presence status: %d error=%+v", status, ack.Error)
	}

	sessionsFor := func(users socialUsersResponse, name string) map[string]struct {
		UserID      string `json:"userId"`
		SessionID   string `json:"sessionId"`
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		Note        string `json:"note"`
		Kind        string `json:"kind"`
		Mutual      bool   `json:"mutual"`
		Ignored     bool   `json:"ignored"`
		Status      string `json:"status"`
		Mode        string `json:"mode"`
		BoardID     string `json:"boardId"`
		BoardName   string `json:"boardName"`
		ThreadID    string `json:"threadId"`
		Location    string `json:"locationLabel"`
		FromHost    string `json:"fromHost"`
		Online      bool   `json:"online"`
		LastSeen    int64  `json:"lastSeen"`
	} {
		out := map[string]struct {
			UserID      string `json:"userId"`
			SessionID   string `json:"sessionId"`
			Name        string `json:"name"`
			DisplayName string `json:"displayName"`
			Note        string `json:"note"`
			Kind        string `json:"kind"`
			Mutual      bool   `json:"mutual"`
			Ignored     bool   `json:"ignored"`
			Status      string `json:"status"`
			Mode        string `json:"mode"`
			BoardID     string `json:"boardId"`
			BoardName   string `json:"boardName"`
			ThreadID    string `json:"threadId"`
			Location    string `json:"locationLabel"`
			FromHost    string `json:"fromHost"`
			Online      bool   `json:"online"`
			LastSeen    int64  `json:"lastSeen"`
		}{}
		for _, user := range users.Users {
			if user.Name == name {
				out[user.SessionID] = user
			}
		}
		return out
	}

	globalOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", aliceToken, nil, &globalOnline); status != http.StatusOK {
		t.Fatalf("list global online status: %d", status)
	}
	bobSessions := sessionsFor(globalOnline, "bob")
	if len(bobSessions) != 2 || bobSessions["web"].BoardID != "general" || bobSessions["ssh"].Mode != "mail" || bobSessions["ssh"].FromHost != "ssh.test" {
		t.Fatalf("expected two bob sessions in global online list, got %+v", globalOnline.Users)
	}
	boardOnline := socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/general/online", aliceToken, nil, &boardOnline); status != http.StatusOK {
		t.Fatalf("list board online status: %d", status)
	}
	bobSessions = sessionsFor(boardOnline, "bob")
	if len(bobSessions) != 1 || bobSessions["web"].SessionID != "web" {
		t.Fatalf("expected only bob web session on general board, got %+v", boardOnline.Users)
	}
	stats := communityStatsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/stats/community", aliceToken, nil, &stats); status != http.StatusOK {
		t.Fatalf("community stats status: %d", status)
	}
	if stats.OnlineUsers != 1 {
		t.Fatalf("expected public online count to count distinct users, got %+v", stats)
	}

	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"sessionId": "web",
		"status":    "offline",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set web offline status: %d error=%+v", status, ack.Error)
	}
	globalOnline = socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", aliceToken, nil, &globalOnline); status != http.StatusOK {
		t.Fatalf("list global online after one session offline status: %d", status)
	}
	bobSessions = sessionsFor(globalOnline, "bob")
	if len(bobSessions) != 1 || bobSessions["ssh"].SessionID != "ssh" {
		t.Fatalf("expected bob to remain online via ssh session, got %+v", globalOnline.Users)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"sessionId": "ssh",
		"status":    "invisible",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set ssh invisible status: %d error=%+v", status, ack.Error)
	}
	globalOnline = socialUsersResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/presence/online", aliceToken, nil, &globalOnline); status != http.StatusOK {
		t.Fatalf("list global online after all sessions hidden status: %d", status)
	}
	if bobSessions = sessionsFor(globalOnline, "bob"); len(bobSessions) != 0 {
		t.Fatalf("expected bob hidden after all sessions hidden, got %+v", globalOnline.Users)
	}

	loginWatch := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPut, "/api/v1/users/bob/login-watch", aliceToken, nil, &loginWatch); status != http.StatusCreated {
		t.Fatalf("set login watch while hidden status: %d error=%+v", status, loginWatch.Error)
	}
	notifs := notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications while hidden status: %d", status)
	}
	if len(notifs.Notifications) != 0 {
		t.Fatalf("expected hidden sessions not to satisfy login watch immediately, got %+v", notifs)
	}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/presence", bobToken, map[string]string{
		"sessionId": "web",
		"status":    "active",
	}, &ack); status != http.StatusOK {
		t.Fatalf("set visible session status: %d error=%+v", status, ack.Error)
	}
	notifs = notificationsResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/notifications", aliceToken, nil, &notifs); status != http.StatusOK {
		t.Fatalf("list notifications after visible session status: %d", status)
	}
	if len(notifs.Notifications) != 1 || notifs.Notifications[0].Kind != "login" || notifs.Notifications[0].Actor != "bob" {
		t.Fatalf("expected visible session to satisfy login watch, got %+v", notifs)
	}
}

func TestHTTPBoardReadMarkersLifecycle(t *testing.T) {
	_, handler := setupHTTPTestServer(t)

	adminToken := registerUser(t, handler, "admin")
	aliceToken := registerUser(t, handler, "alice")

	createAck := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/threads", adminToken, map[string]string{
		"title": "Unread board",
		"body":  "First post",
	}, &createAck); status != http.StatusCreated {
		t.Fatalf("create thread status: %d error=%+v", status, createAck.Error)
	}

	summary := boardSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/summary", aliceToken, nil, &summary); status != http.StatusOK {
		t.Fatalf("list board summaries status: %d", status)
	}
	generalSummaryIndex := -1
	for i := range summary.Boards {
		if summary.Boards[i].ID == "general" {
			generalSummaryIndex = i
			break
		}
	}
	if generalSummaryIndex < 0 || summary.Boards[generalSummaryIndex].UnreadPosts != 1 || summary.Boards[generalSummaryIndex].UnreadThreads != 1 {
		t.Fatalf("expected initial unread summary, got %+v", summary.Boards)
	}

	unread := boardSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/unread", aliceToken, nil, &unread); status != http.StatusOK {
		t.Fatalf("list unread boards status: %d", status)
	}
	if len(unread.Boards) != 1 || unread.Boards[0].ID != "general" {
		t.Fatalf("expected general in unread boards, got %+v", unread.Boards)
	}

	ack := ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/read", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("mark board read status: %d error=%+v", status, ack.Error)
	}

	unread = boardSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/unread", aliceToken, nil, &unread); status != http.StatusOK {
		t.Fatalf("list unread after mark-read status: %d", status)
	}
	if len(unread.Boards) != 0 {
		t.Fatalf("expected no unread boards after mark-read, got %+v", unread.Boards)
	}

	ack = ackResponse{}
	if status := doJSONRequest(t, handler, http.MethodPost, "/api/v1/boards/general/read/restore", aliceToken, nil, &ack); status != http.StatusCreated {
		t.Fatalf("restore board read status: %d error=%+v", status, ack.Error)
	}

	unread = boardSummariesResponse{}
	if status := doJSONRequest(t, handler, http.MethodGet, "/api/v1/boards/unread", aliceToken, nil, &unread); status != http.StatusOK {
		t.Fatalf("list unread after restore status: %d", status)
	}
	if len(unread.Boards) != 1 || unread.Boards[0].UnreadPosts != 1 {
		t.Fatalf("expected restored unread board, got %+v", unread.Boards)
	}
}
