[CmdletBinding()]
param(
    [string]$DeviceName = 'DotControl Windows',
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'DotControl'),
    [string]$StateDir = (Join-Path ([Environment]::GetFolderPath('ApplicationData')) 'DotControl'),
    [string]$TunnelId = '',
    [string]$Go = 'go'
)
$ErrorActionPreference = 'Stop'
if (-not [Environment]::Is64BitOperatingSystem) { throw 'Windows x64 is required.' }
$projectRoot = Split-Path -Parent $PSScriptRoot
$dependencyLock = Get-Content -LiteralPath (Join-Path $projectRoot 'dependencies.lock.json') -Raw | ConvertFrom-Json
$installRoot = [IO.Path]::GetFullPath($InstallDir)
$binDir = Join-Path $installRoot 'bin'
$cacheDir = Join-Path $installRoot 'downloads'
New-Item -ItemType Directory -Path $binDir,$cacheDir -Force | Out-Null
function Get-VerifiedArchive([string]$Url, [string]$File, [string]$SHA256) {
    if (-not (Test-Path -LiteralPath $File)) { Invoke-WebRequest -Uri $Url -OutFile $File }
    if ((Get-FileHash -LiteralPath $File -Algorithm SHA256).Hash.ToLowerInvariant() -ne $SHA256) {
        throw "Checksum mismatch: $File. Kept for inspection; no extraction performed."
    }
}
$driverAsset = $dependencyLock.cua_driver.assets.'windows-x86_64'
$driverZip = Join-Path $cacheDir $driverAsset.file
Get-VerifiedArchive "https://github.com/trycua/cua/releases/download/$($dependencyLock.cua_driver.tag)/$($driverAsset.file)" $driverZip $driverAsset.sha256
$driverDir = Join-Path $installRoot "cua-$($dependencyLock.cua_driver.version)"
Expand-Archive -LiteralPath $driverZip -DestinationPath $driverDir -Force
$driverCandidates = @(Get-ChildItem -LiteralPath $driverDir -Filter cua-driver.exe -Recurse -File)
if ($driverCandidates.Count -ne 1) { throw 'Expected exactly one cua-driver.exe in the verified archive.' }
$driverPath = $driverCandidates[0].FullName
$tunnelAsset = $dependencyLock.tunnel_client.windows_amd64
$tunnelZip = Join-Path $cacheDir $tunnelAsset.file
Get-VerifiedArchive "https://github.com/openai/tunnel-client/releases/download/v$($dependencyLock.tunnel_client.version)/$($tunnelAsset.file)" $tunnelZip $tunnelAsset.sha256
$tunnelDir = Join-Path $installRoot "tunnel-$($dependencyLock.tunnel_client.version)"
Expand-Archive -LiteralPath $tunnelZip -DestinationPath $tunnelDir -Force
$tunnelCandidates = @(Get-ChildItem -LiteralPath $tunnelDir -Filter tunnel-client.exe -Recurse -File)
if ($tunnelCandidates.Count -ne 1) { throw 'Expected exactly one tunnel-client.exe in the verified archive.' }
$agentPath = Join-Path $binDir 'dotcontrol.exe'
Push-Location $projectRoot
try {
    & $Go build -trimpath -o $agentPath ./cmd/dotcontrol
    if ($LASTEXITCODE -ne 0) { throw 'Go build failed.' }
} finally { Pop-Location }
$licenseDir = Join-Path $installRoot 'licenses'
New-Item -ItemType Directory -Path $licenseDir -Force | Out-Null
Copy-Item -LiteralPath (Join-Path $projectRoot 'LICENSE') -Destination $licenseDir -Force
Copy-Item -LiteralPath (Join-Path $projectRoot 'third_party') -Destination $licenseDir -Recurse -Force
if (Test-Path -LiteralPath (Join-Path $StateDir 'config.json')) {
    Write-Host "Existing device identity preserved. Review driver_path/tunnel_path in $StateDir/config.json."
} else {
    & $agentPath --state $StateDir setup --name $DeviceName --driver $driverPath --tunnel $tunnelCandidates[0].FullName --tunnel-id $TunnelId
    if ($LASTEXITCODE -ne 0) { throw 'Setup failed.' }
}
Write-Host "Installed: $agentPath"
Write-Host 'Next: doctor, credential (hidden input), then serve. See docs/INSTALL.md.'
