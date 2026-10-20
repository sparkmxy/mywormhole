param([switch]$Large)
. (Join-Path $PSScriptRoot 'toolchain.ps1')
Initialize-TaskEnvironment
$taskGo = Get-TaskGo
$taskNode = Get-TaskNode
$taskOldLarge = $env:MYWORMHOLE_LARGE_TEST
Push-Location $TaskProjectRoot
try {
    & $taskGo test ./...
    Assert-TaskExit
    & $taskGo vet ./...
    Assert-TaskExit
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    & $taskGo build -trimpath -o dist/mywormhole.exe ./cmd/mywormhole
    Assert-TaskExit
    Push-Location worker
    try {
        & $taskNode node_modules/typescript/bin/tsc --noEmit
        Assert-TaskExit
        & $taskNode --test test/worker.test.mjs
        Assert-TaskExit
        $env:MYWORMHOLE_LARGE_TEST = if ($Large) { '1' } else { '0' }
        & $taskNode test/e2e.mjs
        Assert-TaskExit
    } finally { Pop-Location }
} finally {
    $env:MYWORMHOLE_LARGE_TEST = $taskOldLarge
    Pop-Location
}
