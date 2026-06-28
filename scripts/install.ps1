Param(
    [string]$InstallDir = "$HOME\bin"
)

$ErrorActionPreference = "Stop"

$Repo = "OpenCSGs/csglite"
$BinaryName = "csghub-lite.exe"
$LlamaCppRepo = "ggml-org/llama.cpp"
$LlamaCppDefaultTag = if ($env:CSGHUB_LITE_LLAMA_CPP_TAG) { $env:CSGHUB_LITE_LLAMA_CPP_TAG } else { "b9158" }

$GitHubApi = "https://api.github.com/repos"
$GitLabHost = "https://git-devops.opencsg.com"
$GitLabApi = "$GitLabHost/api/v4/projects"
$GitLabCsghubId = "392"
$GitLabLlamaId = "393"
$EnterpriseLicenseUrl = "$GitLabHost/opensource/public_files/-/raw/main/license.txt"

function Info([string]$msg) { Write-Host "[INFO] $msg" -ForegroundColor Green }
function Warn([string]$msg) { Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Fail([string]$msg) { Write-Host "[ERROR] $msg" -ForegroundColor Red; exit 1 }

function Detect-Region {
    $region = $env:CSGHUB_LITE_REGION
    if ($region) { return $region }
    try {
        $country = (Invoke-WebRequest -Uri "https://ipinfo.io/country" -UseBasicParsing -TimeoutSec 5).Content.Trim()
        if ($country -eq "CN") { return "CN" }
        if ($country) { return "INTL" }
    } catch {}
    return "CN"
}

function Download-File([string]$Url, [string]$OutFile) {
    Invoke-WebRequest -Uri $Url -OutFile $OutFile -UseBasicParsing
}

function Try-Download {
    param([string]$OutFile, [string[]]$Urls)
    foreach ($url in $Urls) {
        try {
            Info "Trying $url"
            Download-File -Url $url -OutFile $OutFile
            Info "Downloaded from $url"
            return $true
        } catch {
            Warn "Failed: $url"
        }
    }
    return $false
}

function Try-DownloadText {
    param([string[]]$Urls)
    foreach ($url in $Urls) {
        try {
            return (Invoke-RestMethod -Uri $url -UseBasicParsing -TimeoutSec 30)
        } catch {
            continue
        }
    }
    return $null
}

function Region-Download {
    param([string]$OutFile, [string]$GitHubUrl, [string]$GitLabUrl)
    if ($script:Region -eq "CN") {
        return Try-Download -OutFile $OutFile -Urls @($GitLabUrl, $GitHubUrl)
    } else {
        return Try-Download -OutFile $OutFile -Urls @($GitHubUrl, $GitLabUrl)
    }
}

function Region-DownloadText {
    param([string]$GitHubUrl, [string]$GitLabUrl)
    if ($script:Region -eq "CN") {
        return Try-DownloadText -Urls @($GitLabUrl, $GitHubUrl)
    } else {
        return Try-DownloadText -Urls @($GitHubUrl, $GitLabUrl)
    }
}

function Get-ReleaseAssetNames {
    param(
        [Parameter(Mandatory = $true)] $Release,
        [Parameter(Mandatory = $true)][string]$Pattern
    )

    if (-not $Release) {
        return @()
    }

    try {
        $serialized = $Release | ConvertTo-Json -Depth 10 -Compress
    } catch {
        $serialized = [string]$Release
    }

    $regex = [regex]$Pattern
    $seen = @{}
    $assets = @()
    foreach ($match in $regex.Matches($serialized)) {
        if (-not $seen.ContainsKey($match.Value)) {
            $seen[$match.Value] = $true
            $assets += $match.Value
        }
    }

    return $assets
}

function Get-LatestVersion {
    $ghUrl = "$GitHubApi/$Repo/releases/latest"
    $glUrl = "$GitLabApi/$GitLabCsghubId/releases/permalink/latest"
    $release = Region-DownloadText -GitHubUrl $ghUrl -GitLabUrl $glUrl
    if ($release -and $release.tag_name) { return $release.tag_name }
    return $null
}

function Ensure-PathContains([string]$dir) {
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if ($userPath) { $parts = $userPath.Split(';') }
    if ($parts -notcontains $dir) {
        $newPath = if ($userPath) { "$dir;$userPath" } else { $dir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        if ($env:Path -notlike "*$dir*") {
            $env:Path = "$dir;$env:Path"
        }
        Info "Added $dir to PATH."
    }
}

function Resolve-NVIDIASMI {
    $cmd = Get-Command "nvidia-smi" -ErrorAction SilentlyContinue
    if ($cmd) {
        return $cmd.Source
    }

    $candidates = @()
    if ($env:ProgramFiles) {
        $candidates += Join-Path $env:ProgramFiles "NVIDIA Corporation\NVSMI\nvidia-smi.exe"
    }
    $programFilesX86 = ${env:ProgramFiles(x86)}
    if ($programFilesX86) {
        $candidates += Join-Path $programFilesX86 "NVIDIA Corporation\NVSMI\nvidia-smi.exe"
    }

    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }

    return $null
}

function Install-EnterpriseLicense([string]$dir) {
    if ($env:EE -ne "1") { return }

    $licensePath = Join-Path $dir "license.txt"
    Info "EE=1 detected. Installing enterprise license to $licensePath"

    try {
        $licenseText = (Invoke-WebRequest -Uri $EnterpriseLicenseUrl -UseBasicParsing -TimeoutSec 30).Content
    } catch {
        Fail "Failed to download enterprise license from $EnterpriseLicenseUrl"
    }

    if (-not $licenseText) {
        Fail "Downloaded enterprise license is empty."
    }

    Set-Content -Path $licensePath -Value $licenseText -Encoding UTF8 -Force
    Info "Installed enterprise license to $licensePath"
}

function Install-CsghubLite {
    $arch = $env:PROCESSOR_ARCHITECTURE
    switch ($arch) {
        "AMD64" { $archToken = "amd64" }
        "ARM64" { $archToken = "arm64" }
        default { Fail "Unsupported architecture: $arch" }
    }

    $version = if ($env:CSGHUB_LITE_VERSION) { $env:CSGHUB_LITE_VERSION } else { Get-LatestVersion }
    if (-not $version) { Fail "Could not determine latest version. Set CSGHUB_LITE_VERSION manually." }
    Info "Version: $version"

    $versionNum = $version.TrimStart('v')
    $archiveName = "csghub-lite_${versionNum}_windows-${archToken}.zip"
    $githubUrl = "https://github.com/$Repo/releases/download/$version/$archiveName"
    $gitlabUrl = "$GitLabApi/$GitLabCsghubId/packages/generic/csghub-lite/${versionNum}/${archiveName}"

    $tmpDir = Join-Path ([IO.Path]::GetTempPath()) ("csghub-lite-install-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null
    $zipPath = Join-Path $tmpDir $archiveName

    if (-not (Region-Download -OutFile $zipPath -GitHubUrl $githubUrl -GitLabUrl $gitlabUrl)) {
        Fail "Failed to download csghub-lite."
    }

    Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force
    $bin = Get-ChildItem -Path $tmpDir -Recurse -Filter "csghub-lite.exe" | Select-Object -First 1
    if (-not $bin) { Fail "csghub-lite.exe not found in archive." }

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $target = Join-Path $InstallDir "csghub-lite.exe"
    Copy-Item -Path $bin.FullName -Destination $target -Force
    Install-EnterpriseLicense -dir $InstallDir
    Ensure-PathContains -dir $InstallDir
    Info "Installed csghub-lite to $target"
}

function Install-LlamaServer {
    $existingLlama = Get-Command "llama-server.exe" -ErrorAction SilentlyContinue
    if ($existingLlama) {
        Info "llama-server found at $($existingLlama.Source)"
    } else {
        Warn "llama-server not found. It is required for model inference."
    }

    $customCmd = $env:CSGHUB_LITE_LLAMA_CPP_INSTALL_CMD
    if ($customCmd) {
        Info "Installing llama.cpp via custom command..."
        try {
            powershell -NoProfile -ExecutionPolicy Bypass -Command $customCmd | Out-Null
            if (Get-Command "llama-server.exe" -ErrorAction SilentlyContinue) {
                Info "llama-server installed."
                return
            }
        } catch {
            Warn "Custom install command failed: $customCmd"
        }
    }

    $llamaTag = $LlamaCppDefaultTag
    $ghUrl = "$GitHubApi/$LlamaCppRepo/releases/tags/$llamaTag"
    $glUrl = "$GitLabApi/$GitLabLlamaId/releases/$llamaTag"
    $release = Region-DownloadText -GitHubUrl $ghUrl -GitLabUrl $glUrl
    if (-not $release) {
        Warn "Failed to query llama.cpp release metadata for $llamaTag. Continuing with the pinned tag."
    }

    # Compare local and remote versions to skip unnecessary downloads.
    # llama-server --version prints "version: <n> (<hash>)". Release tags: "b<n>".
    # Upstream <n> is often git rev-list --count; shallow clones get small n — do not treat as official b-id.
    if ($existingLlama) {
        $localBuild = $null
        $llamaBinDir = Split-Path $existingLlama.Source -Parent
        $savedPath = $env:Path
        try {
            # Help loader find co-located DLLs when running --version
            if ($llamaBinDir -and $env:Path -notlike "*$llamaBinDir*") {
                $env:Path = "$llamaBinDir;$env:Path"
            }
            $verLines = @(& $existingLlama.Source --version 2>&1 | ForEach-Object { $_.ToString() })
            $verFooter = $verLines | Where-Object { $_ -match '^\s*version:\s+\d+\s+\(' } | Select-Object -Last 1
            if ($verFooter -match 'version:\s+(\d+)\s+\(') {
                $localBuild = $Matches[1]
            } elseif ($verLines) {
                foreach ($line in ($verLines | Select-Object -Last 20)) {
                    if ($line -match 'version:\s+(\d+)') {
                        $localBuild = $Matches[1]
                        break
                    }
                }
            }
        } catch {
        } finally {
            $env:Path = $savedPath
        }

        $remoteBuild = $llamaTag.TrimStart('b')
        $rn = 0
        $ln = 0
        if ($localBuild -and [int]::TryParse($remoteBuild, [ref]$rn) -and [int]::TryParse($localBuild, [ref]$ln)) {
            if ($ln -le 100 -and $rn -ge 2000) {
                Info "Ignoring local llama-server build id $localBuild (not comparable to official $llamaTag; often from shallow git clone)."
                $localBuild = $null
            }
        }
        if ($localBuild -and $localBuild -eq $remoteBuild) {
            Info "llama-server is already up to date ($llamaTag)."
            return
        }
        if ($localBuild) {
            Info "Upgrading llama-server from b$localBuild to $llamaTag..."
        } else {
            Info "Upgrading llama-server to $llamaTag..."
        }
    }

    Info "llama.cpp release: $llamaTag (aligned with bundled converter)"

    $arch = $env:PROCESSOR_ARCHITECTURE
    $archToken = if ($arch -eq "AMD64") { "x64" } elseif ($arch -eq "ARM64") { "arm64" } else { $null }
    if (-not $archToken) {
        Warn "Unsupported architecture for llama-server: $arch"
        return
    }

    $nvidiaSmi = Resolve-NVIDIASMI
    $hasCuda = [bool]$nvidiaSmi

    # Build ordered list of candidate assets (best match first)
    $candidates = [System.Collections.Generic.List[object]]::new()
    $candidateAssets = @{}
    $cudartName = $null
    $escapedTag = [regex]::Escape($llamaTag)
    function Add-Candidate {
        param([string]$Asset, [string]$Cudart = $null)
        if (-not $Asset) {
            return
        }
        if ($candidateAssets.ContainsKey($Asset)) {
            return
        }
        $candidateAssets[$Asset] = $true
        [void]$candidates.Add(@{ Asset = $Asset; Cudart = $Cudart })
    }

    if ($hasCuda) {
        Info "NVIDIA GPU detected, trying CUDA build first."
        Add-Candidate -Asset "llama-${llamaTag}-bin-win-cuda-12.4-${archToken}.zip" -Cudart "cudart-llama-bin-win-cuda-12.4-${archToken}.zip"
        $cudaAssets = Get-ReleaseAssetNames -Release $release -Pattern "llama-${escapedTag}-bin-win-cuda-[0-9.]+-${archToken}\.zip"
        foreach ($asset in $cudaAssets) {
            $cudartAsset = $null
            if ($asset -match "^llama-${escapedTag}-bin-win-cuda-([0-9.]+)-${archToken}\.zip$") {
                $cudartAsset = "cudart-llama-bin-win-cuda-$($Matches[1])-${archToken}.zip"
            }
            Add-Candidate -Asset $asset -Cudart $cudartAsset
        }
        Add-Candidate -Asset "llama-${llamaTag}-bin-win-vulkan-${archToken}.zip"
    }
    Add-Candidate -Asset "llama-${llamaTag}-bin-win-cpu-${archToken}.zip"

    $tmpDir = Join-Path ([IO.Path]::GetTempPath()) ("llama-install-" + [Guid]::NewGuid().ToString("N"))
    New-Item -ItemType Directory -Force -Path $tmpDir | Out-Null

    $downloaded = $false
    $assetName = $null
    foreach ($c in $candidates) {
        $tryAsset = $c.Asset
        $githubDl = "https://github.com/$LlamaCppRepo/releases/download/$llamaTag/$tryAsset"
        $gitlabDl = "$GitLabApi/$GitLabLlamaId/packages/generic/llama-cpp/$llamaTag/$tryAsset"
        $zipPath = Join-Path $tmpDir $tryAsset
        if (Region-Download -OutFile $zipPath -GitHubUrl $githubDl -GitLabUrl $gitlabDl) {
            $assetName = $tryAsset
            $cudartName = $c.Cudart
            $downloaded = $true
            break
        }
        Warn "Asset $tryAsset not available, trying next option..."
    }
    if (-not $downloaded) {
        Warn "Failed to download llama.cpp $llamaTag."
        return
    }
    Info "Downloaded $assetName"

    if ($cudartName) {
        $cudartGh = "https://github.com/$LlamaCppRepo/releases/download/$llamaTag/$cudartName"
        $cudartGl = "$GitLabApi/$GitLabLlamaId/packages/generic/llama-cpp/$llamaTag/$cudartName"
        $cudartZip = Join-Path $tmpDir $cudartName
        if (Region-Download -OutFile $cudartZip -GitHubUrl $cudartGh -GitLabUrl $cudartGl) {
            Expand-Archive -Path $cudartZip -DestinationPath $tmpDir -Force
        } else {
            Warn "Failed to download CUDA runtime. GPU acceleration may not work."
        }
    }

    Expand-Archive -Path $zipPath -DestinationPath $tmpDir -Force
    $server = Get-ChildItem -Path $tmpDir -Recurse -Filter "llama-server.exe" | Select-Object -First 1
    if (-not $server) {
        Warn "llama-server.exe not found in archive."
        return
    }

    $llamaInstallDir = $InstallDir
    if ($existingLlama) {
        $llamaInstallDir = Split-Path $existingLlama.Source -Parent
    }

    New-Item -ItemType Directory -Force -Path $llamaInstallDir | Out-Null
    # Recursively copy all DLLs from the extract tree (archives may place deps outside the exe folder).
    Get-ChildItem -Path $tmpDir -Recurse -Filter "*.dll" -File | ForEach-Object {
        Copy-Item -Path $_.FullName -Destination (Join-Path $llamaInstallDir $_.Name) -Force
    }
    Copy-Item -Path $server.FullName -Destination (Join-Path $llamaInstallDir "llama-server.exe") -Force
    Ensure-PathContains -dir $llamaInstallDir
    Info "Installed llama-server to $llamaInstallDir"
}

function Check-Existing {
    $existing = Get-Command "csghub-lite.exe" -ErrorAction SilentlyContinue
    if (-not $existing) {
        Info "No existing installation found."
        return
    }

    $oldVersion = "unknown"
    try {
        $oldVersion = (& $existing.Source --version 2>$null) | Select-Object -First 1
        if (-not $oldVersion) { $oldVersion = "unknown" }
    } catch {}

    Write-Host ""
    Warn "Existing installation detected:"
    Write-Host "  Binary:  $($existing.Source)"
    Write-Host "  Version: $oldVersion"

    $procs = Get-Process -Name "csghub-lite" -ErrorAction SilentlyContinue
    $hasRunning = $false
    if ($procs) {
        $hasRunning = $true
        $script:WasServerRunning = $true
        Warn "Running csghub-lite process(es) detected."
    }

    if ($env:CSGHUB_LITE_FORCE -eq "1") {
        if ($hasRunning) {
            Info "Force mode: stopping running processes..."
            $procs | Stop-Process -Force -ErrorAction SilentlyContinue
            Start-Sleep -Seconds 2
        }
        return
    }

    Write-Host ""
    if ($hasRunning) {
        $prompt = "Stop running instances and replace with the new version? [y/N]"
    } else {
        $prompt = "Replace the existing installation? [y/N]"
    }

    $answer = Read-Host $prompt
    if ($answer -notmatch '^[yY](es)?$') {
        Info "Installation cancelled."
        exit 0
    }

    if ($hasRunning) {
        Info "Stopping running processes..."
        $procs | Stop-Process -Force -ErrorAction SilentlyContinue
        Start-Sleep -Seconds 2
    }
}

function Check-PythonOptional {
    # Python is optional — only needed for rare/unsupported architectures.
    # The built-in Go converter handles 160+ architectures natively.
    $python = $null
    foreach ($name in @("python3", "python")) {
        $cmd = Get-Command $name -ErrorAction SilentlyContinue
        if ($cmd) {
            try {
                $ver = & $cmd.Source -c "import sys; print(sys.version_info.major)" 2>$null
                if ($ver -eq "3") {
                    $python = $cmd.Source
                    break
                }
            } catch {}
        }
    }

    if ($python) {
        Info "Python 3 found (optional): $(& $python --version 2>&1)"
    } else {
        Info "Python 3 not found (optional - not required for most models)."
    }
}

function Test-CsghubLiteServerRunning {
    param([string]$BinaryPath)

    $nativeErrorPrefVar = Get-Variable -Name PSNativeCommandUseErrorActionPreference -Scope Global -ErrorAction SilentlyContinue
    $nativeErrorPrefOriginal = $null
    if ($nativeErrorPrefVar) {
        $nativeErrorPrefOriginal = $nativeErrorPrefVar.Value
        Set-Variable -Name PSNativeCommandUseErrorActionPreference -Scope Global -Value $false
    }

    $exitCode = 1
    try {
        try {
            & $BinaryPath ps *> $null
        } catch {
            # A stopped server should not turn the installer probe into a terminating error.
        }
        if ($null -ne $LASTEXITCODE) {
            $exitCode = $LASTEXITCODE
        }
        return ($exitCode -eq 0)
    } finally {
        if ($nativeErrorPrefVar) {
            Set-Variable -Name PSNativeCommandUseErrorActionPreference -Scope Global -Value $nativeErrorPrefOriginal
        }
    }
}

function Start-CsghubLiteServer {
    param([string]$BinaryPath)

    if (Test-CsghubLiteServerRunning -BinaryPath $BinaryPath) {
        Info "csghub-lite server is already running."
        $script:ServerStartStatus = "running"
        return
    }

    try {
        Start-Process -FilePath $BinaryPath -ArgumentList "serve" -WorkingDirectory (Split-Path $BinaryPath -Parent) -WindowStyle Hidden | Out-Null
    } catch {
        Warn "Failed to launch background server: $($_.Exception.Message)"
        return
    }

    for ($i = 0; $i -lt 20; $i++) {
        Start-Sleep -Milliseconds 500
        if (Test-CsghubLiteServerRunning -BinaryPath $BinaryPath) {
            Info "Started csghub-lite server in background."
            $script:ServerStartStatus = "started"
            return
        }
    }

    Warn "Could not verify background server startup. Try: csghub-lite serve"
    $script:ServerStartStatus = "failed"
}

# ---- Main ----
$script:Region = Detect-Region
$script:ServerStartStatus = "failed"
$script:WasServerRunning = $false
Info "Detected region: $script:Region"

Info "Checking for existing installation..."
Check-Existing

Info "Installing csghub-lite..."
Install-CsghubLite

$autoInstall = if ($env:CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER) { $env:CSGHUB_LITE_AUTO_INSTALL_LLAMA_SERVER } else { "1" }
if ($autoInstall -eq "1") {
    Install-LlamaServer
}

Check-PythonOptional
if ($script:WasServerRunning) {
    Info "Restarting csghub-lite server after upgrade..."
} else {
    Info "Starting csghub-lite server..."
}
Start-CsghubLiteServer -BinaryPath (Join-Path $InstallDir "csghub-lite.exe")

Write-Host ""
Write-Host "Quick start:" -ForegroundColor White
if ($script:ServerStartStatus -eq "started" -or $script:ServerStartStatus -eq "running") {
    Write-Host "  csghub-lite run Qwen/Qwen3-0.6B-GGUF    # Run a model"
    Write-Host "  csghub-lite ps                          # List running models"
    Write-Host "  csghub-lite stop-service                # Stop background server"
} else {
    Write-Host "  csghub-lite serve                       # Start server with Web UI"
    Write-Host "  csghub-lite run Qwen/Qwen3-0.6B-GGUF    # Run a model"
    Write-Host "  csghub-lite ps                          # List running models"
}
Write-Host "  csghub-lite login                       # Set CSGHub token"
Write-Host "  csghub-lite --help                      # Show all commands"
Write-Host ""
Write-Host "Web UI:" -ForegroundColor White
if ($script:ServerStartStatus -eq "started" -or $script:ServerStartStatus -eq "running") {
    Write-Host "  Server is already running. Open " -NoNewline
} else {
    Write-Host "  Start the server and open " -NoNewline
}
Write-Host "http://localhost:11435" -ForegroundColor Cyan -NoNewline
Write-Host " in your browser."
Write-Host "  Dashboard, Marketplace, Library and Chat are all available."
Write-Host ""
Write-Host "Want more?" -ForegroundColor White
Write-Host "  Visit https://opencsg.com for advanced features,"
Write-Host "  enterprise solutions, and the full CSGHub platform."
Write-Host ""
