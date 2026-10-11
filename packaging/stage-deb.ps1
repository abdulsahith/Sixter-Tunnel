# Run on Windows from the project root:  .\packaging\stage-deb.ps1
# Builds fresh Linux binaries (frontends embedded) and copies them into the
# .deb tree, ready to copy to the VPS for packaging.

$ErrorActionPreference = "Stop"
$root = Split-Path $PSScriptRoot -Parent
Set-Location $root

Write-Host "Building frontends + Linux binaries..." -ForegroundColor Cyan
.\build.ps1 -Dashboard -Landing -Linux

$bin = "packaging\sixter-deb\usr\local\bin"
New-Item -ItemType Directory -Force -Path $bin | Out-Null
Remove-Item "$bin\.keep" -ErrorAction SilentlyContinue

Copy-Item bin\linux\sixter            $bin\ -Force
Copy-Item bin\linux\sixter-agent      $bin\ -Force
Copy-Item bin\linux\sixter-client     $bin\ -Force
Copy-Item bin\linux\sixter-server     $bin\ -Force
Copy-Item bin\linux\sixter-repo       $bin\ -Force
Copy-Item bin\linux\sixter-reposerver $bin\ -Force

# bundle the VPS hardening files (applied by the .deb postinst)
$share = "packaging\sixter-deb\usr\local\share\sixter\hardening"
New-Item -ItemType Directory -Force -Path $share | Out-Null
Copy-Item packaging\hardening\sixter-sysctl.conf,
          packaging\hardening\sixter-nftables.nft,
          packaging\hardening\nginx-hardening.conf,
          packaging\hardening\apply-hardening.sh,
          packaging\hardening\README.md $share\ -Force

Write-Host "Staged binaries + hardening into packaging\sixter-deb" -ForegroundColor Green
Write-Host "Now copy the folder 'packaging\sixter-deb' to your VPS and run:" -ForegroundColor Yellow
Write-Host '  chmod 0755 sixter-deb/DEBIAN/postinst sixter-deb/DEBIAN/prerm sixter-deb/usr/local/bin/*'
Write-Host '  dpkg-deb --build --root-owner-group sixter-deb sixter_2.0.0_amd64.deb'
Write-Host '  sudo dpkg -i sixter_2.0.0_amd64.deb'
