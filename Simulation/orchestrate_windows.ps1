<#
Runs the Simulation stack but opens one local terminal window per component.

Why this exists:
- Simulation/orchestrate.sh can open terminals via `wt`, but when you run it on a *jump server*
  it can't open windows on your local Windows machine.
- This script runs locally and uses SSH to start each component, so you can watch logs live.

Usage examples:
  ./Simulation/orchestrate_windows.ps1 attach-up -JumpHost <jump-host> -JumpUser <user>
  ./Simulation/orchestrate_windows.ps1 attach-up -JumpHost jump -JumpUser ben -Terminal WindowsTerminal

Notes:
- Requires `ssh` (OpenSSH client) on Windows.
- By default uses Windows Terminal if available, otherwise falls back to spawning new PowerShell console windows.
#>

[CmdletBinding()]
param(
  [Parameter(Mandatory = $true, Position = 0)]
  [ValidateSet('attach', 'attach-up', 'logs', 'down')]
  [string]$Action,

  [Parameter()]
  [string]$EnvFile = (Join-Path $PSScriptRoot 'orchestrate.env'),

  [Parameter()]
  [string]$JumpHost,

  [Parameter()]
  [string]$JumpUser,

  [Parameter()]
  [ValidateSet('Auto', 'WindowsTerminal', 'Conhost')]
  [string]$Terminal = 'Auto',

  # logs mode
  [Parameter(Position = 1)]
  [string]$LogHost,

  [Parameter(Position = 2)]
  [string]$Name
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:RunnerScript = Join-Path $PSScriptRoot 'wt_ssh_session.ps1'

function Get-SshExePath {
  $cmd = Get-Command 'ssh.exe' -ErrorAction SilentlyContinue
  if ($cmd -and $cmd.Source) { return $cmd.Source }

  $cmd = Get-Command 'ssh' -ErrorAction SilentlyContinue
  if ($cmd -and $cmd.CommandType -eq 'Application' -and $cmd.Source) { return $cmd.Source }

  throw "ssh.exe not found in PATH. Install the Windows Optional Feature 'OpenSSH Client' or add ssh.exe to PATH."
}

function Test-CommandExists {
  param([Parameter(Mandatory = $true)][string]$Name)
  return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

function Unquote-EnvValue {
  param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)
  $v = $Value.Trim()
  if (($v.StartsWith('"') -and $v.EndsWith('"')) -or ($v.StartsWith("'") -and $v.EndsWith("'"))) {
    return $v.Substring(1, $v.Length - 2)
  }
  return $v
}

function Read-BashEnvFile {
  param([Parameter(Mandatory = $true)][string]$Path)

  if (-not (Test-Path -LiteralPath $Path)) {
    throw "Missing env file: $Path (copy Simulation/orchestrate.env.example -> Simulation/orchestrate.env and edit it)"
  }

  $map = @{}
  foreach ($rawLine in (Get-Content -LiteralPath $Path)) {
    $line = ($rawLine + '').Trim()
    if (-not $line) { continue }
    if ($line.StartsWith('#')) { continue }

    # Strip inline comments only if they are preceded by whitespace (avoid breaking DSNs/URLs)
    $line = ($line -replace '\s+#.*$', '').Trim()
    if (-not $line) { continue }

    $m = [regex]::Match($line, '^(?<k>[A-Za-z_][A-Za-z0-9_]*)=(?<v>.*)$')
    if (-not $m.Success) { continue }

    $k = $m.Groups['k'].Value
    $v = Unquote-EnvValue -Value $m.Groups['v'].Value
    $map[$k] = $v
  }

  return $map
}

function Get-SshUserForHost {
  param(
    [Parameter(Mandatory = $true)][hashtable]$Env,
    [Parameter(Mandatory = $true)][string]$TargetHost
  )

  $defaultUser = $Env['SSH_USER_DEFAULT']
  if (-not $defaultUser) { $defaultUser = $Env['SSH_USER'] }
  if (-not $defaultUser) { $defaultUser = $env:USERNAME }
  if (-not $defaultUser) { $defaultUser = 'user' }

  $mapping = $Env['SSH_USERS']
  if ($mapping) {
    foreach ($item in ($mapping -split '\s+')) {
      if (-not $item) { continue }
      $parts = $item.Split(',', 2)
      if ($parts.Count -ne 2) { continue }
      if ($parts[0] -eq $TargetHost -and $parts[1]) {
        return $parts[1]
      }
    }
  }

  return $defaultUser
}

function Collect-AllHosts {
  param([Parameter(Mandatory = $true)][hashtable]$Env)

  $hosts = New-Object 'System.Collections.Generic.HashSet[string]'

  foreach ($k in @('TDS_SERVER_HOST', 'CS_HOST', 'PCTRBO_HOST', 'PA_HOST')) {
    if ($Env[$k]) { [void]$hosts.Add($Env[$k]) }
  }

  $stationsRaw = ''
  if ($Env.ContainsKey('STATIONS') -and $Env['STATIONS']) { $stationsRaw = $Env['STATIONS'] }
  foreach ($item in ($stationsRaw -split '\s+')) {
    if (-not $item) { continue }
    $vmHost = $item.Split(',', 2)[0]
    if ($vmHost) { [void]$hosts.Add($vmHost) }
  }

  $gatesRaw = ''
  if ($Env.ContainsKey('GATES') -and $Env['GATES']) { $gatesRaw = $Env['GATES'] }
  foreach ($item in ($gatesRaw -split '\s+')) {
    if (-not $item) { continue }
    $vmHost = $item.Split(',', 2)[0]
    if ($vmHost) { [void]$hosts.Add($vmHost) }
  }

  return $hosts
}

function Escape-BashSingleQuotes {
  param([Parameter(Mandatory = $true)][string]$Value)
  # In bash single-quoted strings, embed a single quote by ending the quote,
  # inserting a literal single quote, then resuming single quotes:
  #   'foo'"'"'bar'
  # i.e. replace each ' with: '"'"'
  $insert = "'" + [char]34 + "'" + [char]34 + "'"
  return ($Value -replace "'", $insert)
}

function New-SshArgs {
  param(
    [Parameter(Mandatory = $true)][hashtable]$Env,
    [Parameter(Mandatory = $true)][string]$TargetHost,
    [Parameter(Mandatory = $true)][string]$RemoteBashCommand,
    [Parameter()][string]$JumpHost,
    [Parameter()][string]$JumpUser,
    [Parameter()][bool]$AllocateTty = $true
  )

  $targetUser = Get-SshUserForHost -Env $Env -TargetHost $TargetHost

  $sshArgsList = New-Object 'System.Collections.Generic.List[string]'
  if ($AllocateTty) {
    [void]$sshArgsList.Add('-tt')
  } else {
    # Ensure ssh is non-interactive and doesn't keep stdin/pty open.
    [void]$sshArgsList.Add('-T')
  }
  [void]$sshArgsList.Add('-o'); [void]$sshArgsList.Add('BatchMode=no')
  [void]$sshArgsList.Add('-o'); [void]$sshArgsList.Add('StrictHostKeyChecking=accept-new')

  if ($JumpHost) {
    if (-not $JumpUser) {
      $JumpUser = $targetUser
    }
    [void]$sshArgsList.Add('-J')
    [void]$sshArgsList.Add("$JumpUser@$JumpHost")
  }

  [void]$sshArgsList.Add("$targetUser@$TargetHost")

  $escaped = Escape-BashSingleQuotes -Value $RemoteBashCommand
  [void]$sshArgsList.Add("bash -lc '$escaped'")

  return $sshArgsList
}

function New-SshBaseArgs {
  param(
    [Parameter(Mandatory = $true)][hashtable]$Env,
    [Parameter(Mandatory = $true)][string]$TargetHost,
    [Parameter()][string]$JumpHost,
    [Parameter()][string]$JumpUser,
    [Parameter()][bool]$AllocateTty = $true
  )

  $targetUser = Get-SshUserForHost -Env $Env -TargetHost $TargetHost

  $sshArgsList = New-Object 'System.Collections.Generic.List[string]'
  if ($AllocateTty) {
    [void]$sshArgsList.Add('-tt')
  } else {
    [void]$sshArgsList.Add('-T')
  }
  [void]$sshArgsList.Add('-o'); [void]$sshArgsList.Add('BatchMode=no')
  [void]$sshArgsList.Add('-o'); [void]$sshArgsList.Add('StrictHostKeyChecking=accept-new')

  if ($JumpHost) {
    if (-not $JumpUser) {
      $JumpUser = $targetUser
    }
    [void]$sshArgsList.Add('-J')
    [void]$sshArgsList.Add("$JumpUser@$JumpHost")
  }

  [void]$sshArgsList.Add("$targetUser@$TargetHost")
  return $sshArgsList
}

function Invoke-SshBashScript {
  param(
    [Parameter(Mandatory = $true)][hashtable]$Env,
    [Parameter(Mandatory = $true)][string]$TargetHost,
    [Parameter(Mandatory = $true)][string]$BashScript,
    [Parameter()][string]$JumpHost,
    [Parameter()][string]$JumpUser,
    [Parameter(Mandatory = $true)][string]$SshExe
  )

  $baseArgs = New-SshBaseArgs -Env $Env -TargetHost $TargetHost -JumpHost $JumpHost -JumpUser $JumpUser -AllocateTty $false
  $fullArgs = New-Object 'System.Collections.Generic.List[string]'
  foreach ($a in $baseArgs) { [void]$fullArgs.Add($a) }
  [void]$fullArgs.Add('bash')
  [void]$fullArgs.Add('-s')

  Write-Host ("--- " + $TargetHost + " ---") -ForegroundColor Cyan
  $BashScript | & $SshExe @($fullArgs.ToArray())
}

function Invoke-Ssh {
  param(
    [Parameter(Mandatory = $true)][string]$TargetHost,
    [Parameter(Mandatory = $true)][System.Collections.Generic.List[string]]$SshArgs,
    [Parameter(Mandatory = $true)][string]$SshExe
  )

  Write-Host ("--- " + $TargetHost + " ---") -ForegroundColor Cyan
  & $SshExe @($SshArgs.ToArray())
}

function Start-TerminalSession {
  param(
    [Parameter(Mandatory = $true)][string]$Title,
    [Parameter(Mandatory = $true)][System.Collections.Generic.List[string]]$SshArgs,
    [Parameter(Mandatory = $true)][string]$Terminal
  )

  if (-not (Test-Path -LiteralPath $script:RunnerScript)) {
    throw "Missing helper script: $script:RunnerScript"
  }

  $sshExe = Get-SshExePath
  $sshArgsJson = ($SshArgs.ToArray() | ConvertTo-Json -Compress)
  $sshArgsB64 = [Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($sshArgsJson))

  if ($Terminal -eq 'Auto') {
    if (Test-CommandExists -Name 'wt.exe') { $Terminal = 'WindowsTerminal' } else { $Terminal = 'Conhost' }
  }

  if ($Terminal -eq 'WindowsTerminal') {
    # Use new-tab and a helper file to avoid `wt` interpreting ';' in -Command as command separators.
    Start-Process -FilePath 'wt.exe' -ArgumentList @(
      '-w', '0',
      'new-tab', '--title', $Title,
      'powershell', '-NoExit', '-ExecutionPolicy', 'Bypass',
      '-File', $script:RunnerScript,
      '-Title', $Title,
      '-SshExe', $sshExe,
      '-SshArgsB64', $sshArgsB64
    ) | Out-Null
    return
  }

  # Conhost / classic PowerShell window
  Start-Process -FilePath 'powershell.exe' -ArgumentList @(
    '-NoExit', '-ExecutionPolicy', 'Bypass',
    '-File', $script:RunnerScript,
    '-Title', $Title,
    '-SshExe', $sshExe,
    '-SshArgsB64', $sshArgsB64
  ) | Out-Null
}

$envMap = Read-BashEnvFile -Path $EnvFile
$sshExePath = Get-SshExePath

function Get-PortNumber {
  param([Parameter(Mandatory = $true)][string]$Value)

  $v = ($Value + '').Trim()
  if (-not $v) { return $null }

  # :9101
  if ($v -match '^:(\d+)$') { return [int]$Matches[1] }

  # 127.0.0.1:5100
  if ($v -match ':(\d+)$') { return [int]$Matches[1] }

  # plain number
  if ($v -match '^(\d+)$') { return [int]$Matches[1] }

  return $null
}

function Collect-SimPorts {
  param([Parameter(Mandatory = $true)][hashtable]$Env)

  $ports = New-Object 'System.Collections.Generic.HashSet[int]'

  foreach ($k in @('TDS_SERVER_PORT', 'PROXY_LISTEN', 'CS_LISTEN', 'PCTRBO_LISTEN', 'PA_LISTEN')) {
    if ($Env.ContainsKey($k) -and $Env[$k]) {
      $p = Get-PortNumber -Value $Env[$k]
      if ($p) { [void]$ports.Add($p) }
    }
  }

  # STATIONS: host,id,listen,advertise
  $stationsRaw = ''
  if ($Env.ContainsKey('STATIONS') -and $Env['STATIONS']) { $stationsRaw = $Env['STATIONS'] }
  foreach ($item in ($stationsRaw -split '\s+')) {
    if (-not $item) { continue }
    $parts = $item.Split(',', 4)
    if ($parts.Count -eq 4) {
      $p = Get-PortNumber -Value $parts[2]
      if ($p) { [void]$ports.Add($p) }
    }
  }

  # GATES: host,id,stationId,listen
  $gatesRaw = ''
  if ($Env.ContainsKey('GATES') -and $Env['GATES']) { $gatesRaw = $Env['GATES'] }
  foreach ($item in ($gatesRaw -split '\s+')) {
    if (-not $item) { continue }
    $parts = $item.Split(',', 4)
    if ($parts.Count -eq 4) {
      $p = Get-PortNumber -Value $parts[3]
      if ($p) { [void]$ports.Add($p) }
    }
  }

  return $ports
}

switch ($Action) {
  'down' {
    $portsSet = Collect-SimPorts -Env $envMap
    $portsList = ($portsSet | Sort-Object) -join ' '

    $allHosts = Collect-AllHosts -Env $envMap
    foreach ($h in $allHosts) {
      $scriptLines = @(
        'set -euo pipefail',
        '',
        "ports=($portsList)",
        '',
        '# Kill anything we explicitly recorded.',
        'if compgen -G ~/tds_sim_logs/*.pid >/dev/null 2>&1; then',
        '  for f in ~/tds_sim_logs/*.pid; do',
        '    pid=$(cat "$f" 2>/dev/null || true)',
        '    if [[ -n "$pid" ]]; then',
        '      # kill the whole process group first (catches go-run child binaries / pipelines)',
        '      kill -- -"$pid" >/dev/null 2>&1 || true',
        '      kill "$pid" >/dev/null 2>&1 || true',
        '    fi',
        '    rm -f "$f" >/dev/null 2>&1 || true',
        '  done',
        'fi',
        '',
        '# Fallback: kill listeners on the known ports.',
        'for port in "${ports[@]}"; do',
        '  if command -v ss >/dev/null 2>&1; then',
        '    pids=$(ss -ltnp 2>/dev/null | grep -F ":$port" | sed -nE ''s/.*pid=([0-9]+).*/\1/p'' | sort -u)',
        '    for pid in $pids; do',
        '      kill -- -"$pid" >/dev/null 2>&1 || true',
        '      kill "$pid" >/dev/null 2>&1 || true',
        '    done',
        '  elif command -v lsof >/dev/null 2>&1; then',
        '    pids=$(lsof -t -iTCP:"$port" -sTCP:LISTEN 2>/dev/null | sort -u)',
        '    for pid in $pids; do',
        '      kill -- -"$pid" >/dev/null 2>&1 || true',
        '      kill "$pid" >/dev/null 2>&1 || true',
        '    done',
        '  elif command -v fuser >/dev/null 2>&1; then',
        '    fuser -k "${port}/tcp" >/dev/null 2>&1 || true',
        '  fi',
        'done',
        '',
        '# Last resort: try to stop go-run patterns.',
        'pkill -f -- "go run -mod=vendor" >/dev/null 2>&1 || true',
        'pkill -f -- "tail -f /dev/null | go run" >/dev/null 2>&1 || true',
        '',
        'echo stopped'
      )

      $script = ($scriptLines -join "`n")

      Invoke-SshBashScript -Env $envMap -TargetHost $h -BashScript $script -JumpHost $JumpHost -JumpUser $JumpUser -SshExe $sshExePath
    }

    Write-Host 'Stop commands sent to all hosts.' -ForegroundColor Green
    break
  }

  'logs' {
    if (-not $LogHost -or -not $Name) {
      throw "Usage: ./Simulation/orchestrate_windows.ps1 logs <host> <name> [-JumpHost ...] [-JumpUser ...]"
    }

    $remote = "tail -n 200 -f ~/tds_sim_logs/$Name.log"
    $sshArgs = New-SshArgs -Env $envMap -TargetHost $LogHost -RemoteBashCommand $remote -JumpHost $JumpHost -JumpUser $JumpUser
    Start-TerminalSession -Title "logs:$Name@$LogHost" -SshArgs $sshArgs -Terminal $Terminal
    break
  }

  'attach' {
    $hosts = Collect-AllHosts -Env $envMap
    foreach ($h in $hosts) {
      $sshArgs = New-SshArgs -Env $envMap -TargetHost $h -RemoteBashCommand 'exec bash -l' -JumpHost $JumpHost -JumpUser $JumpUser
      Start-TerminalSession -Title "ssh@$h" -SshArgs $sshArgs -Terminal $Terminal
      Start-Sleep -Milliseconds 150
    }
    break
  }

  'attach-up' {
    foreach ($k in @('REMOTE_REPO_DIR', 'TDS_SERVER_HOST', 'TDS_SERVER_PORT', 'PROXY_LISTEN', 'CS_HOST', 'PCTRBO_HOST', 'PA_HOST')) {
      if (-not $envMap[$k]) { throw "Missing $k in $EnvFile" }
    }

    $repo = $envMap['REMOTE_REPO_DIR']
    $serverAddr = "{0}:{1}" -f $envMap['TDS_SERVER_HOST'], $envMap['TDS_SERVER_PORT']

    # 1) TDS server
    $tdsCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; go run -mod=vendor ./cmd/server -port $($envMap['TDS_SERVER_PORT'])"
    $sshArgs = New-SshArgs -Env $envMap -TargetHost $envMap['TDS_SERVER_HOST'] -RemoteBashCommand $tdsCmd -JumpHost $JumpHost -JumpUser $JumpUser
    Start-TerminalSession -Title "tds_server@$($envMap['TDS_SERVER_HOST'])" -SshArgs $sshArgs -Terminal $Terminal

    # 2) client_proxy on every host in the sim
    $allHosts = Collect-AllHosts -Env $envMap
    foreach ($h in $allHosts) {
      $proxyCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; export TDS_SERVER_ADDR='$serverAddr'; export TDS_PROXY_LISTEN='$($envMap['PROXY_LISTEN'])'; go run -mod=vendor ./cmd/client_proxy -background"
      $sshArgs = New-SshArgs -Env $envMap -TargetHost $h -RemoteBashCommand $proxyCmd -JumpHost $JumpHost -JumpUser $JumpUser
      Start-TerminalSession -Title "client_proxy@$h" -SshArgs $sshArgs -Terminal $Terminal
      Start-Sleep -Milliseconds 90
    }

    # 3) Services
    $csCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; export CS_DB_DSN='$($envMap['CS_DB_DSN'])'; go run -mod=vendor ./Simulation/cs -listen $($envMap['CS_LISTEN']) -proxy $($envMap['PROXY_LISTEN']) -advertise $($envMap['CS_ADVERTISE'])"
    $sshArgs = New-SshArgs -Env $envMap -TargetHost $envMap['CS_HOST'] -RemoteBashCommand $csCmd -JumpHost $JumpHost -JumpUser $JumpUser
    Start-TerminalSession -Title "cs@$($envMap['CS_HOST'])" -SshArgs $sshArgs -Terminal $Terminal

    $pctrboCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; export PCTRBO_DB_DSN='$($envMap['PCTRBO_DB_DSN'])'; go run -mod=vendor ./Simulation/pctrbo -listen $($envMap['PCTRBO_LISTEN']) -proxy $($envMap['PROXY_LISTEN']) -advertise $($envMap['PCTRBO_ADVERTISE'])"
    $sshArgs = New-SshArgs -Env $envMap -TargetHost $envMap['PCTRBO_HOST'] -RemoteBashCommand $pctrboCmd -JumpHost $JumpHost -JumpUser $JumpUser
    Start-TerminalSession -Title "pctrbo@$($envMap['PCTRBO_HOST'])" -SshArgs $sshArgs -Terminal $Terminal

    $paCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; export PA_DB_DSN='$($envMap['PA_DB_DSN'])'; go run -mod=vendor ./Simulation/pa -listen $($envMap['PA_LISTEN']) -proxy $($envMap['PROXY_LISTEN']) -private-key $($envMap['PA_PRIVATE_KEY']) -advertise $($envMap['PA_ADVERTISE'])"
    $sshArgs = New-SshArgs -Env $envMap -TargetHost $envMap['PA_HOST'] -RemoteBashCommand $paCmd -JumpHost $JumpHost -JumpUser $JumpUser
    Start-TerminalSession -Title "pa@$($envMap['PA_HOST'])" -SshArgs $sshArgs -Terminal $Terminal

    # Stations
    $stationsRaw = ''
    if ($envMap.ContainsKey('STATIONS') -and $envMap['STATIONS']) { $stationsRaw = $envMap['STATIONS'] }
    foreach ($item in ($stationsRaw -split '\s+')) {
      if (-not $item) { continue }
      $parts = $item.Split(',', 4)
      if ($parts.Count -ne 4) { throw "Bad STATIONS entry: $item" }
      $vmHost = $parts[0]
      $stationId = $parts[1]
      $listen = $parts[2]
      $advertise = $parts[3]

      $stationCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; go run -mod=vendor ./Simulation/station_computer -station-id $stationId -listen $listen -proxy $($envMap['PROXY_LISTEN']) -advertise $advertise"
      $sshArgs = New-SshArgs -Env $envMap -TargetHost $vmHost -RemoteBashCommand $stationCmd -JumpHost $JumpHost -JumpUser $JumpUser
      Start-TerminalSession -Title "station:$stationId@$vmHost" -SshArgs $sshArgs -Terminal $Terminal
      Start-Sleep -Milliseconds 70
    }

    # Gates
    $gatesRaw = ''
    if ($envMap.ContainsKey('GATES') -and $envMap['GATES']) { $gatesRaw = $envMap['GATES'] }
    foreach ($item in ($gatesRaw -split '\s+')) {
      if (-not $item) { continue }
      $parts = $item.Split(',', 4)
      if ($parts.Count -ne 4) { throw "Bad GATES entry: $item" }
      $vmHost = $parts[0]
      $gateId = $parts[1]
      $stationId = $parts[2]
      $listen = $parts[3]

      $gateCmd = "set -euo pipefail; cd $repo; export GOTOOLCHAIN=local; export PCTR_PUBLIC_KEY='$($envMap['PCTR_PUBLIC_KEY'])'; go run -mod=vendor ./Simulation/gate -id $gateId -station-id $stationId -listen $listen -proxy $($envMap['PROXY_LISTEN']) -pctr-public-key $($envMap['PCTR_PUBLIC_KEY'])"
      $sshArgs = New-SshArgs -Env $envMap -TargetHost $vmHost -RemoteBashCommand $gateCmd -JumpHost $JumpHost -JumpUser $JumpUser
      Start-TerminalSession -Title "gate:$gateId@$vmHost" -SshArgs $sshArgs -Terminal $Terminal
      Start-Sleep -Milliseconds 70
    }

    Write-Host "Started all sessions. Each window stays attached to its process; close the window or Ctrl+C to stop." -ForegroundColor Green
    break
  }
}
