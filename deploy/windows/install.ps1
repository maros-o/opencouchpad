# Install OpenCouchpad for the current Windows user.
#   Right-click > "Run with PowerShell" next to opencouchpad-windows-amd64.exe,
#   or: powershell -ExecutionPolicy Bypass -File install.ps1 [-Pin 1234] [-Name "Office PC"] [-HostName office]
param(
  [string]$Pin = "",
  [string]$Name = "",
  [string]$HostName = "",
  [switch]$Uninstall
)
$ErrorActionPreference = "Stop"
$dir = Join-Path $env:LOCALAPPDATA "OpenCouchpad"
$exe = Join-Path $dir "opencouchpad.exe"

if ($Uninstall) {
  if (Test-Path $exe) { & $exe --uninstall }
  Get-Process opencouchpad -ErrorAction SilentlyContinue | Stop-Process -Force
  Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
  Write-Host "OpenCouchpad removed."
  exit 0
}

$src = Get-ChildItem -Path $PSScriptRoot, (Get-Location) -Filter "opencouchpad*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $src) { throw "Put opencouchpad-windows-*.exe next to this script." }

New-Item -ItemType Directory -Force -Path $dir | Out-Null
Get-Process opencouchpad -ErrorAction SilentlyContinue | Stop-Process -Force
Copy-Item $src.FullName $exe -Force

$flags = @()
if ($Pin)      { $flags += "--pin", $Pin }
if ($Name)     { $flags += "--name", $Name }
if ($HostName) { $flags += "--host", $HostName }
$quoted = @("--background") + ($flags | ForEach-Object { if ($_ -match '\s') { "`"$_`"" } else { $_ } })

# Allow phones on private networks to reach it (asks for admin once).
$rule = "netsh advfirewall firewall add rule name=OpenCouchpad dir=in action=allow program=`"$exe`" enable=yes profile=private"
try { Start-Process powershell -Verb RunAs -Wait -ArgumentList "-NoProfile -Command `"netsh advfirewall firewall delete rule name=OpenCouchpad | Out-Null; $rule`"" } catch { Write-Warning "Firewall rule not added; Windows will ask on first start." }

& $exe --install @flags
Start-Process $exe -ArgumentList $quoted -WindowStyle Hidden
Write-Host "OpenCouchpad is running and will start at login. Open http://remote.local on your phone."
