# Plan — 抽卡分析 GachaBoard 重畫 + 分頁外殼修正

**Date:** 2026-06-05 · **Branch:** gacha-ui · **Spec:** docs/superpowers/specs/2026-06-05-omnigate-gacha-board-redesign.md
**Status:** DRAFT — pending subagent plan review.

One cohesive wave (backend one-field add + frontend rewrite are coupled by the avg-pity card). TDD per sub-step; **no commit until the wave's spec-compliance + code-quality reviews APPROVE**.

## S0 — backend: expose ExpectedPity (AC8)

1. `internal/core/gacha_stats.go`: add field to `GachaSummary`:
   `ExpectedPity float64 \`json:"expectedPity"\`` (place near AvgPity).
2. In `ComputeSummary`, set `s.ExpectedPity = cfg.ExpectedPity` (cfg is already a param).
3. `internal/core/gacha_stats_test.go`: extend an existing case (testConfig has `ExpectedPity`) to assert `ComputeSummary(...).ExpectedPity == cfg.ExpectedPity`.
4. `frontend/src/stores/gacha.ts`: add `expectedPity: number;` to the `GachaSummary` interface (load-bearing; the store casts to this). Bindings regen is automatic on `wails build`.

## S1 — shell: BottomBar overview-only (AC1)

1. `frontend/src/App.vue:76`: `<BottomBar v-if="view.viewMode === 'detail' && view.homeTab === 'overview'" />`.
2. Test (AC1): a small vitest over the `view` store asserting the boolean — `view.viewMode='detail'; view.setHomeTab('gacha')` → guard false; `setHomeTab('overview')` → true. (New file e.g. `frontend/src/__tests__/bottombar_guard.test.ts`, or extend an existing view-store test.) No App.vue mount.
3. Manual/visual: gacha tab fills area (DetailView `.gacha-slot{inset:0}` already), no gap where BottomBar was.

## S2 — i18n keys (AC9)

**HARD REQUIREMENT (parity test):** `frontend/src/__tests__/i18n_parity.test.ts` asserts the flattened key sets of en/zh-TW/zh-CN are **identical** (`toEqual`). Every new key MUST be added to all three files or the suite goes red (AC11). Diff the key sets before finishing.

Add to `en.json`, `zh-TW.json`, `zh-CN.json` under `gacha` (zh-TW primary). Keep + reuse existing keys (`loading/unsupported/empty/url_hint/refresh/total_pulls/spend_est/headline_cnt/avg_pity/luck/worst/pull_count`). **Do NOT relabel `headline_cnt`** — its current values ("Top-rarity"/"最高星數"/"最高星数") are already rarity-agnostic; reuse as-is for the card-3 title. New keys (values illustrative):
- `spend_est_note` ("估算" qualifier), card subs: pull-split/type-split are built from labels at runtime (no static key), `expected_pity` ("期望值 {n}"), `below_expected`/`above_expected`/`at_expected` ("略低於期望值 {n}" / "略高於期望值 {n}" / "符合期望值 {n}" — the `at_expected` branch covers avgPity == expectedPity so no raw literal is emitted).
- Sections: `luck_title` ("歐非程度評比"), `pity_title` ("保底進度"), `dist_title` ("出貨分佈"), `recent_title` ("最近五星紀錄"), `pity_remain` ("距離保底還有 {n} 抽"), `dist_axis_low` ("1-9"), `dist_axis_high` ("80+"), `dist_axis_mid` ("抽數區間").
- Donut trio: `stat_worst` ("最非"), `stat_avg` ("平均出貨"), `stat_headline` ("最高星"), units `unit_pull`("抽")/`unit_count`("個").
- Luck bands `luck_band.*`: e.g. `superb`("極歐 · 天選之人"), `good`("微歐 · 運氣不錯"), `avg`("中規中矩"), `bad`("微非 · 別灰心"), `awful`("極非 · 下次一定"). Band thresholds defined in component (S3).
- Item-type map `item_type.*`: known raw keys → label (`char`→"角色", `weapon`→"武器", `lightcone`→"光錐", `wengine`→"音擎", `bangboo`→"邦布"); unknown → raw string.
- States: `error_other` ("讀取失敗，請重試").
- **Parity test**: existing locale-parity test (if any) must pass; otherwise add keys to all three. Verify with a quick key-set diff.

## S3 — GachaBoard.vue rewrite (AC2-AC7, AC10)

Replace the template + style. Script keeps `gacha.load/refresh`, adds computed helpers. Use ONLY existing theme tokens (`--accent --gold-hi --gold-glow --gold-soft --ok --hot --info --text --text-2 --text-3 --panel --elev --border --border-strong --tx-dim`); **never `--gold-1`**.

**Computed/helpers:**
- `bannerLabel(key)`: `sum.pity.find(p=>p.key===key)?.label |> localize` else raw key.
- `typeLabel(raw)`: i18n `item_type[raw]` else raw.
- `localize(m)`: existing `m[locale]??m.en??first` helper (keep).
- `luckBand(score)`: map to band key by thresholds (e.g. ≥85 superb, ≥65 good, ≥45 avg, ≥25 bad, else awful) → `t('gacha.luck_band.'+band)`.
- `donutStyle`: `{ background: 'conic-gradient(var(--accent) ' + luckScore + '%, var(--border) 0)' }` (inline, asserted by AC3). Inner hole = absolutely-positioned circle with panel bg showing the score number.
- `pullSplit`: `Object.entries(sum.perBanner).map(([k,n]) => bannerLabel(k)+' '+n)`.join(' · ').
- `typeSplit`: `Object.entries(sum.headlineByType).map(([k,n]) => typeLabel(k)+' '+n).join(' · ')`.
- `distMax`: `Math.max(...sum.distribution)`; a bucket is highlighted when `v===distMax && v>0`.
- `remain(b)`: `Math.max(0, b.cap - b.current)`.
- `recentCool(count)`: `count > 75`.

**Template — states (order):**
1. `st.loading` → `.gacha-skeleton` (4 card placeholders + 2 panel placeholders; shimmer via existing/added keyframe).
2. `isUnsupported` → `.gacha-unsupported`.
3. `st.errKind==='url'` → `.gacha-empty` + `.gacha-url-hint` + refresh btn.
4. `isEmpty` (supported && totalPulls===0) → `.gacha-empty` import-first + refresh btn.
5. `st.errKind==='other' && !sum` → `.gacha-error` + refresh btn (AC10, previously blank).
6. `sum` → dashboard:
   - `.gacha-cards` grid(4): total(+pullSplit) · spend(currency+spendEst, note) · headline(+typeSplit) · avgPity(1dp green, +expected compare).
   - `.gacha-mid` grid: `.gacha-luck` (donut + band conclusion + 3 trio stats) | `.gacha-pity` (bars, `.near` glow when nearPity, remain text) | `.gacha-dist` (9 bars, `.hot`/gold on max).
   - `.gacha-recent` (≤5 `.recent-item` cards: ✦ + bannerLabel + date + name + pull_count, `.cool` when recentCool).
   - small actions row: `UID {uid}` + 重新整理紀錄 button (keep `.gacha-refresh`).

**Style:** scoped; 4-col grid `repeat(4,1fr)` gap 12 padding 14-22; cards `background:var(--panel); border:1px solid var(--border-strong); border-radius:10px`. Donut ~150px, conic-gradient ring + center hole. Pity bar track `--border`, fill `--accent`; `.near` fill `--gold-hi` + `box-shadow:0 0 10px var(--gold-glow)`. Dist bars flex-end mini chart, max bar `--gold-hi`. Recent cards row, `.cool` value `--info` else `--ok`. Numbers `.mono` tabular-nums. Match overview/grid glass aesthetic.

## S4 — tests (vitest) (AC2-AC7, AC10)

Update `GachaBoard.spec.ts` (keep harness). The `base` fixture already has pity/distribution/recentHeadline; add `expectedPity` + a `perBanner`/`headlineByType` with keys, and a `pity` entry whose key matches the recent card's bannerKey (so bridge resolves).
- Rewrite 4 existing cases to new DOM.
- skeleton-while-pending: `getSummary.mockReturnValue(new Promise(()=>{}))` → `.gacha-skeleton` before flush (AC2).
- populated full-section presence + donut inline style contains `conic-gradient` and luckScore (AC3).
- donut trio on HoYo-shaped (winRate5050:null) and Endfield-shaped; assert no 小保底命中 text (AC4).
- nearPity glow class present/absent (AC5).
- distribution max highlight incl. a tie fixture (AC6).
- bannerKey→label: recent card shows pity label not raw key.
- error_other panel when refresh rejects non-url AND summary null path (AC10).

## S5 — verify (AC11)

- `go test ./internal/core/...`
- frontend: `cd frontend && npm run test` (vitest) — GachaBoard + view guard.
- `wails build` (regens bindings, compiles frontend+Go).
- Launch built exe, eyeball gacha tab vs mockup (manual, user-facing).

## Out of scope (untouched)

Go stats logic beyond the one field; providers; NavStrip; account chip; export; "最後更新" subtitle; winRate5050 computation.

## Risks / watch

- conic-gradient hole: use a pseudo-element/inner div with `--panel` bg (not transparent) so the score reads over the key-art bg.
- locale parity: ensure all three files get identical key sets (a missing zh-CN key throws at runtime in some setups) — diff key sets before done.
- Skeleton shimmer keyframe: **`@keyframes` names are NOT scoped by Vue `<style scoped>`** (only selectors are) → use a globally-unique name like `gacha-shimmer` to avoid colliding with theme.css's global `@keyframes pulse/blink`.
- `at_expected` branch: the avg-pity comparison must handle `avgPity === expectedPity` (use `at_expected` key), not just below/above, or a raw literal leaks (AC9).
- 16:9 fixed area: dashboard must fit; `.gacha-board{overflow-y:auto;max-height:100%}` retained so it scrolls if cramped.

## Commit (after wave review APPROVE)

One commit on `gacha-ui`: `feat(gacha-ui): GachaBoard mockup-parity redesign + BottomBar overview-only + expectedPity`. No Co-Authored-By. Then finish-branch → merge --no-ff into dev (user-gated).
