param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"
$DefaultManifestUrl = "https://opencsg-public-resource.oss-cn-beijing.aliyuncs.com/csgstudio/channels/release/downloads.json"
$ManifestUrl = if ($env:CSGHUB_LITE_CSGSTUDIO_MANIFEST_URL) { $env:CSGHUB_LITE_CSGSTUDIO_MANIFEST_URL } else { $DefaultManifestUrl }
$RuntimeRoot = Join-Path $env:USERPROFILE ".local\share\csgstudio"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Find-CSGStudioExecutable {
    $registryPaths = @(
        "HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*",
        "HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*"
    )
    foreach ($entry in Get-ItemProperty -Path $registryPaths -ErrorAction SilentlyContinue) {
        if ([string]$entry.DisplayName -notmatch "CSGStudio") { continue }
        $icon = ([string]$entry.DisplayIcon).Trim('"')
        $icon = $icon -replace ',\d+$', ''
        if ($icon -and (Test-Path -LiteralPath $icon -PathType Leaf)) {
            return $icon
        }
        if ($entry.InstallLocation) {
            $candidate = Join-Path ([string]$entry.InstallLocation) "CSGStudio.exe"
            if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
        }
    }

    $dirs = @(
        (Join-Path $env:LOCALAPPDATA "Programs\CSGStudio"),
        (Join-Path $env:LOCALAPPDATA "CSGStudio")
    )
    foreach ($dir in $dirs) {
        $candidate = Join-Path $dir "CSGStudio.exe"
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

Emit-Progress 10 "detecting_platform"
if ([System.Environment]::OSVersion.Platform -ne [System.PlatformID]::Win32NT) {
    throw "this installer only runs on Windows"
}
$archNames = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") {
    @("arm64", "aarch64")
} elseif ([Environment]::Is64BitOperatingSystem) {
    @("x86_64", "amd64", "x64")
} else {
    throw "CSGStudio requires a 64-bit Windows"
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
    throw "CSGStudio version $version was not found in $ManifestUrl"
}
$artifact = $null
foreach ($arch in $archNames) {
    $artifact = $release.artifacts | Where-Object {
        $_.platform -eq "windows" -and $_.arch -eq $arch
    } | Select-Object -First 1
    if ($artifact) { break }
}
if (-not $artifact -or -not $artifact.url) {
    throw "CSGStudio $version does not publish a Windows $($archNames[0]) package"
}
$url = [string]$artifact.url
$extension = [System.IO.Path]::GetExtension(([Uri]$url).AbsolutePath).ToLowerInvariant()
if ($extension -ne ".exe" -and $extension -ne ".msi") {
    throw "unsupported CSGStudio Windows package: $url"
}

$workDir = Join-Path ([System.IO.Path]::GetTempPath()) ("csgstudio-install-" + [Guid]::NewGuid().ToString("N"))
$installerPath = Join-Path $workDir ("csgstudio-installer" + $extension)
try {
    New-Item -ItemType Directory -Force -Path $workDir | Out-Null

    Emit-Progress 55 "downloading_archive"
    Write-Output "INFO: downloading CSGStudio $version for Windows $($archNames[0])"
    Invoke-WebRequest -Uri $url -OutFile $installerPath -UseBasicParsing -TimeoutSec 3600

    Emit-Progress 75 "verifying_checksum"
    if ($artifact.sha256) {
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $installerPath).Hash.ToLowerInvariant()
        if ($actual -ne ([string]$artifact.sha256).ToLowerInvariant()) {
            throw "checksum verification failed"
        }
    } else {
        Write-Output "WARN: downloads.json has no sha256 for this package; skipping checksum verification"
    }

    Emit-Progress 85 "running_installer"
    $process = if ($extension -eq ".msi") {
        Start-Process -FilePath "msiexec.exe" -ArgumentList "/i", "`"$installerPath`"" -Wait -PassThru
    } else {
        Start-Process -FilePath $installerPath -Wait -PassThru
    }
    if ($process.ExitCode -ne 0) {
        throw "CSGStudio installer exited with code $($process.ExitCode)"
    }

    Emit-Progress 95 "verifying_install"
    $target = Find-CSGStudioExecutable
    if (-not $target) {
        throw "installer completed but the CSGStudio executable was not found"
    }
    New-Item -ItemType Directory -Force -Path $RuntimeRoot | Out-Null
    Set-Content -LiteralPath (Join-Path $RuntimeRoot "version") -Value $version -Encoding ASCII
    $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
    [System.IO.File]::WriteAllText((Join-Path $RuntimeRoot "launch-target"), "$target`r`n", $utf8NoBom)

    Emit-Progress 100 "complete"
    Write-Output "INFO: installed CSGStudio $version to $target"
} finally {
    if (Test-Path -LiteralPath $workDir) {
        Remove-Item -LiteralPath $workDir -Recurse -Force -ErrorAction SilentlyContinue
    }
}
