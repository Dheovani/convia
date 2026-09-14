<#
.SYNOPSIS
Runs Convia and its interface together, for local testing.

.DESCRIPTION
Starts the local dependencies with Docker Compose, applies the migrations,
builds and starts the API, and serves the interface with hot reloading. Open
http://localhost:5173: the interface reaches the API through the Vite proxy, so
the browser still sees one origin and the session cookie works as it does in
production.

Configuration comes from .env at the repository root, exactly as the README
describes. The media plane (LiveKit) and the shared channel (Redis) are started
only when .env configures them.

Nobody needs an account beforehand: create one on the sign-in page.

Press Ctrl+C to stop both. The containers are left running, because they are the
slowest part to start and they hold the database. Stop them with
`docker compose down`.

.PARAMETER SkipMigrations
Starts without applying migrations first.

.EXAMPLE
./scripts/dev.ps1
#>
[CmdletBinding()]
param(
    [switch]$SkipMigrations
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# The interface's development server. web/vite.config.ts proxies /v1 from here
# to the API on 8080.
$interfacePort = 5173
$proxiedApiPort = 8080

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

# Invoke-Checked runs a native command and stops the script when it fails,
# which PowerShell 5.1 does not do on its own.
function Invoke-Checked {
    param([string]$Description, [scriptblock]$Command)

    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Description failed with exit code $LASTEXITCODE."
    }
}

# Import-DotEnv loads KEY=VALUE lines into this process, the way
# `set -a && . ./.env && set +a` does in a POSIX shell.
function Import-DotEnv {
    param([string]$Path)

    foreach ($line in Get-Content -LiteralPath $Path) {
        $trimmed = $line.Trim()
        if ($trimmed -eq '' -or $trimmed.StartsWith('#')) {
            continue
        }

        $separator = $trimmed.IndexOf('=')
        if ($separator -lt 1) {
            continue
        }

        $name = $trimmed.Substring(0, $separator).Trim()
        if ($name.StartsWith('export ')) {
            $name = $name.Substring(7).Trim()
        }

        $value = $trimmed.Substring($separator + 1).Trim()
        $quoted = $value.Length -ge 2 -and (
            ($value.StartsWith('"') -and $value.EndsWith('"')) -or
            ($value.StartsWith("'") -and $value.EndsWith("'")))
        if ($quoted) {
            $value = $value.Substring(1, $value.Length - 2)
        }

        [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
}

# Test-PortInUse reports whether something already answers on a local port.
function Test-PortInUse {
    param([int]$Port)

    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $attempt = $client.BeginConnect('127.0.0.1', $Port, $null, $null)
        return ($attempt.AsyncWaitHandle.WaitOne(300) -and $client.Connected)
    }
    finally {
        $client.Close()
    }
}

if (-not (Test-Path -LiteralPath '.env')) {
    throw '.env does not exist. Create it from the template with: Copy-Item .env.example .env'
}
Import-DotEnv '.env'

foreach ($tool in 'docker', 'go', 'node', 'npm') {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool is not installed or is not on PATH."
    }
}

$apiPort = $proxiedApiPort
if ($env:CONVIA_HTTP_PORT) {
    $apiPort = [int]$env:CONVIA_HTTP_PORT
}
if ($apiPort -ne $proxiedApiPort) {
    throw "CONVIA_HTTP_PORT is $apiPort, but web/vite.config.ts proxies the interface to $proxiedApiPort."
}

foreach ($port in $apiPort, $interfacePort) {
    if (Test-PortInUse $port) {
        throw "Port $port is already in use. Stop whatever holds it - possibly an earlier Convia - and run this again."
    }
}

$compose = @('compose')
if ($env:CONVIA_LIVEKIT_URL) {
    $compose += @('--profile', 'media')
}
if ($env:CONVIA_REDIS_URL) {
    $compose += @('--profile', 'shared')
}

Write-Host 'Starting the dependencies...'
Invoke-Checked 'docker compose up' { docker @compose up --detach --wait }

if (-not $SkipMigrations) {
    Write-Host 'Applying migrations...'
    Invoke-Checked 'Applying migrations' { go run ./cmd/convia migrate up }
}

# The interface's dependencies are installed when they are missing, or when the
# lockfile changed since they were.
$installed = 'web/node_modules/.package-lock.json'
$stale = -not (Test-Path -LiteralPath $installed)
if (-not $stale) {
    $stale = (Get-Item 'web/package-lock.json').LastWriteTime -gt (Get-Item $installed).LastWriteTime
}
if ($stale) {
    Write-Host 'Installing the interface dependencies...'
    Push-Location web
    try {
        Invoke-Checked 'npm ci' { npm ci }
    }
    finally {
        Pop-Location
    }
}

if (-not $env:CONVIA_LIVEKIT_URL) {
    Write-Warning 'No media plane is configured in .env, so calls cannot be started. See docs/media.md.'
}

$binary = Join-Path ([System.IO.Path]::GetTempPath()) 'convia-dev\convia.exe'
Write-Host 'Building Convia...'
Invoke-Checked 'go build' { go build -o $binary ./cmd/convia }

$api = Start-Process -FilePath $binary -ArgumentList 'serve' -NoNewWindow -PassThru
# Reading the handle now is what lets ExitCode be read after the process ends.
$null = $api.Handle

try {
    $deadline = (Get-Date).AddSeconds(30)
    $ready = $false
    while (-not $ready -and (Get-Date) -lt $deadline) {
        if ($api.HasExited) {
            throw "Convia exited with code $($api.ExitCode) before it was ready. Its output is above."
        }
        try {
            $health = Invoke-WebRequest -Uri "http://127.0.0.1:$apiPort/health" -UseBasicParsing -TimeoutSec 2
            $ready = $health.StatusCode -eq 200
        }
        catch {
            Start-Sleep -Milliseconds 500
        }
    }
    if (-not $ready) {
        throw "Convia did not answer on port $apiPort within 30 seconds."
    }

    Write-Host ''
    Write-Host "  Interface  http://localhost:$interfacePort"
    Write-Host "  API        http://localhost:$apiPort"
    Write-Host '  No account yet? Create one on the sign-in page.'
    Write-Host '  Ctrl+C stops both.'
    Write-Host ''

    # Vite is started through node rather than npm, because stopping a batch
    # file with Ctrl+C makes Windows ask whether to terminate it.
    Push-Location web
    try {
        & node node_modules/vite/bin/vite.js --port $interfacePort --strictPort
    }
    finally {
        Pop-Location
    }
}
finally {
    if (-not $api.HasExited) {
        Stop-Process -Id $api.Id -Force -ErrorAction SilentlyContinue
    }
}
