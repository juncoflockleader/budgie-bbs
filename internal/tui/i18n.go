package tui

import "strings"

type localeCode string

const (
	localeEN   localeCode = "en"
	localeZHCN localeCode = "zh-CN"
	localeZHTW localeCode = "zh-TW"
)

type msgKey string

const (
	msgAppName msgKey = "app.name"
	msgTagline msgKey = "app.tagline"

	msgStatusError              msgKey = "status.error"
	msgStatusPresence           msgKey = "status.presence"
	msgStatusDisconnected       msgKey = "status.disconnected"
	msgStatusProfileSaved       msgKey = "status.profileSaved"
	msgStatusProfileLoading     msgKey = "status.profileLoading"
	msgStatusPostSubmitted      msgKey = "status.postSubmitted"
	msgStatusChatSent           msgKey = "status.chatSent"
	msgStatusThreadSubmitted    msgKey = "status.threadSubmitted"
	msgStatusNoPostSelected     msgKey = "status.noPostSelected"
	msgStatusThreadNotLoaded    msgKey = "status.threadNotLoaded"
	msgStatusThreadNotInBoard   msgKey = "status.threadNotInBoard"
	msgStatusNoBoardSelected    msgKey = "status.noBoardSelected"
	msgStatusRefreshing         msgKey = "status.refreshing"
	msgStatusFirstPage          msgKey = "status.alreadyFirstPage"
	msgStatusLastPage           msgKey = "status.alreadyLastPage"
	msgStatusLoadingThreads     msgKey = "status.loadingThreads"
	msgStatusNoPostToReact      msgKey = "status.noPostToReact"
	msgStatusNoPostToMark       msgKey = "status.noPostToMark"
	msgStatusPollNoActive       msgKey = "status.pollNoActive"
	msgStatusPollClosed         msgKey = "status.pollClosed"
	msgStatusPollVotedOrClosed  msgKey = "status.pollVotedOrClosed"
	msgStatusPollNeedTrust      msgKey = "status.pollNeedTrust"
	msgStatusPollChecking       msgKey = "status.pollChecking"
	msgStatusFocusBodyFirst     msgKey = "status.focusBodyFirst"
	msgStatusTitleRequired      msgKey = "status.titleRequired"
	msgStatusBodyRequired       msgKey = "status.bodyRequired"
	msgStatusTitleTooLong       msgKey = "status.titleTooLong"
	msgStatusSendingChat        msgKey = "status.sendingChat"
	msgStatusThreadLocked       msgKey = "status.threadLocked"
	msgStatusThreadListFirst    msgKey = "status.threadListFirst"
	msgStatusThreadListLast     msgKey = "status.threadListLast"
	msgStatusChatNotFound       msgKey = "status.chatNotFound"
	msgStatusFailedRead         msgKey = "status.failedRead"
	msgStatusFailedReadAll      msgKey = "status.failedReadAll"
	msgStatusFailedDelete       msgKey = "status.failedDelete"
	msgStatusDeleted            msgKey = "status.deleted"
	msgStatusNotificationMarked msgKey = "status.notificationMarked"
	msgStatusLoadingThread      msgKey = "status.loadingThread"
	msgStatusFailedClearRead    msgKey = "status.failedClearRead"
	msgStatusClearRead          msgKey = "status.clearRead"
	msgStatusFailedClearAll     msgKey = "status.failedClearAll"
	msgStatusClearedAll         msgKey = "status.clearedAll"
	msgStatusThreadAction       msgKey = "status.threadAction"
	msgStatusLocked             msgKey = "status.locked"
	msgStatusUnlocked           msgKey = "status.unlocked"

	msgLabelThreadTitle    msgKey = "label.threadTitle"
	msgLabelAuthor         msgKey = "label.author"
	msgLabelTime           msgKey = "label.time"
	msgLabelTitle          msgKey = "label.title"
	msgLabelUser           msgKey = "label.user"
	msgLabelDisplay        msgKey = "label.display"
	msgLabelRole           msgKey = "label.role"
	msgLabelTrust          msgKey = "label.trust"
	msgLabelPosts          msgKey = "label.posts"
	msgLabelVisible        msgKey = "label.visibleSessions"
	msgLabelMeta           msgKey = "label.meta"
	msgLabelProfile        msgKey = "label.profile"
	msgLabelNoResults      msgKey = "label.noResults"
	msgLabelSearch         msgKey = "label.search"
	msgLabelLoading        msgKey = "label.loading"
	msgLabelNoMessages     msgKey = "label.noMessages"
	msgLabelNoProfileField msgKey = "label.noProfileField"

	msgHelpMainMenu        msgKey = "help.mainMenu"
	msgHelpBoardList       msgKey = "help.boardList"
	msgHelpThreadList      msgKey = "help.threadList"
	msgHelpNotifications   msgKey = "help.notifications"
	msgHelpProfile         msgKey = "help.profile"
	msgHelpProfileEdit     msgKey = "help.profileEdit"
	msgHelpOnline          msgKey = "help.online"
	msgHelpThreadReader    msgKey = "help.threadReader"
	msgHelpThreadReaderMod msgKey = "help.threadReaderMod"
	msgHelpPoll            msgKey = "help.poll"
	msgHelpPollClosed      msgKey = "help.pollClosed"
	msgHelpPollVoted       msgKey = "help.pollVoted"
	msgHelpNewThread       msgKey = "help.newThread"
	msgHelpCompose         msgKey = "help.compose"
	msgHelpChat            msgKey = "help.chat"
	msgHelpSearch          msgKey = "help.search"

	msgHeaderUnread  msgKey = "header.unread"
	msgHeaderStatus  msgKey = "header.statusPrefix"
	msgHeaderFormat  msgKey = "header.format"
	msgHeaderAppName msgKey = "header.appName"

	msgPlaceholderThreadTitle  msgKey = "placeholder.threadTitle"
	msgPlaceholderComposeBody  msgKey = "placeholder.composeBody"
	msgPlaceholderSearchPrompt msgKey = "placeholder.searchPrompt"
	msgPlaceholderChatMessage  msgKey = "placeholder.chatMessage"
	msgPlaceholderSearchInput  msgKey = "placeholder.searchInput"

	msgComposeHelpBase        msgKey = "compose.helpBase"
	msgComposeHelpWithPoll    msgKey = "compose.helpWithPoll"
	msgComposeHelpPollCheck   msgKey = "compose.helpPollCheck"
	msgComposeHelpPollTrust   msgKey = "compose.helpPollTrust"
	msgComposeHelpPollChecked msgKey = "compose.helpPollChecked"

	msgTitleMainMenu      msgKey = "title.mainMenu"
	msgTitleBoards        msgKey = "title.boards"
	msgTitleNotifications msgKey = "title.notifications"
	msgTitleProfile       msgKey = "title.profile"
	msgTitleProfileEdit   msgKey = "title.profileEdit"
	msgTitleOnlineUsers   msgKey = "title.onlineUsers"
	msgTitlePoll          msgKey = "title.poll"
	msgTitleNewThread     msgKey = "title.newThread"
	msgTitleNewReply      msgKey = "title.newReply"
	msgTitleLiveChat      msgKey = "title.liveChat"
	msgTitleSearch        msgKey = "title.search"
	msgTitleBoardFallback msgKey = "title.boardFallback"
	msgTitleNoPoll        msgKey = "title.noPoll"
	msgTitleNoMessages    msgKey = "title.noMessages"

	msgPageBoard      msgKey = "menu.boardTitle"
	msgPageSearch     msgKey = "menu.search"
	msgPageProfile    msgKey = "menu.profile"
	msgPageOnline     msgKey = "menu.online"
	msgPageOnlineHint msgKey = "menu.onlineHint"
	msgPageExit       msgKey = "menu.exit"

	msgPageBoardsDesc        msgKey = "menu.boardsDesc"
	msgPageChatDesc          msgKey = "menu.chatDesc"
	msgPageNotificationsDesc msgKey = "menu.notificationsDesc"
	msgPageSearchDesc        msgKey = "menu.searchDesc"
	msgPageProfileDesc       msgKey = "menu.profileDesc"
	msgPageOnlineDesc        msgKey = "menu.onlineDesc"
	msgPageExitDesc          msgKey = "menu.exitDesc"

	msgMenuThreadListEmpty  msgKey = "menu.threadListEmpty"
	msgMenuNotifications    msgKey = "menu.notifications"
	msgProfileSettingsTitle msgKey = "title.profileSettings"

	msgPostPrefixSeq        msgKey = "post.seq"
	msgPostOp               msgKey = "post.op"
	msgPostPrefixUpdated    msgKey = "post.updated"
	msgPostPrefixVersion    msgKey = "post.version"
	msgPostPrefixEdited     msgKey = "post.edited"
	msgPostPrefixReply      msgKey = "post.reply"
	msgPostPrefixType       msgKey = "post.type"
	msgPostReaction         msgKey = "post.reaction"
	msgPostPollTag          msgKey = "post.poll"
	msgPostAttachments      msgKey = "post.attachments"
	msgPostMarked           msgKey = "post.marked"
	msgPostRecommended      msgKey = "post.recommended"
	msgPostNoReply          msgKey = "post.noReply"
	msgPostTex              msgKey = "post.tex"
	msgPostMailBack         msgKey = "post.mailBack"
	msgPostRedacted         msgKey = "post.redacted"
	msgPostSource           msgKey = "post.source"
	msgPostMetaLinePrefix   msgKey = "post.metaLinePrefix"
	msgPostMetaContinuation msgKey = "post.metaContinuation"

	msgNotifMention msgKey = "notif.mention"
	msgNotifReply   msgKey = "notif.reply"
	msgNotifWatched msgKey = "notif.watched"

	msgListByPosts        msgKey = "list.byPosts"
	msgListNoPoll         msgKey = "list.noPoll"
	msgListPollClosed     msgKey = "list.pollClosed"
	msgListNoPollLoaded   msgKey = "list.noPollLoaded"
	msgListReplyVoteVotes msgKey = "list.voteCount"
	msgListOpen           msgKey = "list.open"
	msgListClosesAt       msgKey = "list.closesAt"
	msgListCloses         msgKey = "list.closes"

	msgProfileDisplayName   msgKey = "profile.fieldDisplayName"
	msgProfileTitle         msgKey = "profile.fieldTitle"
	msgProfileBio           msgKey = "profile.fieldBio"
	msgProfileAvatar        msgKey = "profile.fieldAvatar"
	msgProfileSignature     msgKey = "profile.fieldSignature"
	msgProfilePlan          msgKey = "profile.fieldPlan"
	msgProfileHomepage      msgKey = "profile.fieldHomepage"
	msgProfileDisplayDesc   msgKey = "profile.descDisplay"
	msgProfileTitleDesc     msgKey = "profile.descTitle"
	msgProfileBioDesc       msgKey = "profile.descBio"
	msgProfileAvatarDesc    msgKey = "profile.descAvatar"
	msgProfileSignatureDesc msgKey = "profile.descSignature"
	msgProfilePlanDesc      msgKey = "profile.descPlan"
	msgProfileHomepageDesc  msgKey = "profile.descHomepage"

	msgProfileEditTitlePrefix msgKey = "profile.editPrefix"

	msgCommonBoard             msgKey = "common.board"
	msgCommonThread            msgKey = "common.thread"
	msgCommonLocked            msgKey = "common.locked"
	msgCommonUnlocked          msgKey = "common.unlocked"
	msgCommonOnline            msgKey = "common.online"
	msgCommonNoVisibleOnline   msgKey = "common.noVisibleOnline"
	msgCommonNoProfileSelected msgKey = "common.noProfileSelected"
	msgCommonThreadInBoard     msgKey = "common.threadInBoard"
	msgCommonPostFormat        msgKey = "common.postFormat"
	msgCommonStatusIn          msgKey = "common.statusIn"
	msgCommonThreadPrefix      msgKey = "common.threadPrefix"
	msgCommonNoSearch          msgKey = "common.noSearch"
	msgCommonVoteSuffix        msgKey = "common.voteSuffix"
	msgCommonVotePluralSuffix  msgKey = "common.votePluralSuffix"
	msgCommonUntitled          msgKey = "common.untitled"
	msgCommonUnknown           msgKey = "common.unknown"

	msgComposeTitlePrefix    msgKey = "compose.titleLabel"
	msgSearchResults         msgKey = "search.results"
	msgSearchNoResults       msgKey = "search.noResults"
	msgSearchNoResultText    msgKey = "search.noQuery"
	msgSearchAuthorsInThread msgKey = "search.authorsInThread"

	msgOnlineNoUsers          msgKey = "online.noUsers"
	msgOnlineRefreshHint      msgKey = "online.refreshHint"
	msgOnlineNoUsersHint      msgKey = "online.noUsersHint"
	msgOnlineModeIdleTemplate msgKey = "online.idle"
	msgOnlineModeOnline       msgKey = "online.online"

	msgNotificationsNoRead msgKey = "notifications.none"

	msgStatusLocaleSwitch msgKey = "status.localeSwitch"
)

var tuiMessages = map[localeCode]map[msgKey]string{
	localeEN: {
		msgAppName:                  "BudgieBBS",
		msgTagline:                  "A server-hosted campus BBS over SSH",
		msgStatusError:              "error: {message}",
		msgStatusPresence:           "presence: {message}",
		msgStatusDisconnected:       "disconnected",
		msgStatusProfileSaved:       "profile saved",
		msgStatusProfileLoading:     "profile loading",
		msgStatusPostSubmitted:      "post submitted",
		msgStatusChatSent:           "chat sent",
		msgStatusThreadSubmitted:    "thread submitted",
		msgStatusNoPostSelected:     "no post selected",
		msgStatusThreadNotLoaded:    "thread list not loaded",
		msgStatusThreadNotInBoard:   "current thread not in board list",
		msgStatusNoBoardSelected:    "no board selected",
		msgStatusRefreshing:         "refreshing threads",
		msgStatusFirstPage:          "already at first page",
		msgStatusLastPage:           "already at last page",
		msgStatusLoadingThreads:     "loading threads {from}-{to}",
		msgStatusNoPostToReact:      "no post to react",
		msgStatusNoPostToMark:       "no post selected",
		msgStatusPollNoActive:       "selected post has no active poll",
		msgStatusPollClosed:         "poll closed",
		msgStatusPollVotedOrClosed:  "already voted · {status}",
		msgStatusPollNeedTrust:      "polls require trust level 2+",
		msgStatusPollChecking:       "checking poll permission…",
		msgStatusFocusBodyFirst:     "focus body first",
		msgStatusTitleRequired:      "title is required",
		msgStatusBodyRequired:       "body is required",
		msgStatusTitleTooLong:       "title must be 80 characters or less",
		msgStatusSendingChat:        "sending chat…",
		msgStatusThreadLocked:       "thread {action} by {by}",
		msgStatusThreadListFirst:    "already at first thread",
		msgStatusThreadListLast:     "already at last thread",
		msgStatusChatNotFound:       "thread not found",
		msgStatusFailedRead:         "failed to mark notification read",
		msgStatusFailedReadAll:      "failed to mark notifications read",
		msgStatusFailedDelete:       "failed to delete notification",
		msgStatusDeleted:            "notification deleted",
		msgStatusNotificationMarked: "notification marked as read",
		msgStatusFailedClearRead:    "failed to clear read notifications",
		msgStatusClearRead:          "read notifications cleared",
		msgStatusFailedClearAll:     "failed to clear notifications",
		msgStatusClearedAll:         "notifications cleared",
		msgStatusThreadAction:       "thread {action} by {by}",
		msgStatusLoadingThread:      "Loading thread...",
		msgStatusLocked:             "locked",
		msgStatusUnlocked:           "unlocked",
		msgLabelThreadTitle:         "Title:",
		msgLabelAuthor:              "Author:",
		msgLabelTime:                "Time:",
		msgLabelTitle:               "Title:",
		msgLabelUser:                "User:",
		msgLabelDisplay:             "Display:",
		msgLabelRole:                "Role:",
		msgLabelTrust:               "Trust:",
		msgLabelPosts:               "Posts:",
		msgLabelVisible:             "Visible sessions:",
		msgLabelMeta:                "Meta: ",
		msgLabelProfile:             "Profile Settings",
		msgLabelLoading:             "Loading profile…",
		msgLabelNoProfileField:      "No profile field selected.",
		msgHelpMainMenu:             "enter/→=open  1-7=jump  p=profile  o=online  q=quit",
		msgHelpBoardList:            "enter/→=open board  c=chat  N=notifications  /=search  esc/←=menu  q=quit",
		msgHelpThreadList:           "enter/→=open thread  n=new thread  r=refresh  Ctrl+↑/↓=page  /=search  esc/←=back  q=quit",
		msgHelpNotifications:        "enter/→=mark read  a=mark all read  d=delete  x=clear read  c=clear all  esc/←=back",
		msgHelpProfile:              "enter/→=edit  r=refresh  esc/←=menu  q=quit",
		msgHelpProfileEdit:          "Ctrl+S=save  Esc=cancel",
		msgHelpOnline:               "r=refresh  enter/→=details  esc/←=menu  q=quit",
		msgHelpThreadReader:         "↑/↓=thread  Ctrl+↑/↓=line  Space/PgDn,b/PgUp=page  Home/End  n=reply  r=react  p=poll  esc/←=back",
		msgHelpThreadReaderMod:      "↑/↓=thread  Ctrl+↑/↓=line  Space/PgDn,b/PgUp=page  Home/End  n=reply  L=lock  r=react  p=poll  esc/←=back",
		msgHelpPoll:                 "1-9 vote · esc/←=back",
		msgHelpPollClosed:           "poll closed · esc/←=back",
		msgHelpPollVoted:            "already voted · 1-9 vote · esc/←=back",
		msgHelpNewThread:            "enter/tab=body  ctrl+s=body  esc=cancel",
		msgHelpCompose:              "",
		msgHelpChat:                 "enter=send  esc/←=back",
		msgHelpSearch:               "type query  enter=search  esc/←=back",
		msgHeaderUnread:             " ● {count} unread",
		msgHeaderStatus:             " | {status}",
		msgHeaderFormat:             "{app} | {user} | {title}",
		msgHeaderAppName:            "BudgieBBS",
		msgPlaceholderThreadTitle:   "Thread title…",
		msgPlaceholderComposeBody:   "Write your post… (Ctrl+S to submit, Esc to cancel)",
		msgPlaceholderSearchPrompt:  "Type your query and press Enter to search…",
		msgPlaceholderChatMessage:   "Say something…",
		msgPlaceholderSearchInput:   "Search: ",
		msgComposeHelpBase:          "Ctrl+S=submit  Esc=cancel",
		msgComposeHelpWithPoll:      "Ctrl+S=submit  Esc=cancel  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll",
		msgComposeHelpPollCheck:     "Ctrl+S=submit  Esc=cancel  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll (checking permission…)",
		msgComposeHelpPollTrust:     "Ctrl+S=submit  Esc=cancel  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll (trust level 2+ required)",
		msgComposeHelpPollChecked:   "Ctrl+S=submit  Esc=cancel  Ctrl+P=add poll template  Ctrl+E=1h poll  Ctrl+D=1d poll  Ctrl+W=1w poll",
		msgTitleMainMenu:            "Main Menu",
		msgTitleBoards:              "Boards",
		msgTitleNotifications:       "Notifications",
		msgTitleProfile:             "My Profile",
		msgTitleProfileEdit:         "Edit Profile",
		msgTitleOnlineUsers:         "Who is Online",
		msgTitlePoll:                "Poll",
		msgTitleNewThread:           "New Thread",
		msgTitleNewReply:            "New Reply",
		msgTitleLiveChat:            "Live Chat - #lobby",
		msgTitleSearch:              "Search",
		msgTitleBoardFallback:       "Board",
		msgTitleNoPoll:              "No poll loaded",
		msgTitleNoMessages:          "No messages yet. Say hello!",
		msgPageBoard:                "Boards",
		msgPageSearch:               "Search",
		msgPageProfile:              "Profile",
		msgPageOnline:               "Online",
		msgPageOnlineHint:           "online",
		msgPageExit:                 "Exit",
		msgPageBoardsDesc:           "Browse boards and threads",
		msgPageChatDesc:             "Join the lobby chat",
		msgPageNotificationsDesc:    "Read mentions, replies, watched threads",
		msgPageSearchDesc:           "Search posts",
		msgPageProfileDesc:          "Edit your public profile and signature",
		msgPageOnlineDesc:           "See who is online right now",
		msgPageExitDesc:             "Leave BudgieBBS",
		msgMenuThreadListEmpty:      "thread list empty",
		msgMenuNotifications:        "Notifications",
		msgProfileSettingsTitle:     "Profile Settings",
		msgPostPrefixSeq:            "Seq: #{seq}",
		msgPostOp:                   "OP",
		msgPostPrefixUpdated:        "Updated: #{seq}",
		msgPostPrefixVersion:        "Version: v{version}",
		msgPostPrefixEdited:         "Edited: {time}",
		msgPostPrefixReply:          "Reply: {reply}",
		msgPostPrefixType:           "Type: {type}",
		msgPostReaction:             "♥ {count}",
		msgPostPollTag:              "[poll]",
		msgPostAttachments:          "Attachments: {count}",
		msgPostMarked:               "Marked",
		msgPostRecommended:          "Recommended",
		msgPostNoReply:              "No reply",
		msgPostTex:                  "TeX",
		msgPostMailBack:             "Mail back",
		msgPostRedacted:             "Redacted",
		msgPostSource:               "Source: {source}",
		msgPostMetaLinePrefix:       "      Meta: ",
		msgPostMetaContinuation:     "            ",
		msgNotifMention:             "@ mention",
		msgNotifReply:               "↩ reply",
		msgNotifWatched:             "👁 watched",
		msgListByPosts:              "by {author} · {count} posts",
		msgListNoPoll:               "No poll loaded",
		msgListPollClosed:           "poll closed",
		msgListNoPollLoaded:         "No poll loaded",
		msgListReplyVoteVotes:       "{count} vote{s}",
		msgListOpen:                 "open",
		msgListClosesAt:             "closes {time}",
		msgListCloses:               "closed",
		msgProfileDisplayName:       "Display name",
		msgProfileTitle:             "Title",
		msgProfileBio:               "Bio",
		msgProfileAvatar:            "Avatar",
		msgProfileSignature:         "Signature",
		msgProfilePlan:              "Plan",
		msgProfileHomepage:          "Homepage",
		msgProfileDisplayDesc:       "Shown on your public profile",
		msgProfileTitleDesc:         "Short BBS title or rank",
		msgProfileBioDesc:           "Public profile introduction",
		msgProfileAvatarDesc:        "Emoji or short avatar text",
		msgProfileSignatureDesc:     "Default post signature",
		msgProfilePlanDesc:          "Classic BBS plan text",
		msgProfileHomepageDesc:      "Personal URL or homepage",
		msgProfileEditTitlePrefix:   "Edit {field}",
		msgCommonBoard:              "Board",
		msgCommonThread:             "Thread",
		msgCommonLocked:             "locked",
		msgCommonUnlocked:           "unlocked",
		msgCommonOnline:             "online",
		msgCommonNoVisibleOnline:    "No visible users online. Press r to refresh.",
		msgCommonNoProfileSelected:  "No profile field selected.",
		msgCommonThreadInBoard:      " in {thread}",
		msgCommonPostFormat:         "{label} in {thread}",
		msgCommonStatusIn:           "thread {id}",
		msgCommonThreadPrefix:       "{index} in {thread}",
		msgCommonNoSearch:           "No results found.",
		msgCommonVoteSuffix:         "",
		msgCommonVotePluralSuffix:   "s",
		msgCommonUntitled:           "(untitled)",
		msgCommonUnknown:            "(unknown)",
		msgComposeTitlePrefix:       "Title: ",
		msgSearchResults:            "{count} result{plural}",
		msgSearchNoResults:          "No results for \"{query}\".",
		msgSearchNoResultText:       "No query",
		msgSearchAuthorsInThread:    "{author} in {thread}",
		msgOnlineNoUsers:            "No visible users online. Press r to refresh.",
		msgOnlineRefreshHint:        "Refresh users",
		msgOnlineNoUsersHint:        "No visible users online. Press r to refresh.",
		msgOnlineModeIdleTemplate:   "idle {idle}",
		msgOnlineModeOnline:         "online",
		msgNotificationsNoRead:      "No notifications yet.",
		msgStatusLocaleSwitch:       "Language: English",
	},
	localeZHCN: {
		msgAppName:                 "BudgieBBS",
		msgStatusError:             "错误：{message}",
		msgStatusPresence:          "presence: {message}",
		msgStatusDisconnected:      "已断开",
		msgStatusProfileSaved:      "资料已保存",
		msgStatusProfileLoading:    "资料加载中",
		msgStatusPostSubmitted:     "帖子已发送",
		msgStatusChatSent:          "消息已发送",
		msgStatusThreadSubmitted:   "主题已发布",
		msgStatusNoPostSelected:    "未选中帖子",
		msgStatusThreadNotLoaded:   "帖子列表未加载",
		msgStatusThreadNotInBoard:  "当前主题不在当前版块列表内",
		msgStatusNoBoardSelected:   "未选中版面",
		msgStatusRefreshing:        "正在刷新主题",
		msgStatusFirstPage:         "已是第一页",
		msgStatusLastPage:          "已是最后一页",
		msgStatusLoadingThreads:    "正在加载 {from}-{to} 条主题",
		msgStatusNoPostToReact:     "没有可回应的帖子",
		msgStatusNoPostToMark:      "未选中帖子",
		msgStatusPollNoActive:      "当前帖子没有进行中的投票",
		msgStatusPollClosed:        "投票已结束",
		msgStatusPollVotedOrClosed: "已投票 · {status}",
		msgStatusPollNeedTrust:     "发起投票要求信任度 2+",
		msgStatusPollChecking:      "正在检查权限…",
		msgStatusFocusBodyFirst:    "请先填写正文",
		msgStatusTitleRequired:     "标题不能为空",
		msgStatusBodyRequired:      "正文不能为空",
		msgStatusTitleTooLong:      "标题不能超过 80 个字",
		msgStatusSendingChat:       "正在发送消息…",
		msgStatusThreadLocked:      "主题 {action}，作者 {by}",
		msgStatusThreadListFirst:   "已到达首串",
		msgStatusThreadListLast:    "已到达末串",
		msgStatusChatNotFound:      "主题未找到",
		msgStatusFailedRead:        "无法标记通知为已读",
		msgStatusFailedReadAll:     "无法标记所有通知为已读",
		msgStatusFailedDelete:      "无法删除通知",
		msgStatusDeleted:           "通知已删除",
		msgStatusFailedClearRead:   "无法清空已读通知",
		msgStatusClearRead:         "已读通知已清空",
		msgStatusFailedClearAll:    "无法清空所有通知",
		msgStatusClearedAll:        "通知已全部清空",
		msgStatusThreadAction:      "主题 {action}，作者 {by}",
		msgLabelThreadTitle:        "标题：",
		msgLabelAuthor:             "作者：",
		msgLabelTime:               "时间：",
		msgLabelTitle:              "标题：",
		msgLabelUser:               "用户：",
		msgLabelDisplay:            "显示名：",
		msgLabelRole:               "角色：",
		msgLabelTrust:              "信任：",
		msgLabelPosts:              "帖子：",
		msgLabelVisible:            "当前在线：",
		msgLabelMeta:               "附加信息：",
		msgLabelProfile:            "个人设置",
		msgLabelLoading:            "资料加载中…",
		msgLabelNoProfileField:     "未选择资料项。",
		msgStatusLocaleSwitch:      "语言：简体中文",
		msgHelpMainMenu:            "enter/→=打开  1-7=跳转  p=资料  o=在线  q=退出",
		msgHelpBoardList:           "enter/→=打开版面  c=聊天  N=通知  /=搜索  esc/←=主菜单  q=退出",
		msgHelpThreadList:          "enter/→=打开串  n=发帖  r=刷新  Ctrl+↑/↓=分页  /=搜索  esc/←=返回  q=退出",
		msgHelpNotifications:       "enter/→=标记已读  a=全部标记  d=删除  x=清空已读  c=清空全部  esc/←=返回",
		msgHelpProfile:             "enter/→=编辑  r=刷新  esc/←=菜单  q=退出",
		msgHelpProfileEdit:         "Ctrl+S=保存  Esc=取消",
		msgHelpOnline:              "r=刷新  enter/→=详情  esc/←=菜单  q=退出",
		msgHelpThreadReader:        "↑/↓=切换帖子  Ctrl+↑/↓=行滚动  Space/PgDn,b/PgUp=翻页  Home/End  n=回复  r=回应  p=投票  esc/←=返回",
		msgHelpThreadReaderMod:     "↑/↓=切换帖子  Ctrl+↑/↓=行滚动  Space/PgDn,b/PgUp=翻页  Home/End  n=回复  L=锁定  r=回应  p=投票  esc/←=返回",
		msgHelpPoll:                "1-9 投票 · esc/←=返回",
		msgHelpPollClosed:          "投票已结束 · esc/←=返回",
		msgHelpPollVoted:           "已经投票 · 1-9 投票 · esc/←=返回",
		msgHelpNewThread:           "enter/tab=正文  ctrl+s=正文  esc=取消",
		msgHelpCompose:             "",
		msgHelpChat:                "enter=发送  esc/←=返回",
		msgHelpSearch:              "输入关键词  enter=搜索  esc/←=返回",
		msgHeaderUnread:            " ● {count} 条未读",
		msgHeaderStatus:            " | {status}",
		msgHeaderFormat:            "{app} | {user} | {title}",
		msgHeaderAppName:           "BudgieBBS",
		msgPlaceholderThreadTitle:  "标题…",
		msgPlaceholderComposeBody:  "写点东西… (Ctrl+S 提交，Esc 取消)",
		msgPlaceholderSearchPrompt: "输入关键词并回车搜索…",
		msgPlaceholderChatMessage:  "说两句…",
		msgPlaceholderSearchInput:  "搜索: ",
		msgComposeHelpBase:         "Ctrl+S=提交  Esc=取消",
		msgComposeHelpWithPoll:     "Ctrl+S=提交  Esc=取消  Ctrl+P=添加投票模板  Ctrl+E=1小时投票  Ctrl+D=1天投票  Ctrl+W=1周投票",
		msgComposeHelpPollCheck:    "Ctrl+S=提交  Esc=取消  Ctrl+P=添加投票模板  Ctrl+E=1小时投票  Ctrl+D=1天投票  Ctrl+W=1周投票（权限检查中…）",
		msgComposeHelpPollTrust:    "Ctrl+S=提交  Esc=取消  Ctrl+P=添加投票模板  Ctrl+E=1小时投票  Ctrl+D=1天投票  Ctrl+W=1周投票（需要信任度2+）",
		msgComposeHelpPollChecked:  "Ctrl+S=提交  Esc=取消  Ctrl+P=添加投票模板  Ctrl+E=1小时投票  Ctrl+D=1天投票  Ctrl+W=1周投票",
		msgTitleMainMenu:           "主菜单",
		msgTitleBoards:             "版面",
		msgTitleNotifications:      "通知",
		msgTitleProfile:            "我的资料",
		msgTitleProfileEdit:        "编辑资料",
		msgTitleOnlineUsers:        "谁在线",
		msgTitlePoll:               "投票",
		msgTitleNewThread:          "发布新帖",
		msgTitleNewReply:           "回复",
		msgTitleLiveChat:           "大厅聊天",
		msgTitleSearch:             "搜索",
		msgTitleBoardFallback:      "版面",
		msgTitleNoPoll:             "未加载投票",
		msgTitleNoMessages:         "暂无消息，先说点什么吧！",
		msgPageBoard:               "版面",
		msgPageSearch:              "搜索",
		msgPageProfile:             "资料",
		msgPageOnline:              "在线",
		msgPageOnlineHint:          "在线",
		msgPageExit:                "退出",
		msgPageBoardsDesc:          "浏览版面与帖子",
		msgPageChatDesc:            "加入大厅聊天",
		msgPageNotificationsDesc:   "查看提及、回复、关注的帖子",
		msgPageSearchDesc:          "搜索帖子",
		msgPageProfileDesc:         "编辑公开资料与签名",
		msgPageOnlineDesc:          "看当前在线用户",
		msgPageExitDesc:            "离开 BudgieBBS",
		msgMenuThreadListEmpty:     "当前版面为空",
		msgMenuNotifications:       "通知",
		msgProfileSettingsTitle:    "个人设置",
		msgPostPrefixSeq:           "楼号: #{seq}",
		msgPostOp:                  "楼主",
		msgPostPrefixUpdated:       "更新: #{seq}",
		msgPostPrefixVersion:       "版本: v{version}",
		msgPostPrefixEdited:        "编辑: {time}",
		msgPostPrefixReply:         "回复: {reply}",
		msgPostPrefixType:          "类型: {type}",
		msgPostReaction:            "♥ {count}",
		msgPostPollTag:             "[投票]",
		msgPostAttachments:         "附件: {count}",
		msgPostMarked:              "已标记",
		msgPostRecommended:         "推荐",
		msgPostNoReply:             "禁回",
		msgPostTex:                 "TeX",
		msgPostMailBack:            "回邮",
		msgPostRedacted:            "已删除",
		msgPostSource:              "来源: {source}",
		msgPostMetaLinePrefix:      "      附加信息: ",
		msgPostMetaContinuation:    "            ",
		msgNotifMention:            "@ 提及",
		msgNotifReply:              "↩ 回复",
		msgNotifWatched:            "👁 关注",
		msgListByPosts:             "由 {author} 发布 · {count} 楼",
		msgListNoPoll:              "未加载投票",
		msgListPollClosed:          "投票已结束",
		msgListNoPollLoaded:        "未加载投票",
		msgListReplyVoteVotes:      "{count} 票{plural}",
		msgListOpen:                "开放中",
		msgListClosesAt:            "{time} 关闭",
		msgListCloses:              "已关闭",
		msgProfileDisplayName:      "显示名",
		msgProfileTitle:            "头衔",
		msgProfileBio:              "简介",
		msgProfileAvatar:           "头像",
		msgProfileSignature:        "签名",
		msgProfilePlan:             "计划",
		msgProfileHomepage:         "主页",
		msgProfileDisplayDesc:      "用于公开展示",
		msgProfileTitleDesc:        "短篇头衔或等级",
		msgProfileBioDesc:          "公开的简介信息",
		msgProfileAvatarDesc:       "表情符号或短文本",
		msgProfileSignatureDesc:    "默认帖子签名",
		msgProfilePlanDesc:         "传统的简报文本",
		msgProfileHomepageDesc:     "个人网站或主页",
		msgProfileEditTitlePrefix:  "编辑 {field}",
		msgCommonBoard:             "版面",
		msgCommonThread:            "串",
		msgCommonOnline:            "在线",
		msgCommonNoVisibleOnline:   "当前无可见在线用户，按 r 刷新。",
		msgCommonNoProfileSelected: "未选择资料项。",
		msgCommonThreadInBoard:     " in {thread}",
		msgCommonPostFormat:        "{label} in {thread}",
		msgCommonStatusIn:          "thread {id}",
		msgCommonThreadPrefix:      "{index} in {thread}",
		msgCommonNoSearch:          "未找到结果。",
		msgCommonVoteSuffix:        "",
		msgCommonVotePluralSuffix:  "",
		msgComposeTitlePrefix:      "标题: ",
		msgSearchResults:           "{count} 条结果",
		msgSearchNoResults:         "没有找到“{query}”的结果。",
		msgSearchNoResultText:      "未输入查询条件",
		msgOnlineNoUsers:           "当前无可见在线用户。按 r 刷新。",
		msgOnlineRefreshHint:       "刷新",
		msgOnlineNoUsersHint:       "当前无可见在线用户。按 r 刷新。",
		msgOnlineModeIdleTemplate:  "空闲 {idle}",
		msgOnlineModeOnline:        "在线",
		msgNotificationsNoRead:     "目前没有新通知。",
	},
	localeZHTW: {
		msgAppName:                 "BudgieBBS",
		msgStatusError:             "錯誤：{message}",
		msgStatusPresence:          "presence: {message}",
		msgStatusDisconnected:      "已中斷",
		msgStatusProfileSaved:      "資料已儲存",
		msgStatusProfileLoading:    "資料載入中",
		msgStatusPostSubmitted:     "文章已發送",
		msgStatusChatSent:          "訊息已發送",
		msgStatusThreadSubmitted:   "主題已發布",
		msgStatusNoPostSelected:    "未選取文章",
		msgStatusThreadNotLoaded:   "主題清單未載入",
		msgStatusThreadNotInBoard:  "目前主題不在目前版面清單中",
		msgStatusNoBoardSelected:   "未選取看板",
		msgStatusRefreshing:        "正在重新整理主題",
		msgStatusFirstPage:         "已到第一頁",
		msgStatusLastPage:          "已到最後一頁",
		msgStatusLoadingThreads:    "正在載入 {from}-{to} 篇主題",
		msgStatusNoPostToReact:     "沒有可回應的貼文",
		msgStatusNoPostToMark:      "未選取貼文",
		msgStatusPollNoActive:      "目前貼文沒有進行中的投票",
		msgStatusPollClosed:        "投票已關閉",
		msgStatusPollVotedOrClosed: "已投票 · {status}",
		msgStatusPollNeedTrust:     "發布投票需信譽值 2+",
		msgStatusPollChecking:      "正在檢查權限…",
		msgStatusFocusBodyFirst:    "請先輸入內文",
		msgStatusTitleRequired:     "標題不能為空",
		msgStatusBodyRequired:      "內容不能為空",
		msgStatusTitleTooLong:      "標題不能超過 80 個字",
		msgStatusSendingChat:       "發送中…",
		msgStatusThreadLocked:      "主題 {action}，作者 {by}",
		msgStatusThreadListFirst:   "已到第一篇",
		msgStatusThreadListLast:    "已到最後一篇",
		msgStatusChatNotFound:      "主題未找到",
		msgStatusFailedRead:        "無法將通知標記為已讀",
		msgStatusFailedReadAll:     "無法將所有通知標記為已讀",
		msgStatusFailedDelete:      "無法刪除通知",
		msgStatusDeleted:           "通知已刪除",
		msgStatusFailedClearRead:   "無法清除已讀通知",
		msgStatusClearRead:         "已讀通知已清空",
		msgStatusFailedClearAll:    "無法清除所有通知",
		msgStatusClearedAll:        "通知已全部清除",
		msgStatusThreadAction:      "主題 {action}，作者 {by}",
		msgLabelThreadTitle:        "標題：",
		msgLabelAuthor:             "作者：",
		msgLabelTime:               "時間：",
		msgLabelTitle:              "標題：",
		msgLabelUser:               "使用者：",
		msgLabelDisplay:            "顯示名稱：",
		msgLabelRole:               "角色：",
		msgLabelTrust:              "信譽：",
		msgLabelPosts:              "貼文：",
		msgLabelVisible:            "目前可見上線人數：",
		msgLabelMeta:               "額外資訊：",
		msgLabelProfile:            "個人設定",
		msgLabelLoading:            "資料載入中…",
		msgLabelNoProfileField:     "未選取個人資料欄位。",
		msgHelpMainMenu:            "enter/→=開啟  1-7=跳轉  p=個人資料  o=線上  q=離開",
		msgHelpBoardList:           "enter/→=開啟看板  c=聊天室  N=通知  /=搜尋  esc/←=主選單  q=離開",
		msgHelpThreadList:          "enter/→=開啟主題  n=新主題  r=重新整理  Ctrl+↑/↓=分頁  /=搜尋  esc/←=返回  q=離開",
		msgHelpNotifications:       "enter/→=標記為已讀  a=全部標記  d=刪除  x=清除已讀  c=全部清除  esc/←=返回",
		msgHelpProfile:             "enter/→=編輯  r=重新整理  esc/←=選單  q=離開",
		msgHelpProfileEdit:         "Ctrl+S=儲存  Esc=取消",
		msgHelpOnline:              "r=重新整理  enter/→=詳細  esc/←=選單  q=離開",
		msgHelpThreadReader:        "↑/↓=切換主題  Ctrl+↑/↓=行捲動  Space/PgDn,b/PgUp=頁面  Home/End  n=回文  r=回應  p=投票  esc/←=返回",
		msgHelpThreadReaderMod:     "↑/↓=切換主題  Ctrl+↑/↓=行捲動  Space/PgDn,b/PgUp=頁面  Home/End  n=回文  L=鎖定  r=回應  p=投票  esc/←=返回",
		msgHelpPoll:                "1-9 投票 · esc/←=返回",
		msgHelpPollClosed:          "投票已關閉 · esc/←=返回",
		msgHelpPollVoted:           "已投票 · 1-9 投票 · esc/←=返回",
		msgHelpNewThread:           "enter/tab=內文  ctrl+s=內文  esc=取消",
		msgHelpCompose:             "",
		msgHelpChat:                "enter=傳送  esc/←=返回",
		msgHelpSearch:              "輸入關鍵字  enter=搜尋  esc/←=返回",
		msgHeaderUnread:            " ● {count} 則未讀",
		msgHeaderStatus:            " | {status}",
		msgHeaderFormat:            "{app} | {user} | {title}",
		msgHeaderAppName:           "BudgieBBS",
		msgPlaceholderThreadTitle:  "標題…",
		msgPlaceholderComposeBody:  "撰寫內容… (Ctrl+S 送出，Esc 取消)",
		msgPlaceholderSearchPrompt: "輸入關鍵字並按 Enter 搜尋…",
		msgPlaceholderChatMessage:  "說一句…",
		msgPlaceholderSearchInput:  "搜尋: ",
		msgComposeHelpBase:         "Ctrl+S=送出  Esc=取消",
		msgComposeHelpWithPoll:     "Ctrl+S=送出  Esc=取消  Ctrl+P=新增投票範本  Ctrl+E=1小時投票  Ctrl+D=1天投票  Ctrl+W=1週投票",
		msgComposeHelpPollCheck:    "Ctrl+S=送出  Esc=取消  Ctrl+P=新增投票範本  Ctrl+E=1小時投票  Ctrl+D=1天投票  Ctrl+W=1週投票（檢查權限中…）",
		msgComposeHelpPollTrust:    "Ctrl+S=送出  Esc=取消  Ctrl+P=新增投票範本  Ctrl+E=1小時投票  Ctrl+D=1天投票  Ctrl+W=1週投票（需信譽值2+）",
		msgComposeHelpPollChecked:  "Ctrl+S=送出  Esc=取消  Ctrl+P=新增投票範本  Ctrl+E=1小時投票  Ctrl+D=1天投票  Ctrl+W=1週投票",
		msgTitleMainMenu:           "主選單",
		msgTitleBoards:             "看板",
		msgTitleNotifications:      "通知",
		msgTitleProfile:            "我的個人資料",
		msgTitleProfileEdit:        "編輯個人資料",
		msgTitleOnlineUsers:        "誰在線",
		msgTitlePoll:               "投票",
		msgTitleNewThread:          "發表新主題",
		msgTitleNewReply:           "回應",
		msgTitleLiveChat:           "大廳聊天室",
		msgTitleSearch:             "搜尋",
		msgTitleBoardFallback:      "看板",
		msgTitleNoPoll:             "未載入投票",
		msgTitleNoMessages:         "還沒有人說話，先說一些吧！",
		msgPageBoard:               "看板",
		msgPageSearch:              "搜尋",
		msgPageProfile:             "個人資料",
		msgPageOnline:              "線上",
		msgPageOnlineHint:          "上線",
		msgPageExit:                "離開",
		msgPageBoardsDesc:          "瀏覽看板與文章",
		msgPageChatDesc:            "加入大廳聊天室",
		msgPageNotificationsDesc:   "查看提及、回文、追蹤主題",
		msgPageSearchDesc:          "搜尋貼文",
		msgPageProfileDesc:         "編輯公開個人資料與簽名",
		msgPageOnlineDesc:          "查看誰在線上",
		msgPageExitDesc:            "離開 BudgieBBS",
		msgMenuThreadListEmpty:     "目前看板空白",
		msgMenuNotifications:       "通知",
		msgProfileSettingsTitle:    "個人設定",
		msgPostPrefixSeq:           "樓數: #{seq}",
		msgPostOp:                  "樓主",
		msgPostPrefixUpdated:       "更新: #{seq}",
		msgPostPrefixVersion:       "版本: v{version}",
		msgPostPrefixEdited:        "編輯: {time}",
		msgPostPrefixReply:         "回文: {reply}",
		msgPostPrefixType:          "類型: {type}",
		msgPostReaction:            "♥ {count}",
		msgPostPollTag:             "[投票]",
		msgPostAttachments:         "附件: {count}",
		msgPostMarked:              "標記",
		msgPostRecommended:         "推薦",
		msgPostNoReply:             "不許回",
		msgPostTex:                 "TeX",
		msgPostMailBack:            "回文",
		msgPostRedacted:            "已刪除",
		msgPostSource:              "來源: {source}",
		msgPostMetaLinePrefix:      "      附加資訊: ",
		msgPostMetaContinuation:    "            ",
		msgNotifMention:            "@ 提及",
		msgNotifReply:              "↩ 回文",
		msgNotifWatched:            "👁 追蹤",
		msgListByPosts:             "由 {author} 發表 · {count} 版文",
		msgListNoPoll:              "未載入投票",
		msgListPollClosed:          "投票已關閉",
		msgListNoPollLoaded:        "未載入投票",
		msgListReplyVoteVotes:      "{count} 票{plural}",
		msgListOpen:                "開放",
		msgListClosesAt:            "{time} 關閉",
		msgListCloses:              "已關閉",
		msgProfileDisplayName:      "顯示名稱",
		msgProfileTitle:            "稱號",
		msgProfileBio:              "個人簡介",
		msgProfileAvatar:           "頭像",
		msgProfileSignature:        "簽名",
		msgProfilePlan:             "個人規劃",
		msgProfileHomepage:         "主頁",
		msgProfileDisplayDesc:      "公開展示用",
		msgProfileTitleDesc:        "簡短稱號或等級",
		msgProfileBioDesc:          "公開個人介紹",
		msgProfileAvatarDesc:       "表情符號或簡短頭像文字",
		msgProfileSignatureDesc:    "預設貼文簽名",
		msgProfilePlanDesc:         "傳統計劃文字",
		msgProfileHomepageDesc:     "個人網站或個人主頁",
		msgProfileEditTitlePrefix:  "編輯 {field}",
		msgCommonBoard:             "看板",
		msgCommonThread:            "主題",
		msgCommonOnline:            "上線",
		msgCommonNoVisibleOnline:   "目前沒有可見線上用戶，按 r 重新整理。",
		msgCommonNoProfileSelected: "未選取個人資料欄位。",
		msgCommonThreadInBoard:     " 在 {thread}",
		msgCommonPostFormat:        "{label} 在 {thread}",
		msgCommonStatusIn:          "主題 {id}",
		msgCommonThreadPrefix:      "{index} 在 {thread}",
		msgCommonNoSearch:          "查無結果。",
		msgCommonVoteSuffix:        "",
		msgCommonVotePluralSuffix:  "",
		msgComposeTitlePrefix:      "標題: ",
		msgSearchResults:           "{count} 則結果",
		msgSearchNoResults:         "未找到有關「{query}」的結果。",
		msgSearchNoResultText:      "未輸入查詢",
		msgOnlineNoUsers:           "目前沒有可見線上使用者，按 r 重新整理。",
		msgOnlineRefreshHint:       "重新整理",
		msgOnlineNoUsersHint:       "目前沒有可見線上使用者，按 r 重新整理。",
		msgOnlineModeIdleTemplate:  "閒置 {idle}",
		msgOnlineModeOnline:        "上線",
		msgNotificationsNoRead:     "目前尚無通知。",
		msgStatusLocaleSwitch:      "語言：繁體中文",
	},
}

func localeFromEnviron(env []string) localeCode {
	values := parseEnviron(env)
	for _, key := range []string{"BUDGIE_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		raw := values[key]
		if raw == "" {
			continue
		}
		parsed := parseLocale(raw)
		if parsed != "" {
			return parsed
		}
	}
	return localeEN
}

func parseLocale(raw string) localeCode {
	normalized := normalizeLocale(raw)
	switch {
	case normalized == "":
		return localeEN
	case isLocale(normalized, "en", "en-us", "c", "posix"):
		return localeEN
	case isLocale(normalized, "zh", "zh-cn", "zh_cn", "zh-hans", "zh-sg"):
		return localeZHCN
	case isLocale(normalized, "zh-tw", "zh-hant", "zh-hk", "zh-mo"):
		return localeZHTW
	default:
		return ""
	}
}

func normalizeLocale(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	if v == "" {
		return ""
	}
	if i := strings.IndexAny(v, ".@"); i >= 0 {
		v = v[:i]
	}
	v = strings.ReplaceAll(v, "_", "-")
	return v
}

func isLocale(value string, expected ...string) bool {
	for _, expectedValue := range expected {
		if value == expectedValue {
			return true
		}
	}
	return false
}

func (m model) tr(key msgKey, values ...map[string]string) string {
	return trLocale(m.locale, key, values...)
}

func trLocale(locale localeCode, key msgKey, values ...map[string]string) string {
	if locale == "" {
		locale = localeEN
	}
	dict := tuiMessages[locale]
	template := ""
	if dict != nil {
		template = dict[key]
	}
	if template == "" {
		template = tuiMessages[localeEN][key]
	}
	if template == "" {
		return string(key)
	}
	if len(values) == 0 {
		return template
	}
	for name, value := range values[0] {
		template = strings.ReplaceAll(template, "{"+name+"}", value)
	}
	return template
}
