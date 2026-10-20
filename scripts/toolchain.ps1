$ErrorActionPreference = 'Stop'
$TaskProjectRoot = Split-Path -Parent $PSScriptRoot

function Get-TaskGo {
    $taskLocalGo = Join-Path $TaskProjectRoot '.tools\go\bin\go.exe'
    if (Test-Path -LiteralPath $taskLocalGo) { return $taskLocalGo }
    $taskInstalledGo = Get-Command go -ErrorAction SilentlyContinue
    if ($taskInstalledGo) { return $taskInstalledGo.Source }
    throw 'Go 1.26+ is required: https://go.dev/dl/'
}

function Get-TaskNode {
    $taskTools = Join-Path $TaskProjectRoot '.tools'
    if (Test-Path -LiteralPath $taskTools) {
        $taskPortable = Get-ChildItem -LiteralPath $taskTools -Directory -Filter 'node-*-win-x64' | Sort-Object Name -Descending | Select-Object -First 1
        if ($taskPortable) { return (Join-Path $taskPortable.FullName 'node.exe') }
    }
    $taskInstalledNode = Get-Command node -ErrorAction SilentlyContinue
    if ($taskInstalledNode) { return $taskInstalledNode.Source }
    throw 'Node.js 22+ is required: https://nodejs.org/'
}

function Initialize-TaskEnvironment {
    $env:GOPATH = Join-Path $TaskProjectRoot '.cache\gopath'
    $env:GOCACHE = Join-Path $TaskProjectRoot '.cache\gobuild'
    $env:WRANGLER_SEND_METRICS = 'false'
    $env:WRANGLER_LOG_PATH = Join-Path $TaskProjectRoot '.cache\wrangler-logs'
}

function Assert-TaskExit {
    if ($LASTEXITCODE -ne 0) { throw "Command failed with exit code $LASTEXITCODE" }
}
