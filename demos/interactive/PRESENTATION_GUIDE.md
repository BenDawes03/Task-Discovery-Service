# TDS Presentation Guide

Quick reference for delivering a live demonstration of the Task Discovery Service.

## Pre-Presentation Checklist

### 5 Minutes Before
- [ ] Build demo executable: `go build -o bin/demo.exe ./cmd/demo`
- [ ] Build server executable: `go build -o bin/server.exe ./cmd/server`
- [ ] Test run both to verify they work
- [ ] **Verify protocol**: Demo uses TCP - confirm server will use TCP mode too
- [ ] Close unnecessary applications
- [ ] **Increase terminal font size** (18-24pt for projectors)
- [ ] Set terminal to maximum window size
- [ ] Have backup slides ready (in case of technical issues)

### Just Before Starting
- [ ] Open two terminal windows:
  - Terminal 1: Server (will run in background)
  - Terminal 2: Demo (main presentation window)
- [ ] Clear both terminal screens
- [ ] Navigate both to correct directories

## Presentation Script

### Opening (30 seconds)

**What to say:**
> "Today I'll demonstrate the Task Discovery Service - a system for service registration and discovery. This is a live demonstration showing real interactions."

**Action:**
- Show Terminal 2 (demo window)

---

### Step 1: Start the Server (1 minute)

**What to say:**
> "First, let me start the TDS server. This is the central registry that will coordinate between services and clients."

**Action in Terminal 1:**
```powershell
cd cmd\server
.\..\bin\server.exe
# When prompted: Select transport mode: 1) udp  2) tcp  3) tls
# IMPORTANT: Enter 2 for TCP (demo uses TCP mode)
```

**What to say:**
> "The server is now running using TCP and listening on port 5000. You can see its TUI here showing the empty registry."

**Optional:** Briefly show the TUI interface, explain the sections

---

### Step 2: Run the Demo (5-7 minutes)

**What to say:**
> "Now I'll run our demonstration program which will interact with this server."

**Action in Terminal 2:**
```powershell
cd cmd\demo
.\..\bin\demo.exe
```

**Alternative (launcher script):**
```powershell
# From repo root
.\demos\interactive\run_demo.ps1
```

#### At Each Pause Point:

**Welcome Screen:**
- Press ENTER quickly
- "This shows what we'll be demonstrating"

**Architecture Diagram:**
- **SPEND TIME HERE** - explain the diagram
- Point out: Services, Server, Clients
- Explain: REGISTER, QUERY, HEARTBEAT flows
- "The server maintains a registry of services, organized by task type"

**Server Check:**
- Press ENTER
- "Verifying connection to the server we just started"

**Service Registration:**
- Watch as each service registers
- "Five services are registering - notice we have 3 web services, which will demonstrate load balancing"
- Point out the task names
- Switch to Terminal 1 briefly to show services appearing in the TUI

**Basic Queries:**
- Watch the queries
- "Now clients are querying for services by task name"
- Point out the successful and failed (nonexistent task) query
- Switch to Terminal 1 to show query counters incrementing

**Round-Robin:**
- **KEY DEMONSTRATION**
- "This shows our load balancing. Notice how TDS cycles through the three web services evenly"
- Point out the pattern: service 1, 2, 3, 1, 2, 3...
- "The distribution chart shows each service got exactly 3 requests"

**Concurrent Performance:**
- "Now we stress test with 20 concurrent clients"
- Let it complete
- Highlight the metrics: queries/second, success rate
- "Even with concurrent load, the server maintains high performance"

**Summary:**
- Review the key statistics
- "Perfect success rate demonstrates reliability"
- Point out key features demonstrated

---

### Step 3: Show Server Stats (1 minute)

**What to say:**
> "Let's check the server to see what it recorded."

**Action:**
- Switch to Terminal 1 (server TUI)
- Show the registry with all services
- Show the statistics (total queries, registrations)
- "The server tracked everything we just did"

**Optional - Show Filtering (if time permits):**
- In server TUI, press 'f' to filter
- Type "task_web" to show only web services
- "The TUI allows filtering to focus on specific tasks"
- Press ESC to clear filter

---

### Closing (30 seconds)

**What to say:**
> "This demonstrates the core functionality of TDS: services can register themselves, clients can discover them by task type, and the system automatically load balances using round-robin. The server operates efficiently even under concurrent load."

**Final action:**
- Press 'q' in server to quit gracefully
- Show it saves state and exits cleanly

---

## Handling Q&A

### Common Questions & Answers

**Q: "What protocols does it support?"**
> A: "Currently UDP, TCP, and TLS. The demo used UDP for speed, but production deployments can use TLS for security."

**Q: "What happens if a service goes down?"**
> A: "Services must send heartbeats every 30 seconds. If they miss heartbeats, the server removes them from the registry automatically. Let me show you in the code..." [Open cleanup code if time permits]

**Q: "How does it handle thousands of services?"**
> A: "The in-memory registry is optimized with Go's concurrent maps and mutexes. For larger scale, there's also a PostgreSQL-backed implementation."

**Q: "Can clients request multiple services?"**
> A: "Currently the query returns one service per request using round-robin. Clients can make multiple queries to get different services."

**Q: "Is this production-ready?"**
> A: "It's a research prototype demonstrating service discovery. For production, you'd want to add features like health checking, more sophisticated load balancing algorithms, and distributed deployment."

## TRegistrations Fail (Most Common!)
**Symptom:** Server check passes, but all registrations fail with ✗

**Cause:** Protocol mismatch - server is UDP but demo expects TCP (or vice versa)

**Recovery:**
1. Check server window - look for "mode=tcp" or "mode=udp"
2. If server is UDP:
   - Option A (Quick): Kill server, restart, **select TCP (option 2)**
   - Option B: Tell audience "I'll switch demo to UDP mode" (but skip - takes time)
3. If repeats: Have backup explanation ready

### roubleshooting During Presentation

### Demo Won't Connect to Server
**Symptom:** "Server not running!" error

**Recovery:**
1. Stay calm: "Let me restart the server"
2. Switch to Terminal 1, restart server
3. Wait 2 seconds
4. Return to demo, retry
5. If still failing: "Let me show you the code instead" [have backup slides]

### Server Crashes
**Symptom:** Server terminal closes or shows panic

**Recovery:**
1. "We've found a bug! This is research software."
2. Restart server
3. Restart demo
4. If repeats: Switch to explaining the architecture from code/slides

### Performance is Slow
**Symptom:** Queries taking> 1 second each

**Recovery:**
1. "Network latency is higher than expected"
2. Continue demo - it still works, just slower
3. Adjust your timing/commentary accordingly

## Backup Plan

If technical issues prevent live demo:

1. **Screen recording:** Have a pre-recorded demo video ready
2. **Screenshots:** Capture key screens beforehand
3. **Code walkthrough:** Explain implementation without running it
4. **Architecture focus:** Spend more time on design/architecture discussion

## Tips for Success

1. **Practice timing:** Full demo takes ~8-10 minutes with explanations
2. **Know your pauses:** Pause after each major point for questions
3. **Engage audience:** "Does everyone see the round-robin pattern?"
4. **Be enthusiastic:** Show excitement about what you built
5. **Don't rush:** Better to explain less thoroughly than rush everything
6. **Have confidence:** You built this, you understand it
7. **Terminal preparation:** Clear screens before starting looksprofessional

## Time Variations

**5-minute version:** Skip concurrent performance, show only through round-robin

**10-minute version:** Full demo as scripted above

**15-minute version:** Add code walkthrough of registry implementation

**20-minute version:** Add database backend discussion, show configuration options
