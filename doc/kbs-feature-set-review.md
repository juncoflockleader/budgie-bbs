# KBS BBS Feature Set Review

This document reviews KBS BBS as a product, not as a backend architecture. The
code is old and the implementation is not a modern model, but the feature set is
valuable: it captures what early-2000s Chinese college BBS users expected from a
campus-scale online community.

Primary sources reviewed:

- `doc/kbsIntro.txt`, decoded from GBK, especially board, article, user, mail,
  and shared-state sections.
- `bbshome/etc/menu.ini`, decoded from GBK, for actual telnet menu features.
- `bbshome/help/*`, decoded from GBK, for key-level user workflows.
- `doc/README.SYSOP`, `doc/README.blog`, `doc/INSTALL.blog`, and
  `doc/README.utils`, decoded from GBK, for operations and optional modules.
- Source modules under `src/`, `libBBS/`, `php/`, `service/`, `daemon/`,
  `mail2bbs/`, `innbbsd/`, and `bbs2www`.

## Executive Summary

KBS BBS was not merely a forum. It was a campus social operating system built
around boards, user identity, real-time presence, private mail, short messages,
chat rooms, archives, moderation workflows, public rankings, and optional web
and blog surfaces.

The most reusable product ideas are:

- Boards are high-context communities, not just categories.
- A user's daily workflow starts from unread items, favorite boards, live users,
  and private notifications.
- Moderation is deeply integrated into reading and board management.
- Private messaging, mail, mentions, replies, and online presence form a
  parallel social layer beside public posts.
- "Archive" and "digest" are first-class content curation flows.
- A community can support both serious information exchange and lightweight
  social rituals: chat rooms, diaries, games, rankings, blessings, and notices.

## Product Model

KBS centers on several durable primitives.

### Users

Users have stable IDs, nicknames, real-profile fields, titles, permission bits,
login counts, post counts, total online time, signatures, personal plans, friend
lists, mail settings, read markers, favorite boards, and optional personal/blog
spaces.

The user model is more expansive than a typical forum account:

- Public identity: ID, nickname, title/rank, plan/profile, signature, homepage,
  photo/avatar fields, visible stats.
- Private identity: real name, real email, registration email, address, phone,
  birthday, mobile number, school/contact fields in optional address-list code.
- Behavioral state: login count, last host, total time online, article count,
  score in later forks, current online mode, current board, invisible/cloak
  mode.
- Personal configuration: pager/message options, screen/menu preferences,
  unread display settings, language conversion, mailbox behavior, IP login ACL,
  custom hotkeys.

Product lesson: a BBS account was both a forum identity and a campus identity.
It carried reputation, contactability, permissions, and personal expression.

### Boards

Boards are the primary public community unit. A board has:

- English filesystem/name ID.
- Chinese display title and description.
- Section and category metadata.
- Moderator list.
- Access permissions.
- Board-level flags: read-only, no-reply, can/cannot be zapped, can accept
  email posts, participates in statistics, supports attachments, supports
  outgoing relay, anonymous posting, club modes, censorship/filter modes,
  board-member read/post modes, score functions in later forks.
- Digest/archive path.
- Parent directory/group relationship.
- Current article count, online user count, top posts, last post ID, and
  thread/origin cache status.

Boards can also be directories, allowing nested board hierarchies. The product
distinction is important: a board can be a place to post, or a folder-like entry
that organizes other boards.

### Articles

Articles are indexed by per-board records. Each article tracks:

- Filename and timestamp-derived storage name.
- Article ID, thread group ID, and reply-to ID.
- Cross-board / recommended original metadata.
- Relay status.
- Owner.
- Effective size.
- Post time.
- Attachment offset.
- Title.
- Flags: marked, digest, imported, no-reply, deleted, mail-back replies,
  recommended, censored, TeX mode, Like/score-related flags in later forks.

The `id`, `groupid`, and `reid` fields are the key product idea: threading does
not depend on title matching. A reply tree is explicit. This enables same-topic
reading, first/last/next in thread, reply tree traversal, and thread-level web
views.

### Mail And Messages

KBS has two private communication layers:

- Mail: durable, article-like private messages with inbox, sent, deleted,
  custom mailboxes, forwarding, replies, moving, marking, attachments, group
  sending, and optional Internet email.
- Messages: short real-time messages tied to online users, with unread queues,
  reply shortcuts, history display, web-message support, and later a
  SQLite-backed threaded/new-message system with attachments.

This split is product-significant. Mail is asynchronous and archival; messages
are presence-oriented and conversational.

### Presence

Online presence is first-class. The system tracks online users, current mode,
current board, idle time, from-host, chat ID, friend status, pager state, and
whether a user is visible or cloaked. Menus expose:

- Online user lists.
- Friend online lists.
- Current-user query.
- Talk/pager/message actions from lists.
- Monitor views.
- Admin UTMP/WWW guest lists.
- Board online user counts.

Product lesson: old BBS communities were live spaces. Reading was not isolated
from who else was present.

## Main User-Facing Feature Areas

### 1. Board Discovery And Navigation

Feature set:

- Categorized discussion areas by campus, academics, recreation, culture,
  society, games, sports, chat/life, computing, development, OS, and technical
  topics.
- "All boards" list.
- "New boards" list.
- Board search by name/keyword/description.
- Board directories and nested groups.
- Per-board online counts and total article counts.
- Alphabetical/category/online sorting modes.
- ZAP/subscription-like ability to hide or unhide boards from unread workflows.
- No-ZAP system boards that remain visible.

Notable workflow:

- The user can enter all boards, favorite boards, category boards, unread-only
  boards, or newly opened boards from the top menu.
- Board lists support fast keyboard navigation, page navigation, numeric jump,
  search, direct board select, toggling all/new view, preview/full-screen view,
  and quick messaging/mail while browsing.

Modern lesson:

- Make "where should I read today?" a first-class workflow.
- Let users maintain different board collections, not just one flat favorite
  list.

### 2. Personal Board Collections

Feature set:

- Favorite board list.
- Nested favorite-board folders.
- Add/remove board from favorites.
- Create/rename/delete/reorder favorite directories.
- Move favorite entries up/down.
- Restore previous read markers.
- Sync favorite folders, friends, and unread markers.
- Default favorite boards for new users via site config.

This is stronger than a simple subscribe button. It lets a user build a personal
navigation tree over a large site.

Modern lesson:

- For high-volume communities, let users curate their own map of the site.
- Treat favorites as a personal workspace, not a secondary bookmark list.

### 3. Reading Workflows

Feature set:

- Sequential reading.
- Unread-first reading.
- Same-thread reading.
- Same-author reading.
- First unread in thread.
- First/last article in a thread.
- Search by author.
- Search by title.
- Board search from inside reading.
- Preview/full-screen toggle.
- Mark article read.
- Clear read markers to current article or for whole board.
- ZModem download of articles and attachments.
- Author info from article.
- Send message to author.
- Add author as friend.
- Mail author.
- Cross-post/repost article.
- Forward article to email/mail.
- Import article into digest/archive.
- Import article into blog/personal corpus in later branches.

The core reading surface is action-dense. It lets a user move, search, reply,
moderate, message, archive, and download without leaving context.

Modern lesson:

- Reading screens should expose context actions on the item, author, thread, and
  board.
- Thread traversal and unread state matter as much as chronological order.

### 4. Posting And Article Composition

Feature set:

- New post.
- Reply.
- Reply with quote.
- Mail original author from article.
- Change title.
- Edit article.
- Delete article.
- Cross-post or repost.
- Attach files to articles.
- Up to site-defined attachment count and size.
- TeX article flag.
- Mail-back replies.
- Anonymous posting on supported boards.
- Template-based posting in later configuration.
- Bad-word/filter checks.
- Censored-board workflow where flagged posts enter a review board.
- Failed-post preservation.
- ANSI editor with color controls, block operations, import/export, search, and
  preview.

Modern lesson:

- Powerful keyboard composition and recovery workflows mattered because posting
  was high-effort and text-heavy.
- Moderation hooks were built into composition, not bolted on afterward.

### 5. Threads, Hot Topics, And Public Rankings

Feature set:

- Explicit thread ID/reply ID structure.
- Same-topic reading independent of title.
- Reply counts in later structures.
- Top ten / hot topic calculation.
- Board post statistics.
- Blessing board ranking.
- Public counters: logins, total stay time, web guests, max online, top topics.
- MySQL-backed post logs/top logs as optional scaling/statistics path.
- BBSLists-style system boards for generated rankings and statistics.

Modern lesson:

- Community attention needs native surfaces: top topics, latest replies, most
  active boards, and ranked public rituals.

### 6. Digest, Archives, And Announcements

Feature set:

- Site-wide announcement/digest area under `0Announce`.
- Board-linked digest paths.
- Per-board digest index.
- Top/pinned article index.
- Marked articles.
- Imported articles.
- Moderator flow to save articles into digest.
- Create submenus/directories in archive.
- Copy/cut/paste/move/delete archive items.
- Rename archive files.
- Edit archive content/title.
- Search archive menus.
- Mail archive article back to Internet email.
- ZModem download.
- Path/date display for moderators.

This is one of KBS's most important product ideas: the forum is ephemeral, but
good content is curated into a durable knowledge base.

Modern lesson:

- A mature forum should have an explicit "canon" layer: digest, wiki, archive,
  pinned guide, curated FAQ, or reading list.

### 7. Private Mail

Feature set:

- Inbox, sent box, deleted/trash box.
- Custom user-defined mailboxes.
- Read new mail.
- Read all mail.
- Reply.
- Forward.
- Delete and range-delete.
- Move mail.
- Mark/keep mail.
- Cross-post mail to board.
- Same-thread and same-author mail reading.
- Save sent mail option.
- Force-delete option.
- Auto-clear trash option.
- Disable mail notice option.
- Mailbox shortcut option.
- Mail storage quota and used-space display.
- Group send.
- Send to friend list.
- User-defined mailing lists.
- Import friends into mail groups.
- Sysop mail-all.
- Optional outbound Internet email.
- Optional POP3/POP3S daemon for retrieving BBS mail externally.

Modern lesson:

- Private mail can reuse article/thread mechanics while still having its own
  foldering and quota model.

### 8. Real-Time Messages And Notifications

Feature set:

- Send a short message to an online user.
- Receive, view, and reply to messages.
- Show all pending messages.
- Reply to last message.
- Restrict messages to friends or all users via pager settings.
- Friend-message and all-message toggles.
- Sound/message/mail notices.
- Web message API.
- Optional SMS support.
- Later SQLite-backed "new message" system with:
  - Conversation list.
  - Per-user message history.
  - Reply and forward.
  - Attachments.
  - Read state.
  - Capacity and size display.
  - New-message count.

Modern lesson:

- A forum benefits from both long-form posts and lightweight conversational
  backchannels.

### 9. Mentions, Replies, Likes, And Refer Feeds

Later code adds a refer/notification layer:

- Notify users when they are mentioned with `@`.
- Notify users of replies.
- Notify users of Likes where enabled.
- Separate refer, reply, and like counts.
- Read all, delete, range-delete, mark read, truncate.
- Refer notifications can target board members or club members in supported
  builds.

Modern lesson:

- Notification feeds should be distinct from private mail. KBS evolved toward
  the same separation now common in modern apps: inbox, mentions, replies, and
  reactions.

### 10. Friends, Ignore Lists, Fans, And Social Graph

Feature set:

- Add/remove friends.
- Friend notes/descriptions.
- Show friends.
- Show online friends.
- Wait for friend login notification.
- Add author as friend from article.
- Add/remove friend from online-user lists.
- Friend list can feed mail groups.
- Ignore/badlist support.
- Web-exposed friends, ignores, online friends, and fans in later branches.
- Friend wall command in command registry.

Modern lesson:

- Friendship was not just social proof; it powered notification routing,
  messaging permissions, mail groups, and daily navigation.

### 11. Chat And Talk

Feature set:

- One-to-one talk/pager request.
- Pager modes.
- Chat hall / chat rooms.
- Multiple chat daemons or rooms.
- User-chosen chat ID.
- Create/join/list rooms.
- Invite users.
- Private message inside chat by chat ID.
- Change chat nickname.
- List room users.
- List online friends from chat.
- Ignore/listen controls.
- Custom emote aliases.
- MUD-like actions.
- Knock on a room.
- Mail-check command inside chat.
- Record chat logs.
- Chat room topic.
- Room flags: locked, secret, no-action.
- Room operator commands: grant op, kick by chat ID or real ID, transfer op,
  rename room, broadcast.

Modern lesson:

- Real-time gathering spaces create community presence beyond forum threads.
- Room-level moderation and identity controls matter even in small communities.

### 12. Registration, Identity, And Account Lifecycle

Feature set:

- Self-registration with ID validation.
- Registration form.
- Account approval/rejection by account managers.
- Registration system boards: `Registry`, `reject_registry`, `newcomers`.
- Email verification / activation in later code.
- Invite support in later code.
- Password setting/changing.
- Password recovery check by ID, real name, email in PHP layer.
- User profile editing.
- Admin user-data editing.
- User title/rank editing.
- Permission editing.
- Query users.
- Show real registration info to privileged staff.
- Account deletion.
- Confirm deletion of account directories.
- Self-suicide/offline account action.
- Give-up-net/self-restriction flow.
- Transfer ID.
- Protect ID in later code.
- IP login control/ACL per user.

Modern lesson:

- College BBS identity was semi-official. Registration was a community trust
  workflow, not just a sign-up form.

### 13. User Preferences And Personal Tools

Feature set:

- User-defined parameters.
- Secondary/telnet-specific parameters.
- Edit personal files.
- Edit plan/profile.
- Edit signatures, up to configured maximum.
- Recalculate signature count.
- Custom function keys.
- Lock screen.
- Alarm/clock/reminder system with multiple reminder types.
- Hide/cloak mode for privileged users.
- GB/BIG5 conversion.
- Clear unread markers.
- Custom login IP ACL.
- Badlist/blacklist.
- Diary/calendar service.
- Calculator, dictionary, and other tools.

Modern lesson:

- The BBS was an everyday environment. Small personal utilities helped users
  stay inside it.

### 14. Moderation And Board Management

Feature set:

- Create board.
- Edit board description/settings.
- Delete board.
- Assign/remove moderators.
- Query moderators.
- Board read/post permission restrictions.
- Board title/rank visibility restriction.
- Read-only boards.
- No-reply boards.
- No-ZAP boards.
- Email-post boards.
- Attachment-enabled boards.
- Anonymous boards.
- Relay-enabled boards.
- Board directories and parent relationships.
- Board description keywords for search.
- Board recycle bin for moderator-deleted posts.
- Board junk bin for user-deleted posts.
- Restore deleted/junked posts.
- Range delete.
- Deny/undeny posting per board.
- Deny reasons and custom reasons.
- Moderation logs in system boards.
- Edit board recycle-bin access list.
- Clear board junk.
- Edit system files.
- Search system traces/logs.
- Stop login.
- Kick user.
- Broadcast to all users.
- System password.
- Search IP location.

Modern lesson:

- Moderation was not a separate dashboard. It lived directly in board and article
  reading flows, with strong system-board audit trails.

### 15. Club And Board-Member Systems

KBS supports two membership-like concepts.

Club mode:

- Restrict board read access by club read rights.
- Restrict board write access by club write rights.
- Hide club boards from non-members.
- Let board managers or senior admins manage club members.
- Query club rights.

Board-member mode, added later:

- Users can apply to become resident members of a board.
- Members can leave.
- Moderators/managers can approve, reject, remove, or blacklist.
- Board-level member config can define requirements:
  - Login count.
  - Post count.
  - Score.
  - Level.
  - Board posts.
  - Board original posts.
  - Board marks.
  - Board digests.
  - Max members.
  - Approval mode.
- Member statuses: candidate, normal, manager, blacklisted.
- Member managers have granular permissions: delete, deny, sign/mark, announce,
  refer/member article handling, junk access, vote management, recommend,
  range delete, note/template management, thread operations, modify article,
  club management, Like-related recording.
- Board-member titles can be created, modified, sorted, assigned, and removed.
- Board-member reading mode aggregates latest articles from multiple resident
  boards.

Modern lesson:

- "Member of a board" is a useful middle layer between ordinary subscriber and
  moderator.
- It supports identity, status, responsibility, and special workflows inside a
  subcommunity.

### 16. Blog And Personal Corpus

The KBS.Blog / personal corpus module extends the BBS into personal publishing.

Feature set:

- Per-user blog/personal corpus.
- Custom domain support.
- Public, friends-only, private, favorites, and deleted sections.
- Category support.
- Directory-mode browsing.
- Section-level article and directory limits.
- Templates.
- Custom blog name, description, theme, logo, background, and homepage mode.
- Friend links.
- Trackback ping.
- Monthly archives and downloads.
- Site-wide latest posts and latest comments.
- RSS output.
- Blog and user search.
- Custom XML/XSL templates.
- Group blogs with member management.
- Blog blacklists, both per-blog and site-wide.
- Category recommendation.
- Blog/user/category statistics.
- Automatic refer extraction from BBS-like blog posts.
- User blocking.
- OPML channel groups.
- Blog application and approval.
- Single-entry blocking.
- Filters.
- Personal file space with optional permissions and anti-hotlink/referrer checks.
- Telnet admin: open, modify, close blogs; set quotas.
- Telnet user flow: publish/modify/delete posts or comments; import board posts;
  import BBS friends; search blog users.

Modern lesson:

- The BBS acted as the social graph and identity substrate for personal
  publishing.
- Good forum posts could flow into durable personal spaces.

### 17. Web, WAP, And External Interfaces

Feature set:

- PHP extension exposes most core BBS operations to web code:
  - Boards, board search, favorite boards, board permissions, board online
    counts.
  - Articles, thread trees, title/author search, attachments, parsed ANSI output.
  - Posting, updating, deleting, forwarding, cross-posting, recommending,
    read-marker sync.
  - Mail, mailboxes, refer notifications, new messages.
  - Friends, ignores, fans, online friends.
  - Sessions, online users, guest sessions.
  - Registration, invite, activation.
  - Admin board/user operations.
  - Board-member APIs.
  - Votes/templates.
  - Likes in later code.
- WAP pages under `bbs2www/wap`.
- POP3/POP3S daemon for BBS mail.
- mail2bbs/qmail2bbs for inbound mail-to-board/mail flows.
- innbbsd for news/NNTP relay.
- SSH wrapper around the telnet BBS.

Modern lesson:

- The BBS core was intended to be multi-surface: telnet, SSH, web, WAP, mail,
  POP3, and news relay.

### 18. Services, Games, And Campus Utilities

Feature set:

- Dynamic module runner for telnet services.
- Friend test.
- Quiz.
- BBS network "travel"/site hopping.
- PIP / "star fighting chicken" game.
- Sokoban.
- Dictionary.
- Diary/calendar.
- Calculator.
- Killer/Mafia game.
- ANSI/Belle editor.
- Typing game.
- Tetris.
- Minesweeper.
- Snake.
- Ball.
- Weather display.
- Text WWW browser entry.
- Optional personal DNS service.

Modern lesson:

- Community stickiness came partly from non-forum rituals and utilities. The
  BBS was a place to hang out, not just a content database.

## System Boards As Product Features

The sysop docs recommend a set of special boards that make operations visible
and legible:

- `bbsnet`: site-hopping/network service logs.
- `BBSLists`: statistics lists.
- `Blessing`: blessing/ranking board.
- `denypost`: board-level posting bans.
- `Filter`: auto-filtered/censored posts for review.
- `GiveupNotice`: give-up-net notices.
- `Goodbye`: account suicide records.
- `newcomers`: automatic new-user posts.
- `notepad`: public/system notepad.
- `Recommend`: recommended articles and homepage recommendations.
- `Registry`: approved registrations.
- `reject_registry`: rejected registrations.
- `syssecurity`: security/admin changes such as moderator appointments,
  permission changes, and board setting changes.
- `undenypost`: unban records.
- `vote`: votes and results.
- `sysmail`: SYSOP mail exposed as a restricted board.

Modern lesson:

- Operations can be community-native. Instead of hiding everything in admin logs,
  KBS turned many workflows into boards with permissions.

## Permission Model

KBS uses a broad permission bitset. The exact labels vary by site config, but
the important product concepts are:

- Basic access.
- Chat access.
- Pager/message access.
- Post access.
- Registration-approved status.
- Cloak/invisibility.
- See cloaked users.
- Long-term/protected account.
- Edit system files.
- Moderator.
- Account manager.
- Chat-room manager/op.
- Entertainment/service ban.
- System maintenance/sysop.
- Read/post restriction marker.
- Digest/announcement manager.
- Board/category manager.
- Activity board manager.
- No-ZAP exemption.
- Admin/super-admin.
- Honor/special accounts.
- Arbitration/jury-like roles.
- Account suicide/deletion states.
- Collective account.
- System board access.
- Mail ban.

The product idea is not "many bits." It is a layered trust model: ordinary user,
registered/approved user, board-level operator, domain-specific operator,
system-level operator, and punished/restricted user.

## Data Structures Worth Understanding

These are not recommended as implementation designs, but they reveal the product
model.

### `fileheader`

Represents both articles and mail entries. Product meaning:

- Stable item identity.
- Explicit thread and reply graph.
- Original/cross-post lineage.
- Author/peer.
- Attachments.
- Presentation title.
- Moderation and curation flags.

### `boardheader`

Represents a board or board directory. Product meaning:

- Public place identity.
- Moderation ownership.
- Category/search metadata.
- Access rules.
- Club/member/reply/relay/anonymous/email/attachment behavior.
- Digest/archive linkage.
- Hierarchy.

### `userec` And `userdata`

Split account state into:

- Login/security/stats/permission core.
- Extended profile/contact fields.

Product meaning:

- Public reputation and private registration data are both part of identity.

### `user_info`

Represents an online session. Product meaning:

- The system cares what a user is doing right now: reading, chatting, posting,
  in mail, idle, cloaked, in which board, with which chat ID.

### Favorite Board Structures

Nested folders over board IDs. Product meaning:

- Personal site navigation is user-authored.

### Mailbox And Mail Group Structures

Mailboxes and mailing lists are user-owned. Product meaning:

- Private communication has organization and group workflows.

### `refer`

Mention/reply/like notification item. Product meaning:

- Notification objects are separate from mail and articles.

### Board Member Structures

Membership config, member row, member status, member title. Product meaning:

- Boards can have internal membership, roles, and progression separate from
  global site permissions.

### Blog / Personal Corpus Structures

Users, nodes, comments, logs. Product meaning:

- Personal publishing is grafted onto BBS identity and social graph.

## Feature Ideas Worth Reusing Today

### High-Value Ideas

- Personal board folders.
- Unread-first workflows across favorite boards.
- Explicit same-thread and same-author reading modes.
- Board-level digest/archive as a first-class curation layer.
- System boards for transparent operational events.
- Board membership as a status layer below moderator.
- Online presence integrated into board and user lists.
- Separate durable mail, short messages, and notification feeds.
- Moderator actions available in reading context.
- Board-specific permissions and flags that shape community norms.
- Import good public posts into personal/blog/digest spaces.
- Friend login notifications and online-friend views.
- Rich text-mode keyboard workflows for high-volume users.

### Ideas To Reinterpret Carefully

- Real-name/contact fields: useful for campus trust, but modern privacy
  expectations are very different.
- Public login/idle/host visibility: creates presence, but needs consent and
  privacy controls.
- Broad admin permission bits: powerful, but should map to auditable role-based
  access today.
- Mail-all and system broadcast: useful, but needs rate limits and targeting.
- Board-level censorship queues: useful if transparent and reviewable.
- Self-suicide/give-up-net flows: meaningful cultural artifacts, but should be
  redesigned with modern account-deactivation and wellbeing practices.

## Suggested Modern Feature Taxonomy

If designing a modern product inspired by KBS, group features this way:

1. Places: boards, categories, directories, clubs, board membership.
2. Reading: unread queues, thread views, author views, hot topics, search.
3. Writing: posts, replies, attachments, templates, drafts, cross-posting.
4. Curation: digest, recommended articles, pinned/top posts, archive, wiki.
5. Identity: profile, titles, reputation, privacy, verification.
6. Social graph: friends, follows/fans, ignores, online friends.
7. Private communication: mail, messages, mentions, reply notifications.
8. Presence: online users, current board, chat rooms, activity modes.
9. Moderation: per-board tools, member managers, bans, audit boards, review
   queues.
10. Operations: system boards, logs, statistics, registration queues.
11. Personal spaces: blogs, diaries, collections, imports from public boards.
12. Community rituals: rankings, games, blessings, weather/tools, events.

## Source Map

Most relevant files and directories:

- `doc/kbsIntro.txt`: core explanation of board/article/user/mail/shared-memory
  structures.
- `bbshome/etc/menu.ini`: telnet menus and user-visible feature map.
- `bbshome/help/`: keyboard-level workflows for boards, reading, mail, chat,
  friends, votes, announcements, board-member mode.
- `doc/README.SYSOP`: board permissions, system boards, relay, sysop workflow.
- `doc/README.blog` and `doc/INSTALL.blog`: KBS.Blog / personal corpus features.
- `doc/README.utils`: hot topics, statistics, and optional games/services.
- `src/comm_lists.c`: command registry connecting menu names to functions.
- `src/bbs.c`, `src/read.h`, `src/newread.c`: article reading/posting flows.
- `src/boards_t.c` and `libBBS/boards.c`: board lists, favorites, permissions,
  clubs.
- `src/mail.c`, `libBBS/bbs_sendmail.c`, `libBBS/stuff.c`: mail, mailboxes,
  mail groups.
- `src/sendmsg.c`, `src/webmsg.h`, `src/newmsg.c`, `libBBS/libnewmsg.c`:
  short messages and later conversation messages.
- `src/chat.c`, `daemon/station.c`: chat rooms.
- `src/talk.c`, `src/list.c`: online users, friends, pager/talk.
- `src/announce.c`, `libBBS/libann.c`: digest/announcement archive.
- `src/maintain.c`, `src/delete.c`, `src/xyz.c`, `src/bm.c`: administration and
  moderation.
- `src/member.c`, `libBBS/member_cache.c`, `php/phpbbs_member.c`: board-member
  system.
- `src/personal_corp.c`, `libBBS/libpc.c`: blog/personal corpus.
- `php/`: web API exposure of BBS features.
- `service/`: games and utility modules.
- `daemon/`, `mail2bbs/`, `innbbsd/`, `bbs2www/`, `sshbbsd/`: external surfaces
  and background services.
