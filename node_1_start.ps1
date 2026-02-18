$env:TDS_PROXY_LISTEN = ":5100"
if ("" -eq "") {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6000
} else {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6000 -bootstrap 
}
