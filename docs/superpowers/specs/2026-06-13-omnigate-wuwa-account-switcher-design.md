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

### 4.2 kurogames provider implementation

**Account source — KRSDK user cache (read-only for listing):**
- Locate via glob `%APPDATA%\KR_G153\*\KRSDKUserCache.json` (the `*` is the
  channel code, e.g. `A1730`; glob avoids hardcoding it). Pick the single match;
  if multiple, the newest by mtime.
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

**Game UID enrichment (local, no API):**
- The active account's game UID = `RecentlyLoginUID` in
  `<installDir>\Client\Saved\LocalStorage\LocalStorage.db` (SQLite, table
  `LocalStorage(key,value)`). Same install-dir base the gacha provider already
  uses for `Client\Saved\Logs\Client.log`.
- On each `ListAccounts`, record `active.cuid → RecentlyLoginUID` into a small
  omnigate sidecar `wuwa_uid_cache.json` (next to settings, like
  `playstate.json`). Fill non-active accounts' `UID` from this cache; leave ""
  if never seen active. **Cache holds only numbers — no tokens, not sensitive.**
- **Correctness guard:** only record the mapping when `LocalStorage.db`'s mtime
  is **newer than** `KRSDKUserCache.json`'s mtime — i.e. the game has actually
  run since the last login-pointer change, so `RecentlyLoginUID` provably belongs
  to the current `last_login_cuid`. Otherwise (we just switched but the game
  hasn't launched yet) the DB still holds the *previous* account's UID; skip
  recording and serve the active account's UID from cache (may be "" until first
  launch). This prevents mis-mapping a cuid to the wrong UID in the
  switch-before-launch window.

**Switch operation `SwitchAccount(gid, cuid)`:**
1. **Game-closed gate** — abort with a typed `ErrGameRunning` if any of
   `Client-Win64-Shipping`, `Wuthering Waves`, `KRSDKExternal` is running.
2. Read the JSON file as raw text. Replace **only** the `last_login_cuid` value
   (quoted-string aware regex: `("last_login_cuid"\s*:\s*")\d+(")`); never touch
   tokens or `account_list` (whose entries also contain the same cuid).
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
- Resolve provider; if it does not implement `core.AccountSwitcher`, return a
  typed "capability not supported" so the frontend simply omits the chip.
- Wire into the Wails `Bind` list.

### 4.4 Frontend

- New component `AccountChip.vue`, mounted at the **right end of `NavStrip.vue`**
  (tabs stay left; chip pushed right via `margin-left:auto`).
- Chip: avatar (initial circle) + **primary = UID** (active account always has
  one; fall back to email when UID unknown) + **secondary = email** + `▾`.
- Dropdown: each remembered account as a row (UID-or-email primary, email
  secondary), `✓` on the active one; a trailing hint row "＋ 在遊戲登入新帳號…".
  Selecting a non-active row → `SwitchGameAccount` → toast「已切到 X，啟動遊戲生效」.
- Chip only renders for games whose backend reports the capability (WuWa in v1).
- Single-account case: chip still shows the one account; dropdown shows only the
  hint row beyond it.

## 5. Error handling

| Condition | Behavior |
|---|---|
| KRSDK cache file absent (never logged in) | `ListAccounts` → empty; frontend hides chip |
| Game running on switch | `ErrGameRunning` → toast「請先關閉遊戲再切換」 |
| Malformed JSON / pattern not found | typed error → toast; **no write performed** |
| `LocalStorage.db` unreadable / locked | UID enrichment skipped (UID stays ""); listing still works |

## 6. Security & privacy

- Omnigate **never reads or stores account tokens**; it parses the JSON for
  cuid/email/username only and rewrites a single non-secret pointer field.
- The only omnigate-written state is `wuwa_uid_cache.json` (cuid→UID numbers).
- Email is the user's own and shown only as the chip's secondary line.
- The KRSDK file is mutated only with the game closed, atomically, with a backup.

## 7. Testing (TDD)

Pure, fixture-driven units:
- Parse `account_list` + `last_login_cuid` → `[]GameAccount` with correct `Active`.
- Flip `last_login_cuid` on a fixture JSON: target value changed, **tokens and
  account_list cuids byte-for-byte unchanged**, output BOM-free, valid JSON.
- Glob/locate the cache file (temp dir fixtures; multiple channel dirs → newest).
- cuid→UID cache: enrich active from a fixture `LocalStorage.db`; persist/reload.
- Process-gate: inject a fake process-detector; running → `ErrGameRunning`, no write.

Frontend: `AccountChip` renders primary/secondary correctly; hidden when
capability absent; switch calls the binding and shows the toast.

## 8. Out of scope (explicit follow-ups)

1. Kuro player-info API → in-game nickname + UID for all accounts.
2. HoYoverse / Endfield switching (backup/restore credential-vault model).
3. Custom per-account nicknames.
