$env:TDS_PROXY_LISTEN = ":5102"
if ("127.0.0.1:6000" -eq "") {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6002
} else {
    .\cmd\client_proxy\client_proxy.exe -p2p -p2p-port :6002 -bootstrap 127.0.0.1:6000
}
