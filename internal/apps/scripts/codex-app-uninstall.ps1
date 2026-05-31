$ErrorActionPreference = "Stop"

$runtimeRoot = Join-Path $env:USERPROFILE ".local\share\codex-app"
$launcherPath = Join-Path $env:USERPROFILE ".local\bin\codex-app.cmd"

if (Test-Path $runtimeRoot) {
    Remove-Item -Path $runtimeRoot -Recurse -Force
}
if (Test-Path $launcherPath) {
    Remove-Item -Path $launcherPath -Force
}

Write-Output "INFO: removed Codex App launcher and user data under $runtimeRoot"
