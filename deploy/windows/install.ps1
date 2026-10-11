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

# Runs a command as administrator (one UAC prompt). EncodedCommand avoids
# quoting problems with paths that contain spaces.
function Invoke-Elevated([string]$cmd) {
  $enc = [Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($cmd))
  Start-Process powershell -Verb RunAs -Wait -WindowStyle Hidden -ArgumentList "-NoProfile -EncodedCommand $enc"
}

# Stops every running copy, including one started by double-clicking the download.
function Stop-OpenCouchpad {
  $p = Get-Process opencouchpad* -ErrorAction SilentlyContinue
  if ($p) { $p | Stop-Process -Force; $p | Wait-Process -Timeout 5 -ErrorAction SilentlyContinue }
}

if ($Uninstall) {
  if (Test-Path $exe) { & $exe --uninstall }
  Stop-OpenCouchpad
  Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
  try { Invoke-Elevated "netsh advfirewall firewall delete rule name=OpenCouchpad" } catch { Write-Warning "Firewall rule not removed." }
  Write-Host "OpenCouchpad removed."
  exit 0
}

$src = Get-ChildItem -Path $PSScriptRoot, (Get-Location) -Filter "opencouchpad*.exe" -ErrorAction SilentlyContinue | Select-Object -First 1
if (-not $src) { throw "Put opencouchpad-windows-*.exe next to this script." }

New-Item -ItemType Directory -Force -Path $dir | Out-Null
Stop-OpenCouchpad
Copy-Item $src.FullName $exe -Force
# Drop the "downloaded from the internet" mark so SmartScreen doesn't block the hidden start or the login start.
Unblock-File $exe

$flags = @()
if ($Pin)      { $flags += "--pin", $Pin }
if ($Name)     { $flags += "--name", $Name }
if ($HostName) { $flags += "--host", $HostName }
$quoted = @("--background") + ($flags | ForEach-Object { if ($_ -match '\s') { "`"$_`"" } else { $_ } })

# Allow phones on private networks to reach it (asks for admin once).
try {
  Invoke-Elevated "netsh advfirewall firewall delete rule name=OpenCouchpad | Out-Null; netsh advfirewall firewall add rule name=OpenCouchpad dir=in action=allow program='$exe' enable=yes profile=private"
} catch { Write-Warning "Firewall rule not added; Windows will ask on first start." }
$public = Get-NetConnectionProfile -ErrorAction SilentlyContinue | Where-Object NetworkCategory -eq Public
if ($public) {
  Write-Warning "Your network '$($public[0].Name)' is set to Public, so phones can't connect. Set it to Private in Settings > Network & internet > (your Wi-Fi/Ethernet) > Network profile type."
}

& $exe --install @flags
Start-Process $exe -ArgumentList $quoted -WindowStyle Hidden
Write-Host "OpenCouchpad is running and will start at login. Open http://remote.local on your phone."
