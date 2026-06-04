# Plan — 抽卡 refresh 進度 + 石頭花費 + 隱藏空池（合併 wave）

**Date:** 2026-06-05 · **Branch:** gacha-ui
**Specs:** docs/superpowers/specs/2026-06-05-omnigate-gacha-refresh-progress.md (APPROVED) · docs/superpowers/specs/2026-06-05-omnigate-gacha-spend-stones-and-empty-pools.md (APPROVED)
**Status:** DRAFT — pending subagent plan review.

One wave (all three touch GachaBoard + the gacha pipeline). TDD; **no commit until the wave's spec-compliance + code-quality reviews APPROVE**.

## S1 — core: progress reporter (refresh-progress spec)
`internal/core/gacha_progress.go` (new):
- `type GachaProgress struct { BannerKey string; Page int; PoolIndex int; PoolTotal int }`
- private `type gachaProgressKey struct{}`
- `func WithGachaProgress(ctx context.Context, fn func(GachaProgress)) context.Context` → `context.WithValue`.
- `func ReportGachaProgress(ctx context.Context, p GachaProgress)` → load fn; if non-nil call it. Nil-safe (no fn → no-op).
Test `gacha_progress_test.go`: reporter attached → received; no reporter → no panic (AC1).

## S2 — providers: emit per page (refresh-progress spec)
At the top of each per-page loop body, after the `ctx.Err()` check, before the HTTP call:
- **hoyoverse `fetchHoyoGacha`** (gacha.go): outer `for i, gt := range gachaTypesToQuery[gid]` → `core.ReportGachaProgress(ctx, core.GachaProgress{BannerKey: bannerForGachaType(gid, gt), Page: page, PoolIndex: i+1, PoolTotal: len(gachaTypesToQuery[gid])})`. (Do NOT report inside `selectAuthCandidate` probes.) Need the loop index → change `for _, gt :=` to `for i, gt :=`.
- **kurogames `fetchWuwa`**: `for pool := 1; pool <= 7; pool++` → report once `{BannerKey: poolBanner(pool), Page: 1, PoolIndex: pool, PoolTotal: 7}`.
- **hypergryph `fetchEndfield`**: loop is `for page := 0; page < endfieldMaxPages; page++` (0-based, gacha.go:203) → `for i, pool := range endfieldPools` (add index `i`; inner var is `page`, no collision) → per page `{BannerKey: pool.bannerKey, Page: page + 1, PoolIndex: i+1, PoolTotal: len(endfieldPools)}` (Page is **1-based** per spec, so `page+1`).
Test (AC2): hoyoverse + endfield (or wuwa) — attach reporter via `core.WithGachaProgress(ctx, …)`, run fetch against existing httptest seam (delay=0), assert ≥1 progress with non-empty BannerKey, Page≥1, PoolTotal≥1. Existing reporter-less tests stay green (AC3).

## S3 — backend: stones pricing (stones spec)
- **hoyoverse/gacha.go:180**: shared literal `PullPrice: 100` → `160`; drop `Currency:"NT$"` from shared (or leave default); assign per-gid inside switch cases: genshin `cfg.Currency = "primogem"`, starrail `"stellar_jade"`, zzz `"polychrome"`.
- **kurogames/gacha.go:106**: `Currency:"NT$"` → `"astrite"` (PullPrice already 160).
- **hypergryph/gacha.go:21**: `endfieldPullPrice = 100` → `160`; comment → `// placeholder: real Endfield per-pull cost TBD`; gacha.go:87 `Currency:"NT$"` → `"endfield_pull"`.
- Go test (AC1 stones): new case — config PullPrice 160, 10 pulls (1 IsFree) → SpendEst 1440; Currency == expected code. (Keep core testConfig's own assertions as-is.)

## S4 — app: emit + label resolution (refresh-progress spec)
- Extract the inline emit closure (`app.go:74-78`) to `func (a *App) emit(name string, args ...any) { if a.ctx != nil { wruntime.EventsEmit(a.ctx, name, args...) } }`; in `New`, pass `a.emit` to `NewUpdateStateRegistry` (behavior unchanged).
- New pure helper (testable): `func gachaProgressPayload(cfg core.GachaConfig, p core.GachaProgress) map[string]any` → resolve banner label: find `cfg.Banners[k].Key == p.BannerKey` → `.Label` (LocalizedString); fallback `core.LocalizedString{"en": p.BannerKey}`. Return `{"banner": label, "page": p.Page, "poolIndex": p.PoolIndex, "poolTotal": p.PoolTotal}`.
- `RefreshGacha`: before `FetchGacha`, `ctx = core.WithGachaProgress(ctx, func(p core.GachaProgress){ a.emit("gacha:progress", gameID, gachaProgressPayload(gp.GachaConfig(gid), p)) })`.
- Go test (AC4): `gachaProgressPayload` maps a known banner key → its label + counts; unknown key → fallback. (The EventsEmit edge stays untested — acceptable.)
- Token safety: payload has only banner/page/counts; never res.URL (AC7).

## S5 — frontend store: progress state + bind (refresh-progress spec)
`stores/gacha.ts`:
- Add top import: `import { EventsOn } from '../../wailsjs/runtime/runtime';` (currently absent; mirrors updates.ts:14).
- `export interface GachaProgress { banner: Record<string,string>; page: number; poolIndex: number; poolTotal: number; }`
- `GachaState` += `progress: GachaProgress | null`; `blank()` sets `progress: null`.
- **Every `GachaState` object literal must include `progress`** — not just `blank()`/`refresh`: `load()` builds three literals (gacha.ts:27 loading-set, :30 success, :32 catch) → all need `progress: null` (load leaves progress null). `refresh(gid)` (:37 start, :40 success, :44 error) sets `progress: null` (start clears any prior; success/error clear). TS will error if a literal omits it once the interface requires it — good.
- `bind()` (idempotent via a module-level `bound` flag): `EventsOn('gacha:progress', (gid: string, p: GachaProgress) => { const s = this.byGid[gid]; if (s) s.progress = p; })`. Mirror `updates.ts:102`.
- `App.vue` onMounted: call `gacha.bind()` once (next to `updates.bind()`).

## S6 — frontend GachaBoard.vue (both specs)
- **Loading branch**: render a `.gacha-spinner` (CSS, keyframe `gacha-spin`) + a text line: `st.progress ? t('gacha.progress', { banner: localize(st.progress.banner), page: st.progress.page, pool: st.progress.poolIndex, total: st.progress.poolTotal }) : t('gacha.loading')`. Keep the skeleton beneath. Progress text only inside the `st.loading` branch (ordering safety: a late event mutating progress after loading=false is invisible).
- **Spend card rewrite** (mandatory): replace `{{ t('gacha.approx') }} {{ sum.currency }}{{ nf(sum.spendEst) }}` with `{{ nf(sum.spendEst) }} {{ currencyName(sum.currency) }}`; label `spend_est` now "消耗". Add `currencyName(code)`: `te('gacha.currency.'+code) ? t('gacha.currency.'+code) : code`.
- **Pity filter** (hide empty): `v-for="b in visiblePity"` where `const visiblePity = computed(() => (sum.value?.pity ?? []).filter(b => (sum.value?.perBanner?.[b.key] ?? 0) > 0))`.

## S7 — i18n (both specs; all 3 locales, parity)
Add under `gacha`:
- `currency`: nested `{ primogem, stellar_jade, polychrome, astrite, endfield_pull }` → 原石/星瓊/菲林/星聲/尋訪 (zh-TW); 原石/星琼/菲林/星声/寻访 (zh-CN); Primogems/Stellar Jade/Polychrome/Astrite/Pulls (en).
- `progress`: "{banner} · 第 {page} 頁（{pool}/{total}）" / zh-CN 同 / en "{banner} · page {page} ({pool}/{total})".
- Relabel `spend_est` value → 消耗 / 消耗 / Consumed. Leave `approx` as a dead key (parity).
Use the i18n merge script (uv run python) to keep key sets identical; verify with the parity test.

## S8 — tests
- Go: S1 reporter roundtrip; S2 provider reports (hoyoverse + endfield); S3 stones SpendEst+code; S4 gachaProgressPayload mapping.
- vitest (GachaBoard.spec + gacha store test):
  - **runtime mock**: since `gacha.ts` now imports `EventsOn` at top level, any suite invoking `bind()` must `vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: (name,fn)=>{ /* capture */ } }))` (mirror updates_store.test.ts:16-21). The existing GachaBoard.spec / gacha_store suites import-resolve the real runtime.js fine, but the new bind()/progress tests add this mock to capture+invoke the handler.
  - progress event → `byGid[gid].progress` set (invoke the captured handler).
  - loading + progress → `.gacha-spinner` exists + text shows banner/page; loading w/o progress → generic loading text.
  - spend card: summary `currency:'primogem'`, spendEst 1440 → text contains localized 原石/Primogems + "1,440"; no "估算"/approx.
  - pity filter: pity [A perBanner>0, B perBanner 0] → only A row.
  - update `base` fixture currency to a real code.

## S9 — verify
`go test ./...` · `cd frontend && npm run test` · `wails build` · launch + eyeball (user-facing).

## Out of scope
Endfield real stone number (placeholder); cancel button; % bar; NTD display; pity computation changes.

## Risks / watch
- hoyoverse loop index: `for _, gt :=` → `for i, gt :=` (i used for PoolIndex); ensure no shadowing.
- emit extraction must preserve nil-ctx guard + update-registry behavior.
- progress event ordering: gate text on `st.loading` (late event harmless); store guards `if (s)`.
- spinner keyframe `gacha-spin` unique (not pulse/blink/gacha-shimmer).
- i18n parity: currency is nested → all 3 locales need the same nested keys; run parity test.
- Find any test asserting old Currency/PullPrice (verified: none in providers; core testConfig independent) — keep green.

## Commit (after wave review APPROVE)
One commit on gacha-ui: `feat(gacha-ui): refresh progress indicator + stone-cost spend + hide empty pools`. No Co-Authored-By. Then user-gated merge → dev.
