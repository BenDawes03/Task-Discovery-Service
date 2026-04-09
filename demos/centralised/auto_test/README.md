Auto test for centralised demo

Run with:

```powershell
go run ./demos/centralised/auto_test -server 127.0.0.1:5000 -protocol tcp
```

Flags:
- `-tasks` (int): number of distinct task names (default 5)
- `-services` (int): services to register per task (default 3)
- `-clients` (int): concurrent client goroutines (default 10)
- `-queries` (int): queries per client (default 50)
- `-protocol`: "tcp" or "udp"
- `-server`: server address host:port
