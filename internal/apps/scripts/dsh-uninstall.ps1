$ErrorActionPreference = "Continue"

function Emit-ProgressLine {
    param(
        [int]$Progress,
        [string]$Phase
    )
    Write-Output "CSGHUB_PROGRESS|$Progress|$Phase"
}

Emit-ProgressLine 20 "uninstalling_dsh"
$installRoot = $env:CSGHUB_LITE_DSH_INSTALL_ROOT
if ([string]::IsNullOrWhiteSpace($installRoot)) {
    $installRoot = Join-Path $env:USERPROFILE ".local\share\deepseek-harness"
}
$launcherPath = Join-Path $env:USERPROFILE ".local\bin\dsh.cmd"

Remove-Item -Recurse -Force $installRoot -ErrorAction SilentlyContinue
Remove-Item -Force $launcherPath -ErrorAction SilentlyContinue

Emit-ProgressLine 100 "complete"
Write-Output "INFO: DeepSeek Harness uninstall complete."
