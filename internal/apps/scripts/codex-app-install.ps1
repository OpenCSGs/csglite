param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"
$DefaultDistBaseUrl = "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/codex-app-releases"
$DistBaseUrl = if ($env:CSGHUB_LITE_CODEX_APP_DIST_BASE_URL) { $env:CSGHUB_LITE_CODEX_APP_DIST_BASE_URL } else { $DefaultDistBaseUrl }

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Trim-TrailingSlash([string]$Value) {
    if ([string]::IsNullOrWhiteSpace($Value)) {
        return $Value
    }
    return $Value.TrimEnd('/')
}

function Normalize-RequestedVersion([string]$Value) {
    if ([string]::IsNullOrWhiteSpace($Value) -or $Value -eq "latest") {
        return "latest"
    }
    return $Value.TrimStart('v')
}

function Ensure-PathContains([string]$Dir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($userPath) { $parts = $userPath.Split(';') }
    if ($parts -notcontains $Dir) {
        $newPath = if ($userPath) { "$Dir;$userPath" } else { $Dir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    }
    if ($env:Path -notlike "*$Dir*") {
        $env:Path = "$Dir;$env:Path"
    }
}

function Install-DesktopRuntime([string]$Version, [string]$BinaryPath) {
    $launcherDir = Join-Path $env:USERPROFILE ".local\bin"
    $runtimeRoot = Join-Path $env:USERPROFILE ".local\share\codex-app"
    $versionsDir = Join-Path $runtimeRoot "versions"
    $versionDir = Join-Path $versionsDir $Version
    $installedBinary = Join-Path $versionDir (Split-Path -Leaf $BinaryPath)
    $launcherPath = Join-Path $launcherDir "codex-app.cmd"

    New-Item -ItemType Directory -Force -Path $launcherDir | Out-Null
    New-Item -ItemType Directory -Force -Path $versionDir | Out-Null

    if (Test-Path $installedBinary) {
        Remove-Item -Path $installedBinary -Force
    }
    Copy-Item -Path $BinaryPath -Destination $installedBinary -Force

    Set-Content -Path (Join-Path $runtimeRoot "version") -Value $Version -Encoding ascii
    Set-Content -Path (Join-Path $runtimeRoot "launch-target") -Value $installedBinary -Encoding ascii

    $currentLink = Join-Path $runtimeRoot "current"
    if (Test-Path $currentLink) {
        Remove-Item -Path $currentLink -Recurse -Force
    }
    New-Item -ItemType Junction -Path $currentLink -Target $versionDir | Out-Null

    @(
        "@echo off",
        "start `"`" `"$installedBinary`""
    ) | Set-Content -Path $launcherPath -Encoding ascii

    Ensure-PathContains -Dir $launcherDir
    Write-Output "INFO: installed Codex App $Version to $installedBinary"
    Write-Output "INFO: updated launcher $launcherPath"
}

if (-not [Environment]::Is64BitProcess) {
    throw "Codex App does not support 32-bit Windows."
}

$distBaseUrl = Trim-TrailingSlash $DistBaseUrl
$requestedVersion = Normalize-RequestedVersion $Target
$workDir = Join-Path $env:TEMP ("codex-app-install-" + [guid]::NewGuid().ToString("N"))
$downloadDir = Join-Path $workDir "downloads"

try {
    Emit-Progress 10 "detecting_platform"
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
        $platform = "win32-arm64"
    } else {
        $platform = "win32-x64"
    }

    New-Item -ItemType Directory -Force -Path $downloadDir | Out-Null

    Emit-Progress 25 "resolving_latest"
    if ($requestedVersion -eq "latest") {
        $version = (Invoke-WebRequest -Uri "$distBaseUrl/latest" -UseBasicParsing).Content.Trim()
    } else {
        $version = $requestedVersion
    }
    if ([string]::IsNullOrWhiteSpace($version)) {
        throw "failed to resolve Codex App version"
    }

    $manifest = Invoke-RestMethod -Uri "$distBaseUrl/$version/manifest.json"
    $platformMeta = $manifest.platforms.$platform
    if (-not $platformMeta) {
        throw "platform $platform not found in manifest"
    }

    $assetName = [string]$platformMeta.asset
    $checksum = [string]$platformMeta.checksum
    if (-not $assetName -or -not $checksum) {
        throw "manifest entry for $platform is incomplete"
    }

    $archivePath = Join-Path $downloadDir $assetName

    Emit-Progress 55 "downloading_archive"
    Write-Output "INFO: downloading Codex App $version for $platform from $distBaseUrl"
    Invoke-WebRequest -Uri "$distBaseUrl/$version/$platform/$assetName" -OutFile $archivePath -ErrorAction Stop

    Emit-Progress 75 "verifying_checksum"
    $actualChecksum = (Get-FileHash -Algorithm SHA256 -Path $archivePath).Hash.ToLowerInvariant()
    if ($actualChecksum -ne $checksum.ToLowerInvariant()) {
        throw "checksum verification failed"
    }

    Emit-Progress 90 "installing_runtime"
    Install-DesktopRuntime -Version $version -BinaryPath $archivePath

    Emit-Progress 100 "complete"
    Write-Output "INFO: Codex App installation complete"
} finally {
    if (Test-Path $workDir) {
        Remove-Item -Path $workDir -Recurse -Force
    }
}
