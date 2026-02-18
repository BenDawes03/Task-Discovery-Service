$env:TDS_PROXY_LISTEN = ":5103"
if ("127.0.0.1:6000" -eq "") {
    .\bin\client_proxy.exe -p2p -p2p-port :6003
} else {
    .\bin\client_proxy.exe -p2p -p2p-port :6003 -bootstrap 127.0.0.1:6000
}
