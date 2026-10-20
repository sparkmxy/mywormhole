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
        $env:GOOS = 'darwin'
        $env:CGO_ENABLED = '0'
        foreach ($taskArch in @('arm64', 'amd64')) {
            $env:GOARCH = $taskArch
            & $taskGo build -trimpath -o "dist/mywormhole-darwin-$taskArch" ./cmd/mywormhole
            Assert-TaskExit
        }
    }
    $taskPackage = Join-Path $TaskProjectRoot 'dist\mywormhole-mac'
    New-Item -ItemType Directory -Force -Path $taskPackage | Out-Null
    $taskHashes = @()
    foreach ($taskName in @('mywormhole-darwin-arm64', 'mywormhole-darwin-amd64')) {
        $taskBinary = Join-Path $TaskProjectRoot "dist\$taskName"
        if (-not (Test-Path -LiteralPath $taskBinary -PathType Leaf)) {
            throw "Missing $taskName. Run scripts/package-mac.ps1 -Rebuild."
        }
        Copy-Item -LiteralPath $taskBinary -Destination (Join-Path $taskPackage $taskName) -Force
        $taskDigest = (Get-FileHash -LiteralPath $taskBinary -Algorithm SHA256).Hash.ToLowerInvariant()
        $taskHashes += "$taskDigest  $taskName"
    }
    # Explicit allowlist: the package never includes local account/configuration files.
    Copy-Item -LiteralPath scripts/install-mac.sh -Destination (Join-Path $taskPackage 'install-mac.sh') -Force
    Copy-Item -LiteralPath docs/MACOS.md -Destination (Join-Path $taskPackage 'README.md') -Force
    Copy-Item -LiteralPath THIRD_PARTY_NOTICES.md -Destination (Join-Path $taskPackage 'THIRD_PARTY_NOTICES.md') -Force
    [IO.File]::WriteAllText((Join-Path $taskPackage 'SHA256SUMS'), ($taskHashes -join "`n") + "`n", [Text.UTF8Encoding]::new($false))
    $taskFiles = @('install-mac.sh', 'mywormhole-darwin-arm64', 'mywormhole-darwin-amd64', 'README.md', 'SHA256SUMS', 'THIRD_PARTY_NOTICES.md')
    $taskEntries = foreach ($taskName in $taskFiles) { Join-Path $taskPackage $taskName }
    Compress-Archive -LiteralPath $taskEntries -DestinationPath dist/mywormhole-mac.zip -CompressionLevel Optimal -Force
    Write-Output ('Mac package: ' + (Join-Path $TaskProjectRoot 'dist\mywormhole-mac.zip'))
} finally {
    $env:GOOS = $taskOldOS
    $env:GOARCH = $taskOldArch
    $env:CGO_ENABLED = $taskOldCGO
    Pop-Location
}
