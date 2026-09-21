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

. (Join-Path $PSScriptRoot 'common.ps1')

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

Start-Dependencies

if (-not $SkipMigrations) {
    Write-Host 'Applying migrations...'
    Invoke-Checked 'Applying migrations' { go run ./cmd/convia migrate up }
}

Install-InterfaceDependencies

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
    Wait-ForConvia -Process $api -Port $apiPort

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
