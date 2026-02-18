$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent $scriptDir
Set-Location $projectRoot

$env:TDS_PROXY_LISTEN = ":5104"
if ("127.0.0.1:6000" -eq "") {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6004
} else {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6004 -bootstrap 127.0.0.1:6000
}
