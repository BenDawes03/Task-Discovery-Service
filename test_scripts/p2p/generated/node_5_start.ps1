$env:TDS_PROXY_LISTEN = ":5104"
if ("127.0.0.1:6000" -eq "") {
    .\bin\client_proxy.exe -p2p -p2p-port :6004
} else {
    .\bin\client_proxy.exe -p2p -p2p-port :6004 -bootstrap 127.0.0.1:6000
}
