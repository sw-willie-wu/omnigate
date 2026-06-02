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
LOCAL_VERSION_SOURCE   = PENDING-USER: needs real install — see §Local version source
UPTODATE_DISCRIMINATOR = PENDING-LIVE: archive + live both show action=1,state=0,launcher_action=0 with full packs; see §Up-to-date discriminator
PACKAGE_PATH_LIVE      = yes (live + archive both return non-empty pkg.packs[]); confirm at smoke
RESPONSE_ENVELOPE      = flat object (NO {rsp:{...}} wrapper). The live HTTP body IS the get_latest object. The archive wraps each capture as {updatedAt,req,rsp} for storage only — `rsp` is the body. Confirm live shape at smoke (already observed flat 2026-06-02).
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

## LOCKED decision — full packs only

M3.C consumes `pkg.packs[]` (full install / sequential split archive) and
**ignores `patch` / `pre_patch`** (the v2 HDiffPatch delta format described in the
archive `MEMO.md`). This keeps Phase B's apply logic to "download all parts →
concatenate/verify → extract", with no diff engine.

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

> **`LOCAL_VERSION_SOURCE = PENDING-USER`** — resolving this requires a real
> Endfield install, which this research session does not have. Below is the
> investigation procedure to run on a real install, in priority order. This step
> resolves **two** questions at once:
> 1. Where the GRYPHLINK launcher records the **installed game version** (so
>    `version.go` can populate `VersionInfo.Current`).
> 2. Whether a **custom (non-default) install location** can be auto-discovered
>    (detection parity — relevant to `detect.go`/`bg.go`).

### What M2/pre-spec research already ruled OUT

(From the prior `version.go` stub comment — do not re-investigate these:)

- `Endfield.exe` FileVersion → the Unity **engine** version (`2021.3.34f5`), not
  the game version.
- `Endfield_Data/app.info` → only `"Gryphline\nEndfield"`, no version.
- GRYPHLINK's `<root>/<x.y.z>/` folder → the **launcher** version, not the game.

### Candidate sources to check on a real install (priority order)

1. **A launcher-written JSON/config under the game dir** holding a version that
   matches `rsp.version` (e.g. `1.2.5`). Check, near `Endfield.exe` / under the
   install root:
   - any `*.json` / `*.config` / `manifest*` / `version*` / `config.ini` written
     by GRYPHLINK (not Unity). Grep installed files for the literal installed
     version string (e.g. `1.2.5`) to locate the authoritative record.
   - The protocol's `pkg.game_files_md5` and `file_path` (`.../<ver>_<rand>/files`)
     suggest the launcher persists the installed version somewhere to drive its
     own update check — find that store.
2. **Windows registry**: `HKCU\Software\Hypergryph\…\Endfield` (and `HKLM`
   fallback). Inspect for an install-path value AND a version value. (`reg query
   HKCU\Software\Hypergryph /s` on a real machine.) A registry install-path value
   would also answer the custom-location auto-discovery question.
3. **A sidecar under `%LOCALAPPDATA%` / the GRYPHLINK launcher data dir** — the
   launcher likely keeps a per-game record (installed version + install path) it
   reads to render its own "update available" state. Look under
   `%LOCALAPPDATA%`, `%APPDATA%`, and the GRYPHLINK install dir for a games/state
   JSON. This is the most promising for BOTH the version and custom-path questions
   because a launcher must persist exactly this to function offline.

Community references to mine on a real install (no install needed to *read* them,
but they describe layouts to verify): `AugustLigh/LLauncher`,
`daydreamer-json/ak-endfield-api-archive` (`MEMO.md`, `src/utils/`).

### Degrade path if none is reliable

Spec §6: if no clean local source surfaces, `version.go` leaves
`VersionInfo.Current` **empty** (sidebar shows `就緒` with no `· vX.Y` suffix) and
still populates `Latest` from `get_latest.version`. Task A3 Step 0 branches on
this outcome.

## Up-to-date discriminator

> **`UPTODATE_DISCRIMINATOR = PENDING-LIVE`** — not observable from fixtures.

Across **all 9 archive samples** (`output/.../game/6/all_patch.json`), including
queries that send `version=<installed>` (e.g. `1.0.13`, `1.0.14`, `1.1.9`), every
response is identical on the discriminator fields:

```
action = 1,  state = 0,  launcher_action = 0,  pkg.packs = non-empty (full set)
```

The live unversioned GET (2026-06-02) likewise returns `action=1, state=0,
launcher_action=0`, `request_version=""`, `client_version="1.2.5"`, full packs.

So an **up-to-date** response (client already on latest) is NOT captured anywhere
available to this session. The user/smoke must perform a **live GET with
`&version=<latest>`** (i.e. claim to already have the newest build) and record how
the response differs. Likely discriminators to confirm:

- `pkg.packs` becomes **empty** (nothing to download), and/or
- `action` / `launcher_action` changes to a "no update" code, and/or
- `version == request_version` is itself the signal.

Phase B's `CheckForUpdate` must use the confirmed discriminator. Until then,
treat "non-empty `pkg.packs` AND `version != local`" as update-available, and
record the real up-to-date body into
`testdata/get_latest-uptodate-sample.json` during the smoke.

## Sources

- Live capture: `GET https://launcher.gryphline.com/api/game/get_latest?...`
  (2026-06-02, this session).
- Public archive `daydreamer-json/ak-endfield-api-archive`:
  - `output/akEndfield/launcher/game/6/latest.json` (full `1.2.5` response)
  - `output/akEndfield/launcher/game/6/all_patch.json` (9 `{updatedAt,req,rsp}` captures)
  - `src/utils/api/akEndfield/launcher.ts` (param names + semver validation)
  - `MEMO.md` (v2 `patch` HDiffPatch format — explicitly out of scope for M3.C)
