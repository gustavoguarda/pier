# pier

> Outbound-only agent that lets your SaaS deliver files to a server inside a corporate network — no inbound ports, no VPN, no port forwarding.

`pier` is a tiny Windows/Linux/macOS service that runs on the client's on-prem server, opens a single outbound WebSocket connection to your SaaS, and reads/writes files on the local disk on demand. Useful when:

- Your SaaS receives file uploads from end users and needs to deliver them to a customer's internal file share.
- The customer's IT won't open inbound ports, won't run a VPN client, won't expose their server.
- You don't want to depend on SMB, FTP, or sync tools you can't control.

It's the **inverted polarity** of SMB/SFTP: instead of _your SaaS connecting in_, the _on-prem agent connects out_.

## Architecture

```
                  outbound WSS + HTTPS
┌────────────┐ ───────────────────────▶ ┌────────────┐
│ on-prem    │                          │ SaaS       │
│ pier-agent │ ◀────── pull/push ────── │ (your app) │
│ disk: ...  │                          │            │
└────────────┘                          └────────────┘
   (no inbound)                            (public)
```

The agent only ever **opens** connections — it never accepts them. The SaaS is the public endpoint, and it asks the agent to fetch a file (`write` command) or to send a file back (`read` command) by sending JSON commands over the WebSocket.

## Components

This repo ships two binaries:

| Binary      | Where it runs                                             | Purpose                                                                                                                                                                                                                                             |
| ----------- | --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `agent`     | On-prem server (your customer's machine, Windows usually) | Connects out to the SaaS, executes file write/read commands                                                                                                                                                                                         |
| `saas-stub` | Anywhere you can host an HTTP service                     | **Reference implementation** of the HTTP+WS contract. Useful for local end-to-end testing and as a starting point if you're building your own SaaS endpoint. **Not intended for production as-is** (in-memory queue, single agent, no persistence). |

If you're plugging `pier` into an existing SaaS, you'll usually write your own SaaS-side adapter (Node, PHP, Python, whatever) that speaks the same HTTP+WS contract as `saas-stub`.

## Quick start (local end-to-end)

```bash
# Terminal 1 — fake SaaS
make build && ./dist/saas-stub

# Terminal 2 — agent
cp config.example.yaml config.yaml
./dist/agent -config config.yaml

# Terminal 3 — push a file through
echo "Hello from SaaS" > /tmp/hello.txt
curl -X POST http://localhost:9000/upload \
  -F "client_name=Cliente A" \
  -F "file=@/tmp/hello.txt"

ls "data/clientes/Cliente A/"
# hello.txt

# Pull the file back through the agent
curl "http://localhost:9000/download?client_name=Cliente%20A&filename=hello.txt"
```

## Repository layout

```
cmd/agent/          — agent binary (runs as Windows Service in production)
cmd/saas-stub/      — minimal reference SaaS for tests + as a contract spec
internal/config/    — YAML config loader
internal/saas/      — WebSocket client + HTTP fetch
internal/filestore/ — safe file writes/reads on local disk
docs/               — operational guides (install, diagnostics)
scripts/            — PowerShell helpers for Windows installation
```

## Protocol

The SaaS sends JSON commands over the WebSocket connection.

### Write — SaaS pushes a file to the agent

```json
{
  "action": "write",
  "client": "Cliente A",
  "filename": "x.pdf",
  "file_url": "/files/123"
}
```

The agent fetches `file_url` (relative to `http_base_url`, or absolute as long as the host matches) with `Authorization: Bearer <token>` and writes the body to `<base_path>/<client>/<filename>`.

### Read — SaaS asks the agent to send a file back

```json
{
  "action": "read",
  "client": "Cliente A",
  "filename": "x.pdf",
  "upload_url": "/agent/responses/<request_id>",
  "request_id": "<request_id>"
}
```

The agent reads `<base_path>/<client>/<filename>` and POSTs the body to `upload_url` with the bearer token. The SaaS uses `request_id` to match the response with the original HTTP caller.

## Configuration (agent side)

`config.yaml`:

```yaml
saas_url: wss://saas.example.com/agent/ws # required
http_base_url: https://saas.example.com # required
token: your-shared-token # auth header value
base_path: C:\rede\clientes # required; on Windows
```

## Run as Windows Service

```powershell
# Build on dev machine
make build-windows                 # AMD64 → dist/agent.exe
make build-windows-arm64           # ARM64 → dist/agent-arm64.exe

# On the Windows server (PowerShell as Administrator)
cd C:\pier
.\agent.exe -config C:\pier\config.yaml -service install
.\agent.exe -service start
Get-Service pier-agent
```

Other service actions: `stop`, `restart`, `uninstall`.

> ⚠️ Use **absolute paths** in `-config`. Windows Services run with `cwd = C:\Windows\System32`, so relative paths fail.

## Reconnect / resilience

- WebSocket client uses exponential backoff (1s → 60s), resets to 1s if the previous connection stayed up at least 30s.
- Each incoming command runs in its own goroutine — slow operations don't block the read loop.
- Loss of connectivity never crashes the service.

## Docs

- [`docs/install-windows.md`](docs/install-windows.md) — end-to-end install on a Windows server
- [`docs/diagnostics.md`](docs/diagnostics.md) — observability commands for SaaS↔agent uploads

## License

[MIT](LICENSE) — © 2026 Gustavo Guarda.
