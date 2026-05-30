param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"
$DefaultDistBaseUrl = "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/antigravity-releases"
$DistBaseUrl = if ($env:CSGHUB_LITE_ANTIGRAVITY_DIST_BASE_URL) { $env:CSGHUB_LITE_ANTIGRAVITY_DIST_BASE_URL } else { $DefaultDistBaseUrl }

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

if (-not [Environment]::Is64BitProcess) {
    throw "Antigravity does not support 32-bit Windows."
}

$distBaseUrl = Trim-TrailingSlash $DistBaseUrl
$requestedVersion = Normalize-RequestedVersion $Target
$workDir = Join-Path $env:TEMP ("antigravity-install-" + [guid]::NewGuid().ToString("N"))
$downloadDir = Join-Path $workDir "downloads"

try {
    Emit-Progress 10 "detecting_platform"
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
        $platform = "win32-arm64"
        $upstreamPlatform = "windows_arm64"
    } else {
        $platform = "win32-x64"
        $upstreamPlatform = "windows_amd64"
    }

    New-Item -ItemType Directory -Force -Path $downloadDir | Out-Null

    Emit-Progress 25 "resolving_latest"
    $version = $null
    $checksum = $null
    $assetName = $null
    $archiveFormat = $null
    $downloadUrl = $null
    $downloadSource = $null

    try {
        $version = if ($requestedVersion -eq "latest") {
            (Invoke-RestMethod -Uri "$distBaseUrl/latest" -ErrorAction Stop).ToString().Trim()
        } else {
            $requestedVersion
        }
        if (-not [string]::IsNullOrWhiteSpace($version)) {
            $manifest = Invoke-RestMethod -Uri "$distBaseUrl/$version/manifest.json" -ErrorAction Stop
            $platformMeta = $manifest.platforms.$platform
            if ($platformMeta) {
                $checksum = $platformMeta.checksum_sha512
                $assetName = $platformMeta.asset
                $archiveFormat = $platformMeta.archive_format
                if ($checksum -and $assetName -and $archiveFormat) {
                    $downloadUrl = "$distBaseUrl/$version/$platform/$assetName"
                    $downloadSource = $distBaseUrl
                }
            }
        }
    } catch {
        $downloadUrl = $null
    }

    if (-not $downloadUrl) {
        if ($requestedVersion -ne "latest") {
            throw "mirrored Antigravity version $requestedVersion is unavailable"
        }
        Write-Output "INFO: Antigravity mirror is not ready; falling back to the official Google CLI release manifest"
        $manifest = Invoke-RestMethod -Uri "https://antigravity-cli-auto-updater-974169037036.us-central1.run.app/manifests/$upstreamPlatform.json" -ErrorAction Stop
        $version = $manifest.version
        $downloadUrl = $manifest.url
        $checksum = $manifest.sha512
        $assetName = Split-Path ([uri]$downloadUrl).AbsolutePath -Leaf
        $archiveFormat = "raw"
        $downloadSource = "official"
    }

    if ([string]::IsNullOrWhiteSpace($version) -or -not $downloadUrl -or -not $checksum -or -not $assetName -or -not $archiveFormat) {
        throw "failed to resolve Antigravity version"
    }

    $archivePath = Join-Path $downloadDir $assetName
    Emit-Progress 55 "downloading_archive"
    Write-Output "INFO: downloading Antigravity $version for $platform from $downloadSource"
    Invoke-WebRequest -Uri $downloadUrl -OutFile $archivePath -ErrorAction Stop

    Emit-Progress 75 "verifying_checksum"
    $actualChecksum = (Get-FileHash -Path $archivePath -Algorithm SHA512).Hash.ToLower()
    if ($actualChecksum -ne $checksum) {
        throw "checksum verification failed"
    }

    Emit-Progress 90 "installing_runtime"
    $launcherDir = Join-Path $env:USERPROFILE ".local\bin"
    $launcherPath = Join-Path $launcherDir "agy.exe"
    New-Item -ItemType Directory -Force -Path $launcherDir | Out-Null
    Copy-Item -Path $archivePath -Destination $launcherPath -Force
    Unblock-File -Path $launcherPath -ErrorAction SilentlyContinue
    Ensure-PathContains -Dir $launcherDir

    try { & $launcherPath install } catch {}

    Emit-Progress 100 "complete"
    try { & $launcherPath --version } catch {}
    Write-Output "INFO: Antigravity installation complete"
} finally {
    if (Test-Path $workDir) {
        Remove-Item -Path $workDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
