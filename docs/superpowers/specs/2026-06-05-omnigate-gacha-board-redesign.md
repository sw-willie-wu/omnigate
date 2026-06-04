# Spec — 抽卡分析 GachaBoard 重畫 + 分頁外殼修正

**Date:** 2026-06-05 · **Branch:** gacha-ui (off dev) · **Status:** DRAFT — pending subagent spec review.

## Background

P3 shipped a working gacha backend (`GachaSummary` is fully populated) but the frontend `GachaBoard.vue` is a stripped-down skeleton that diverges sharply from the approved standalone mockup (`Desktop/export/omnigate Gacha (standalone).html` + `omnigate Gacha - Prompt.md`, the design source-of-truth referenced by the P3 design doc §line 10). User-reported defects on dev:

1. The launch/settings/version bar (`BottomBar`) shows on the **抽卡分析** tab; it belongs only to **總覽**.
2. While fetching, the board shows only a bare centered "載入中" — no structure/feedback.
3. The board looks nothing like the mockup (missing the luck donut, pity progress bars, distribution bar chart, recent-5★ cards; uses non-existent CSS tokens so the palette is off).

Root causes verified in code:
- `App.vue:76` `<BottomBar v-if="view.viewMode === 'detail'" />` — gated on viewMode only, not on `view.homeTab`. So it stays on the gacha tab.
- `GachaBoard.vue` renders 4 plain cards + pity rows + a recent list; **no donut / progress bars / distribution chart / recent cards**, and references `var(--gold-1, #e8c265)` — a token that **does not exist** in `frontend/src/styles/theme.css` → falls back to the hardcoded off-palette `#e8c265`. (`var(--tx-dim)` it also uses DOES exist at theme.css:13 and is fine.)
- `GachaSummary` (core/gacha_stats.go) provides almost every datum the mockup needs: `totalPulls`, `perBanner`, `spendEst`+`currency`, `headlineCnt`, `headlineByType`, `avgPity`, `luckScore`, `worstPull`, `pity[]` (key/label/current/cap/nearPity), `distribution[]` (9 buckets), `recentHeadline[]`. **Two gaps** (see Data bridges + Non-goals): (a) `ExpectedPity` is not on the summary (only on `GachaConfig`) — needs the one-field add; (b) `winRate5050` exists as a field but is never populated → always nil.

## Data bridges (label mapping — needed because some keys are raw)

- **`recentHeadline[].bannerKey` is a raw key, not a label.** `HeadlineEntry` carries only `bannerKey` (e.g. "character"/"weapon"/"special"); `BannerPity` carries a localized `label`. §2.5 cards must resolve the banner name via a lookup over `summary.pity` (`pity.find(p => p.key === h.bannerKey)?.label`), with the raw key as fallback when no pity entry matches.
- **`perBanner` keys are raw banner keys** (per-game: "character"/"lightcone"/"char"/…). The §2.1 总抽数 sub-split ("角色池 812 · 光錐池 472") resolves each key via the same `pity[]` label lookup (fallback: raw key).
- **`headlineByType` keys are raw item-type strings** — for HoYoverse these arrive already localized in the fetch language (e.g. "角色"/"光錐"), for Endfield it is the literal "char". The §2.1 五星 sub-split shows `{type} {n}` joined; an i18n item-type map covers known raw keys (e.g. "char"→角色), raw string shown verbatim otherwise. (Accept minor cross-locale imperfection: HoYo type strings reflect the fetch lang, not the UI lang — documented, not blocking.)

## Goals

Bring the 抽卡分析 content to mockup parity using the app's real theme tokens, fix the shell so launch chrome is overview-only, and give fetch a structured loading state — with per-game graceful degradation (no 50/50, missing data) consistent with NewsPanel/last-played.

## Non-goals

- **One scoped backend exception only**: add a single `ExpectedPity float64 \`json:"expectedPity"\`` field to `core.GachaSummary`, populated in `ComputeSummary` from `cfg.ExpectedPity`. The **load-bearing frontend change** is adding `expectedPity` to the hand-written `gacha.ts` `GachaSummary` interface (the store casts the binding result against it; nothing imports the generated `wailsjs/go/models.ts`). Regenerating Wails bindings is hygiene only (happens on `wails build` anyway), not a gating dependency. This is needed because the avg-pity card's "vs 期望值" comparison (mockup line 21) is otherwise unrenderable — `ExpectedPity` currently lives only on `GachaConfig` (Go-internal). No other Go/stats logic changes.
- **NOT computing a real 50/50 win-rate.** `GachaSummary.WinRate5050` is declared but **never assigned anywhere** → it is `nil` for *every* game (verified: no `WinRate5050 =` in the codebase). A genuine 小保底命中率 needs per-pull featured-vs-standard detection (a standard-pool item list per game) that the backend does not have. That is a separate backend feature, explicitly **out of scope** here. Consequence below (donut stats).
- No account-chip in the nav strip (design doc marks it a later sub-task; UID stays shown in-board for now).
- No 匯出報表 (export) button, and no "最後更新 N 分鐘前" subtitle (mockup §3 marks both optional; `GachaSummary` carries no fetch timestamp → the subtitle is a **deliberate cut**, not an oversight).
- No new gacha providers / Endfield token rework (tracked separately).

## Design reference (mockup Prompt.md §2, source of truth)

Content area = dashboard, 4-col grid (`repeat(4,1fr)`, gap ~12, padding 14–22), top→bottom:

1. **§2.1 Top 4 stat cards** (1 col each): 總抽數 (`totalPulls` + sub `perBanner` split via pity-label bridge) · 已花費(估算) (`currency``spendEst`, label marks 估算) · 最高星數量 (`headlineCnt` + sub `headlineByType` split via item-type map) · 平均出貨 (`avgPity` to 1 dp, green, sub compares to `expectedPity` — "略低/高於期望值 {expectedPity}"). **Card-3 label is rarity-agnostic** ("最高星" not hardcoded "五星" — Endfield headline is 6★, WuWa/HoYo 5★; the mockup's "五星" is HoYo-centric).
2. **§2.2 歐非程度評比** (left, spans 2 rows): centered **conic-gradient donut** showing `luckScore`; conclusion line (gold) derived from luckScore bands + a description; bottom 3 small stats (see "Donut bottom stats" below).
3. **§2.3 保底進度** (center, spans 2 cols): one bar per `pity[]` entry — label + `current`/`cap`, fill width = current/cap, **`nearPity` bars glow gold**; below each "距離保底還有 N 抽" (= cap−current, omit/clamp at ≤0).
4. **§2.4 出貨分佈** (right, 1 col): mini vertical bar chart over `distribution[]` (9 buckets 1–9…80+); tallest bar(s) tinted gold; x-axis end labels.
5. **§2.5 最近五星紀錄** (bottom, spans 4 cols): up to 5 horizontal cards from `recentHeadline[]` — ✦ + banner label (resolved from `bannerKey` via the pity-label bridge) + date(mono) + name + "N 抽出貨"; count >75 tinted cool-blue (`--info`), else green (`--ok`).

Visual tokens (use the app's real `theme.css` vars — NOT inventing `--gold-1`/`--tx-dim`):
`--accent`(#d6b04b gold CTA), `--gold-hi`, `--gold-glow`, `--gold-soft`, `--ok`(#74d68a green), `--hot`(#ff6f6f), `--info`(#6fa8ff cool), `--text`/`--text-2`/`--text-3`, `--panel`/`--elev`, `--border`/`--border-strong`. Mono numbers via `font-family:'JetBrains Mono'` + `font-variant-numeric:tabular-nums`. Match the surrounding overview/grid card aesthetic (glass panels, rounded ~10–14px).

## Donut bottom stats (matches backend reality)

`winRate5050` is **never populated** (nil for all games), so the mockup's "小保底命中 71%" cannot be shown honestly for ANY game. The donut therefore shows **three universally-available stats for all games**:

`最非 {worstPull} 抽` · `平均出貨 {avgPity} 抽` · `最高星 {headlineCnt} 個`

- Always exactly 3 stats; no `小保底命中`/`大保底` line until the backend computes a real win-rate.
- **Flagged follow-up (out of scope, surface to user):** a true 小保底命中率 (50/50 win-rate) + 大保底 needs the backend to detect featured-vs-standard per 5★ (a per-game standard-pool list, as the reference `MiHoYoGachaAnalysis.check_is_up` hardcodes). The user noted "all games have 大/小保底, just different algorithms" — but the current stats engine does not compute it. We render honest universal stats now and track the win-rate as a backend follow-up; we do **not** fabricate a number.

## States (four, like today, but structured)

- **loading**: a **skeleton** mirroring the dashboard grid (4 card placeholders + 2 panel placeholders), not a bare text line. Shown on initial `load` and during `refresh`.
- **unsupported** (`summary.supported === false`): friendly "not supported" panel.
- **empty / url** (`errKind==='url'` or `supported && totalPulls===0`): url → reopen-guidance text + 重新整理紀錄 button; empty → friendly import-first text + button.
- **error other** (`errKind==='other'` and no summary): a generic error panel with a 重新整理紀錄 button (currently this case renders nothing — fix it).
- populated: the full dashboard. A subtle inline refresh affordance stays (UID + 重新整理紀錄), matching today.

## Shell fix

`App.vue`: `BottomBar` shows only when `view.viewMode === 'detail' && view.homeTab === 'overview'`. NavStrip tab selection already styles 抽卡分析 active (theme.css `.nav-tab.active`); no change there. Verify the gacha board still fills the area (DetailView `.gacha-slot { inset:0 }`) and that hiding BottomBar leaves no layout gap on the gacha tab.

## Acceptance criteria

- AC1. `BottomBar` is **not** rendered on 抽卡分析 (`homeTab==='gacha'`), rendered on 總覽. Asserted by testing the boolean guard `viewMode==='detail' && homeTab==='overview'` driven by the `view` store (no full App.vue mount required); the App.vue `v-if` uses exactly this condition.
- AC2. Loading shows a skeleton (a `.gacha-skeleton` element), not only text. Tested by making `GetGachaSummary` return a **never-resolving** promise, mounting, and asserting `.gacha-skeleton` exists **before** `flushPromises`.
- AC3. Populated board renders all five sections: 4 stat cards (with sub-splits), luck donut, pity bars (fill width ∝ current/cap, `near` class when `nearPity`), distribution bars (9), recent-5★ cards. Each asserted by element presence + a representative value. The donut is asserted via its **inline style string** (contains `conic-gradient` and the `luckScore` value, or a bound `--score` custom prop) — not computed style.
- AC4. Donut bottom stats render the universal trio (最非/平均出貨/最高星) for **all** games (50/50 and non-50/50 alike), since `winRate5050` is always nil; **no** 小保底命中 line is rendered. Asserted on both a HoYo-shaped and a WuWa/Endfield-shaped summary.
- AC5. `nearPity` bar gets the gold-glow class; non-near does not.
- AC6. distribution: the max bucket(s) get the highlight class (all buckets tied at max are highlighted).
- AC7. Uses only theme.css tokens that exist — specifically **no `--gold-1`** (the one truly-missing token; `--tx-dim` exists at theme.css:13 and is fine to use). Confirmed by grep/review of GachaBoard styles.
- AC8. expectedPity reaches the frontend: `GachaSummary.ExpectedPity` added (Go), populated from `cfg.ExpectedPity` in `ComputeSummary`, present in the regenerated Wails TS model + the `gacha.ts` interface; the avg-pity card renders the comparison. A Go test asserts `ComputeSummary(...).ExpectedPity == cfg.ExpectedPity`.
- AC9. i18n parity: every new visible string has keys in en / zh-TW / zh-CN; no raw CJK literals in template. zh-TW primary. Includes the item-type map + luck-conclusion bands + new section/label strings.
- AC10. All four non-populated states render a usable panel (no blank), incl. the previously-blank `errKind==='other'` (+ no summary) case.
- AC11. vitest GachaBoard suite green (existing 4 cases updated to new DOM + new cases); `go test ./internal/core/...` green (expectedPity); `wails build` green.

## Test plan

- **Go** (core): `ComputeSummary` populates `ExpectedPity` from cfg (AC8).
- **vitest** (mirror existing GachaBoard.spec.ts harness — pinia + i18n(en) + mocked GetGachaSummary/RefreshGacha):
  - Update existing 4 cases to new DOM (stat cards, empty, unsupported, url-hint).
  - skeleton-while-pending via never-resolving promise (AC2).
  - full-section presence + representative values on populated; donut asserted via inline style string (AC3).
  - donut universal trio on both HoYo-shaped and WuWa/Endfield-shaped summaries; no 小保底命中 line (AC4).
  - nearPity glow (AC5); distribution max highlight incl. tie (AC6); other-error panel (AC10).
  - bannerKey→label bridge: a recentHeadline card shows the pity label, not the raw key.
- **BottomBar guard (AC1):** unit-test the boolean `viewMode==='detail' && homeTab==='overview'` over the `view` store (set homeTab='gacha' → false; 'overview' → true). No App.vue mount.

## Risks

- Donut via conic-gradient: keep it CSS-only (no canvas) for testability + theme consistency; the score arc = `conic-gradient(var(--accent) <score>%, track 0)`.
- Distribution max-highlight when multiple buckets tie at max — highlight all maxes (simple, deterministic).
- The "all games have 大/小保底" intent vs backend reality (no soft-pity-rate for non-50/50): spec keeps substitution + flags; do not fabricate a metric.
- 16:9 locked layout (theme.css `.app-wrap`): the dashboard must fit the detail content area at the app's fixed aspect; verify no overflow/scroll jank (board may scroll internally as today via `.gacha-board{overflow-y:auto}`).
