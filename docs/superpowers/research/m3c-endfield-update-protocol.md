# Endfield (GRYPHLINK) `get_latest` update protocol — research (2026-06-02)

> Fixture note: `testdata/get_latest-sample.json` is **LIVE-sourced** — captured
> directly from `launcher.gryphline.com/api/game/get_latest` on 2026-06-02 (the
> sandbox network reached the endpoint). It is byte-for-byte identical in shape to
> the public archive `daydreamer-json/ak-endfield-api-archive`
> (`output/akEndfield/launcher/game/6/latest.json`), trimmed to a representative
> 4-pack subset (3 full packs + the final partial-size pack). No account/device
> IDs are present (the endpoint is unauthenticated).

## Outputs (Phase A/B read these)

```
PHASE_B_APPROACH       = Option A (file-level incremental via game_files manifest). NOT packs. See DECISION section below + spec §0.4.
LOCAL_VERSION_SOURCE   = RESOLVED: <gameDir>/config.ini, AES-256-CBC encrypted (key/IV below) → INI `version=` line. Verified live on a real install 2026-06-02 → version=1.2.5. See §Local version source.
UPTODATE_DISCRIMINATOR = RESOLVED via version compare: local config.ini `version` != get_latest `version`. NOTE: do NOT use action==1 when version is known (all fixtures show action==1, up-to-date value never observed → would loop a freshly-updated install). action==1 is a degrade-only hint. See §Up-to-date discriminator.
PER_FILE_CDN           = {pkg.file_path} serves `/game_files` (AES manifest) + `/<path>` (per-file downloads). The artifact Option A consumes. Confirm full-coverage at smoke (Q3/R1).
RESPONSE_ENVELOPE      = flat object (NO {rsp:{...}} wrapper) on the GET variant omnigate ships. The live HTTP body IS the get_latest object. (The Collapse plugin uses a POST batch_proxy with a WRAPPED proxy_rsps[].get_latest_game_rsp — different envelope; see spec §0.6.) Archive wraps each capture as {updatedAt,req,rsp} for storage only.
```

## Endpoint

```
GET https://launcher.gryphline.com/api/game/get_latest
```

Query params:

| Param | Required | Value (global / region "os") | Notes |
|---|---|---|---|
| `appcode` | yes | `YDUTE5gscDZ229CW` | game appCode |
| `launcher_appcode` | yes | `TiaytKBUIEdoEwRT` | launcher appCode |
| `channel` | yes | `6` | |
| `sub_channel` | yes | `6` | |
| `launcher_sub_channel` | yes | `6` | |
| `version` | optional | semver e.g. `1.2.5` | the *installed* version; omit to query latest. The community client (`ak-endfield-api-archive` `src/utils/api/akEndfield/launcher.ts`) validates with `semver.valid()` and sends `undefined` when null. |

Unauthenticated — no headers, cookies, account ID, device ID, or signature
required. A bare `curl` GET returns the full body.

CDN host for package downloads: `beyond.hg-cdn.com`.

### Out of scope: CN region

CN uses game appCode `6LL0KJuqHBVz33WK`, `channel=1`, and a different launcher
base (`launcherCN`). M3.C targets **global / "os" only**.

## Response shape

The HTTP body is a **flat JSON object** (there is no `rsp` envelope on the wire;
the archive's `{updatedAt, req, rsp}` wrapper is a storage convention — `rsp` is
the body). Top-level fields:

| Field | Type | Meaning |
|---|---|---|
| `action` | int | update action code (observed always `1`) |
| `state` | int | (observed always `0`) |
| `launcher_action` | int | (observed always `0`) |
| `version` | string | **target / latest** game version (e.g. `1.2.5`) |
| `client_version` | string \| null | latest client version; mirrors `version` on unversioned queries — NOT the locally installed version |
| `request_version` | string | echoes the `version` query param (`""` when omitted) |
| `pkg` | object | full-install package set — see below |
| `patch` | object \| null | delta-update descriptor. **M3.C IGNORES this** (LOCKED) |
| `pre_patch` | object \| null | pre-download delta descriptor. Ignored. |

### `pkg` object (full install)

| Field | Type | Meaning |
|---|---|---|
| `packs` | array | ordered list of pack parts (see below) |
| `total_size` | string | total bytes across all packs (e.g. `107144762581`) |
| `file_path` | string | CDN base for per-file (VFS) downloads — `.../files` |
| `url` | string | unused for pkg (empty `""`) |
| `md5` | string | unused for pkg (empty `""`) |
| `package_size` | string | unused for pkg (`"0"`) |
| `file_id` | string | (`"0"`) |
| `sub_channel` | string | `"6"` |
| `game_files_md5` | string | aggregate MD5 of the installed file tree |

Each entry of `pkg.packs[]`:

| Field | Type | Meaning |
|---|---|---|
| `url` | string | CDN URL of one pack part (`.zip.NNN`) |
| `md5` | string | **MD5** of that pack part (lowercase hex) |
| `package_size` | **string** | byte size of that part — a JSON **string**, e.g. `"1073741824"` (preserve as string when unmarshalling) |

> The hash algorithm is **MD5** (per-pack `md5` + `pkg.game_files_md5`). There is
> no SHA in this protocol.

## DECISION (REVISED 2026-06-02) — Option A: file-level incremental via `game_files`

> **Supersedes the earlier "full packs only" decision.** After reading the Collapse
> plugin source (`HgGameRepairer.cs` / `HgGameInstaller.Install.cs`), Phase B was
> re-scoped to **Option A**: M3.C does **NOT** consume `pkg.packs[]` (full split zips)
> or `patch.patches[]` (delta zips). Instead it mirrors the plugin's **repair layer** —
> fetch the new version's `game_files` manifest (`{pkg.file_path}/game_files`, AES
> decrypt → JSON-lines `{path,md5,size}`), MD5-compare vs the local install, and download
> only changed/missing files individually from `{pkg.file_path}/<path>`, then atomic
> rename + config.ini AES re-encrypt writeback. **No zip-extract, no concat, no
> HDiffPatch, no MultiVolumeStream.** The pack/delta protocol below is documented for
> completeness but is **not consumed** by Option A (it's the rejected Option B / a
> M3.C.v2 candidate). See the spec §0.4 for the full rationale. The `pack_url_regex`
> TEST_ANCHOR is retained as a true protocol fact (Phase A drift test) though unused.

## Pack URL form

Observed (live + archive), 46 parts for `1.2.5`:

```
https://beyond.hg-cdn.com/YDUTE5gscDZ229CW/1.2/update/6/6/Windows/1.2.5_GyQOi4WaWC2Ju0kW/packs/Beyond_Release_v1d2-Rel-os-6434019-8_prod_obt_official.zip.001
                          └ appcode ──────┘ └maj.min└chan└sub└OS  └ver_<rand>──────┘        └ base name ────────────────────────────────────────┘ └3-digit part┘
```

- `<rand>` (`GyQOi4WaWC2Ju0kW`) is a per-build identifier in the path — not a
  secret, but redacted in sanitized fixtures/logs for noise reduction (see
  `sanitizeURL` in Task A2). The pack `<ver>_<rand>` segment is what
  `randSegRe` targets.
- Parts are zero-padded 3-digit suffixes `.001`..`.NNN`; the final part is a
  smaller `package_size` (the remainder).

### TEST_ANCHOR — pack URL regex

Phase B's drift test (`m3c_protocol_doc_test.go`) parses the block below.

<!-- TEST_ANCHOR: pack_url_regex -->
^https://beyond\.hg-cdn\.com/[A-Za-z0-9]+/[0-9.]+/update/\d+/\d+/Windows/[0-9.]+_[A-Za-z0-9]+/packs/.+\.zip\.\d{3}$
<!-- END_ANCHOR: pack_url_regex -->

## Local version source

> **`LOCAL_VERSION_SOURCE = RESOLVED 2026-06-02`** — `<gameDir>/config.ini`,
> AES-256-CBC encrypted. Decrypt → INI text; read the `version=` line. **Verified
> live on a real install** (`C:\Program Files\GRYPHLINK\games\EndField Game\config.ini`,
> 256 bytes) → decrypts to:
>
> ```ini
> [Game]
> version=1.2.5
> entry=Endfield.exe
> entry_md5=3154e0efecfc2db4585da5389325fe91
> appcode=YDUTE5gscDZ229CW
> region=sg
> channel=6
> sub_channel=6
> ```
>
> This is the kuro-`launcherDownloadConfig.json` equivalent. After an update,
> write the new version back by **re-encrypting** config.ini with the same key/IV.

### AES parameters (from Collapse plugin `HgCrypto.cs`; verified working)

- **Algorithm:** AES-256-CBC, PKCS7 padding.
- **Key (32 bytes):** `C0 F3 0E 1C E7 63 BB C2 1C C3 55 A3 43 03 AC 50 39 94 44 BF F6 8C 4A 22 AF 39 8C 0A 16 6E E1 43`
- **IV (16 bytes):** `33 46 78 61 19 27 50 64 95 01 93 72 64 60 84 00`
- These are a **reverse-engineered constant already publicly published** in
  `misaka10843/Hi3Helper.Plugin.Hypergryph/Hi3Helper.Hypergryph.Core/Utils/HgCrypto.cs`
  (a Collapse Launcher plugin). Omnigate hardcodes them with attribution
  (consistent with the existing kurogames `AppCred` hardcoded constant). Used
  ONLY for interop with the official launcher's local config format. If omnigate
  is published and this draws concern, swap to build-time/runtime injection.

### `game_files` — same AES → file-level MD5 manifest

`<gameDir>/game_files` (159 KB on the test install) decrypts with the SAME AES
key/IV to JSON-lines: `{"path":"...","md5":"...","size":N}` per installed file —
the full per-file manifest. Enables MD5 change-detection + repair (the kuro
`filterChangedFiles` equivalent). Phase B uses this.

### What M2/pre-spec research ruled OUT (do not re-investigate)

- `Endfield.exe` FileVersion → Unity **engine** version (`2021.3.34f5`).
- `Endfield_Data/app.info` → only `"Gryphline\nEndfield"`.
- GRYPHLINK `<root>/<x.y.z>/` folder → **launcher** version (was `1.3.0`, self-updated
  to `1.4.0`), not the game.
- `HKCU\Software\GRYPHLINK\Launcher\<hash>` → has `install_path` but NO game version.
- `eld_*.db` (e.g. `eld_Games.db`) → SQLCipher-encrypted, not openable; not needed
  (config.ini is the clean source).
- Resource-index files (`Endfield_Data/Persistent/index_main.json`) use a DIFFERENT
  cipher (base64 + additive Vigenère, key `Assets/Beyond/DynamicAssets/Gameplay/UI/Fonts/`)
  and carry the **VFS res_version** (`7215718-17`), not the semver — that's the
  in-client hot-update layer, out of M3.C scope.

## Up-to-date discriminator

> **`UPTODATE_DISCRIMINATOR = RESOLVED`** — compare the **local** `config.ini`
> `version` against `get_latest`'s `version`. Per Collapse `HgGameManager.cs`:
> `IsGameHasUpdate = (ApiGameVersion != CurrentGameVersion) || latestGameInfo.Action == 1`.

So `action`/`state`/`launcher_action` are NOT the primary discriminator (they stay
`1/0/0` regardless — confirmed across all 9 archive samples + the live GET). The
real signal is the **version-string comparison**, which we can now do because the
local version is readable (config.ini). `action==1` is an additional
update-available hint. `client_version` in the response also echoes the latest
(`1.2.5`).

`testdata/get_latest-uptodate-sample.json` is therefore no longer needed for the
discriminator (version compare suffices); still worth capturing at smoke for
completeness.

## Sources

- Live capture: `GET https://launcher.gryphline.com/api/game/get_latest?...`
  (2026-06-02, this session).
- Public archive `daydreamer-json/ak-endfield-api-archive`:
  - `output/akEndfield/launcher/game/6/latest.json` (full `1.2.5` response)
  - `output/akEndfield/launcher/game/6/all_patch.json` (9 `{updatedAt,req,rsp}` captures)
  - `src/utils/api/akEndfield/launcher.ts` (param names + semver validation)
  - `MEMO.md` (v2 `patch` HDiffPatch format)
- **AUTHORITATIVE reference — Collapse Launcher Hypergryph plugin** `misaka10843/Hi3Helper.Plugin.Hypergryph` (fully reverse-engineered Endfield; trust per project policy, same as M3.B's Collapse Sophon reference). Verbatim mirror sources for M3.C:
  - `Hi3Helper.Hypergryph.Core/Utils/HgCrypto.cs` — AES-256-CBC key/IV + decrypt/encrypt.
  - `Hi3Helper.Hypergryph.Core/Utils/ConfigTool.cs` — read config.ini → `ParseVersion`.
  - `Hi3Helper.Hypergryph.Core/Management/HgGameManager.cs` — version + `IsGameHasUpdate`.
  - `Hi3Helper.Hypergryph.Core/Management/HgGameInstaller.Install.cs` — install/update flow.
  - `Hi3Helper.Hypergryph.Core/Management/Api/HgApiContext.cs` + `HgApiStructs.cs` — get_latest.
  - `Hi3Helper.Hypergryph.Core/Utils/MultiVolumeStream.cs` — `.zip.NNN` volume concat.
  - `SharpHDiffPatch.Core/` — C# HDiffPatch (Phase B incremental).
  - Found via `lTinchl/Xel-Launcher` (C# Hypergryph launcher) → its dep `Hi3Helper.Plugin.Endfield`.
