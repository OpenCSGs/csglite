param(
    [string]$Target = "latest"
)

$ErrorActionPreference = "Stop"
$SiteUrl = if ($env:CSGHUB_LITE_ZCODE_SITE_URL) { $env:CSGHUB_LITE_ZCODE_SITE_URL } else { "https://zcode.z.ai/en/changelog" }
$DistBaseUrl = if ($env:CSGHUB_LITE_ZCODE_DIST_BASE_URL) { $env:CSGHUB_LITE_ZCODE_DIST_BASE_URL } else { "https://cdn-zcode.z.ai/zcode/electron/releases" }
$HomeDir = if ($env:USERPROFILE) { $env:USERPROFILE } else { [Environment]::GetFolderPath("UserProfile") }
$TmpRoot = if ($env:CSGHUB_LITE_TMPDIR) { $env:CSGHUB_LITE_TMPDIR } else { Join-Path $HomeDir ".csghub-lite\tmp\apps\zcode" }
$RuntimeRoot = Join-Path $HomeDir ".local\share\zcode"
$WorkDir = Join-Path $TmpRoot ("zcode-install-" + [guid]::NewGuid().ToString("N"))

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Resolve-Version([string]$Requested) {
    $normalized = if ($Requested) { $Requested.Trim().TrimStart('v') } else { "latest" }
    if ($normalized -and $normalized -ne "latest") {
        return $normalized
    }

    $html = (Invoke-WebRequest -Uri $SiteUrl -UseBasicParsing -ErrorAction Stop).Content
    $match = [regex]::Match($html, 'Release\s+v?([0-9]+\.[0-9]+\.[0-9]+)')
    if (-not $match.Success) {
        $match = [regex]::Match($html, '(?<![0-9])([0-9]+\.[0-9]+\.[0-9]+)(?![0-9])')
    }
    if (-not $match.Success) {
        throw "could not parse the latest ZCode version from $SiteUrl"
    }
    return $match.Groups[1].Value
}

function Get-ManifestMetadata([string]$ManifestPath, [string]$AssetName) {
    $active = $false
    $sha512 = $null
    $size = $null
    foreach ($line in [IO.File]::ReadLines($ManifestPath)) {
        if ($line -match '^\s*-\s+url:\s*(.+?)\s*$') {
            $url = $Matches[1].Trim().Trim('"').Trim("'")
            $active = $url -eq $AssetName
            continue
        }
        if ($active -and $line -match '^\s*sha512:\s*(.+?)\s*$') {
            $sha512 = $Matches[1].Trim().Trim('"').Trim("'")
        } elseif ($active -and $line -match '^\s*size:\s*([0-9]+)\s*$') {
            $size = [int64]$Matches[1]
        }
    }
    if ($sha512 -and $null -ne $size) {
        return @{ Sha512 = $sha512; Size = $size }
    }
    return $null
}

function Verify-Asset([string]$AssetPath, [string]$AssetName, [string]$Version) {
    $asset = Get-Item -LiteralPath $AssetPath
    if ($asset.Length -le 0) {
        throw "downloaded asset is empty"
    }

    $manifestPath = Join-Path $WorkDir "latest.yml"
    try {
        Invoke-WebRequest -Uri "$($DistBaseUrl.TrimEnd('/'))/$Version/latest.yml" -OutFile $manifestPath -UseBasicParsing -ErrorAction Stop
        $metadata = Get-ManifestMetadata -ManifestPath $manifestPath -AssetName $AssetName
    } catch {
        $metadata = $null
    }

    if ($metadata) {
        if ($asset.Length -ne $metadata.Size) {
            throw "asset size verification failed"
        }
        $stream = [IO.File]::OpenRead($AssetPath)
        try {
            $hasher = [Security.Cryptography.SHA512]::Create()
            try {
                $actual = [Convert]::ToBase64String($hasher.ComputeHash($stream))
            } finally {
                $hasher.Dispose()
            }
        } finally {
            $stream.Dispose()
        }
        if ($actual -cne $metadata.Sha512) {
            throw "asset SHA-512 verification failed"
        }
        Write-Output "INFO: verified size and SHA-512 from latest.yml"
    } else {
        Write-Output "INFO: no matching checksum metadata; validated the non-empty asset size"
    }
}

function Ensure-PathContains([string]$Dir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = if ($userPath) { @($userPath.Split(';')) } else { @() }
    if ($parts -notcontains $Dir) {
        $newPath = if ($userPath) { "$Dir;$userPath" } else { $Dir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    }
    if (@($env:Path.Split(';')) -notcontains $Dir) {
        $env:Path = "$Dir;$env:Path"
    }
}

if ([string]::IsNullOrWhiteSpace($HomeDir)) {
    throw "could not determine the user profile directory"
}

New-Item -ItemType Directory -Force -Path $TmpRoot | Out-Null
$env:TMPDIR = $TmpRoot
$env:TMP = $TmpRoot
$env:TEMP = $TmpRoot

try {
    Emit-Progress 10 "detecting_platform"
    $machineArch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    switch ($machineArch.ToUpperInvariant()) {
        "ARM64" { $arch = "arm64" }
        "AMD64" { $arch = "x64" }
        default { throw "unsupported Windows architecture $machineArch" }
    }

    New-Item -ItemType Directory -Force -Path $WorkDir | Out-Null

    Emit-Progress 25 "resolving_latest"
    $version = Resolve-Version -Requested $Target
    if ($version -notmatch '^[0-9]+\.[0-9]+\.[0-9]+$') {
        throw "invalid ZCode version: $version"
    }

    $assetName = "ZCode-$version-win-$arch.exe"
    $assetPath = Join-Path $WorkDir $assetName
    $versionDir = Join-Path (Join-Path $RuntimeRoot "versions") $version
    $launcherDir = Join-Path $HomeDir ".local\bin"
    $launcherPath = Join-Path $launcherDir "zcode.cmd"

    Emit-Progress 50 "downloading_asset"
    Write-Output "INFO: downloading $assetName"
    Invoke-WebRequest -Uri "$($DistBaseUrl.TrimEnd('/'))/$version/$assetName" -OutFile $assetPath -UseBasicParsing -ErrorAction Stop

    Emit-Progress 70 "verifying_asset"
    Verify-Asset -AssetPath $assetPath -AssetName $assetName -Version $version
    $header = New-Object byte[] 2
    $headerStream = [IO.File]::OpenRead($assetPath)
    try {
        $headerLength = $headerStream.Read($header, 0, 2)
    } finally {
        $headerStream.Dispose()
    }
    if ($headerLength -ne 2 -or $header[0] -ne 0x4d -or $header[1] -ne 0x5a) {
        throw "the downloaded installer is not a Windows executable"
    }

    Emit-Progress 85 "installing_runtime"
    if (Test-Path -LiteralPath $versionDir) {
        Remove-Item -LiteralPath $versionDir -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $versionDir | Out-Null
    New-Item -ItemType Directory -Force -Path $launcherDir | Out-Null

    & $assetPath "/S" "/currentuser" "/D=$versionDir"
    if ($LASTEXITCODE -ne 0) {
        throw "ZCode installer exited with code $LASTEXITCODE"
    }

    $installedExe = Get-ChildItem -LiteralPath $versionDir -Filter "ZCode.exe" -File -Recurse |
        Select-Object -First 1 -ExpandProperty FullName
    if (-not $installedExe -or -not (Test-Path -LiteralPath $installedExe -PathType Leaf)) {
        throw "ZCode.exe was not found under $versionDir after installation"
    }

    [IO.File]::WriteAllText((Join-Path $RuntimeRoot "version"), "$version`n", [Text.UTF8Encoding]::new($false))
    [IO.File]::WriteAllText((Join-Path $RuntimeRoot "launch-target"), "$installedExe`n", [Text.UTF8Encoding]::new($false))
    $current = Join-Path $RuntimeRoot "current"
    if (Test-Path -LiteralPath $current) {
        Remove-Item -LiteralPath $current -Recurse -Force
    }
    New-Item -ItemType Junction -Path $current -Target $versionDir | Out-Null

    @(
        "@echo off",
        "setlocal",
        "set /p `"ZCODE_TARGET=`"<`"$RuntimeRoot\launch-target`"",
        "start `"`" `"%ZCODE_TARGET%`" %*"
    ) | Set-Content -LiteralPath $launcherPath -Encoding ascii
    Ensure-PathContains -Dir $launcherDir

    Emit-Progress 100 "complete"
    Write-Output "INFO: installed ZCode $version to $versionDir"
} finally {
    if (Test-Path -LiteralPath $WorkDir) {
        Remove-Item -LiteralPath $WorkDir -Recurse -Force
    }
}
