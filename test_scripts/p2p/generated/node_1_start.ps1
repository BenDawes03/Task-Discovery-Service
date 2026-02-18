$env:TDS_PROXY_LISTEN = ":5100"
if ("" -eq "") {
    .\bin\client_proxy.exe -p2p -p2p-port :6000
} else {
    .\bin\client_proxy.exe -p2p -p2p-port :6000 -bootstrap 
}
