# Copilot instructions for TDS

Quick summary
- This repository implements a task-to-IP registry service (server) with a TUI. Key components:
  - [cmd/server/main.go](cmd/server/main.go): TUI entrypoint and server wiring.
  - [pkg/registry/registry.go](pkg/registry/registry.go): `Registry` interface, `ServiceEntry`, `Stats` types.
  - [pkg/registry/memory.go](pkg/registry/memory.go): in-memory `MemoryRegistry` (mutexes, round-robin, query counters).
  - [pkg/transport/udp_server.go](pkg/transport/udp_server.go): transport layer (currently unimplemented).
  - [pkg/client/client.go](pkg/client/client.go): client helpers (currently empty).

Big-picture guidance for code-writing agents
- Use the `Registry` abstraction in [pkg/registry/registry.go](pkg/registry/registry.go) when modifying or extending registry behaviour.
- `MemoryRegistry` is the canonical in-memory implementation. It uses a `sync.RWMutex` and stores `map[string][]ServiceEntry` per task.
- Concurrency rules:
  - Readers that only inspect state should use `RLock`/`RUnlock`.
  - `GetService` mutates per-entry counters and the per-task `roundRobinIndex`, so it currently requires a write lock.
  - `ListServices()` must return a deep copy of the map/slices to avoid races with the TUI.

Project-specific behaviours to preserve
- Round-robin selection: `MemoryRegistry` keeps a `roundRobinIndex` per task and increments it after each `GetService` call. Preserve this when adding new store implementations.
- Query accounting: `GetService` increments `ServiceEntry.QueryCount` and `MemoryRegistry.totalQueries`. Keep these counters if adding metrics.
- Cleanup semantics: `Cleanup(timeout time.Duration)` should remove entries whose `LastHeartbeat` is older than `timeout` and return the number removed. `ServiceEntry.LastHeartbeat` is the authoritative liveness timestamp.

Build / run / debug
- Build the project from repo root:
```powershell
go build ./...
```
- Run the server (from repo root):
```powershell
go run ./cmd/server
```
- For race detection while running the server:
```powershell
go run -race ./cmd/server
```
- Module name: `go.mod` declares `module tds`. Fix any incorrect imports (for example `cmd/server/main.go` currently imports `TDS/...` with the wrong case).

Where to look first when editing
- Start with [pkg/registry/memory.go](pkg/registry/memory.go) — it contains the main in-memory logic and concurrency considerations.
- Review [cmd/server/main.go](cmd/server/main.go) to see UI wiring and where the `Registry` is expected to be provided.
- Implement transport logic in [pkg/transport/udp_server.go](pkg/transport/udp_server.go) and client helpers in [pkg/client/client.go](pkg/client/client.go). These files are currently empty; coordinate any protocol decisions with the maintainer before inventing formats.

Style & small rules
- Keep package import paths lower-case and matching `module tds` (e.g., `tds/pkg/registry`).
- Prefer returning explicit errors for not-found cases rather than `"", nil` (e.g., define `var ErrNotFound = errors.New("service not found")`).
- Avoid holding write locks for long-running operations (network IO); copy required state under lock and release before IO.

If unclear
- If you need the wire protocol for registration/queries, ask: transport and client files are empty so there is no canonical protocol in-repo.

Next actions for this agent (suggested)
- Implement `Register` and `Cleanup` in `pkg/registry/memory.go` following the concurrency rules above.
- Implement a minimal transport in `pkg/transport/udp_server.go` that delegates to the `Registry` once the protocol is agreed.
- Fix import casing in `cmd/server/main.go` so it builds.

If any of these areas are incomplete or you want me to implement a protocol or a storage backend, tell me which piece to do next.
