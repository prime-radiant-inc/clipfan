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

# 2. Stop any running instance and remove a previously registered task.
#    (Must happen before the file copy: the running daemon and any
#    sshd-spawned gateway children lock the exe. Retry briefly — processes
#    may take a moment to terminate and release their handles.)
if (-not $NoRestart) {
    Get-ScheduledTask -TaskName "clipfan" -ErrorAction SilentlyContinue | Stop-ScheduledTask -ErrorAction SilentlyContinue
    Get-Process -Name clipfan, clipfan-tray -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    $deadline = (Get-Date).AddSeconds(10)
    while ((Get-Date) -lt $deadline -and (Get-Process -Name clipfan -ErrorAction SilentlyContinue)) {
        Start-Sleep -Milliseconds 500
    }
    Unregister-ScheduledTask -TaskName "clipfan" -Confirm:$false -ErrorAction SilentlyContinue
}

# 3. Install the binaries (daemon + tray app).
New-Item -ItemType Directory -Force -Path $Dest | Out-Null
$exe = Join-Path $Dest "clipfan.exe"
Copy-Item $src $exe -Force
$traySrc = Join-Path $here "clipfan-tray-windows-$arch.exe"
$trayExe = Join-Path $Dest "clipfan-tray.exe"
if (Test-Path $traySrc) {
    Copy-Item $traySrc $trayExe -Force
    Write-Host "Installed $exe"
    Write-Host "Installed $trayExe"
} else {
    Write-Host "Installed $exe (tray app not present in this package; skipping)"
}

# 4. Ensure the config dir exists (the daemon writes config.json here on first run,
#    mirroring ~/.config/clipfan on macOS/Linux).
$cfgDir = Join-Path $env:USERPROFILE ".config\clipfan"
New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
Write-Host "Config dir: $cfgDir"

if ($NoRestart) { return }

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

# 7. Start the tray app at logon via the user's Startup folder (a tray app
#    needs an interactive desktop session; the Run registry key / Startup
#    shortcut is the standard per-user autostart).
if (Test-Path $trayExe) {
    $startup = [Environment]::GetFolderPath("Startup")
    $shortcutPath = Join-Path $startup "clipfan-tray.lnk"
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = $trayExe
    $shortcut.Description = "clipfan tray app"
    $shortcut.Save()
    Write-Host "Tray autostart shortcut: $shortcutPath"
    # Start it now if it isn't already running — but only from an interactive
    # session. A tray app started in session 0 (e.g. the installer run over
    # SSH, from a scheduled task, or an sshd-spawned shell) has no desktop to
    # attach its icon to and just lingers invisibly; the Startup shortcut
    # covers that case at next logon.
    $mySession = (Get-Process -Id $PID).SessionId
    if ($mySession -ne 0 -and -not (Get-Process -Name "clipfan-tray" -ErrorAction SilentlyContinue)) {
        Start-Process -FilePath $trayExe
        Write-Host "Tray app started."
    }
}
