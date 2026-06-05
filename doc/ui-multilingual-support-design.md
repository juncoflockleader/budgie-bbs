# UI Multilingual Support Design

This document is an implementation guide for adding multilingual UI support to Budgie BBS. It is intentionally explicit because the implementation may be handed to a faster model that should not infer architecture from scratch.

## Goal

Add first-class UI localization for the Web UI and SSH/TUI UI without changing backend event payloads, command names, database schema, or user-generated content.

Initial supported locales:

- `en`: English. This is the current UI language and must remain the fallback.
- `zh-CN`: Simplified Chinese.

Non-goals for this milestone:

- Do not translate board names, thread titles, post bodies, usernames, display names, signatures, chat messages, mail contents, or admin-created category names.
- Do not localize backend protocol constants such as event names, command names, role IDs, board IDs, notification kinds, content types, or API error codes.
- Do not add an external translation service.
- Do not add a backend profile preference yet. Web can persist locale in `localStorage`; TUI can read locale from SSH environment variables.

## Current State

The UI strings are currently inline:

- Web React components under `web/src/App.tsx`, `web/src/pages/*.tsx`, and `web/src/components/*.tsx`.
- TUI strings are mostly in `internal/tui/app.go`, including page titles, headers, help lines, status messages, empty states, labels, and compose placeholders.

There is no existing i18n package or dependency.

## Design Principles

1. Keep the system small and local. Use a project-owned dictionary instead of adding `i18next` or another dependency.
2. Make English the source locale and fallback.
3. Use stable message keys. Never use English text itself as the key.
4. Translate only interface chrome and locally generated messages.
5. Keep dynamic user data outside translations except as interpolation values.
6. For TUI, preserve terminal layout by measuring display width with `lipgloss.Width`, not byte length or rune count.
7. Allow partial migration only behind fallback behavior: missing `zh-CN` keys must fall back to English, but tests should push toward full coverage.

## Web Implementation

### Files To Add

Create:

- `web/src/i18n/messages/en.ts`
- `web/src/i18n/messages/zh-CN.ts`
- `web/src/i18n/index.tsx`
- `web/src/i18n/format.ts`

### Message Shape

Use flat keys. Flat keys are easier to grep, refactor, and type-check.

Example:

```ts
// web/src/i18n/messages/en.ts
export const en = {
  'app.name': 'Budgie BBS',
  'nav.searchPlaceholder': 'Search...',
  'nav.searchAria': 'Search posts',
  'nav.unread': 'Unread',
  'nav.resident': 'Resident',
  'nav.chat': 'Chat',
  'nav.people': 'People',
  'nav.rankings': 'Rankings',
  'nav.admin': 'Admin',
  'nav.inbox': 'Inbox',
  'nav.notifications': 'Notifications',
  'nav.openProfile': 'Open your profile',
  'nav.logout': 'Logout',
  'common.back': 'Back',
  'common.refresh': 'Refresh',
  'common.loading': 'Loading...',
  'common.save': 'Save',
  'common.cancel': 'Cancel',
  'common.delete': 'Delete',
  'common.error': 'Error: {message}',
  'settings.language': 'Language',
} as const

export type MessageKey = keyof typeof en
```

```ts
// web/src/i18n/messages/zh-CN.ts
import type { MessageKey } from './en'

export const zhCN = {
  'app.name': 'Budgie BBS',
  'nav.searchPlaceholder': '搜索...',
  'nav.searchAria': '搜索帖子',
  'nav.unread': '未读',
  'nav.resident': '驻站',
  'nav.chat': '聊天',
  'nav.people': '用户',
  'nav.rankings': '排行',
  'nav.admin': '管理',
  'nav.inbox': '信箱',
  'nav.notifications': '通知',
  'nav.openProfile': '打开个人资料',
  'nav.logout': '退出',
  'common.back': '返回',
  'common.refresh': '刷新',
  'common.loading': '加载中...',
  'common.save': '保存',
  'common.cancel': '取消',
  'common.delete': '删除',
  'common.error': '错误：{message}',
  'settings.language': '语言',
} satisfies Record<MessageKey, string>
```

Important: `zh-CN.ts` should use `satisfies Record<MessageKey, string>` so TypeScript fails when English adds a key and Chinese does not.

### Provider And Hook

Implement a small provider:

```ts
// web/src/i18n/index.tsx
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { en, type MessageKey } from './messages/en'
import { zhCN } from './messages/zh-CN'

export type LocaleCode = 'en' | 'zh-CN'
type Values = Record<string, string | number | boolean | null | undefined>

const dictionaries = {
  en,
  'zh-CN': zhCN,
} satisfies Record<LocaleCode, Record<MessageKey, string>>

const storageKey = 'budgie:ui-locale'

interface I18nContextValue {
  locale: LocaleCode
  setLocale: (locale: LocaleCode) => void
  t: (key: MessageKey, values?: Values) => string
}

const I18nContext = createContext<I18nContextValue | null>(null)

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<LocaleCode>(() => detectInitialLocale())

  useEffect(() => {
    document.documentElement.lang = locale
  }, [locale])

  const value = useMemo<I18nContextValue>(() => ({
    locale,
    setLocale(next) {
      window.localStorage.setItem(storageKey, next)
      setLocaleState(next)
    },
    t(key, values) {
      const template = dictionaries[locale][key] ?? dictionaries.en[key] ?? key
      return interpolate(template, values)
    },
  }), [locale])

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>
}

export function useI18n() {
  const value = useContext(I18nContext)
  if (!value) throw new Error('useI18n must be used inside I18nProvider')
  return value
}

function detectInitialLocale(): LocaleCode {
  const saved = window.localStorage.getItem(storageKey)
  if (saved === 'zh-CN' || saved === 'en') return saved
  const browser = navigator.language || ''
  return browser.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en'
}

function interpolate(template: string, values?: Values) {
  if (!values) return template
  return template.replace(/\{(\w+)\}/g, (_, name: string) => {
    const value = values[name]
    return value == null ? `{${name}}` : String(value)
  })
}
```

In `web/src/main.tsx`, wrap the app:

```tsx
<I18nProvider>
  <App />
</I18nProvider>
```

### Locale Switcher

Add a compact language selector in `App.tsx` top nav:

```tsx
const { locale, setLocale, t } = useI18n()

<select
  className="nav-locale"
  value={locale}
  onChange={event => setLocale(event.currentTarget.value as LocaleCode)}
  aria-label={t('settings.language')}
>
  <option value="en">EN</option>
  <option value="zh-CN">中文</option>
</select>
```

Add messages:

- `settings.language`: `Language` / `语言`

Do not make language selection require login.

### Date And Number Formatting

Create `web/src/i18n/format.ts`:

```ts
import type { LocaleCode } from './index'

export function formatDateTime(ms: number, locale: LocaleCode) {
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(new Date(ms))
}

export function formatCount(value: number, locale: LocaleCode) {
  return new Intl.NumberFormat(locale).format(value)
}
```

Use this only where dates/numbers are UI chrome. It is acceptable to keep existing date formatting in the first pass if changing it creates risk; prioritize text extraction first.

### Web Migration Order

Migrate in this order to minimize conflicts:

1. Add `web/src/i18n/*` and wrap `App`.
2. Migrate `App.tsx` nav labels, placeholders, titles, and button text.
3. Migrate high-traffic pages:
   - `AuthPage.tsx`
   - `BoardListPage.tsx`
   - `ThreadListPage.tsx`
   - `ThreadPage.tsx`
   - `NewThreadPage.tsx`
4. Migrate utility pages:
   - `NotificationsPage.tsx`
   - `PrivatePage.tsx`
   - `ChatPage.tsx`
   - `SearchPage.tsx`
   - `UnreadPage.tsx`
   - `ResidentFeedPage.tsx`
   - `SocialPage.tsx`
   - `RankingsPage.tsx`
   - `UserProfilePage.tsx`
   - `AuthorPostsPage.tsx`
   - `AdminPage.tsx`
5. Migrate shared components:
   - `PollWidget.tsx`
   - `PollComposer.tsx`
   - `AttachmentComposer.tsx`
   - `Spinner.tsx`

When migrating, do not translate API data:

```tsx
// Good
<h2>{t('threadList.title', { board: board.name })}</h2>

// Bad: never pass board.name through a translation dictionary
<h2>{t(board.name as MessageKey)}</h2>
```

### Web Tests And Validation

Required checks:

```bash
cd web
npm run build
```

Recommended lightweight tests if test infrastructure is added later:

- `detectInitialLocale()` returns `zh-CN` for `zh`, `zh-CN`, and `zh-TW`; returns `en` otherwise.
- `t('common.error', { message: 'x' })` returns localized text with `x` interpolated.
- `zh-CN` dictionary satisfies all English keys at TypeScript build time.

Manual QA:

- Open Web UI in English. Existing labels should look unchanged except the language selector.
- Switch to Chinese. Top nav, auth page, board page, thread list, thread page, compose page, notifications, inbox, admin page labels should change.
- Refresh page. Locale should persist.
- Log out. Locale should persist.
- User-generated content remains unchanged.

## TUI Implementation

### Files To Add

Create:

- `internal/tui/i18n.go`
- `internal/tui/i18n_test.go`

### Model Changes

Add a locale field to `model`:

```go
type localeCode string

const (
	localeEN   localeCode = "en"
	localeZHCN localeCode = "zh-CN"
)

type model struct {
	// existing fields...
	locale localeCode
}
```

Update `terminalProfile` in `internal/tui/terminal.go`:

```go
type terminalProfile struct {
	supportsANSI bool
	baudDelay    time.Duration
	locale       localeCode
}
```

In `terminalProfileFromEnviron`, parse locale from env:

Priority:

1. `BUDGIE_LANG`
2. `LC_ALL`
3. `LC_MESSAGES`
4. `LANG`

Accepted values:

- `en`, `en_US`, `en-US`, `C`, `POSIX` -> `en`
- `zh`, `zh_CN`, `zh-CN`, `zh_Hans`, `zh-Hans` -> `zh-CN`
- Unknown -> `en`

In `internal/tui/server.go`, pass the profile locale into `newModel`.

Change constructor signature:

```go
func newModel(c *core.Core, actor *core.User, width, height int, supportsANSI bool, locale localeCode) model
```

If updating every test call is too noisy, add a helper:

```go
func newModelWithLocale(c *core.Core, actor *core.User, width, height int, supportsANSI bool, locale localeCode) model
```

and keep `newModel` defaulting to `localeEN`.

### TUI Translation API

Use typed string keys and simple interpolation:

```go
type msgKey string

const (
	msgAppName             msgKey = "app.name"
	msgPageMainMenu        msgKey = "page.mainMenu"
	msgPageBoards          msgKey = "page.boards"
	msgPageNotifications   msgKey = "page.notifications"
	msgHelpMainMenu        msgKey = "help.mainMenu"
	msgHelpBoardList       msgKey = "help.boardList"
	msgCommonBack          msgKey = "common.back"
	msgStatusProfileSaved  msgKey = "status.profileSaved"
	msgStatusError         msgKey = "status.error"
	msgEmptyNoMessages     msgKey = "empty.noMessages"
)

var tuiMessages = map[localeCode]map[msgKey]string{
	localeEN: {
		msgAppName: "BudgieBBS",
		msgPageMainMenu: "Main Menu",
		msgHelpMainMenu: "enter/→=open  1-7=jump  p=profile  o=online  q=quit",
		msgStatusError: "error: {message}",
	},
	localeZHCN: {
		msgAppName: "BudgieBBS",
		msgPageMainMenu: "主菜单",
		msgHelpMainMenu: "enter/→=打开  1-7=跳转  p=资料  o=在线  q=退出",
		msgStatusError: "错误：{message}",
	},
}

func (m model) tr(key msgKey, values ...map[string]string) string {
	return trLocale(m.locale, key, values...)
}

func trLocale(locale localeCode, key msgKey, values ...map[string]string) string {
	dict := tuiMessages[locale]
	template := ""
	if dict != nil {
		template = dict[key]
	}
	if template == "" {
		template = tuiMessages[localeEN][key]
	}
	if template == "" {
		template = string(key)
	}
	if len(values) == 0 {
		return template
	}
	for name, value := range values[0] {
		template = strings.ReplaceAll(template, "{"+name+"}", value)
	}
	return template
}
```

### TUI Migration Order

Migrate `internal/tui/app.go` in small passes:

1. Page/header titles:
   - `headerTitle()`
   - `pageName()`
   - main menu item titles/descriptions
2. Help lines:
   - Main menu
   - Board list
   - Thread list
   - Thread reader
   - Compose
   - Poll
   - Notifications
   - Profile
   - Online
   - Chat
   - Search
3. Empty/loading/status messages:
   - `statusMsg` assignments
   - Empty states such as no online users, no messages, no search results
4. Field labels:
   - Post metadata labels: `Title`, `Author`, `Time`, `Meta`
   - Profile field labels/descriptions
   - Poll labels
5. Input placeholders:
   - Thread title
   - Compose body
   - Chat input

Keep keyboard names stable. Translate actions, not physical keys.

Examples:

- Good: `enter/→=打开  esc/←=返回  q=退出`
- Bad: `回车/右箭头=打开` unless the implementation also verifies alignment on common terminals.

### TUI Width And Layout Requirements

Chinese characters are usually double-width in terminals. Therefore:

- Use `lipgloss.Width` for display width.
- Continue using `truncateDisplayWidth`.
- Do not use `len(s)` for visual width.
- After adding Chinese strings, rerun height tests. The first header line must not wrap.
- Help lines can be truncated by existing full-width rendering if necessary.

Add or update tests:

- `TestLocaleFromEnv`
- `TestTUITranslationFallback`
- `TestTUIChineseHeaderDoesNotWrap`
- Existing `TestViewHeightFitsTerminal` must pass for `localeZHCN`.

Suggested test shape:

```go
func TestLocaleFromEnv(t *testing.T) {
	cases := []struct {
		env  []string
		want localeCode
	}{
		{[]string{"BUDGIE_LANG=zh-CN"}, localeZHCN},
		{[]string{"LANG=zh_CN.UTF-8"}, localeZHCN},
		{[]string{"LANG=en_US.UTF-8"}, localeEN},
		{[]string{"LANG=C"}, localeEN},
	}
	for _, tc := range cases {
		if got := localeFromEnviron(tc.env); got != tc.want {
			t.Fatalf("localeFromEnviron(%v) = %s, want %s", tc.env, got, tc.want)
		}
	}
}
```

Required checks:

```bash
GOCACHE=/private/tmp/budgie-bbs-go-cache go test ./internal/tui
```

## Message Key Naming

Use these prefixes:

- `app.*`
- `nav.*`
- `page.*`
- `common.*`
- `action.*`
- `status.*`
- `error.*`
- `empty.*`
- `auth.*`
- `board.*`
- `thread.*`
- `post.*`
- `compose.*`
- `poll.*`
- `notification.*`
- `private.*`
- `profile.*`
- `admin.*`
- `chat.*`
- `search.*`
- `online.*`
- `ranking.*`

Prefer specific keys over reusing a generic key where grammar may differ.

Good:

- `thread.replyCount`: `{count} replies` / `{count} 条回复`
- `admin.createBoard`: `Create board` / `创建版面`

Avoid:

- Reusing `common.create` + raw noun everywhere, because Chinese word order and phrasing may differ by context.

## Error Handling

For this milestone:

- Translate client-side validation errors.
- Translate locally generated TUI status messages.
- Do not translate backend error messages returned by API, except for adding a localized prefix such as `Error: {message}`.

Reason: backend errors are currently not stable i18n keys. Translating them safely requires a separate backend error-code pass.

## Acceptance Criteria

The milestone is done when:

1. Web UI has a visible language selector.
2. Web locale persists across refresh and logout.
3. Web build passes with English and Chinese dictionaries type-checked.
4. TUI locale can be selected with `BUDGIE_LANG=zh-CN` or `LANG=zh_CN.UTF-8`.
5. TUI English remains the default for unknown or missing locale.
6. Main TUI views render Chinese headers/help/statuses without wrapping the first header line.
7. No backend protocol, event, command, or database schema changes are required.
8. User-generated content is never translated.
9. Existing TUI and Web behavior tests still pass.

## Suggested Implementation Checklist

- [ ] Add Web i18n files and provider.
- [ ] Wrap `App` with `I18nProvider`.
- [ ] Add Web language selector.
- [ ] Migrate `App.tsx`.
- [ ] Migrate Web auth, board, thread, compose, notification, private, profile, admin, chat, search, ranking pages.
- [ ] Add Web build validation.
- [ ] Add TUI i18n files.
- [ ] Parse TUI locale from SSH environment.
- [ ] Add locale field to TUI model.
- [ ] Migrate TUI page titles and help lines.
- [ ] Migrate TUI statuses, empty states, field labels, placeholders.
- [ ] Add TUI i18n tests.
- [ ] Run `go test ./internal/tui`.

## Follow-Up Milestones

After this milestone, consider:

- Persisting locale in user profile so Web and TUI share preference.
- Adding `zh-TW` separately if needed. Do not alias it to `zh-CN` forever.
- Moving backend errors to stable error codes and localizing display messages in clients.
- Localizing email templates or notification body text if those become user-facing outside the app UI.
