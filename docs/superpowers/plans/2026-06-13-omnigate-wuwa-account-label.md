# WuWa AccountChip — User-Defined Labels Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development` to implement task-by-task. Each task is TDD (red → green → refactor) and **left uncommitted** until its review gate (`subagent-review-gates`) returns `APPROVE`; only then commit that task.

**Goal:** Let the user assign a local free-text label to each remembered WuWa account from the chip dropdown; display priority becomes `label > uid > email`.

**Spec:** `docs/superpowers/specs/2026-06-13-omnigate-wuwa-account-label-design.md` (APPROVED, 3-round opus gate). Read it — this plan assumes its decisions.

**Branch:** `feat/wuwa-account-switcher` (continuation). Commit convention: no `Co-Authored-By` trailer. Tests CGO-free → **never** pass `-race`.

**Tech:** Go, Wails, Vue 3 + vue-i18n, vitest.

---

## File map

- `internal/core/provider.go` — add `GameAccount.Label` (T1)
- `internal/app/account_handler.go` — `acctMeta` + custom unmarshal, `map[string]acctMeta`, `SetLabel`, label-aware `Record`/`Backfill`, `App.SetAccountLabel` (T1, T2)
- `internal/app/account_handler_test.go` — tests 1–5 (T1, T2)
- `frontend/src/components/AccountChip.vue` — label display (T3) + rename UI (T4)
- `frontend/src/components/__tests__/AccountChip.spec.ts` — tests 6–11 (T3, T4)
- `frontend/src/locales/{en,zh-TW,zh-CN}.json` — 2 keys (T4)
- `frontend/wailsjs/go/app/App.{js,d.ts}` — regenerated (T4)

---

## Task 1: Backend cache data model + label ops (Go)

**Files:** modify `internal/core/provider.go`, `internal/app/account_handler.go`; modify `internal/app/account_handler_test.go`.

Covers spec tests 1–4.

- [ ] **Step 1 — failing tests** in `account_handler_test.go`:
  - `TestUIDCache_LoadsLegacyStringFormat`: write `{"537195734":"700727240"}` to a temp file, `loadUIDCache`, `Backfill([]GameAccount{{ID:"537195734"}})` → UID == `700727240`. (regression)
  - `TestUIDCache_SetLabelPersistsAndBackfillsActive`: `SetLabel("537195734","主帳")`; reload; `Backfill` an **active** account `{ID:"537195734", UID:"700727240", Active:true}` → `.Label == "主帳"` (pins B2: active account, which has a UID, still gets its label).
  - `TestUIDCache_RecordPreservesLabel_SetLabelPreservesUID`: `Record(cuid,uid)` then `SetLabel(cuid,label)` then `Record(cuid,uid)` again → both uid and label intact.
  - `TestUIDCache_SetLabelTrimClampClear`: `"  x  "` → stored `"x"`; a >24-rune string → truncated to 24 runes; `"   "`/`""` → label cleared, uid kept.
- [ ] **Step 2 — run, verify red** (`go test ./internal/app/ -run TestUIDCache`). Expect compile failure (`acctMeta`/`SetLabel`/`GameAccount.Label` undefined).
- [ ] **Step 3 — `GameAccount.Label`** in `provider.go`: add `Label string `json:"label"`` between `UID` and `Email`; comment that it is App-populated, providers leave it empty.
- [ ] **Step 4 — `acctMeta`** in `account_handler.go`:
  ```go
  type acctMeta struct {
      UID   string `json:"uid"`
      Label string `json:"label,omitempty"`
  }
  func (m *acctMeta) UnmarshalJSON(b []byte) error {
      if len(b) > 0 && b[0] == '"' { // legacy string = bare UID
          var s string; if err := json.Unmarshal(b, &s); err != nil { return err }
          m.UID = s; return nil
      }
      type raw acctMeta; var r raw
      if err := json.Unmarshal(b, &r); err != nil { return err }
      *m = acctMeta(r); return nil
  }
  ```
  Change `uidCache.m` to `map[string]acctMeta`.
- [ ] **Step 5 — `Record`** read-modify-write: lock, `e := c.m[cuid]`, if `e.UID == uid` return; `e.UID = uid; c.m[cuid] = e`; persist. **Preserves `e.Label`.**
- [ ] **Step 6 — `SetLabel(cuid, label string)`**: `label = strings.TrimSpace(label)`; clamp to 24 runes (`[]rune` slice); lock; `e := c.m[cuid]; e.Label = label; c.m[cuid] = e`; persist atomically. (Empty label stored as `""` → `omitempty` drops it on write.)
- [ ] **Step 7 — `Backfill`** label-aware, **deadlock-safe** (mutex non-reentrant): for each account, under one lock acquisition read both the cache entry's label (always → `accts[i].Label`) and, when `accts[i].UID == ""`, its uid; **release the lock**, then for accounts that already had a UID call `Record` (which re-locks). Do **not** call `Record` while holding `c.mu`.
- [ ] **Step 8 — run green**, then `go build ./... && go test ./internal/app/ ./internal/core/`.
- [ ] **Step 9 — review gate** (uncommitted): dispatch spec-compliance + code-quality opus reviewers on the working-tree diff. Iterate to APPROVE. **Then commit** `feat(wuwa-switcher): cuid->{uid,label} cache + SetLabel`.

## Task 2: App.SetAccountLabel binding (Go)

**Files:** modify `internal/app/account_handler.go`, `internal/app/account_handler_test.go`. Covers spec test 5.

- [ ] **Step 1 — failing test** `TestSetAccountLabel_Unsupported`: build `App{}` and register the existing `noSwitchProvider` via `a.registerProvider(noSwitchProvider{})` (exactly as `TestListGameAccounts_Unsupported`, account_handler_test.go:41-42, so `a.provider(gid)` resolves) → `a.SetAccountLabel("fake/g","id","x")` returns `core.ErrAccountSwitchUnsupported`.
- [ ] **Step 2 — verify red.**
- [ ] **Step 3 — implement** `(*App) SetAccountLabel(gameID, accountID, label string) error`: resolve provider via `a.provider(gid)`; assert `core.AccountSwitcher` (else `ErrAccountSwitchUnsupported`); guard `if a.uidCache != nil { a.uidCache.SetLabel(accountID, label) }`; return nil. Mirror `SwitchGameAccount`'s structure (`account_handler.go:99`).
- [ ] **Step 4 — green**, `go test ./internal/app/`.
- [ ] **Step 5 — review gate** → APPROVE → commit `feat(wuwa-switcher): App.SetAccountLabel binding + capability gate`.

## Task 3: Frontend label display priority (Vue)

**Files:** modify `frontend/src/components/AccountChip.vue`, `frontend/src/components/__tests__/AccountChip.spec.ts`. Covers spec tests 6, 11.

- [ ] **Step 1 — failing tests:**
  - test 6 `primary prefers label`: list an account with `label:'主帳', uid:'700727240', active:true` → `w.text()` contains `主帳`. (Existing mock rows gain `label:''`.)
  - test 11 (adapt existing): the `button`→`div` row swap keeps `data-test="account-opt-<id>"` click switching working — the existing "calls SwitchGameAccount" test must still pass after the swap.
- [ ] **Step 2 — verify red** (`npx vitest run AccountChip`): test 6 fails (label not preferred).
- [ ] **Step 3 — implement:** extend `Account` type with `label: string`; `primary(a) = a.label || a.uid || a.email || a.username`; avatar initial `(active?.label || active?.username || '?').slice(0,1)`. Convert the dropdown row `<button class="account-opt">` to `<div class="account-opt" role="button" tabindex="0" @click="pick(a)" @keydown.enter.prevent="pick(a)" @keydown.space.prevent="pick(a)">` (keydown handlers edit-state-guarded — but no edit mode yet in T3, so a plain guard placeholder is fine; T4 wires editing). Add `.account-opt:focus-visible` outline in `<style>`.
- [ ] **Step 4 — green**; ensure the 4 pre-existing tests still pass.
- [ ] **Step 5 — review gate** → APPROVE → commit `feat(wuwa-switcher): chip shows label > uid > email`.

## Task 4: Frontend inline rename UI (Vue) + i18n + binding regen

**Files:** modify `AccountChip.vue`, `AccountChip.spec.ts`, `frontend/src/locales/{en,zh-TW,zh-CN}.json`, regenerate `frontend/wailsjs/go/app/App.{js,d.ts}`. Covers spec tests 7–10.

- [ ] **Step 1 — failing tests** (mock gains `SetAccountLabel`; test i18n `messages` gain `account.rename` + `account.namePlaceholder`):
  - test 7: click `[data-test="account-rename-<id>"]` → input appears; set value, trigger `keydown.enter` → `SetAccountLabel('kurogames/wutheringwaves', id, 'NewName')` called, then reload.
  - test 8: clicking `✎` / typing does **not** call `SwitchGameAccount`.
  - test 9: empty input + Enter → `SetAccountLabel(..., '')`.
  - test 10 (commit-once / D2): after Enter, manually trigger `blur` on the input **before** `flushPromises` → `SetAccountLabel` called exactly once with the typed value (not a trailing `''`).
- [ ] **Step 2 — verify red.**
- [ ] **Step 3 — implement** per spec §Frontend:
  - `editingId = ref<string|null>(null)`, `draft = ref('')`.
  - `✎` `<button data-test="account-rename-<id>" @click.stop>` → `editingId=a.id; draft=a.label`; focus the input after `nextTick()` (focus directive or template ref).
  - When `editingId===a.id` render `<input data-test="account-rename-input-<id>" v-model="draft" :maxlength="24" @click.stop @keydown.stop @keydown.enter.stop.prevent="commit(a)" @keydown.esc.stop.prevent="cancel" @blur="commit(a)">`.
  - `commit(a)`: `if (editingId.value !== a.id) return;` capture `const v = draft.value`; set `editingId.value=null; draft.value=''`; `await SetAccountLabel(props.gameId, a.id, v); await load();`.
  - `cancel()`: `editingId.value=null; draft.value=''`.
  - Edit-state-guard the row's `@keydown.enter/space` so they no-op while `editingId===a.id`.
- [ ] **Step 4 — i18n:** add `account.rename` + `account.namePlaceholder` to all three locales (zh-TW「重新命名」/「自訂名稱」; zh-CN 简体; en "Rename"/"Custom name").
- [ ] **Step 5 — regen bindings:** `wails generate module` (updates `frontend/wailsjs/go/app/App.{js,d.ts}` with `SetAccountLabel`).
- [ ] **Step 6 — green** (`npx vitest run` full) + `npm run build` (frontend typecheck).
- [ ] **Step 7 — review gate** → APPROVE → commit `feat(wuwa-switcher): inline account rename in chip dropdown`.

---

## Exit

After T4's gate APPROVE + commit: full `go test ./...` (no `-race`) + `npx vitest run` green, `go build ./...` + `npm run build` clean. Real-machine e2e (rename an account, confirm label persists + shows as primary, switch still works) is a USER step. Then this feature joins the rest of `feat/wuwa-account-switcher`; merge to dev is a separate user-gated decision (per existing memory).

## Out of scope (from spec)

No login/network/server-nickname; no other-provider labels; no custom avatar image; orphan labels not pruned in v1.
