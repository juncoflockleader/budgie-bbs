// Package httpapi implements the HTTP transport (Tiers 3 and 4 of the
// transport ladder). All requests are validated here; authority lives in core.
package httpapi

import (
	"io/fs"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"

	"github.com/juncoflockleader/budgie-bbs/internal/core"
)

// Server wires the HTTP routes to the core.
type Server struct {
	core             *core.Core
	jwtSecret        []byte
	writeRegionProxy *httputil.ReverseProxy
	writeRegionURL   string
	// webRoot, if non-empty, serves a SPA from this filesystem path.
	webRoot string

	// Brute-force limiters for credential/verification endpoints (per process).
	loginLimiter     *failureLimiter // login + change-password (per IP and per account)
	twoFactorLimiter *failureLimiter // 2FA code verification (per user and per IP)
	recoveryLimiter  *failureLimiter // password-recovery requests (per IP)
}

// New creates an HTTP server.
func New(c *core.Core, jwtSecret []byte) *Server {
	return &Server{
		core:      c,
		jwtSecret: jwtSecret,
		// 10 failures / 15 min → escalating 15 min lockout. Login is keyed per IP
		// and per account so neither a single source nor a single target can be
		// hammered.
		loginLimiter: newFailureLimiter(10, 15*time.Minute, 15*time.Minute),
		// 2FA codes are short, so be stricter: 6 tries then a 15 min lockout.
		twoFactorLimiter: newFailureLimiter(6, 15*time.Minute, 15*time.Minute),
		// Password recovery is unauthenticated and side-effectful; cap per-IP spam.
		recoveryLimiter: newFailureLimiter(10, time.Hour, 30*time.Minute),
	}
}

// SetWebRoot configures a directory to serve the web SPA from.
// All non-API requests are served from this directory; unknown paths fall back
// to index.html (client-side routing).
func (s *Server) SetWebRoot(path string) { s.webRoot = path }

// registerOps mounts the unauthenticated liveness/readiness/metrics routes.
// Shared by the full Handler and the ops-only OpsHandler so every node — even
// non-api roles — exposes the same probes.
func (s *Server) registerOps(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)
	mux.HandleFunc("GET /metrics", s.handleMetrics)
}

// OpsHandler returns a handler that serves only /healthz, /readyz, and /metrics.
// Used on nodes without the api role so load balancers and scrapers can still
// reach them without exposing the API surface.
func (s *Server) OpsHandler() http.Handler {
	mux := http.NewServeMux()
	s.registerOps(mux)
	return mux
}

// GatewayHandler returns a handler for stateless live-transport gateway nodes.
// It exposes ops probes plus authenticated poll/long-poll/SSE replay endpoints;
// the WebSocket endpoint is mounted by cmd/budgied so it can choose whether
// command frames are accepted on this gateway.
func (s *Server) GatewayHandler() http.Handler {
	mux := http.NewServeMux()
	s.registerOps(mux)

	auth := s.requireAuth
	mux.Handle("GET /api/v1/events", auth(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/v1/events/stream", auth(http.HandlerFunc(s.handleEventsStream)))
	mux.Handle("GET /api/v1/commands/{command}", auth(http.HandlerFunc(s.handleGetCommandStatus)))
	return s.routeWriteRegion(mux)
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Health probes and metrics (no auth required).
	s.registerOps(mux)

	// Auth (no middleware required)
	mux.HandleFunc("GET /api/v1/auth/policy", s.handleAuthPolicy)
	mux.HandleFunc("GET /api/v1/auth/privacy-policy", s.handlePrivacyPolicy)
	mux.HandleFunc("GET /api/v1/site/appearance", s.handleGetSiteAppearance)
	mux.HandleFunc("GET /api/v1/auth/captcha", s.handleCaptchaChallenge)
	mux.HandleFunc("GET /api/v1/auth/verify-email", s.handleVerifyEmail)
	mux.HandleFunc("POST /api/v1/auth/resend-verification", s.handleResendVerification)
	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/2fa/verify", s.handleVerify2FA)
	mux.HandleFunc("POST /api/v1/auth/2fa/email", s.handleRequestEmail2FACode)
	mux.HandleFunc("POST /api/v1/auth/password-recovery", s.handleRequestPasswordRecovery)
	mux.HandleFunc("POST /api/v1/presence/guest", s.handleSetGuestPresence)

	// Authenticated read-only endpoints
	auth := s.requireAuth
	mux.Handle("GET /api/v1/events", auth(http.HandlerFunc(s.handleEvents)))
	mux.Handle("GET /api/v1/events/stream", auth(http.HandlerFunc(s.handleEventsStream)))
	mux.Handle("GET /api/v1/commands/{command}", auth(http.HandlerFunc(s.handleGetCommandStatus)))
	mux.Handle("GET /api/v1/categories", auth(http.HandlerFunc(s.handleListCategories)))
	mux.Handle("GET /api/v1/stats/community", auth(http.HandlerFunc(s.handleGetCommunityStats)))
	mux.Handle("GET /api/v1/stats/community/history", auth(http.HandlerFunc(s.handleListCommunityStatHistory)))
	mux.Handle("GET /api/v1/rankings/boards", auth(http.HandlerFunc(s.handleListBoardRankings)))
	mux.Handle("GET /api/v1/rankings/threads", auth(http.HandlerFunc(s.handleListThreadRankings)))
	mux.Handle("GET /api/v1/rankings/replies", auth(http.HandlerFunc(s.handleListReplyRankings)))
	mux.Handle("GET /api/v1/rankings/users", auth(http.HandlerFunc(s.handleListUserRankings)))
	mux.Handle("GET /api/v1/rankings/blessings", auth(http.HandlerFunc(s.handleListBlessingRankings)))
	mux.Handle("GET /api/v1/rankings/archive", auth(http.HandlerFunc(s.handleListArchiveRankings)))
	mux.Handle("GET /api/v1/admin/registration-settings", auth(http.HandlerFunc(s.handleGetAccountRegistrationSettings)))
	mux.Handle("GET /api/v1/admin/registrations", auth(http.HandlerFunc(s.handleListAccountRegistrations)))
	mux.Handle("GET /api/v1/admin/password-recovery", auth(http.HandlerFunc(s.handleListPasswordRecoveryRequests)))
	mux.Handle("GET /api/v1/admin/hot-thread-splits", auth(http.HandlerFunc(s.handleListHotThreadSplits)))
	// M14 — Node spy
	mux.Handle("GET /api/v1/admin/nodes", auth(http.HandlerFunc(s.handleListNodes)))
	mux.Handle("POST /api/v1/admin/nodes/{nodeId}/kick", auth(http.HandlerFunc(s.handleKickNode)))
	mux.Handle("POST /api/v1/admin/nodes/{nodeId}/message", auth(http.HandlerFunc(s.handleSendNodeMessage)))
	mux.Handle("GET /api/v1/boards", auth(http.HandlerFunc(s.handleListBoards)))
	mux.Handle("GET /api/v1/boards/recommended", auth(http.HandlerFunc(s.handleListRecommendedBoards)))
	mux.Handle("GET /api/v1/boards/favorites", auth(http.HandlerFunc(s.handleListFavoriteBoards)))
	mux.Handle("GET /api/v1/boards/favorites/tree", auth(http.HandlerFunc(s.handleListFavoriteTree)))
	mux.Handle("GET /api/v1/boards/favorites/export", auth(http.HandlerFunc(s.handleExportFavoriteTree)))
	mux.Handle("GET /api/v1/boards/summary", auth(http.HandlerFunc(s.handleListBoardSummaries)))
	mux.Handle("GET /api/v1/boards/unread", auth(http.HandlerFunc(s.handleListUnreadBoards)))
	mux.Handle("GET /api/v1/boards/resident-feed", auth(http.HandlerFunc(s.handleListResidentBoardPosts)))
	mux.Handle("GET /api/v1/feed/latest", auth(http.HandlerFunc(s.handleListLatestFeedPosts)))
	mux.Handle("GET /api/v1/digest/search", auth(http.HandlerFunc(s.handleSearchDigestEntries)))
	mux.Handle("GET /api/v1/digest", auth(http.HandlerFunc(s.handleListSiteDigestEntries)))
	mux.Handle("GET /api/v1/announcements", auth(http.HandlerFunc(s.handleListSiteAnnouncements)))
	mux.Handle("GET /api/v1/boards/{board}", auth(http.HandlerFunc(s.handleGetBoard)))
	mux.Handle("GET /api/v1/boards/{board}/online", auth(http.HandlerFunc(s.handleListBoardOnlineUsers)))
	mux.Handle("GET /api/v1/boards/{board}/members", auth(http.HandlerFunc(s.handleListBoardMembers)))
	mux.Handle("GET /api/v1/boards/{board}/moderator-history", auth(http.HandlerFunc(s.handleListBoardModeratorHistory)))
	mux.Handle("GET /api/v1/boards/{board}/member-applications", auth(http.HandlerFunc(s.handleListBoardMemberApplications)))
	mux.Handle("GET /api/v1/boards/{board}/deleted", auth(http.HandlerFunc(s.handleListBoardDeletedPosts)))
	mux.Handle("GET /api/v1/boards/{board}/digest/tree", auth(http.HandlerFunc(s.handleListDigestPathTree)))
	mux.Handle("GET /api/v1/boards/{board}/digest", auth(http.HandlerFunc(s.handleListDigestEntries)))
	mux.Handle("GET /api/v1/boards/{board}/threads", auth(http.HandlerFunc(s.handleListThreads)))
	mux.Handle("GET /api/v1/mail", auth(http.HandlerFunc(s.handleListMail)))
	mux.Handle("GET /api/v1/mail/groups", auth(http.HandlerFunc(s.handleListMailGroups)))
	mux.Handle("GET /api/v1/mail/usage", auth(http.HandlerFunc(s.handleGetMailUsage)))
	mux.Handle("GET /api/v1/mail/attachments/{attachment}", auth(http.HandlerFunc(s.handleDownloadMailAttachment)))
	mux.Handle("GET /api/v1/mail/thread/{mail}", auth(http.HandlerFunc(s.handleListMailThread)))
	mux.Handle("GET /api/v1/mail/author/{mail}", auth(http.HandlerFunc(s.handleListMailByAuthor)))
	mux.Handle("GET /api/v1/mail/{mail}", auth(http.HandlerFunc(s.handleGetMail)))
	mux.Handle("GET /api/v1/digest/{entry}/download", auth(http.HandlerFunc(s.handleDownloadDigestEntry)))
	mux.Handle("GET /api/v1/relay/deliveries", auth(http.HandlerFunc(s.handleListRelayDeliveries)))
	mux.Handle("GET /api/v1/messages", auth(http.HandlerFunc(s.handleListDirectMessageConversations)))
	mux.Handle("GET /api/v1/messages/settings", auth(http.HandlerFunc(s.handleGetDirectMessageSettings)))
	mux.Handle("GET /api/v1/messages/{name}", auth(http.HandlerFunc(s.handleListDirectMessages)))
	mux.Handle("GET /api/v1/social/{list}", auth(http.HandlerFunc(s.handleListSocialUsers)))
	mux.Handle("GET /api/v1/presence/online", auth(http.HandlerFunc(s.handleListOnlineUsers)))
	mux.Handle("GET /api/v1/chat/rooms", auth(http.HandlerFunc(s.handleListChatRooms)))
	mux.Handle("GET /api/v1/chat/{room}/recent", auth(http.HandlerFunc(s.handleListChatLines)))
	mux.Handle("GET /api/v1/chat/{room}/online", auth(http.HandlerFunc(s.handleListChatOnlineUsers)))
	mux.Handle("GET /api/v1/authors/{name}/posts", auth(http.HandlerFunc(s.handleListReadableAuthorPosts)))
	mux.Handle("GET /api/v1/threads/unread", auth(http.HandlerFunc(s.handleListUnreadThreads)))
	mux.Handle("GET /api/v1/threads/{thread}", auth(http.HandlerFunc(s.handleGetThread)))
	mux.Handle("GET /api/v1/threads/{thread}/posts", auth(http.HandlerFunc(s.handleListPosts)))
	mux.Handle("GET /api/v1/threads/{thread}/polls", auth(http.HandlerFunc(s.handleListThreadPolls)))
	mux.Handle("GET /api/v1/attachments/{attachment}", auth(http.HandlerFunc(s.handleDownloadAttachment)))
	mux.Handle("GET /api/v1/search", auth(http.HandlerFunc(s.handleSearch)))
	mux.Handle("GET /api/v1/audit", auth(http.HandlerFunc(s.handleAuditLog)))
	mux.Handle("GET /api/v1/notifications", auth(http.HandlerFunc(s.handleListNotifications)))
	mux.Handle("GET /api/v1/polls/{poll}", auth(http.HandlerFunc(s.handleGetPoll)))
	mux.Handle("GET /api/v1/posts/{post}/reply-tree", auth(http.HandlerFunc(s.handleListPostReplyTree)))
	mux.Handle("GET /api/v1/posts/{post}/poll", auth(http.HandlerFunc(s.handleGetPollByPost)))
	mux.Handle("GET /api/v1/users/{name}/trust", auth(http.HandlerFunc(s.handleGetTrust)))
	mux.Handle("GET /api/v1/mod/reviewables", auth(http.HandlerFunc(s.handleListReviewables)))
	mux.Handle("GET /api/v1/admin/content-filters", auth(http.HandlerFunc(s.handleListContentFilters)))
	mux.Handle("GET /api/v1/users/{name}/sanctions", auth(http.HandlerFunc(s.handleListUserSanctions)))
	mux.Handle("GET /api/v1/users/me/private-profile", auth(http.HandlerFunc(s.handleGetOwnPrivateProfile)))
	mux.Handle("GET /api/v1/users/{name}/private-profile", auth(http.HandlerFunc(s.handleGetUserPrivateProfile)))
	mux.Handle("GET /api/v1/users/me/files", auth(http.HandlerFunc(s.handleListOwnPersonalFiles)))
	mux.Handle("GET /api/v1/users/me/files/{file}", auth(http.HandlerFunc(s.handleGetOwnPersonalFile)))

	// Public read-only profile endpoints.
	mux.HandleFunc("GET /api/v1/users/{name}", s.handleGetUserProfile)
	mux.HandleFunc("GET /api/v1/users/{name}/posts", s.handleListUserPosts)
	mux.HandleFunc("GET /api/v1/users/{name}/files", s.handleListUserPersonalFiles)
	mux.HandleFunc("GET /api/v1/users/{name}/files/{file}", s.handleGetUserPersonalFile)

	// Authenticated write endpoints
	mux.Handle("POST /api/v1/auth/pubkey", auth(http.HandlerFunc(s.handleAddPubkey)))
	mux.Handle("GET /api/v1/account/2fa", auth(http.HandlerFunc(s.handleGet2FA)))
	mux.Handle("POST /api/v1/account/2fa/totp", auth(http.HandlerFunc(s.handleInitTOTP)))
	mux.Handle("POST /api/v1/account/2fa/totp/confirm", auth(http.HandlerFunc(s.handleConfirmTOTP)))
	mux.Handle("DELETE /api/v1/account/2fa/totp", auth(http.HandlerFunc(s.handleDisableTOTP)))
	mux.Handle("POST /api/v1/account/2fa/email", auth(http.HandlerFunc(s.handleEnableEmail2FA)))
	mux.Handle("DELETE /api/v1/account/2fa/email", auth(http.HandlerFunc(s.handleDisableEmail2FA)))
	mux.Handle("POST /api/v1/account/2fa/backup-codes", auth(http.HandlerFunc(s.handleGenerateBackupCodes)))
	mux.Handle("GET /api/v1/admin/security-settings", auth(http.HandlerFunc(s.handleGetSecuritySettings)))
	mux.Handle("PATCH /api/v1/admin/security-settings", auth(http.HandlerFunc(s.handleSetSecuritySettings)))
	mux.Handle("PATCH /api/v1/admin/site-appearance", auth(http.HandlerFunc(s.handleSetSiteAppearance)))
	mux.Handle("GET /api/v1/users/{name}/2fa", auth(http.HandlerFunc(s.handleGetUser2FAStatus)))
	mux.Handle("GET /api/v1/boards/{board}/automod-rules", auth(http.HandlerFunc(s.handleListBoardAutomodRules)))
	mux.Handle("GET /api/v1/boards/{board}/automod-activity", auth(http.HandlerFunc(s.handleListBoardAutomodActivity)))
	mux.Handle("PATCH /api/v1/admin/registration-settings", auth(http.HandlerFunc(s.handleSetAccountRegistrationSettings)))
	mux.Handle("POST /api/v1/admin/registrations/{name}/review", auth(http.HandlerFunc(s.handleReviewAccountRegistration)))
	mux.Handle("POST /api/v1/admin/password-recovery/{request}/review", auth(http.HandlerFunc(s.handleReviewPasswordRecoveryRequest)))
	mux.Handle("POST /api/v1/admin/users/{name}/transfer-id", auth(http.HandlerFunc(s.handleTransferUserID)))
	mux.Handle("DELETE /api/v1/admin/users/{name}", auth(http.HandlerFunc(s.handleDeleteUser)))
	mux.Handle("PUT /api/v1/admin/hot-thread-splits/{thread}", auth(http.HandlerFunc(s.handleSetHotThreadSplit)))
	mux.Handle("DELETE /api/v1/admin/hot-thread-splits/{thread}", auth(http.HandlerFunc(s.handleDeleteHotThreadSplit)))
	mux.Handle("POST /api/v1/admin/content-filters", auth(http.HandlerFunc(s.handleSetContentFilter)))
	mux.Handle("PATCH /api/v1/admin/content-filters/{filter}", auth(http.HandlerFunc(s.handleSetContentFilter)))
	mux.Handle("PATCH /api/v1/categories/{category}", auth(http.HandlerFunc(s.handleUpdateCategory)))
	mux.Handle("PATCH /api/v1/users/me/password", auth(http.HandlerFunc(s.handleChangePassword)))
	mux.Handle("POST /api/v1/users/me/deactivate", auth(http.HandlerFunc(s.handleDeactivateAccount)))
	mux.Handle("PATCH /api/v1/users/me/private-profile", auth(http.HandlerFunc(s.handleUpdateOwnPrivateProfile)))
	mux.Handle("PUT /api/v1/users/me/files/{file}", auth(http.HandlerFunc(s.handleSaveOwnPersonalFile)))
	mux.Handle("PATCH /api/v1/users/me/files/{file}", auth(http.HandlerFunc(s.handleSaveOwnPersonalFile)))
	mux.Handle("DELETE /api/v1/users/me/files/{file}", auth(http.HandlerFunc(s.handleDeleteOwnPersonalFile)))
	mux.Handle("GET /api/v1/users/me/signatures", auth(http.HandlerFunc(s.handleListOwnSignatures)))
	mux.Handle("POST /api/v1/users/me/signatures", auth(http.HandlerFunc(s.handleCreateOwnSignature)))
	mux.Handle("POST /api/v1/users/me/signatures/recount", auth(http.HandlerFunc(s.handleRecountOwnSignatures)))
	mux.Handle("PATCH /api/v1/users/me/signatures/settings", auth(http.HandlerFunc(s.handleSetOwnSignatureSettings)))
	mux.Handle("PATCH /api/v1/users/me/signatures/{signature}", auth(http.HandlerFunc(s.handleUpdateOwnSignature)))
	mux.Handle("DELETE /api/v1/users/me/signatures/{signature}", auth(http.HandlerFunc(s.handleDeleteOwnSignature)))
	mux.Handle("GET /api/v1/users/me/login-acl", auth(http.HandlerFunc(s.handleListOwnLoginACL)))
	mux.Handle("POST /api/v1/users/me/login-acl/rules", auth(http.HandlerFunc(s.handleCreateOwnLoginACLRule)))
	mux.Handle("PATCH /api/v1/users/me/login-acl/settings", auth(http.HandlerFunc(s.handleSetOwnLoginACLSettings)))
	mux.Handle("PATCH /api/v1/users/me/login-acl/rules/{rule}", auth(http.HandlerFunc(s.handleUpdateOwnLoginACLRule)))
	mux.Handle("DELETE /api/v1/users/me/login-acl/rules/{rule}", auth(http.HandlerFunc(s.handleDeleteOwnLoginACLRule)))
	mux.Handle("PATCH /api/v1/users/me", auth(http.HandlerFunc(s.handleUpdateOwnProfile)))
	mux.Handle("POST /api/v1/auth/logout", auth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("POST /api/v1/boards", auth(http.HandlerFunc(s.handleCreateBoard)))
	mux.Handle("PATCH /api/v1/boards/{board}/settings", auth(http.HandlerFunc(s.handleSetBoardSettings)))
	mux.Handle("PUT /api/v1/boards/{board}/recommended", auth(http.HandlerFunc(s.handleSetRecommendedBoard)))
	mux.Handle("DELETE /api/v1/boards/{board}/recommended", auth(http.HandlerFunc(s.handleSetRecommendedBoard)))
	mux.Handle("PATCH /api/v1/boards/{board}/member-requirements", auth(http.HandlerFunc(s.handleSetBoardMemberRequirements)))
	mux.Handle("PUT /api/v1/boards/{board}/moderators/{user}", auth(http.HandlerFunc(s.handleSetBoardModerator)))
	mux.Handle("DELETE /api/v1/boards/{board}/moderators/{user}", auth(http.HandlerFunc(s.handleSetBoardModerator)))
	mux.Handle("PUT /api/v1/boards/{board}/members/{user}", auth(http.HandlerFunc(s.handleSetBoardMember)))
	mux.Handle("DELETE /api/v1/boards/{board}/members/{user}", auth(http.HandlerFunc(s.handleSetBoardMember)))
	mux.Handle("POST /api/v1/boards/{board}/member-applications", auth(http.HandlerFunc(s.handleApplyBoardMembership)))
	mux.Handle("POST /api/v1/boards/{board}/members/leave", auth(http.HandlerFunc(s.handleLeaveBoardMembership)))
	mux.Handle("POST /api/v1/board-member-applications/{application}/review", auth(http.HandlerFunc(s.handleReviewBoardMembership)))
	mux.Handle("POST /api/v1/presence", auth(http.HandlerFunc(s.handleSetPresence)))
	mux.Handle("POST /api/v1/stats/community/snapshot", auth(http.HandlerFunc(s.handlePublishStatsSnapshot)))
	mux.Handle("POST /api/v1/admin/notices", auth(http.HandlerFunc(s.handlePublishSystemNotice)))
	mux.Handle("POST /api/v1/commands", auth(http.HandlerFunc(s.handleCommand)))
	mux.Handle("PUT /api/v1/boards/{board}/favorite", auth(http.HandlerFunc(s.handleSetBoardFavorite)))
	mux.Handle("PATCH /api/v1/boards/{board}/favorite", auth(http.HandlerFunc(s.handleMoveBoardFavorite)))
	mux.Handle("DELETE /api/v1/boards/{board}/favorite", auth(http.HandlerFunc(s.handleSetBoardFavorite)))
	mux.Handle("PUT /api/v1/boards/{board}/zap", auth(http.HandlerFunc(s.handleSetBoardZap)))
	mux.Handle("DELETE /api/v1/boards/{board}/zap", auth(http.HandlerFunc(s.handleSetBoardZap)))
	mux.Handle("POST /api/v1/boards/favorites/import", auth(http.HandlerFunc(s.handleImportFavoriteTree)))
	mux.Handle("POST /api/v1/boards/favorites/read", auth(http.HandlerFunc(s.handleMarkFavoriteFolderRead)))
	mux.Handle("POST /api/v1/boards/favorites/read/restore", auth(http.HandlerFunc(s.handleRestoreFavoriteFolderRead)))
	mux.Handle("POST /api/v1/boards/favorites/folders", auth(http.HandlerFunc(s.handleCreateFavoriteFolder)))
	mux.Handle("PATCH /api/v1/boards/favorites/folders/{folder}", auth(http.HandlerFunc(s.handleUpdateFavoriteFolder)))
	mux.Handle("DELETE /api/v1/boards/favorites/folders/{folder}", auth(http.HandlerFunc(s.handleDeleteFavoriteFolder)))
	mux.Handle("POST /api/v1/boards/favorites/folders/{folder}/read", auth(http.HandlerFunc(s.handleMarkFavoriteFolderRead)))
	mux.Handle("POST /api/v1/boards/favorites/folders/{folder}/read/restore", auth(http.HandlerFunc(s.handleRestoreFavoriteFolderRead)))
	mux.Handle("POST /api/v1/boards/{board}/read", auth(http.HandlerFunc(s.handleMarkBoardRead)))
	mux.Handle("POST /api/v1/boards/{board}/read/restore", auth(http.HandlerFunc(s.handleRestoreBoardRead)))
	mux.Handle("POST /api/v1/boards/{board}/posts/range-delete", auth(http.HandlerFunc(s.handleRedactPostRange)))
	mux.Handle("POST /api/v1/boards/{board}/deleted/range-restore", auth(http.HandlerFunc(s.handleRestorePostRange)))
	mux.Handle("POST /api/v1/boards/{board}/deleted/junk/clear", auth(http.HandlerFunc(s.handleClearBoardJunk)))
	mux.Handle("POST /api/v1/boards/{board}/digest/directories", auth(http.HandlerFunc(s.handleCreateDigestDirectory)))
	mux.Handle("POST /api/v1/boards/{board}/digest/paths/move", auth(http.HandlerFunc(s.handleMoveDigestPath)))
	mux.Handle("POST /api/v1/boards/{board}/digest/paths/copy", auth(http.HandlerFunc(s.handleCopyDigestPath)))
	mux.Handle("DELETE /api/v1/boards/{board}/digest/paths", auth(http.HandlerFunc(s.handleDeleteDigestPath)))
	mux.Handle("POST /api/v1/boards/{board}/threads", auth(http.HandlerFunc(s.handleCreateThread)))
	mux.Handle("POST /api/v1/boards/{board}/mail-in", auth(http.HandlerFunc(s.handlePostBoardMail)))
	mux.Handle("POST /api/v1/threads/{thread}/posts", auth(http.HandlerFunc(s.handleAppendPost)))
	mux.Handle("POST /api/v1/threads/{thread}/mail-in", auth(http.HandlerFunc(s.handlePostThreadMail)))
	mux.Handle("PATCH /api/v1/posts/{post}", auth(http.HandlerFunc(s.handleEditPost)))
	mux.Handle("PATCH /api/v1/posts/{post}/flags", auth(http.HandlerFunc(s.handleSetPostFlag)))
	mux.Handle("POST /api/v1/posts/{post}/repost", auth(http.HandlerFunc(s.handleRepostPost)))
	mux.Handle("DELETE /api/v1/posts/{post}", auth(http.HandlerFunc(s.handleRedactPost)))
	mux.Handle("POST /api/v1/posts/{post}/restore", auth(http.HandlerFunc(s.handleRestorePost)))
	mux.Handle("POST /api/v1/posts/{post}/purge", auth(http.HandlerFunc(s.handlePurgePost)))
	mux.Handle("POST /api/v1/posts/{post}/flag", auth(http.HandlerFunc(s.handleFlagPost)))
	mux.Handle("POST /api/v1/posts/{post}/read", auth(http.HandlerFunc(s.handleMarkPostRead)))
	mux.Handle("POST /api/v1/posts/{post}/digest", auth(http.HandlerFunc(s.handleCuratePost)))
	mux.Handle("POST /api/v1/posts/{post}/mail", auth(http.HandlerFunc(s.handleMailPostAuthor)))
	mux.Handle("POST /api/v1/posts/{post}/attachments", auth(http.HandlerFunc(s.handleUploadPostAttachment)))
	mux.Handle("POST /api/v1/posts/{post}/react", auth(http.HandlerFunc(s.handleReactPost)))
	mux.Handle("DELETE /api/v1/posts/{post}/react", auth(http.HandlerFunc(s.handleUnreactPost)))
	mux.Handle("POST /api/v1/polls/{poll}/vote", auth(http.HandlerFunc(s.handleVotePoll)))
	mux.Handle("POST /api/v1/polls/{poll}/publish-result", auth(http.HandlerFunc(s.handlePublishPollResult)))
	mux.Handle("POST /api/v1/notifications/{id}/read", auth(http.HandlerFunc(s.handleMarkNotificationRead)))
	mux.Handle("POST /api/v1/notifications/read-all", auth(http.HandlerFunc(s.handleMarkAllRead)))
	mux.Handle("DELETE /api/v1/notifications", auth(http.HandlerFunc(s.handleDeleteNotifications)))
	mux.Handle("DELETE /api/v1/notifications/{id}", auth(http.HandlerFunc(s.handleDeleteNotification)))
	mux.Handle("PUT /api/v1/threads/{thread}/prefs", auth(http.HandlerFunc(s.handleSetThreadPref)))
	mux.Handle("POST /api/v1/threads/{thread}/read", auth(http.HandlerFunc(s.handleMarkThreadRead)))
	mux.Handle("POST /api/v1/threads/{thread}/read/restore", auth(http.HandlerFunc(s.handleRestoreThreadRead)))
	mux.Handle("POST /api/v1/threads/{thread}/digest", auth(http.HandlerFunc(s.handleCurateThread)))
	mux.Handle("PATCH /api/v1/threads/{thread}/title", auth(http.HandlerFunc(s.handleSetThreadTitle)))
	mux.Handle("POST /api/v1/threads/{thread}/lock", auth(http.HandlerFunc(s.handleLockThread)))
	mux.Handle("POST /api/v1/digest/{entry}/mail", auth(http.HandlerFunc(s.handleSendDigestEntryMail)))
	mux.Handle("PATCH /api/v1/digest/{entry}", auth(http.HandlerFunc(s.handleUpdateDigestEntry)))
	mux.Handle("PUT /api/v1/digest/{entry}/body", auth(http.HandlerFunc(s.handleSetDigestEntryBody)))
	mux.Handle("DELETE /api/v1/digest/{entry}/body", auth(http.HandlerFunc(s.handleResetDigestEntryBody)))
	mux.Handle("DELETE /api/v1/digest/{entry}", auth(http.HandlerFunc(s.handleRemoveDigestEntry)))
	mux.Handle("POST /api/v1/mail", auth(http.HandlerFunc(s.handleSendMail)))
	mux.Handle("POST /api/v1/mail/range-delete", auth(http.HandlerFunc(s.handleDeleteMailRange)))
	mux.Handle("POST /api/v1/mail/groups", auth(http.HandlerFunc(s.handleSetMailGroup)))
	mux.Handle("PUT /api/v1/mail/groups/{group}", auth(http.HandlerFunc(s.handleSetMailGroup)))
	mux.Handle("PATCH /api/v1/mail/groups/{group}", auth(http.HandlerFunc(s.handleSetMailGroup)))
	mux.Handle("DELETE /api/v1/mail/groups/{group}", auth(http.HandlerFunc(s.handleDeleteMailGroup)))
	mux.Handle("POST /api/v1/mail/{mail}/attachments", auth(http.HandlerFunc(s.handleUploadMailAttachment)))
	mux.Handle("POST /api/v1/mail/{mail}/forward", auth(http.HandlerFunc(s.handleForwardMail)))
	mux.Handle("POST /api/v1/mail/{mail}/board", auth(http.HandlerFunc(s.handlePostMailToBoard)))
	mux.Handle("PATCH /api/v1/mail/{mail}", auth(http.HandlerFunc(s.handleUpdateMail)))
	mux.Handle("DELETE /api/v1/mail/{mail}", auth(http.HandlerFunc(s.handleDeleteMail)))
	mux.Handle("POST /api/v1/messages", auth(http.HandlerFunc(s.handleSendDirectMessage)))
	mux.Handle("PATCH /api/v1/messages/settings", auth(http.HandlerFunc(s.handleSetDirectMessageSettings)))
	mux.Handle("POST /api/v1/messages/{message}/read", auth(http.HandlerFunc(s.handleMarkDirectMessageRead)))
	mux.Handle("DELETE /api/v1/messages/{message}", auth(http.HandlerFunc(s.handleDeleteDirectMessage)))
	mux.Handle("PUT /api/v1/users/{name}/login-watch", auth(http.HandlerFunc(s.handleSetLoginWatch)))
	mux.Handle("DELETE /api/v1/users/{name}/login-watch", auth(http.HandlerFunc(s.handleSetLoginWatch)))
	mux.Handle("POST /api/v1/users/{name}/bless", auth(http.HandlerFunc(s.handleBlessUser)))
	mux.Handle("PUT /api/v1/users/{name}/{kind}", auth(http.HandlerFunc(s.handleSetUserRelationship)))
	mux.Handle("DELETE /api/v1/users/{name}/{kind}", auth(http.HandlerFunc(s.handleSetUserRelationship)))
	mux.Handle("POST /api/v1/mod/reviewables/{id}/resolve", auth(http.HandlerFunc(s.handleResolveReview)))
	mux.Handle("POST /api/v1/users/{name}/sanctions", auth(http.HandlerFunc(s.handleSanctionUser)))
	mux.Handle("DELETE /api/v1/users/{name}/sanctions", auth(http.HandlerFunc(s.handleClearUserSanction)))
	mux.Handle("POST /api/v1/chat/{room}/lines", auth(http.HandlerFunc(s.handleSendChatLine)))

	// SPA static files (must come last — catches everything not matched above).
	if s.webRoot != "" {
		mux.Handle("/", spaHandler(s.webRoot))
	}

	return securityHeaders(s.routeWriteRegion(mux))
}

// contentSecurityPolicy is a strict, SPA-compatible CSP. The single allowed
// inline script is the theme bootstrap in web/index.html (hashed below so we
// don't need 'unsafe-inline' for scripts). style-src keeps 'unsafe-inline'
// because React sets inline style attributes; Google Fonts are allow-listed.
// If the inline bootstrap in web/index.html changes, update the hash (compute:
// printf '%s' "<script body>" | openssl dgst -sha256 -binary | openssl base64).
const contentSecurityPolicy = "default-src 'self'; " +
	"base-uri 'self'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"img-src 'self' data:; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"script-src 'self' 'sha256-ZKEaS57sgKgiS9jYCWMu9BV09EirGsVTYWGi4S4x1RQ='; " +
	"connect-src 'self'"

// securityHeaders sets defense-in-depth response headers on every response:
// clickjacking protection (frame-ancestors/X-Frame-Options), MIME-sniff
// blocking (so uploaded files can't be sniffed into executable types), a
// restrictive referrer policy, and the CSP above.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves files from root; requests to unknown paths fall back to
// index.html so that the client-side router can handle them.
func spaHandler(root string) http.Handler {
	dir := os.DirFS(root)
	fileServer := http.FileServer(http.FS(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Don't intercept API paths (safety net — these are matched first by mux).
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		// Check if the requested file exists; if not, serve index.html.
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(dir, path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
