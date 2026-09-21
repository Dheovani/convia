<#
.SYNOPSIS
Runs Convia and its desktop application together, for local testing.

.DESCRIPTION
Starts the local dependencies with Docker Compose, applies the migrations,
builds the interface into the application, starts the API, and opens the
application's window.

This is the product as somebody installs it: there is no development server and
no proxy. The application carries the interface inside its own binary and talks
to Convia over the public API, which is the arrangement everything about the
session depends on.

Use ./scripts/dev.ps1 instead for working on the interface itself -- it serves
web/ with hot reloading, and a change is on the screen when it is saved. Here a
change needs the interface rebuilt and the window reopened, which is what
running this again does.

Configuration comes from .env at the repository root, exactly as the README
describes. The media plane (LiveKit) and the shared channel (Redis) are started
only when .env configures them.

The application is for Windows, so this script is too.

Closing the window stops Convia. The containers are left running, because they
are the slowest part to start and they hold the database. Stop them with
`docker compose down`.

.PARAMETER SkipMigrations
Starts without applying migrations first.

.PARAMETER SkipInterface
Reuses the interface built last time instead of building it again. It is the
slowest step here, and nothing about it changes when only Go changed.

.EXAMPLE
./scripts/app.ps1

.EXAMPLE
./scripts/app.ps1 -SkipInterface
#>
[CmdletBinding()]
param(
    [switch]$SkipMigrations,
    [switch]$SkipInterface
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

# Where Convia answers when .env says nothing. The application asks which
# installation to connect to and remembers the answer, so this is only what the
# script prints and waits for.
$defaultApiPort = 8080

# Where Vite writes and where the binaries embed from. See web/vite.config.ts.
$bundle = 'internal/web/assets/dist/index.html'

$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

. (Join-Path $PSScriptRoot 'common.ps1')

if ($env:OS -ne 'Windows_NT') {
    throw "Convia's application is for Windows. Use ./scripts/dev.sh to run the service and the interface in a browser."
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

$apiPort = $defaultApiPort
if ($env:CONVIA_HTTP_PORT) {
    $apiPort = [int]$env:CONVIA_HTTP_PORT
}

if (Test-PortInUse $apiPort) {
    throw "Port $apiPort is already in use. Stop whatever holds it - possibly an earlier Convia - and run this again."
}

Start-Dependencies

if (-not $SkipMigrations) {
    Write-Host 'Applying migrations...'
    Invoke-Checked 'Applying migrations' { go run ./cmd/convia migrate up }
}

Install-InterfaceDependencies

# The application refuses to open a window with no interface in it, so a build
# that was skipped when there is nothing to skip has to be caught here, where
# the remedy can be named.
if ($SkipInterface -and -not (Test-Path -LiteralPath $bundle)) {
    throw 'There is no interface to reuse. Run this again without -SkipInterface.'
}
if (-not $SkipInterface) {
    Write-Host 'Building the interface...'
    Push-Location web
    try {
        Invoke-Checked 'npm run build' { npm run build }
    }
    finally {
        Pop-Location
    }
}

if (-not $env:CONVIA_LIVEKIT_URL) {
    Write-Warning 'No media plane is configured in .env, so calls cannot be started. See docs/media.md.'
}

$output = Join-Path ([System.IO.Path]::GetTempPath()) 'convia-dev'
$service = Join-Path $output 'convia.exe'
$application = Join-Path $output 'convia-desktop.exe'

<#
The application needs Wails' build tags. Without `production` it compiles a stub
that opens no window and says the tags are missing; `desktop` selects nothing
today and is what Wails' own CLI passes. There is no `-H windowsgui` here on
purpose: in development the console is where the log goes.
#>
Write-Host 'Building Convia and its application...'
Invoke-Checked 'go build' { go build -o $service ./cmd/convia }
Invoke-Checked 'go build' { go build -tags desktop,production -o $application ./cmd/convia-desktop }

$api = Start-Process -FilePath $service -ArgumentList 'serve' -NoNewWindow -PassThru
# Reading the handle now is what lets ExitCode be read after the process ends.
$null = $api.Handle

try {
    Wait-ForConvia -Process $api -Port $apiPort

    Write-Host ''
    Write-Host "  Convia       http://localhost:$apiPort"
    Write-Host '  Application  the window that is opening'
    Write-Host '  No account yet? Create one from the application.'
    Write-Host '  Closing the window stops Convia.'
    Write-Host ''

    # The window runs in the foreground: this script exists to be stopped by
    # closing it, the way somebody quits an application.
    Invoke-Checked 'The application' { & $application }
}
finally {
    if (-not $api.HasExited) {
        Stop-Process -Id $api.Id -Force -ErrorAction SilentlyContinue
    }
}
