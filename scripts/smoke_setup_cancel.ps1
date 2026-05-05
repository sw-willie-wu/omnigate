# Smoke #4 (cancel) setup: delete a 109 MB pak so download takes long enough
# that user can click × mid-flight.
$ErrorActionPreference = 'Stop'

$pak = 'C:\Program Files\Wuthering Waves\Wuthering Waves Game\Client\Content\Paks\pakchunk73-WindowsNoEditor.pak'
$bak = "$env:USERPROFILE\Desktop\pakchunk73.pak.bak"
$cfg = 'C:\Program Files\Wuthering Waves\Wuthering Waves Game\launcherDownloadConfig.json'

Copy-Item $pak $bak -Force
Write-Host "1/3 backup: $bak"

$j = Get-Content $cfg -Raw | ConvertFrom-Json
$j.version = '3.0.0'
[System.IO.File]::WriteAllText($cfg, ($j | ConvertTo-Json -Depth 10), [System.Text.UTF8Encoding]::new($false))
Write-Host "2/3 fake version=3.0.0 in $cfg"

Remove-Item $pak -Force
Write-Host "3/3 deleted: $pak"
