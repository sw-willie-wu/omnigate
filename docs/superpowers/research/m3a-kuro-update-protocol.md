# Kuro WuWa update protocol — observed (2026-05-05)

Source basis: public documentation + live HTTP capture. **mitmproxy NOT required** — the relevant endpoints are unauthenticated (signed only by URL path), and their full structure was reverse-engineered by the community before this work.

## Source priority used (per spec §4.2)

1. ✅ **GitHub gists / OSS launchers** (priority 1 in this case — turned up complete protocol):
   - [DynamiByte's gist](https://gist.github.com/DynamiByte/d839bf9f671c975b6666d0f6e6634641) — endpoint catalog
   - [yuhkix/wuwa-downloader](https://github.com/yuhkix/wuwa-downloader) (Rust, MIT) — production reference impl
   - [yuhkix/wuwa.json gist](https://gist.githubusercontent.com/yuhkix/b8796681ac2cd3bab11b7e8cdc022254/raw/) — channel routing (live/beta × cn/os)
   - [DiceTsuki gist](https://gist.github.com/DiceTsuki/770b229126354e5167e45ae0d5de3d0c) — manual install procedure
2. ✅ **Live HTTP probe** (zero-credential GETs only, no mitmproxy):
   - `index.json` fetched + parsed
   - `indexFile.json` (full install) fetched + parsed (243 KiB, 603 entries)
   - `indexFile.json` (3.2.2→3.3.0 patch) fetched + parsed (266 KiB, 30 entries)
   - File CDN Range request verified (206 Partial Content)
3. ❌ Cache scan — confirmed cache contains only LAUNCHER-UI endpoints (bg config, news, social, payment); the game-update protocol uses a separate native HTTP client whose responses don't enter the WebView cache.

## Auth prerequisite — NONE

**Critical correction to spec §4.1:** the `accountID` we extracted in M2 (`50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c`) is **NOT per-machine**. It's a hardcoded `appId_appKey` pair issued by Kuro for the official launcher; identical for every WuWa-Global install on every machine.

| Constant | Value | Meaning |
|---|---|---|
| `appId` | `50004` | KuroGame app identifier (WuWa Global) |
| `appKey` | `obOHXFrFanqsaIEOmuKroCcbZkQRBC7c` | shared secret for OS/global region |
| `gameId` | `G153` | game identifier (WuWa) |
| accountID | `50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c` | concatenation of above; appears in URL paths |

This collapses M2's cache-scrape extraction step into a compile-time constant. **Implementation impact:** Task 7 (`update_manifest.go`) does NOT need `extractAccountID(); use a const.

Channel mapping (from yuhkix/wuwa.json):

| Region | Channel | App credential | Index URL |
|---|---|---|---|
| OS / global | live | `50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c` | `https://prod-alicdn-gamestarter.kurogame.com/launcher/game/G153/50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c/index.json` |
| OS / global | beta | `50013_HiDX7UaJOXpKl3pigJwVxhg5z1wllus5` | (same shape, different cred) |
| CN | live | `10003_Y8xXrXk65DqFHEDgApn3cpK5lfczpFx5` (G152) | `https://prod-cn-alicdn-gamestarter.kurogame.com/launcher/game/G152/...` |
| CN | beta | `10008_Pa0Q0EMFxukjEqX33pF9Uyvdc8MaGPSz` (G152) | (same shape) |

**M3.A scope = OS / live only.** CN/beta noted for completeness; treat as future extension.

## Endpoints

<!-- TEST_ANCHOR: manifest_url_regex -->
### Manifest top-level (`index.json`)

- **Method**: GET
- **URL**: `https://prod-alicdn-gamestarter.kurogame.com/launcher/game/G153/50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c/index.json`
- **Headers (request)**: `Accept-Encoding: gzip` (server requires; raw is gzipped without it)
- **Headers (response)**: `Last-Modified: <RFC 1123 date>`, `Content-MD5: <base64>`, `Content-Encoding: gzip`. **No `ETag`** at this level.
- **Cache validation**: `If-Modified-Since` against `Last-Modified` → 304.
- **Status**: 200 OK; gzipped JSON body.
- **Server**: Aliyun OSS (Tengine) — `https://prod-alicdn-gamestarter.kurogame.com/`.

URL template (regex form):

```
^https://prod-alicdn-gamestarter\.kurogame\.com/launcher/game/G153/50004_[A-Za-z0-9]+/index\.json$
```

<!-- END_ANCHOR: manifest_url_regex -->

### File-list manifest (`indexFile.json`)

- **Method**: GET
- **URL pattern**: `<cdn>/<config.indexFile>` — where `<cdn>` is selected from `index.json::default.cdnList[].url` (lowest `P` first) and `<config.indexFile>` is the relative path string from `index.json::default.config.indexFile`.
  - Example (full install): `https://hw-pcdownload-qcloud.aki-game.net/launcher/game/G153/50004/3.3.0/havaNOxMkiRcphWdKDvjILpCxEmxoxEx/resource/50004/3.3.0/indexFile.json`
  - Example (patch from 3.2.2 → 3.3.0): `https://hw-pcdownload-qcloud.aki-game.net/launcher/game/G153/50004/3.3.0/havaNOxMkiRcphWdKDvjILpCxEmxoxEx/resource/50004/3.3.0/3.2.2/indexFile.json`
- **Headers (response)**: `ETag: "<md5_hex>"` (the value equals the `indexFileMd5` field in `index.json`'s `config`/`patchConfig` block — same MD5 referenced two ways), `Accept-Ranges: bytes`, `Last-Modified`, `Content-Encoding: gzip`.
- **Cache validation**: `If-None-Match` against the ETag → 304.
- **Status**: 200 OK; gzipped JSON body.
- **Server**: Tencent COS (`tencent-cos`) — `https://hw-pcdownload-qcloud.aki-game.net/` (and 4 other CDN mirrors).

### File payloads

- **CDNs** (from `index.json::default.cdnList`, sorted by priority `P` ascending):
  | P | host |
  |---|---|
  | 0 | `https://hw-pcdownload-qcloud.aki-game.net/` (Tencent, primary) |
  | 0 | `https://hw-pcdownload-aws.aki-game.net/` (AWS CloudFront, primary) |
  | 150 | `https://pcdownload-huoshan.aki-game.net/` (ByteDance Volcano, fallback) |
  | 2205 | `https://hw-pcdownload-aliyun.aki-game.net/` (Aliyun, fallback) |
  | 7857 | `https://hw-pcdownload-akamai.aki-game.net/` (Akamai, fallback) |
- **URL pattern**: `<cdn>/<baseUrl><dest>` for full-install entries, `<cdn>/<entry.fromFolder><dest>` for patch entries (per-entry override).
- **Range support**: ✅ `Accept-Ranges: bytes`; `Range: bytes=0-1023` returns `HTTP 206 Partial Content` + correct `Content-Length: 1024`. Verified 2026-05-05.
- **Throttling**: no `X-RateLimit-*` / `Retry-After` headers observed. CloudFront/Tencent edge caches will likely 503 on aggressive parallel hammering — spec §2.8 retry policy (3 retries, 1s/4s/16s backoff) is appropriate.

## JSON shapes

### `index.json` top-level

```json
{
  "default": {
    "version": "3.3.0",
    "cdnList": [
      { "P": 0, "K1": 1, "K2": 1, "url": "https://hw-pcdownload-qcloud.aki-game.net/" },
      { "P": 0, "K1": 1, "K2": 1, "url": "https://hw-pcdownload-aws.aki-game.net/" }
    ],
    "config": {
      "version": "3.3.0",
      "indexFile": "launcher/game/G153/50004/3.3.0/<HASH>/resource/50004/3.3.0/indexFile.json",
      "indexFileMd5": "9214741ee69cc3d43ce65f92fa945fde",
      "baseUrl": "launcher/game/G153/50004/3.3.0/<HASH>/zip/",
      "size": 106887022428,
      "unCompressSize": 106887022428,
      "patchType": "patch",
      "patchConfig": [
        {
          "version": "3.2.2",
          "indexFile": "launcher/game/G153/50004/3.3.0/<HASH>/resource/50004/3.3.0/3.2.2/indexFile.json",
          "indexFileMd5": "e8209aacfc0c0d61b6bccc29b3e70449",
          "baseUrl": "launcher/game/G153/50004/3.3.0/<HASH>/resource/50004/3.3.0/3.2.2/resources/",
          "size": 30623762979,
          "unCompressSize": 103313076848,
          "ext": { "requiredDiskSpace": 42299403764, "maxFileSize": 13700756902 }
        }
      ]
    },
    "resources": "launcher/game/G153/50004/3.3.0/<HASH>/resource.json",
    "resourcesBasePath": "launcher/game/G153/50004/3.3.0/<HASH>/zip",
    "changelog": "",
    "changelogVisible": 0
  },
  "predownloadSwitch": 1,
  "keyFileCheckSwitch": 1,
  "keyFileCheckList": [
    "Client/Binaries/Win64/Client-Win64-Shipping.exe",
    "Client/Binaries/Win64/Client-Win64-ShippingBase.dll",
    "Wuthering Waves.exe",
    "Client/Binaries/Win64/ThirdParty/KrPcSdk_Global/KRSDKRes/KRSDK.bin"
  ],
  "RHIOptionSwitch": 1, "RHIOptionList": [...],
  "experiment": {...},
  "commandSwitch": 1, "commandList": [...],
  "fingerprints": [...],
  "resourcesLogin": [...]
}
```

**Predownload**: `predownloadSwitch == 1` indicates the launcher SUPPORTS predownload, but a `predownload` block (sibling of `default`) only appears when one is actively published. As of 2026-05-05 there is no active predownload, so the `predownload` key is absent.  When present, expect identical shape to `default` (channel-typed `version` / `cdnList` / `config` / `patchConfig`).

### `indexFile.json` (per-version file manifest)

```json
{
  "resource": [
    {
      "dest": "Wuthering Waves.exe",
      "md5": "eeaf9a3b0f3b7a136bb726903fa3a5e5",
      "size": 483768
    },
    {
      "dest": "Client/Content/Paks/pakchunk70-WindowsNoEditor.pak",
      "md5": "8077c874640921506289436f2cf85e90",
      "size": 29650949103,
      "chunkInfos": [
        { "start": 0, "end": 104857599, "md5": "cc6d9944068e4dda667420b417774ed0" },
        { "start": 104857600, "end": 209715199, "md5": "3b87eeb1b1385f1bf5adc1f8159f332d" }
      ]
    }
  ]
}
```

**Per-file entry — observed fields:**

| Field | Type | Required? | Meaning |
|---|---|---|---|
| `dest` | string | required | Relative path under game install dir (forward slashes; spaces allowed). e.g. `"Client/Content/Paks/pakchunk70-WindowsNoEditor.pak"`. Some entries have spaces (`"Wuthering Waves.exe"`) — caller must NOT URL-encode in the FS path layer; URL builder may need to encode (see §URL construction). |
| `md5` | string | required | Lowercase hex MD5 of the full file as it should appear on disk. |
| `size` | int64 | required | Bytes of the on-disk file. Note: bytes ON DISK = bytes from server (no compression at the file-payload level despite `/zip/` baseUrl segment — verified via Wuthering Waves.exe, server returns 483768 bytes which matches the on-disk MD5). |
| `chunkInfos[]` | array | optional | Present when `size > 100 MiB` (104857600 bytes). Each chunk: `{ start, end, md5 }`. Chunk size is exactly 100 MiB; last chunk may be shorter. Per-chunk MD5 enables partial resume + verify. |
| `fromFolder` | string | optional, patch-only | When this entry's bytes live under a different relative path than the parent `baseUrl` (typical for patch indexFiles). URL = `<cdn>/<fromFolder><dest>`; falls back to parent baseUrl if absent. |

**Top-level fields seen on real entries**: `["chunkInfos", "dest", "fromFolder", "md5", "size"]`. No `url` field per-entry (URL is constructed by caller from CDN + baseUrl + dest).

### Sample sizes (for sanity)

- Full install (`indexFile.json` for 3.3.0 fresh): **603 entries**, sum size ≈ **99.5 GiB** (mostly the pakchunk*.pak files; 90% of bytes in <50 entries).
- Patch (3.2.2 → 3.3.0, `3.2.2/indexFile.json`): **30 entries**, sum size ≈ **30 GiB compressed source** → ~96 GiB after apply.

## URL construction

Pseudocode the implementation will follow:

```
function fetchManifest():
    indexJSON = GET /launcher/game/G153/<APP_CRED>/index.json
    cdn = indexJSON.default.cdnList | filter(P == min) | random
    cfg = indexJSON.default.config              # or patchConfig[i] matching localVersion
    indexFileURL = cdn.url + cfg.indexFile
    indexFile = GET indexFileURL                # ETag = cfg.indexFileMd5
    return cfg, indexFile

function downloadFile(cdn, parentBaseUrl, entry):
    folder = entry.fromFolder ?? parentBaseUrl
    fileURL = cdn.url + folder + entry.dest    # spaces in `dest` should be %-encoded
    GET fileURL  # supports Range; chunkInfos used for resume + verify
```

## Diff format detection

**Decision rule applied:** look at `default.config.patchType`:

- `"patch"` → diff-based; consume `patchConfig[]` entry matching the user's local version. Each patch entry's `indexFile` lists ONLY the changed files (much smaller than full install). Apply by overwriting these files in place.
- `"full"` (hypothetical; not seen) → use `default.config.indexFile`; replace all listed files.
- absent → treat as `"full"`.

**Magic-byte inspection**: NOT needed at the M3.A level — patch entries are full-replace files, not hpatch/bsdiff binary deltas. Spec §1.2.3 MVP-minus is essentially the same code path; the only thing patch mode adds is "use the smaller per-version indexFile". This means:

> **Spec §1.2.3 MVP-minus is moot for WuWa specifically.** The entire patchConfig mechanism is a per-version file-list selector, not a binary-delta system. M3.A can ship patch support with no extra logic beyond "select the right indexFile URL by local version".

## Key file integrity check

`index.json::keyFileCheckList[]` lists 4 critical files:

```
Client/Binaries/Win64/Client-Win64-Shipping.exe
Client/Binaries/Win64/Client-Win64-ShippingBase.dll
Wuthering Waves.exe
Client/Binaries/Win64/ThirdParty/KrPcSdk_Global/KRSDKRes/KRSDK.bin
```

The official KRLauncher checks these on launch and triggers a repair if any is missing/wrong-MD5. M3.A should:
- Inform the user (via toast) if any of these is missing post-update — the install is broken even if all other files match.
- NOT block launch on this check (KRLauncher itself will catch it; we don't want to duplicate that gate).

## Sanitization rules (pre-commit regex)

Synthetic fixture (`testdata/manifest-sample.json`) is hand-curated and contains no live secrets, so sanitization is a no-op for it. If a future contributor commits a captured raw response, apply:

| Pattern | Replacement |
|---|---|
| `50004_[A-Za-z0-9]{32,}` | `<APP_CRED>` |
| `havaNOxMkiRcphWdKDvjILpCxEmxoxEx` (the 3.3.0 version-build hash; 32 chars alnum) | `<VERSION_HASH>` |
| `[a-f0-9]{32}` (any free-floating MD5; spare entry hashes — don't replace `md5` field values) | leave as-is unless verified secret |

The launcher protocol surfaces no per-user tokens / device IDs / OAuth secrets in cleartext (everything we observed was hardcoded constants + content hashes). Login-gated endpoints exist (`resourcesLogin` in index.json refers to them) but M3.A doesn't need them.

## Open questions / TODOs for M3.A.v2

1. **Predownload block shape**: assumed identical to `default`; verify when one is published next (typical: 2 weeks before a major version).
2. **CN region**: G152 uses different app credentials and CDN (`prod-cn-alicdn-gamestarter`); M3.B+ extension if CN support is added.
3. **`resourcesLogin`**: untested; spec doesn't surface it; capture if user-auth features become needed.
4. **`fingerprints` / `commandList` / `experiment`**: launcher feature flags; harmless to ignore.
5. **`resources` (string at `default` level)**: `"launcher/.../resource.json"` — different from `config.indexFile`; possibly an older manifest format. Untested. M3.A consumers should prefer `config.indexFile` (newer, has chunkInfos).

## Spec deltas (callouts for plan executor)

The spec at `docs/superpowers/specs/2026-05-04-launcher-collection-m3a-wuwa-update-design.md` was written before this research. **Implementer subagents should treat the spec's protocol assumptions as APPROXIMATE — the values below are corrected.** All architecture/state-machine/UX assertions in the spec remain valid.

| Spec claim | Reality | Implementation impact |
|---|---|---|
| `accountID` is per-user; extract via cache scrape | Hardcoded constant `50004_obOHXFrFanqsaIEOmuKroCcbZkQRBC7c` | Task 7 — drop `extractAccountID`, use `const APP_CRED`. |
| Manifest is one-step `{path, hash, size, url}` per file | Two-step: `index.json` → `indexFile.json`. URL constructed from `cdn + baseUrl + dest`; not stored per-entry. | Task 7 — `manifestRaw` struct gets a wrapper layer. |
| Hash algorithm = SHA-256 | **MD5** (32-char hex lowercase). | Task 8 — replace `crypto/sha256` with `crypto/md5`; rename `Hash` → `MD5` (or keep `Hash` field, document algo in struct comment). |
| Patch detection via per-entry `patchUrl/baseHash/patchSize` | Per-version `patchConfig[i].indexFile` selector | Task 7 — add `patchType + patchConfig[]` parsing; pick the right indexFile by local version. |
| Resume via mtime + size | **chunkInfos[] per file >100 MiB** with per-chunk MD5 enables byte-range resume + per-chunk verify | Task 8 — for `chunkInfos != nil`, do parallel range-GETs per chunk; verify each MD5; reassemble. For files without chunkInfos, single GET. Spec §5.1 mtime+size resume can be retained as a fallback for the chunkless case. |
| ETag is generic | `indexFile.json` ETag IS the file's MD5 (matches `config.indexFileMd5`); `index.json` uses `Last-Modified` only (no ETag) | Task 7 — `If-None-Match` for indexFile, `If-Modified-Since` for index.json. |
| Spec §1.2.3 MVP-minus = "no patch support" | Patch support adds ZERO complexity for WuWa (just URL selection) | Spec MVP-minus branch is moot; M3.A ships full patch support. |

## Change log

- 2026-05-05: initial protocol capture from public sources + live HTTP verification (no mitmproxy).
