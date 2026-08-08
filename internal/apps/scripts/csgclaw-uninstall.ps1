$ErrorActionPreference = "Stop"
$runtimeRoot = Join-Path $env:USERPROFILE ".local\share\csgclaw-desktop"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

Emit-Progress 20 "stopping_services"
Get-Process -ErrorAction SilentlyContinue | Where-Object {
    $_.ProcessName -match "^(csgclaw|csgclaw-desktop)$"
} | ForEach-Object {
    $_.CloseMainWindow() | Out-Null
}

Emit-Progress 45 "running_uninstaller"
$uninstallCommand = $null
$registryPaths = @(
    "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
    "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
    "HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
)
foreach ($entry in Get-ItemProperty -Path $registryPaths -ErrorAction SilentlyContinue) {
    if ([string]$entry.DisplayName -match "CSGClaw") {
        $uninstallCommand = if ($entry.QuietUninstallString) { [string]$entry.QuietUninstallString } else { [string]$entry.UninstallString }
        if ($uninstallCommand) { break }
    }
}

if ($uninstallCommand) {
    $process = Start-Process -FilePath "cmd.exe" -ArgumentList "/d", "/s", "/c", $uninstallCommand -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "CSGClaw Desktop uninstaller exited with code $($process.ExitCode)"
    }
} else {
    throw "CSGClaw Desktop uninstaller was not found"
}

Emit-Progress 85 "removing_runtime"
if (Test-Path -LiteralPath $runtimeRoot) {
    Remove-Item -LiteralPath $runtimeRoot -Recurse -Force
}

Emit-Progress 100 "complete"
Write-Output "INFO: CSGClaw Desktop uninstallation complete"
