# Omnigate — Predownload Wiring & Completion (Design Spec)

Date: 2026-06-07
Status: DRAFT (rev 2 — incorporates adversarial review round 1 findings B1/B2/B3 + M1–M5)
Scope: WuWa (kurogames) + HoYoverse (Genshin Sophon / HSR / ZZZ legacy). Endfield (hypergryph) deferred — §8.
Delivery: phased (§9) — WuWa → Genshin → HSR/ZZZ legacy, each independently shippable.

## §0 Problem

The official launchers advertise a predownload (藍色「可預先下載」) for the next game
version. Omnigate shows the blue availability pill but offers **no way to start a
predownload** — the button never appears for any game.

### §0.1 Root cause (verified in code 2026-06-07)

Predownload has three frontend signals; only one is wired to anything actionable,
and they are split across three sources:

| Signal | Source | Drives | State |
|---|---|---|---|
| `has_predownload` (games store) | `frontend/src/stores/games.ts:40,90` ← `v.Predownload` (`core.VersionInfo.Predownload`, set by each provider's `CheckVersion`) | blue pill only | wired |
| `available_predl` (update snapshot) | `internal/app/update_state.go:56` (`GameUpdateState.AvailablePredl`) | **predl button** (`BottomBar.vue:196-197`) | **never populated** — only ever set to `nil` (`update_handler.go:201`) |
| `predownload_available` (update snapshot) | `update_handler.go:570` ← hoyoverse `predlExposer.GetPredownloadAvailable` | nothing (no component reads it; `updates.ts:56` declares it) | dead |

Net: the pill shows (via `has_predownload`), but the button is gated on
`available_predl`, which nothing ever sets → button never renders. Verified: the only
assignment to `AvailablePredl` in all of `internal/` is `= nil` (`update_handler.go:201`).

### §0.2 Backend maturity (verified 2026-06-07, corrected per review)

| Game | Detection (`CheckVersion`) | Predl plan build | Predl download/stage | Predl **apply** | Net gap |
|---|---|---|---|---|---|
| WuWa (kuro) | ✅ `kurogames.go:162-163` (`idx.Predownload`→`vi.Predownload`) | ❌ `CheckForUpdate` only targets `idx.Default` (`kurogames.go:243,272`) | ✅ generic download + `RenameToPredlReady` (`kurogames.go:364-370`) | ✅ generic file-rename apply, no flavor switch (`kurogames.go:372-383`); `ApplyPredownload` reuses it (`update_handler.go:236-240`) | wiring + predl-targeted `CheckForPredownload` |
| Genshin (Sophon) | ⚠️ Sophon branch (`hoyoverse.go:154-163`) sets Latest via `fetchBranchTag` only — **never sets `vi.Predownload`** | ⚠️ `buildSophonPredlPlan` exists (`update_sophon_plan.go:459`) but is reachable **only** via `buildSophonPlan` in the *normal-build* branch (`hoyoverse.go:643`), which the **idle short-circuit skips when up-to-date** (`hoyoverse.go:609-618`) → `gp.predlPlan == nil` exactly when predl is offered | ✅ `runSophonPredownload` (`hoyoverse.go:737-812`) | ✅ predl-consume on apply (`detectPredlConsume`, `hoyoverse.go:620-638,711-728`) | wiring + `CheckVersion` predl surfacing + predl-plan reachable in idle case (B1) |
| HSR/ZZZ (legacy) | ✅ `version.go:170-172` sets Predownload (no `!=current` filter) | ❌ `buildPlan` sets `predlAvailable` flag only (`plan_internal.go:115`), never assigns `flavorPredlPatch/PredlFull`; returns `flavorNone`/empty when up-to-date (`update_manifest.go:117-131`) | ⚠️ download branch runs for any `PlanPredownload` (`hoyoverse.go:364-373`) | ❌ **apply path absent**: apply switch handles only `flavorPatch/AudioOnly/Full` → predl flavors hit `default:"unsupported flavor"` (`hoyoverse.go:375-390`); cold `manifestCache` on apply → `"manifestCache miss"` (`hoyoverse.go:347-349`); **no legacy predl-consume reconstruction** | wiring + predl plan build + **new legacy predl apply/consume path** (B2) |
| Endfield (hypergryph) | ❌ | ❌ | ❌ | ❌ | OUT OF SCOPE (§8) |

## §1 Architecture & data flow (Approach A — App owns `available_predl`)

Unify on a single actionable signal, `available_predl`, mirroring `available_update`.

### §1.1 Cheap probe populates availability
Extend the App `CheckForUpdate` probe (`update_handler.go:498`, the Refresh-time
path). After the existing `p.CheckVersion` call and `AvailableUpdate` block
(`update_handler.go:534-543`), also set `AvailablePredl`:
- Set `state.AvailablePredl = &core.UpdatePlan{GameID, Kind: PlanPredownload, Version: vi.Predownload.TargetVersion}` (lightweight; **no Files**) **iff**:
  - `vi.Predownload != nil`, AND
  - **the provider for `gid` implements `core.PredownloadChecker` AND its `SupportsPredownload(gid)` returns true** (per-game capability — see §1.2). Only light the button when the heavy path can actually honor *this game*. This gate makes the §9 phased rollout safe: legacy `fetchVersion` sets `vi.Predownload` unconditionally for HSR/ZZZ (`version.go:170-172`), so without a per-game gate an HSR/ZZZ predl window would light the button before that game's `CheckForPredownload` branch exists, routing a click into a fallthrough/unsupported path → empty up-to-date plan → force-`Kind=PlanPredownload` (`update_handler.go:126`) → an empty/mislabeled staged predl. `SupportsPredownload(gid)` keeps every phase honest, AND
  - the game is up-to-date (`vi.Latest == vi.Current` — predl and update are mutually exclusive), AND
  - no staged `state.PredlReady` for that target version (else 套用 takes over).
- Otherwise `state.AvailablePredl = nil`.

### §1.2 New capability interface
```go
type PredownloadChecker interface {
    // SupportsPredownload is the cheap, PER-GAME capability predicate. It must
    // return true only for gids whose CheckForPredownload branch is actually
    // implemented.
    SupportsPredownload(gid GameID) bool
    // CheckForPredownload builds a real predl plan that targets the predownload
    // manifest. For any gid where SupportsPredownload is false it must return
    // core.ErrPredownloadUnsupported (defense-in-depth).
    CheckForPredownload(ctx context.Context, gid GameID, onProgress func(done, total int)) (UpdatePlan, error)
}
```
`CheckForPredownload` builds a real predl plan that **targets the predownload
manifest** (not the live/default one), `Kind = PlanPredownload`,
`Version = <predl target>`, and caches whatever per-provider state the matching apply
path needs.

**Why a per-game predicate, not a Go type assertion (round-3 BLOCKER fix):** Go
interface satisfaction is per-**type**, and one provider type serves multiple games
— `*hoyoverse.Provider` serves Genshin (Sophon) + HSR + ZZZ (legacy), whose predl
branches land in different phases (§9). A `p.(core.PredownloadChecker)` assertion
would become true for **all** hoyoverse games the moment Genshin's method lands in
Phase 2, re-lighting HSR/ZZZ before their branch exists (Phase 3). `SupportsPredownload(gid)`
gates per game: hoyoverse returns `g.UsesSophon` after Phase 2, all games after Phase 3.

### §1.3 Heavy-path routing
In `runStartUpdateAsync` (`update_handler.go:84+`), when `kind == PlanPredownload`
and the provider implements `PredownloadChecker` **and `SupportsPredownload(gid)` is
true**, call `CheckForPredownload` instead of `CheckForUpdate`. The returned plan
flows into the existing download worker → `RunUpdate(PlanPredownload)` → provider
stages + `RenameToPredlReady`. If `CheckForPredownload` returns
`core.ErrPredownloadUnsupported` (or the capability is false), clear
`state.AvailablePredl` and idle gracefully — never force `Kind=PlanPredownload` onto a
non-predl plan.

### §1.4 Apply
`ApplyPredownload` (`update_handler.go:227`) resumes from the staged `PredlReady` and applies.
Per provider, apply must be able to rebuild any plan state it needs from the staged
sidecar when `manifestCache` is cold (see §2.2 for the legacy case, which needs new
reconstruction; kuro + Sophon already cover this).

### §1.5 Retire dead/bypass signals
Frontend predl button **and** pill read `available_predl` (+ `predl_ready`). Remove
the `has_predownload`-only pill bypass (`BottomBar.vue:86`) and stop relying on the
unread `predownload_available`. Go fields `GameStatus.HasPredownload` /
`GameUpdateSnapshot.PredownloadAvailable` may remain but are no longer the UI's truth.
(Note: post-change the pill goes dark for any non-Updater backend — only Endfield,
which is out of scope and has no predl anyway.)

## §2 Per-provider work

### §2.1 WuWa (kurogames) — implement `CheckForPredownload` [smallest]
Mirror `CheckForUpdateWithProgress` (`kurogames.go:212`) but source from
`idx.Predownload`:
- Guard: `idx.Predownload == nil` → empty plan so the App clears `available_predl`.
- **Parameterize index-file selection**: `pickIndexFileForVersion` currently hardcodes
  `idx.Default.Config` (`update_manifest.go:212-219`) — add a variant/param that takes
  the predl config. Then `pickCDN(idx.Predownload.CDNList)` +
  `filterChangedFiles(installPath, cdn, predlCfg.BaseURL, predlIdxFile.Resource, …)`
  diffs **current install vs predl-version manifest**.
- Plan: `Version = idx.Predownload.Version`, `Kind = PlanPredownload`, predl index ETag.
- Download / `RenameToPredlReady` / `ApplyPredownload` reuse M3.A verbatim — kuro's
  apply is a generic file-rename with no flavor switch, so the staged predl applies
  unchanged and `writeLauncherConfigVersion` writes the predl target version.

### §2.2 HSR/ZZZ (legacy hoyoverse) — predl plan **and** new apply path [largest]
Two genuinely new pieces (the existing legacy RunUpdate predl branch only handles the
*download* half):

1. **`CheckForPredownload` (legacy):** fetch `getGamePackages`, build the plan from
   `entry.PreDownload` (patches matching `currentVer→predlVer`, else `PreDownload.Major`
   full). Set `gp.flavor` to the **normal** `flavorPatch`/`flavorFull` (NOT a predl
   flavor — the apply switch only accepts the normal ones), `Kind = PlanPredownload`,
   `Version = entry.PreDownload.Major.Version`. `flavorPredlPatch/PredlFull`
   (`plan_internal.go:17-18`) stay unused or are removed.
2. **Legacy predl-consume on apply:** the staged `predl_ready.json` must carry enough
   to rebuild `gp` so `ApplyPredownload` works after an app restart with a cold
   `manifestCache`. The legacy `planSnapshot` (`update_progress.go:14-25`, written by
   `RenameToPredlReady`, `update_progress.go:154-178`) **already** persists `Files`,
   `SourceVersion`, `TargetVersion`, `ManifestETag`, `AudioLanguages` — the **only**
   missing field is `flavor`. So: (a) add a `Flavor` field to `planSnapshot` and write
   it at the predl-stage site (`hoyoverse.go:364-372`); (b) add a legacy reconstruction
   in `RunUpdate` (mirror of Sophon's `detectPredlConsume`/`predlSnapshot`): when a
   `predl_ready` for `plan.Version` exists and `manifestCache` misses, rebuild `gp`
   (flavor + files + etag + version) from the snapshot instead of erroring at
   `hoyoverse.go:347-349`, then apply via the normal `flavorPatch`/`flavorFull` switch
   reading staged zips. Note `applyRecoveryState` (`update_handler.go:799-828`) only
   reconstructs `state.PredlReady` via `core.LoadProgressFromPath` (ProgressFile
   portion) — it does **not** rebuild `gp`, which is why (b) is required. This is the
   bulk of §2.2.

### §2.3 Genshin (Sophon hoyoverse) — wiring + reach the predl plan in idle case [medium]
Download/stage + predl-consume apply are complete; two pieces are needed:

1. **`CheckVersion` predl surfacing (cheap probe).** The Sophon branch
   (`hoyoverse.go:154-163`) fetches only the tag. It must use a branch fetch that also
   returns `PreDownload` (the `getGameBranches` data `checkForUpdateSophon` already
   reads at `hoyoverse.go:594`) and set
   `info.Predownload = &core.PredownloadInfo{TargetVersion: branch.PreDownload.Tag}`
   when `!branch.PreDownload.IsEmpty()` and the predl tag differs from local.
2. **`CheckForPredownload` that reaches the predl plan when up-to-date.** Do **not**
   delegate to `checkForUpdateSophon` — its idle short-circuit (`hoyoverse.go:609-618`,
   the exact up-to-date case predl is offered in) returns `flavorNone` with
   `gp.predlPlan == nil`, and `RunUpdate(sophon predl)` then errors at
   `hoyoverse.go:738-739`. Instead call `buildSophonPredlPlan` (`update_sophon_plan.go:459`)
   directly (fetch branch → build predl plan from `branch.PreDownload`), cache the `gp`
   with `predlPlan` populated, and return a `Kind = PlanPredownload` plan. The existing
   `runSophonPredownload` + predl-consume apply then work unchanged. (Minor: the
   standalone build does not set `gp.TotalBytes` — set from the *main* plan at
   `update_sophon_plan.go:417` — so the predl progress denominator emits 0; set it from
   the predl plan's byte total to avoid a 0-total progress bar.)

## §3 UX (BottomBar)

The predl button, `predl_ready`/`predl_stale` handling, `onPredl`/`onApplyPredl`/
`onRemovePredl`, the `predl_stale` confirm dialog (`BottomBar.vue:138-146,196-208,221-223`)
and all i18n keys (3 locales) **already exist and are unit-tested**
(`predl_hidden_hypergryph.test.ts`). The only frontend gaps are:

- **Reposition** the predl button to sit next to the per-game config gear
  (`GameConfigPopover`, `BottomBar.vue:211` — the gear left of Play/Update, NOT the
  titlebar gear). Visible when `available_predl != null && !inFlight && !predlReady`.
- **Unify** the pill + button on `available_predl` (§1.5); drop the `has_predownload`
  OR-branch in `hasAnyPredl` (`BottomBar.vue:86`).
- Add any new i18n key only if copy changes; otherwise none.

### §3.1 Default behaviours (user-approved)
1. Predl button appears only when up-to-date; behind-version shows Update, not predl.
2. Apply is **manual** (user clicks 套用); no auto-apply on launch.
3. Availability uses the cheap Refresh signal to show the button; the real plan (MD5
   verification) runs on click — consistent with the Update button.

## §4 Error handling & edge cases

- **Predl target drift / `predl_stale`:** when `available_predl.version` differs from a
  staged `predl_ready.version`, run the existing stale flow.
- **Phantom-predl self-heal — must be generalized (B3).** The self-heal lives in
  `RefreshVersion` at `app.go:544-562` (deletes the sidecar + clears `PredlReady` when
  `vi.Current == predl.Version`). It currently hardcodes
  `a.tempDirFor(kurogames.BackendID, gid)` (`app.go:553`) → wrong temp dir for a
  HoYoverse predl. Generalize it to the **owning backend** (derive from the provider
  for `gid`, e.g. `p.ID()`), so it cleans the correct backend's staging dir. Required
  before HoYoverse predl ships.
- **Legacy lacks a detection-time version filter (M3).** kuro filters
  `idx.Predownload.Version != vi.Current` (`kurogames.go:162`); legacy `version.go:170`
  sets `Predownload` unconditionally. §1.1's `vi.Latest == vi.Current` gate +
  generalized phantom self-heal cover the up-to-date/phantom cases for legacy.
- **0-file predl plan:** fully staged / no diff → empty plan → `RenameToPredlReady`
  immediately → 套用 (or ready). The predl path must tolerate 0 files.
- **Cancel / interrupted resume:** ctx-cancel mid-download or crash → `ScanRecovery`
  classification (`recovery.go`; `RecoveryPhasePredlAwaiting` →
  `applyRecoveryState`, `update_handler.go:799-821`) + bell-drawer resume.
- **Predl manifest 404 / race (predl just pulled):** map to `manifest_not_found`. The
  Refresh probe is best-effort — on any error it silently clears `available_predl`, no
  toast (matches the update probe, `update_handler.go:528-531`).
- **Update precedence:** behind-version → only `AvailableUpdate`, never
  `available_predl` (mutually exclusive, §1.1).
- **Apply prerequisites:** game not running (process-check), apply lock, Program Files
  admin — identical to update apply; no new handling.

## §5 Components & boundaries

| Unit | Responsibility | Status |
|---|---|---|
| `core.PredownloadChecker` | capability interface: `SupportsPredownload(gid) bool` (per-game) + `CheckForPredownload(...)` | new |
| `core.ErrPredownloadUnsupported` | sentinel returned by `CheckForPredownload` for unsupported gids | new |
| `App.CheckForUpdate` (extend) | populate/clear `available_predl` from cheap probe | edit |
| `App.runStartUpdateAsync` (extend) | route predl starts through `PredownloadChecker` | edit |
| `App.RefreshVersion` phantom self-heal (B3) | generalize to owning backend | edit |
| `kurogames.CheckForPredownload` + parameterized index-file pick | predl plan vs `idx.Predownload` | new |
| `hoyoverse.CheckVersion` (Sophon branch) | surface `vi.Predownload` from `getGameBranches.PreDownload` | edit |
| `hoyoverse.CheckForPredownload` | Sophon: build predl plan directly via `buildSophonPredlPlan`; legacy: build from `entry.PreDownload` | new |
| `hoyoverse` legacy predl-consume reconstruction (B2) | rebuild `gp` from `predl_ready` on cold-cache apply | new |
| `BottomBar.vue` (edit) | reposition predl button by the gear; unify on `available_predl` | edit |

## §6 Testing

- **Go:** kuro `CheckForPredownload` (httptest fixture with `predownload` block →
  asserts predl version/CDN targeting + correct `plan.Version`); Sophon
  `CheckForPredownload` builds `gp.predlPlan` in the up-to-date case (regression for
  B1); legacy `CheckForPredownload` + cold-cache `ApplyPredownload` reconstruction
  (regression for B2); App probe three cases (sets `AvailablePredl` when predl
  available + up-to-date; clears when update pending; suppresses when `predl_ready`
  staged); generalized phantom self-heal cleans the correct backend dir (regression
  for B3); `PredownloadChecker` routing in `runStartUpdateAsync`; **per-game capability
  gate** — App probe sets `AvailablePredl` only when `SupportsPredownload(gid)` is true
  (round-3 regression: with hoyoverse implementing the interface but
  `SupportsPredownload` → `g.UsesSophon`, a Genshin gid lights while HSR/ZZZ gids stay
  dark; `CheckForPredownload` returns `ErrPredownloadUnsupported` for HSR/ZZZ pre-Phase-3).
- **Frontend (Vitest):** predl button renders by the gear when `available_predl` set;
  hidden when null; hidden (套用 shown) when `predl_ready`; pill driven by
  `available_predl`; i18n parity if any key added.
- **Whole-repo green:** `go vet ./...`, `go test ./...`, frontend build + vitest,
  `wails build`.
- **USER real-machine smoke (per phase):** WuWa currently has an active predl window —
  button appears → download → ready → apply → version writeback, no bounce. Each
  HoYoverse phase smoked against an open predl window (time-limited; timing matters).

## §7 Out of scope
- Endfield (hypergryph) predownload — §8.
- Auto-apply-on-launch.
- Mid-file byte-range resume changes beyond M3.A/M3.B.

## §8 Endfield deferral
M3.C deliberately deferred predl (M3.C.v2 candidate). Endfield's `get_latest` predl
semantics are unverified (whether a predownload package field exists needs a protocol
spike). Adding it requires research + new provider work; a separate spec/plan later.

## §9 Phasing (delivery order, each independently shippable)

Review round 1 showed the three providers differ greatly in remaining work, so deliver
in increasing-effort order; each phase is a working, smoke-tested predl for its games.
**Per-game `SupportsPredownload(gid)` (§1.2) is what makes every intermediate state
safe** — a provider lights a game's button only after that game's branch lands, even
though one provider type (hoyoverse) serves three games across two phases:

1. **Phase 1 — WuWa (kuro):** the App probe (§1.1), `core.PredownloadChecker` +
   `ErrPredownloadUnsupported` (§1.2), heavy-path routing (§1.3), generalized phantom
   self-heal (§4/B3), frontend reposition + unify (§3, §1.5), and kuro
   `CheckForPredownload` (§2.1). kuro `SupportsPredownload` → true for wuwa. Ships predl
   for the game the user reported. Smallest, highest-confidence (apply reuses M3.A).
2. **Phase 2 — Genshin (Sophon):** `CheckVersion` predl surfacing + idle-case
   `CheckForPredownload` (§2.3). hoyoverse `SupportsPredownload(gid)` → **`g.UsesSophon`
   only** (Genshin true; HSR/ZZZ false → stay dark). Apply/consume already exist.
3. **Phase 3 — HSR/ZZZ (legacy):** legacy `CheckForPredownload` + the new legacy
   predl-consume apply path (§2.2). hoyoverse `SupportsPredownload(gid)` → true for all
   three. Largest; isolated so its risk doesn't block 1–2.
