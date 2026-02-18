# Firewall Rules

The TDS server supports firewall-aware routing to ensure that clients only receive service addresses they are allowed to communicate with based on network firewall rules.

## Overview

When a client queries the server for a task, the server will:
1. Identify the client's IP address from the request
2. Filter available service addresses based on firewall rules
3. Return only addresses that the client is allowed to reach
4. Return `FORBIDDEN` if no allowed services are available

## Firewall Rules File Format

Firewall rules are specified in a simple text file with one rule per line:

```
source_ip_or_cidr destination_ip_or_cidr
```

### Rules:
- **source**: The IP address or CIDR block of the requesting client
- **destination**: The IP address or CIDR block of the service provider
- Lines starting with `#` are treated as comments
- Empty lines are ignored
- Both single IPs and CIDR notation are supported

### Example Rules File:

```
# Allow client at 192.168.1.10 to reach server at 10.0.0.5
192.168.1.10 10.0.0.5

# Allow entire client subnet to access server subnet
192.168.1.0/24 10.0.0.0/24

# Allow specific client to reach multiple servers
192.168.1.20 10.0.0.10
192.168.1.20 10.0.0.11

# Allow specific subnet to access specific server
10.1.0.0/16 172.16.0.100
```

## Usage

Start the server with the `--firewall-rules` flag to load a firewall rules file:

```bash
# In-memory mode with firewall rules
go run ./cmd/server --firewall-rules firewall_rules.txt

# With PostgreSQL backend
go run ./cmd/server --store-url "postgres://user:pass@localhost/tds" --firewall-rules firewall_rules.txt
```

If no firewall rules file is specified, the server operates in **permissive mode** and allows all requests.

## Protocol Changes

When firewall rules are active, the server may return a new response code:

- **FORBIDDEN**: No service is available that the requestor is allowed to reach according to firewall rules

Existing response codes:
- **OK**: Registration successful
- **NOTFOUND**: No service registered for the requested task
- **ERR**: Protocol error or malformed request

## How It Works

### Query Flow with Firewall:

1. Client at `192.168.1.10` sends: `QUERY task1`
2. Server checks registry for `task1` and finds services at:
   - `10.0.0.5:8080`
   - `10.0.0.6:8080`
   - `172.16.0.100:8080`
3. Server applies firewall rules:
   - `192.168.1.10 → 10.0.0.5`: **ALLOWED** (explicit rule)
   - `192.168.1.10 → 10.0.0.6`: **DENIED** (no rule)
   - `192.168.1.10 → 172.16.0.100`: **DENIED** (no rule)
4. Server uses round-robin among allowed services
5. Server responds: `10.0.0.5:8080`

### Round-Robin with Firewall:

The server maintains round-robin state per task and selects the next available service that passes firewall checks. This ensures fair distribution among allowed services while respecting network policies.

## Address Format

Service addresses can be in two formats:
- **IP only**: `10.0.0.5`
- **IP with port**: `10.0.0.5:8080`

The firewall filtering extracts the IP portion and applies rules to the host IP only.

## Default Behavior

- **No firewall rules loaded**: All requests are allowed (permissive mode)
- **Firewall rules loaded but no matching rule**: Request is denied (whitelist mode)
- **Multiple services available**: Only those passing firewall checks are considered for selection

## Example Scenarios

### Scenario 1: Single Client, Multiple Servers

```
# Rules file
192.168.1.10 10.0.0.5
192.168.1.10 10.0.0.6
```

Client `192.168.1.10` can reach both `10.0.0.5` and `10.0.0.6`. Round-robin will alternate between them.

### Scenario 2: Subnet-Based Access

```
# Rules file
192.168.1.0/24 10.0.0.0/24
```

Any client in `192.168.1.0/24` can reach any service in `10.0.0.0/24`.

### Scenario 3: Mixed Rules

```
# Rules file
192.168.1.10 10.0.0.5          # Specific client to specific server
192.168.1.0/24 10.0.0.0/24     # Subnet to subnet
10.1.0.0/16 172.16.0.100       # Large subnet to single server
```

Rules are checked in order; the first matching rule allows the connection.

## Integration with Store-Backed Registry

Firewall rules work seamlessly with both in-memory and PostgreSQL-backed registries:

- Rules are evaluated in-memory for performance
- The cache respects firewall constraints
- Database lookups are filtered before being cached

## Performance Considerations

- Firewall rule evaluation is O(n) where n is the number of rules
- Rules are checked on every query
- Consider using CIDR blocks to reduce the number of rules
- No firewall rules = maximum performance (permissive mode)

## Security Notes

1. **Default-deny**: Once a firewall rules file is loaded, only explicitly allowed connections are permitted
2. **IP-based**: Firewall rules are based on IP addresses only (no hostname resolution)
3. **No authentication**: This is network-level filtering only; additional authentication may be needed
4. **Client IP spoofing**: The server trusts the transport layer for client IP (UDP/TCP source address)

## Troubleshooting

### All queries return FORBIDDEN

- Check that firewall rules file is formatted correctly
- Verify client IPs match the source specifications in rules
- Verify service IPs match the destination specifications in rules
- Check for typos in IP addresses or CIDR notation

### Some services never selected

- Verify firewall rules allow access to those services
- Check the service IP format matches what's expected by firewall rules
- Use CIDR blocks for flexibility

### Performance degradation

- Reduce the number of firewall rules if possible
- Use broader CIDR blocks instead of individual IP rules
- Consider implementing rule caching if needed (not currently implemented)
