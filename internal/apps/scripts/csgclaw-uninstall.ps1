$ErrorActionPreference = "Continue"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Remove-IfExists([string]$Path) {
    if ([string]::IsNullOrWhiteSpace($Path) -or -not (Test-Path -LiteralPath $Path)) {
        return
    }
    try {
        Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction Stop
        Write-Output "INFO: removed $Path"
    } catch {
        Write-Output "WARN: failed to remove ${Path}: $($_.Exception.Message)"
    }
}

function Stop-CSGClawProcesses {
    $binary = Join-Path $env:USERPROFILE ".local\bin\csgclaw.cmd"
    $binaryExe = Join-Path $env:USERPROFILE ".local\bin\csgclaw.exe"
    $pidFile = Join-Path $env:USERPROFILE ".csghub-lite\apps\logs\csgclaw.pid"
    $command = if (Test-Path -LiteralPath $binary) { $binary } elseif (Test-Path -LiteralPath $binaryExe) { $binaryExe } else { "" }
    if (-not [string]::IsNullOrWhiteSpace($command)) {
        if (Test-Path -LiteralPath $pidFile) {
            & $command stop --pid $pidFile *> $null
        }
        & $command agent stop u-manager *> $null
    }

    Get-Process -ErrorAction SilentlyContinue | Where-Object {
        $_.ProcessName -like "csgclaw*" -or
        $_.ProcessName -like "boxlite-shim*" -or
        $_.Path -like "*\csgclaw\*"
    } | ForEach-Object {
        try {
            Stop-Process -Id $_.Id -Force -ErrorAction Stop
            Write-Output "INFO: stopped process $($_.Id) $($_.ProcessName)"
        } catch {
            Write-Output "WARN: failed to stop process $($_.Id): $($_.Exception.Message)"
        }
    }
}

Emit-Progress 5 "preflight"

Emit-Progress 20 "stopping_services"
Stop-CSGClawProcesses

$installDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:USERPROFILE ".local\bin" }
$runtimeRoot = if ($env:LIB_DIR) { $env:LIB_DIR } else { Join-Path $env:USERPROFILE ".local\lib\csgclaw" }
$legacyRuntimeRoot = Join-Path $env:USERPROFILE ".local\share\csgclaw"
$configRoot = Join-Path $env:USERPROFILE ".csgclaw"
$launchers = @(
    (Join-Path $installDir "csgclaw.exe"),
    (Join-Path $installDir "csgclaw.cmd"),
    (Join-Path $installDir "csgclaw.ps1"),
    (Join-Path $env:USERPROFILE "bin\csgclaw.exe"),
    (Join-Path $env:USERPROFILE "bin\csgclaw.cmd"),
    (Join-Path $env:USERPROFILE "bin\csgclaw.ps1")
)

Emit-Progress 35 "removing_runtime"
foreach ($launcher in $launchers) {
    Remove-IfExists $launcher
}
Remove-IfExists $runtimeRoot
Remove-IfExists $legacyRuntimeRoot
Remove-IfExists $configRoot

Emit-Progress 80 "verifying_uninstall"
if (Get-Command csgclaw -ErrorAction SilentlyContinue) {
    $cmd = (Get-Command csgclaw).Source
    Write-Output "ERROR: CSGClaw binary is still available at $cmd"
    exit 1
}

Emit-Progress 100 "complete"
Write-Output "INFO: CSGClaw uninstallation complete"
