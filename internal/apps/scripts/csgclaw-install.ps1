param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"
$DefaultManifestUrl = "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/csgclaw-desktop/channels/release/downloads.json"
$ManifestUrl = if ($env:CSGHUB_LITE_CSGCLAW_DESKTOP_MANIFEST_URL) { $env:CSGHUB_LITE_CSGCLAW_DESKTOP_MANIFEST_URL } else { $DefaultManifestUrl }
$RuntimeRoot = Join-Path $env:USERPROFILE ".local\share\csgclaw-desktop"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Find-CSGClawDesktopExecutable {
    $registryPaths = @(
        "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
    )
    foreach ($entry in Get-ItemProperty -Path $registryPaths -ErrorAction SilentlyContinue) {
        if ([string]$entry.DisplayName -notmatch "CSGClaw") { continue }
        $icon = ([string]$entry.DisplayIcon).Trim('"')
        $icon = $icon -replace ',\d+$', ''
        if ($icon -and (Test-Path -LiteralPath $icon -PathType Leaf)) {
            return $icon
        }
        if ($entry.InstallLocation) {
            foreach ($name in @("CSGClaw.exe", "csgclaw-desktop.exe")) {
                $candidate = Join-Path ([string]$entry.InstallLocation) $name
                if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
            }
        }
    }

    $dirs = @(
        (Join-Path $env:LOCALAPPDATA "Programs\CSGClaw"),
        (Join-Path $env:LOCALAPPDATA "Programs\csgclaw-desktop"),
        (Join-Path $env:LOCALAPPDATA "CSGClaw"),
        (Join-Path $env:LOCALAPPDATA "csgclaw-desktop")
    )
    foreach ($dir in $dirs) {
        foreach ($name in @("CSGClaw.exe", "csgclaw-desktop.exe")) {
            $candidate = Join-Path $dir $name
            if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
        }
    }
    return $null
}

Emit-Progress 10 "detecting_platform"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "CSGClaw Desktop installation is only supported on macOS and Windows"
}
if (-not [Environment]::Is64BitOperatingSystem -or $env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    throw "CSGClaw Desktop currently supports Windows x86_64 only"
}

Emit-Progress 25 "resolving_latest"
$manifest = Invoke-RestMethod -Uri $ManifestUrl -UseBasicParsing -TimeoutSec 60
$version = if ([string]::IsNullOrWhiteSpace($Target) -or $Target -eq "latest") {
    [string]$manifest.latest
} else {
    $Target.TrimStart("v")
}
$release = $manifest.versions.$version
if (-not $release) {
    throw "CSGClaw Desktop version $version was not found in $ManifestUrl"
}
$artifact = $release.artifacts | Where-Object {
    $_.platform -eq "windows" -and $_.arch -eq "x86_64"
} | Select-Object -First 1
if (-not $artifact.url -or -not $artifact.sha256) {
    throw "no CSGClaw Desktop artifact for Windows x86_64"
}

$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ("csgclaw-desktop-install-" + [Guid]::NewGuid().ToString("N"))
$installerPath = Join-Path $workDir "csgclaw-desktop-installer.exe"
try {
    New-Item -ItemType Directory -Force -Path $workDir | Out-Null

    Emit-Progress 55 "downloading_archive"
    Write-Output "INFO: downloading CSGClaw Desktop $version for Windows x86_64"
    Invoke-WebRequest -Uri ([string]$artifact.url) -OutFile $installerPath -UseBasicParsing -TimeoutSec 3600

    Emit-Progress 75 "verifying_checksum"
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $installerPath).Hash.ToLowerInvariant()
    if ($actual -ne ([string]$artifact.sha256).ToLowerInvariant()) {
        throw "checksum verification failed"
    }

    Emit-Progress 85 "running_installer"
    $process = Start-Process -FilePath $installerPath -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "CSGClaw Desktop installer exited with code $($process.ExitCode)"
    }

    Emit-Progress 95 "verifying_install"
    $target = Find-CSGClawDesktopExecutable
    if (-not $target) {
        throw "installer completed but the CSGClaw Desktop executable was not found"
    }
    New-Item -ItemType Directory -Force -Path $RuntimeRoot | Out-Null
    Set-Content -LiteralPath (Join-Path $RuntimeRoot "version") -Value $version -Encoding ASCII
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText((Join-Path $RuntimeRoot "launch-target"), "$target`r`n", $utf8NoBom)

    Emit-Progress 100 "complete"
    Write-Output "INFO: installed CSGClaw Desktop $version to $target"
} finally {
    if (Test-Path -LiteralPath $workDir) {
        Remove-Item -LiteralPath $workDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
