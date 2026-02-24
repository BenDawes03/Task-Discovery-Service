<#
Helper invoked by orchestrate_windows.ps1.
Runs an SSH command and keeps the window open.
#>

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)]
  [string]$Title,

  [Parameter(Mandatory = $true)]
  [string]$SshExe,

  [Parameter(Mandatory = $true)]
  [string]$SshArgsB64
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

try {
  $Host.UI.RawUI.WindowTitle = $Title
} catch {
  # ignore
}

$sshArgs = @()
if ($SshArgsB64) {
  $json = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($SshArgsB64))
  $sshArgs = (ConvertFrom-Json -InputObject $json)
}

if (-not (Test-Path -LiteralPath $SshExe)) {
  throw "ssh executable not found at: $SshExe"
}

& $SshExe @sshArgs

Write-Host ''
Write-Host 'SSH session ended. Press Enter to close this window...' -ForegroundColor Yellow
[void]([Console]::ReadLine())
