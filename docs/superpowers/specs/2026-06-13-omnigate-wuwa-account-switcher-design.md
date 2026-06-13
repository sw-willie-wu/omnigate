# Omnigate — WuWa (鳴潮) Account Switcher — Design

**Date:** 2026-06-13
**Status:** Approved (design), pending implementation plan
**Scope:** WuWa-first, via an extensible optional provider capability

---

## 1. Motivation

Players with multiple Kuro accounts switch between them through the in-game
KRSDK login screen. The login screen can switch passwordlessly because KRSDK
keeps **all remembered accounts and their tokens in a local file**. Omnigate can
surface the same switch from the launcher home screen: list the remembered
accounts and flip which one logs in next — without ever storing a credential
itself.

This was reverse-engineered on a real machine (2026-06-13): swapping the game's
`LocalStorage.db`/`DeviceStorage.db` did **not** change the account, but flipping
one field in the KRSDK user-cache JSON did — verified by launching the game and
landing in the other account with no password.

## 2. The account-model asymmetry (why WuWa-first)

| | **WuWa (KRSDK)** | **HoYoverse / Endfield** |
|---|---|---|
| Local store | KRSDK keeps a **multi-account list + per-account token** | Only the **current** account's credential blob; no list |
| Account source | Read KRSDK's own file (free) | No list to read — one account at a time |
| Switch = | Repoint `last_login_cuid` | Backup/restore: omnigate must save each account's blob itself |
| Omnigate stores tokens? | **No** (only points) | **Yes** (becomes a credential vault → encryption, explicit capture) |

The HoYo/Endfield "backup/restore" model is a fundamentally different, heavier,
credential-storing feature. `AccountSwitcher` is therefore an **optional
per-provider capability**: only `kurogames` implements it in v1, so **only WuWa
shows the account chip**. The interface accommodates both models (where accounts
come from, and what "switch" means, are provider-internal), so HoYo/Endfield can
adopt it later without an interface change.

## 3. Goals / Non-goals

**Goals**
- List the KRSDK-remembered WuWa accounts and show which is active.
- Switch the active account by flipping `last_login_cuid` (game closed).
- Omnigate stores **no tokens**.
- Show **game UID (primary) + email (secondary)** per account.

**Non-goals (follow-ups)**
- In-game **nickname** and **UID for never-yet-active accounts** — both require a
  Kuro player-info API (token use + endpoint research). Deferred.
- HoYoverse / Endfield account switching (backup/restore model).
- Custom account nicknames.

## 4. Architecture

Follows the existing optional-capability-interface pattern (cf. `NewsProvider`,
`InstallLocator`, `LastPlayedProbe` in `internal/core/provider.go`).

### 4.1 Core — new optional capability

```go
// internal/core/provider.go
type GameAccount struct {
    ID       string // KRSDK cuid — stable opaque key used by SwitchAccount
    UID      string // in-game UID; "" when not yet known
    Email    string
    Username string // e.g. U547195734A
    Active   bool   // currently last_login_cuid
}

// AccountSwitcher is an optional Provider capability: list the launcher-
// remembered accounts for a game and switch which one logs in next. The
// provider never exposes credentials; switching mutates only the publisher's
// own login-pointer state. Implementations require the game to be closed.
type AccountSwitcher interface {
    ListAccounts(ctx context.Context, gid GameID) ([]GameAccount, error)
    SwitchAccount(ctx context.Context, gid GameID, accountID string) error // accountID = GameAccount.ID (cuid)
}
```

**Type handling note:** in the JSON, `cuid` is a number (`537195734`), `id` a
float (`537195734.0`), and `last_login_cuid` a quoted string. `GameAccount.ID`
is the canonical string form of `cuid` (via `strconv`). `SwitchAccount` MUST
validate that `accountID` is all digits before substituting it into the
`last_login_cuid` regex — the target is a token-bearing file, so a malformed id
must error out, never get written.

### 4.2 kurogames provider implementation

**Account source — KRSDK user cache (read-only for listing):**
- Locate via glob `%APPDATA%\KR_G153\*\KRSDKUserCache.json` (the `*` is the
  channel code, e.g. `A1730`; glob avoids hardcoding it). Pick the single match;
  if multiple, the newest by mtime. (Note: distinct channel dirs can be different
  account sets, so "newest" is a best-effort tie-break — v1 assumes one match in
  practice, which holds for the standard global client.)
- Shape:
  ```json
  { "account_list": [
      { "cuid": 537195734, "email": "...", "username": "U547195734A",
        "token": "<~78 chars>", "code": "<~36>", "id": 537195734.0,
        "loginType": 13, "thirdNickName": "" }, … ],
    "last_login_cuid": "537195734" }   // NOTE: quoted string
  ```
- Map each `account_list` entry → `GameAccount{ID:cuid, Email, Username}`. Mark
  `Active` where `cuid == last_login_cuid`. **The `token` field is never read
  into memory.**

**Game UID enrichment — layer split (provider reads, App persists):**
The persistent cuid→UID cache lives in the **App layer**, not the provider: the
"sidecar next to settings" convention is App knowledge (`settingsP` +
`playStatePathFor`/`gachaDBPathFor` in `internal/app/`), and the kurogames
`Provider` has no settings-dir seam (its only injected path is `tempRootFn`). So:
- **Provider** (`ListAccounts`) fills only the **active** account's `UID`, read
  from `RecentlyLoginUID` in `<installDir>\Client\Saved\LocalStorage\LocalStorage.db`
  (SQLite, table `LocalStorage(key,value)`; via `modernc.org/sqlite`, the
  CGO-free driver already used in `internal/store/sqlite.go`). Same install-dir
  base the gacha provider uses for `Client\Saved\Logs\Client.log`. Non-active
  accounts get `UID:""`. The provider persists nothing.
- **Provider correctness guard:** report the active UID only when
  `LocalStorage.db`'s mtime is **newer than** `KRSDKUserCache.json`'s mtime — the
  game has run since the last login-pointer change, so `RecentlyLoginUID` provably
  belongs to the current `last_login_cuid`. Otherwise (just switched, game not yet
  launched) the DB holds the *previous* account's UID → return `UID:""` for the
  active account rather than a wrong value.
- **App** owns the persistent map `wuwa_uid_cache.json` (next to settings, like
  `playstate.json`; numbers only — no tokens, not sensitive). On each
  `ListGameAccounts`: if the provider returned a non-empty active UID, record
  `active.cuid → UID`; then back-fill every account's `UID` from the cache.
- **Acknowledged degradations** (not mis-maps): (a) KRSDK rewriting
  `KRSDKUserCache.json` on token refresh can bump its mtime above the DB for the
  *same* account, making the guard skip a valid refresh (UID under-fills / serves
  the still-correct cached value — a false negative); (b) one cuid can map to
  multiple in-game UIDs across servers — `RecentlyLoginUID` is the most-recent
  one, which is what we show. Both are acceptable for v1.

**Switch operation `SwitchAccount(gid, cuid)`:**
1. **Game-closed gate** — abort with a typed `ErrGameRunning` if any of
   `Wuthering Waves.exe`, `Client-Win64-Shipping.exe`, `KRSDKExternal.exe` is
   running. The existing `IsGameRunning` only checks the single registered
   `meta.ExeName` (`"Wuthering Waves.exe"`) via an exact `EqualFold` match
   (`process_check_windows.go`); this needs a **new multi-name detector helper**
   (`.exe`-suffixed exact names) plus an **injectable seam** on the provider — a
   `procRunningFn` field mirroring the existing `convLogPathsFn` injection — so
   the gate is unit-testable with a fake detector.
2. Read the JSON file as raw text. Validate `accountID` is all digits (see §4.1),
   then replace **only** the `last_login_cuid` value (quoted-string aware regex:
   `("last_login_cuid"\s*:\s*")\d+(")`); never touch tokens or `account_list`
   (whose entries also contain the same cuid). Optionally assert `accountID`
   exists in `account_list` before writing.
3. **Atomic write**: write a temp file then rename over the original. Keep a
   one-time `.omnigate-bak` of the original (this file holds login tokens —
   corrupting it forces a full re-login, so a backup is warranted).
4. Encoding: **UTF-8 without BOM** (PowerShell-era lesson: a BOM breaks KRSDK's
   parser). In Go, `os.WriteFile` of UTF-8 bytes is already BOM-free; the design
   note exists so no future change introduces one.

### 4.3 App layer

```go
// internal/app/  (new account_handler.go, mirroring gacha.go style)
func (a *App) ListGameAccounts(gameID string) ([]core.GameAccount, error)
func (a *App) SwitchGameAccount(gameID, accountID string) error
```
- Resolve the provider via the existing `a.provider(gid)`; type-assert
  `p.(core.AccountSwitcher)` exactly as `RefreshGacha` does `p.(core.GachaProvider)`
  in `internal/app/gacha.go`. If it does not implement the capability, return a
  typed `ErrAccountSwitchUnsupported` so the frontend simply omits the chip.
- `ListGameAccounts` calls `provider.ListAccounts`, then applies the App-owned
  cuid→UID cache (record active, back-fill all — see §4.2).
- **No Bind edit needed:** `main.go` binds `Bind: []interface{}{a}`, so all
  exported `App` methods are auto-exposed; only `wailsjs` bindings need
  regenerating.

### 4.4 Frontend

- New component `AccountChip.vue`, mounted at the **right end of `NavStrip.vue`**
  (tabs stay left; chip pushed right via `margin-left:auto`).
- Chip: avatar (initial circle) + **primary = UID** (active account always has
  one; fall back to email when UID unknown) + **secondary = email** + `▾`.
- Dropdown: each remembered account as a row (UID-or-email primary, email
  secondary), `✓` on the active one; a trailing hint row "＋ 在遊戲登入新帳號…".
  Selecting a non-active row → `SwitchGameAccount` → toast「已切到 X，啟動遊戲生效」.
- The chip reads the active game id from the games store (`stores/games.ts`
  exposes `selectedID` / `selected`); `NavStrip.vue` currently imports only
  `useViewStore` (home tab) and has no game context, so the games store must be
  wired into the chip.
- Capability-gating is data-driven: the chip calls `ListGameAccounts(gid)` and
  renders only when it returns accounts; `ErrAccountSwitchUnsupported` (or empty)
  → hide the chip. No per-backend frontend conditional needed (WuWa is the only
  one that returns accounts in v1).
- Single-account case: chip still shows the one account; dropdown shows only the
  hint row beyond it.

## 5. Error handling

New typed errors `ErrGameRunning` and `ErrAccountSwitchUnsupported` go in
`internal/core/errors.go` (sentinel `var` block) and its `core.ErrorCode`
switch. Surfacing a stable code to the frontend touches two further sites:
`App.ErrorCode` (a hardcoded sentinel **loop** in `app.go`, which already omits
some errors — add ours) and `App.ErrorMessage` (switch).
**Reality check (don't assume infra that isn't there):** today nothing in
`frontend/src` calls `ErrorCode`/`ErrorMessage`, and `pushToast`
(`composables/useToast.ts`) has zero callers — there is no existing error→toast
pipeline to "key off." The chip therefore owns its own error detection (match on
the `ErrorCode` string, or on the error text) and its own **localized** toast
(via the i18n strings below). Extending `App.ErrorMessage` is optional/cosmetic
since it is English-only ("M2 ships English only") and the chip localizes itself.

| Condition | Behavior |
|---|---|
| KRSDK cache file absent (never logged in) | `ListAccounts` → empty; frontend hides chip |
| Backend lacks the capability | `ErrAccountSwitchUnsupported` → chip hidden |
| Game running on switch | `ErrGameRunning` → toast「請先關閉遊戲再切換」 |
| Malformed JSON / pattern not found / non-digit accountID | typed error → toast; **no write performed** |
| `LocalStorage.db` unreadable / locked | UID enrichment skipped (UID stays ""); listing still works |

## 6. Security & privacy

- Omnigate **never reads or stores account tokens**; it parses the JSON for
  cuid/email/username only and rewrites a single non-secret pointer field.
- The only omnigate-written state is `wuwa_uid_cache.json` (cuid→UID numbers).
- Email is the user's own and shown only as the chip's secondary line.
- The KRSDK file is mutated only with the game closed, atomically, with a backup.

## 7. Testing (TDD)

Required seams (mirroring the existing `convLogPathsFn` injection on the kuro
`Provider`): an injectable `procRunningFn` for the process gate, and injectable
path resolvers for the KRSDK cache file and `LocalStorage.db` so units run
against temp-dir fixtures. These seams are part of the implementation, not just
the tests.

Provider, pure/fixture-driven units:
- Parse `account_list` + `last_login_cuid` → `[]GameAccount` with correct `Active`
  (incl. `cuid` number→string, quoted `last_login_cuid`).
- Flip `last_login_cuid` on a fixture JSON: target value changed, **tokens and
  account_list cuids byte-for-byte unchanged**, output BOM-free, valid JSON;
  non-digit `accountID` → error, no write.
- Glob/locate the cache file (temp dir fixtures; multiple channel dirs → newest).
- Active-UID read from a fixture `LocalStorage.db` + mtime guard: DB newer →
  UID filled; cache file newer (switch-before-launch) → `UID:""`.
- Process-gate: inject a fake `procRunningFn`; running → `ErrGameRunning`, no write.

App-layer units (App owns the persistent cache):
- cuid→UID cache: record active, back-fill others, persist/reload `wuwa_uid_cache.json`.
- `ListGameAccounts` returns `ErrAccountSwitchUnsupported` for a non-capable backend.

Frontend: `AccountChip` renders primary/secondary correctly; hidden when
capability absent; switch calls the binding and shows the toast.

## 8. Out of scope (explicit follow-ups)

1. Kuro player-info API → in-game nickname + UID for all accounts.
2. HoYoverse / Endfield switching (backup/restore credential-vault model).
3. Custom per-account nicknames.
