$ErrorActionPreference = "Continue"

function Emit-Progress([int]$Progress, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Progress|$Phase"
}

Emit-Progress 20 "uninstalling_kimi_code"
$installDir = $env:KIMI_INSTALL_DIR
if ([string]::IsNullOrWhiteSpace($installDir)) {
    $installDir = Join-Path $env:USERPROFILE ".kimi-code"
}
$launcherPath = Join-Path $env:USERPROFILE ".local\bin\kimi.cmd"

Remove-Item -Recurse -Force $installDir -ErrorAction SilentlyContinue
Remove-Item -Force $launcherPath -ErrorAction SilentlyContinue

Emit-Progress 100 "complete"
Write-Output "INFO: Kimi Code uninstall complete."
