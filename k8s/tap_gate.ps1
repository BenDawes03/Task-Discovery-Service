<#
Tap helper for Kubernetes-deployed gates.

Features:
- Discovers gate services from Kubernetes (namespace default: tds-simulation)
- Builds tap URLs from NodePort services (default host: localhost)
- Optional health probe (/health)
- Interactive or one-shot tap POST to /tap

Examples:
  .\k8s\tap_gate.ps1
  .\k8s\tap_gate.ps1 -ListOnly
  .\k8s\tap_gate.ps1 -Service gate-2 -Tap "OY:card-123" -Once
  .\k8s\tap_gate.ps1 -NodeHost 192.168.65.3 -Service gate -CardType PCTR -CardId token-1 -Once
  .\k8s\tap_gate.ps1 -GateUrl http://localhost:31462 -Tap "OY:card-999" -Once
#>

[CmdletBinding()]
param(
  [Parameter()]
  [string]$Namespace = "tds-simulation",

  [Parameter()]
  [string]$Service,

  [Parameter()]
  [string]$GateUrl,

  [Parameter()]
  [string]$NodeHost = "localhost",

  [Parameter()]
  [ValidateSet("OY", "PCTR")]
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
$ErrorActionPreference = "Stop"

function Get-Kubectl {
  $cmd = Get-Command kubectl -ErrorAction SilentlyContinue
  if (-not $cmd) {
    throw "kubectl was not found in PATH."
  }
}

function Get-GateServices {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Namespace,
    [Parameter(Mandatory = $true)]
    [string]$NodeHost
  )

  $json = kubectl get svc -n $Namespace -o json
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to read services from namespace '$Namespace'."
  }

  $obj = $json | ConvertFrom-Json
  $rows = New-Object System.Collections.Generic.List[object]

  foreach ($svc in $obj.items) {
    $name = [string]$svc.metadata.name
    if ($name -notmatch '^gate(?:-\d+)?$') {
      continue
    }

    $nodePort = $null
    foreach ($port in $svc.spec.ports) {
      if ($null -ne $port.nodePort) {
        if ($port.port -eq 9200) {
          $nodePort = [int]$port.nodePort
          break
        }
        if ($null -eq $nodePort) {
          $nodePort = [int]$port.nodePort
        }
      }
    }

    if ($null -eq $nodePort) {
      continue
    }

    $rows.Add([pscustomobject]@{
      ServiceName = $name
      Namespace   = $Namespace
      NodePort    = $nodePort
      Url         = "http://$NodeHost`:$nodePort"
      Reachable   = $null
      Health      = ""
    })
  }

  return @($rows | Sort-Object ServiceName)
}

function Test-GateHealth {
  param([Parameter(Mandatory = $true)]$Gate)

  try {
    $resp = Invoke-WebRequest -Uri ($Gate.Url.TrimEnd('/') + '/health') -Method Get -TimeoutSec 2 -UseBasicParsing
    $Gate.Reachable = ($resp.StatusCode -ge 200 -and $resp.StatusCode -lt 300)
    $Gate.Health = "HTTP $($resp.StatusCode)"
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
  param([Parameter(Mandatory = $true)][object[]]$Gates)

  $i = 1
  $rows = foreach ($g in $Gates) {
    [pscustomobject]@{
      Index     = $i
      Service   = $g.ServiceName
      Namespace = $g.Namespace
      Url       = $g.Url
      Reachable = if ($null -eq $g.Reachable) { "?" } elseif ($g.Reachable) { "yes" } else { "no" }
      Health    = $g.Health
    }
    $i += 1
  }

  if ($rows.Count -eq 0) {
    Write-Host "No gate NodePort services found in namespace '$Namespace'." -ForegroundColor Yellow
    return
  }

  $rows | Format-Table -AutoSize | Out-Host
}

function Select-Gate {
  param(
    [Parameter(Mandatory = $true)][object[]]$Gates,
    [Parameter()][string]$RequestedService,
    [Parameter()][string]$RequestedUrl
  )

  if ($RequestedUrl) {
    $normalized = $RequestedUrl.TrimEnd('/')
    $match = $Gates | Where-Object { $_.Url -eq $normalized } | Select-Object -First 1
    if ($match) {
      return $match
    }

    return [pscustomobject]@{
      ServiceName = if ($RequestedService) { $RequestedService } else { "custom" }
      Namespace   = $Namespace
      NodePort    = $null
      Url         = $normalized
      Reachable   = $null
      Health      = ""
    }
  }

  if ($RequestedService) {
    $match = $Gates | Where-Object { $_.ServiceName -eq $RequestedService } | Select-Object -First 1
    if (-not $match) {
      throw "Service '$RequestedService' was not found in namespace '$Namespace'."
    }
    return $match
  }

  while ($true) {
    $choice = Read-Host "Select gate number (or q to quit)"
    if ($choice -match '^[Qq]$') {
      return $null
    }

    $idx = 0
    if ([int]::TryParse($choice, [ref]$idx)) {
      if ($idx -ge 1 -and $idx -le $Gates.Count) {
        return $Gates[$idx - 1]
      }
    }

    Write-Host "Invalid selection." -ForegroundColor Yellow
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

  $kind = $DefaultCardType
  if (-not $kind) {
    $kind = Read-Host "Card type [OY/PCTR] (default OY)"
    if (-not $kind) {
      $kind = "OY"
    }
  }

  $kind = $kind.Trim().ToUpperInvariant()
  if ($kind -notin @("OY", "PCTR")) {
    throw "Unsupported card type '$kind'. Use OY or PCTR."
  }

  $id = $DefaultCardId
  if (-not $id) {
    $id = Read-Host "Card ID"
  }

  $id = $id.Trim()
  if (-not $id) {
    throw "Card ID is required."
  }

  return "$kind`:$id"
}

function Send-Tap {
  param(
    [Parameter(Mandatory = $true)]$Gate,
    [Parameter(Mandatory = $true)][string]$TapLine
  )

  $body = @{ tap = $TapLine } | ConvertTo-Json -Compress
  return Invoke-RestMethod -Uri ($Gate.Url.TrimEnd('/') + '/tap') -Method Post -ContentType 'application/json' -Body $body -TimeoutSec 5
}

Get-Kubectl
$gates = Get-GateServices -Namespace $Namespace -NodeHost $NodeHost

if (-not $NoProbe) {
  foreach ($g in $gates) {
    Test-GateHealth -Gate $g
  }
}

Show-Gates -Gates $gates

if ($ListOnly) {
  exit 0
}

if (-not $GateUrl -and $gates.Count -eq 0) {
  throw "No tap targets found. Use -GateUrl or expose gate services as NodePort."
}

while ($true) {
  $selected = Select-Gate -Gates $gates -RequestedService $Service -RequestedUrl $GateUrl
  if (-not $selected) {
    break
  }

  $tapLine = Read-TapInput -DefaultCardType $CardType -DefaultCardId $CardId -DefaultTap $Tap
  Write-Host ("Sending {0} to {1} ({2})..." -f $tapLine, $selected.ServiceName, $selected.Url)

  try {
    $resp = Send-Tap -Gate $selected -TapLine $tapLine
    $allowed = $false
    if ($resp.PSObject.Properties.Name -contains 'allowed') {
      $allowed = [bool]$resp.allowed
    }
    $reason = ''
    if ($resp.PSObject.Properties.Name -contains 'reason') {
      $reason = [string]$resp.reason
    }

    Write-Host ("Result: allowed={0} reason={1}" -f $allowed, $reason)
  } catch {
    Write-Error ("Tap failed: {0}" -f $_.Exception.Message)
  }

  if ($Once -or $Service -or $GateUrl -or $Tap -or ($CardType -and $CardId)) {
    break
  }

  $again = Read-Host "Send another tap? [Y/n]"
  if ($again -match '^[Nn]') {
    break
  }
}
