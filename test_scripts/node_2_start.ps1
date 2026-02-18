$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent $scriptDir
Set-Location $projectRoot

$env:TDS_PROXY_LISTEN = ":5101"
if ("127.0.0.1:6000" -eq "") {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6001
} else {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6001 -bootstrap 127.0.0.1:6000
}
