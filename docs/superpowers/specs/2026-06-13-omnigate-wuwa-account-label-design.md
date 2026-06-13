# WuWa AccountChip — User-Defined Account Labels (Design)

**Status:** APPROVED (spec gate — 3 rounds, opus adversarial review)
**Branch:** `feat/wuwa-account-switcher` (continuation; follow-up item A from the switcher work)
**Commit convention:** no `Co-Authored-By` trailer. Tests run CGO-free → never pass `-race`.

## Problem

The WuWa AccountChip (shipped in the switcher work) shows, per remembered
account, `uid || email || username`. Two gaps:

1. The in-game nickname is **server-only** — it cannot be read from any local
   file (confirmed by scanning `LocalStorage.db` + game logs on 2026-06-13).
2. The UID only lights up **after** that account has actually played a full
   game session (the mtime trust guard, by design). Until then the chip falls
   back to the raw email — not a friendly identity.

So an account the user hasn't recently played is shown as a bare email, and
there is no way to give it a human name.

## Goal

Let the user assign a **local, free-text label** to each remembered account
from the chip dropdown. This is the only zero-risk, zero-login way to surface a
recognizable name. The label becomes the highest-priority display identity.

**Display priority (everywhere the chip shows an identity): `label > uid > email`.**

## Non-Goals

- No login, no network, no scraping of server-side nicknames.
- No change to the switch mechanism (`last_login_cuid` flip) or the UID mtime
  guard.
- No labels for other providers (HoYoverse / Endfield) — they don't even render
  a chip yet. The label store is keyed by kurogames cuid; v1 is single-provider.
- No custom avatar image (out of scope; avatar stays the initial letter).

## Data model

The App-owned sidecar `wuwa_uid_cache.json` (non-sensitive, numbers + free
text only — **never** a token) grows from `cuid → uid` to `cuid → {uid, label}`.

**Old format (must still load):**
```json
{ "537195734": "700727240" }
```
**New format (always written):**
```json
{ "537195734": { "uid": "700727240", "label": "主帳" } }
```

Backward compatibility is per-value: each map value may be a JSON **string**
(legacy `uid`) or a JSON **object** `{uid,label}`. Implemented via a custom
`UnmarshalJSON` on the per-entry struct that accepts either shape. `label` is
omitted (`omitempty`) when empty, so a cache that never sets labels round-trips
to the object form `{"uid":"…"}` — acceptable, still loads everywhere.

Internal Go shape (JSON tags give the documented lowercase wire format;
`omitempty` keeps label-less entries as `{"uid":"…"}`):
```go
type acctMeta struct {
    UID   string `json:"uid"`
    Label string `json:"label,omitempty"`
}
// UnmarshalJSON (pointer receiver): if the raw bytes are a JSON string → {UID: s};
//                else decode as the {uid,label} object.
```
`uidCache.m` becomes `map[string]acctMeta`. encoding/json invokes the
pointer-receiver `Unmarshaler` on each map value, so legacy string values
(`"700727240"`) decode correctly. Persist remains atomic (`.tmp` write +
`os.Rename`), best-effort.

## Backend API

`internal/app/account_handler.go`:

- `(*uidCache) Record(cuid, uid string)` — unchanged contract (store/refresh
  the UID) but **preserves any existing label** for that cuid. No-op when
  cuid/uid empty or uid already equal.
- `(*uidCache) SetLabel(cuid, label string)` — new. Trims surrounding
  whitespace and clamps to **24 runes**; an all-whitespace/empty result
  **clears** the label (entry keeps its uid). Preserves the existing uid.
  Creates the entry if the cuid is unknown (harmless orphan; reconciled on next
  list). Persists atomically.
- `(*uidCache) Backfill(accts []core.GameAccount)` — unchanged UID behavior,
  **plus** fills `accts[i].Label` from the cache for **every** account,
  including the active one. ⚠️ The current body `continue`s for accounts that
  already carry a UID (`account_handler.go:62`); the active account always has a
  UID, so the label fill must happen **before/around that `continue`** (e.g.
  one lookup at the top of the loop that sets `accts[i].Label` from the cache
  entry, then the existing UID record/backfill logic). Otherwise the active
  account — the chip's primary display — silently loses its label.
  ⚠️ `sync.Mutex` is **non-reentrant**: do the label lookup and the existing
  cache-backfill read in one critical section, and **release the lock before
  calling `Record`** (which re-locks) — never call `Record` while holding
  `c.mu`, or it deadlocks.

- `(*App) SetAccountLabel(gameID, accountID, label string) error` — new bound
  method. Mirrors `SwitchGameAccount`'s shape and capability gate: resolves the
  provider, requires `core.AccountSwitcher` (else `ErrAccountSwitchUnsupported`),
  then — guarding `a.uidCache != nil` exactly as `ListGameAccounts` does
  (`account_handler.go:93`) — calls `a.uidCache.SetLabel(accountID, label)`.
  `accountID` is the cuid. Returns `nil` on success. (Gating keeps the binding
  symmetric with List/Switch and rejects calls for games that can't switch.)

## Core change

`internal/core/provider.go` — add a `Label` field to `GameAccount`:
```go
type GameAccount struct {
    ID       string `json:"id"`
    UID      string `json:"uid"`
    Label    string `json:"label"`   // App-owned user label; providers leave empty
    Email    string `json:"email"`
    Username string `json:"username"`
    Active   bool   `json:"active"`
}
```
Doc comment notes: `Label` is set only by the App layer (from the uid cache);
providers never populate it (they hold no user-facing naming, only credentials
they must not expose).

## Frontend (`AccountChip.vue`)

Type gains `label: string`. Identity helper:
```ts
function primary(a: Account): string { return a.label || a.uid || a.email || a.username; }
```
Avatar initial: `(active?.label || active?.username || '?').slice(0,1)`.

**Row restructure (required — HTML validity):** the dropdown row is currently a
`<button class="account-opt">` (`AccountChip.vue:80`). A `<button>` may not
contain interactive content, and the rename UI nests a `✎` button **and** an
`<input>` inside the row — invalid content model; WebView2/Chromium will
reparse the nodes and focus/blur becomes unreliable (`@click.stop` does **not**
fix this — it's a DOM-validity issue, not event bubbling). Change the row to a
`<div class="account-opt" role="button" tabindex="0">` with `@click="pick(a)"`
and a `@keydown.enter.prevent`/`@keydown.space.prevent` → `pick(a)` for keyboard
parity. **These row keydown handlers must early-return when `editingId === a.id`**
(or be removed from the DOM in edit mode) so they don't fire while the user is
typing in the inline input — see D1 below. The `✎` button and `<input>` then
nest legally. Add a `:focus-visible` outline to `.account-opt` (the native
`<button>` had one; the div loses it otherwise — keyboard-focus a11y).

**Rename interaction (per dropdown row):**
- Edit state is a single `editingId = ref<string | null>(null)` (only one row
  edits at a time). A separate `draft = ref('')` holds the in-progress text.
- Each row shows a small rename affordance (a `✎` `<button>`,
  `data-test="account-rename-<id>"`, `@click.stop` → set `editingId=a.id`,
  `draft=a.label`).
- When `editingId === a.id`, the row renders an inline `<input>`
  (`data-test="account-rename-input-<id>"`, `:maxlength="24"`, `v-model="draft"`)
  in place of the static identity. **Focus** it on insert — `autofocus` does not
  work for a `v-if`-inserted node; use a small focus directive or
  `await nextTick(); el.focus()` after setting `editingId`. (Focus matters
  because blur is half the commit contract.)
- A single `commit(a)` function handles Enter and blur. **Commit-once guard
  (D2):** `commit` must early-return if `editingId.value !== a.id`. It reads
  `draft`, then clears `editingId`/`draft` **before** awaiting, calls
  `SetAccountLabel(gameId, a.id, value)`, then `load()`. Because Enter clears
  `editingId` first, the `v-if` removal of the input fires a second `blur` →
  `commit(a)` → but now `editingId !== a.id` → no-op. Without this guard the
  blur re-commit runs with `draft` already `''` and wipes the just-saved label.
- **Esc** clears `editingId`/`draft` without saving (also guarded so it doesn't
  trigger a blur-commit of the discarded draft).
- **Keydown isolation (D1):** the inline `<input>` must use `@keydown.stop`
  (Vue `.prevent` is preventDefault only — it does **not** stop propagation).
  Otherwise typing a space or pressing Enter bubbles to the row's
  `@keydown.space`/`@keydown.enter` → `pick(a)` → an unwanted switch. Bind
  `@keydown.enter.stop.prevent="commit(a)"` and `@keydown.esc.stop.prevent="cancel"`,
  and `@keydown.stop` on the input to swallow remaining keys (stop only — text
  entry is unaffected). The row keydown handlers are also edit-state-guarded
  (see Row restructure), giving belt-and-suspenders.
- Empty/whitespace input commits as `''` → clears the label (backend trims +
  clamps; the frontend `maxlength="24"` counts UTF-16 units, so the backend
  24-**rune** clamp is authoritative for supplementary-plane chars).
- The `✎` button and the `<input>` must `@click.stop` so they never trigger
  `pick()` (the switch) or the chip-root `@click="open=!open"`. Only a clean
  row click (not in edit mode) switches accounts.
- Outside-click while editing: blur fires and commits before `onDocClick`
  (`AccountChip.vue:52`) tears down the dropdown — acceptable; the edit is saved.
- No toast on rename (the inline change is its own feedback); keep it light.

i18n keys added to `en` / `zh-TW` / `zh-CN`:
- `account.rename` — rename button title / aria-label
- `account.namePlaceholder` — input placeholder (e.g. zh-TW「自訂名稱」)

## Wails binding regen

Adding `App.SetAccountLabel` requires regenerating the TS bindings
(`frontend/wailsjs/go/app/App.{js,d.ts}`) via `wails generate module` (or a
build). Frontend unit tests mock the module so they don't depend on regen, but
the dev/build path does — the plan includes this step.

## Test strategy

**Go (`internal/app/account_handler_test.go`):**
1. Legacy-format load: a file containing `{"537195734":"700727240"}` backfills
   UID correctly (regression — old caches keep working).
2. `SetLabel` persists; a fresh `loadUIDCache` then `Backfill` populates
   `GameAccount.Label`. The asserted account must be **active (non-empty UID)**
   — this pins the `continue`-branch fix (B2): the active account, whose label
   wins the primary display, must receive its label too.
3. `Record` preserves an existing label; `SetLabel` preserves the existing uid.
4. `SetLabel` with whitespace/empty clears the label; clamp truncates >24 runes.
5. `App.SetAccountLabel` on a provider without the capability returns
   `ErrAccountSwitchUnsupported` (capability gate).

**Frontend (`AccountChip.spec.ts`, mock gains `SetAccountLabel`; the test's
inline i18n `messages` gain `account.rename` + `account.namePlaceholder` to
avoid missing-key warnings):**
6. `primary` prefers label: an account with a label renders the label, not the
   uid.
7. Clicking `✎` reveals the input; typing + Enter calls
   `SetAccountLabel('kurogames/wutheringwaves', id, 'NewName')` then reloads.
8. Clicking `✎` / editing does **not** call `SwitchGameAccount`.
9. Empty input + Enter calls `SetAccountLabel(..., '')`.
10. Commit-once guard (D2): after Enter commits, a subsequent `blur` on the same
    row does **not** call `SetAccountLabel` a second time — assert the mock was
    called exactly once with the typed value (not a trailing `''`). (happy-dom
    won't auto-fire blur on `v-if` removal, so the test triggers blur manually
    to exercise the guard. Fire blur **before** `flushPromises` while the input
    is still mounted — a `v-if`-removed node can't be `.trigger`-ed; the guard's
    synchronous `editingId=null` makes the manual blur a no-op.)
11. Existing 4 tests stay green (no label → fallback to uid/email); the
    `button`→`div[role=button]` row swap keeps the `data-test="account-opt-<id>"`
    click path working.

## Files touched

- `internal/core/provider.go` — `GameAccount.Label` field + comment.
- `internal/app/account_handler.go` — `acctMeta` + custom unmarshal, `SetLabel`,
  `Record`/`Backfill` label-aware, `App.SetAccountLabel`.
- `internal/app/account_handler_test.go` — tests 1–5.
- `frontend/src/components/AccountChip.vue` — label display + rename UI.
- `frontend/src/components/__tests__/AccountChip.spec.ts` — tests 6–11 (+ mock).
- `frontend/src/locales/{en,zh-TW,zh-CN}.json` — 2 new keys.
- `frontend/wailsjs/go/app/App.{js,d.ts}` — regenerated.

## Risks / edge cases

- **Orphan labels:** `SetLabel` on an unknown cuid creates an entry; harmless,
  reconciled when the account next lists. Not pruned in v1.
- **Concurrent writes:** uidCache already serializes via its mutex; SetLabel
  takes the same lock.
- **cuid namespace:** label keys are kurogames cuids; safe while kurogames is
  the only switcher. A future second provider would need namespacing — noted,
  not built.
