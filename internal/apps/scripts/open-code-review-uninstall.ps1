$ErrorActionPreference = "Stop"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

$runtimeRoot = Join-Path $env:USERPROFILE ".local\share\open-code-review"
$launcherDir = Join-Path $env:USERPROFILE ".local\bin"
$launcherPath = Join-Path $launcherDir "ocr.exe"

Emit-Progress 5 "preflight"

Emit-Progress 55 "removing_runtime"
if (Test-Path $launcherPath) {
    Remove-Item -Path $launcherPath -Force -ErrorAction SilentlyContinue
}
if (Test-Path $runtimeRoot) {
    Remove-Item -Path $runtimeRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Emit-Progress 80 "verifying_uninstall"
if (Get-Command ocr -ErrorAction SilentlyContinue) {
    $cmd = (Get-Command ocr).Source
    throw "Open Code Review binary is still available at $cmd"
}

Emit-Progress 100 "complete"
Write-Output "INFO: Open Code Review uninstallation complete"
