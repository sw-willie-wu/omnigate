# Omnigate — Predownload Wiring & Completion (Design Spec)

Date: 2026-06-07
Status: APPROVED (brainstorm, 4 sections approved by user)
Scope: WuWa (kurogames) + HoYoverse (Genshin / HSR / ZZZ). Endfield (hypergryph) explicitly deferred — see §8.

## §0 Problem

The official launchers advertise a predownload (藍色「可預先下載」) for the next game
version. Omnigate already shows the blue availability pill, but there is **no way to
actually start a predownload** — the button never appears for any game.

### §0.1 Root cause (verified in code 2026-06-07)

Predownload has three frontend signals, only one of which is wired to anything
actionable, and they are split across three sources:

| Signal | Source | Drives | State |
|---|---|---|---|
| `has_predownload` (games store) | `frontend/src/stores/games.ts:40,90` ← `v.Predownload` (`core.VersionInfo.Predownload`, set by each provider's `CheckVersion`) | blue pill only | wired |
| `available_predl` (update snapshot) | `internal/app/update_state.go:56` (`GameUpdateState.AvailablePredl`) | **predl button** (`BottomBar.vue:196`) | **never populated** — only ever set to `nil` (`update_handler.go:201`) |
| `predownload_available` (update snapshot) | `update_handler.go:570` ← hoyoverse `predlExposer.GetPredownloadAvailable` | nothing (BottomBar never reads it) | dead |

Net: the pill shows (via `has_predownload`), but the button is gated on
`available_predl`, which nothing ever sets → button never renders.

### §0.2 Backend maturity (verified 2026-06-07)

| Game | Detection (`CheckVersion`) | Predl plan build | Predl download/staging | Gap |
|---|---|---|---|---|
| Genshin (Sophon) | ⚠️ `CheckVersion` Sophon branch (`hoyoverse.go:154-163`) sets Latest from `fetchBranchTag` only — **never sets `vi.Predownload`** (the `version.go:171` assignment is in the legacy `fetchVersion` path, which Sophon does not use) | ✅ `buildSophonPlan` → `gp.predlPlan` | ✅ `runSophonPredownload` (`hoyoverse.go:737-812`) + predl-consume + WAL/recovery | UI wiring + **`CheckVersion` must surface predl from `getGameBranches.PreDownload`** |
| HSR / ZZZ (legacy) | ✅ `version.go:171` | ❌ `buildPlan` sets `predlAvailable` flag only (`plan_internal.go:115`); never assigns `flavorPredlPatch/PredlFull`; returns empty plan when up-to-date | ❌ (RunUpdate has the `RenameToPredlReady` branch but no staged files) | wiring + predl plan build |
| WuWa (kuro) | ✅ `kurogames.go:162-163` (`idx.Predownload` → `vi.Predownload`) | ❌ `CheckForUpdate` always targets `idx.Default`, never `idx.Predownload` | ❌ (RunUpdate has `RenameToPredlReady`, `kurogames.go:365`) | wiring + predl plan build |
| Endfield (hypergryph) | ❌ | ❌ | ❌ | OUT OF SCOPE (§8) |

## §1 Architecture & data flow (Approach A — App owns `available_predl`)

Unify on a single actionable signal, `available_predl`, mirroring how
`available_update` already works.

1. **App `CheckForUpdate` probe extension** (`update_handler.go:498`, the cheap
   Refresh-time path). After the existing `p.CheckVersion` call and
   `AvailableUpdate` decision, also set `AvailablePredl`:
   - Set `state.AvailablePredl = &core.UpdatePlan{GameID, Kind: PlanPredownload, Version: vi.Predownload.TargetVersion}` (lightweight; **no Files**) **iff**:
     - `vi.Predownload != nil`, AND
     - the game is up-to-date (`vi.Latest == vi.Current`, i.e. no pending update — predl and update are mutually exclusive), AND
     - no staged `state.PredlReady` exists for that target version (else the "套用" button takes over).
   - Otherwise `state.AvailablePredl = nil`.
   - This is symmetric with the existing `AvailableUpdate` block (`update_handler.go:535-543`).

2. **New optional capability interface** `core.PredownloadChecker`:
   ```go
   type PredownloadChecker interface {
       CheckForPredownload(ctx context.Context, gid GameID, onProgress func(done, total int)) (UpdatePlan, error)
   }
   ```
   Builds a real predl plan that **targets the predownload manifest** (not the
   live/default one), with `Kind = PlanPredownload` and `Version = <predl target>`.

3. **Heavy-path routing.** In `runStartUpdateAsync` (`update_handler.go:84+`), when
   `kind == core.PlanPredownload` and the provider implements `PredownloadChecker`,
   call `CheckForPredownload` instead of `CheckForUpdate`. Fallback to the existing
   `CheckForUpdate` path otherwise (so a provider without the interface degrades to
   current behaviour). The returned plan flows into the existing download worker →
   `RunUpdate(PlanPredownload)` → provider stages and calls `RenameToPredlReady`.

4. **Apply** is unchanged: `ApplyPredownload` resumes from `predl_ready.json`,
   applies staged files, writes the version back.

5. **Retire dead/bypass signals.** Frontend predl button **and** pill both read
   `available_predl` (+ `predl_ready`). The `has_predownload`-only pill bypass and
   the unread `predownload_available` field are removed from the frontend decision
   logic. (Go `GameStatus.HasPredownload` and `GameUpdateSnapshot.PredownloadAvailable`
   may remain as fields but are no longer the UI's source of truth.)

## §2 Per-provider work

### §2.1 WuWa (kurogames) — implement `CheckForPredownload`
Mirror `CheckForUpdateWithProgress` (`kurogames.go:212`) but source from
`idx.Predownload` instead of `idx.Default`:
- Guard: if `idx.Predownload == nil` → return a typed "no predownload" no-op
  (empty plan) so the App can clear `available_predl`.
- `pickIndexFileForVersion` against the predl config; `pickCDN(idx.Predownload.CDNList)`;
  `filterChangedFiles(installPath, cdn, predlCfg.BaseURL, predlIdxFile.Resource, ...)`
  diffs **current install vs predl-version manifest**.
- Plan: `Version = idx.Predownload.Version`, `Kind = PlanPredownload`,
  `ManifestETag = <predl index etag>`.
- Download / staging / `RenameToPredlReady` / `ApplyPredownload` reuse the existing
  M3.A paths verbatim (download to `tempDir/<gid>/<predlVersion>/`).

### §2.2 HSR / ZZZ (legacy hoyoverse) — implement `CheckForPredownload`
Build the plan from `entry.PreDownload` (the predl manifest) rather than
`entry.Main`, producing `flavorPredlPatch` / `flavorPredlFull` (already defined,
`plan_internal.go:17-18`, currently never assigned). Download + `RenameToPredlReady`
use the existing legacy RunUpdate predl branch (`hoyoverse.go:364`).

### §2.3 Genshin (Sophon hoyoverse) — wiring + predl detection
Download/staging backend complete (`runSophonPredownload` + `predlPlan` +
predl-consume). Two pieces are still needed:

1. **`CheckVersion` predl surfacing (cheap probe).** The Sophon branch
   (`hoyoverse.go:154-163`) currently fetches only the tag via `fetchBranchTag`. It
   must use a branch fetch that also returns `PreDownload` (the same
   `getGameBranches` data `checkForUpdateSophon` already consumes at
   `hoyoverse.go:594`) and set
   `info.Predownload = &core.PredownloadInfo{TargetVersion: branch.PreDownload.Tag}`
   when `!branch.PreDownload.IsEmpty()` and the predl tag differs from the local
   version. Without this, Approach A's probe never sets `available_predl` for
   Genshin and the button never appears.
2. **`CheckForPredownload`.** Ensures `gp.predlPlan` is cached (delegates to the
   existing `checkForUpdateSophon`, which already populates it) and returns a plan
   with `Kind = PlanPredownload`. `RunUpdate(PlanPredownload)` then consumes
   `gp.predlPlan` as it does today.

> hoyoverse implements one `CheckForPredownload` that internally routes Sophon vs
> legacy by `g.UsesSophon`, matching `checkForUpdate` (`hoyoverse.go:212`).

## §3 UX (BottomBar)

- **New predl button next to the per-game config gear** (`GameConfigPopover`,
  `BottomBar.vue:211` — the gear left of Play/Update, NOT the titlebar gear).
  Visible when `available_predl != null && !inFlight && !predlReady`. Click →
  `onPredl` → `updates.startPredownload(id)`.
- **In progress:** the predl area shows the existing download progress bar
  (`inFlight.kind === 'predownload'` branch, `BottomBar.vue:202-208`).
- **Staged:** existing `predl_ready` → 「套用預先下載」(`onApplyPredl`) + remove
  (`onRemovePredl`).
- **Stale:** existing `predl_stale` confirm dialog reused.
- **Pill:** blue 「可預先下載」pill retained, now driven by `available_predl`.

### §3.1 Default behaviours (user-approved)
1. Predl button appears only when up-to-date; behind-version shows Update, not predl.
2. Apply is **manual** (user clicks 套用); no auto-apply on launch.
3. Availability uses the cheap Refresh signal to show the button; the real plan
   (with MD5 verification) runs on click — consistent with the Update button.

## §4 Error handling & edge cases

- **Predl target drift / `predl_stale`:** when `available_predl.version` differs
  from a staged `predl_ready.version`, run the existing stale flow; phantom-predl
  self-heal (`update_handler.go:544-561`, delete sidecar when install == predl
  version) is retained.
- **0-file predl plan:** fully staged / no diff → empty plan → `RenameToPredlReady`
  immediately → shows 套用 (or ready). The predl plan path must tolerate 0 files.
- **Cancel / interrupted resume:** ctx-cancel mid-download or crash → existing
  `ScanRecovery` classification (predl_ready vs download progress) + bell-drawer
  resume.
- **Predl manifest 404 / race (predl just pulled):** map to `manifest_not_found`.
  The Refresh probe is best-effort — on any `CheckVersion`/predl error it silently
  clears `available_predl`, no toast (same as the update probe, `update_handler.go:528-531`).
- **Phantom predl (predl version == installed version):** filtered at detection
  (kuro `kurogames.go:162`; hoyo `version.go:170`) + App phantom self-heal.
- **Update precedence:** behind-version → only `AvailableUpdate` set, never
  `available_predl` (mutually exclusive, §1.1).
- **Apply prerequisites:** game not running (process-check), apply lock, Program
  Files admin requirement — identical to update apply; no new handling.

## §5 Components & boundaries

| Unit | Responsibility | Depends on |
|---|---|---|
| `core.PredownloadChecker` | capability interface | core types |
| `App.CheckForUpdate` (extend) | populate/clear `available_predl` from cheap probe | `Provider.CheckVersion` |
| `App.runStartUpdateAsync` (extend) | route predl starts through `PredownloadChecker` | the interface |
| `kurogames.CheckForPredownload` | predl plan vs `idx.Predownload` | existing manifest/download/apply |
| `hoyoverse.CheckVersion` (Sophon branch, edit) | surface `vi.Predownload` from `getGameBranches.PreDownload` | branch fetch |
| `hoyoverse.CheckForPredownload` | predl plan (Sophon cache / legacy `PreDownload`) | existing Sophon + legacy paths |
| `BottomBar.vue` (edit) | predl button by the gear; unify on `available_predl` | updates store |

## §6 Testing

- **Go:** kuro `CheckForPredownload` (httptest fixture with `predownload` block →
  asserts predl version/CDN targeting, correct `plan.Version`); legacy hoyo
  `CheckForPredownload`; App probe three cases (sets `AvailablePredl` when predl
  available + up-to-date; clears when update pending; suppresses when `predl_ready`
  staged); `PredownloadChecker` routing in `runStartUpdateAsync`.
- **Frontend (Vitest):** predl button renders by the gear when `available_predl`
  set; hidden when null; hidden (apply shown) when `predl_ready`; pill state; i18n
  parity for any new keys (3 locales).
- **Whole-repo green:** `go vet ./...`, `go test ./...`, frontend build + vitest,
  `wails build`.
- **USER real-machine smoke:** WuWa currently has an active predl window — button
  appears → download → ready → apply → version writeback, no bounce. Plus one
  HoYoverse game with an open predl window (time-limited; smoke timing matters).

## §7 Out of scope
- Endfield (hypergryph) predownload — see §8.
- Auto-apply-on-launch.
- Mid-file byte-range resume changes beyond what M3.A/M3.B already provide.

## §8 Endfield deferral
M3.C deliberately deferred predl (M3.C.v2 candidate). Endfield's `get_latest`
predl semantics are unverified (whether a predownload package field exists needs a
protocol spike). Adding it requires research + new provider work; tracked as a
separate spec/plan after this lands.
