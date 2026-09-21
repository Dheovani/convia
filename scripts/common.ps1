<#
.SYNOPSIS
What the development scripts in this directory both need.

.DESCRIPTION
Dot-source it:

    . (Join-Path $PSScriptRoot 'common.ps1')

There are two scripts because there are two ways to run Convia locally, and
they differ in what they start rather than in how they read .env or wait for a
port. This holds the part that is the same, so that a fix to it is one fix.

It is the sibling of common.sh and the two are kept in step.
#>

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

# Install-InterfaceDependencies installs web/'s dependencies when they are
# missing, or when the lockfile changed since they were installed.
function Install-InterfaceDependencies {
    $installed = 'web/node_modules/.package-lock.json'
    $stale = -not (Test-Path -LiteralPath $installed)
    if (-not $stale) {
        $stale = (Get-Item 'web/package-lock.json').LastWriteTime -gt (Get-Item $installed).LastWriteTime
    }
    if (-not $stale) {
        return
    }

    Write-Host 'Installing the interface dependencies...'
    Push-Location web
    try {
        Invoke-Checked 'npm ci' { npm ci }
    }
    finally {
        Pop-Location
    }
}

<#
.SYNOPSIS
Starts the containers Convia needs, according to what .env configures.

.DESCRIPTION
The media plane and the shared channel are opt-in profiles, because Convia runs
without either. Starting what is not configured would mean a container nothing
talks to.
#>
function Start-Dependencies {
    $compose = @('compose')
    if ($env:CONVIA_LIVEKIT_URL) {
        $compose += @('--profile', 'media')
    }
    if ($env:CONVIA_REDIS_URL) {
        $compose += @('--profile', 'shared')
    }

    Write-Host 'Starting the dependencies...'
    Invoke-Checked 'docker compose up' { docker @compose up --detach --wait }
}

<#
.SYNOPSIS
Waits until Convia answers, and says what happened when it does not.

.DESCRIPTION
A process that exited is reported as having exited, with its code, rather than
as a timeout: the two have different causes and the same symptom.
#>
function Wait-ForConvia {
    param(
        [System.Diagnostics.Process]$Process,
        [int]$Port,
        [int]$TimeoutSeconds = 30
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        if ($Process.HasExited) {
            throw "Convia exited with code $($Process.ExitCode) before it was ready. Its output is above."
        }
        try {
            $health = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/health" -UseBasicParsing -TimeoutSec 2
            if ($health.StatusCode -eq 200) {
                return
            }
        }
        catch {
            Start-Sleep -Milliseconds 500
        }
    }

    throw "Convia did not answer on port $Port within $TimeoutSeconds seconds."
}
