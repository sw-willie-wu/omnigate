# M3.B Genshin Protocol Validation

**Date:** 2026-05-06
**Source:** Live `getGamePackages` probes + local Genshin install check.
**API endpoint:** `https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api/getGamePackages?launcher_id=VYTpXlbWo8&game_ids[]=gopR6Cufr3`
**Observed game version at probe time:** 5.5.0

## Step 1 — byte-range CDN support

- Probe URL: `https://autopatchhk.yuanshen.com/client_app/download/pc_zip/20250314110016_HcIQuDGRmsbByeAE/GenshinImpact_5.5.0.zip.001`
- Probe URL manifest size: `10737418240` bytes (first split part)
- CDN domain: `autopatchhk.yuanshen.com` (Alibaba OSS + CloudFront)
- HEAD `Accept-Ranges: bytes`: ✓
- HEAD `Content-Length` matches manifest size: ✓ (10737418240)
- HEAD `ETag` header: present (value: `"D84E0E7EE26E05E62AEF927A85C7A2F1-100"`)
  - Note: ETag is a multipart hash (suffix `-100` = 100 parts); NOT a simple MD5 of the file.
    Do NOT use ETag to verify integrity against `game_pkgs[].md5`. Use manifest `md5` field instead.
- HEAD `Last-Modified`: `Fri, 14 Mar 2025 16:16:55 GMT`
- HEAD `x-oss-object-type`: `Multipart`
- HEAD server stack: `AliyunOSS` + CloudFront (`X-Amz-Cf-Pop: TPE54-P2`)
- Range `bytes=0-1023` returns `HTTP/1.1 206 Partial Content`: ✓
- `Content-Range: bytes 0-1023/10737418240` in 206 response: ✓
- Downloaded file size = 1024 bytes: ✓
- **Conclusion:** byte-range resume IS safe to use. The CDN (AliyunOSS + CloudFront) fully
  supports `Accept-Ranges: bytes`. Manifest stability comparison should use `game_pkgs[].md5`
  (standard MD5 of full file) from the API response as the fingerprint — the ETag is a
  multipart-upload hash not suitable for direct content verification. The `manifest_etag` sidecar
  field (spec §2) should store the raw ETag string for conditional GET, while integrity checking
  uses the API-provided MD5.

## Step 2 — hdiff format

- patches[] available at observation time: **yes** (versions 5.4.0→5.5.0 and 5.3.0→5.5.0)
- Patch URL examined: `https://autopatchhk.yuanshen.com/client_app/update/hk4e_global/game_5.4.0_5.5.0_hdiff_IlvHovyEdpXnwiCH.zip`
- Patch zip size: `16321789202` bytes (16 GB — full-game delta)
- Patch zip format: **ZIP64** (1112 total entries; central directory parsed from file tail)
- Zip TOC observed (parsed from central directory fetched via byte-range):
  - `hdiffmap.json`: ✗ (not present)
  - `hdifffiles.txt`: ✓ (present — lists files to apply hdiff patches to)
  - `deletefiles.txt`: ✓ (present — lists files to delete)
  - `*.hdiff` files: ✓ (88 `.hdiff` files observed, e.g. `GenshinImpact_Data/StreamingAssets/AudioAssets/Banks0.pck.hdiff`)
  - `pkg_version`: ✓ (present — package version metadata)
- Extension breakdown: `.blk` 904 files, `.hdiff` 88 files, `.dll` 35, `.exe` 5, `.assets` 8, `.usm` 5
- **Conclusion:** The observed format uses `hdifffiles.txt` (legacy-style listing) — NOT
  `hdiffmap.json` (modern format). Per locked decision (spec §0), M3.B v1 supports BOTH formats.
  Currently observed format: **legacy** (`hdifffiles.txt` + `deletefiles.txt` + `*.hdiff`).
  Task 15 (`update_patch.go`) must parse `hdifffiles.txt` for the file-rename/patch map and
  `deletefiles.txt` for deletion list.

## Step 3 — audio_pkgs[].language values

- Observed values from `major.audio_pkgs[]`:
  - `zh-cn` (size: 16129028997 bytes, ~15 GB)
  - `en-us` (size: 18412580831 bytes, ~17 GB)
  - `ko-kr` (size: 15886147984 bytes, ~15 GB)
  - `ja-jp` (size: 20934015385 bytes, ~19 GB)
- Observed values from `patches[0].audio_pkgs[]` (5.4.0→5.5.0 delta):
  - `zh-cn`, `en-us`, `ja-jp`, `ko-kr` (same four codes, delta sizes ~288-348 MB)
- **Conclusion:** lang code → AudioAssets folder name mapping table
  (used in `update_manifest.go::audioLanguageIntersect`):

  | API `language` value | Folder name (Genshin 5.x StreamingAssets/AudioAssets/) |
  |---|---|
  | `zh-cn` | `Chinese` |
  | `en-us` | `English(US)` |
  | `ja-jp` | `Japanese` |
  | `ko-kr` | `Korean` |

  Note: folder names are inferred from audio zip filenames (`Audio_Chinese_5.5.0.zip`,
  `Audio_English(US)_5.5.0.zip`, etc.) observed in the `audio_pkgs[].url` field. The actual
  on-disk folder names follow this pattern. Task 12 (`update_manifest.go`) should hardcode
  this mapping (4 languages, stable across 5.x versions per Collapse reference).

## Step 4 — config.ini format

- Local Genshin install path `C:\Program Files\Genshin Impact\` not found on this host.
- **DEFERRED:** to smoke checklist Task 22 / Task 6 fixtures.
- Fallback reference: Collapse Launcher `IniConfig.cs` (per project convention — trust Collapse,
  don't run live-server probes). Expected format based on Collapse source:
  - Encoding: UTF-8 **with** or **without** BOM (both observed in wild; parser should strip BOM)
  - Line endings: CRLF on Windows installs
  - Structure: `[General]` section header, then key=value pairs
  - Key of interest: `game_version=X.Y.Z` (e.g. `game_version=5.5.0`)
  - `config_ini.go` parser MUST handle BOM (`\xef\xbb\xbf` prefix) and both CRLF/LF line endings
    (`bufio.Scanner` default handles both).

## Step 5 — hpatchz binary

See `internal/providers/hoyoverse/third_party_hpatchz/README.md` for tag + SHA-256 + size.

Summary:
- **Tag:** `v4.12.2`
- **Release zip:** `hdiffpatch_v4.12.2_bin_windows64.zip` (878 KB)
- **hpatchz.exe size:** 481280 bytes (~470 KB)
- **hpatchz.exe SHA-256:** `d4ba353cea752216d6c2dc32669681b55bd50dee24a58b2cc41e9933d81e91ff`
- **License:** MIT (not BSD-3 as anticipated in plan; `LICENSE` file confirms "MIT License, HDiffPatch, Copyright (c) 2012-2025 housisong")

## Additional observations

### API field naming deviation from plan
The plan templates reference `game_pkgs[].package` but the actual field is `game_pkgs[].url`.
Update Tasks 11/12 accordingly: use `.url` (not `.package`) when referencing `getGamePackages` response fields.

### Patch zip is ZIP64
The `game_5.4.0_5.5.0_hdiff_*.zip` file is 16 GB and uses ZIP64 extensions. Any Go code that
opens these zips must use `archive/zip` (which handles ZIP64 transparently) rather than streaming
unzip utilities that may have ZIP64 limitations.

### CDN infrastructure
Alibaba OSS (`autopatchhk.yuanshen.com`) fronted by CloudFront (`X-Amz-Cf-Pop: TPE54-P2`).
This explains the dual-CDN caching behavior. For our purposes, byte-range requests work reliably.
