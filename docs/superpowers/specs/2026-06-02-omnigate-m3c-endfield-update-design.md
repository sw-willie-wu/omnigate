# Omnigate M3.C — Hypergryph / Endfield Update Support Design

Brings the `hypergryph` provider from M2-level (detect / launch / bg + empty version)
up to full game-package update support, mirroring the shipped M3.A `kurogames` (WuWa)
provider. Branch `m3c/spec`, forked from `main` @ `v0.4.0-m3b`.

Status: REVISED — Phase A SHIPPED (version detection via config.ini AES). Phase B
re-scoped to **Option A: file-level incremental** after reading the Collapse plugin
source (`misaka10843/Hi3Helper.Plugin.Hypergryph` — `HgGameInstaller.Install.cs`,
`HgGameManager.cs`, `HgGameRepairer.cs`, `HgApiStructs.cs`). The source revealed the
plugin's "binary-diff incremental" path applies HDiffPatch to the **VFS layer** (the
exact thing §0.1 defers), and that its *repair* layer (decrypt `game_files` AES manifest
→ per-file MD5 verify → per-file CDN re-download) is itself a complete file-level
incremental mechanism. M3.C Phase B mirrors **that repair layer** as the primary update
path — no zip-extract, no HDiffPatch, no VFS patching — reusing M3.A `kurogames`
`filterChangedFiles`/download/apply almost verbatim. Date: 2026-06-02.

---

## §0. Locked decisions (do NOT revisit)

0.1 **Scope = sync the game-package file tree to the latest version.** M3.C brings an
out-of-date Endfield install up to the latest *launchable* version by syncing its
on-disk file tree to the new version's **`game_files` manifest** (the per-file
`{path, md5, size}` list the official launcher itself verifies against — Collapse
`HgGameRepairer.cs`). The **in-client runtime VFS hot-update** (`get_latest_resources`
`.chk` chunks the game exe self-fetches *after launch*) is **deferred OUT of M3.C scope
by decision** — it is the game exe's own runtime subsystem (Addressables-style), an
independently-versioned delivery mechanism with an encrypted index, and M3.A/M3.B never
did the in-client layer either. Under Option A there is **no contradiction**: M3.C syncs
the package-time file tree (which includes whatever VFS files are in `game_files` at
package time); the runtime VFS hot-update remains the game's job. VFS-runtime sync is a
candidate M3.C.v2 / later milestone.

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

0.4 **File-level incremental via the `game_files` manifest; hash = MD5; no zip-extract,
no hpatchz.** M3.C does **NOT** consume `get_latest`'s `pkg.packs[]` (full split zips)
or `patch.patches[]` (delta zips). Instead it mirrors the Collapse plugin's **repair
layer**: fetch the new version's `game_files` manifest from the per-file CDN
(`{pkg.file_path}/game_files`), AES-decrypt it (same key/IV as config.ini) into a
JSON-lines `{path, md5, size}` list, MD5-compare against the local install, and download
**only the changed/missing files** individually from `{pkg.file_path}/<path>`. Apply is
per-file atomic rename into the game dir (kuro model) — **no zip concatenation, no
archive extraction, no HDiffPatch/hpatchz, no `patch.json`, no `MultiVolumeStream`.**
The **full** case is the *same* per-file path with the MD5 filter disabled (download
every `game_files` entry) — a degenerate case, not a separate code path. **Note this
full-via-per-file path is omnigate-original:** the reference's *repair* layer only ever
downloads the broken subset, and its *full-install* path uses zip packs + extract (which
we reject). M3.C never does a fresh from-scratch install (the game is already installed;
we update it), so per-file-for-everything is only a fallback, validated at smoke (§2.5-Q3).
Hash is MD5 (per-file `md5` + `game_files_md5`). The binary-diff /
VFS-HDiffPatch path (Collapse `IsDeltaUpdate` branch) is **rejected for M3.C** (drags the
VFS layer into scope, needs hpatchz + 7z multi-volume + the un-reverse-engineered
`v2_patch_info` format) and is a M3.C.v2 candidate.

0.5 **Both versions are now resolved (Phase A).** Latest = `get_latest`.`version`
(server-provided). Local = `<gameDir>/config.ini` `version=` line, AES-256-CBC decrypted
(Phase A SHIPPED, verified live → `1.2.5`). **Staleness = `local != latest`
(version-string compare is authoritative).** `action` is NOT relied on for the
up-to-date decision: every observed fixture (9 archive + the live GET) shows `action==1`
*including* queries that returned the latest, and the up-to-date `action` value was never
observed (§2.5-Q2). We therefore use `action==1` ONLY as a fallback hint when the local
version is unreadable (degrade, §6) — never to override a version match. (Collapse
`HgGameManager.HasUpdate = versionDiffers || action==1`; we deliberately diverge on the
`action` term because we cannot confirm its up-to-date value and a false `action==1`
would strand a freshly-updated install in a permanent update prompt — see §3.1/§6.)

0.6 **Protocol references are public + trusted:** (a) the Collapse Launcher plugin
`misaka10843/Hi3Helper.Plugin.Hypergryph` is the authoritative reference for the
**mechanism + crypto + struct shapes** (AES key/IV, config.ini, `game_files`, the repair
flow Option A mirrors; trust per project policy, same as M3.B's Collapse Sophon
reference). ⚠️ **Caveat — wire envelope differs:** the plugin calls `get_latest` via a
`POST …/batch_proxy` with an `{seq, proxy_reqs:[{kind:"get_latest_game", …}]}` body and a
**wrapped** `proxy_rsps[].get_latest_game_rsp` response (`HgGameManager.cs`,
`HgApiStructs.cs`). Omnigate instead ships the **flat `GET /game/get_latest?…`** variant
(Phase A — **live-verified** flat response 2026-06-02). Both return the same
discriminating fields (`action`, `version`, `pkg.file_path`); we treat the plugin as a
*struct-shape* reference, not a verbatim request mirror. (b) `daydreamer-json/ak-endfield-api-archive`
(archived `get_latest` responses + decrypted manifests). The protocol research spike is
**DONE** (Phase A):
`docs/superpowers/research/m3c-endfield-update-protocol.md` (with `<!-- TEST_ANCHOR -->`
markers per §9) + sanitized fixtures + the resolved AES params. A few live GETs confirmed
the flat `get_latest` shape.

---

## §1. Package layout & file responsibilities

All files in `internal/providers/hypergryph/` unless noted; filenames parallel
`internal/providers/kurogames/` for faithful mirroring (§0.2).

### §1.1 New files

| File | Responsibility |
|---|---|
| `update_manifest.go` | Launcher API client (`get_latest` GET — Phase A has the client + response structs) + endpoint/appCode/channel constants + the `SetAPIBaseURL` test seam (Phase A); **fetch + AES-decrypt the new version's `game_files` manifest** from `{pkg.file_path}/game_files` → JSON-lines `{path, md5, size}`; map to `[]core.FileTask{Path, Hash:md5, Size, URL:{pkg.file_path}/<path>}`; `filterChangedFiles` (per-file MD5 vs the local install, kuro-style — drop files already size+MD5-matching on disk; skip the `config.ini` entry); `sanitizeURL`; disk-space + same-volume preflight (inlined, kurogames-style). **No separate `api.go`.** |
| `crypto.go` | (Phase A) AES-256-CBC config.ini **decrypt** — Phase B extends with: `game_files` decrypt (same key/IV) and config.ini **re-encrypt** for version writeback (§5/§6). |
| `update_download.go` | 4-worker per-file download (single GET per file → `.part` → rename; kuro model — **NOT** mid-file byte-range resume, which kuro's `singleDownload` also lacks; a partial `.part` is re-fetched whole) + per-file MD5 verify + bounded retry (injectable `retryClock`); ctx-aware cancel; progress accounting into `progressStore`. Mirrors kuro `update_download.go` (per-file, not per-pack). |
| `update_apply.go` | `validateSameVolume` (reject cross-volume → `cross_volume_midrun`); WAL-staged per-file atomic rename into the game dir (downloaded file → `<gameDir>/<path>`, replacing in place); write back local version via config.ini **AES re-encrypt** (conditional, §6); post-success cleanup. **No zip/concat/extract, no hpatchz (§0.4); no `cross_device_*.go`.** |
| `update_progress.go` | `progressStore` sidecar I/O (per-provider, like kurogames; reuses `core` recovery/sidecar/WAL helpers — `progressStore` itself stays in-package, it is NOT in `core`). |
| `apply_lock.go` / `apply_lock_windows.go` / `apply_lock_other.go` | `LockFileEx` apply lock (Windows) / noop (other); lock under `tempRoot` (no admin). Mirrors kurogames. |
| `process_check_windows.go` / `process_check_other.go` | `Endfield.exe` running-detection (`CreateToolhelp32Snapshot`) / noop. |

### §1.2 Changed files

| File | Change |
|---|---|
| `version.go` | (Phase A SHIPPED) latest from `get_latest`.`version`; local from config.ini AES decrypt (graceful-degrade if unreadable, §6). Phase B only touches it if the staleness wiring needs adjustment. |
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
kurogames): `progress.json`, `apply.wal`, `apply.lock`, and per-file `<path>.part`
download temporaries (directory tree mirrors the game-relative paths). Per-`gid` paths
via `core.ParseGameID` so future Hypergryph games (e.g. POPUCOM) stay generic.

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
| CDN host | `beyond.hg-cdn.com` | host of `pkg.file_path` (per-file `game_files` + downloads) |
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
  "pkg": {                    // ← M3.C CONSUMES file_path (+ derives game_files manifest)
    "packs": [ ... ],         //   IGNORED (full split zips — Option B only)
    "total_size": "...",
    "file_path": "https://beyond.hg-cdn.com/<appcode>/<ver>/.../files",  // ← per-file CDN base
    "game_files_md5": "..."   // aggregate MD5 of the install tree (informational)
  },
  "patch": { ... }            // DELTA zip set  ← M3.C IGNORES (§0.4)
}
```

**`game_files` manifest** (the consumed artifact): `GET {pkg.file_path}/game_files` →
AES-256-CBC-encrypted body → decrypt (same key/IV as config.ini) → **JSON-lines**, one
object per installed file: `{"path":"...","md5":"<hex>","size":<int>}`. Per-file download
URL = `{pkg.file_path}/<path>`. (Source: Collapse `HgGameRepairer.StartRepairAsync` +
`HgManifestNode`.) The `config.ini` entry is **skipped** (its version is written
separately, §5/§6).

> The reference reads a *local* `<gameDir>/game_files` first and only CDN-fetches on
> miss — but the local copy is the **stale** (currently-installed) manifest. For an
> *update* we need the **new** version's manifest, so M3.C fetches from CDN
> (`{pkg.file_path}/game_files`) unconditionally; the reference's disk-first read is a
> repair optimization that does not apply here.

### §2.4 Mapping to `core` types

- `game_files` node `{path, md5, size}` → `core.FileTask{ Path:path, Hash:md5,
  Size:size, URL:{pkg.file_path}/path }` (after `filterChangedFiles` drops on-disk
  matches; `config.ini` entry skipped).
- `get_latest`.`version` → `UpdatePlan.Version`; local config.ini `version` is the
  staleness comparand.
- `UpdatePlan.ManifestETag` = the server `version` string (no HTTP ETag exists for this
  endpoint; **the re-check key is the `version` string**, §3).
- `pkg.packs`, `patch`, `pre_patch`, `state`, `launcher_action`, `client_version`,
  `pkg.total_size`, VFS/`res_version` fields → **ignored** in M3.C.

### §2.5 Spike questions — RESOLVED (Phase A + source read)

1. **Local installed-version source — RESOLVED.** `<gameDir>/config.ini`, AES-256-CBC
   decrypted → `version=` line. Phase A SHIPPED + smoke-verified (`1.2.5`).
2. **Up-to-date discriminator — RESOLVED via version compare; `action` up-to-date value
   intentionally NOT depended on.** Staleness = `local config.ini version != get_latest
   version` (authoritative, feasible because local version is readable). The
   `action`/`state`/`launcher_action` enums show `1/0/0` across all 9 archive samples +
   the live GET — *including* latest-version queries — so the up-to-date `action` value
   was **never observed** and may well stay `1`. We therefore do NOT use `action==1` to
   flag staleness when the version is known (doing so would loop a freshly-updated install
   forever — §3.1/§6/B3-B4 review). `action==1` is used only as a degrade-mode hint when
   the local version is unreadable (§6). A live post-update GET could later confirm the
   up-to-date `action`; until then we don't rely on it.
3. **Per-file CDN live? — confirm at smoke (low risk).** Option A relies on
   `{pkg.file_path}/game_files` (the manifest) and `{pkg.file_path}/<path>` (per-file
   downloads) being served — exactly what the Collapse plugin's `HgGameRepairer` depends
   on, so high confidence. The **only** smoke-blocking unknown is whether the per-file
   CDN serves *every* `game_files` path on demand (vs. only a repair subset). If a path
   404s mid-update → `manifest_not_found`/`network` with a clear message; if systematically
   stranded → `protocol_unsupported` fallback (§3.4, §7) + M3.C ships as Phase A
   (version-detection + check-only). Task B-smoke confirms.

(No longer open: full-vs-delta — neither; Option A uses the per-file `game_files` repair
mechanism (§0.4). `rand_str` / pack URLs — irrelevant under Option A. Manifest re-check
key = `version` string (§2.4).)

---

## §3. Decision tree

### §3.1 `CheckForUpdate(gid)` (read-only, idempotent)

```
1. DetectInstall → InstallPath; absent ⇒ ErrGameNotInstalled.
2. Read local version (version.go: config.ini AES decrypt, Phase A) → curVer
   (may be "" if config.ini unreadable — see §6 degrade).
3. GET get_latest(version=curVer or "").
4. Staleness — **version compare is authoritative** (§0.5, §2.5-Q2); `action` is NOT used
   when curVer is known:
     up-to-date:        curVer != "" AND curVer == rsp.version
                          → empty UpdatePlan (no Files) → UI [開始遊戲]. Return early.
     update available:  curVer != "" AND curVer != rsp.version  → fetch manifest.
     degrade (no ver):  curVer == ""  → treat as update-available IFF action==1,
                          then verify the whole manifest (§6).
   no usable rsp.pkg.file_path / manifest 404 ⇒ *UpdateError{manifest_not_found}; if the
        per-file CDN is systematically stranded (Q3) ⇒ protocol_unsupported (§3.4).
5. filterChangedFiles: for each manifest node, keep iff the on-disk file is absent
   OR size-mismatched OR MD5-mismatched (skip config.ini node). Matches ⇒ dropped.
6. Return UpdatePlan{Kind:PlanUpdate, Version:rsp.version, Files (URL={file_path}/<path>),
   TotalBytes:Σsize, ManifestETag:rsp.version, Reason:ReasonVersionChanged}.
```
**Two-tier cost (M3.A pattern, reused):** the *CheckForUpdate RPC* (App, on Refresh) runs
only the cheap `CheckVersion` (config.ini decrypt + one get_latest GET, **no MD5**) and
sets `AvailableUpdate` purely on the version compare. The heavy per-file MD5 verify of the
local install (steps 5–6, GB-class tree) runs **only on the [更新] click** via
`CheckForUpdateWithProgress`, surfaced through the existing `core.CheckForUpdateProgress`
"verifying X/Y" UI. So a freshly-updated install (curVer == latest) never triggers the
verify pass and never bounces back to [更新] — provided post-apply writeback persisted the
new version (§6, B3).

### §3.2 `RunUpdate(plan, onEvent)`

```
1. Re-fetch get_latest; if rsp.version != plan.ManifestETag ⇒ UpdateError{manifest_changed}.
2. Preflight: disk space (⇒ disk_full) + temp/game same-volume (⇒ cross_volume_temp).
3. ProcessChecker: Endfield.exe running ⇒ UpdateError{process_blocked}.
4. PhaseDownload: 4-worker per-file single-GET download → tempRoot/<version>/<path>.part;
   per-file MD5 verify (⇒ corrupt); progressStore.Persist; cancel-aware.
5. PhaseApply: acquire applyLock → validateSameVolume (⇒ cross_volume_midrun) →
   WAL-staged per-file atomic rename into game dir (replace in place, creating dirs) →
   write back local version via config.ini AES re-encrypt (§6) → release lock →
   RemoveAll(version dir).
6. Emit UpdateEvent throughout (App throttles ~8 Hz; phase/done/error bypass throttle).
```

### §3.3 Phase/Stage labels

Reuse existing `update.stage.*` keys where applicable (`verifying`, `applying`,
`cleanup` — note **no `extracting`** stage under Option A, since there is no archive
extraction). No new stage keys expected; if a label is genuinely missing, add it to all
three locales + the parity required-list (§8).

### §3.4 Predownload suppression

M3.C targets `PlanUpdate` only; predownload is a M3.C.v2 candidate. Suppression requires
**zero provider action** — the BottomBar predl button is gated purely on *data presence*,
not on any provider opt-out signal:
- the button shows only when `state.AvailablePredl` is populated (kurogames sets this via
  a `CheckForUpdate` returning `Kind:PlanPredownload`) **or** the optional `predlExposer`
  interface reports `has_predownload` (only hoyoverse implements it).
- hypergryph implements **neither** → `AvailablePredl` stays empty, `predlExposer` is not
  satisfied → the button is hidden automatically.

So the provider does **not** return any "no predownload" error (the earlier
`manifest_not_found`-style instruction was wrong) and adds no predl guard. The §8 Vitest
simply asserts the predl button is not rendered given a Hypergryph snapshot with no
`available_predl`/`has_predownload`. (Plan should still confirm the exact gating fields in
`BottomBar.vue` + `update_handler.go` before writing the test.)

---

## §4. Download layer

- 4 worker goroutines (mirror kurogames `update_download.go`); per-file single-GET
  download into `<path>.part` temporaries (kuro `singleDownload` model — a partial
  `.part` is re-fetched whole, not byte-range resumed; mid-file resume is an optional
  M3.C.v2 improvement the Collapse plugin has but M3.A does not); `bytesDone atomic.Int64`
  summed across workers.
- Per-file MD5 verify on completion; mismatch → bounded retry (injectable `retryClock`),
  then `UpdateError{corrupt, Retryable:true}`.
- Network/HTTP errors → retry per policy, then `UpdateError{network, Retryable:true}`. A
  per-file 404 (path not on the CDN) → `manifest_not_found` (the Q3 stranded signal).
- `progressStore.Persist` holds its lock through the WriteFile/Rename (M3.B Task-14 race
  fix — do NOT release before the file I/O).
- Cancel: ctx checked between files and inside the range loop; returns `ctx.Err()`.
- File count can be large (thousands of small files for a point release); the worker
  pool + throttled progress keep the UI responsive. (Many small HTTP GETs vs. one big
  zip is the Option-A tradeoff — accepted; we update existing installs, not fresh ones.)

---

## §5. Apply layer

- `applyLock` (LockFileEx) under `tempRoot/<gid>/<version>/` — **no admin for the lock**
  (M3.A lesson). Writing into a Program Files game dir still needs admin; non-elevated
  failures surface as `UpdateError{apply_partial}` / `{unrecoverable}` with a clear
  message (§7, R4).
- `validateSameVolume`: temp staging must be on the same volume as the game dir for
  atomic rename. Cross-volume is **rejected** (`cross_volume_temp` at preflight,
  `cross_volume_midrun` mid-run) — kurogames model; **no copy+remove fallback**.
- WAL (`apply.wal`) records each file's apply step; flush every-op (kurogames/hoyoverse
  pattern). On crash, recovery replays from the staged `.part`/verified files; the
  App-layer crash-recovery bell-drawer prompt (§7 — App-derived, not provider-emitted)
  fires on next launch (reused unchanged).
- Apply: for each verified downloaded file, atomic-rename it to `<gameDir>/<path>`
  (creating parent dirs, replacing the old file in place). **No zip/concat/extract (§0.4);
  no HDiffPatch.** (Endfield files are already whole — `game_files` lists complete files,
  not diffs.)
- Version writeback (**omnigate-original — NOT mirrored**): the reference copies a
  server-supplied `config.ini.new` (delta path only) and its repair-only path writes
  nothing. M3.C instead re-encrypts the *existing* config.ini in place (AES, same key/IV)
  with the updated `version=` line. This runs on **every** successful apply (config.ini is
  a confirmed-writable sink, Phase A), mirroring kurogames' *unconditional* post-apply
  version write — which exists precisely so a 0-file or successful apply does not leave a
  stale version that re-flags `AvailableUpdate` and bounces the UI back to [更新] (the
  staleness loop, §6/B3). A writeback failure surfaces a clear non-fatal warning (the
  apply itself already succeeded); it is not silently dropped. Then `RemoveAll(version
  dir)` so stray `.part`/lock don't trigger spurious `interrupted_resume` next launch.

---

## §6. Version detection (`version.go` rewrite)

`CheckVersion(gid)` returns `core.VersionInfo{Current, Latest}` (Phase A SHIPPED):
- **Latest** = `get_latest(version="")`.`version`.
- **Current** = `<gameDir>/config.ini` AES-256-CBC decrypt → `version=` line.

Resolved source (no spike branch remains):
- **Normal (source readable — the expected case, Phase A proved config.ini is readable +
  writable)** → `Current` is real; sidebar shows `就緒 · v<X.Y>`; stale-detection compares
  `Current != Latest` (version-only, NOT `action`); on `RunUpdate` success the new version
  is **always** written back by AES-re-encrypting config.ini (same key/IV). Because
  `Current` then equals `Latest`, the next check shows up-to-date — no bounce.
- **Degrade (config.ini missing/undecryptable)** → `Current` left empty; staleness leans
  on `action==1` + a full-manifest verify; the post-apply writeback is a **no-op** (no
  sink, no `version_write_failed` code). **Known limitation:** if config.ini stays
  unreadable AND `action` stays `1`, such an install may re-prompt for update after a
  successful apply (we cannot persist the new version without the sink). This is a true
  edge — config.ini was readable in Phase A's smoke; if it ever isn't, we accept the
  re-prompt rather than block. Sidebar shows `就緒` (no `· vX.Y`). Never blocks the flow.

---

## §7. Error code catalog — reuse the shipped `update.errors.*` (M3.A) set verbatim

Namespace is **`update.errors.*`** (plural — the M3.A namespace; M3.B's `update.error.*`
singular set is separate and NOT used here). Frontend renders via
`update.errors.<code>`. All codes below already exist in all three locales and in the
`i18n_parity.test.ts` required-list — **M3.C adds NO new i18n keys** unless the Q3
stranded fallback forces `protocol_unsupported` (§8). (§3.4 predl-suppression forces no
key — it is zero-action.)

| Code (existing) | M3.C trigger |
|---|---|
| `manifest_changed` | `rsp.version` mismatch at RunUpdate entry |
| `manifest_not_found` | `get_latest` returns no usable `pkg.file_path` / `game_files` 404 / a per-file path 404s (stranded) |
| `network` | transport failure after retries |
| `auth_failed` | `get_latest` returns 401/403 (already emitted by Phase A `fetchGetLatest`; in the `i18n_parity` required-list) |
| `corrupt` | per-file MD5 mismatch after retries (= kurogames `corrupt`, NOT `hash_mismatch`); also a failed `game_files` AES decrypt |
| `disk_full` | preflight short on space |
| `cross_volume_temp` | temp staging not same volume as game dir (preflight) |
| `cross_volume_midrun` | cross-volume detected mid-apply |
| `unsupported_filesystem` | staging volume FS can't support `.part` resume semantics (carry M3.A check) |
| `process_blocked` | `Endfield.exe` running at apply |
| `apply_partial` | apply interrupted / partial rename |
| `unrecoverable` | WAL replay can't recover (e.g. admin-perm writeback failure) |
| `internal` | unexpected internal error |

**Crash-recovery prompts are NOT provider-emitted codes.** The provider never emits
`interrupted_resume*`. Recovery is surfaced entirely by the **App layer** (reused
unchanged): `ScanRecovery` → `applyRecoveryState` sets `UpdateError{Code:"interrupted_resume",
Params:{phase, wasPredl}}`, and the **frontend** derives the `interrupted_resume_download`
/ `interrupted_resume_apply` i18n key from those params (`useResumePrompt.ts`). M3.C only
ensures `scanForRecovery`'s `knownBackendIDs` includes `hypergryph` (it already does) and
that the provider writes the WAL/progress sidecars the scanner reads.

**`errcode_coverage_test.go`** asserts every code **the provider emits** has a source
reference, mirroring kurogames — so it covers the table above (incl. `auth_failed`) but
NOT the App-layer-derived `interrupted_resume*` keys.

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
- `update_manifest_test.go`: a sanitized `game_files` fixture (AES-encrypted bytes whose
  plaintext is JSON-lines `{path,md5,size}`, generated in-test from the known key/IV) +
  the `get_latest` `testdata/` fixture (Phase A); tests manifest decrypt → nodes →
  FileTask (URL = `{file_path}/<path>`, `config.ini` skipped), `filterChangedFiles` MD5
  compare against an on-disk tree, `sanitizeURL`, disk/same-volume preflight.
- `crypto_test.go`: AES round-trip — config.ini decrypt (Phase A) + the new `game_files`
  decrypt + config.ini **re-encrypt→decrypt** equality (version writeback path).
- `version_test.go`: local-version detection (config.ini AES, Phase A) AND the
  config.ini-unreadable graceful-degrade path (§6).
- `update_download_test.go`: `httptest` server serving fake per-file payloads; 4-worker,
  partial-`.part` re-fetch (NOT byte-range), MD5 verify (`corrupt`), per-file 404 →
  `manifest_not_found`, retry, cancel.
- `update_apply_test.go`: WAL apply, per-file atomic rename (replace in place + dir
  creation), `validateSameVolume` reject (`cross_volume_midrun`), config.ini AES
  re-encrypt writeback, applyLock, post-success cleanup.
- `apply_lock_*_test.go`, `process_check_test.go` (asserts `core.ProcessChecker`
  implemented + detects a running exe via a test double).
- **`errcode_coverage_test.go`** (every §7 code referenced) and **`m3c_protocol_doc_test.go`**
  (TEST_ANCHOR drift vs the research doc) — the kurogames-parity seams.

### §9.2 Integration / fuzz / bench
- `update_integration_test.go`: `t.Skip` live placeholders + the Phase-A `SetAPIBaseURL`
  URL-injection seam (adopted from M3.B/hoyoverse). **Note the seam covers `get_latest`
  only;** the per-file `game_files`/download URLs come from `rsp.pkg.file_path` (an
  absolute URL in the response body), so to exercise the download/apply flow against
  `httptest` the test must serve a `get_latest` fixture whose `pkg.file_path` points at
  the test server. **Concrete Phase-B work:** the Phase-A `getLatestResponse` struct has
  no `pkg.file_path` / `game_files_md5` fields yet — Phase B adds the `pkg` object
  (`file_path` at minimum) to the struct + parser.
- Fuzz: `sanitizeURL` + `game_files` JSON-lines manifest parser. Bench: download
  worker-pool throughput.

### §9.3 Smoke (USER — milestone close)
On a real Endfield install, an M3.A-shaped checklist: [開始遊戲] at latest → fake-stale
local version (re-encrypt config.ini with an older `version=` — writable sink confirmed
in Phase A) → [更新遊戲] appears → per-file MD5 verify progress visible → download progress
→ cancel mid-download → real per-file download + atomic apply + config.ini version
writeback → i18n toggle → crash-recovery resume. Plus a Program-Files-install
admin-writeback check. **Confirms the Q3 risk:** that the per-file CDN serves every
`game_files` path for a real version delta.

---

## §10. Risks & deviations

- **R1 — per-file CDN coverage** (§2.5-Q3): the one smoke-blocking unknown — does
  `{pkg.file_path}` serve *every* `game_files` path (not just a repair subset)? High
  confidence (Collapse `HgGameRepairer` depends on it). Mitigation: a per-file 404 →
  `manifest_not_found`/`network`; systematic stranding → `protocol_unsupported` fallback +
  M3.C ships as Phase A. Task B-smoke confirms.
- **R2 — large file count / many small GETs** (Option-A tradeoff): a point release may
  touch thousands of files; per-file HTTP is slower than one big zip. Accepted (we update
  existing installs). Mitigation: 4-worker pool + throttled progress; the heavy local-MD5
  verify uses the existing `CheckForUpdateProgress` "verifying X/Y" UI.
- **R3 — config.ini writeback robustness**: AES re-encrypt must round-trip byte-stable
  enough for the official launcher to re-read. Mitigation: `crypto_test.go` round-trip;
  on any writeback failure, degrade to no-op (§6) — never blocks the apply.
- **R4 — Program Files admin** (M3.A-known): writeback into a Program Files game dir
  needs admin; documented runtime constraint, clear `apply_partial`/`unrecoverable`
  error, not a blocker. Possible future UAC self-elevation (shared w/ M3.A.v2).
- **D1** — binary-diff / VFS-HDiffPatch update (Collapse `IsDeltaUpdate` path) deferred
  to M3.C.v2 (§0.4).
- **D2** — in-client runtime VFS hot-update deferred (§0.1). **D3** — CN region deferred
  (§2.2). **D4** — predownload deferred (§3.4).

---

## §11. Authorization & workflow

Task-by-task with user checkpoints (NOT a batch grant; M3.B v2 batch autonomy EXPIRED).
Pipeline status: spec → iter-review round 1 (full-packs design) ✅ → Phase A
(version-detection) SHIPPED + smoke ✅ → **Option-A re-scope + iter-review round 2**
(this revision; 2 adversarial reviewers, blocking findings folded) ✅ → user spec review
(NEXT) → writing-plans for Phase B (mirror M3.A kurogames) → plan review gate →
subagent-driven-development execution → USER smoke + tag `v0.5.0-m3c` + `--no-ff` merge to
main (`feedback_commits`: no Co-Authored-By trailer).
