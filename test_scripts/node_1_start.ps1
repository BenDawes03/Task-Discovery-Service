# Change to project root
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent $scriptDir
Set-Location $projectRoot

$env:TDS_PROXY_LISTEN = ":5100"
if ("" -eq "") {
    .\client_proxy.exe -p2p -p2p-port :6000
} else {
    .\client_proxy.exe -p2p -p2p-port :6000 -bootstrap 
}
