param([Parameter(ValueFromRemainingArguments = $true)][string[]]$WranglerArgs)
. (Join-Path $PSScriptRoot 'toolchain.ps1')
Initialize-TaskEnvironment
$taskNode = Get-TaskNode
$taskWrangler = Join-Path $TaskProjectRoot 'worker\node_modules\wrangler\bin\wrangler.js'
if (-not (Test-Path -LiteralPath $taskWrangler)) { throw 'Run npm ci inside worker first.' }
Push-Location (Join-Path $TaskProjectRoot 'worker')
try {
    & $taskNode $taskWrangler @WranglerArgs
    Assert-TaskExit
} finally { Pop-Location }
