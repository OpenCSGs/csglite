$ErrorActionPreference = "Stop"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

Emit-Progress 5 "preflight"

$launchers = @(
    (Join-Path $env:USERPROFILE ".local\bin\agy.exe"),
    (Join-Path $env:USERPROFILE "bin\agy.exe")
)

Emit-Progress 55 "removing_runtime"
foreach ($launcher in $launchers) {
    if (Test-Path $launcher) {
        Remove-Item -Path $launcher -Force
    }
}

$stagingDir = Join-Path $env:LOCALAPPDATA "antigravity\staging"
if (Test-Path $stagingDir) {
    Remove-Item -Path $stagingDir -Recurse -Force -ErrorAction SilentlyContinue
}

Emit-Progress 80 "verifying_uninstall"
if (Get-Command agy -ErrorAction SilentlyContinue) {
    Write-Output "ERROR: Antigravity binary is still available at $((Get-Command agy).Source)"
    exit 1
}

Emit-Progress 100 "complete"
Write-Output "INFO: Antigravity uninstallation complete"
