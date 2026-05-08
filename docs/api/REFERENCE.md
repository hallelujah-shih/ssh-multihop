# REST API Reference

## Base URL

```
http://localhost:8080/api/v1
```

## Forward Types

The system supports three forward types:

1. **`local_listen_to_remote`** - SSH -L (local listen to remote service)
   - Listens on local port, forwards to remote service via SSH tunnel
   - Example: Access remote database from local machine

2. **`remote_listen_to_local`** - SSH -R (remote listen to local service)
   - Listens on remote port via SSH, forwards to local service
   - Example: Expose local web server to remote network

3. **`remote_listen_to_remote`** - Remote-to-remote bridging
   - Bridges two remote hosts without binding a local port
   - Example: Connect service on VM A to VM B

## Address Format

Addresses use standard `[ip]:port` format:
- `127.0.0.1:8888` - loopback only
- `:8888` - all interfaces (shorthand for `0.0.0.0:8888`)
- `192.168.1.10:8000` - specific IP

## API Endpoints

### Create Forward

**POST** `/api/v1/forwards`

Create a new port forwarding rule.

**Request Body:**
```json
{
  "type": "local_listen_to_remote | remote_listen_to_local | remote_listen_to_remote",
  "listen_host": "SSH hostname for listen side",
  "listen_addr": "Address to bind to ([ip]:port or :port)",
  "service_host": "SSH hostname for service side",
  "service_addr": "Service address ([ip]:port or :port)",
  "max_conns": 0,          // Optional: max concurrent connections
  "description": "Text"    // Optional: description (max 500 chars)
}
```

**Response:**
```json
{
  "id": "uuid",
  "type": "local_listen_to_remote",
  "listen_host": "local",
  "listen_addr": "127.0.0.1:8888",
  "service_host": "vmr.u24",
  "service_addr": "127.0.0.1:8888",
  "status": "running",
  "created_at": "2026-03-15T10:00:00Z"
}
```

**Validation Rules:**

#### local_listen_to_remote
- `listen_host` must be `"local"`
- `service_host` cannot be `"local"`

#### remote_listen_to_local
- `listen_host` cannot be `"local"`
- `service_host` must be `"local"`

#### remote_listen_to_remote
- `listen_host` cannot be `"local"`
- `service_host` cannot be `"local"`

**Example:**
```bash
curl -X POST http://localhost:8080/api/v1/forwards \
  -H "Content-Type: application/json" \
  -d '{
    "type": "remote_listen_to_local",
    "listen_host": "vmr.u24",
    "listen_addr": ":4000",
    "service_host": "local",
    "service_addr": "127.0.0.1:4000",
    "description": "Expose local service on vmr.u24"
  }'
```

---

### List Forwards

**GET** `/api/v1/forwards`

List all port forwarding rules.

**Response:**
```json
[
  {
    "id": "uuid-1",
    "type": "local_listen_to_remote",
    "listen_host": "local",
    "listen_addr": "127.0.0.1:8888",
    "service_host": "vmr.u24",
    "service_addr": "127.0.0.1:8888",
    "status": "running",
    "created_at": "2026-03-15T10:00:00Z"
  },
  {
    "id": "uuid-2",
    "type": "remote_listen_to_local",
    "listen_host": "vmr.u24",
    "listen_addr": ":4000",
    "service_host": "local",
    "service_addr": "127.0.0.1:4000",
    "status": "running",
    "created_at": "2026-03-15T10:05:00Z"
  }
]
```

**Status Values:**
- `stopped` - Forward not running
- `running` - Forward actively running
- `error` - Forward encountered error (auto-rebuild in progress)

---

### Get Forward

**GET** `/api/v1/forwards/:id`

Get details of a specific forward.

**Response:**
```json
{
  "id": "uuid",
  "type": "local_listen_to_remote",
  "listen_host": "local",
  "listen_addr": "127.0.0.1:8888",
  "service_host": "vmr.u24",
  "service_addr": "127.0.0.1:8888",
  "status": "running",
  "max_conns": 0,
  "description": "Forward description",
  "created_at": "2026-03-15T10:00:00Z",
  "updated_at": "2026-03-15T10:00:00Z"
}
```

**Error Response (404):**
```json
{
  "error": "Forward not found",
  "details": "forward with id 'xxx' does not exist",
  "code": "NOT_FOUND"
}
```

---

### Delete Forward

**DELETE** `/api/v1/forwards/:id`

Delete a port forwarding rule.

**Response:** `204 No Content`

**Error Response (404):**
```json
{
  "error": "Forward not found",
  "details": "forward with id 'xxx' does not exist",
  "code": "NOT_FOUND"
}
```

---

### Get Forward Status

**GET** `/api/v1/forwards/:id/status`

Get the current status of a forward.

**Response:**
```json
{
  "forward_id": "uuid",
  "status": "running",
  "last_heartbeat": "2026-03-15T10:30:00Z",
  "error_message": "",
  "active_connections": 3
}
```

**Error Response (404):**
```json
{
  "error": "Forward status not found",
  "code": "NOT_FOUND"
}
```

---

## Error Responses

All endpoints may return error responses in the following format:

```json
{
  "error": "Error message",
  "code": "ERROR_CODE",
  "details": "Detailed error information"
}
```

**Common Error Codes:**

| Code | Description |
|------|-------------|
| `INVALID_LISTEN_ADDR` | Invalid listen address format |
| `INVALID_SERVICE_ADDR` | Invalid service address format |
| `INVALID_CONFIGURATION` | Invalid type-specific configuration |
| `CREATE_FAILED` | Failed to create forward |
| `LIST_FAILED` | Failed to list forwards |
| `GET_FAILED` | Failed to get forward |
| `DELETE_FAILED` | Failed to delete forward |
| `NOT_FOUND` | Resource not found |
| `INVALID_ID` | Invalid forward ID format |

---

## Common Usage Examples

### Example 1: Access Remote Service Locally

Scenario: Service running on `vmr.u24:8888`, access from `localhost:8888`

```bash
curl -X POST http://localhost:8080/api/v1/forwards \
  -H "Content-Type: application/json" \
  -d '{
    "type": "local_listen_to_remote",
    "listen_host": "local",
    "listen_addr": "127.0.0.1:8888",
    "service_host": "vmr.u24",
    "service_addr": "127.0.0.1:8888"
  }'
```

Now access: `curl localhost:8888` → reaches `vmr.u24:8888`

---

### Example 2: Expose Local Service on Remote

Scenario: Service on `localhost:4000`, expose on `vmr.u24:4000`

```bash
curl -X POST http://localhost:8080/api/v1/forwards \
  -H "Content-Type: application/json" \
  -d '{
    "type": "remote_listen_to_local",
    "listen_host": "vmr.u24",
    "listen_addr": ":4000",
    "service_host": "local",
    "service_addr": "127.0.0.1:4000"
  }'
```

Now `vmr.u24:4000` → tunnels to → `localhost:4000`

---

### Example 3: Bridge Two Remote Hosts

Scenario: Service on `vmr.u24:11434`, expose on `dc4:11434`

```bash
curl -X POST http://localhost:8080/api/v1/forwards \
  -H "Content-Type: application/json" \
  -d '{
    "type": "remote_listen_to_remote",
    "listen_host": "dc4",
    "listen_addr": "127.0.0.1:11434",
    "service_host": "vmr.u24",
    "service_addr": "127.0.0.1:11434"
  }'
```

Now `dc4:11434` → tunnels to → `vmr.u24:11434`

---

### Example 4: Bind to All Interfaces

Scenario: Expose local service on all remote interfaces

```bash
curl -X POST http://localhost:8080/api/v1/forwards \
  -H "Content-Type: application/json" \
  -d '{
    "type": "remote_listen_to_local",
    "listen_host": "vmr.u24",
    "listen_addr": ":4000",
    "service_host": "local",
    "service_addr": "127.0.0.1:4000",
    "description": "Accessible from any interface on vmr.u24"
  }'
```

Use `:4000` to bind to all interfaces (`0.0.0.0:4000`)

---

## Architecture Notes

### Forward Types and Implementations

| Forward Type | Implementation | Description |
|--------------|----------------|-------------|
| `local_listen_to_remote` | `LocalListenToRemote` | SSH -L forwarding |
| `remote_listen_to_local` | `RemoteListenToLocal` | SSH -R forwarding |
| `remote_listen_to_remote` | `RemoteListenToRemote` | Remote-to-remote bridge |
| (via orchestrator) | `InlineForwardOrchestrator` | UDS-based composition |

### Service Layer Recovery

The system implements **simplified architecture** where:

1. **Forward instances** only handle connection and health checking
2. **ForwardService** manages lifecycle (creation, rebuild, deletion)
3. Forwards fail fast on errors and set database status to `"error"`
4. Service layer sync loop (every 5s) detects and rebuilds error forwards

See [architecture.md](../architecture.md) for details.

### Auto-Recovery

- Forwards are automatically rebuilt when they encounter errors
- Rebuild uses exponential retry (max 10 attempts, 3s delay)
- Database is single source of truth for configuration
- Health checks run every 15 seconds

---

## Testing

Use the test scripts in `docs/scripts/`:

```bash
# Test all 7 common scenarios
./docs/scripts/test-7-scenarios.sh

# Test API endpoints
./docs/scripts/test-api.sh
```

For more test examples, see the test scripts in `docs/scripts/` directory.
