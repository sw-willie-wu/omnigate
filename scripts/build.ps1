# Build the portable omnigate.exe and stage it into dist/.
#
# Wails has no native "output the binary to a separate dir" option: `wails build`
# only takes -o (filename) and always writes to <build:dir>/bin, and build:dir
# relocates the WHOLE build/ tree (incl. the committed icon/manifest/installer
# assets) — so it can't be pointed at the gitignored dist/. Hence this wrapper:
# run the normal build, then copy the artifact into dist/.
#
# It also stamps the Windows PE file version from the real app version: Wails
# reads info.productVersion only from wails.json (no build flag), and that field
# is normally unset → defaults to 1.0.0.0. We patch it from the nearest git tag
# (mirroring frontend/vite.config.ts) before building and restore wails.json
# afterward, so the committed file stays clean.
#
# Usage:  pwsh ./scripts/build.ps1            # host platform (windows/amd64)
#         pwsh ./scripts/build.ps1 -upx       # extra `wails build` args pass through
#
# NOTE: do NOT set $ErrorActionPreference='Stop' globally — wails logs to stderr,
# and under 'Stop' PowerShell turns native stderr into a terminating error. Gate
# on $LASTEXITCODE for the native call; use -ErrorAction Stop on the cmdlets.

# Resolve the numeric X.Y.Z for the PE version resource.
#   1. OMNIGATE_VERSION env (CI sets it to the release tag, e.g. v0.3.1)
#   2. nearest reachable v* tag — incl. milestone "-mN" tags, since clean release
#      tags live on main's squash commits and aren't in dev-branch ancestry
#      (so a local dev build stamps the current milestone, e.g. v0.5.0-m3c → 0.5.0)
#   3. 0.0.0 when git has no tags
# Strips the leading "v" and any "-suffix" (the Windows FIXEDFILEINFO version is
# numeric-only).
function Resolve-PEVersion {
    $v = $env:OMNIGATE_VERSION
    if (-not $v) {
        $v = (& git describe --tags --match 'v*' 2>$null) | Select-Object -First 1
    }
    if (-not $v) { return '0.0.0' }
    $v = ($v -replace '^v', '')
    $v = ($v -split '-')[0]
    if ($v -match '^\d+\.\d+\.\d+$') { return $v }
    return '0.0.0'
}

$repo = Split-Path -Parent $PSScriptRoot
Push-Location $repo
$wjson = Join-Path $repo 'wails.json'
$origJson = [System.IO.File]::ReadAllText($wjson)
try {
    $wails = Join-Path (go env GOPATH) 'bin/wails.exe'
    if (-not (Test-Path $wails)) { throw "wails CLI not found at $wails (go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0)" }

    # Patch wails.json info.productVersion (reverted in finally).
    $ver = Resolve-PEVersion
    $cfg = $origJson | ConvertFrom-Json
    if (-not $cfg.PSObject.Properties['info']) {
        $cfg | Add-Member -NotePropertyName info -NotePropertyValue ([pscustomobject]@{})
    }
    if ($cfg.info.PSObject.Properties['productVersion']) { $cfg.info.productVersion = $ver }
    else { $cfg.info | Add-Member -NotePropertyName productVersion -NotePropertyValue $ver }
    # WriteAllText = UTF-8 *without* BOM (Set-Content -Encoding utf8 adds a BOM,
    # which Go's json parser rejects).
    [System.IO.File]::WriteAllText($wjson, ($cfg | ConvertTo-Json -Depth 20))
    Write-Host "wails.json info.productVersion -> $ver (Windows PE file version)"

    & $wails build @args
    if ($LASTEXITCODE -ne 0) { throw "wails build failed (exit $LASTEXITCODE)" }
    New-Item -ItemType Directory -Force -Path dist -ErrorAction Stop | Out-Null
    Copy-Item -Force build/bin/omnigate.exe dist/omnigate.exe -ErrorAction Stop
    Write-Host "staged -> dist/omnigate.exe"
} finally {
    [System.IO.File]::WriteAllText($wjson, $origJson) # restore committed wails.json verbatim
    Pop-Location
}
