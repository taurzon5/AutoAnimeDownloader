# 156. The web UI is English only, and its message layer stays with one locale

**Location:** `src/internal/frontend/project.inlang/settings.json` (`"locales": ["en"]`),
`messages/en.json` (the only message file; there is no `pt-BR.json`),
`src/lib/stores/locale.ts` (a store that is always `en`, and the clean-up of a stored Portuguese
choice), `src/lib/domain/format.ts` (`FormatLocale = 'en'`). The language switch that sat in the
nav rail and the More menu is gone. Pinned by `tests/unit/plurals.test.ts`, `format.test.ts` and
`recent.test.ts`, which check English only.

**What it looks like:** an i18n library (paraglide), a message file per key, `$locale && m.x()` in
every screen and a `locale` store, all for one language. The obvious cleanup is to inline the
strings and drop the store.

**Why it's right:** on 2026-11-26 the user dropped the Portuguese translation: "Drop pt language,
keep only English". The message layer stays because it still pays for itself. Every user-facing
string lives in one file, so wording is reviewed and changed in one place. The plural forms
(`count=1` / `count=*`) keep "1 episode" right without `episode(s)`. Tests find text by message.
Removing paraglide would rewrite hundreds of lines for no behavior change. `$locale &&` stays as
written: the store never changes now, so it costs nothing, and removing it everywhere is churn
with no gain.

A browser that used the Portuguese UI keeps `PARAGLIDE_LOCALE` in localStorage, and paraglide may
have left a cookie. `locale.ts` removes the key and sets English at startup, which overwrites both.

Before this, CLAUDE.md made the Portuguese UI translation the one exception to "everything in
English"; that exception is gone too.

**Don't "fix" by:**
- Inlining the strings and removing paraglide: it loses the single message file and the plural
  forms.
- Adding a language back as a half measure (a few screens, or machine-translated): a second
  locale needs every key, the switch, and the width checks in the layout tests that ran in it.
- Removing `locale.ts`: screens subscribe to it, and `setLocale('en')` there is what cleans a
  stored Portuguese choice.
