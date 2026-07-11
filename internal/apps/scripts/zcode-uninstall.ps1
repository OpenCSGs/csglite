$ErrorActionPreference = "Stop"

$homeDir = if ($env:USERPROFILE) { $env:USERPROFILE } else { [Environment]::GetFolderPath("UserProfile") }
$runtimeRoot = Join-Path $homeDir ".local\share\zcode"
$launcherPath = Join-Path $homeDir ".local\bin\zcode.cmd"

Write-Output "CSGHUB_PROGRESS|10|stopping_uninstaller"
if (Test-Path -LiteralPath $runtimeRoot) {
    $uninstaller = Get-ChildItem -LiteralPath $runtimeRoot -Filter "Uninstall ZCode.exe" -File -Recurse |
        Select-Object -First 1 -ExpandProperty FullName
    if ($uninstaller) {
        & $uninstaller "/S"
        if ($LASTEXITCODE -ne 0) {
            throw "ZCode uninstaller exited with code $LASTEXITCODE"
        }
    }
}

Write-Output "CSGHUB_PROGRESS|60|removing_runtime"
if (Test-Path -LiteralPath $runtimeRoot) {
    Remove-Item -LiteralPath $runtimeRoot -Recurse -Force
}

Write-Output "CSGHUB_PROGRESS|85|removing_launcher"
if (Test-Path -LiteralPath $launcherPath) {
    Remove-Item -LiteralPath $launcherPath -Force
}

Write-Output "CSGHUB_PROGRESS|100|complete"
Write-Output "INFO: removed the managed ZCode runtime and launcher"
