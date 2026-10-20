[CmdletBinding()]
param(
    [string]$Server,
    [Security.SecureString]$InviteToken,
    [string]$InstallRoot,
    [string]$BinaryPath,
    [ValidateSet('auto', 'amd64', 'arm64')][string]$Architecture = 'auto',
    [switch]$SkipHealthCheck,
    [switch]$SkipPathUpdate,
    [switch]$NonInteractive
)

$script:TaskWindowsInstallerDirectory = $PSScriptRoot
# PowerShell 7 may pass its module search path to Windows PowerShell 5.1.
# Select this host's built-in security module explicitly for DPAPI and ACLs.
if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
    Import-Module (Join-Path $PSHOME 'Modules\Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1') -ErrorAction Stop
}

function ConvertTo-MyWormholeServerOrigin {
    param([string]$Value)
    $taskUri = $null
    if (-not [Uri]::TryCreate($Value, [UriKind]::Absolute, [ref]$taskUri) -or
        $taskUri.Scheme -ne 'https' -or -not $taskUri.Host -or $taskUri.UserInfo -or
        $taskUri.Query -or $taskUri.Fragment -or $taskUri.AbsolutePath -ne '/') {
        throw '请输入完整 HTTPS 服务地址，不含用户名、路径、查询参数或片段。'
    }
    return $taskUri.GetLeftPart([UriPartial]::Authority)
}

function Get-MyWormholeWindowsHealth {
    param([string]$ServerOrigin)
    [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    return Invoke-RestMethod -Uri ($ServerOrigin + '/health') -Method Get -UseBasicParsing -MaximumRedirection 0 -TimeoutSec 15 -ErrorAction Stop
}

function Set-MyWormholePrivateDirectory {
    param([string]$Path)
    [void][IO.Directory]::CreateDirectory($Path)
    $taskIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
    # Modify only the access rules. Replacing owner/audit sections would request
    # privileges that a normal user does not have when reconfiguring a folder.
    $taskAcl = Get-Acl -LiteralPath $Path -ErrorAction Stop
    $taskAcl.SetAccessRuleProtection($true, $false)
    foreach ($taskExistingRule in @($taskAcl.GetAccessRules($true, $false, [Security.Principal.SecurityIdentifier]))) {
        [void]$taskAcl.RemoveAccessRuleSpecific($taskExistingRule)
    }
    $taskInheritance = [Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
    $taskRule = New-Object Security.AccessControl.FileSystemAccessRule(
        $taskIdentity.User, [Security.AccessControl.FileSystemRights]::FullControl,
        $taskInheritance, [Security.AccessControl.PropagationFlags]::None,
        [Security.AccessControl.AccessControlType]::Allow)
    [void]$taskAcl.AddAccessRule($taskRule)
    if ($PSVersionTable.PSEdition -eq 'Desktop') {
        [IO.Directory]::SetAccessControl($Path, $taskAcl)
    } else {
        [IO.FileSystemAclExtensions]::SetAccessControl([IO.DirectoryInfo]::new($Path), $taskAcl)
    }
}

function Write-MyWormholeAtomicBytes {
    param([string]$Path, [byte[]]$Bytes)
    $taskTemporary = Join-Path ([IO.Path]::GetDirectoryName($Path)) ('.install-' + [Guid]::NewGuid().ToString('N'))
    try {
        [IO.File]::WriteAllBytes($taskTemporary, $Bytes)
        if ([IO.File]::Exists($Path)) { [IO.File]::Replace($taskTemporary, $Path, [System.Management.Automation.Language.NullString]::Value) }
        else { [IO.File]::Move($taskTemporary, $Path) }
    } finally {
        if ([IO.File]::Exists($taskTemporary)) { Remove-Item -LiteralPath $taskTemporary -Force }
    }
}

function Write-MyWormholeAtomicText {
    param([string]$Path, [string]$Text)
    # A BOM preserves Chinese text and paths in Windows PowerShell 5.1.
    $taskEncoding = New-Object Text.UTF8Encoding($true)
    $taskBytes = $taskEncoding.GetPreamble() + $taskEncoding.GetBytes($Text)
    Write-MyWormholeAtomicBytes -Path $Path -Bytes $taskBytes
}

function Register-MyWormholeWindowsPath {
    param([string]$BinDirectory)
    $taskUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $taskEntries = @($taskUserPath -split ';' | Where-Object { $_ })
    $taskFound = @($taskEntries | Where-Object { $_.Trim().TrimEnd('\') -ieq $BinDirectory.TrimEnd('\') }).Count -gt 0
    if (-not $taskFound) {
        $taskNewPath = if ($taskUserPath) { $BinDirectory + ';' + $taskUserPath } else { $BinDirectory }
        [Environment]::SetEnvironmentVariable('Path', $taskNewPath, 'User')
    }
    $taskProcessEntries = @($env:Path -split ';' | Where-Object { $_ })
    if (@($taskProcessEntries | Where-Object { $_.Trim().TrimEnd('\') -ieq $BinDirectory.TrimEnd('\') }).Count -eq 0) {
        $env:Path = $BinDirectory + ';' + $env:Path
    }
}

function Install-MyWormholeWindows {
    [CmdletBinding()]
    param(
        [string]$Server,
        [Security.SecureString]$InviteToken,
        [string]$InstallRoot,
        [string]$BinaryPath,
        [ValidateSet('auto', 'amd64', 'arm64')][string]$Architecture = 'auto',
        [switch]$SkipHealthCheck,
        [switch]$SkipPathUpdate,
        [switch]$NonInteractive
    )
    $ErrorActionPreference = 'Stop'
    if ([Environment]::OSVersion.Platform -ne [PlatformID]::Win32NT) { throw '此安装脚本适用于 Windows。' }
    if (-not $InstallRoot) { $InstallRoot = Join-Path $env:LOCALAPPDATA 'mywormhole' }
    $taskRoot = [IO.Path]::GetFullPath($InstallRoot)
    $taskBin = Join-Path $taskRoot 'bin'
    $taskApp = Join-Path $taskRoot 'app'
    $taskConfigDirectory = Join-Path $taskRoot 'config'
    $taskConfigPath = Join-Path $taskConfigDirectory 'settings.json'

    if ($Architecture -eq 'auto') {
        $taskMachine = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
        switch ($taskMachine.ToUpperInvariant()) {
            'AMD64' { $Architecture = 'amd64' }
            'ARM64' { $Architecture = 'arm64' }
            default { throw '此安装包支持 64 位 Intel/AMD 和 ARM64 Windows。' }
        }
    }
    if (-not $BinaryPath) {
        $taskName = 'mywormhole-windows-' + $Architecture + '.exe'
        $BinaryPath = Join-Path $script:TaskWindowsInstallerDirectory $taskName
        if (-not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) {
            $BinaryPath = Join-Path (Split-Path -Parent $script:TaskWindowsInstallerDirectory) ('dist\' + $taskName)
        }
    }
    if (-not (Test-Path -LiteralPath $BinaryPath -PathType Leaf)) { throw '未找到对应的 EXE，请完整解压安装包后再运行。' }
    $BinaryPath = (Resolve-Path -LiteralPath $BinaryPath).Path
    $taskManifest = Join-Path $script:TaskWindowsInstallerDirectory 'SHA256SUMS'
    if (Test-Path -LiteralPath $taskManifest -PathType Leaf) {
        $taskName = [IO.Path]::GetFileName($BinaryPath)
        $taskExpected = @()
        foreach ($taskLine in [IO.File]::ReadAllLines($taskManifest)) {
            if ($taskLine -match '^([0-9a-fA-F]{64})\s+(.+)$' -and $Matches[2] -ceq $taskName) { $taskExpected += $Matches[1] }
        }
        if ($taskExpected.Count -ne 1 -or (Get-FileHash -LiteralPath $BinaryPath -Algorithm SHA256).Hash -ine $taskExpected[0]) {
            throw 'EXE 的 SHA-256 与安装包校验清单不一致，请重新获取安装包。'
        }
    }

    $taskVersion = & $BinaryPath version 2>&1
    if ($LASTEXITCODE -ne 0 -or -not (($taskVersion -join "`n") -match '^mywormhole ')) { throw '程序启动检查失败。请确认程序来源、芯片版本和系统提示。' }
    Write-Host ($taskVersion -join "`n")
    $taskExisting = $null
    if (Test-Path -LiteralPath $taskConfigPath -PathType Leaf) {
        $taskExisting = Get-Content -LiteralPath $taskConfigPath -Raw -Encoding UTF8 | ConvertFrom-Json
    }
    $taskServer = if ($Server) { $Server } elseif ($taskExisting.Server) { $taskExisting.Server } else { $env:MYWORMHOLE_SERVER }
    if (-not $Server -and -not $NonInteractive) {
        $taskPrompt = if ($taskServer) { 'Worker 地址（回车保留 ' + $taskServer + '）' } else { 'Cloudflare Worker 地址（https://...）' }
        $taskAnswer = Read-Host $taskPrompt
        if ($taskAnswer) { $taskServer = $taskAnswer }
    }
    $taskServer = ConvertTo-MyWormholeServerOrigin $taskServer
    if (-not $SkipHealthCheck) {
        Write-Host '正在检查服务连接……'
        try { $taskHealth = Get-MyWormholeWindowsHealth -ServerOrigin $taskServer }
        catch { throw '无法连接服务，请检查地址和网络。离线配置时可加 -SkipHealthCheck。' }
        if ($taskHealth.service -cne 'mywormhole' -or $taskHealth.protocol -ne 1) { throw '此地址不是兼容的 mywormhole 服务。' }
        if ($taskHealth.ready -ne $true) { throw '服务配置尚未就绪（ready 不是 true），请联系部署者。' }
    }

    $taskOldToken = $null
    if ($taskExisting.InviteTokenProtected) {
        try { $taskOldToken = ConvertTo-SecureString -String $taskExisting.InviteTokenProtected -ErrorAction Stop }
        catch { Write-Host '原令牌无法由当前 Windows 账户解密，请重新输入邀请令牌。' }
    }
    if (-not $InviteToken) {
        if ($NonInteractive) { $InviteToken = $taskOldToken }
        else {
            $taskPrompt = if ($taskOldToken) { '邀请令牌 INVITE_TOKEN（不明文显示，回车保留原令牌）' } else { '邀请令牌 INVITE_TOKEN（不明文显示）' }
            $InviteToken = Read-Host $taskPrompt -AsSecureString
            if ($InviteToken.Length -eq 0 -and $taskOldToken) { $InviteToken = $taskOldToken }
        }
    }
    if (-not $InviteToken -or $InviteToken.Length -lt 24) { throw '需要至少 24 个字符的 INVITE_TOKEN，使用部署时设置的邀请令牌。' }
    $taskPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($InviteToken)
    try {
        $taskPlain = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($taskPointer)
        if ($taskPlain.Contains("`r") -or $taskPlain.Contains("`n")) { throw '邀请令牌不能包含换行符。' }
    } finally {
        $taskPlain = $null
        [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($taskPointer)
    }
    $taskProtected = ConvertFrom-SecureString -SecureString $InviteToken
    $taskSettings = [ordered]@{ Version = 1; Server = $taskServer; InviteTokenProtected = $taskProtected } | ConvertTo-Json

    [void][IO.Directory]::CreateDirectory($taskBin)
    [void][IO.Directory]::CreateDirectory($taskApp)
    if (Test-Path -LiteralPath (Join-Path $taskBin 'mywormhole.exe')) {
        throw 'bin 目录已有同名 EXE，会覆盖配置启动器的命令查找。请将旧 EXE 移到其他目录，或使用新的 -InstallRoot。'
    }
    Set-MyWormholePrivateDirectory -Path $taskConfigDirectory
    $taskBinaryDestination = Join-Path $taskApp 'mywormhole.exe'
    if ($BinaryPath -ine $taskBinaryDestination) {
        Write-MyWormholeAtomicBytes -Path $taskBinaryDestination -Bytes ([IO.File]::ReadAllBytes($BinaryPath))
    }
    $taskQuotedConfig = $taskConfigPath.Replace("'", "''")
    $taskQuotedBinary = $taskBinaryDestination.Replace("'", "''")
    $taskLauncher = @'
# mywormhole managed launcher v1
$ErrorActionPreference = 'Stop'
Import-Module (Join-Path $PSHOME 'Modules\Microsoft.PowerShell.Security\Microsoft.PowerShell.Security.psd1') -ErrorAction Stop
$taskSettings = Get-Content -LiteralPath '__CONFIG__' -Raw -Encoding UTF8 | ConvertFrom-Json
$taskToken = ConvertTo-SecureString -String $taskSettings.InviteTokenProtected
$taskPointer = [Runtime.InteropServices.Marshal]::SecureStringToBSTR($taskToken)
$taskPreviousServer = $env:MYWORMHOLE_SERVER
$taskPreviousToken = $env:MYWORMHOLE_INVITE_TOKEN
$taskExitCode = 1
try {
    $env:MYWORMHOLE_SERVER = $taskSettings.Server
    $env:MYWORMHOLE_INVITE_TOKEN = [Runtime.InteropServices.Marshal]::PtrToStringBSTR($taskPointer)
    & '__BINARY__' @args
    $taskExitCode = $LASTEXITCODE
} finally {
    $env:MYWORMHOLE_SERVER = $taskPreviousServer
    $env:MYWORMHOLE_INVITE_TOKEN = $taskPreviousToken
    [Runtime.InteropServices.Marshal]::ZeroFreeBSTR($taskPointer)
    $taskToken.Dispose()
}
exit $taskExitCode
'@
    $taskLauncher = $taskLauncher.Replace('__CONFIG__', $taskQuotedConfig).Replace('__BINARY__', $taskQuotedBinary)
    Write-MyWormholeAtomicText -Path (Join-Path $taskBin 'mywormhole-launcher.ps1') -Text $taskLauncher
    $taskCommand = "@echo off`r`n`"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe`" -NoLogo -NoProfile -ExecutionPolicy Bypass -File `"%~dp0mywormhole-launcher.ps1`" %*`r`nexit /b %errorlevel%`r`n"
    Write-MyWormholeAtomicBytes -Path (Join-Path $taskBin 'mywormhole.cmd') -Bytes ([Text.Encoding]::ASCII.GetBytes($taskCommand))
    Write-MyWormholeAtomicText -Path $taskConfigPath -Text $taskSettings
    $taskNotice = Join-Path $script:TaskWindowsInstallerDirectory 'THIRD_PARTY_NOTICES.md'
    if (-not (Test-Path -LiteralPath $taskNotice)) { $taskNotice = Join-Path (Split-Path -Parent $script:TaskWindowsInstallerDirectory) 'THIRD_PARTY_NOTICES.md' }
    if (Test-Path -LiteralPath $taskNotice -PathType Leaf) { Copy-Item -LiteralPath $taskNotice -Destination (Join-Path $taskRoot 'THIRD_PARTY_NOTICES.md') -Force }
    if (-not $SkipPathUpdate) {
        try { Register-MyWormholeWindowsPath -BinDirectory $taskBin }
        catch { Write-Warning '未能更新用户 PATH。配置已保存，可使用下方的完整命令路径。' }
    }

    Write-Host ''
    Write-Host ('配置完成。服务地址：' + $taskServer)
    Write-Host '邀请令牌使用 Windows 当前用户 DPAPI 加密保存。'
    Write-Host '请重新打开终端，然后运行：'
    Write-Host '  mywormhole send "D:\Files\archive.zip"'
    Write-Host '  mywormhole receive 483921-amber-brook-cloud-river'
    Write-Host ('当前终端也可直接使用：& "' + (Join-Path $taskBin 'mywormhole.cmd') + '" version')
    Write-Host '使用发送和接收命令时请运行 mywormhole 或 mywormhole.cmd；直接运行底层 mywormhole.exe 不会加载保存的配置。'
}

if ($MyInvocation.InvocationName -ne '.') { Install-MyWormholeWindows @PSBoundParameters }
