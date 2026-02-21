param(
    [Parameter(Mandatory = $true)]
    [string]$ProxyListen,

    [Parameter(Mandatory = $true)]
    [string]$P2PListen,

    [string]$Bootstrap = "",

    [string]$LogFile = "client_proxy.log"
)

$ErrorActionPreference = "Stop"

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== client_proxy P2P start =====" | Out-File -FilePath $LogFile -Append
"[$timestamp] ProxyListen=$ProxyListen P2PListen=$P2PListen Bootstrap=$Bootstrap" | Out-File -FilePath $LogFile -Append

$env:TDS_PROXY_LISTEN = $ProxyListen

if (!(Test-Path "./client_proxy") -and (Test-Path "./client_proxy.exe")) {
    # allow running on Windows if needed
    $proxyBin = ".\\client_proxy.exe"
} else {
    $proxyBin = "./client_proxy"
}

if ($Bootstrap -and $Bootstrap.Trim() -ne "") {
    & $proxyBin -p2p -p2p-port $P2PListen -bootstrap $Bootstrap *>> $LogFile
} else {
    & $proxyBin -p2p -p2p-port $P2PListen *>> $LogFile
}
