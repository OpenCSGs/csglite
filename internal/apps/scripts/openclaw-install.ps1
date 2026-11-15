$ErrorActionPreference = "Stop"

$requiredNodeVersion = [Version]"22.16.0"

function Emit-Progress([int]$Percent, [string]$Phase) {
    Write-Output "CSGHUB_PROGRESS|$Percent|$Phase"
}

function Get-NodeVersion {
    if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
        return $null
    }
    try {
        $raw = (node -v).Trim()
        if (-not $raw) {
            return $null
        }
        return [Version]($raw.TrimStart("v").Split("-")[0])
    } catch {
        return $null
    }
}

function Test-NodeRequirement {
    $current = Get-NodeVersion
    return $current -and $current -ge $requiredNodeVersion
}

function Refresh-Path {
    $machine = [System.Environment]::GetEnvironmentVariable("Path", "Machine")
    $user = [System.Environment]::GetEnvironmentVariable("Path", "User")
    $env:Path = "$machine;$user"
}

function Ensure-Node22 {
    Emit-Progress 15 "ensuring_node"
    if (Test-NodeRequirement) {
        Write-Output "INFO: using Node.js $(node -v)"
        return
    }

    if (Get-Command winget -ErrorAction SilentlyContinue) {
        Write-Output "INFO: installing Node.js 22 with winget"
        winget install OpenJS.NodeJS.LTS --source winget --accept-package-agreements --accept-source-agreements
        Refresh-Path
    } elseif (Get-Command choco -ErrorAction SilentlyContinue) {
        Write-Output "INFO: installing Node.js 22 with Chocolatey"
        choco install nodejs-lts -y
        Refresh-Path
    } elseif (Get-Command scoop -ErrorAction SilentlyContinue) {
        Write-Output "INFO: installing Node.js 22 with Scoop"
        scoop install nodejs-lts
        Refresh-Path
    }

    if (-not (Test-NodeRequirement)) {
        throw "OpenClaw requires Node.js >= $requiredNodeVersion. Install Node.js 22+ and retry."
    }

    Write-Output "INFO: using Node.js $(node -v)"
}

function Resolve-Registry([string]$PackageName) {
    $registries = @()
    if ($env:NPM_CONFIG_REGISTRY) {
        $registries += $env:NPM_CONFIG_REGISTRY
    }
    $registries += "https://registry.npmmirror.com"
    $registries += "https://registry.npmjs.org"

    foreach ($registry in ($registries | Select-Object -Unique)) {
        Write-Host "INFO: checking npm registry $registry"
        try {
            npm view $PackageName version --registry $registry *> $null
            return $registry
        } catch {
            continue
        }
    }
    throw "unable to reach a working npm registry for $PackageName"
}

function Prewarm-OpenClawRuntime([string]$Registry) {
    if (-not (Get-Command openclaw -ErrorAction SilentlyContinue)) {
        return
    }

    Emit-Progress 70 "prewarming_runtime"
    Write-Output "INFO: prewarming OpenClaw runtime dependencies for csghub-lite profile"
    $oldRegistry = $env:NPM_CONFIG_REGISTRY
    $oldDisableBonjour = $env:OPENCLAW_DISABLE_BONJOUR
    try {
        $env:NPM_CONFIG_REGISTRY = $Registry
        $env:OPENCLAW_DISABLE_BONJOUR = "1"
        openclaw --profile csghub-lite onboard `
            --non-interactive `
            --auth-choice custom-api-key `
            --custom-provider-id opencsg `
            --custom-compatibility openai `
            --custom-base-url http://127.0.0.1:11435/ai-gateway/v1 `
            --custom-model-id Qwen/Qwen3-0.6B `
            --custom-api-key csghub-lite `
            --accept-risk `
            --skip-channels `
            --skip-search `
            --skip-ui `
            --skip-skills `
            --skip-daemon `
            --skip-health
    } finally {
        $env:NPM_CONFIG_REGISTRY = $oldRegistry
        $env:OPENCLAW_DISABLE_BONJOUR = $oldDisableBonjour
    }
}

function Wait-OpenClawDependencyInstalls([int]$TimeoutSeconds = 600) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $running = Get-CimInstance Win32_Process -Filter "name = 'node.exe' OR name = 'npm.cmd' OR name = 'npm.exe'" -ErrorAction SilentlyContinue |
            Where-Object {
                $_.CommandLine -match "npm install" -and
                ($_.CommandLine -match "@openai/codex|@mariozechner/pi|@anthropic-ai/sdk")
            }
        if (-not $running) {
            return
        }
        Start-Sleep -Seconds 2
    }
    Write-Output "WARN: OpenClaw dependency npm install is still running after ${TimeoutSeconds}s"
}

function Test-TcpPort([int]$Port, [int]$TimeoutSeconds) {
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $client = [System.Net.Sockets.TcpClient]::new()
        try {
            $iar = $client.BeginConnect("127.0.0.1", $Port, $null, $null)
            if ($iar.AsyncWaitHandle.WaitOne(500)) {
                $client.EndConnect($iar)
                return $true
            }
        } catch {
        } finally {
            $client.Close()
        }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Prewarm-OpenClawGatewayRuntime([string]$Registry) {
    if (-not (Get-Command openclaw -ErrorAction SilentlyContinue)) {
        return
    }

    Emit-Progress 75 "prewarming_gateway"
    Write-Output "INFO: prewarming OpenClaw gateway runtime dependencies for csghub-lite profile"
    $logPrefix = Join-Path ([System.IO.Path]::GetTempPath()) "openclaw-gateway-prewarm-$([Guid]::NewGuid())"
    $gatewayOutLog = "$logPrefix.out.log"
    $gatewayErrLog = "$logPrefix.err.log"
    $oldRegistry = $env:NPM_CONFIG_REGISTRY
    $oldDisableBonjour = $env:OPENCLAW_DISABLE_BONJOUR
    try {
        $env:NPM_CONFIG_REGISTRY = $Registry
        $env:OPENCLAW_DISABLE_BONJOUR = "1"
        $process = Start-Process openclaw `
            -ArgumentList @("--profile", "csghub-lite", "gateway", "run", "--force") `
            -RedirectStandardOutput $gatewayOutLog `
            -RedirectStandardError $gatewayErrLog `
            -PassThru
        if (Test-TcpPort 18789 180) {
            Write-Output "INFO: OpenClaw gateway prewarm completed"
        } else {
            Write-Output "WARN: OpenClaw gateway prewarm did not become ready within 180s"
        }
        if (-not $process.HasExited) {
            Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue
            $process.WaitForExit()
        }
        foreach ($gatewayLog in @($gatewayOutLog, $gatewayErrLog)) {
            if (Test-Path -LiteralPath $gatewayLog) {
                Get-Content -LiteralPath $gatewayLog | ForEach-Object { Write-Output "openclaw-gateway-prewarm: $_" }
            }
        }
    } finally {
        $env:NPM_CONFIG_REGISTRY = $oldRegistry
        $env:OPENCLAW_DISABLE_BONJOUR = $oldDisableBonjour
        Remove-Item -LiteralPath $gatewayOutLog,$gatewayErrLog -Force -ErrorAction SilentlyContinue
    }
}

$packageName = "openclaw@latest"
Emit-Progress 5 "preflight"
Ensure-Node22
if (-not (Get-Command npm -ErrorAction SilentlyContinue)) {
    throw "npm is required to install OpenClaw."
}
$registry = Resolve-Registry $packageName
Write-Output "INFO: using npm registry $registry"

Emit-Progress 35 "installing"
npm install -g $packageName --registry $registry
Prewarm-OpenClawRuntime $registry
Wait-OpenClawDependencyInstalls 600
Prewarm-OpenClawGatewayRuntime $registry

Emit-Progress 80 "verifying"
if (Get-Command openclaw -ErrorAction SilentlyContinue) {
    $cmd = (Get-Command openclaw).Source
    Write-Output "INFO: installed binary: $cmd"
    try { openclaw --version } catch {}
}

Emit-Progress 100 "complete"
Write-Output "INFO: OpenClaw installation complete"
