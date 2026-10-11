# Build sixter-setup.exe (Windows installer) via Inno Setup.
#
# Prereqs:
#   - Go (in PATH)
#   - Inno Setup 6  (https://jrsoftware.org/isdl.php)  → provides ISCC.exe
#
# Usage:
#   packaging\windows\build-windows-installer.ps1                 # version 2.0.0
#   packaging\windows\build-windows-installer.ps1 -Version 2.1.0

param([string]$Version = "2.0.0")
$ErrorActionPreference = "Stop"

# repo root = two levels up from this script (packaging\windows\)
$root = Resolve-Path (Join-Path $PSScriptRoot "..\..")
Push-Location $root
try {
    Write-Host "Building Windows client binaries..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path bin\windows | Out-Null
    $env:GOOS = "windows"; $env:GOARCH = "amd64"; $env:CGO_ENABLED = "0"
    go build -buildvcs=false -ldflags="-s -w" -o bin\windows\sixter.exe        .\cmd\sixter
    go build -buildvcs=false -ldflags="-s -w" -o bin\windows\sixter-client.exe .\cmd\tunnel-client
    go build -buildvcs=false -ldflags="-s -w" -o bin\windows\sixter-repo.exe   .\cmd\repo-client
    Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED
    Write-Host "  -> bin\windows\{sixter,sixter-client,sixter-repo}.exe" -ForegroundColor Green

    # locate the Inno Setup compiler
    $iscc = $null
    foreach ($p in @(
        "${env:ProgramFiles(x86)}\Inno Setup 6\ISCC.exe",
        "${env:ProgramFiles}\Inno Setup 6\ISCC.exe"
    )) { if (Test-Path $p) { $iscc = $p; break } }
    if (-not $iscc) {
        $cmd = Get-Command ISCC.exe -ErrorAction SilentlyContinue
        if ($cmd) { $iscc = $cmd.Source }
    }
    if (-not $iscc) {
        throw "ISCC.exe (Inno Setup 6) not found. Install from https://jrsoftware.org/isdl.php"
    }

    Write-Host "Compiling installer with Inno Setup..." -ForegroundColor Cyan
    New-Item -ItemType Directory -Force -Path installer | Out-Null
    & $iscc "/DMyAppVersion=$Version" "packaging\windows\sixter-setup.iss"
    if ($LASTEXITCODE -ne 0) { throw "ISCC failed ($LASTEXITCODE)" }

    Write-Host "`nDone -> installer\sixter-setup.exe" -ForegroundColor Green
}
finally {
    Pop-Location
}
