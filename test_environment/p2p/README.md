# TDS P2P (DHT) VM Test Suite

This suite stands up a **3-node P2P network** (DHT) using `cmd/client_proxy` in `-p2p` mode on three VMs, then performs **bulk service registrations** and **high-volume queries** via each node’s local proxy.

## Requirements

- 3 Linux VMs reachable via SSH from your control machine
- `pwsh` available on the VMs (PowerShell 7)
- Control machine (this repo) has: `go`, `ssh`, `scp`

## Files

- `orchestrator.ps1` — control-machine script: deploys binaries/scripts, starts 3 proxies, runs register+query load, and collects summary stats.
- `start_client_proxy_p2p.ps1` — runs a single client proxy in P2P mode on a VM.
- `p2p_register_load.ps1` — bulk REGISTER load against the local proxy (plaintext protocol).
- `p2p_query_load.ps1` — concurrent QUERY load against the local proxy (plaintext protocol).

## Run

From the repo root on your control machine:

```powershell
pwsh -File .\test_environment\p2p\orchestrator.ps1 \
  -NodeVMs @("192.168.0.180","192.168.0.181","192.168.0.182") \
  -Username "user" \
  -ServicesPerNode 20000 \
  -NumTasks 500 \
  -QueryThreadsPerNode 20 \
  -QueriesPerThread 20000
```

Cleanup (kills remote proxies and load scripts):

```powershell
pwsh -File .\test_environment\p2p\orchestrator.ps1 -CleanupOnly
```

## Notes

- Registrations/queries use the **client proxy plaintext protocol**:
  - `REGISTER <task> <address>`
  - `QUERY <task>`
- This suite registers *addresses only* (no service processes are started). It’s meant to stress the DHT store/lookup path.
