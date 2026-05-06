# M3.B HoYoverse Update — Pre-Brainstorm Notes

**Created:** 2026-05-06 (post m3-refactor ship, post Omnigate rebrand)
**Status:** Pre-brainstorm reference. Inputs collected during the aborted M3.B brainstorming session that pivoted to m3-refactor. Use as input for the actual M3.B brainstorming when it's restarted.
**Reading audience:** Future Omnigate session that picks up M3.B HoYoverse work.

---

## Locked decisions (already agreed during prior session — do NOT re-ask)

| Decision | Choice | Rationale |
|---|---|---|
| Game scope | **Genshin Impact only** for v1 | HSR/ZZZ defer to M3.B.v2; `meta.go` already lists all 3 but only Genshin gets Updater impl |
| Feature scope | **Full M3.A parity**: update + predownload + crash recovery | All M3.A frontend infra (BottomBar 8-state, ConfirmDialog, ToastHost, bell drawer, i18n keys) reusable as-is |
| Delta strategy | **HPatchZ patch when available; full main.major fallback** | Collapse Launcher v3+ behavior. No multi-hop patch chain (single hop or full only). |
| Audio packs | **Auto-detect installed lang folders, update those alongside main package** | No settings UI panel; fully implicit |
| Code architecture | **Independent `internal/providers/hoyoverse/` package** | m3-refactor (v0.3.1) shipped the abstractions enabling this — `core.Updater`, `core.ProcessChecker`, `core.ScanRecovery`, `core.LoadProgress`, etc. |

---

## What m3-refactor (v0.3.1) ALREADY DELIVERED for M3.B

The pre-rebrand m3-refactor session extracted these from kurogames into `internal/core/`:

- `core.ProgressFile` / `core.ProgressEntry` — sidecar JSON schema (M3.B reuses this verbatim)
- `core.RecoveryPhase` enum + 5 const + `core.RecoveryState` struct
- `core.ScanRecovery(dir)` — provider-agnostic sidecar collision resolver
- `core.LoadProgress` / `core.LoadProgressFromPath` / `core.ReadWALETag` — sidecar I/O utils
- `core.ProcessChecker` interface — `IsGameRunning(gid) (bool, error)` (kurogames Provider already implements; hoyoverse Provider should too)
- `App.tempDirFor(backend, gid)` — per-backend temp root resolver. v0.3.1 has only the `kurogames` switch arm. **M3.B needs to add `case hoyoverse.BackendID:` arm** when adding `HoyoverseSettings.TempDir` field.

So M3.B's app-side changes are minimal:
- Add `case hoyoverse.BackendID:` to `tempDirFor` (settings.go gets `HoyoverseSettings.TempDir` field, no schema bump)
- `update_handler.go` plug-in is **automatic** once hoyoverse Provider implements `core.Updater`

The `scanForRecovery` (still single-rooted in v0.3.1) needs to be generalized to walk per-backend tempRoots — **this is M3.B's responsibility** (deferred from m3-refactor per spec §1.7).

---

## Protocol findings from Collapse Launcher (cross-checked during prior review)

**Collapse Launcher source = canonical reference** per `memory/feedback_collapse_reference.md`. No live API probes needed.

### `getGamePackages` response shape

Endpoint already known and called by existing `internal/providers/hoyoverse/api.go`:
```
GET https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api/getGamePackages
   ?launcher_id=VYTpXlbWo8&game_ids[]=gopR6Cufr3   # gopR6Cufr3 = Genshin global API id
```

Per `HypLauncherGameResourcePackageApi.cs` in Collapse, the response shape is:

```jsonc
{
  "data": {
    "game_packages": [
      {
        "game": { "id": "gopR6Cufr3", "biz": "hk4e_global" },
        "main": {
          "major":   {
            "version": "5.6.0",
            "game_pkgs": [ /* array of HypPackageData — N entries possible */ ],
            "audio_pkgs": [ /* per-version, NESTED inside major */ ],
            "res_list_url": "..."
          },
          "patches": [
            {
              "version": "5.5.0",   // FROM version
              "game_pkgs": [...],
              "audio_pkgs": [...]
            },
            {
              "version": "5.4.0",   // FROM version (older)
              "game_pkgs": [...],
              "audio_pkgs": [...]
            }
          ]
        },
        "pre_download": { "major": {...}, "patches": [...] }   // optional, only during predl window
      }
    ]
  }
}
```

`HypPackageInfo` (each `major` / `patches[i]` / `pre_download.major` entry) contains:
- `version`
- `game_pkgs[]` — **plural** (HoYoverse may split the main payload across multiple zip blobs)
- `audio_pkgs[]` — **nested per-version** (not top-level!)
- `res_list_url`

`HypPackageData` (each entry in `game_pkgs[]` or `audio_pkgs[]`) contains:
- `language` (audio_pkgs only — values to observe in research task; don't lock literal codes in spec yet)
- `package` (download URL)
- `md5`
- `size`
- `decompressed_size` ← useful for pre-flight free-space check

### `patches[].version` is the FROM version (verified)

Collapse code at `GameVersionBase.GameApi.cs`:
```csharp
preloadRegionPatch.FirstOrDefault(x => x.Version == GameVersionInstalled)
```
Matches against the *currently installed* version. So `patches[i].version == currentLocal` means "this entry takes you from currentLocal to main.major.version".

### Branch decision (single-hop or full)

```
read config.ini -> currentVer
fetchPackages -> latestVer (main.major.version)
if currentVer == latestVer: PlanNone
else if patches[] has entry where .version == currentVer:
    use that patch  (download patch.zip ~2-5GB)
else:
    use main.major  (full reinstall — game_pkgs[] full payload, ~80GB for Genshin)
```

NO multi-hop chain. v1 simplification (Collapse v3-pre behavior).

### Patch zip internal structure (CORRECTED from my draft §2)

After downloading & extracting a patch zip, the canonical layout per `InstallManagerBase.cs` in Collapse:

```
staging/  (or directly into gameDir per Collapse — implementation choice)
  hdifffiles.txt          ← ONE WORD, no underscore. (My §2 draft had `hdiff_files.txt` — WRONG.)
  deletefiles.txt
  hdiffmap.json           ← NEWER format. Collapse handles BOTH this and hdifffiles.txt.
  GenshinImpact_Data/
    foo.dll.hdiff         ← HPatchZ binary diff for foo.dll
    bar.dll               ← (?) direct file replacement — but UNVERIFIED in Collapse source
    ...
```

**`hdifffiles.txt`** — each line deserializes to a `PkgVersionProperties` JSON object with at least:
- `remoteName` (path within game dir)
- `fileSize`

**`hdiffmap.json`** — modern format. Collapse handles it as alternative to hdifffiles.txt. Root structure:
```jsonc
{
  "entries": [
    {
      "sourceFileName": "...",
      "targetFileName": "...",
      "patchFileName": "...",
      "sourceFileSize": ...,
      "sourceMD5Hash": "...",
      "targetFileSize": ...,
      "canDeleteSource": true|false
    },
    ...
  ]
}
```

**`deletefiles.txt`** — plain path lines, one per line.

**`.hdiff` extension** — verified (`patchPath = patchBasePath + ".hdiff"` in Collapse).

**Direct full-replacement files in same zip** — my §2 draft speculated these exist. NOT documented in Collapse. Drop this assumption; rely only on hdiff + delete operations.

### M3.B v1 design choice: which diff-listing format?

Collapse handles both `hdifffiles.txt` and `hdiffmap.json`. For M3.B v1, **pick one** based on what Genshin's CDN currently emits. Plan task 1 (protocol research) should observe a recent Genshin patch to decide. If both are emitted, prefer `hdiffmap.json` (newer, more structured, MD5 included for sourceFile validation).

### Audio pack handling — CORRECTED

Path: `<gameDir>/GenshinImpact_Data/StreamingAssets/AudioAssets/` ✓ verified (Genshin 3.6+; pre-3.6 was `StreamingAssets/Audio/GeneratedSoundBanks/Windows/` — irrelevant for M3.B which targets 5.x+).

Subfolder names — Collapse uses an indirection via static file `audio_lang_14` and `_gameVoiceLanguageID`. Literal subfolder names (`Chinese`, `English(US)`, `Japanese`, `Korean`) are widely cited in community but **NOT** lifted directly from Collapse source. Plan task 1 should validate against a real install.

`audio_pkgs[].language` field values — observe in research task. Don't lock literal lang codes (`zh-cn` / `en-us` / `ja-jp` / `ko-kr`) in spec without verification.

### Local version detection: `<gameDir>/config.ini`

Format verified via `GameVersionBase.IniConfig.cs`:

```ini
[General]
channel=1
sub_channel=1
cps=mihoyo
game_version=5.6.0
```

Read with stdlib `bufio.Scanner` (no third-party ini lib — format is simple). The launcher itself writes this file; on a fresh HoYoPlay install the file may not exist until something writes it. `version_unknown` error code covers both "file absent" and "key missing".

HoYoPlay (the meta-launcher above per-game folders) has its own higher-level state file, but `<gameDir>/config.ini` remains authoritative for the installed version. Don't integrate with HoYoPlay state in M3.B.

---

## State machine corrections (from `§3` review)

**My §3 draft had a few errors** — record them so the next brainstorm doesn't repeat:

1. **Predownload completion marker** is `progress.json` → `predl_ready.json` rename (per kurogames' `progressStore.RenameToPredlReady`), NOT a `Phase=Predownload, complete=true` field. M3.B should use the same rename pattern.
2. **`InFlightOp.Stage`** in M3.A is empty string during download (Phase carries it); only `verifying` stage uses non-empty Stage. M3.B introduces `extracting` / `patching` / `applying` / `cleanup` stages — this is a **divergence** from M3.A's convention; document it explicitly in the M3.B spec.
3. **`CancelInFlight`** in `update_handler.go` blocks cancel during `Phase==PhaseApply` (kurogames-era safety guard). M3.B's new `extracting` and `patching` sub-stages should map to `Phase=PhaseDownload` (still cancellable) — ONLY when truly atomic-renaming files should the Phase flip to `PhaseApply`. Otherwise the guard accidentally blocks cancel during long extract/patch phases.
4. **`scanForRecovery`** is single-rooted in v0.3.1. M3.B needs to generalize it: iterate over `a.providers`, call `a.tempDirFor(backend.ID(), "")` for each, walk each tree. This was deferred from m3-refactor explicitly.

---

## HPatchZ binary distribution

- Source: `https://github.com/sisong/HDiffPatch/releases` (BSD-3 license)
- Windows console build: ~200-400KB (Collapse embeds ~250KB version)
- Embed via `go:embed`:
  ```
  third_party/hpatchz/
    hpatchz.exe        ← bsdiff console binary
    LICENSE
    README.md          ← source URL + version pin
  ```
- Binary impact on `omnigate.exe`: +~250KB (12.27MB → ~12.5MB)

Implementation: extract the embedded binary to a temp file at first use, invoke via `exec.CommandContext(ctx, hpatchzPath, oldFile, hdiffFile, newFile)`. Cancel propagates via context.

Plan task 1 should download the actual release artifact, verify its size + signature, and commit binary + LICENSE + README to repo.

---

## Suggested package layout (carry into M3.B brainstorm)

```
internal/providers/hoyoverse/
  apply_lock{,_windows,_other}.go    // mirror kurogames pattern
  audio_pack.go                       // detect AudioAssets/<lang>/ subfolders
  config_ini.go                       // parse <gameDir>/config.ini for game_version
  hoyoverse.go                        // extend M2 Provider — implement core.Updater + core.ProcessChecker
  hpatchz.go                          // embed hpatchz.exe + runner
  process_check{,_windows,_other}.go  // mirror kurogames; Provider.IsGameRunning impl
  update_apply.go                     // extract zip → run hpatchz → atomic rename + delete
  update_download.go                  // 1-2 worker zip blob downloader (byte-range resume)
  update_manifest.go                  // fetchPackages + branch decision
  update_progress.go                  // hoyoverse-specific progressStore using core.ProgressFile
  testdata/
    manifest-sample.json
    config-ini-sample.ini

third_party/hpatchz/
  hpatchz.exe
  LICENSE
  README.md
```

**Skip the layout's helper files** if M3.B brainstorming reveals consolidation makes more sense. This is a starting point, not a contract.

---

## Sources for reference (Collapse Launcher repo)

- `HypLauncherGameResourcePackageApi.cs` — manifest schema
- `InstallManagerBase.cs` — patch zip apply algorithm
- `GenshinInstall.cs` — Genshin-specific install logic
- `GameVersionBase.GameApi.cs` — patch chain matching
- `GameVersionBase.IniConfig.cs` — config.ini read/write
- `audio_lang_14` static file mechanism — audio pack lang resolution

Plus community references for items not covered in Collapse:
- LiuQixuan gist: Genshin config.ini sample
- GesthosNetwork/GI-Hdiff-Patcher: deletefiles.txt convention

---

## What to do FIRST in the M3.B brainstorming session

1. Read this file end-to-end.
2. Skim the m3-refactor spec + plan + final memory `project_status.md` to confirm what's available.
3. Decide whether the locked decisions (top of this file) still apply, or if anything needs revisit.
4. Brainstorm any **open** questions (not in locked-decisions table). Examples:
   - Predownload UX: should the bell drawer auto-show predl-ready, or only on user click?
   - Patch zip extraction location: directly into gameDir (Collapse) vs separate staging dir (safer for cancel)?
   - Audio pack disk-space prompt: warn user if patch needs >X GB free?
   - Concurrent updates across games: allowed (per-game InFlight) or queued?
5. Write the spec, follow the same review-iterate-ship pattern m3-refactor used.
