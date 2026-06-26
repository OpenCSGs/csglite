param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$DefaultDistBaseUrl = "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/open-code-review-releases"
$DistBaseUrl = if ($env:CSGHUB_LITE_OPEN_CODE_REVIEW_DIST_BASE_URL) { $env:CSGHUB_LITE_OPEN_CODE_REVIEW_DIST_BASE_URL } else { $DefaultDistBaseUrl }

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
    $currentParts = @()
    if ($env:Path) { $currentParts = $env:Path.Split(';') }
    if ($currentParts -notcontains $Dir) {
        $env:Path = "$Dir;$env:Path"
    }
}

function Install-BinaryRuntime([string]$Version, [string]$BinaryName, [string]$SourcePath) {
    $launcherDir = Join-Path $env:USERPROFILE ".local\bin"
    $runtimeRoot = Join-Path $env:USERPROFILE ".local\share\open-code-review\versions"
    $versionDir = Join-Path $runtimeRoot $Version
    $binaryPath = Join-Path $versionDir $BinaryName
    $launcherPath = Join-Path $launcherDir "ocr.exe"

    New-Item -ItemType Directory -Force -Path $launcherDir | Out-Null
    New-Item -ItemType Directory -Force -Path $versionDir | Out-Null
    Copy-Item -Path $SourcePath -Destination $binaryPath -Force

    if (Test-Path $launcherPath) {
        Remove-Item -Path $launcherPath -Force
    }

    $linked = $false
    try {
        New-Item -ItemType HardLink -Path $launcherPath -Target $binaryPath | Out-Null
        $linked = $true
    } catch {
        Copy-Item -Path $binaryPath -Destination $launcherPath -Force
    }

    Ensure-PathContains -Dir $launcherDir
    Write-Output "INFO: installed Open Code Review $Version to $versionDir"
    if ($linked) {
        Write-Output "INFO: updated launcher $launcherPath via hard link"
    } else {
        Write-Output "INFO: updated launcher $launcherPath via file copy"
    }
}

if (-not [Environment]::Is64BitProcess) {
    throw "Open Code Review does not support 32-bit Windows."
}

$distBaseUrl = Trim-TrailingSlash $DistBaseUrl
$requestedVersion = Normalize-RequestedVersion $Target
$workDir = Join-Path $env:TEMP ("open-code-review-install-" + [guid]::NewGuid().ToString("N"))
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
    $version = if ($requestedVersion -eq "latest") {
        (Invoke-RestMethod -Uri "$distBaseUrl/latest" -ErrorAction Stop).ToString().Trim()
    } else {
        $requestedVersion
    }
    if ([string]::IsNullOrWhiteSpace($version)) {
        throw "failed to resolve Open Code Review version"
    }

    $manifest = Invoke-RestMethod -Uri "$distBaseUrl/$version/manifest.json" -ErrorAction Stop
    $platformMeta = $manifest.platforms.$platform
    if (-not $platformMeta) {
        throw "platform $platform not found in manifest"
    }

    $checksum = $platformMeta.checksum
    $binaryName = $platformMeta.binary
    $assetName = $platformMeta.asset
    $archiveFormat = $platformMeta.archive_format
    if (-not $checksum -or -not $binaryName -or -not $assetName -or -not $archiveFormat) {
        throw "manifest is missing fields for platform $platform"
    }
    if ($archiveFormat -ne "binary") {
        throw "unsupported archive format $archiveFormat for $platform"
    }

    $binaryPath = Join-Path $downloadDir $assetName
    Emit-Progress 55 "downloading_binary"
    Write-Output "INFO: downloading Open Code Review $version for $platform from $distBaseUrl"
    Invoke-WebRequest -Uri "$distBaseUrl/$version/$platform/$assetName" -OutFile $binaryPath -ErrorAction Stop

    Emit-Progress 75 "verifying_checksum"
    $actualChecksum = (Get-FileHash -Path $binaryPath -Algorithm SHA256).Hash.ToLower()
    if ($actualChecksum -ne $checksum) {
        throw "checksum verification failed"
    }

    Emit-Progress 90 "installing_runtime"
    Install-BinaryRuntime -Version $version -BinaryName $binaryName -SourcePath $binaryPath

    Emit-Progress 100 "complete"
    if (Get-Command ocr -ErrorAction SilentlyContinue) {
        try { ocr version } catch {}
    }
    Write-Output "INFO: Open Code Review installation complete"
} finally {
    if (Test-Path $workDir) {
        Remove-Item -Path $workDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
