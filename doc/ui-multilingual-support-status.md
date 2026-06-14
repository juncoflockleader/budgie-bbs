# Multilingual Support Status

Date: 2026-06-14

## Scope

This status summarizes the current gap against
[`ui-multilingual-support-design.md`](ui-multilingual-support-design.md) for
adding first-class UI localization with English fallback plus Simplified Chinese
(`zh-CN`) and Traditional Chinese (`zh-TW`).

## What Is In Place

- Web i18n scaffolding exists under `web/src/i18n`.
- Web dictionaries exist for `en`, `zh-CN`, and `zh-TW`.
- `I18nProvider` wraps the Web app in `web/src/main.tsx`.
- The Web locale is persisted in `localStorage` under `budgie:ui-locale`.
- The logged-in Web top nav has a language selector with `EN`, `中文`, and `中文（繁）`.
- TUI locale support has been started with `localeEN`, `localeZHCN`, and `localeZHTW`.
- TUI environment parsing includes `BUDGIE_LANG`, `LC_ALL`, `LC_MESSAGES`, and `LANG`.

## Current Gaps

- Web build passes through `./scripts/build-web.sh`, which pins a stable
  Node/npm resolution order for local and Codex-driven builds.
- High-traffic Web pages still contain hardcoded English strings, especially:
  - `web/src/pages/BoardListPage.tsx`
  - `web/src/pages/ThreadListPage.tsx`
  - `web/src/pages/ThreadPage.tsx`
- The Web language selector is currently only visible after login. The design doc requires language selection to not require login.
- Chinese dictionaries still rely partly on English fallback, so some UI will remain English until all referenced keys are translated.
- TUI work appears partially implemented but still needs verification with `go test ./internal/tui`.

## How To Switch Language

### Web

After logging in, use the language selector in the top navigation:

- `EN` selects English.
- `中文` selects Simplified Chinese (`zh-CN`).
- `中文（繁）` selects Traditional Chinese (`zh-TW`).

The selected Web locale persists across refreshes through `localStorage`.

### TUI / SSH

Set a locale environment variable before starting the SSH/TUI session:

```bash
BUDGIE_LANG=zh-CN
BUDGIE_LANG=zh-TW
```

Standard locale environment variables should also work once the TUI changes are verified:

```bash
LANG=zh_CN.UTF-8
LANG=zh_TW.UTF-8
```

## Next Steps

- Add all missing English message keys used by Web components.
- Add matching `zh-CN` and `zh-TW` translations.
- Replace remaining hardcoded Web UI strings with `t(...)`.
- Move or duplicate the Web language selector onto the auth screen so language selection does not require login.
- Keep `./scripts/build-web.sh` in the release path.
- Run `go test ./internal/tui` and fix any TUI localization regressions.
