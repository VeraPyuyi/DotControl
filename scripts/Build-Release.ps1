[CmdletBinding()]
param([string]$Go = 'go', [string]$OutputDir = 'dist/release')
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
$previousCGO = $env:CGO_ENABLED
try {
    $out = [IO.Path]::GetFullPath((Join-Path $projectRoot $OutputDir))
    New-Item -ItemType Directory -Path $out -Force | Out-Null
    $manifest = @()
    foreach ($target in @(@('windows','amd64'),@('darwin','arm64'),@('darwin','amd64'))) {
        $env:GOOS = $target[0]; $env:GOARCH = $target[1]; $env:CGO_ENABLED = '0'
        $suffix = if ($target[0] -eq 'windows') { '.exe' } else { '' }
        $name = "dotcontrol-$($target[0])-$($target[1])$suffix"
        $file = Join-Path $out $name
        & $Go build -trimpath -buildvcs=false -ldflags '-s -w' -o $file ./cmd/dotcontrol
        if ($LASTEXITCODE -ne 0) { throw "Build failed: $name" }
        $manifest += [ordered]@{file=$name;sha256=(Get-FileHash -LiteralPath $file -Algorithm SHA256).Hash.ToLowerInvariant();bytes=(Get-Item -LiteralPath $file).Length;desktop_acceptance='See docs/validation/ACCEPTANCE.md'}
    }
    $manifest | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath (Join-Path $out 'SHA256.json') -Encoding utf8
    Write-Host "Built three targets in $out. Mac requires the native helper and system permissions."
} finally {
    $env:GOOS=$previousOS; $env:GOARCH=$previousArch; $env:CGO_ENABLED=$previousCGO
    Pop-Location
}
