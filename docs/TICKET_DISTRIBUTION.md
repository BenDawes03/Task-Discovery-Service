# Ticket Distribution System — Setup & Testing Guide

## Overview

The ticket distribution system enables CS (Card Services) to generate ticket manifests at regular intervals and notify a ticket distributor, which then polls CS for new manifests and writes per-station CSV files for station computers (SCS) to fetch.

### Architecture

```
┌──────────────────────────────────────────────────────────────────┐
│ CS (Card Services)                                                │
│ ├─ /ticket POST: insert a ticket (train_time, station, passenger)│
│ ├─ /manifests GET?since=: poll manifests generated after time    │
│ ├─ /manifest GET?id=: fetch one manifest by id                   │
│ └─ Manifest Generator (every 5 min by default):                  │
│    • Finds new tickets added since last manifest                 │
│    • Builds list of active tickets (train_time in next 15 min)   │
│    • Stores manifest JSON in DB (manifests table)                │
└──────────────────────────────────────────────────────────────────┘
                             ↓ (HTTP polling)
┌──────────────────────────────────────────────────────────────────┐
│ Ticket Distributor                                                │
│ ├─ Polls CS /manifests every 30 seconds (configurable)           │
│ ├─ For each manifest:                                             │
│ │  • Groups tickets by station_id                                │
│ │  • Writes manifest_<id>_<station>.csv files                    │
│ └─ /file GET?manifest_id=&station=: serves CSV files to SCS      │
└──────────────────────────────────────────────────────────────────┘
                             ↓ (HTTP fetch)
                   SCS retrieves CSV files
```

## Setup & Quick Start

### Prerequisites

- PostgreSQL running with a database for the TDS simulation
- Go 1.20+
- PowerShell 5.1+ (for seed_tickets.ps1)

### Build

From the repo root:

```powershell
go build ./Simulation/cs
go build ./Simulation/ticketdistributor
```

Binaries are created in the respective directories (or in the root, depending on your build setup).

### Step 1: Start CS

Example (adjust DATABASE_URL or -db flag with your Postgres DSN):

```powershell
$env:DATABASE_URL = "postgres://user:pass@localhost:5432/tds?sslmode=disable"
.\Simulation\cs\cs.exe -listen :9101 -ticket-interval 5m
```

**Flags:**
- `-listen :9101` — HTTP listen address
- `-ticket-interval 5m` — how often to generate and store manifests (default 5 minutes)
- `-proxy localhost:5100` — simproxy address for service discovery
- `-db <DSN>` — Postgres connection string (or use DATABASE_URL env var)

CS will:
- Create necessary schema tables (cards, transactions, tickets, manifests)
- Start a manifest generation goroutine
- Expose HTTP endpoints: `/health`, `/validate`, `/batch`, `/ticket`, `/manifests`, `/manifest`

### Step 2: Start Ticket Distributor

Example:

```powershell
.\Simulation\ticketdistributor\ticketdistributor.exe -listen :9110 -cs-base http://localhost:9101 -outdir .\manifests -poll-interval 30s
```

**Flags:**
- `-listen :9110` — HTTP listen address
- `-cs-base http://localhost:9101` — direct CS URL (overrides proxy-based discovery)
- `-cs-task sim.cs` — task name for CS discovery via proxy (if -cs-base not set)
- `-outdir .\manifests` — where to write per-station CSV files
- `-poll-interval 30s` — how often to poll CS for new manifests (default 30 seconds)
- `-proxy localhost:5100` — simproxy address
- `-task sim.ticketdistributor` — task name to register under with proxy

Distributor will:
- Register with simproxy (if available)
- Start a polling loop every 30 seconds
- Query CS for new manifests and write CSV files

### Step 3: Seed Tickets (Continuous Flow)

Use the provided PowerShell script to continuously insert tickets:

```powershell
.\scripts\seed_tickets.ps1 -CSBaseUrl "http://localhost:9101" -IntervalSeconds 300 -TicketsPerBatch 5 -DurationMinutes 120
```

**Script Parameters:**
- `-CSBaseUrl` — CS endpoint (default http://localhost:9101)
- `-IntervalSeconds` — sleep between batches (default 300 = 5 minutes)
- `-TicketsPerBatch` — tickets per batch (default 5)
- `-DurationMinutes` — total runtime (default 120 minutes for testing)

The script:
- Inserts tickets with `train_time` 1–14 minutes from now
- Runs every 5 minutes (or custom interval)
- Each ticket goes to a random station (station-1, station-2, or station-3)
- Logs progress to console

**Example: Quick Test (2 minutes of seeding)**
```powershell
.\scripts\seed_tickets.ps1 -CSBaseUrl "http://localhost:9101" -DurationMinutes 2 -TicketsPerBatch 3
```

### Step 4: Verify Manifests & CSV Files

While seeding is running, check manifests on CS:

```powershell
# List all manifests
Invoke-RestMethod -Method Get "http://localhost:9101/manifests"

# Fetch one manifest
Invoke-RestMethod -Method Get "http://localhost:9101/manifest?id=m-1708866000"
```

Check distributor-generated CSV files:

```powershell
# List CSV files in output directory
Get-ChildItem .\manifests\manifest_*.csv

# View a CSV
Get-Content .\manifests\manifest_m-1708866000_station-1.csv
```

Or fetch via HTTP:

```powershell
Invoke-RestMethod -Method Get "http://localhost:9110/file?manifest_id=m-1708866000&station=station-1" -OutFile .\downloaded.csv
Get-Content .\downloaded.csv
```

## Database Schema

### `manifests` Table (CS)

```sql
CREATE TABLE manifests (
    id TEXT PRIMARY KEY,
    generated_at TIMESTAMPTZ NOT NULL,
    payload_json JSONB NOT NULL
);
CREATE INDEX idx_manifests_generated_at ON manifests(generated_at);
```

- **id**: manifest identifier (e.g., `m-1708866000`)
- **generated_at**: timestamp when manifest was generated
- **payload_json**: full manifest JSON: `{id, generated_at, tickets[{id, station_id, train_time, passenger}]}`

### `tickets` Table (CS)

```sql
CREATE TABLE tickets (
    id BIGSERIAL PRIMARY KEY,
    station_id TEXT NOT NULL,
    train_time TIMESTAMPTZ NOT NULL,
    passenger TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active BOOLEAN NOT NULL DEFAULT TRUE
);
CREATE INDEX idx_tickets_train_time ON tickets(train_time);
CREATE INDEX idx_tickets_created_at ON tickets(created_at);
```

- **id**: ticket ID (auto-increment)
- **station_id**: destination station
- **train_time**: when the train departs
- **passenger**: passenger name
- **created_at**: when ticket was created
- **active**: whether ticket is still valid

## HTTP Endpoints

### CS Endpoints

#### POST /ticket
Insert a new ticket.

**Request:**
```json
{
  "station_id": "station-1",
  "train_time": "2026-02-24T12:10:00.000000Z",
  "passenger": "Alice"
}
```

**Response:**
```json
{
  "ok": true,
  "id": 1
}
```

#### GET /manifests?since=<rfc3339nano>
Fetch manifests generated after a given time (or all if no `since`).

**Response:**
```json
[
  {
    "id": "m-1708866000",
    "generated_at": "2026-02-24T12:00:00Z",
    "payload": {
      "id": "m-1708866000",
      "generated_at": "2026-02-24T12:00:00Z",
      "tickets": [
        {
          "id": 1,
          "station_id": "station-1",
          "train_time": "2026-02-24T12:10:00.000000Z",
          "passenger": "Alice-0"
        }
      ]
    }
  }
]
```

#### GET /manifest?id=<id>
Fetch one manifest by ID.

**Response:**
```json
{
  "id": "m-1708866000",
  "generated_at": "2026-02-24T12:00:00Z",
  "payload": { ... }
}
```

### Distributor Endpoints

#### GET /file?manifest_id=<id>&station=<station>
Download the CSV file for a manifest/station.

**Response:** CSV with columns: `ticket_id, passenger, train_time`

## Troubleshooting

### Distributor not finding new manifests

1. **Check CS is running and accessible:**
   ```powershell
   Invoke-RestMethod -Method Get "http://localhost:9101/health"
   ```

2. **Check manifests exist in CS:**
   ```powershell
   Invoke-RestMethod -Method Get "http://localhost:9101/manifests"
   ```

3. **Verify CS is inserting tickets via seed script logs.**

4. **Check distributor logs** — look for `manifest polled id=...` messages.

### No CSV files being written

1. **Check distributor -outdir exists and is writable:**
   ```powershell
   Test-Path .\manifests
   ```

2. **Check distributor polling is running** — should log every 30s (or custom poll interval).

3. **Verify manifest payload contains tickets** — inspect CS `/manifests` response.

### High database latency

- Consider increasing context timeout in CS/distributor code if DB is slow.
- Add indexes on `tickets.train_time` and `manifests.generated_at` (already present in schema).

## Example: End-to-End Test (5 minutes)

1. **Terminal 1 — Start CS:**
   ```powershell
   $env:DATABASE_URL = "postgres://user:pass@localhost:5432/tds?sslmode=disable"
   .\Simulation\cs\cs.exe -listen :9101 -ticket-interval 5m
   ```

2. **Terminal 2 — Start Distributor:**
   ```powershell
   .\Simulation\ticketdistributor\ticketdistributor.exe -listen :9110 -cs-base http://localhost:9101 -outdir .\manifests
   ```

3. **Terminal 3 — Seed tickets for 2 minutes:**
   ```powershell
   .\scripts\seed_tickets.ps1 -CSBaseUrl "http://localhost:9101" -DurationMinutes 2 -TicketsPerBatch 3
   ```

4. **While seeding, monitor in Terminal 1/2:**
   - Terminal 1 logs should show: `manifest stored: id=m-... tickets=...`
   - Terminal 2 logs should show: `manifest polled id=m-... stations=...`

5. **After 2 minutes, verify CSV files:**
   ```powershell
   Get-ChildItem .\manifests\manifest_*.csv | Select -First 3
   ```

## Future Enhancements

- **SCS integration:** extend SCS to fetch CSV files via distributor `/file` endpoint
- **Manifest retention:** add cleanup of old manifests (older than X days)
- **Compression:** add gzip support for CSV transfer
- **Signatures:** add cryptographic signatures to manifests for security
- **Rate limiting:** add per-IP rate limits to prevent abuse
