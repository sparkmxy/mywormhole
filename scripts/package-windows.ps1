param([switch]$Rebuild)
. (Join-Path $PSScriptRoot 'toolchain.ps1')
Initialize-TaskEnvironment
$taskOldOS = $env:GOOS
$taskOldArch = $env:GOARCH
$taskOldCGO = $env:CGO_ENABLED
Push-Location $TaskProjectRoot
try {
    if ($Rebuild) {
        $taskGo = Get-TaskGo
        $env:GOOS = 'windows'
        $env:CGO_ENABLED = '0'
        foreach ($taskArch in @('amd64', 'arm64')) {
            $env:GOARCH = $taskArch
            & $taskGo build -trimpath -o "dist/mywormhole-windows-$taskArch.exe" ./cmd/mywormhole
            Assert-TaskExit
        }
    }
    $taskPackage = Join-Path $TaskProjectRoot 'dist\mywormhole-windows'
    New-Item -ItemType Directory -Force -Path $taskPackage | Out-Null
    $taskHashes = @()
    foreach ($taskName in @('mywormhole-windows-amd64.exe', 'mywormhole-windows-arm64.exe')) {
        $taskBinary = Join-Path $TaskProjectRoot "dist\$taskName"
        if (-not (Test-Path -LiteralPath $taskBinary -PathType Leaf)) { throw "Missing $taskName. Run scripts/package-windows.ps1 -Rebuild." }
        Copy-Item -LiteralPath $taskBinary -Destination (Join-Path $taskPackage $taskName) -Force
        $taskDigest = (Get-FileHash -LiteralPath $taskBinary -Algorithm SHA256).Hash.ToLowerInvariant()
        $taskHashes += "$taskDigest  $taskName"
    }
    # Fixed allowlist: never package account credentials, local settings or runtime caches.
    Copy-Item -LiteralPath scripts/install-windows.ps1 -Destination (Join-Path $taskPackage 'install-windows.ps1') -Force
    Copy-Item -LiteralPath scripts/setup-windows.cmd -Destination (Join-Path $taskPackage 'setup-windows.cmd') -Force
    Copy-Item -LiteralPath docs/WINDOWS.md -Destination (Join-Path $taskPackage 'README.md') -Force
    Copy-Item -LiteralPath THIRD_PARTY_NOTICES.md -Destination (Join-Path $taskPackage 'THIRD_PARTY_NOTICES.md') -Force
    [IO.File]::WriteAllText((Join-Path $taskPackage 'SHA256SUMS'), ($taskHashes -join "`n") + "`n", [Text.UTF8Encoding]::new($false))
    $taskFiles = @('setup-windows.cmd', 'install-windows.ps1', 'mywormhole-windows-amd64.exe', 'mywormhole-windows-arm64.exe', 'README.md', 'SHA256SUMS', 'THIRD_PARTY_NOTICES.md')
    $taskEntries = foreach ($taskName in $taskFiles) { Join-Path $taskPackage $taskName }
    Compress-Archive -LiteralPath $taskEntries -DestinationPath dist/mywormhole-windows.zip -CompressionLevel Optimal -Force
    Write-Output ('Windows package: ' + (Join-Path $TaskProjectRoot 'dist\mywormhole-windows.zip'))
} finally {
    $env:GOOS = $taskOldOS
    $env:GOARCH = $taskOldArch
    $env:CGO_ENABLED = $taskOldCGO
    Pop-Location
}
