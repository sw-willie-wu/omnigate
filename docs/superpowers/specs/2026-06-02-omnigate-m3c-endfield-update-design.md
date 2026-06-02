# Omnigate M3.C — Hypergryph / Endfield Update Support Design

Brings the `hypergryph` provider from M2-level (detect / launch / bg + empty version)
up to full game-package update support, mirroring the shipped M3.A `kurogames` (WuWa)
provider. Branch `m3c/spec`, forked from `main` @ `v0.4.0-m3b`.

Status: DRAFT — revised after iter-review round 1 (3 adversarial reviewers; protocol
claims re-verified against the reference repo). Date: 2026-06-02.

---

## §0. Locked decisions (do NOT revisit)

0.1 **Scope = launcher's game-package layer only.** M3.C downloads + applies the
**game-package** archives (`get_latest` → `pkg.packs[]`) to bring an out-of-date
Endfield install to the latest *launchable* version. The **resource / VFS layer**
(`Endfield_Data/StreamingAssets/VFS` `.chk` chunks) is **deferred OUT of M3.C scope by
decision** — NOT because the game handles it. The reference shows the GRYPHLINK
launcher itself drives the VFS layer via a separate `get_latest_resources` endpoint +
HDiffPatch (`patch.json`). We defer it because (a) it is a second, independently-versioned
delivery mechanism with an encrypted index, and (b) a freshly-extracted full
`pkg.packs[]` install is launchable and the client can self-fetch remaining dynamic
assets. VFS sync is a candidate M3.C.v2 / later milestone. This mirrors M3.A/M3.B
shipping the launcher's package layer without the in-client resource updater.

0.2 **Approach A — mirror kurogames, no shared-engine extraction.** New update files
live inside `internal/providers/hypergryph/` with the **same filenames and function
shapes as kurogames' M3.A layout** (verified file set: `update_manifest.go`,
`update_download.go`, `update_apply.go`, `update_progress.go`, `apply_lock*.go`,
`process_check_*.go`). kurogames folds its API client + constants into
`update_manifest.go` (no separate `api.go`), inlines disk/volume preflight into
`update_manifest.go`/`update_apply.go` (no separate `update_preflight.go`), and
**rejects** cross-volume staging via `validateSameVolume` rather than doing a
copy+remove fallback (no `cross_device_*.go`). **M3.C follows kurogames exactly on all
three points.** A future engine extraction ("B") happens after M3.C ships and we have
three real `core.Updater` impls. M3.C does NOT refactor shipped kurogames/hoyoverse
internals.

0.3 **No new shared abstraction.** `core.Updater` + `core.UpdatePlan` / `core.FileTask`
/ `core.UpdateEvent` / `core.UpdateError` already exist and are provider-agnostic; the
App layer already type-asserts `provider.(core.Updater)`, `.(core.CheckForUpdateProgress)`,
`.(core.ProcessChecker)` at RPC entry. hypergryph implements these; **App-layer RPC
handlers, the updates Pinia store, and the BottomBar/SidebarRow/bell UI are reused
unchanged.** The ONLY App-layer edits are the `Settings.TempDir` plumbing in §1.6
(settings struct + provider construction + `tempDirFor` case) — this is real, in-scope
work, NOT "already done."

0.4 **Full packs only; hash = MD5; no hpatchz in M3.C.** `get_latest` (when called
with `version=<current>`) returns BOTH a full `pkg.packs[]` set AND a sibling `patch`
delta set (confirmed in the reference, §2.3). **M3.C consumes the full `pkg.packs[]`
set and ignores `patch`.** Therefore M3.C apply is **unzip-only** (concatenate split
parts → extract); it does NOT need HDiffPatch/hpatchz. Delta consumption is a M3.C.v2
candidate. Hash is MD5 (per-pack `md5` field).

0.5 **Latest version is server-provided; only the LOCAL version is unknown.** The
`get_latest` response carries `version` (the target/latest, e.g. `1.0.14`) and may carry
`client_version`. So "latest" needs no reverse-engineering. The genuine open unknown is
only the *local installed* version source on disk (§2.4-Q1).

0.6 **Protocol reference is public + trusted:** `daydreamer-json/ak-endfield-api-archive`
(archives Endfield's official launcher CDN API + decrypted manifests). Prefer the
reference + archived fixtures over heavy live probing; a few live GETs to confirm the
current `action`/up-to-date shape is acceptable (Endfield is a Kuro/Hyper-class
target). Task 1 is a protocol research spike (mirrors M3.A Task 1) producing
`docs/superpowers/research/m3c-endfield-update-protocol.md` (with `<!-- TEST_ANCHOR -->`
markers per §9) + sanitized fixtures, resolving §2.4 before protocol-dependent tasks.

---

## §1. Package layout & file responsibilities

All files in `internal/providers/hypergryph/` unless noted; filenames parallel
`internal/providers/kurogames/` for faithful mirroring (§0.2).

### §1.1 New files

| File | Responsibility |
|---|---|
| `update_manifest.go` | Launcher API client (`get_latest` GET) + endpoint/appCode/channel constants + a `SetAPIBaseURL` test seam (adopting M3.B/hoyoverse's URL-injection seam — a deliberate improvement over M3.A/kurogames, which hard-codes its URL and `t.Skip`s the seam-dependent integration test); parse response → `[]core.FileTask` from `pkg.packs[]`; `filterChangedFiles` (MD5 vs already-staged); `sanitizeURL`; disk-space + same-volume preflight (inlined, kurogames-style). **No separate `api.go`.** |
| `update_download.go` | 4-worker byte-range download + per-pack MD5 verify + bounded retry (injectable `retryClock`); ctx-aware cancel; progress accounting into `progressStore`. |
| `update_apply.go` | `validateSameVolume` (reject cross-volume → `cross_volume_midrun`); WAL-staged unzip of split packs (`.zip.001..NNN` concat → extract); atomic rename into game dir; write back local version (conditional, §6); post-success cleanup. **Unzip-only, no hpatchz (§0.4); no `cross_device_*.go`.** |
| `update_progress.go` | `progressStore` sidecar I/O (per-provider, like kurogames; reuses `core` recovery/sidecar/WAL helpers — `progressStore` itself stays in-package, it is NOT in `core`). |
| `apply_lock.go` / `apply_lock_windows.go` / `apply_lock_other.go` | `LockFileEx` apply lock (Windows) / noop (other); lock under `tempRoot` (no admin). Mirrors kurogames. |
| `process_check_windows.go` / `process_check_other.go` | `Endfield.exe` running-detection (`CreateToolhelp32Snapshot`) / noop. |

### §1.2 Changed files

| File | Change |
|---|---|
| `version.go` | **Replace** empty-string stub: latest from `get_latest`.`version` (known); local from §2.4-Q1 source (graceful-degrade if none, §6). |
| `hypergryph.go` | Add `core.Updater` (`CheckForUpdate`, `RunUpdate`), `core.CheckForUpdateProgress`, and **mandatory** `core.ProcessChecker` methods; add interface assertions (`var _ core.Updater = …`, `_ core.ProcessChecker = …`). Predownload suppressed (§3.4). |
| `meta.go` | Confirm/add `Endfield.exe` exe-name + `FolderName` constants used by detect/process-check. |

### §1.3 Test files (new) — mirror kurogames' test seams

`update_manifest_test.go`, `update_download_test.go`, `update_apply_test.go`,
`version_test.go`, `update_progress_test.go`, `apply_lock_*_test.go`,
`process_check_test.go`, plus the three kurogames-parity seams: **`errcode_coverage_test.go`**
(every §7 code has a source ref), **`m3c_protocol_doc_test.go`** (TEST_ANCHOR drift
check vs the research doc), **`update_integration_test.go`** (`t.Skip` live placeholders, made
runnable against an `httptest` server via the `SetAPIBaseURL` seam — see §9.2), and a
`sanitize_url_fuzz_test.go`.

### §1.4 `internal/core/` — reused unchanged

`Updater`, `CheckForUpdateProgress`, `UpdatePlan`, `FileTask`, `UpdateEvent`,
`UpdateError`, `ReasonCode`, `Phase`, `PlanKind`, recovery/sidecar/WAL helpers,
`ProcessChecker`, `ParseGameID`. No core changes anticipated; flag in plan if the spike
surfaces a genuine new shared need.

### §1.5 Sidecar / staging layout

Staging + progress sidecars under `tempDirFor("hypergryph", gid)/<version>/` (mirrors
kurogames): `progress.json`, `apply.wal`, `apply.lock`, `*.zip.NNN.part`. Per-`gid`
paths via `core.ParseGameID` so future Hypergryph games (e.g. POPUCOM) stay generic.

### §1.6 App-layer changes (the ONLY ones — §0.3)

1. `internal/app/settings.go`: add `TempDir string` (with `temp_dir,omitempty` TOML tag)
   to `HypergryphSettings`; project it in `LoadSettings` field-by-field (the M2 Task-5
   gotcha — a new field needs an explicit projection line).
2. `internal/app/app.go` `constructProviders`: pass `TempDir` into
   `hypergryph.New(hypergryph.Settings{Path:…, TempDir:…})`.
3. `internal/app/app.go` `tempDirFor`: add a `case "hypergryph"` returning the
   settings override when set (kurogames already has its case; hypergryph currently
   falls through to the default `<TEMP>/omnigate/hypergryph`).
4. `internal/providers/hypergryph/hypergryph.go`: add `TempDir` to `Settings`.

Settings-panel UI already edits `path`/`temp_dir` generically per backend (shipped
v0.4.0-m3b), so no new settings UI is needed.

---

## §2. Protocol layer

### §2.1 Endpoint (global, region `os`; CN parallel via `launcher.hypergryph.com/api`)

Base: `launcher.gryphline.com/api`

`GET /game/get_latest?appcode=<game>&launcher_appcode=<launcher>&channel=<n>&sub_channel=<n>&launcher_sub_channel=<n>&version=<current>`

`version` is **optional** (`semver`-validated when present; omit / `null` → server
echoes `request_version:""` and returns latest). M3.C calls it with the detected local
version to get the staleness signal, and with `version=null` for a pure latest probe in
`CheckVersion`.

### §2.2 Constants (verified against reference `config.ts`)

| Name | Global (os) | Notes |
|---|---|---|
| game appCode | `YDUTE5gscDZ229CW` | matches M2's recorded Endfield CDN folder ID |
| launcher appCode | `TiaytKBUIEdoEwRT` | Epic = `BBWoqCzuZ2bZ1Dro` |
| channel / subChannel | `6` / `6` | Epic sub `801`, GooglePlay sub `802`; CN channel `1`, Bilibili `2` |
| CDN host | `beyond.hg-cdn.com` | host of `pkg.packs[].url` |
| base (os / cn) | `launcher.gryphline.com/api` / `launcher.hypergryph.com/api` | base64-decoded from `config.ts` |

CN (game appCode `6LL0KJuqHBVz33WK`, channel 1) is **out of scope** — global only, matching
the provider's `SupportedRegions: ["global"]`.

### §2.3 `get_latest` response shape (verified from archived fixture)

```jsonc
{
  "action": 1,            // enum — see §2.4-Q2 (all fixtures show 1; up-to-date value unconfirmed)
  "state": 0,             // enum — unconfirmed; carried for completeness
  "launcher_action": 0,   // enum — unconfirmed; carried for completeness
  "version": "1.0.14",        // TARGET / latest version  ← authoritative latest (§0.5)
  "client_version": null,     // sometimes a clean version string; may be null
  "request_version": "1.0.13",// echoes the queried version (re-check seam, §3)
  "pkg": {                    // FULL install set  ← M3.C CONSUMES THIS
    "packs": [ { "url": "...zip.001", "md5": "...", "package_size": "1073741824" }, ... ],
    "total_size": "...", "file_path": "...", "game_files_md5": "..."   // present; only packs[] used
  },
  "patch": {                  // DELTA set from request_version  ← M3.C IGNORES (§0.4)
    "url": "...", "md5": "...", "package_size": "...", "total_size": "...", "file_id": ...,
    "patches": [ { "url": ".../patches/<fromVer>/...zip.001", "md5": "...", "package_size": "..." }, ... ]
  }
}
```

Pack URL path: `…/update/<ch>/<sub>/Windows/<ver>_<rand>/packs/<name>.zip.NNN` on
`beyond.hg-cdn.com`. The `<rand>` segment is embedded in the URL (not separately
sourced).

### §2.4 Mapping to `core` types

- `pkg.packs[i]` → `core.FileTask{ Path:<staged pack filename>, Hash:md5, Size:package_size(int64), URL:url }`.
- `version` → `UpdatePlan.Version`; `request_version` is the staleness echo.
- `UpdatePlan.ManifestETag` = the server `version` string (no HTTP ETag exists for this
  endpoint; **the locked re-check key is the `version` string**, §3).
- `patch`, `pre_patch`, `state`, `launcher_action`, `client_version`, `pkg.total_size`,
  VFS/`res_version` fields → **ignored** in M3.C.

### §2.5 OPEN QUESTIONS — Task 1 spike (BLOCKING for dependent tasks)

1. **Local installed-version source.** M2 found none clean (Endfield.exe FileVersion =
   Unity engine `2021.3.34f5`; `app.info` = name only; GRYPHLINK `<x.y.z>/` = launcher
   version). Re-investigate post-release: a launcher-written config/manifest JSON under
   the game dir, `HKCU\Software\…\Endfield` registry, or a written sidecar. Gates
   `version.go` + CheckForUpdate's `version=` arg. (Latest is NOT unknown — §0.5.)
2. **`action` / `state` / `launcher_action` enum semantics.** All fixtures show
   `1/0/0` (even unversioned queries), so the *up-to-date* response is **unverifiable
   from archived fixtures** — the spike must do a live GET with `version=<latest>` to
   observe it. Determines §3 step-4's "up-to-date" branch detection.
3. **Is the game-package path still live-served?** (M3.B-trap check.) Confirm the
   official launcher still installs via `get_latest` `pkg.packs[]` and hasn't migrated
   to a VFS-only model that would strand this pipeline. If stranded → `protocol_unsupported`
   fallback (§3.4, §7) + M3.C ships as version-detection + check-only.

(Resolved during review, no longer open: full-vs-delta = both, take full (§0.4);
`rand_str` = `get_latest_resources`-only, embedded in pack URL, irrelevant to scope;
manifest re-check key = `version` string (§2.4).)

---

## §3. Decision tree

### §3.1 `CheckForUpdate(gid)` (read-only, idempotent)

```
1. DetectInstall → InstallPath; absent ⇒ ErrGameNotInstalled.
2. Detect local version (version.go, Q1) → curVer (may be "" if no source — see §6).
3. GET get_latest(version=curVer or null).
4. Branch on action/state (Q2):
     up-to-date          → empty UpdatePlan (no Files) → UI [開始遊戲].
     update available     → parse pkg.packs[] → []FileTask.
     stranded/unsupported → *UpdateError{Code:"manifest_not_found"} or, if the whole
                            package path is gone (Q3), the protocol_unsupported fallback (§3.4).
5. filterChangedFiles: drop packs already fully staged + MD5-verified.
6. Return UpdatePlan{Kind:PlanUpdate, Version:rsp.version, Files, TotalBytes:Σsize,
   ManifestETag:rsp.version, Reason:ReasonVersionChanged}.
```
Staleness: `curVer != rsp.version` ⇒ update available. If `curVer==""` (no local
source), rely on the action/state up-to-date signal instead (§6 degrade).

### §3.2 `RunUpdate(plan, onEvent)`

```
1. Re-fetch get_latest; if rsp.version != plan.ManifestETag ⇒ UpdateError{manifest_changed}.
2. Preflight: disk space (⇒ disk_full) + temp/game same-volume (⇒ cross_volume_temp).
3. ProcessChecker: Endfield.exe running ⇒ UpdateError{process_blocked}.
4. PhaseDownload: 4-worker byte-range download packs → progressStore.Persist; cancel-aware.
5. PhaseApply: acquire applyLock → validateSameVolume (⇒ cross_volume_midrun) →
   WAL-staged concat+unzip → atomic rename into game dir → write back local version
   (if a writable sink exists, §6) → release lock → RemoveAll(version dir).
6. Emit UpdateEvent throughout (App throttles ~8 Hz; phase/done/error bypass throttle).
```

### §3.3 Phase/Stage labels

Reuse existing `update.stage.*` keys where applicable (`extracting`, `applying`,
`cleanup`). No new stage keys expected; if a label is genuinely missing, add it to all
three locales + the parity required-list (§8).

### §3.4 Predownload suppression

M3.C targets `PlanUpdate` only; predownload is a M3.C.v2 candidate. Because the App
layer + Pinia store + BottomBar predl button are reused unchanged (§0.3), the provider
MUST make the predl path inert: hypergryph does **not** advertise predownload — the App
predl entry points (`StartPredownload`/`ApplyPredownload`/`RemovePredownload`) detect
the absence (provider does not implement an optional predl capability / returns a
`*UpdateError{Code:"manifest_not_found", Retryable:false}`-style "no predownload"
result) and the BottomBar predl button stays hidden for Hypergryph games. **Plan must
specify the exact suppression mechanism after reading `update_handler.go`'s predl
guards, and add a test asserting the predl button is not shown for a Hypergryph game.**

---

## §4. Download layer

- 4 worker goroutines (mirror kurogames `update_download.go`); per-pack byte-range
  resume via `.part` files; `bytesDone atomic.Int64` summed across workers.
- Per-pack MD5 verify on completion; mismatch → bounded retry (injectable `retryClock`),
  then `UpdateError{corrupt, Retryable:true}`.
- Network/HTTP errors → retry per policy, then `UpdateError{network, Retryable:true}`.
- `progressStore.Persist` holds its lock through the WriteFile/Rename (M3.B Task-14 race
  fix — do NOT release before the file I/O).
- Cancel: ctx checked between packs and inside the range loop; returns `ctx.Err()`.

---

## §5. Apply layer

- `applyLock` (LockFileEx) under `tempRoot/<gid>/<version>/` — **no admin for the lock**
  (M3.A lesson). Writing into a Program Files game dir still needs admin; non-elevated
  failures surface as `UpdateError{apply_partial}` / `{unrecoverable}` with a clear
  message (§7, R4).
- `validateSameVolume`: temp staging must be on the same volume as the game dir for
  atomic rename. Cross-volume is **rejected** (`cross_volume_temp` at preflight,
  `cross_volume_midrun` mid-run) — kurogames model; **no copy+remove fallback**.
- WAL (`apply.wal`) records each pack's apply step; flush every-op (kurogames/hoyoverse
  pattern). On crash, recovery replays from staged packs; the `interrupted_resume_*`
  bell-drawer prompt (existing `update.errors.interrupted_resume_*` keys) fires on next
  launch (reused unchanged).
- Apply: concatenate split parts (`<name>.zip.001..NNN`) → extract archive into a
  staging tree → atomic rename into game dir. **Unzip-only (§0.4).**
- Post-success: write local version (conditional on §6 sink), then `RemoveAll(version dir)`
  so stray `.part`/lock don't trigger spurious `interrupted_resume` next launch.

---

## §6. Version detection (`version.go` rewrite)

`CheckVersion(gid)` returns `core.VersionInfo{Current, Latest}`:
- **Latest** = `get_latest(version=null)`.`version` (known, §0.5).
- **Current** = local source from §2.4-Q1 spike.

Two outcomes from the spike:
- **Writable local source found** → `Current` is real; sidebar shows `就緒 · v<X.Y>`;
  stale-detection compares `Current != Latest`; `RunUpdate` writes the new version back
  to that sink on success.
- **No reliable local source** → graceful degrade: `Current` left empty; staleness is
  driven by the `action`/`state` up-to-date signal from `get_latest` instead; the
  post-apply "write back local version" step (§3.2/§5) becomes a **no-op** (and
  `version_write_failed` is not used — M3.C has no such code). Sidebar shows `就緒`
  with no `· vX.Y` suffix, same as today. Never blocks the update flow.

---

## §7. Error code catalog — reuse the shipped `update.errors.*` (M3.A) set verbatim

Namespace is **`update.errors.*`** (plural — the M3.A namespace; M3.B's `update.error.*`
singular set is separate and NOT used here). Frontend renders via
`update.errors.<code>`. All codes below already exist in all three locales and in the
`i18n_parity.test.ts` required-list — **M3.C adds NO new i18n keys** unless the §3.4
predl-suppression or Q3 stranded fallback forces one (§8).

| Code (existing) | M3.C trigger |
|---|---|
| `manifest_changed` | `rsp.version` mismatch at RunUpdate entry |
| `manifest_not_found` | `get_latest` returns no usable pkg / 404 |
| `network` | transport failure after retries |
| `corrupt` | pack MD5 mismatch after retries (= kurogames `corrupt`, NOT `hash_mismatch`) |
| `disk_full` | preflight short on space |
| `cross_volume_temp` | temp staging not same volume as game dir (preflight) |
| `cross_volume_midrun` | cross-volume detected mid-apply |
| `unsupported_filesystem` | staging volume FS can't support `.part` resume semantics (carry M3.A check) |
| `process_blocked` | `Endfield.exe` running at apply |
| `apply_partial` | apply interrupted / partial rename |
| `unrecoverable` | WAL replay can't recover (e.g. admin-perm writeback failure) |
| `internal` | unexpected internal error |
| `interrupted_resume_download` / `interrupted_resume_apply` | crash-recovery resume prompts (reused) |

**`errcode_coverage_test.go`** asserts every code M3.C emits has a source reference,
mirroring kurogames.

If Q3's stranded fallback is needed, add a single new code `protocol_unsupported` with
literal zh-TW / zh-CN / en values AND register it in BOTH `i18n_parity.test.ts` required
lists; otherwise reuse `manifest_not_found`.

---

## §8. Frontend

**No new components.** Endfield flows through the existing updates Pinia store +
BottomBar 8-state matrix + SidebarRow inline progress + Topbar bell drawer
(provider-agnostic since M3.A).

- i18n: M3.C expects **zero new keys** (§7 reuses existing `update.errors.*`). The
  predl button must stay hidden for Hypergryph (§3.4), and any genuinely-new key (only
  `protocol_unsupported`, contingent) MUST be added to en / zh-TW / zh-CN **and** the
  `i18n_parity.test.ts` required-list (the test asserts identical key sets + non-empty
  values across all three locales — a missing key fails CI immediately).
- Vitest: extend i18n parity coverage if a new key lands; add a test asserting the
  predl button is not rendered for a Hypergryph game (§3.4). No other new component test
  expected.

---

## §9. Testing strategy

TDD red→green per task; `CGO_ENABLED=0`, no `-race` (`feedback_no_cgo_race`).

### §9.1 Unit
- `update_manifest_test.go`: real fixtures from `ak-endfield-api-archive` (`get_latest`
  response incl. both `pkg` + `patch`; verify only `pkg.packs[]` is consumed) sanitized
  into `testdata/`; tests pack parse → FileTask, `filterChangedFiles` MD5 compare,
  `sanitizeURL`, disk/same-volume preflight.
- `version_test.go`: local-version detection for the spike-confirmed source AND the
  no-source graceful-degrade path (§6).
- `update_download_test.go`: `httptest` server serving fake split packs; 4-worker,
  byte-range resume, MD5 verify (`corrupt`), retry, cancel.
- `update_apply_test.go`: WAL apply, split concat + unzip, atomic rename,
  `validateSameVolume` reject (`cross_volume_midrun`), applyLock, post-success cleanup.
- `apply_lock_*_test.go`, `process_check_test.go` (asserts `core.ProcessChecker`
  implemented + detects a running exe via a test double).
- **`errcode_coverage_test.go`** (every §7 code referenced) and **`m3c_protocol_doc_test.go`**
  (TEST_ANCHOR drift vs the research doc) — the kurogames-parity seams.

### §9.2 Integration / fuzz / bench
- `update_integration_test.go`: `t.Skip` live placeholders + a `SetAPIBaseURL` URL-
  injection seam adopted from M3.B/hoyoverse (NOT M3.A — kurogames hard-codes its URL
  and skips the seam-dependent test; M3.C improves on that so the manifest/download
  flow is exercisable against an `httptest` server).
- Fuzz: `sanitizeURL` + manifest parser. Bench: download worker-pool throughput.

### §9.3 Smoke (USER — milestone close; conditional steps noted)
On a real Endfield install, an M3.A-shaped checklist: [開始遊戲] at latest → fake-stale
local version *(only if Q1 found a spoofable/writable source; otherwise skip per §6)* →
[更新遊戲] appears → download progress visible → cancel mid-download → real pack download +
unzip + apply (+ version writeback if §6 sink exists) → i18n toggle → crash-recovery
resume. Plus a Program-Files-install admin-writeback check.

---

## §10. Risks & deviations

- **R1 — protocol drift / VFS-only migration** (§2.5-Q3): biggest risk; Task 1 spike
  must confirm the package path is live before protocol-dependent tasks. Mitigation:
  `protocol_unsupported`/`manifest_not_found` fallback; version-detection still ships.
- **R2 — no clean local-version source** (§2.5-Q1): mitigation = graceful degrade (§6);
  M3.C ships even if stale-detection leans on the server up-to-date signal.
- **R3 — `action` up-to-date value unknown** (§2.5-Q2): fixtures only ever show `1/0/0`;
  spike must observe a live up-to-date response. Until then §3 step-4 detection is
  provisional.
- **R4 — Program Files admin** (M3.A-known): writeback into a Program Files game dir
  needs admin; documented runtime constraint, clear `apply_partial`/`unrecoverable`
  error, not a blocker. Possible future UAC self-elevation (shared w/ M3.A.v2).
- **D1** — delta `patch` consumption deferred to M3.C.v2 (§0.4).
- **D2** — VFS resource layer deferred (§0.1). **D3** — CN region deferred (§2.2).
  **D4** — predownload deferred (§3.4).

---

## §11. Authorization & workflow

Task-by-task with user checkpoints (NOT a batch grant; M3.B v2 batch autonomy EXPIRED).
Pipeline: this spec → iter-review (done: round 1) → user spec review → writing-plans →
subagent-driven-development execution (Task 1 = protocol spike, unblocks the
protocol-dependent set) → USER smoke + tag `v0.5.0-m3c` + `--no-ff` merge to main
(`feedback_commits`: no Co-Authored-By trailer).
