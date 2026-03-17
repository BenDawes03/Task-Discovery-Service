<#
Interactive helper for sending card taps to running Simulation gates.

It reads the GATES entries from Simulation/orchestrate.env, derives each gate's
HTTP listener URL, optionally probes /health, lets you choose a gate, and posts
the tap payload to /tap so you do not need to build curl requests manually.
#>

[CmdletBinding()]
param(
  [Parameter()]
  [string]$EnvFile = (Join-Path $PSScriptRoot 'orchestrate.env'),

  [Parameter()]
  [string]$GateId,

  [Parameter()]
  [string]$GateUrl,

  [Parameter()]
  [ValidateSet('OY', 'PCTR')]
  [string]$CardType,

  [Parameter()]
  [string]$CardId,

  [Parameter()]
  [string]$Tap,

  [Parameter()]
  [switch]$NoProbe,

  [Parameter()]
  [switch]$ListOnly,

  [Parameter()]
  [switch]$Once
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function ConvertFrom-EnvValue {
  param([Parameter(Mandatory = $true)][AllowEmptyString()][string]$Value)

  $trimmed = $Value.Trim()
  if (($trimmed.StartsWith('"') -and $trimmed.EndsWith('"')) -or ($trimmed.StartsWith("'") -and $trimmed.EndsWith("'"))) {
    return $trimmed.Substring(1, $trimmed.Length - 2)
  }

  return $trimmed
}

function Read-BashEnvFile {
  param([Parameter(Mandatory = $true)][string]$Path)

  if (-not (Test-Path -LiteralPath $Path)) {
    throw "Missing env file: $Path"
  }

  $map = @{}
  foreach ($rawLine in (Get-Content -LiteralPath $Path)) {
    $line = ($rawLine + '').Trim()
    if (-not $line) { continue }
    if ($line.StartsWith('#')) { continue }

    $line = ($line -replace '\s+#.*$', '').Trim()
    if (-not $line) { continue }

    $match = [regex]::Match($line, '^(?<key>[A-Za-z_][A-Za-z0-9_]*)=(?<value>.*)$')
    if (-not $match.Success) { continue }

    $map[$match.Groups['key'].Value] = ConvertFrom-EnvValue -Value $match.Groups['value'].Value
  }

  return $map
}

function Get-PortFromListen {
  param([Parameter(Mandatory = $true)][string]$Listen)

  $trimmed = $Listen.Trim()
  if (-not $trimmed) {
    throw 'Gate listen address is empty.'
  }

  if ($trimmed -match ':(\d+)$') {
    return $Matches[1]
  }

  throw "Could not extract port from listen address '$Listen'."
}

function Get-GateUrl {
  param(
    [Parameter(Mandatory = $true)][string]$GateHost,
    [Parameter(Mandatory = $true)][string]$Listen
  )

  $listenValue = $Listen.Trim()
  if ($listenValue -match '^https?://') {
    return $listenValue.TrimEnd('/')
  }

  $port = Get-PortFromListen -Listen $listenValue
  return ("http://{0}:{1}" -f $GateHost.Trim(), $port)
}

function Get-GatesFromEnv {
  param([Parameter(Mandatory = $true)][string]$Path)

  $envMap = Read-BashEnvFile -Path $Path
  $raw = ''
  if ($envMap.ContainsKey('GATES') -and $envMap['GATES']) {
    $raw = $envMap['GATES']
  }

  if (-not $raw) {
    throw "No GATES entry found in $Path"
  }

  $gates = New-Object System.Collections.Generic.List[object]
  $index = 1
  foreach ($item in ($raw -split '\s+')) {
    if (-not $item) { continue }

    $parts = $item.Split(',')
    if ($parts.Count -ne 4) {
      throw "Bad GATES entry: $item"
    }

    $gateHost = $parts[0].Trim()
    $gate = $parts[1].Trim()
    $station = $parts[2].Trim()
    $listen = $parts[3].Trim()

    $gates.Add([pscustomobject]@{
      Index = $index
      Host = $gateHost
      GateID = $gate
      StationID = $station
      Listen = $listen
      Url = Get-GateUrl -GateHost $gateHost -Listen $listen
      Reachable = $null
      Health = ''
    })
    $index += 1
  }

  return $gates
}

function Test-GateHealth {
  param([Parameter(Mandatory = $true)]$Gate)

  try {
    $response = Invoke-WebRequest -Uri ($Gate.Url.TrimEnd('/') + '/health') -Method Get -TimeoutSec 2 -UseBasicParsing
    $Gate.Reachable = $response.StatusCode -ge 200 -and $response.StatusCode -lt 300
    $Gate.Health = "HTTP $($response.StatusCode)"
  } catch {
    $Gate.Reachable = $false
    if ($_.Exception.Response -and $_.Exception.Response.StatusCode) {
      $Gate.Health = "HTTP $([int]$_.Exception.Response.StatusCode)"
    } else {
      $Gate.Health = $_.Exception.Message
    }
  }
}

function Show-Gates {
  param([Parameter(Mandatory = $true)][System.Collections.IEnumerable]$Gates)

  $rows = foreach ($gate in $Gates) {
    [pscustomobject]@{
      Index = $gate.Index
      GateID = $gate.GateID
      StationID = $gate.StationID
      Host = $gate.Host
      Url = $gate.Url
      Reachable = if ($null -eq $gate.Reachable) { '?' } elseif ($gate.Reachable) { 'yes' } else { 'no' }
      Health = $gate.Health
    }
  }

  $rows | Format-Table -AutoSize | Out-Host
}

function Select-Gate {
  param(
    [Parameter(Mandatory = $true)][System.Collections.Generic.List[object]]$Gates,
    [Parameter()][string]$RequestedGateId,
    [Parameter()][string]$RequestedGateUrl
  )

  if ($RequestedGateUrl) {
    $normalized = $RequestedGateUrl.TrimEnd('/')
    $match = $Gates | Where-Object { $_.Url -eq $normalized } | Select-Object -First 1
    if ($match) {
      return $match
    }

    return [pscustomobject]@{
      Index = 0
      Host = ''
      GateID = if ($RequestedGateId) { $RequestedGateId } else { $normalized }
      StationID = ''
      Listen = $normalized
      Url = $normalized
      Reachable = $null
      Health = ''
    }
  }

  if ($RequestedGateId) {
    $match = $Gates | Where-Object { $_.GateID -eq $RequestedGateId } | Select-Object -First 1
    if (-not $match) {
      throw "Gate '$RequestedGateId' was not found in $EnvFile"
    }
    return $match
  }

  while ($true) {
    $choice = Read-Host 'Select gate number (or q to quit)'
    if ($choice -match '^[Qq]$') {
      return $null
    }

    $selectedIndex = 0
    if ([int]::TryParse($choice, [ref]$selectedIndex)) {
      $match = $Gates | Where-Object { $_.Index -eq $selectedIndex } | Select-Object -First 1
      if ($match) {
        return $match
      }
    }

    Write-Host 'Invalid selection.' -ForegroundColor Yellow
  }
}

function Read-TapInput {
  param(
    [Parameter()][string]$DefaultCardType,
    [Parameter()][string]$DefaultCardId,
    [Parameter()][string]$DefaultTap
  )

  if ($DefaultTap) {
    return $DefaultTap.Trim()
  }

  $cardType = $DefaultCardType
  if (-not $cardType) {
    $cardType = Read-Host 'Card type [OY/PCTR] (default OY)'
    if (-not $cardType) {
      $cardType = 'OY'
    }
  }

  $cardType = $cardType.Trim().ToUpperInvariant()
  if ($cardType -notin @('OY', 'PCTR')) {
    throw "Unsupported card type '$cardType'. Use OY or PCTR."
  }

  $cardId = $DefaultCardId
  if (-not $cardId) {
    $cardId = Read-Host 'Card ID'
  }

  $cardId = $cardId.Trim()
  if (-not $cardId) {
    throw 'Card ID is required.'
  }

  return "$cardType`:$cardId"
}

function Send-Tap {
  param(
    [Parameter(Mandatory = $true)]$Gate,
    [Parameter(Mandatory = $true)][string]$TapLine
  )

  $body = @{ tap = $TapLine } | ConvertTo-Json -Compress
  return Invoke-RestMethod -Uri ($Gate.Url.TrimEnd('/') + '/tap') -Method Post -ContentType 'application/json' -Body $body -TimeoutSec 5
}

$gates = Get-GatesFromEnv -Path $EnvFile
if (-not $NoProbe) {
  foreach ($gate in $gates) {
    Test-GateHealth -Gate $gate
  }
}

Show-Gates -Gates $gates

if ($ListOnly) {
  exit 0
}

while ($true) {
  $selectedGate = Select-Gate -Gates $gates -RequestedGateId $GateId -RequestedGateUrl $GateUrl
  if (-not $selectedGate) {
    break
  }

  $tapLine = Read-TapInput -DefaultCardType $CardType -DefaultCardId $CardId -DefaultTap $Tap
  Write-Host ("Sending {0} to {1} ({2})..." -f $tapLine, $selectedGate.GateID, $selectedGate.Url)

  try {
    $response = Send-Tap -Gate $selectedGate -TapLine $tapLine
    $allowed = $false
    if ($response.PSObject.Properties.Name -contains 'allowed') {
      $allowed = [bool]$response.allowed
    }
    $reason = ''
    if ($response.PSObject.Properties.Name -contains 'reason') {
      $reason = [string]$response.reason
    }

    Write-Host ("Result: allowed={0} reason={1}" -f $allowed, $reason)
  } catch {
    Write-Error ("Tap failed: {0}" -f $_.Exception.Message)
  }

  if ($Once -or $GateId -or $GateUrl -or $Tap -or ($CardType -and $CardId)) {
    break
  }

  $again = Read-Host 'Send another tap? [Y/n]'
  if ($again -match '^[Nn]') {
    break
  }
}