$ErrorActionPreference = 'Stop'
$taskProjectRoot = Split-Path -Parent $PSScriptRoot
. (Join-Path $taskProjectRoot 'scripts\toolchain.ps1')
Initialize-TaskEnvironment
$taskGo = Get-TaskGo
$taskCache = Join-Path $taskProjectRoot '.cache'
[void][IO.Directory]::CreateDirectory($taskCache)
$taskTestRoot = Join-Path $taskCache ('windows-installer-' + [Guid]::NewGuid().ToString('N'))
$taskPackage = Join-Path $taskTestRoot 'package with spaces'
$taskDestination = Join-Path $taskTestRoot 'Win user''s $private 中文'
[void][IO.Directory]::CreateDirectory($taskPackage)
$taskOldUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$taskFixtureSource = @'
package main
import ("encoding/json"; "fmt"; "os")
func main() {
    if len(os.Args)>1 && os.Args[1]=="version" { fmt.Println("mywormhole installer-fixture"); return }
    if len(os.Args)>1 && os.Args[1]=="fail" { os.Exit(7) }
    if len(os.Args)<3 { os.Exit(2) }
    data,_:=json.Marshal(map[string]any{"server":os.Getenv("MYWORMHOLE_SERVER"), "token":os.Getenv("MYWORMHOLE_INVITE_TOKEN"), "args":os.Args[1:]})
    if err:=os.WriteFile(os.Args[2],data,0600);err!=nil { os.Exit(3) }
}
'@
$taskFixtureFile = Join-Path $taskTestRoot 'fixture.go'
[IO.File]::WriteAllText($taskFixtureFile, $taskFixtureSource, [Text.UTF8Encoding]::new($false))
$taskFixtureBinary = Join-Path $taskPackage 'mywormhole-windows-amd64.exe'
& $taskGo build -trimpath -o $taskFixtureBinary $taskFixtureFile
Assert-TaskExit
Copy-Item -LiteralPath $taskFixtureBinary -Destination (Join-Path $taskPackage 'mywormhole-windows-arm64.exe')
$taskFixtureHash = (Get-FileHash -LiteralPath $taskFixtureBinary -Algorithm SHA256).Hash.ToLowerInvariant()
[IO.File]::WriteAllText((Join-Path $taskPackage 'SHA256SUMS'), "$taskFixtureHash  mywormhole-windows-amd64.exe`n$taskFixtureHash  mywormhole-windows-arm64.exe`n", [Text.UTF8Encoding]::new($false))
. (Join-Path $taskProjectRoot 'scripts\install-windows.ps1')
$script:TaskWindowsInstallerDirectory = $taskPackage
$script:TaskFixtureHealth = [pscustomobject]@{ service = 'mywormhole'; protocol = 1; ready = $true }
function Get-MyWormholeWindowsHealth {
    param([string]$ServerOrigin)
    return $script:TaskFixtureHealth
}
function Assert-Test {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { throw ('ASSERTION FAILED: ' + $Message) }
}
function Expect-TestFailure {
    param([scriptblock]$Action, [string]$Message)
    $taskFailed = $false
    try { & $Action } catch { $taskFailed = $true }
    Assert-Test $taskFailed $Message
}
$taskPlainToken = 'installer-test-token-$(never-run)-''quoted''-with\slash-ABCDEF'
$taskSecureToken = ConvertTo-SecureString $taskPlainToken -AsPlainText -Force
$taskArguments = @{ Server = 'https://example.workers.dev/'; InviteToken = $taskSecureToken;
    InstallRoot = $taskDestination; Architecture = 'amd64'; NonInteractive = $true; SkipPathUpdate = $true }

$taskOutput = Install-MyWormholeWindows @taskArguments 6>&1 | Out-String
Assert-Test (-not $taskOutput.Contains($taskPlainToken)) 'token appeared in installer output'
$taskSettingsPath = Join-Path $taskDestination 'config\settings.json'
$taskRawSettings = Get-Content -LiteralPath $taskSettingsPath -Raw -Encoding UTF8
$taskSettings = $taskRawSettings | ConvertFrom-Json
Assert-Test ($taskSettings.Server -ceq 'https://example.workers.dev') 'origin not normalized'
Assert-Test (-not $taskRawSettings.Contains($taskPlainToken)) 'token saved as plaintext'
Assert-Test ($taskSettings.InviteTokenProtected.Length -gt 100) 'protected token missing'
$taskAcl = Get-Acl -LiteralPath (Join-Path $taskDestination 'config')
Assert-Test $taskAcl.AreAccessRulesProtected 'config ACL still inherits access'
$taskUserSID = [Security.Principal.WindowsIdentity]::GetCurrent().User.Value
$taskAllowRules = @($taskAcl.GetAccessRules($true, $true, [Security.Principal.SecurityIdentifier]) | Where-Object { $_.AccessControlType -eq 'Allow' })
Assert-Test (@($taskAllowRules | Where-Object { $_.IdentityReference.Value -ne $taskUserSID }).Count -eq 0) 'config ACL grants other users access'
Write-Output 'PASS DPAPI encrypted settings, private ACL and hidden token output'

$taskCommand = Join-Path $taskDestination 'bin\mywormhole.cmd'
$taskRecorded = Join-Path $taskTestRoot 'recorded arguments.json'
& $taskCommand record $taskRecorded 'argument with spaces'
Assert-Test ($LASTEXITCODE -eq 0) 'launcher failed'
$taskRecord = Get-Content -LiteralPath $taskRecorded -Raw -Encoding UTF8 | ConvertFrom-Json
Assert-Test ($taskRecord.server -ceq 'https://example.workers.dev' -and $taskRecord.token -ceq $taskPlainToken) 'launcher did not load/decrypt settings correctly'
Assert-Test ($taskRecord.args[2] -ceq 'argument with spaces') 'arguments not preserved'
Assert-Test (-not (Test-Path -LiteralPath (Join-Path $taskDestination 'bin\mywormhole.exe'))) 'EXE would shadow the CMD launcher'
& $taskCommand fail
Assert-Test ($LASTEXITCODE -eq 7) 'CLI exit code not preserved'
Write-Output 'PASS real CMD and Windows PowerShell launcher, Unicode/quoted paths, arguments and exit status'

Install-MyWormholeWindows -InstallRoot $taskDestination -Architecture amd64 -NonInteractive -SkipPathUpdate | Out-Null
$taskSecondSettings = Get-Content -LiteralPath $taskSettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json
Assert-Test ($taskSecondSettings.Server -ceq $taskSettings.Server) 'reinstall changed saved origin'
Write-Output 'PASS noninteractive reinstall preserves saved configuration'

$taskBeforeHash = (Get-FileHash -LiteralPath $taskSettingsPath -Algorithm SHA256).Hash
$script:TaskFixtureHealth.ready = $false
Expect-TestFailure { Install-MyWormholeWindows @taskArguments | Out-Null } 'unready health accepted'
$script:TaskFixtureHealth.ready = $true
$taskBadAddressArguments = $taskArguments.Clone()
$taskBadAddressArguments.Server = 'https://example.workers.dev/unexpected-path'
Expect-TestFailure { Install-MyWormholeWindows @taskBadAddressArguments | Out-Null } 'address with path accepted'
Assert-Test ((Get-FileHash -LiteralPath $taskSettingsPath -Algorithm SHA256).Hash -ceq $taskBeforeHash) 'failure modified existing settings'
Write-Output 'PASS invalid address and unready service preserve old settings'

$taskOfflineArguments = $taskArguments.Clone()
$taskOfflineArguments.Server = 'https://offline.workers.dev'
$script:TaskFixtureHealth.ready = $false
Install-MyWormholeWindows @taskOfflineArguments -SkipHealthCheck | Out-Null
$script:TaskFixtureHealth.ready = $true
Assert-Test ((Get-Content -LiteralPath $taskSettingsPath -Raw -Encoding UTF8 | ConvertFrom-Json).Server -ceq 'https://offline.workers.dev') 'offline configuration failed'
$taskManifestPath = Join-Path $taskPackage 'SHA256SUMS'
[IO.File]::WriteAllText($taskManifestPath, (('0' * 64) + '  mywormhole-windows-amd64.exe'), [Text.UTF8Encoding]::new($false))
Expect-TestFailure { Install-MyWormholeWindows @taskArguments | Out-Null } 'bad SHA-256 accepted'
Assert-Test ([Environment]::GetEnvironmentVariable('Path', 'User') -ceq $taskOldUserPath) 'tests modified real user PATH'
Write-Output 'PASS offline mode, checksum rejection, and no modification of real user PATH'
Write-Output ('All Windows installer tests passed. Test files: ' + $taskTestRoot)
