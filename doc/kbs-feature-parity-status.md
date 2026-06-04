# KBS Feature Parity Status

This document tracks implementation progress against
`doc/kbs-feature-set-review.md`. It is intentionally stricter than a product
roadmap: a feature is only marked done when Budgie has a usable workflow, not
just a schema placeholder or an idea in prose.

## Implemented KBS Parity Slices

### Personal Board Collections: Favorite Folders

KBS made favorite boards part of the default daily reading path, including
foldered personal board workspaces. Budgie now has the core workflow:

- A per-user `board_favorites` projection with stable ordering metadata.
- A per-user `favorite_folders` projection with parent links and stable
  ordering metadata.
- New accounts automatically receive the default `general` board favorite when
  that board exists.
- A `setBoardFavorite` command payload.
- Folder commands:
  - `createFavoriteFolder`
  - `updateFavoriteFolder`
  - `deleteFavoriteFolder`
  - `moveBoardFavorite`
- Authenticated read API: `GET /api/v1/boards/favorites`.
- Authenticated tree API: `GET /api/v1/boards/favorites/tree`.
- REST aliases for add/remove:
  - `PUT /api/v1/boards/{board}/favorite`
  - `DELETE /api/v1/boards/{board}/favorite`
- REST aliases for folders and ordering:
  - `POST /api/v1/boards/favorites/folders`
  - `PATCH /api/v1/boards/favorites/folders/{folder}`
  - `DELETE /api/v1/boards/favorites/folders/{folder}`
  - `PATCH /api/v1/boards/{board}/favorite`
- Web board list sections for `Favorites` and `All Boards`.
- Star controls for adding/removing favorite boards without leaving the board
  list.
- Web favorite folders support nesting, create, rename, delete, move up/down,
  board move-to-folder, and board move up/down controls.
- Authenticated favorite-tree export/import APIs:
  - `GET /api/v1/boards/favorites/export`
  - `POST /api/v1/boards/favorites/import`
- Favorite folder and all-favorites read-marker aliases:
  - `POST /api/v1/boards/favorites/read`
  - `POST /api/v1/boards/favorites/read/restore`
  - `POST /api/v1/boards/favorites/folders/{folder}/read`
  - `POST /api/v1/boards/favorites/folders/{folder}/read/restore`
- Uniform commands:
  - `markFavoriteFolderRead`
  - `restoreFavoriteFolderRead`
- Web favorite controls can export/import the favorite tree and mark/restore
  all favorites or an individual favorite folder subtree.

### Board Directory And Category Browser

KBS exposed boards through nested directories rather than only a flat board
index. Budgie now has the first hierarchy-aware board directory:

- A `categories` projection with parent links, sibling positions, visibility,
  descriptions, and timestamps.
- Board creation creates a matching category row.
- The `createBoard` command accepts optional `parentId` and `position`.
- Durable `board.created` events carry `parentId` and `position` for replay.
- Authenticated category read API: `GET /api/v1/categories`.
- Admin category update API: `PATCH /api/v1/categories/{category}` for
  name/description, parent, sibling position, and directory visibility.
- Board summaries expose KBS-style discovery metadata: board creation time,
  `newBoard`, article/post count, thread count, and current online-user count.
- Board summary reads support KBS-style discovery filters and sort modes:
  `GET /api/v1/boards/summary?q=&sort=&new=&newDays=` and
  `GET /api/v1/boards/unread?q=&sort=&new=&newDays=`.
  Supported sort modes include name, newest, online, article count, thread
  count, recent activity, and unread activity.
- Category directory visibility supports public, staff-only, and hidden rows;
  admins see all categories, moderators see public/staff categories, and normal
  users see public categories.
- Web board navigation renders a `Directory` section from the category tree and
  still keeps flat `Unread`, `Favorites`, and `All Boards` views.
- Web board navigation now includes a search/sort toolbar, a `New Boards`
  section, and compact per-board article/thread/online counts.
- Web admins can edit category text, move categories between parents, reorder
  siblings, and change directory visibility from the board directory.

This does not yet implement drag-and-drop gestures or import/export of legacy
section menus.

### Public Rankings And Community Statistics

KBS surfaced community attention through hot topics, board statistics, public
counters, and generated list boards. Budgie now has the first projection-backed
ranking surface:

- Authenticated community counters including cumulative login count, cumulative
  online/stay time, and anonymous web guest counters:
  `GET /api/v1/stats/community`.
- Authenticated daily stat history with derived trend deltas: `GET
  /api/v1/stats/community/history`.
- Authenticated active-board rankings: `GET /api/v1/rankings/boards`.
- Authenticated hot-thread rankings: `GET /api/v1/rankings/threads`.
- Authenticated latest-reply rankings: `GET /api/v1/rankings/replies`.
- Authenticated top-poster rankings: `GET /api/v1/rankings/users`.
- Authenticated blessing rankings: `GET /api/v1/rankings/blessings`.
- Authenticated archive-path rankings: `GET /api/v1/rankings/archive`.
- Admin-triggered generated stat snapshots: `POST
  /api/v1/stats/community/snapshot`.
- Automatic daily stat-log publishing runs from the server process and ensures
  the current UTC day has deterministic `BBSLists` snapshot, login-count
  history, board-activity history, hot-topic history, and completed
  week/month/year period-summary and hot-topic list threads.
- Login recording maintains UTC day/hour login buckets for KBS `static.c` /
  `countlogins`-style hourly login distribution charts.
- Presence changes and stat snapshots maintain a projection-backed daily
  `community_stat_history` row with current counters, max-online users/time, and
  day-over-day deltas for user, board, thread, post, reaction, mail, direct
  message, and login totals, plus cumulative online/stay-time and guest totals.
- Presence updates accrue per-user total online seconds from visible sessions
  with a five-minute cap per update so stale clients do not inflate stay time.
- Public guest-presence pings (`POST /api/v1/presence/guest`) maintain anonymous
  web guest sessions, daily guest deltas, and max guest peaks without granting
  content access.
- Hot-thread rankings expose distinct participant counts. Scores combine
  visible post count, participant count, reaction count, and a 48-hour recency
  half-life so stale activity decays behind fresh conversation.
- KBS `BOARD_POSTSTAT`-style board policy is available as `statsExcluded`:
  readable/postable boards can opt out of public ranking and generated
  stat-log surfaces without becoming private.
- Board, thread, reply, and archive rankings hide member-read boards from users
  who cannot read those boards and hide `statsExcluded` boards for all users;
  direct thread-ranking queries for inaccessible boards are rejected.
- Stat snapshots lazily create a KBS-style `BBSLists` system board and
  deterministic daily generated thread/posts containing community counters,
  max-online history, recent daily stat-history rows with trend deltas, plus
  public-safe board, thread, latest-reply, user, blessing, and archive rankings.
- Stat snapshots also create deterministic daily KBS `countlogins`-style
  `BBSLists` login-count history threads showing total logins, user totals,
  online users, anonymous guests, online time, recent daily deltas, and a
  24-hour login histogram for the snapshot date.
- Stat snapshots also create deterministic daily KBS-style `BBSLists`
  board-activity history threads showing total board/thread/post counts, top
  public board rankings, last public board activity times, and recent
  board/thread/post/reaction deltas.
- Stat snapshots also create deterministic daily KBS `toplog`-style
  `BBSLists` hot-topic history threads showing ranked public hot topics,
  distinct participant counts, post/reaction counts, decayed score, and last
  activity times, with KBS `day_sec*`-style category/section hot-topic groups.
- On completed period boundaries, stat snapshots create deterministic KBS
  `poststat`-style `BBSLists` weekly, monthly, and yearly activity-history
  threads from daily stat-history rows, including period totals, ending
  counters, max-online peaks, and daily rows.
- On completed period boundaries, stat snapshots also create KBS `poststat` /
  `toplog`-style weekly, monthly, and yearly public hot-topic list threads
  ranked by period posts, distinct participants, reactions, and category/section
  groupings.
- Web `Rankings` page shows community counters, max-online peaks, recent daily
  stat history with trend deltas, cumulative login count, cumulative online/stay
  time, anonymous guest counts, active boards, hot threads, latest replies, top
  posters, blessing counts, and archive paths, with board/thread/reply/archive
  rows opening the relevant reading surface.
- Web `Rankings` includes a 30-day selectable history chart for post deltas,
  login deltas, user totals, reaction deltas, online-time deltas, max-online
  users, and max anonymous guests, plus compact latest/previous/low/high
  summaries for the selected metric.

This does not yet implement every historical/stat-log board from KBS local
utilities beyond the generated `BBSLists` snapshot, login-history, and
board-activity/hot-topic/period-summary history threads.

### Board-Level Unread Workflow And Read Markers

KBS put unread boards and read-marker management on the main reading path.
Budgie now has the first board-level version:

- A per-user `board_read_markers` projection.
- A saved `previous_seq` marker for restoring after an accidental mark-read.
- Authenticated summary APIs:
  - `GET /api/v1/boards/summary`
  - `GET /api/v1/boards/unread`
- REST aliases:
  - `POST /api/v1/boards/{board}/read`
  - `POST /api/v1/boards/{board}/read/restore`
- Uniform commands:
  - `markBoardRead`
  - `restoreBoardRead`
- Web board navigation now exposes `Unread`, `Favorites`, and `All Boards`
  sections, with compact mark-read and restore-marker controls.

This section is board-level only; thread/article-level unread traversal is
tracked below.

### Thread-Level First-Unread Navigation

KBS reading flows distinguish unread boards from unread articles inside a
thread. Budgie now has the first thread-level version:

- A per-user `thread_read_markers` projection.
- Thread summary reads from `GET /api/v1/boards/{board}/threads` include:
  - `unreadPosts`
  - `readSeq`
  - `firstUnreadPostId`
- Thread summary reads support `?unread=1` for board-level unread-thread
  navigation.
- Thread summary reads support KBS-style board-local search filters:
  `GET /api/v1/boards/{board}/threads?q=&author=`.
- Site-wide unread thread queue:
  `GET /api/v1/threads/unread?favorites=&folder=`.
- Favorite-folder unread traversal includes boards in the selected favorite
  folder and descendant folders.
- REST aliases:
  - `POST /api/v1/threads/{thread}/read`
  - `POST /api/v1/threads/{thread}/read/restore`
  - `POST /api/v1/posts/{post}/read`
- Uniform commands:
  - `markThreadRead`
  - `restoreThreadRead`
  - `markPostRead`
- Web thread lists show unread post counts, open at the first unread post, and
  expose compact thread mark-read/restore controls.
- Web thread lists expose board-local title and author search controls.
- Web thread readers expose previous/next unread controls, mark-all-read,
  restore-marker, and per-post `Mark to here` actions.
- Web thread readers expose previous/next unread-thread controls within the
  current board.
- Web thread readers expose KBS-style quoted-reply actions that open the reply
  composer with editable quoted article text.
- The `appendPost` command and thread-post REST alias accept `quotePost` with
  `replyTo` so non-web clients can request the same server-generated quoted
  reply body while preserving direct reply links.
- Web `Unread` page exposes site-wide unread threads, favorite-board unread
  threads, favorite-folder unread threads, direct first-unread opening, and
  mark-thread-read controls.
- Web thread readers expose first/last post jumps for same-topic reading.
- Web thread readers expose same-author trails within the loaded thread.
- Authenticated author-post reads and a web `Author posts` view expose
  cross-board same-author reading across boards the viewer can read. The public
  profile recent-posts view is restricted to public-board posts.
- Authenticated reply-tree reads and a web `Reply tree` focus mode expose root
  plus descendant reply traversal inside a thread.

### Article Metadata Flags

KBS articles carried local metadata flags beyond the body text. Budgie now has
the first article-flag workflow:

- Posts carry KBS-style `marked`, `recommended`, `noReply`, `tex`, and
  `mailBack` metadata.
- Uniform command:
  - `setPostFlag`
- REST alias:
  - `PATCH /api/v1/posts/{post}/flags`
- `marked` and `recommended` require board curation permission.
- `noReply` requires board thread-moderation permission.
- `tex` and `mailBack` can be set by the article author or a board
  thread-moderator.
- A no-reply thread starter blocks ordinary replies to the thread.
- A no-reply parent article blocks ordinary direct replies to that article.
- Board thread moderators and delegated `canModerateThreads` members can
  bypass article-local reply stops.
- Replies to a non-anonymous `mailBack` article generate a durable private-mail
  copy to the opted-in article author when mail quota and ignore relationships
  allow delivery.
- Web thread readers show article flag badges and expose mark/recommend/no-reply,
  TeX, and mail-back controls to users with the corresponding permissions.

### Cross-Post And Repost Lineage

KBS readers could cross-post/repost articles while preserving the original
article context. Budgie now has the first forum-native version:

- Posts can carry source article lineage:
  - `sourcePost`
  - `sourceThread`
  - `sourceBoard`
  - `sourceAuthor`
  - `sourceAuthorId`
  - `sourceTitle`
- Uniform command:
  - `repostPost`
- REST alias:
  - `POST /api/v1/posts/{post}/repost`
- Reposting creates a normal new destination-board thread authored by the
  actor, while the root post records the source article/thread/board/author
  metadata.
- The actor must be able to read the source board and post to the destination
  board. Member-read source boards cannot be reposted by non-members.
- Repost posts include a visible source attribution block in the article body.
- Web thread readers show a repost badge/source line and expose a per-article
  `Repost` action.
- This slice does not clone attachment blobs; attachment-forwarding remains a
  possible follow-up.

### Board Policy Flags And Moderator Lists

KBS boards carry local policy and moderator metadata. Budgie now has the first
enforced version:

- A per-board `board_settings` projection with:
  - `anonymousAllowed`
  - `readOnly`
  - `noReply`
  - `attachmentsAllowed`
  - `mailInAllowed`
  - `relayEnabled`
  - `memberReadMode`
  - `memberPostMode`
  - `statsExcluded`
- A per-board `board_moderators` projection.
- A per-board `board_members` projection with optional board-local titles,
  explicit member-roll ordering, and delegated `canManageMembers`,
  `canCurate`, `canModeratePosts`,
  `canModerateThreads`, `canAnnounce`, and `canSetBoardSettings` permissions.
- A per-board `board_member_applications` projection with pending, approved,
  rejected, and blacklisted statuses.
- A per-board `board_member_requirements` projection with admission mode,
  login-count/post-count/trust-level minimums, board-local post/original/digest
  minimums, reaction-score/board-mark minimums, and member caps.
- A `user_activity.login_count` counter populated by successful registration
  and login flows.
- A per-post `post_attachments` projection populated from durable
  `post.appended` attachment metadata.
- A DB-backed `attachment_blobs` storage table for binary uploads/downloads.
- Authenticated board detail API: `GET /api/v1/boards/{board}`.
- Authenticated member list API: `GET /api/v1/boards/{board}/members`.
- Authenticated member application API:
  `GET /api/v1/boards/{board}/member-applications?status=`.
- Admin relay queue API:
  `GET /api/v1/relay/deliveries?status=pending`.
- REST aliases:
  - `PATCH /api/v1/boards/{board}/settings`
  - `PUT /api/v1/boards/{board}/moderators/{user}`
  - `DELETE /api/v1/boards/{board}/moderators/{user}`
  - `PATCH /api/v1/boards/{board}/member-requirements`
  - `PUT /api/v1/boards/{board}/members/{user}`
  - `DELETE /api/v1/boards/{board}/members/{user}`
  - `POST /api/v1/boards/{board}/member-applications`
  - `POST /api/v1/board-member-applications/{application}/review`
  - `POST /api/v1/boards/{board}/members/leave`
  - `POST /api/v1/boards/{board}/mail-in`
  - `POST /api/v1/threads/{thread}/mail-in`
- Uniform commands:
  - `postBoardMail`
  - `setBoardSettings`
  - `setBoardMemberRequirements`
  - `setBoardModerator`
  - `setBoardMember`
  - `applyBoardMembership`
  - `reviewBoardMembership`
  - `leaveBoardMembership`
- Posting enforcement:
  - `readOnly` blocks new threads for normal users.
  - `noReply` blocks replies for normal users.
  - `anonymousAllowed` enables anonymous public author identity.
  - `memberPostMode` blocks non-members from creating threads or replies.
  - `memberReadMode` blocks non-members from board detail, digest, thread list,
    thread, post-list, and scoped search reads.
  - Global post search hides member-read board posts from viewers who cannot
    read those boards.
  - `attachmentsAllowed` blocks normal users from adding post attachment
    metadata when disabled.
  - `mailInAllowed` gates authenticated inbound mail bridge posting; mail-in
    creates new threads or appends replies through the normal posting path.
  - `relayEnabled` queues pending outbound relay delivery records for posts
    created on that board.
  - `statsExcluded` keeps otherwise readable/postable boards out of public
    board/thread/reply/user/archive rankings and generated `BBSLists`
    snapshot, board-activity, and hot-topic stat logs.
  - Board membership applications enforce `maxMembers`, minimum login count,
    minimum post count, minimum trust level, board-local post count,
    board-local original-thread count, board-local digest count, global reaction
    score, and board-local received marks; eligible applications can be
    auto-approved by board policy.
  - Public-board membership approvals mirror into the KBS-style `Registry`
    system board, and rejected/blacklisted applications mirror into
    `reject_registry`. Generated bodies are sanitized and omit application and
    review notes; member-read board applications stay private to the board
    member-manager queue.
  - Board moderators can bypass local posting locks and moderate their board.
  - Role grants/revokes, public-board setting changes, and public-board
    moderator appointments/removals mirror into a KBS-style `syssecurity`
    system board. Member-read board security changes are not mirrored into the
    public audit board.
- Delegated board members can now:
  - Review applications and edit the member roster with `canManageMembers`.
  - Mark/recommend articles and curate digest/archive/recommended/pinned entries
    with `canCurate`.
  - Curate announcement entries with `canAnnounce`.
  - Redact and restore board posts with `canModeratePosts`.
  - Rename/lock/move threads and toggle article no-reply flags with
    `canModerateThreads`.
  - Publish public poll-result records with `canManagePolls`.
  - Edit board policy flags and member admission requirements with
    `canSetBoardSettings`.
- KBS-style member-manager edge permissions are enforced: delegated member
  managers can approve/reject applications and manage ordinary member rows, but
  cannot grant/revoke delegated operator flags, blacklist applications, review
  their own application, manage board moderators, or manage members who already
  hold delegated board permissions.
- Web thread lists expose board policy badges, settings toggles, moderator
  controls, member controls, member-manager controls, and delegated permission
  badges/toggles.
- Web new-thread and reply composers expose anonymous posting when enabled.
- Web new-thread and reply composers expose attachment metadata controls when
  enabled, and thread readers show attachment chips on posts.
- Web new-thread and reply composers preserve local drafts, keep failed
  submissions editable, expose rendered previews, and provide a full-screen
  compose mode for longer text-heavy posts.
- Web thread readers expose title rename, post-level file upload, and
  authenticated download for stored attachments.
- Web board lists expose a membership application action for member-mode boards.
- Web board settings expose membership requirements, pending applications with
  approve/reject actions for delegated managers, blacklist actions and delegated
  member-permission toggles for full moderators, and current-member leave flows.

External SMTP/Internet-email bridges and legacy transfer protocols such as
ZModem are intentionally outside the current BBS/forum parity focus.

### Digest And Archive Curation

KBS preserved good content by importing articles into board-linked digest and
archive areas. Budgie now has the first durable curation layer:

- A board-local `digest_entries` projection.
- Curated targets can be either posts or whole threads.
- Curated entries carry:
  - `kind`: digest, archive, recommended, pinned, or announcement.
  - `path`: a lightweight archive/menu path.
  - `title`
  - `note`
  - curator metadata and timestamps.
- Authenticated read API: `GET /api/v1/boards/{board}/digest`.
- Authenticated archive path tree API:
  `GET /api/v1/boards/{board}/digest/tree?kind=`.
- Site-wide digest and announcement APIs that respect board read policy:
  - `GET /api/v1/digest?kind=&path=`
  - `GET /api/v1/announcements?path=`
- Authenticated archive/digest search API:
  `GET /api/v1/digest/search?q=&board=&kind=&path=`.
- Authenticated archive/digest text download API:
  `GET /api/v1/digest/{entry}/download`.
- REST aliases:
  - `POST /api/v1/posts/{post}/digest`
  - `POST /api/v1/threads/{thread}/digest`
  - `POST /api/v1/boards/{board}/digest/directories`
  - `POST /api/v1/boards/{board}/digest/paths/move`
  - `POST /api/v1/boards/{board}/digest/paths/copy`
  - `DELETE /api/v1/boards/{board}/digest/paths?kind=&path=`
  - `POST /api/v1/digest/{entry}/mail`
  - `PATCH /api/v1/digest/{entry}`
  - `PUT /api/v1/digest/{entry}/body`
  - `DELETE /api/v1/digest/{entry}/body`
  - `DELETE /api/v1/digest/{entry}`
- Uniform commands:
  - `curatePost`
  - `curateThread`
  - `createDigestDirectory`
  - `moveDigestPath`
  - `copyDigestPath`
  - `deleteDigestPath`
  - `updateDigestEntry`
  - `setDigestEntryBody`
  - `sendDigestEntryMail`
  - `removeDigestEntry`
- Board moderators and delegated board curators can curate and remove entries
  for their boards.
- Curated post exports include the full article body; curated thread exports
  include a transcript of non-redacted posts. Downloads and mail-out exports
  both enforce the owning board's read policy.
- Curators can rename archive entries, move them between lightweight archive
  paths, edit archive article bodies, and reset edited bodies back to the source
  article/thread export. Search, download, and mail-out prefer edited archive
  text when present.
- Curators can create explicit empty archive directories and move, copy, and
  delete lightweight archive path subtrees. Copied entries keep the same curated
  target/title/note/body text but receive fresh entry ids and curator ownership
  metadata; copied explicit directories receive fresh directory ids.
- Public-board announcement curation lazily creates the `0announce` system board
  and a deterministic generated thread/post in it. Member-read board
  announcements stay digest-only to avoid exposing private content.
- Public-board recommended curation lazily creates the KBS-style `Recommend`
  system board and a deterministic generated thread/post in it. Member-read
  recommendations stay digest-only to avoid exposing private content.
- Admin-triggered and automatic stat snapshots lazily create the `BBSLists`
  system board and deterministic generated daily stat/ranking threads.
- Public blessing rituals lazily create the `Blessing` system board and
  generated public blessing threads/posts.
- Public poll result publishing lazily creates the `vote` system board and
  deterministic generated result threads/posts. Poll/thread authors, board
  moderators, site moderators/admins, and delegated board members with
  `canManagePolls` can publish those records. Member-read poll results remain
  only on the source board.
- Admin-managed content filters create open moderation reviews when new threads
  or replies match active global/board-scoped patterns. Public-board matches
  lazily create sanitized KBS-style `Filter` system-board records; member-read
  board matches stay only in the moderator queue.
- Public-board posting mutes/bans lazily create sanitized `denypost`
  system-board records, and sanction clears create matching `undenypost`
  restoration records. Global and member-read board sanctions remain private
  moderation state.
- Public-board moderation flags and review resolutions lazily create sanitized
  `0moderation` audit-log threads. Member-read board reviews stay only in the
  moderator queue so private report text and article bodies are not mirrored.
- Public-board member-application approvals/rejections/blacklists lazily create
  sanitized `Registry` and `reject_registry` system-board records.
- Role grants/revokes, public-board setting changes, and public-board
  moderator appointments/removals lazily create sanitized `syssecurity`
  security/admin audit records.
- Completed account registrations lazily create sanitized `newcomers`
  system-board records with deterministic thread/post IDs.
- Admins can publish public operator notices to lazily-created KBS-style
  `notepad`, `GiveupNotice`, and `bbsnet` system boards. These generated notice
  boards remain directly readable while staying out of organic community
  counters and ranking surfaces.
- Web thread lists expose a KBS-style top/pinned article index above ordinary
  board digests, with pinned entries sourced from `kind=pinned` digest records.
- Web thread lists expose a board digest panel for non-pinned curated records.
- Web thread readers expose moderator/curator digest and pin curation actions
  for threads and posts.

This still does not implement every KBS forum-board workflow such as richer
stat-history boards.

### Profiles, Signatures, And Password Changes

KBS treated identity and personal presentation as part of daily forum use.
Budgie now has first profile and password-management workflows:

- User profiles expose editable display name, public title/rank, bio, avatar,
  signature text, plan/profile text, and homepage URL.
- `PATCH /api/v1/users/me` accepts `title`, `signature`, `plan`, and
  `homepage`.
- Private contact profiles store real name, real/registration email, address,
  phone/mobile, birthday, school, and contact notes outside the public profile
  projection.
- Personal files let users maintain up to 16 named text files beyond the fixed
  plan/signature fields, with per-file public/private visibility.
- Authenticated private-contact APIs:
  - `GET /api/v1/users/me/private-profile`
  - `PATCH /api/v1/users/me/private-profile`
  - `GET /api/v1/users/{name}/private-profile` for admins
- Personal-file APIs:
  - `GET /api/v1/users/{name}/files`
  - `GET /api/v1/users/{name}/files/{file}`
  - `GET /api/v1/users/me/files`
  - `GET /api/v1/users/me/files/{file}`
  - `PUT/PATCH /api/v1/users/me/files/{file}`
  - `DELETE /api/v1/users/me/files/{file}`
- Authenticated signature-bank APIs:
  - `GET /api/v1/users/me/signatures`
  - `POST /api/v1/users/me/signatures`
  - `PATCH /api/v1/users/me/signatures/{signature}`
  - `DELETE /api/v1/users/me/signatures/{signature}`
  - `PATCH /api/v1/users/me/signatures/settings`
  - `POST /api/v1/users/me/signatures/recount`
- `PATCH /api/v1/users/me/password` lets authenticated users change their own
  password after verifying the current password.
- Password recovery requests let unauthenticated users submit account, real
  name, email, and note evidence for admin review. Admins can list pending
  requests and either reset the password or reject the request:
  - `POST /api/v1/auth/password-recovery`
  - `GET /api/v1/admin/password-recovery?status=pending`
  - `POST /api/v1/admin/password-recovery/{request}/review`
- `POST /api/v1/users/me/deactivate` lets authenticated users close their own
  account after verifying the current password.
- Authenticated login host ACL APIs:
  - `GET /api/v1/users/me/login-acl`
  - `POST /api/v1/users/me/login-acl/rules`
  - `PATCH /api/v1/users/me/login-acl/rules/{rule}`
  - `DELETE /api/v1/users/me/login-acl/rules/{rule}`
  - `PATCH /api/v1/users/me/login-acl/settings`
- Admin account-registration approval APIs:
  - `GET /api/v1/admin/registration-settings`
  - `PATCH /api/v1/admin/registration-settings`
  - `GET /api/v1/admin/registrations?status=pending`
  - `POST /api/v1/admin/registrations/{name}/review`
- Completed registrations lazily create a KBS-style `newcomers` system-board
  article. Generated newcomer records contain public account status only and are
  excluded from community ranking/counter surfaces.
- Admins can require account approval. Pending registrations reserve the name
  but receive no session token and cannot authenticate; approval creates the
  normal `newcomers` public record, while rejection keeps the account locked
  out.
- Admins can transfer a user's login ID/name while preserving the stable
  internal account ID and updating authored thread/post display names.
- Admins can hard-delete a user's account row and private/user-owned state while
  preserving old public discussions under a `[deleted]` author tombstone.
- Public profile reads include public title/rank, the current signature,
  plan/profile text, and homepage URL.
- Users can maintain up to eight saved signatures, choose a fixed current
  signature, or rotate randomly among active saved signatures.
- Users can manually recount/repair their saved-signature bank. The recount
  action returns total/active counts, clears stale fixed selections, and
  refreshes the public current-signature preview.
- New thread starters and replies snapshot the author's selected/random current
  signature into the post projection and durable `post.appended` event.
- Later signature edits do not rewrite old posts.
- Anonymous posts do not expose the user's signature.
- Web profile editing includes homepage and plan/profile fields plus a
  title/rank field, signature editor, and saved-signature bank. Profile pages
  preview title/rank, the current plan/profile, and signature, and thread
  readers render captured post signatures below article bodies.
- Web profile editing includes a private contact panel for the caller's own
  real/contact registration fields.
- Web profile pages render public personal files, while the caller's own
  profile includes public/private personal-file editors.
- Users can maintain a disabled-by-default login allow-list using exact IP,
  CIDR, or wildcard host rules. When enabled, password login is rejected unless
  the current request host matches an active rule.
- Web profile editing includes a login-host allow-list panel.
- Admin profile controls include transfer-id and hard-delete account actions.
- Self-deactivation rejects future logins and old tokens, and lazily creates a
  KBS-style `Goodbye` system-board article. Private deactivation notes are kept
  out of the generated public post.

### Private Mail And Short Messages

KBS split private communication into durable article-like mail and short
presence-oriented messages. Budgie now has the first usable version of that
split:

- Durable `mail_messages` projection for private mail bodies.
- Per-user `mail_copies` projection for inbox, sent, trash, kept/custom mailbox,
  read-state, and sender/recipient copy state.
- User-owned `mail_groups` and `mail_group_members` projections for private
  mailing lists.
- Message-scoped `mail_attachments` and `mail_attachment_blobs` projections for
  URL/metadata attachments and sender-uploaded binary mail files.
- Mail usage/quota reads calculate non-trash mailbox space from message
  subject/body and attachment sizes.
- Durable `direct_messages` projection for short user-to-user conversations.
- Per-user `direct_message_settings` projection for pager/direct-message
  delivery policy.
- Account-scoped durable events:
  - `mail.sent`
  - `mail.attachment_added`
  - `direct_message.sent`
- Authenticated mail read APIs:
  - `GET /api/v1/mail?mailbox=&unread=`
  - `GET /api/v1/mail/groups`
  - `GET /api/v1/mail/usage`
  - `GET /api/v1/mail/attachments/{attachment}`
  - `GET /api/v1/mail/{mail}`
- Authenticated short-message read APIs:
  - `GET /api/v1/messages`
  - `GET /api/v1/messages/settings`
  - `GET /api/v1/messages/{user}`
- REST aliases:
  - `POST /api/v1/posts/{post}/mail`
  - `POST /api/v1/mail`
  - `POST /api/v1/mail/groups`
  - `PUT/PATCH /api/v1/mail/groups/{group}`
  - `DELETE /api/v1/mail/groups/{group}`
  - `POST /api/v1/mail/{mail}/attachments`
  - `PATCH /api/v1/mail/{mail}`
  - `DELETE /api/v1/mail/{mail}`
  - `POST /api/v1/messages`
  - `PATCH /api/v1/messages/settings`
  - `POST /api/v1/messages/{message}/read`
  - `DELETE /api/v1/messages/{message}`
- Uniform commands:
  - `mailPostAuthor`
  - `sendMail`
  - `setMailGroup`
  - `deleteMailGroup`
  - `attachMail`
  - `updateMail`
  - `deleteMail`
  - `sendDirectMessage`
  - `setDirectMessageSettings`
  - `markDirectMessageRead`
  - `deleteDirectMessage`
- Mail compose expands direct recipients, named/id mail groups, `group:` tokens,
  and the sender's friend list, with duplicate recipient suppression.
- The built-in dynamic `friends` mail group is returned by the mail-group API
  and can be used anywhere a named/id mail group is accepted.
- Thread readers can mail a visible non-anonymous article author with article
  context; the flow uses durable private mail, not an external email bridge.
- Admin-only sysop mail-all uses the same durable mail path to address every
  other user, bypassing personal ignore rows while still enforcing mailbox
  quota and creating the sender's sent copy.
- Admin-only sysop mail-all also mirrors into the KBS-style restricted
  `sysmail` generated board as an operator-visible thread/post, with the board
  forced into member-read, member-post, read-only, and no-reply mode.
- Mail compose can include attachment metadata, sender-owned mail can receive
  uploaded binary files, and recipients with a visible mail copy can download
  stored mail attachments.
- Mail send, sender upload, and trash-restore flows enforce per-user mailbox
  quota before adding visible non-trash mail space.
- Short direct messages respect ignore rows and per-recipient policy:
  all users, friends only, or no messages.
- Web thread readers expose a Mail action for visible non-anonymous article
  authors. Web `Inbox` surface with mailboxes, unread badges, mail compose/reply,
  read/keep/move/trash actions, mail-group management, group/friend-list
  sending, used/quota display, direct-message conversations, receive-policy
  control, replies, and per-user message deletion.

POP3/Internet email bridges and SMS are intentionally outside the current
BBS/forum parity focus; real-time online-user pager shortcuts remain a possible
forum/social workflow.

### Friends, Fans, Ignores, And Online Friends

KBS used the friend list as daily navigation, notification routing, and a bridge
between reading and private communication. Budgie now has the first usable
social graph:

- A per-user `user_relationships` projection for:
  - friends/following
  - ignores/badlist entries
  - private friend notes
  - one-shot login watches for existing friends
- Fans are derived from reverse friend rows.
- Mutual friendship is derived when both users friend each other.
- A `user_presence` projection records each user's latest `setPresence` status,
  mode, board/thread location, display location label, optional from-host, and
  timestamp.
- Authenticated social read APIs:
  - `GET /api/v1/social/friends`
  - `GET /api/v1/social/fans`
  - `GET /api/v1/social/ignores`
  - `GET /api/v1/social/online-friends`
  - `GET /api/v1/presence/online`
  - `GET /api/v1/boards/{board}/online`
- REST aliases:
  - `PUT /api/v1/users/{user}/friend`
  - `DELETE /api/v1/users/{user}/friend`
  - `PUT /api/v1/users/{user}/ignore`
  - `DELETE /api/v1/users/{user}/ignore`
  - `PUT /api/v1/users/{user}/login-watch`
  - `DELETE /api/v1/users/{user}/login-watch`
  - `POST /api/v1/users/{user}/bless`
  - `POST /api/v1/presence`
- Uniform commands:
  - `setUserRelationship`
  - `setLoginWatch`
  - `blessUser`
- Uniform session command: `setPresence`.
- Ignore rows now block private mail, short direct messages, and public
  blessings from the ignored user.
- Login watches are friend-gated, one-shot waits that create a `login`
  notification when the friend next publishes online presence; active friends
  notify immediately, and ignore rows suppress delivery.
- Public blessing rituals create `user.blessed` events, update blessing
  rankings, and lazily mirror each blessing into a KBS-style generated
  `Blessing` board thread/post.
- Online-user reads include session id, status, mode, current board/thread,
  location label, idle seconds, optional from-host, mutual-friend state, and
  ignore state. Multiple visible sessions for the same user are returned as
  separate online rows, while social/friend reads keep a one-row summary.
- Board-scoped online reads require board read access; global online reads mask
  board/thread details for member-read boards the viewer cannot enter.
- User-level `invisible` presence clears visible location details, hides the
  user from online reads, and does not satisfy login watches until visible
  presence is published again.
- Privileged `cloak` presence is restricted to moderators/admins, is hidden from
  ordinary online lists, online-friend reads, public online counts, and login
  watches, while moderators/admins can still see cloaked users in global and
  board-scoped online reads.
- Web `People` surface exposes all online users, online friends, friends, fans,
  and ignores with presence badges.
- Web board pages publish reading presence and expose board-local online users.
- Web thread pages publish thread-level reading presence.
- Web chat exposes online friends in-room with direct-message shortcuts.
- Web profile pages expose add-friend and ignore actions.
- Web thread readers expose add-author-as-friend and ignore-author actions from
  article context.
- Web People rows and board-local online chips can open a direct-message draft
  for the selected online user.

## Existing Budgie Coverage That Maps To KBS

- Boards and board creation.
- Threaded posts with explicit reply links.
- Search.
- Reactions.
- Polls.
- Profiles and trust/activity counters.
- Moderation review queue, redaction, restore, locks, moves, sanctions, and
  role grants.
- Notifications for mentions, replies, and watched threads.
- Thread watch/mute preferences.
- Board-level unread counts and read-marker restore.
- Favorite tree import/export and favorite-folder read-marker restore.
- Thread-level unread counts, first-unread opening, and read-marker restore.
- Article-level mark-to-here controls and in-thread previous/next unread
  navigation.
- Board-local previous/next unread-thread navigation.
- Site-wide and favorite-folder unread-thread traversal.
- First/last post jumps, same-author trails inside a loaded thread, and
  cross-board same-author reading through authenticated author-post streams.
- Reply-tree-specific traversal inside a loaded thread.
- KBS-style quoted replies from thread reading and the append-post API.
- KBS-style thread title changes via `setThreadTitle` and
  `PATCH /api/v1/threads/{thread}/title`; thread starters can rename inside the
  edit window, and board thread moderators can rename later.
- KBS-style article flags for marked, recommended, no-reply, TeX, and
  mail-back articles, with mail-back replies bridged into private mail.
- KBS-style mail-author-from-article action via `mailPostAuthor` and
  `POST /api/v1/posts/{post}/mail`, adding article context to durable private
  mail while rejecting anonymous or redacted articles.
- KBS-style compose recovery and preview for new threads and replies.
- Cross-post/repost article creation with source post/thread/board/author
  lineage.
- Board policy flags, board detail reads, board-local moderator lists, and
  board-local member rolls, including KBS-style stats-excluded boards.
- Enforced read-only, no-reply, anonymous-posting, board-moderator, and
  member-only read/post flows.
- Board member application, approval/rejection/blacklist, and self-leave flows.
- Sanitized `Registry` / `reject_registry` generated records for public-board
  member-application decisions.
- Sanitized `newcomers` generated records for completed account registrations.
- Sanitized `syssecurity` generated records for role grants/revokes,
  public-board setting changes, and public-board moderator appointments/removals.
- Board member admission requirements for login-count/post-count/trust-level
  minimums, board-local post/original/digest minimums, reaction-score/board-mark
  minimums, member caps, manual approval, and auto-approval.
- Editable profile signatures, saved-signature banks, fixed/random signature
  selection, and per-post signature snapshots.
- Per-user login host ACLs for exact IPs, CIDR ranges, and wildcard host
  prefixes.
- Authenticated self-service password changes.
- Authenticated self-service account deactivation with `Goodbye` generated
  system-board records.
- Admin hard account deletion with tombstoned public authorship.
- Hierarchical board categories, role-filtered category projection reads, and
  admin category management in the web board directory.
- Board summary search, new-board filtering, and sort modes by name, newest,
  online users, article/thread count, recent activity, and unread activity.
- Delegated board-member manager, curator, announcement, post-moderation,
  thread-moderation, and board-settings permissions without full board
  moderator status.
- Post attachment metadata gated by board policy and shown in web thread
  readers.
- Binary post attachment upload and authenticated download.
- Board-local digest/archive curation for posts and threads.
- Derived archive path-tree reads and site-wide digest/announcement reads
  across readable boards.
- Archive/digest search across readable boards.
- Archive/digest text download and private-mail export flows.
- Archive entry rename/path move and edited-body/reset flows.
- Archive path-subtree move/copy/delete flows.
- Explicit empty archive-directory records.
- Generated KBS-style `Recommend` board threads/posts for public recommended
  curation.
- Digest browsing and moderator/curator curation/removal controls in the web UI.
- Authenticated inbound board mail posting for mail-in-enabled boards.
- Pending outbound relay delivery queue for relay-enabled boards.
- Durable private mail with inbox/sent/trash/kept mailbox workflows.
- Admin-only sysop mail-all broadcast through durable private mail and
  restricted `sysmail` generated board records.
- Built-in friend-list mail group expansion.
- Short direct messages with conversation reads and unread counts.
- Friends/fans/ignore lists, online-friend reads, global and board-scoped
  online-user reads, and article/profile relationship actions.
- Friend login-watch notifications.
- Multiple-session presence, user-level invisible presence, and privileged
  cloak presence.
- Anonymous web guest presence pings, online guest counters, daily guest deltas,
  and max guest peaks.
- Direct-message shortcuts from online People rows and board online chips.
- Community counters including cumulative login count, cumulative online/stay
  time and anonymous guest counters, active-board rankings, hot-thread rankings,
  distinct-participant hot-topic scoring, top-poster rankings, latest-reply
  rankings, blessing rankings/rituals, archive-path rankings, automatic
  `BBSLists` generated stat snapshots and login-count, board-activity, and
  hot-topic history posts, category/section hot-topic groups, 24-hour login
  histograms, plus completed week/month/year activity and hot-topic summaries,
  and a web `Rankings` surface with selectable 30-day history charts.
- Sanitized `0moderation` generated audit posts for public-board flags and
  moderation review resolutions.
- Admin-managed global/board-scoped content filters, automatic content-filter
  moderation reviews, and sanitized KBS-style `Filter` system-board records for
  public-board matches.
- Admin-published public operator notices on `notepad`, `GiveupNotice`, and
  `bbsnet` generated system boards.
- Generated `Blessing` board threads/posts for public blessing rituals.
- Deterministic KBS-style `vote` system-board result posts for public polls,
  with member-read poll results kept on their source board.
- Sanitized KBS-style `denypost` / `undenypost` system-board records for
  public-board posting sanction and restoration events, with private boards
  kept out of the public mirror.
- Live chat lines, rich presence events, current board/thread presence, and
  online-friend chat shortcuts in the web UI.
- HTTP, web, WebSocket/SSE, SSH TUI, and NNTP-facing surfaces.

## Remaining Major KBS Parity Areas

- Remaining specialized historical/stat-log boards and richer community
  statistics.
- Explicitly out of the current BBS/forum parity goal: POP3/SMTP bridges,
  blog/personal corpus, legacy transfer protocols, SMS/pager layers, sysop
  import/repair tooling, and optional campus utilities/games.

## Suggested Next Slices

1. Remaining specialized historical/stat-log boards and richer community
   statistics.
