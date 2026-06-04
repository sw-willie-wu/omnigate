# Spec — 抽卡 refresh 進度指示（spinner + 哪個池/第幾頁）

**Date:** 2026-06-05 · **Branch:** gacha-ui · **Status:** DRAFT — pending subagent spec review.

## Background / goal

`RefreshGacha` paginates the record API per banner/pool (HoYoverse: per `gacha_type`, multi-page by `end_id`; WuWa: one POST per pool 1–7; Endfield: per pool, multi-page by `seq_id`). This can take many seconds, during which `GachaBoard` shows only a static skeleton. The user wants a **spinner + live text** ("which pool · which page") while fetching.

`GetGachaSummary` (initial load) is store-only and fast → no per-page progress; its skeleton/spinner stays generic.

## Approach (low blast radius — context-injected reporter)

Do **not** change the `core.GachaProvider.FetchGacha` signature (3 impls + app + many test call sites). Instead carry an optional progress reporter on the `context.Context`:

- `core`: `type GachaProgress struct { BannerKey string; Page int; PoolIndex int; PoolTotal int }`; `func WithGachaProgress(ctx, func(GachaProgress)) context.Context`; `func ReportGachaProgress(ctx, GachaProgress)` (no-op when no reporter is attached).
- Each provider calls `core.ReportGachaProgress(ctx, …)` at the **start of each page fetch** inside its existing loop. `ctx` is already threaded everywhere. Existing tests (which never attach a reporter) are unaffected — the call no-ops.
- `app.RefreshGacha`: `ctx = core.WithGachaProgress(ctx, onProgress)` before `FetchGacha`. `onProgress` resolves the banner KEY → localized label from `gp.GachaConfig(gid).Banners` and emits a Wails event `gacha:progress` with `(gameID, payload)`, mirroring the existing `update:changed` emit pattern (`app.go:75` emit helper, `update_state.go` usage).
- Frontend `stores/gacha.ts`: subscribe via `EventsOn('gacha:progress', …)` (mirror `stores/updates.ts:102` `bind()` pattern), routing by `gameID` into `byGid[gid].progress`. `GachaBoard` loading state renders a CSS spinner + localized text from `progress`.

## Details

### core (gacha progress reporter)
- `GachaProgress{ BannerKey string; Page int; PoolIndex int; PoolTotal int }` — PoolIndex/PoolTotal are 1-based pool position + count (for "(2/4)"); Page is 1-based page within the pool.
- `WithGachaProgress(ctx, fn)` stores `fn` under a private context key; `ReportGachaProgress(ctx, p)` looks it up and calls it if present (nil-safe both ways).
- Pure, no Wails dependency in `core` (keeps core import-clean).

### providers (emit per page) — banner key + pool position
- **hoyoverse** `fetchHoyoGacha`: outer loop over `gachaTypesToQuery[gid]` (PoolTotal = len, PoolIndex = i+1), inner page loop → `ReportGachaProgress(ctx, {BannerKey: bannerForGachaType(gid, gt), Page: page, PoolIndex, PoolTotal})` at the top of each page iteration. (Probes in `selectAuthCandidate` are NOT reported — only real pagination.)
- **kurogames** `fetchWuwa`: loop `pool := 1..7` (PoolTotal=7, PoolIndex=pool), one request per pool → report once per pool with `Page:1`, `BannerKey: poolBanner(pool)`.
- **hypergryph** `fetchEndfield`: loop `endfieldPools` (PoolTotal=len, PoolIndex=i+1), inner page loop → report per page, `BannerKey: pool.bannerKey`.
- Reporting happens AFTER the `ctx.Err()` check, BEFORE the HTTP call, so the UI shows the page about to be fetched.

### app (resolve label + emit)
- Add `(a *App) emit(name string, args ...any)` method (extract the closure currently inline in `New`, reuse for both the update registry and gacha) OR a local closure in `RefreshGacha`; emit only when `a.ctx != nil`.
- `onProgress(p core.GachaProgress)`: find the banner in `gp.GachaConfig(gid).Banners` whose `Key == p.BannerKey` → its `Label` (LocalizedString); fallback to a map with just the raw key. Emit `gacha:progress` with `gameID` + payload `{ banner: LocalizedString, page, poolIndex, poolTotal }`.
- The label is a `LocalizedString` (map) so the frontend localizes with its existing `localize()` (UI-locale aware); the app does not need to know the UI locale.
- Token safety unchanged: progress carries only banner/page/counts — never the URL/authkey.

### frontend
- `stores/gacha.ts`: extend `GachaState` with `progress: GachaProgress | null` where `GachaProgress = { banner: Record<string,string>; page: number; poolIndex: number; poolTotal: number }`. Add `bind()` (idempotent) that calls `EventsOn('gacha:progress', (gid, p) => { const s = this.byGid[gid]; if (s) s.progress = p; })`. `refresh(gid)` sets `progress=null` at start and clears it (`progress=null`) on success/error. `load()` leaves progress null.
- `App.vue` `onMounted`: call `gacha.bind()` once (next to `updates.bind()`).
- `GachaBoard.vue` loading branch: when `st.loading`, render a **spinner** + text. Text = `st.progress ? t('gacha.progress', { banner: localize(progress.banner), page, pool: poolIndex, total: poolTotal }) : t('gacha.loading')`. Keep the skeleton beneath/around or replace — design: spinner + text line ABOVE the existing skeleton (skeleton remains for structure).

### i18n (en/zh-TW/zh-CN parity)
- `gacha.progress` e.g. zh-TW "{banner} · 第 {page} 頁（{pool}/{total}）", en "{banner} · page {page} ({pool}/{total})". (`loading` already exists.)

## Acceptance criteria

- AC1. `core.WithGachaProgress`/`ReportGachaProgress` round-trip: a reporter attached via ctx is invoked by `ReportGachaProgress`; with no reporter it no-ops (no panic). Unit test in `core`.
- AC2. Each provider reports progress during real pagination: a provider test attaches a reporter via `WithGachaProgress`, runs the fetch against the httptest/fixture seam, and asserts ≥1 `GachaProgress` received with a non-empty `BannerKey`, `Page>=1`, `PoolTotal>=1`. (At least hoyoverse + one of wuwa/endfield.)
- AC3. Existing provider/core tests still pass unchanged (no signature change; reporter-less calls no-op).
- AC4. `app.RefreshGacha` attaches the reporter and emits `gacha:progress` with `(gameID, {banner,page,poolIndex,poolTotal})`; banner is the GachaConfig label for the key. (Tested at the app layer if a seam allows, else covered by the provider+core tests + a focused emit unit; do not fabricate Wails runtime in tests — assert the onProgress→payload mapping via a small extracted pure function.)
- AC5. Frontend store: a `gacha:progress` event updates `byGid[gid].progress`; `refresh` clears it at start and end. Tested by invoking the bound handler (mock `EventsOn` capturing the callback, as `updates_store.test.ts` does).
- AC6. `GachaBoard` loading state shows a `.gacha-spinner` + progress text when `progress` is set; generic loading text when not. Tested.
- AC7. No token/authkey in the progress payload or any log (review/grep).
- AC8. i18n parity green; no raw literals. vitest + `go test ./...` + `wails build` green.

## Non-goals
- No progress for `GetGachaSummary` (store-only/fast).
- No cancel button, no percentage bar (page count is unknown upfront) — spinner + text only.
- No change to fetch logic, pity, or the redesign just shipped.

## Risks
- Threading `ctx` correctly into the per-page loops (hoyoverse `fetchHoyoGacha` already receives `ctx`; ensure the reporter survives the `selectAuthCandidate` step — attach on the ctx passed to `FetchGacha`, which both selection and pagination share; just don't report inside probes).
- Event routing by gid: payload must include `gameID` so a stale tab's events don't overwrite the active one; store guards `if (s)`.
- `emit` extraction must not change update-registry behavior (keep the nil-ctx guard).
- Spinner keyframe name unique (e.g. `gacha-spin`), not colliding with theme.css `pulse/blink` or the `gacha-shimmer` just added.
