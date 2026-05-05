# Smoke #7 (crash recovery) setup: delete several pak files so download phase
# spans multiple files. Killing the launcher mid-download will leave
# progress.json in tempDir → on restart, scanForRecovery should fire.
$ErrorActionPreference = 'Stop'
$root = 'C:\Program Files\Wuthering Waves\Wuthering Waves Game'
$desk = "$env:USERPROFILE\Desktop"
$cfg  = "$root\launcherDownloadConfig.json"

$files = @(
    'Client\Content\Paks\pakchunk73-WindowsNoEditor.pak',
    'Client\Content\Paks\pakchunk3-WindowsNoEditor.pak',
    'Client\Content\Paks\pakchunk106-WindowsNoEditor.pak',
    'Client\Content\Paks\pakchunk18-WindowsNoEditor.pak'
)

foreach ($rel in $files) {
    $src = Join-Path $root $rel
    $name = Split-Path $rel -Leaf
    $bak = Join-Path $desk "$name.bak"
    Copy-Item $src $bak -Force
    Write-Host "backup: $bak"
    Remove-Item $src -Force
    Write-Host "deleted: $src"
}

$j = Get-Content $cfg -Raw | ConvertFrom-Json
$j.version = '3.0.0'
[System.IO.File]::WriteAllText($cfg, ($j | ConvertTo-Json -Depth 10), [System.Text.UTF8Encoding]::new($false))
Write-Host "version=3.0.0 in $cfg"
