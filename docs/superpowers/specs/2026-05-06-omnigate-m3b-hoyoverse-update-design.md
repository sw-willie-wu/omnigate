# Omnigate M3.B — HoYoverse Genshin Update Design

**Status:** Draft, post-brainstorm (2026-05-06)
**Target tag:** `v0.4.0-m3b`
**Branch:** `m3b/spec` (to be created)
**Scope:** Genshin Impact update (download / patch / apply / predownload / crash recovery) on the HoYoverse backend. HSR / ZZZ defer to M3.D.

This spec is the source of truth for the M3.B implementation plan. Protocol values were cross-checked against [Collapse Launcher](https://github.com/CollapseLauncher/Collapse) (AGPL-3.0) per the standing convention; live HoYoverse API probing is deliberately avoided.

---

## §0. Locked decisions (do NOT revisit)

| Decision | Choice | Rationale |
|---|---|---|
| Game scope | Genshin Impact only | HSR / ZZZ defer to M3.D |
| Feature scope | Full M3.A parity (update + predownload + crash recovery) | M3.A frontend infra reusable |
| Delta strategy | HPatchZ patch when `patches[].version` matches `currentVer`; else fallback to `main.major` full reinstall (single hop, no chain) | Collapse v3+ behavior |
| Audio packs | Auto-detect installed `AudioAssets/<lang>/` folders; update those alongside main package | No settings UI |
| Code architecture | New `internal/providers/hoyoverse/` package (extends existing M2 Provider with `core.Updater` + `core.ProcessChecker` impl) | m3-refactor v0.3.1 abstractions enable this |
| Patch zip extract location | Staging dir under `<tempRoot>/<gid-flat>/<version>/staging/`; final atomic rename into `gameDir` | Cancel safety; matches M3.A WuWa pattern |
| Concurrent updates | Allowed (per-game `InFlightOp`) | `SidebarRow` already shows per-row inline progress; `BottomBar` reflects selected game only |
| Predownload trigger | User-initiated (BottomBar predl button) | Avoid silently consuming bandwidth |
| Predownload completion notification | Bell drawer `predl_complete` entry | Patch day still requires user action |
| Disk-space pre-flight | Hard block (no override prompt) | Fast fail beats partial download then failure |
| `config.ini` writeback failure | Warn-log, treat apply as successful | Mirror M3.A `launcherDownloadConfig.json` admin caveat; no UAC self-elevation in v1 |

---

## §1. Package layout & file responsibilities

### `internal/providers/hoyoverse/`

M2-existing files reused with extensions:

| File | Status | Responsibility |
|---|---|---|
| `meta.go` | unchanged | BackendID + GameID list |
| `detect.go` / `detect_test.go` | unchanged | HoYoPlay install detection |
| `launch_windows.go` | unchanged | ShellExecute launch |
| `bg.go` / `api.go` | unchanged | Background image (M2 A-hybrid) + basic info / icon fetch |
| `version.go` / `version_test.go` | **extended** | `rawGamePackages` already lives here; M3.B extends parse to include `main.patches[]` + nested `audio_pkgs[]` + `pre_download` sub-tree |
| `hoyoverse.go` | **extended** | `Provider.CheckForUpdate` / `RunUpdate` / `IsGameRunning` impls |

New files:

| File | Lines | Responsibility |
|---|---|---|
| `process_check_windows.go` | ~60 | `EnumProcesses` matching `GenshinImpact.exe` |
| `process_check_other.go` | ~5 | Returns `false, nil` |
| `apply_lock.go` + `_windows.go` + `_other.go` | 80 + 50 + 10 | Mirror kurogames: `<versionDir>/apply.lock` exclusive |
| `config_ini.go` | ~130 | `bufio.Scanner` parse `[General]`; `ReadGameVersion` / `WriteGameVersion`. Tests: missing file / missing section / missing key / BOM / CRLF / comments / key-only line / round-trip (8-10 tests) |
| `audio_packs.go` | ~70 | Detection only: `DetectInstalledLanguages(gameDir) ([]string, error)` lists subfolders of `GenshinImpact_Data/StreamingAssets/AudioAssets/`. Empty slice means "no voice packs" |
| `update_manifest.go` | ~250 | Calls extended `version.go` fetch; branch decide; **audio language intersection** (installed langs × manifest `audio_pkgs[].language`); returns `*core.UpdatePlan` with `Reason` populated |
| `update_preflight.go` | ~80 | `CheckDiskSpace(plan, gameDir, tempRoot)`: `Σ decompressed_size × 1.1` vs `freeSpaceProbe.FreeBytes`. Same-volume check (staging vs gameDir). Mockable interface: `type freeSpaceProbe interface { FreeBytes(path string) (uint64, error) }` (default impl wraps `windows.GetDiskFreeSpaceExW`) |
| `update_download.go` | ~300 | 4-worker pool downloading `game_pkgs[]` + selected `audio_pkgs[]` zip blobs. Byte-range resume. MD5 verify against manifest; mismatch retry 3× exponential backoff |
| `update_progress.go` | ~200 | Hoyoverse-specific `progressStore` wrapping `core.ProgressFile` **verbatim** (no schema fork). Apply / patch state lives in separate `apply.wal` and `extract_progress.json` sidecars |
| `update_patch.go` | ~300 | (PlanPatch only) Extract zip blobs to staging; parse `hdiffmap.json` (modern) or `hdifffiles.txt` (legacy); per-entry source MD5 verify; invoke `hpatchz.Run`; write patched output to staging |
| `update_apply.go` | ~400 | applyWAL (Pending/Done/WasPredl); atomic rename per file; `deletefiles.txt` processing; `config.ini` writeback; cleanup |
| `hpatchz.go` | ~80 | `go:embed third_party/hpatchz/hpatchz.exe`. Init-time `sha256.Sum256` of embedded bytes; first-use extract to `<TEMP>/omnigate/hpatchz-<sha8>.exe` (cross-backend cache, intentional placement outside per-backend tempRoots). `sync.Once`-guarded. `Run(ctx, oldFile, hdiffFile, newFile)` wraps `exec.CommandContext` |
| `testdata/manifest-sample.json` | — | Sanitized `getGamePackages` response |
| `testdata/config-ini-sample.ini` | — | Sample Genshin config.ini |
| `testdata/hdiffmap-sample.json` | — | Sample modern format |

Plus path helpers (provider-local):

```go
func gameSidecarDir(tempRoot string, gid core.GameID) string  // <tempRoot>/<gid-flat>
func versionSidecarDir(tempRoot string, gid core.GameID, ver string) string  // <tempRoot>/<gid-flat>/<ver>
```

### `third_party/hpatchz/`

```
hpatchz.exe         # ~250KB, pinned tag from sisong/HDiffPatch
LICENSE             # BSD-3
README.md           # source URL + tag + SHA-256
```

### Sidecar tree (per-game)

```
<TEMP>/omnigate/hoyoverse/                    ← tempDirFor(hoyoverse, gid)
├── hpatchz-<sha8>.exe                        ← cross-game cache
└── hoyoverse-genshin/                        ← <gid-flat>, per-game
    ├── last_apply_target.json                ← persistent across versions
    └── 5.7.0/                                ← per-version, RemoveAll'd on cleanup
        ├── progress.json
        ├── predl_ready.json
        ├── apply.wal                         ← Stage F PlanPatch resume state
        ├── extract_progress.json             ← Stage F PlanFull resume state
        ├── staging/                          ← PlanPatch only
        ├── blob1.zip / blob1.zip.part
        └── ...
```

At any moment, **at most one** of {`apply.wal`, `extract_progress.json`, `progress.json`, `predl_ready.json`} exists per version dir. Cleanup invariants:
- writing `apply.wal` → `os.Remove(progress.json)` first
- writing `extract_progress.json` → `os.Remove(progress.json)` first
- writing `predl_ready.json` → `os.Rename(progress.json, predl_ready.json)` (atomic)

### App-layer changes

| File | Change |
|---|---|
| `internal/app/settings.go` | Add `HoyoverseSettings.TempDir string \`toml:"temp_dir,omitempty"\`` |
| `internal/app/app.go::tempDirFor` | Add `case hoyoverse.BackendID:` arm returning `<TEMP>/omnigate/hoyoverse/`. Update outdated comment ("unreachable in v0.3.1" → "v0.3.1 only kuro; M3.B adds hoyoverse") |
| `internal/app/app.go::registerProvider` | Add invariant panic: gid must match `^<backendID>/[^/]+$` (single slash, leading segment matches owner). Cheap insurance against future drift |
| `internal/app/update_handler.go::scanForRecovery` | Generalize from single-rooted to per-backend iterate. Filter `<TEMP>/omnigate/<knownBackendID>` subdirs from kurogames-flat root walk to silence noise (registry collects backend ID set at init) |
| `internal/core/updater.go` | Add `ReasonCode string` enum + `UpdatePlan.Reason ReasonCode \`json:"reason,omitempty"\`` |
| `internal/providers/kurogames/update_manifest.go::CheckForUpdate` | One-line: `plan.Reason = core.ReasonVersionChanged` before plan return |

### Cross-backend invariants (documented)

- All gids have form `<backendID>/<localPart>`. Flatten via `strings.ReplaceAll(s, "/", "-", 1)` produces `<owner>-<rest>`; never equals another backend's `BackendID` literal.
- `kurogames` per-backend root: `<TEMP>/omnigate/` (flat, bit-exact preservation).
- `hoyoverse` per-backend root: `<TEMP>/omnigate/hoyoverse/` (subdir).
- `scanForRecovery` walks each backend's root with that backend's owner; cross-backend collision impossible by gid format invariant.

---

## §2. Update flow

### Stage A — `Provider.CheckForUpdate(ctx, gid)`

1. Fetch via extended `version.go` → `getGamePackages?launcher_id=VYTpXlbWo8&game_ids[]=gopR6Cufr3`
2. Parse `data.game_packages[0].main` → `mainMajor` + `patches[]` + optional `pre_download`
3. Read `<gameDir>/config.ini` `[General].game_version` → `currentVer`. Missing key / file → `version_unknown` error code
4. **Branch decide**:
   ```
   if currentVer == mainMajor.version:
       last_apply_target := loadJSONSidecar[lastApplyTarget](gameSidecarDir(tempRoot, gid))
       if last_apply_target == nil:
           return PlanNone   // fresh install / Omnigate never applied; no audio drift baseline
       installed := audio_packs.DetectInstalledLanguages(gameDir)
       if !slices.Equal(sort(installed), sort(last_apply_target.audio_languages)):
           return PlanPatch (synthetic source: audio_pkgs only, no hdiffmap)
                  with Reason=ReasonAudioPackAdded
       return PlanNone
   if currentVer ∈ patches[].version (FROM-version match):
       return PlanPatch (Source=matched-patch-entry, Target=mainMajor.version)
              with Reason = (audio drift detected ? ReasonVersionAndAudio : ReasonVersionChanged)
   return PlanFull (Source=mainMajor)
          with Reason=ReasonVersionChanged
   ```
5. If `pre_download != nil`:
   - `currentVer ∈ pre_download.patches[].version` → `PredlPlan{Kind: PlanPatch, Source: matched-predl-patch, Target: pre_download.major.version}`
   - else → `PredlPlan{Kind: PlanFull, Source: pre_download.major}`
   - Attach `plan.PredownloadAvailable = true`
6. Self-heal: if `last_apply_target.target_version == mainMajor.version` and currentVer differs → assume `config.ini` stale; retry write; success → `PlanNone`. Toast spam suppression: skip if `now - last_writeback_retry_ts < 24h`. Clock-skew tolerance: `now < ts && (ts - now) ≤ 24h` also silent-skip; `> 24h` treats sidecar as corrupt and removes it.

### Stage B — Plan construction (`update_manifest.go` + `update_preflight.go`)

1. Audio language intersect: `needed = installed_langs ∩ source.audio_pkgs[].language` (lang-code mapping resolved via plan task 1 protocol research)
2. Construct `[]core.FileTask{URL, MD5, Size, Path}` over `source.game_pkgs[]` + selected audio pkgs
3. **Pre-flight**: same-volume check (staging vs gameDir) → `cross_volume_setup` if mismatch. `Σ decompressed_size × 1.1` vs free space → `insufficient_space` if short
4. Return `*core.UpdatePlan`

### Stage C — Download (`update_download.go`)

4-worker pool, per-worker:
1. `os.Stat(<staged>/<rel>)` — if exists with matching size, MD5 → skip
2. Else `Range: bytes=N-` resume; stream-write `.part` while computing MD5
3. MD5 verify against `FileTask.MD5`; mismatch → 3× exponential backoff (1s/4s/16s) retry
4. `os.Rename(.part → final)`; `progressStore.MarkComplete(rel, size, mtime, md5)`

Cancel: `ctx.Done()` propagates; pool drains; `progress.json` retains partial state for resume.

### Stage D — Predownload branch

If `plan.IsPredl == true`:
- Run Stage C only
- `progressStore.RenameToPredlReady()` → `progress.json → predl_ready.json`
- `predl_ready.json` carries hoyoverse-local `planSnapshot{SourceVersion, TargetVersion, Files, AudioLanguages, ManifestETag}` alongside `core.ProgressFile` fields (composite type, hoyoverse-local, **no core schema change**)
- emit `UpdateEvent{Kind: PredlReady}`; bell drawer `predl_complete` entry
- STOP (no Stage E/F)

If `plan.IsPredl == false`, before Stage C:
- Read `predl_ready.json` if present
- **Element-wise reuse check**: compare current plan vs `predlReady.PlanSnapshot` on `SourceVersion` + `TargetVersion` + sorted `AudioLanguages` + element-wise `Files` (URL + MD5 + Size). Mismatch on **any** field → invalidate
- Audio drift handling: per-FileTask set difference (added / removed / common). For `removed`: `delete progress.Entries[k]` + `os.Remove(versionDir/k)` + `os.Remove(versionDir/k.part)` (ENOENT ignored). `added`: enqueue to download. `common`: keep
- Full match → emit `UpdateEvent{Stage: "skipping_download_predl_hit"}` and skip to Stage E

### Stage E — Patch (`update_patch.go`, PlanPatch only)

PlanFull skips this stage entirely.

1. Entry guard: `RemoveAll(staging/)` defensively (idempotent re-entry)
2. emit `Stage: "extracting"`. Stream-extract zip blobs **serially** (one at a time) to staging — avoids parallel-unzip RAM peaks
3. Detect format:
   - `<staging>/hdiffmap.json` → modern: per-entry verify `sourceMD5Hash`; mismatch → `source_corrupted` (PlanFull suggested next)
   - `<staging>/hdifffiles.txt` → legacy: per-line size match only (no MD5); mismatch → `source_corrupted_legacy`
   - **Audio-only PlanPatch**: neither file → just zip extract, skip patch loop, emit `Stage: "extracting_audio"`
   - Both absent in non-audio case → `unsupported_manifest` (theoretical)
4. emit `Stage: "patching"`. For each hdiffmap entry: `hpatchz.Run(ctx, srcPath, patchPath, stagedTargetPath)`. Non-zero exit → `apply_failed` with file param. Success → `progressStore.MarkPatched(targetFileName)`
5. emit `Stage: "verifying_patches"`. 4-worker parallel: post-patch MD5 of staging file vs `entries[].targetMD5Hash` (modern only; legacy skipped). 4MB-chunk ctx-cancel granularity. Mismatch → `patch_corrupted`

### Stage F — Apply

#### PlanPatch path (`update_apply.go`)

1. emit `Stage: "applying"`
2. Build `applyWAL{Pending: relPaths, Done: [], WasPredl}`. Write `apply.wal` + fsync
3. **Phase flips to PhaseApply** (cancel blocked from here)
4. WAL replay loop. For each pending: `os.Rename(<staged>/<rel>, <gameDir>/<rel>)`. Same-volume only — EXDEV → `cross_volume_midrun` terminal (no copy fallback; same-volume guarantee enforced at preflight per Stage B). Move from Pending → Done; flush WAL every 10 files or 1s
5. Process `<staged>/deletefiles.txt`: per-line `os.Remove(<gameDir>/<rel>)`. ENOENT → skip; other (EACCES, in-use) → `apply_partial` Retryable=true terminal
6. emit `Stage: "cleanup"`. **Phase flips back to PhaseDownload** (cancel allowed during long RemoveAll)
7. `config.WriteGameVersion(<gameDir>, plan.Target)`. Failure → warn-log + `Stage: "config_writeback_warning"`; apply still considered successful (gameDir is at new version even if config.ini stale)
8. Re-detect audio langs; write `last_apply_target.json` (atomic via tmpfile + rename)
9. `RemoveAll(<versionDir>)` (last_apply_target.json at parent dir survives)
10. emit `Kind: PhaseComplete, Phase: PhaseApply`

#### PlanFull path (no Stage E)

PlanFull's extract-direct-to-gameDir trade-off:

1. emit `Stage: "applying_full"` (NOT `applying`)
2. **Phase flips to PhaseApply** at first byte to gameDir (UI shows "不可中斷（預估 X 分鐘）")
3. Per zip blob:
   - Permission check probe: write a small tag file to gameDir → if EACCES, `permission_denied` terminal
   - Stream-extract zip directly into `gameDir/<rel>`, overwriting partial files
   - `extract_progress.json::blobs[<URL>].extracted = true`; flush after each blob
4. emit `Stage: "cleanup"`. **Phase flips back to PhaseDownload**
5. `config.WriteGameVersion`, `last_apply_target.json` (same as PlanPatch)
6. `RemoveAll(<versionDir>)`

### Stage F — Resume decision table

`Provider.RunUpdate(ctx, plan)` entry, hoyoverse-local sidecar precedence (top-down, first-match):

| Detected | Action |
|---|---|
| `apply.wal` present | Stage F PlanPatch resume (WAL replay) |
| `extract_progress.json` present | Stage F PlanFull resume. **Manifest stability check**: compute the manifest stability token (HTTP `ETag` header value if CDN provides; otherwise `fingerprint = SHA-1(version + "\x00" + sortedJoin(game_pkgs[].package, "\n") + "\x00" + sortedJoin(audio_pkgs[].package, "\n"))`); compare to stored `extract_progress.manifest_etag`; drift → `RemoveAll(<versionDir>)` and restart fresh |
| `progress.json` present + `AllEntriesComplete()` | Stage E re-run: `RemoveAll(staging/)` defensively; re-extract & re-patch idempotently |
| `progress.json` present + not all complete | Stage C resume |
| `predl_ready.json` present | predl-hit (Stage A already converted to PlanNone+PredlReady); StartUpdate path begins fresh element-wise reuse comparison |
| None + `versionDir` exists | Orphan staged files from aborted prior attempt → `RemoveAll(versionDir)` + `MkdirAll(versionDir)` |
| None | Fresh update from Stage A |

`scanForRecovery` (app-layer) emits `RecoveryPhaseDownloadResume` for both Stage C and Stage E mid-states (no `Hint` field on `core.RecoveryState`); hoyoverse `RunUpdate` resume entry-point disambiguates internally.

### State machine — Stage / Phase / cancel matrix

| Stage | Phase | Cancel UI |
|---|---|---|
| `""` (pre-fetch / CheckForUpdate) | n/a | enabled |
| `verifying` (M3.A reused) | PhaseDownload | enabled |
| `predownloading` | PhaseDownload | enabled |
| `skipping_download_predl_hit` | PhaseDownload | n/a (transient toast) |
| `extracting` | PhaseDownload | enabled |
| `extracting_audio` (audio-only PlanPatch) | PhaseDownload | enabled |
| `patching` | PhaseDownload | enabled |
| `verifying_patches` | PhaseDownload | enabled |
| `applying` (PlanPatch) | **PhaseApply** | **disabled** with tooltip |
| `applying_full` (PlanFull) | **PhaseApply** | **disabled** with tooltip |
| `cleanup` | PhaseDownload | enabled |
| `config_writeback_warning` | n/a (post-success warn event) | n/a |

### Sidecar schema reference

**Field naming convention**: All three hoyoverse sidecars use `audio_languages` (not `audio_langs`) and `manifest_etag` (semantically a "manifest stability token" — when CDN provides an HTTP `ETag` header, that value is stored verbatim; otherwise the computed fingerprint hash from §2 Stage F resume table is stored in the same field). One field name, two possible value sources, single comparison logic.

`last_apply_target.json` (per-game, persists across versions):
```jsonc
{
  "target_version": "5.7.0",
  "audio_languages": ["Chinese", "English(US)"],   // sorted alphabetically
  "completion_ts": "2026-05-06T12:34:56+08:00",
  "config_writeback_ok": true,
  "manifest_etag": "...",                          // ETag value or fingerprint hash
  "last_writeback_retry_ts": ""                    // empty unless retry pending
}
```

`extract_progress.json` (per-version, PlanFull only):
```jsonc
{
  "manifest_etag": "...",                          // ETag or fingerprint
  "blobs": {
    "<blob URL 1>": { "extracted": true, "extracted_at": "..." },
    "<blob URL 2>": { "extracted": false }
  }
}
```

`predl_ready.json` (per-version, predl-only):
```jsonc
{
  "game_id": "hoyoverse/genshin",                  // core.ProgressFile fields
  "version": "5.7.0",
  "etag": "...",
  "entries": { ... },
  "plan_snapshot": {                               // hoyoverse-local extension
    "source_version": "5.6.0",
    "target_version": "5.7.0",
    "files": [ ... ],                              // []core.FileTask snapshot
    "audio_languages": ["Chinese", "English(US)"],
    "manifest_etag": "..."                         // ETag or fingerprint
  }
}
```

`core.ProgressFile` schema **unchanged**; `predlReadyFile` is hoyoverse-local composite type.

Generic helper for all hoyoverse sidecars:
```go
func loadJSONSidecar[T any](path string) (*T, error)
// ENOENT → nil, nil
// parse error → log warn + os.Remove + nil, nil
// other I/O → nil, err
```

---

## §3. UI / UX

### Reused M3.A components (no modification)

- `BottomBar.vue` (8-state matrix; render-logic extension below)
- `SidebarRow.vue` (inline progress overlay)
- `ConfirmDialog.vue` + `ToastHost.vue` + composables
- Bell drawer in `Topbar.vue`
- `updates.ts` Pinia store + `EventsOn` rAF batching

### Stage labels (i18n)

`update.stage.*` namespace. New keys for M3.B:

| Key | zh-TW | en |
|---|---|---|
| `update.stage.predownloading` | 預下載中… | Predownloading… |
| `update.stage.skipping_download_predl_hit` | 使用預下載檔（跳過下載） | Using predownloaded files (skipping download) |
| `update.stage.extracting` | 解壓更新檔… | Extracting update… |
| `update.stage.extracting_audio` | 展開語音包… | Extracting voice packs… |
| `update.stage.patching` | 套用差分修補… {x} / {y} | Applying patches… {x} / {y} |
| `update.stage.verifying_patches` | 驗證修補檔案… {x} / {y} | Verifying patches… {x} / {y} |
| `update.stage.applying` | 套用更新（不可中斷） | Applying update (cannot cancel) |
| `update.stage.applying_full` | 正在套用更新（不可中斷，預估 {minutes} 分鐘） | Applying full update (cannot cancel, ~{minutes} min) |
| `update.stage.cleanup` | 清理暫存檔… | Cleaning up… |
| `update.cancel_apply_disabled` | 套用中無法取消 | Cannot cancel during apply |
| `update.cancel_apply_disabled_eta` | 套用中無法取消（預估還有 {minutes} 分鐘） | Cannot cancel during apply (~{minutes} min remaining) |

`applying_full` ETA: `Σ decompressed_size / 100 MB/s` conservative estimate. Display "預估 X 分鐘" only after ≥30% Stage F PlanFull progress, otherwise static "不可中斷"; avoids early-overestimate frustration.

### Error code matrix

| Error code | Retryable | Trigger | i18n key (`update.error.<code>`) |
|---|---|---|---|
| `insufficient_space` | false | preflight free-space check | 「需要 X.X GB，可用 Y.Y GB；請清理後重試」 |
| `cross_volume_setup` | false | preflight: staging ≠ gameDir volume | 「暫存與遊戲目錄不同磁碟；請至設定變更」 |
| `cross_volume_midrun` | false | mid-run EXDEV (rare) | 「磁碟狀態變化，更新中止」 |
| `unsupported_manifest` | false | patch zip lacks both hdiffmap and hdifffiles | 「不支援的更新封包格式」 |
| `source_corrupted` | false | hdiffmap modern: sourceMD5Hash mismatch | 「本機檔案被修改；建議全量重灌」 |
| `source_corrupted_legacy` | false | hdifffiles legacy: source size mismatch | 「本機檔案大小異常；建議全量重灌」 |
| `source_size_mismatch` | true | rare race (file in-use mid-write) | 「源檔案大小不一致；請重試」 |
| `patch_corrupted` | true | post-patch targetMD5Hash mismatch (modern only) | 「修補檔案校驗失敗；請重試」 |
| `apply_failed` | true | hpatchz nonzero / rename fail | 「套用失敗（{file}）；請重試」 |
| `apply_partial` | true | deletefiles EACCES / file in-use | 「部分檔案無法更新（{file}）；請關閉遊戲後重試」 |
| `permission_denied` | false | PlanFull first byte to gameDir EACCES | 「寫入遊戲目錄需要管理員；請以管理員身份重啟」 |
| `version_unknown` | false | config.ini missing or no game_version key | 「無法讀取本地版本」 |
| `process_blocked` | true | M3.A reused: `IsGameRunning(gid) == true` at StartUpdate / StartPredownload / ApplyPredownload entry | 「請先關閉遊戲後再試」 |
| `interrupted_resume` | true | M3.A reused: scanForRecovery detection | 「上次更新中斷；請點繼續以恢復」 |

`CheckForUpdate` is read-only and never gated by `IsGameRunning` — only state-mutating ops gate.

### Bell drawer entry types

| Type | Trigger | Buttons |
|---|---|---|
| `interrupted_resume` (M3.A) | scanForRecovery | [繼續] [取消] |
| `predl_complete` (M3.B new) | Stage D end | [我知道了] [切換到此遊戲] |
| `config_writeback_warning` (M3.B new) | Stage F config write fail | [我知道了] |

`predl_complete` `[切換到此遊戲]` calls frontend `gamesStore.select(gid)` + `dismissBellEntry(entryId)` — **no new RPC** required.

`config_writeback_warning` v1: pure info entry, no action button. Text instructs user to manually right-click → run as admin. `App.RestartElevated()` deferred (M3.B.v2).

### Predownload trigger UI

Reuse existing `BottomBar.vue` predl-area slot (lines 106-118). M3.B **only relabels**:

- Old: `t('update.predl_available')` = "可預下載 ↓"
- New: `t('update.predl_available_size', { size: formatSize(plan.totalSize) })` = "預下載 5.4 GB ↓"

`formatSize(bytes: number): string` lives at new file `frontend/src/utils/format.ts`:
- `< 1 GiB` → `"X MB"` (rounded integer)
- `≥ 1 GiB` → `"X.X GB"` (1 decimal)

### SidebarRow inline icons

| Icon | Visibility | Trigger |
|---|---|---|
| `predl-ready` (☁ ✓) | always-visible | `state.byGame[gid].PredlReady === true` |
| `stale-version-warn` (i) | hover-only on row | `last_apply_target.config_writeback_ok === false` |

### Update reason tooltip on `[更新遊戲]` button

`core.UpdatePlan.Reason` enum (new in M3.B):

```go
type ReasonCode string
const (
    ReasonUnspecified     ReasonCode = ""
    ReasonVersionChanged  ReasonCode = "version_changed"
    ReasonAudioPackAdded  ReasonCode = "audio_pack_added"
    ReasonVersionAndAudio ReasonCode = "version_and_audio"
    ReasonPredownload     ReasonCode = "predownload"
)
```

Frontend conditional: `if (plan.reason) showTooltip()`. M3.A kurogames code populates `ReasonVersionChanged` (one-line touch) for forward-consistency; falling back to `ReasonUnspecified` produces no tooltip (graceful).

| Reason | Tooltip i18n |
|---|---|
| `version_changed` | `{currentVer} → {targetVer}` |
| `audio_pack_added` | 「新增語音包：{langs}」 |
| `version_and_audio` | `{currentVer} → {targetVer}（含新增語音包）` |
| `predownload` | 「預下載 {targetVer}（patch day 套用）」 |

### Concurrent updates indicator

`Topbar.vue` bell button:
- Existing: red dot when `pending.length > 0` (interrupted entries)
- M3.B: rotating spinner badge when `anyInFlight = Object.values(updates.byGame).some(s => s.in_flight != null)`
- Priority: red dot > spinner (red dot covers spinner if both)
- Drawer header (when `anyInFlight`): plain text group "正在更新：Genshin / WuWa" — non-interactive (control surface stays in sidebar)

### `BottomBar.vue` rendering changes

Add two computed:

```typescript
const stageLabel = computed(() => {
  if (!inFlight.value?.stage) return ''
  return t(`update.stage.${inFlight.value.stage}`, inFlight.value.params || {})
})

const cancelDisabledTooltip = computed(() => {
  const eta = inFlight.value?.estimated_seconds_remaining
  return eta && eta > 0
    ? t('update.cancel_apply_disabled_eta', { minutes: Math.ceil(eta / 60) })
    : t('update.cancel_apply_disabled')
})
```

Replace inline ternary at line 138; cancel button area becomes:

```html
<button v-if="inFlight?.phase === 'download'" class="cancel-x" @click="onCancel">×</button>
<span v-else-if="inFlight?.phase === 'apply'" class="cancel-x disabled" :title="cancelDisabledTooltip">×</span>
```

---

## §4. Testing, RPC, Settings, Exit

### Wails RPC surface — **no new RPCs**

M3.B reuses M3.A's existing RPCs entirely:

| RPC | M3.B note |
|---|---|
| `App.CheckForUpdate(gameID) error` | hoyoverse new branch decide; reads `last_apply_target.json` for self-heal |
| `App.StartUpdate(gameID) error` | dispatches to hoyoverse Updater.RunUpdate (isPredl=false) |
| `App.StartPredownload(gameID) error` | dispatches to hoyoverse Updater.RunUpdate (isPredl=true) |
| `App.ApplyPredownload(gameID) error` | hoyoverse predl-hit path (Stage E from cached zip) |
| `App.CancelInFlight(gameID) error` | unchanged — Phase guard already correct |
| `App.ResumeInterrupted(gameID) error` | hoyoverse decides Stage C/E/F internally per resume table |
| `App.RemovePredownload(gameID) error` | wipes hoyoverse versionDir + bell entry |
| `App.DismissError(gameID) error` | shared dismiss for all bell entry types |
| `App.UpdateStatusAll() map[string]GameUpdateSnapshot` | hoyoverse states automatically included |

### Settings schema (no version bump)

```go
type HoyoverseSettings struct {
    HoyoplayPath string `toml:"hoyoplay_path,omitempty"`     // M2 existing
    TempDir      string `toml:"temp_dir,omitempty"`          // M3.B new
}
```

Schema version stays `1`; all new fields are `omitempty` so existing `settings.toml` reads remain valid.

### Cross-cutting touches (M3.B execution scope)

| File | Change |
|---|---|
| `internal/core/updater.go` | + `ReasonCode` enum + `UpdatePlan.Reason` field |
| `internal/providers/kurogames/update_manifest.go::CheckForUpdate` | + `plan.Reason = core.ReasonVersionChanged` (1 line) |
| `internal/app/settings.go` | + `HoyoverseSettings.TempDir` field |
| `internal/app/app.go::tempDirFor` | + `case hoyoverse.BackendID:` arm; comment update |
| `internal/app/app.go::registerProvider` | + gid format invariant panic |
| `internal/app/update_handler.go::scanForRecovery` | per-backend iterate + known-backend-ID filter |

No `core.ProgressFile` or `core.RecoveryState` schema changes.

### Test plan

#### Go unit tests (`internal/providers/hoyoverse/`)

| File | Tests | Purpose |
|---|---|---|
| `config_ini_test.go` | 8-10 | parse edge cases; round-trip |
| `audio_packs_test.go` | 4 | 0 / 1 / N langs / corrupted dir |
| `update_manifest_test.go` | 6 | branch decide cases (PlanNone / PlanPatch / PlanFull / audio-only / predl available / version_unknown) |
| `update_preflight_test.go` | 3 | enough / insufficient / cross-volume |
| `update_download_test.go` | 5 | full / Range resume / MD5 retry / cancel mid-blob / 4-worker parallel |
| `update_progress_test.go` | 6 | MarkComplete / LoadOrInit / RenameToPredlReady / corrupt sidecar / planSnapshot serialize / set-difference invalidation |
| `update_patch_test.go` | 5 | hdiffmap parse / sourceMD5 verify pass+fail / hpatchz invocation success+fail / extracting_audio path |
| `update_apply_test.go` | **10** | WAL write / WAL replay / WAL corrupt / atomic rename same-vol / EXDEV terminal / deletefiles ENOENT / EACCES / config writeback success / failure / RemoveAll preserves last_apply_target |
| `hpatchz_test.go` | 3 | extract-once / sha-keyed filename / Run with cancel |
| `apply_lock_test.go` | 3 | acquire / fail / unlock |
| `process_check_test.go` (windows) | 2 | running / not running |
| `hoyoverse_test.go` (extended) | 4 | CheckForUpdate full + audio-only + predl-hit + Stage E re-run |

`internal/app/app_test.go` adds `TestRegisterProvider_PanicsOnInvalidGID` (4 cases: no slash / two slashes / empty local / valid).

Total Go unit tests: ~55 hoyoverse + 16 app + existing M3.A → ~70 packages-wise.

#### Go integration tests (`integration_test.go`, `// +build integration`)

| Test | Coverage |
|---|---|
| `TestEndToEnd_PlanPatch_HappyPath` | httptest manifest + zip serve → full Stage A→C→E→F |
| `TestEndToEnd_PlanFull_HappyPath` | PlanFull: extract direct to gameDir |
| `TestEndToEnd_AudioOnly_HappyPath` | currentVer==latestVer + audio drift → audio-only PlanPatch |
| `TestEndToEnd_PredlHit` | predl_ready.json present → Stage C skipped |
| `TestEndToEnd_CrashRecovery_StageC` | mid-download interrupt; resume |
| `TestEndToEnd_CrashRecovery_StageE` | mid-patching interrupt; staging wipe + re-run |
| `TestEndToEnd_CrashRecovery_StageF_PlanPatch` | mid-rename interrupt; WAL replay |
| `TestEndToEnd_CrashRecovery_StageF_PlanFull` | mid-extract interrupt; extract_progress.json skip extracted blobs + manifest stability check |
| `TestEndToEnd_ConfigWritebackFail` | gameDir read-only; last_apply_target.config_writeback_ok=false; self-heal on next CheckForUpdate |

#### Go fuzz tests (`fuzz_test.go`)

`FuzzConfigIni`, `FuzzManifestParse`, `FuzzHdiffmapParse`, `FuzzApplyWALParse` — all assert no panic; corrupt input handled gracefully.

#### Go bench tests (`bench_test.go`)

| Bench | Budget |
|---|---|
| `BenchmarkProgressStoreMarkComplete_1k_entries` | < 100ms (M3.A O(N²) carry-over; not fixed in M3.B) |
| `BenchmarkApplyWAL_500_files` | < 500ms (per-10-files / per-1s flush cadence) |

#### Frontend Vitest (`frontend/src/__tests__/`)

| File | Change |
|---|---|
| `i18n_parity.test.ts` | + ~30 new keys to `required[]` (9 stages + 12 errors + 4 bell-entry strings + 4 reason tooltips + 2 cancel-disabled tooltips + 1 predl-size label); + non-empty value assertion across 3 locales |
| `BottomBar.test.ts` | + applying/applying_full cancel button = `[disabled]` not hidden; tooltip via `update.cancel_apply_disabled[_eta]` |
| `updates_store.test.ts` | + predl_complete entry produce/dismiss; config_writeback_warning entry produce/dismiss |
| `format.test.ts` (new) | 5 tests: 0 / sub-MB / sub-GB / over-GB / TB-class |

### Manual smoke checklist — exit criteria

User runs `build/bin/omnigate.exe` against real Genshin install at `C:\Program Files\Genshin Impact\Genshin Impact game\`. Backup `config.ini` before tests. All 22 points must pass to ship.

| # | Test |
|---|---|
| 1 | Genshin already at latest; click Genshin row → `[開始遊戲]` visible, no `[更新]` |
| 2 | Edit `config.ini` `game_version` to `5.5.0`; Refresh → `[更新遊戲]` appears; tooltip shows `5.5.0 → <latest>` |
| 3 | Click `[更新遊戲]`; observe Stage progression: extracting → patching → verifying_patches → applying (cancel disabled) → cleanup → ✓ |
| 4 | During `Stage="patching"`, click cancel → `InFlightOp` exits, state returns to `[更新遊戲]`, staging cleaned |
| 5 | Re-click `[更新遊戲]`; mid-download kill omnigate.exe via Task Manager |
| 6 | Reopen omnigate; bell drawer shows "上次更新中斷"; click `[繼續]` → resumes from progress |
| 7 | Update completes; `config.ini` `game_version` = `<latest>` |
| 8 | Mock manifest with `pre_download` field; Refresh; click `[預下載 X.X GB]` → Stage C runs; bell drawer adds `predl_complete` entry |
| 9 | Restart omnigate; mock manifest `main.major.version` = predl target; click `[更新遊戲]` → `Stage="skipping_download_predl_hit"` toast; goes straight to Stage E |
| 10 | Toggle gear icon language zh-TW ↔ en ↔ zh-CN; all `update.*` keys render with no diamond `?` fallback |
| 11 | Remove Chinese voice folder; Refresh: fresh install case → `PlanNone`; post-Stage-F case → audio-only `PlanPatch` |
| 12 | Install Korean voice via official launcher; Refresh → audio drift detected; `[更新遊戲]` tooltip = "新增語音包：Korean"; click → only Korean audio_pkg downloads |
| 13 | Set gameDir read-only; run update; expect `config_writeback_warning` bell entry; `last_apply_target.config_writeback_ok=false` |
| 14 | Cross-volume setup (`settings.toml` + restart): `temp_dir = "D:\\genshin-temp"`, gameDir on C: → preflight `cross_volume_setup` toast |
| 15 | Fill TempDir disk to < required; run update → preflight `insufficient_space` toast with required/available numbers |
| 16 | Concurrently: WuWa update started; switch to Genshin row, click `[更新遊戲]` → both rows show inline progress; bell button shows spinner badge |
| 17 | `wails build` production binary; `omnigate.exe` size = 12.8MB ± 0.4MB (M3.A 12.27MB + ~250KB hpatchz + ~500KB Go code) |
| 18 | Mock `config.ini` `game_version=3.0.0` (out of patches[] range) → triggers PlanFull → `Stage="applying_full"`, cancel disabled with ETA tooltip |
| 19 | Start `GenshinImpact.exe`; click `[更新遊戲]` → `process_blocked` toast |
| 20 | Manually delete `<TEMP>/omnigate/hpatchz-<sha>.exe`; run update → re-extract automatic; update completes |
| 21 | Manually edit a Genshin `.dll` (1 byte change); trigger PlanPatch → `source_corrupted` toast suggesting full reinstall |
| 22 | Close omnigate; edit `settings.toml` `[backends.hoyoverse] temp_dir = "D:\\genshin-temp"`; restart; click `[更新遊戲]` → uses D: TempDir; if D: ≠ C: gameDir → `cross_volume_setup` |

---

## §5. Backward compatibility

| Change | Impact | Handling |
|---|---|---|
| `core.UpdatePlan.Reason` new field | M3.A kurogames doesn't set | `omitempty`; frontend conditional render; M3.B touches kurogames CheckForUpdate one line for forward-consistency |
| `HoyoverseSettings.TempDir` new field | M2 settings.toml lacks it | `omitempty`; default falls through `tempDirFor` default branch |
| `core.ProgressFile` schema | **unchanged** | hpatchz state in `apply.wal`; PlanFull in `extract_progress.json`; predl extension in hoyoverse-local composite |
| `core.RecoveryState` schema | **unchanged** | Hint field NOT added; hoyoverse internal disambiguation via sidecar inspection |
| kurogames game IDs | invariant: `kurogames/<localPart>` | registry init-time panic enforces |
| `scanForRecovery` walker | broadened to all providers | filter known-backend-ID dirs to suppress noise |

---

## §6. Post-M3.B follow-ups (deferred)

| Item | Target |
|---|---|
| `App.RestartElevated()` RPC + `config_writeback_warning` "以管理員重啟" button | M3.B.v2 / v0.4.x |
| HSR / ZZZ Genshin-style updater | M3.D |
| Promote `gameSidecarDir` / `versionSidecarDir` / `loadJSONSidecar[T]` to `core/sidecar_paths.go` (when M3.C lands as 3rd consumer) | M3.C |
| `last_writeback_retry_ts` clock-skew finer-grained handling | as needed |
| `ReasonInitialInstall` enum value (fresh install detection) | M3.D |
| Bell drawer entry click-to-select-row | UI v2 |
| `progressStore.MarkComplete` O(N²) → append-only WAL (M3.A known issue) | concurrent fix |
| If telemetry shows Genshin emits only one of `hdiffmap.json` / `hdifffiles.txt`, drop the other parser in v0.4.x | post-release optimization |

---

## §7. Plan task 1 — protocol research dependencies

The implementation plan's task 1 (protocol research) must observe a real Genshin patch and verify:

1. Does `getGamePackages` CDN (`sg-hyp-api.hoyoverse.com`) return `Accept-Ranges: bytes` + `206 Partial Content` for `game_pkgs[].package` URLs? (Validates Stage C byte-range resume strategy.)
2. Does Genshin 5.x emit `hdiffmap.json` or `hdifffiles.txt`? Or both? (Confirms which parser is primary; both supported for v1.)
3. What are the actual `audio_pkgs[].language` values? (Confirms folder-name → language-code mapping for `audio_packs.go` × `update_manifest.go` intersect.)
4. Does CDN provide `ETag` header on `getGamePackages` response? (Determines whether `manifest_etag` or computed `fingerprint` is the canonical reuse-comparison token.)
5. Pin hpatchz binary version + SHA-256 from sisong/HDiffPatch releases.
6. Confirm `<gameDir>/config.ini` `[General].game_version` format on a 5.x install (no surprises vs Collapse `IniConfig.cs` reference).

Findings written to `docs/superpowers/research/2026-05-XX-m3b-genshin-protocol-validation.md`; spec updated only if findings invalidate locked decisions.

---

**End of M3.B design spec.**
