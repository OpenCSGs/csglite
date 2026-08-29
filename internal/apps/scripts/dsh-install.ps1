$ErrorActionPreference = "Stop"

function Emit-ProgressLine {
    param(
        [int]$Progress,
        [string]$Phase
    )
    Write-Output "CSGHUB_PROGRESS|$Progress|$Phase"
}

Emit-ProgressLine 5 "checking_node"
if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
    Write-Output "ERROR: npm (Node.js) is required to install DeepSeek Harness."
    Write-Output "Install Node.js first: https://nodejs.org/"
    exit 1
}

$registry = $env:NPM_CONFIG_REGISTRY
if ([string]::IsNullOrWhiteSpace($registry)) {
    $registry = "https://registry.npmmirror.com"
}
$package = $env:CSGHUB_LITE_DSH_PACKAGE
if ([string]::IsNullOrWhiteSpace($package)) {
    $package = "@deepseek-ai/dsh@latest"
}
$installRoot = $env:CSGHUB_LITE_DSH_INSTALL_ROOT
if ([string]::IsNullOrWhiteSpace($installRoot)) {
    $installRoot = Join-Path $env:USERPROFILE ".local\share\deepseek-harness"
}
$launcherDir = Join-Path $env:USERPROFILE ".local\bin"
$launcherPath = Join-Path $launcherDir "dsh.cmd"
$actualBin = Join-Path $installRoot "dsh.cmd"

Emit-ProgressLine 30 "installing_dsh"
Write-Output "INFO: installing DeepSeek Harness package $package to $installRoot"
Remove-Item -Recurse -Force $installRoot -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path $installRoot, $launcherDir | Out-Null
npm install -g --prefix="$installRoot" --registry="$registry" "$package"
if ($LASTEXITCODE -ne 0) {
    exit $LASTEXITCODE
}
if (-not (Test-Path $actualBin)) {
    Write-Output "ERROR: DeepSeek Harness was installed but npm did not create $actualBin."
    exit 1
}

Set-Content -Path $launcherPath -Encoding ASCII -Value "@echo off`r`ncall `"$actualBin`" %*`r`n"
$env:PATH = "$launcherDir;$env:PATH"

Emit-ProgressLine 85 "verifying_dsh"
if (-not (Get-Command dsh -ErrorAction SilentlyContinue) -and -not (Test-Path $launcherPath)) {
    Write-Output "ERROR: DeepSeek Harness was installed but the dsh command was not found on PATH."
    Write-Output "INFO: launcher was written to $launcherPath; add $launcherDir to PATH and retry."
    exit 1
}

& $launcherPath --version
Emit-ProgressLine 100 "complete"
Write-Output "INFO: DeepSeek Harness installed successfully."
Write-Output "INFO: updated launcher $launcherPath"
