# Spec — 花費改顯示「消耗石頭數」 + 隱藏無紀錄的池

**Date:** 2026-06-05 · **Branch:** gacha-ui · **Status:** DRAFT — pending subagent spec review.
Small companion to the GachaBoard redesign; will be planned/implemented together with the refresh-progress feature in one wave.

## A. 花費卡：NTD 估算 → 消耗石頭數（精確）

Today the spend card shows an NTD estimate: `SpendEst = nonFree × cfg.PullPrice`, `Currency = "NT$"` (HoYo 100 / WuWa 160 / Endfield 100). The user wants the **exact premium-currency consumed**: every pull costs 160 premium currency for HoYo (原神/星穹/絕區零) and WuWa; free pulls (`IsFree`) don't cost currency. This is exact, not an estimate.

### Backend (per-provider GachaConfig)
- **hoyoverse restructuring (REQUIRED — Currency is currently shared, not per-gid):** `hoyoverse/gacha.go:180` sets `cfg := core.GachaConfig{... PullPrice:100, Currency:"NT$" ...}` ONCE before the `switch gid`, and the switch only assigns `cfg.Banners`. To give each game its own stone code, change the shared literal to `PullPrice:160` (same for all three) and **assign `cfg.Currency` inside each `case`**: genshin `"primogem"`, starrail `"stellar_jade"`, zzz `"polychrome"`. (Drop `Currency:"NT$"` from the shared literal, or leave a harmless default that every case overwrites.)
- kurogames (WuWa) `gacha.go:106`: single literal → `PullPrice:160` (already 160), `Currency:"astrite"`.
- Endfield (hypergryph): **flagged placeholder** — `endfieldPullPrice` const → `160` (update its `(placeholder unit, NT$)` comment), `Currency:"endfield_pull"` (was "NT$"), pending the user's real per-pull cost + currency name (Endfield fetch is currently broken anyway). Mark with a code comment + memory note.
- `SpendEst = nonFree × PullPrice` is unchanged in formula; it now means "premium currency consumed on paid pulls". `IsFree` exclusion stays (Endfield free pulls / any free pull cost 0).
- The summary's `Currency` now carries a **code**, not a display symbol. Frontend localizes it.

### Frontend (spend card) — MANDATORY markup rewrite
- The current card sub-line (`GachaBoard.vue` ~line 122) is `{{ t('gacha.approx') }} {{ sum.currency }}{{ nf(sum.spendEst) }}` — it treats `sum.currency` as a **prefix symbol** ("NT$1,440"). Once `currency` is a code, this would render `~primogem1,440` (garbage). It **must** be rewritten: localize the code via `gacha.currency.<code>` (fallback raw code), drop `approx`, show as `{nf(spendEst)} {currencyName}` (e.g. "1,440 原石"); main stays `compact(spendEst)`.
- New label "消耗" (Consumed), no "估算". `approx` key is left in all 3 locales as an unused/dead key (removing it from only one breaks i18n parity; leaving it is harmless).
- i18n `gacha.currency.*` codes: `primogem`/`stellar_jade`/`polychrome`/`astrite` (+ `endfield_pull` placeholder), values per locale:
  - primogem: 原石 / 原石 / Primogems
  - stellar_jade: 星瓊 / 星琼 / Stellar Jade
  - polychrome: 菲林 / 菲林 / Polychrome
  - astrite: 星聲 / 星声 / Astrite
  - endfield_pull: 尋訪 / 寻访 / Pulls (placeholder, flagged)
- Relabel `spend_est`: "消耗" / "消耗" / "Consumed". The `spend_est_note`/估算 wording is dropped.

## B. 隱藏無紀錄的池（保底進度）

The 保底進度 section currently lists **every** configured banner (`summary.pity[]` is built for all `cfg.Banners`), including banners the account never pulled on (current=0, cap=N). The user wants pools with **no records** hidden.

- **Frontend filter** (non-breaking, no backend change): render only pity rows whose banner has records, i.e. `(summary.perBanner[b.key] ?? 0) > 0`. `perBanner` counts ALL pulls per banner key (matches banner keys), so `>0` ⇒ the account pulled on that banner. A banner with pulls but trailing pity 0 still shows (records exist).
- The card-1 banner split + headline-type split already filter zero counts (no change).
- Edge: if a populated summary has pulls only on banners not present in `cfg.Banners` (shouldn't happen — keys come from the same config), those simply don't appear; acceptable.
- If, after filtering, zero pity rows remain (account has pulls but none map to a configured banner — degenerate), the 保底進度 panel renders its title with no rows; acceptable (not the empty-state, since totalPulls>0).

## Acceptance criteria
- AC1. Backend: each of the 4 known games' `GachaConfig` has `PullPrice == 160` and a stone `Currency` code; `ComputeSummary` SpendEst = nonFree×160. A Go test asserts SpendEst for a fixture (e.g. 10 pulls, 1 free → 9×160=1440) and that Currency is the expected code.
- AC2. Frontend spend card: shows the localized currency name (via `gacha.currency.<code>`) and the exact consumed amount; no "估算"/approx wording. Tested: a summary with `currency:'primogem'`, spendEst 1440 → card text contains the localized name and 1,440.
- AC3. Currency i18n map present in all 3 locales (parity test green); unknown code falls back to the raw code (no crash).
- AC4. Pity section hides banners with `perBanner[key]` absent/0; shows banners with >0. Tested: a summary whose `pity` has banner A (perBanner 5) and banner B (perBanner 0) renders only A's pity row.
- AC5. Existing tests updated for the changed Currency/PullPrice (any test asserting `Currency=='NT$'` or NTD spend must be updated). `go test ./...`, vitest, `wails build` green.

## Non-goals
- No real Endfield number (placeholder, flagged) — user to provide; Endfield fetch is broken regardless.
- No NTD display, no top-up package modelling.
- No change to pity computation (filtering is display-only).

## Risks / breaking-tests reality (verified)
- Provider `gacha_test.go` (hoyoverse/kurogames/hypergryph): grep shows **none assert `Currency` or `PullPrice`** → the 100→160 + code change does NOT break them. (No update needed; just don't assume otherwise.)
- `internal/core/gacha_stats_test.go` uses a LOCAL `testConfig()` with its own `PullPrice:100` and asserts `SpendEst==600/100` — **independent of production**, stays green. AC1 ADDS a new fixture asserting 9×160 (or keep testConfig and assert via its own price); don't conflate.
- `internal/app/gacha_test.go` fake provider returns `Currency:"NT$"` as an unasserted fixture → won't break; scan to be safe.
- `frontend/.../GachaBoard.spec.ts` `base.currency:'NT$'`: no current test asserts spend-card text, so only the NEW AC2 test needs a `primogem` fixture; update `base` for clarity.
- Currency-as-code is a semantic change to `GachaSummary.Currency`; verified only the spend card + the typed interface consume it — no logic branches on the value.
