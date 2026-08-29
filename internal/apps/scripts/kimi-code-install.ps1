$ErrorActionPreference = "Stop"

$DownloadBase = $env:CSGHUB_LITE_KIMI_CODE_DOWNLOAD_BASE
if ([string]::IsNullOrWhiteSpace($DownloadBase)) {
    $DownloadBase = "https://code.kimi.com/kimi-code"
}
$InstallDir = $env:KIMI_INSTALL_DIR
if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = Join-Path $env:USERPROFILE ".kimi-code"
}
$LauncherDir = Join-Path $env:USERPROFILE ".local\bin"
$LauncherPath = Join-Path $LauncherDir "kimi.cmd"
$BinaryBase = "$DownloadBase/binaries"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
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

$workDir = Join-Path $env:TEMP ("kimi-code-install-" + [guid]::NewGuid().ToString("N"))

try {
    Emit-Progress 10 "detecting_platform"
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
        $platform = "win32-arm64"
    } else {
        $platform = "win32-x64"
    }

    New-Item -ItemType Directory -Force -Path $workDir | Out-Null

    Emit-Progress 25 "resolving_latest"
    $version = (Invoke-RestMethod -Uri "$DownloadBase/latest" -ErrorAction Stop).ToString().Trim()
    if ([string]::IsNullOrWhiteSpace($version)) {
        throw "failed to resolve Kimi Code version"
    }
    Write-Output "INFO: latest version $version"

    $manifest = Invoke-RestMethod -Uri "$BinaryBase/$version/manifest.json" -ErrorAction Stop
    $platformMeta = $manifest.platforms.$platform
    if (-not $platformMeta) {
        throw "platform $platform not found in manifest"
    }

    $filename = $platformMeta.filename
    $checksum = $platformMeta.checksum
    if (-not $filename -or -not $checksum) {
        throw "manifest is missing fields for platform $platform"
    }

    $archivePath = Join-Path $workDir $filename
    Emit-Progress 40 "downloading_binary"
    Write-Output "INFO: downloading $BinaryBase/$version/$filename"
    Invoke-WebRequest -Uri "$BinaryBase/$version/$filename" -OutFile $archivePath -ErrorAction Stop

    Emit-Progress 75 "verifying_kimi_code"
    $actualChecksum = (Get-FileHash -Path $archivePath -Algorithm SHA256).Hash.ToLower()
    if ($actualChecksum -ne $checksum) {
        throw "checksum verification failed"
    }

    Emit-Progress 85 "installing_kimi_code"
    $binDir = Join-Path $InstallDir "bin"
    New-Item -ItemType Directory -Force -Path $binDir, $LauncherDir | Out-Null
    $binaryPath = Join-Path $binDir "kimi.exe"
    Copy-Item -Path $archivePath -Destination $binaryPath -Force

    Set-Content -Path $LauncherPath -Encoding ASCII -Value "@echo off`r`ncall `"$binaryPath`" %*`r`n"
    Ensure-PathContains -Dir $LauncherDir

    Write-Output "INFO: installed Kimi Code $version to $binaryPath"
    Write-Output "INFO: updated launcher $LauncherPath"

    Emit-Progress 100 "complete"
    try { & $LauncherPath --version } catch {}
    Write-Output "INFO: Kimi Code installed successfully."
} finally {
    if (Test-Path $workDir) {
        Remove-Item -Path $workDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
