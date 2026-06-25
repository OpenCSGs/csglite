param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"

$App = if ($env:APP) { $env:APP } else { "csgclaw" }
$Version = if ($env:VERSION) { $env:VERSION } elseif (-not [string]::IsNullOrWhiteSpace($Target)) { $Target } else { "latest" }
$InstallDir = if ($env:INSTALL_DIR) { $env:INSTALL_DIR } else { Join-Path $env:USERPROFILE ".local\bin" }
$LibDir = if ($env:LIB_DIR) { $env:LIB_DIR } else { Join-Path $env:USERPROFILE ".local\lib\csgclaw" }
$BaseUrl = if ($env:BASE_URL) { $env:BASE_URL } else { "https://csgclaw.opencsg.com/releases" }

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Trim-TrailingSlash([string]$Value) {
    if ([string]::IsNullOrWhiteSpace($Value)) {
        return $Value
    }
    return $Value.TrimEnd("/")
}

function Ensure-PathContains([string]$Dir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($userPath) { $parts = $userPath.Split(";") }
    if ($parts -notcontains $Dir) {
        $newPath = if ($userPath) { "$Dir;$userPath" } else { $Dir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Output "INFO: added $Dir to user PATH"
    }
    if ($env:Path -notlike "*$Dir*") {
        $env:Path = "$Dir;$env:Path"
    }
}

function Resolve-LatestVersion([string]$Base) {
    $latestUrl = "$Base/latest"
    $payload = Invoke-RestMethod -Uri $latestUrl -UseBasicParsing -TimeoutSec 30
    if ($payload.tag_name) { return ([string]$payload.tag_name).Trim() }
    if ($payload.version) { return ([string]$payload.version).Trim() }
    throw "failed to resolve latest CSGClaw version from $latestUrl"
}

function Resolve-WindowsArch {
    try {
        $osArch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString().ToLowerInvariant()
    } catch {
        $osArch = ""
    }
    switch ($osArch) {
        "x64" { return "amd64" }
        "" {
            if ($env:PROCESSOR_ARCHITEW6432 -eq "AMD64" -or $env:PROCESSOR_ARCHITECTURE -eq "AMD64") {
                return "amd64"
            }
            throw "unsupported Windows architecture: PROCESSOR_ARCHITECTURE=$($env:PROCESSOR_ARCHITECTURE). CSGClaw release assets currently provide windows_amd64."
        }
        default { throw "unsupported Windows architecture: $osArch. CSGClaw release assets currently provide windows_amd64." }
    }
}

function Write-LauncherCmd([string]$Path, [string]$TargetExePath) {
    $escapedTarget = $TargetExePath.Replace("%", "%%")
    @(
        "@echo off",
        "`"$escapedTarget`" %*"
    ) -join "`r`n" | Set-Content -LiteralPath $Path -Encoding ASCII
}

function Install-Launcher([string]$TargetExePath) {
    $launcherExePath = Join-Path $InstallDir "${App}.exe"
    $launcherCmdPath = Join-Path $InstallDir "${App}.cmd"
    Remove-Item -LiteralPath $launcherExePath, $launcherCmdPath -Force -ErrorAction SilentlyContinue

    try {
        New-Item -ItemType SymbolicLink -Path $launcherExePath -Target $TargetExePath | Out-Null
        return $launcherExePath
    } catch {
        Write-LauncherCmd -Path $launcherCmdPath -TargetExePath $TargetExePath
        return $launcherCmdPath
    }
}

function Install-Runtime([string]$Version, [string]$ExtractDir) {
    $bundlePath = Join-Path $ExtractDir $App
    $bundleExePath = Join-Path $bundlePath "bin\${App}.exe"

    New-Item -ItemType Directory -Force -Path $InstallDir, $LibDir | Out-Null
    if (-not (Test-Path -LiteralPath $bundleExePath)) {
        $flatExePath = Join-Path $ExtractDir "${App}.exe"
        if (-not (Test-Path -LiteralPath $flatExePath)) {
            throw "archive did not contain ${App}.exe"
        }
        $targetExePath = Join-Path $InstallDir "${App}.exe"
        Copy-Item -LiteralPath $flatExePath -Destination $targetExePath -Force
        Ensure-PathContains $InstallDir
        Write-Output "INFO: installed CSGClaw $Version to $targetExePath"
        return
    }

    $installRoot = Join-Path $LibDir $Version
    if (Test-Path -LiteralPath $installRoot) {
        Remove-Item -LiteralPath $installRoot -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $installRoot | Out-Null

    $installedBundlePath = Join-Path $installRoot $App
    Copy-Item -LiteralPath $bundlePath -Destination $installedBundlePath -Recurse
    $targetExePath = Join-Path $installedBundlePath "bin\${App}.exe"
    $launcherPath = Install-Launcher -TargetExePath $targetExePath

    Ensure-PathContains $InstallDir
    Write-Output "INFO: installed CSGClaw $Version to $targetExePath"
    Write-Output "INFO: updated launcher $launcherPath"
}

Emit-Progress 10 "detecting_platform"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "unsupported platform: this installer is for Windows only"
}
$arch = Resolve-WindowsArch
$base = Trim-TrailingSlash $BaseUrl

Emit-Progress 25 "resolving_latest"
$version = if ([string]::IsNullOrWhiteSpace($Version) -or $Version -eq "latest") { Resolve-LatestVersion $base } else { $Version.Trim() }
if ([string]::IsNullOrWhiteSpace($version)) {
    throw "failed to resolve CSGClaw version"
}

$archiveName = "${App}_${version}_windows_${arch}.zip"
$downloadUrl = "$base/$version/$archiveName"
$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ("csgclaw-install-" + [Guid]::NewGuid().ToString("N"))
$archivePath = Join-Path $workDir $archiveName
$extractDir = Join-Path $workDir "extract"

try {
    New-Item -ItemType Directory -Force -Path $workDir, $extractDir | Out-Null

    Emit-Progress 55 "downloading_archive"
    Write-Output "INFO: downloading $downloadUrl"
    Invoke-WebRequest -Uri $downloadUrl -OutFile $archivePath -UseBasicParsing -TimeoutSec 1800

    Emit-Progress 75 "extracting_archive"
    Expand-Archive -Path $archivePath -DestinationPath $extractDir -Force

    Emit-Progress 90 "installing_runtime"
    Install-Runtime -Version $version -ExtractDir $extractDir

    Emit-Progress 100 "complete"
    if (Get-Command csgclaw -ErrorAction SilentlyContinue) {
        try { csgclaw --version } catch {}
    }
    Write-Output "INFO: CSGClaw installation complete"
} finally {
    if (Test-Path -LiteralPath $workDir) {
        Remove-Item -LiteralPath $workDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
