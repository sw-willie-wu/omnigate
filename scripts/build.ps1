# Build the portable omnigate.exe and stage it into dist/.
#
# Wails has no native "output the binary to a separate dir" option: `wails build`
# only takes -o (filename) and always writes to <build:dir>/bin, and build:dir
# relocates the WHOLE build/ tree (incl. the committed icon/manifest/installer
# assets) — so it can't be pointed at the gitignored dist/. Hence this wrapper:
# run the normal build, then copy the artifact into dist/.
#
# Usage:  pwsh ./scripts/build.ps1            # host platform (windows/amd64)
#         pwsh ./scripts/build.ps1 -upx       # extra `wails build` args pass through
#
# NOTE: do NOT set $ErrorActionPreference='Stop' globally — wails logs to stderr,
# and under 'Stop' PowerShell turns native stderr into a terminating error. Gate
# on $LASTEXITCODE for the native call; use -ErrorAction Stop on the cmdlets.
$repo = Split-Path -Parent $PSScriptRoot
Push-Location $repo
try {
    $wails = Join-Path (go env GOPATH) 'bin/wails.exe'
    if (-not (Test-Path $wails)) { throw "wails CLI not found at $wails (go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0)" }
    & $wails build @args
    if ($LASTEXITCODE -ne 0) { throw "wails build failed (exit $LASTEXITCODE)" }
    New-Item -ItemType Directory -Force -Path dist -ErrorAction Stop | Out-Null
    Copy-Item -Force build/bin/omnigate.exe dist/omnigate.exe -ErrorAction Stop
    Write-Host "staged -> dist/omnigate.exe"
} finally {
    Pop-Location
}
