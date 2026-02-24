# Simulation

This folder contains **separate programs** that can run on the same machine or different VMs.

- **Gate**:
  - OY: asks CS if the card has funds.
  - PCTR: encrypts card data with PA public key, asks PA for a token + yes/no.
  - If allowed, sends tap/transaction to the Station Computer.
- **Station Computer**: receives allowed taps, batches per card type, and forwards batches to CS/PCTRBO.
- **CS**: holds OY cards + transactions + station costs; constructs journeys every 5 minutes and debits funds.
- **PCTRBO**: holds token-based PCTR transactions + station costs; constructs journeys on tokens and reports debts to PA every 5 minutes.
- **PA**: holds PCTR private key + token/card mapping + outstanding debts; can generate a restitution file on command.

## Quick start (one machine)

Run each in a separate terminal from repo root.

### Prereq: Postgres for CS + PCTRBO + PA

CS (OY cards), PCTRBO, and PA each use their own Postgres database.

Example (Docker):

```powershell
# CS DB (OY cards)
docker run --name cs-db -e POSTGRES_PASSWORD=pass -e POSTGRES_DB=cs -p 5433:5432 -d postgres:16

# PCTRBO DB
docker run --name pctrbo-db -e POSTGRES_PASSWORD=pass -e POSTGRES_DB=pctrbo -p 5434:5432 -d postgres:16

# PA DB
docker run --name pa-db -e POSTGRES_PASSWORD=pass -e POSTGRES_DB=pa -p 5435:5432 -d postgres:16
```

DSNs:
- CS: `postgres://postgres:pass@localhost:5433/cs?sslmode=disable`
- PCTRBO: `postgres://postgres:pass@localhost:5434/pctrbo?sslmode=disable`
- PA: `postgres://postgres:pass@localhost:5435/pa?sslmode=disable`

### 0) TDS server (centralized registry)

```powershell
go run ./cmd/server -port 5000
```

### 0.1) Client proxy (local discovery endpoint)

```powershell
go run ./cmd/client_proxy
```

Defaults:
- server listens on `:5000`
- proxy listens on `:5100` and forwards to `127.0.0.1:5000`

### 1) CS (OY cards)

```powershell
$env:CS_DB_DSN = "postgres://postgres:pass@localhost:5433/cs?sslmode=disable"
go run ./Simulation/cs -listen :9101 -proxy localhost:5100
```

### 2) PCTRBO

```powershell
$env:PCTRBO_DB_DSN = "postgres://postgres:pass@localhost:5434/pctrbo?sslmode=disable"
go run ./Simulation/pctrbo -listen :9102 -proxy localhost:5100
```

### 2.5) PA (PCTR token authority)

PA needs the RSA private key; the Gate needs the public key.

Example key generation (requires `openssl`):

```powershell
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out pctr_private.pem
openssl rsa -in pctr_private.pem -pubout -out pctr_public.pem
```

Run PA:

```powershell
$env:PA_DB_DSN = "postgres://postgres:pass@localhost:5435/pa?sslmode=disable"
go run ./Simulation/pa -listen :9103 -proxy localhost:5100 -private-key .\pctr_private.pem
```

### 3) Station Computer

```powershell
go run ./Simulation/station_computer -listen :9100 -proxy localhost:5100
```

Optional env vars:
- `BATCH_SIZE` (default `20`)
- `BATCH_FLUSH_SECONDS` (default `30`)

### 4) Gate

```powershell
go run ./Simulation/gate -id gate-1 -proxy localhost:5100 -pctr-public-key .\\pctr_public.pem -listen :9200
```

Then type taps in the gate terminal:
- `OY:1001`
- `PCTR:2001`

Or send taps over HTTP to the gate listener:

```powershell
# plain text body
curl -X POST http://localhost:9200/tap -d "OY:1001"

# JSON body
curl -X POST http://localhost:9200/tap -H "Content-Type: application/json" -d '{"tap":"PCTR:2001"}'
```

(CTRL+C to stop)

## Notes

- Service discovery:
  - CS registers as task `sim.cs`
  - PCTRBO registers as task `sim.pctrbo`
  - PA registers as task `sim.pa`
  - Each Station Computer registers as task `station-Computer-<station-id>` (example: `station-Computer-station-1`)
  - Gate queries these tasks via the local client proxy (`localhost:5100` by default)

- Gate allow/deny logic:
  - OY: CS `/validate`.
  - PCTR: encrypt card data → PA `/tokenize` (returns yes/no and a token).
  - Only allowed taps are sent to the Station Computer.

- Station selection:
  - Each gate is booted with `-station-id`.
  - By default it discovers its station via task `station-Computer-<station-id>`.

- Station Computer batching:
  - Receives allowed taps from gates.
  - Buffers per `card_type`.
  - When it has 5 OY taps, it forwards them to OYBO `POST /batch`.
  - When it has 5 PCTR taps, it forwards them to PCTRBO `POST /batch`.
  - Station Computer overwrites/sets `station_id` on each forwarded transaction.

## Running across VMs

- Run `cmd/server` on one VM (or a host) reachable by all VMs.
- Run `cmd/client_proxy` on each VM and set `TDS_SERVER_ADDR` to point at the server:

```powershell
$env:TDS_SERVER_ADDR = "<SERVER_IP>:5000"
go run ./cmd/client_proxy
```

- When running OYBO/PCTRBO/Station on a VM, set `-advertise` to a URL reachable from other VMs (not `localhost`):

```powershell
go run ./Simulation/oybo -listen :9101 -proxy localhost:5100 -advertise http://<OYBO_VM_IP>:9101
```

### SSH orchestrator

If you can SSH to all VMs and each VM has this repo checked out, you can use the included orchestrator:

1) Copy and edit the env file:

```bash
cp Simulation/orchestrate.env.example Simulation/orchestrate.env
${EDITOR:-nano} Simulation/orchestrate.env
```

If SSH usernames differ per VM, set `SSH_USER_DEFAULT` and/or `SSH_USERS` in `Simulation/orchestrate.env`.

2) Start everything:

```bash
./Simulation/orchestrate.sh up
```

Optional: sync code to all VMs first:

```bash
./Simulation/orchestrate.sh sync
./Simulation/orchestrate.sh up
```

Or set `SYNC_BEFORE_UP=1` in `Simulation/orchestrate.env` to auto-sync before `up`.

3) Tail logs on a VM:

```bash
./Simulation/orchestrate.sh logs <host> <name>
```
