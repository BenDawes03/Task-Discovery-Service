# TDS Live Demonstration Program

A polished, presentation-ready demonstration of the Task Discovery Service (TDS) that visually shows how the system works with step-by-step explanations and real-time interactions.

## What This Demo Shows

This interactive demonstration walks through:

1. **System Architecture** - Visual overview of how TDS works
2. **Server Connection** - Validates the TDS server is running
3. **Service Registration** - Registers multiple services with different tasks
4. **Service Discovery** - Queries TDS to find services by task name
5. **Round-Robin Load Balancing** - Shows how TDS distributes load evenly
6. **Concurrent Performance** - Demonstrates handling many simultaneous queries
7. **Summary Statistics** - Final metrics and success rates

## Features

✨ **Color-coded output** for easy visual tracking
📊 **Real-time statistics** showing operations as they happen  
⚡ **Performance metrics** including queries/second and latency
🎯 **Step-by-step flow** with pauses for explanation
🎨 **Professional formatting** suitable for presentations

## Prerequisites

1. **TDS Server must be running** on `localhost:5000`
   
   Start the server in a separate terminal:
   ```bash
   cd cmd/server
   go run main.go
   ```
   
   When prompted, select transport mode:
   - **TCP (option 2)** - RECOMMENDED for demo (default demo setting)
   - UDP (option 1) - Change `protocol` variable in main.go to "udp"
   - TLS (option 3) - Requires certificates
   
   Or build and run:
   ```bash
   go build -o bin/server.exe ./cmd/server
   ./bin/server.exe
   ```

2. **Protocol must match** between server and demo
   - The demo defaults to **TCP** mode
   - Make sure your server uses the same protocol
   - To change demo protocol: edit `protocol ="tcp"` in [cmd/demo/main.go](../../cmd/demo/main.go)

## Running the Demo

### Quick Start

```bash
# From repository root
go run ./cmd/demo
```

### Building the Demo

```bash
# Build standalone executable
go build -o bin/demo.exe ./cmd/demo

# Run it
./bin/demo.exe
```

### For PowerShell Users

Use the included helper script:
```powershell
.\demos\\centralised\\run_demo.ps1
```

## Demo Flow

The demonstration is **interactive** and pauses at each step. Press ENTER to advance to the next section.

### Step 1: Welcome Banner
Shows what the demo will cover

### Step 2: Architecture Diagram
ASCII art showing TDS system components and message flow

### Step 3: Server Check
Verifies the TDS server is running and accessible

### Step 4: Service Registration
Registers 5 services:
- 3× task_web (ports 8001-8003)
- 1× task_db (port 8004)
- 1× task_cache (port 8005)

### Step 5: Basic Queries
Demonstrates querying for different task types, including a non-existent one

### Step 6: Round-Robin Demo
Makes 9 requests for "task_web" showing how TDS cycles through the 3 registered services evenly

### Step 7: Concurrent Load
Runs 20 concurrent clients making 100 total queries to show performance

### Step 8: Summary
Final statistics and success metrics

## Customization

Edit `main.go` to customize:

- `serverAddr` - Change TDS server address (default: "localhost:5000")
- Number of services registered in `registerServices()`
- Number of concurrent clients in `demonstrateConcurrentQueries()`
- Query patterns and task names

## Output Example

```
╔════════════════════════════════════════════════════════════════╗
║                                                                ║
║         Task Discovery Service (TDS) - Live Demo              ║
║                                                                ║
╚════════════════════════════════════════════════════════════════╝

Welcome to the TDS demonstration!

This demo will show you:
  ✓ How services register with TDS
  ✓ How clients query for services
  ✓ Round-robin load balancing
  ✓ Concurrent query handling

Press ENTER to begin...
```

## Troubleshooting

### "Server not running!" Error

**Problem**: Demo cannot connect to TDS server

**Solution**: 
1. Open a new terminal
2. Navigate to `cmd/server`
3. Run `go run main.go`
4. Wait for "Server started" message
5. Return to demo and try again

### "Server not running!" Error

**Problem**: Demo cannot connect to TDS server

**Solution**: 
1. Open a new terminal
2. Navigate to `cmd/server`
3. Run `go run main.go`
4. **Select TCP mode (option 2)** when prompted
5. Wait for "Server started" message
6. Return to demo and try again

### Registrations Fail

**Problem**: Server check passes but all registrations fail

**Most likely cause**: Protocol mismatch!

**Solution**:
1. Check demo protocol setting in [cmd/demo/main.go](../../cmd/demo/main.go):
   ```go
   var (
       serverAddr = "localhost:5000"
       protocol   = "tcp" // Must match server!
   ```
2. Check what protocol your server is using:
   - Look at server window - it should show "mode=tcp" or "mode=udp"
   - Or check with: `netstat -an | findstr 5000`
     - TCP: Shows "TCP ... :5000 ... LISTENING"
     - UDP: Shows "UDP ... :5000 ... *:*"
3. Make them match:
   - Either restart server with correct protocol
   - Or change `protocol` in demo code and rebuild

### No Response from Server

**Problem**: Server is running but not responding

**Solution**:
- Check server is listening on correct port (5000)
- Verify no firewall blocking UDP traffic
- Check server logs for errors

### Demo Freezes

**Problem**: Demo stops responding

**Solution**:
- Press Ctrl+C to exit
- Restart the server
- Run demo again

## Technical Details

**Protocol**: TCP (default) or UDP
- Demo defaults to TCP for reliability
- Can be changed by modifying `protocol` variable
- Must match server configuration

**Default Port**: 5000

**Message Format** (JSON over TCP/UDP): 
```json
// Register
{"cmd": "REGISTER", "task": "task_web", "address": "localhost:8001"}

// Query  
{"cmd": "QUERY", "task": "task_web"}
```

## Using in Presentations

**Tips for live demonstrations:**

1. **Run full screen** for better visibility
2. **Increase terminal font size** before presenting
3. **Ensure server is pre-started** to avoid delays
4. **Practice the flow** to know what happens at each step
5. **Explain between pauses** what you just saw
6. **Have backup** - build the executable beforehand in case of network issues

## License

Part of the TDS (Task Discovery Service) project.
