param([switch]$All)
. (Join-Path $PSScriptRoot 'toolchain.ps1')
Initialize-TaskEnvironment
$taskGo = Get-TaskGo
$taskOldOS = $env:GOOS
$taskOldArch = $env:GOARCH
$taskOldCGO = $env:CGO_ENABLED
Push-Location $TaskProjectRoot
try {
    New-Item -ItemType Directory -Force -Path dist | Out-Null
    if ($All) {
        $env:CGO_ENABLED = '0'
        $taskTargets = @('windows/amd64', 'linux/amd64', 'linux/arm64', 'darwin/amd64', 'darwin/arm64')
        foreach ($taskTarget in $taskTargets) {
            $env:GOOS, $env:GOARCH = $taskTarget.Split('/')
            $taskSuffix = if ($env:GOOS -eq 'windows') { '.exe' } else { '' }
            $taskOutput = 'dist/mywormhole-' + $env:GOOS + '-' + $env:GOARCH + $taskSuffix
            & $taskGo build -trimpath -o $taskOutput ./cmd/mywormhole
            Assert-TaskExit
            Write-Output "Built $taskOutput"
        }
        Copy-Item -LiteralPath dist/mywormhole-windows-amd64.exe -Destination dist/mywormhole.exe
    } else {
        & $taskGo build -trimpath -o dist/mywormhole.exe ./cmd/mywormhole
        Assert-TaskExit
        Write-Output 'Built dist/mywormhole.exe'
    }
    Copy-Item -LiteralPath THIRD_PARTY_NOTICES.md -Destination dist/THIRD_PARTY_NOTICES.md
} finally {
    $env:GOOS = $taskOldOS
    $env:GOARCH = $taskOldArch
    $env:CGO_ENABLED = $taskOldCGO
    Pop-Location
}
