# TDS Test Environment (VM Suites)

This folder contains VM-oriented test suites for TDS.

## Suites

- `centralised/` — legacy/centralised registry tests (dummy services + client queries + SSH orchestrator).
- `p2p/` — peer-to-peer (DHT) tests using 3 VMs running `client_proxy` in P2P mode, then bulk REGISTER/QUERY load.

## Quick start

- Centralised: see `centralised/README.md`
- P2P: see `p2p/README.md`
