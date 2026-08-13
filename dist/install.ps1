# Installs clipfan on Windows and registers a per-user Scheduled Task that runs
# the daemon at logon. Windows services run in Session 0 and cannot access the
# clipboard, so a logon task — like launchd / systemd --user on macOS/Linux — is
# the right model for a clipboard daemon.
#
# Usage (from an extracted dist folder that also contains the matching exe):
#     powershell -ExecutionPolicy Bypass -File install.ps1 [-NoRestart] [-Dest <dir>]
#
# Parameters:
#     -NoRestart  install the binary only; do not create/start the scheduled task
#     -Dest       install directory for the binary (default: $env:USERPROFILE\.local\bin,
#                 matching the ~/.local/bin convention used on macOS/Linux)

[CmdletBinding()]
param(
    [switch]$NoRestart,
    [string]$Dest = (Join-Path $env:USERPROFILE ".local\bin")
)

$ErrorActionPreference = "Stop"

# 1. Resolve the matching per-arch binary that ships beside this script.
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    "AMD64" { "amd64" }
    "ARM64" { "arm64" }
    default { throw "Unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$here = Split-Path -Parent $MyInvocation.MyCommand.Definition
$src = Join-Path $here "clipfan-windows-$arch.exe"
if (-not (Test-Path $src)) {
    throw "Missing binary: $src (expected next to install.ps1 in the dist folder)"
}

# 2. Install the binary.
New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$exe = Join-Path $Dest "clipfan.exe"
Copy-Item $src $exe -Force
Write-Host "Installed $exe"

# 3. Ensure the config dir exists (the daemon writes config.json here on first run,
#    mirroring ~/.config/clipfan on macOS/Linux).
$cfgDir = Join-Path $env:USERPROFILE ".config\clipfan"
New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
Write-Host "Config dir: $cfgDir"

if ($NoRestart) { return }

# 4. Stop any running instance and remove a previously registered task.
Get-Process -Name clipfan -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Unregister-ScheduledTask -TaskName "clipfan" -Confirm:$false -ErrorAction SilentlyContinue

# 5. Register a per-user Scheduled Task that runs the daemon at logon, in the
#    user's own session (so it can read/write the clipboard). Auto-restarts on
#    failure, mirroring the systemd Restart=always unit.
$action   = New-ScheduledTaskAction -Execute $exe
$trigger  = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
$settings = New-ScheduledTaskSettingsSet `
    -StartWhenAvailable `
    -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) `
    -DontStopIfGoingOnBatteries -AllowStartIfOnBatteries
Register-ScheduledTask -TaskName "clipfan" `
    -Action $action -Trigger $trigger -Settings $settings `
    -RunLevel Limited -Description "clipfan clipboard sync daemon" -Force | Out-Null
Start-ScheduledTask -TaskName "clipfan"
Write-Host "clipfan scheduled task created and started (runs at logon, auto-restarts)."
