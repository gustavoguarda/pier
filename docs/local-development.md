# Local development — end-to-end stack on macOS + a Windows VM

How to run the full upload pipeline locally: your SaaS-side adapter → `saas-stub` on the Mac → `agent.exe` on a Windows VM → file written to the configured `base_path`.

```
┌───────────────────────────┐
│  Mac (Docker Desktop)     │
│                           │       SaaS adapter reaches the stub at
│  ┌─────────────────────┐  │       http://host.docker.internal:9000
│  │ Your SaaS adapter   │──┼──┐    (Docker → host shortcut)
│  │ (in another compose)│  │  │
│  └─────────────────────┘  │  ▼
│                           │     ┌──────────────────────────┐
│  ┌─────────────────────┐  │     │  saas-stub container     │
│  │ pier/               │  │     │  port 9000               │
│  │   docker-compose.dev│──┼────▶│  ./data/ ← files          │
│  └─────────────────────┘  │     └──────────────────────────┘
│                           │                ▲
└───────────────────────────┘                │
                                             │  ws + http
                                             │  via 192.168.64.1:9000
                                             │  (UTM shared network default)
                                  ┌──────────┴──────────┐
                                  │  Windows VM (UTM)   │
                                  │  agent.exe          │
                                  │  base_path          │
                                  └─────────────────────┘
```

This mirrors what would run in production — only with the stub in place of a real SaaS-side adapter, and a local VM in place of a customer's on-prem server.

## Prerequisites

- **Mac with Docker Desktop** running
- **A Windows VM in [UTM](https://mac.getutm.app)** with **Shared Network** (default)
  - The Mac is reachable from the VM at `192.168.64.1` (the NAT gateway)
  - If you use Bridged, replace that IP with the Mac's LAN IP
- **`agent.exe`** built for the VM's architecture (`make build-windows-arm64` or `make build-windows`)

## Setup in 5 steps

### 1. Start `saas-stub` on the Mac

```bash
cd ~/Projects/<your>/pier
docker compose -f docker-compose.dev.yml up -d --build
```

Sanity check:
```bash
curl http://127.0.0.1:9000/healthz
# {"status":"ok","agent_connected":false}
```

Live logs:
```bash
docker compose -f docker-compose.dev.yml logs -f saas-stub
```

Config (via compose env):
- `STORAGE_SAAS_TOKEN=dev-token` — auth token shared with the agent and your SaaS adapter
- `STORAGE_DATA_DIR=/data` — where files are persisted (mapped to `./data/` on the Mac)
- `RETENTION_DAYS=30` — background cleanup of files older than this (set `0` to disable)
- Port `9000` bound to `0.0.0.0` so the VM can reach it

### 2. Configure `config.yaml` on the Windows VM

Inside the VM, somewhere under your agent install directory:

```yaml
saas_url: ws://192.168.64.1:9000/agent/ws
http_base_url: http://192.168.64.1:9000
token: dev-token
base_path: C:\rede\clientes
```

> `192.168.64.1` is the UTM shared-network NAT gateway — the IP the VM uses to reach the Mac. It's stable across Mac reboots. On Bridged setups, use the Mac's LAN IP instead.

### 3. Build the agent and copy it to the VM

On the Mac:
```bash
make build-windows-arm64    # or make build-windows for AMD64
```

Copy `dist/agent.exe` (or `agent-arm64.exe`) to the VM via UTM shared folder, scp, or whatever you prefer.

### 4. Start the agent on the VM (interactive mode for debugging)

PowerShell **as Administrator** in the folder containing the agent binary:

```powershell
.\agent.exe -config .\config.yaml
```

Expected output:
```
INFO connecting to SaaS  url=ws://192.168.64.1:9000/agent/ws
INFO connected to SaaS
```

Back on the Mac:
```bash
curl http://127.0.0.1:9000/healthz
# {"status":"ok","agent_connected":true}    ← true now ✓
```

For running as a Windows Service, see [`install-windows.md`](install-windows.md). For dev work, the interactive mode is usually better — you see logs in the terminal directly.

### 5. Exercise the pipeline

From a third terminal:

```bash
echo "hello pier" > /tmp/test.txt
curl -s -X POST http://127.0.0.1:9000/upload \
  -F "client_name=acme/inbox" \
  -F "file=@/tmp/test.txt"
```

The file should appear in three places:
- **Mac**: `./data/acme/inbox/test.txt`
- **saas-stub logs**: `queued write command client=acme/inbox filename=test.txt file_id=...`
- **Windows VM**: `C:\rede\clientes\acme\inbox\test.txt`

## Port cheat sheet

| Caller → callee | Route | URL |
|---|---|---|
| Your SaaS adapter (Docker container on Mac) → saas-stub | container → host | `http://host.docker.internal:9000` |
| You from terminal/browser on Mac → saas-stub | host → 0.0.0.0 | `http://127.0.0.1:9000` |
| Windows VM → saas-stub | NAT → host | `http://192.168.64.1:9000` |

## Common commands

```bash
# Up
docker compose -f docker-compose.dev.yml up -d --build

# Logs
docker compose -f docker-compose.dev.yml logs -f saas-stub

# Down
docker compose -f docker-compose.dev.yml down

# Disk usage of the persisted data
du -sh ./data

# Wipe everything (CAUTION — deletes persisted files too)
docker compose -f docker-compose.dev.yml down -v
rm -rf ./data
```

## Troubleshooting

### Agent can't connect (`connectex: No connection could be made`)
- Is `saas-stub` running? `docker compose -f docker-compose.dev.yml ps`
- Is the VM on Shared Network? `Get-NetIPConfiguration` should show `192.168.64.x` with gateway `192.168.64.1`
- Mac firewall might be blocking port 9000 — System Settings → Network → Firewall

### Agent returns `unauthorized`
- The `token` in the VM's `config.yaml` must equal `STORAGE_SAAS_TOKEN` in the compose env (default `dev-token`).

### Upload returns 503 `no agent connected`
- The agent on the VM isn't connected. Run `curl http://127.0.0.1:9000/healthz` on the Mac — if `agent_connected:false`, the agent needs starting (or restarting).

### File saved on the Mac but not on the VM
- Look at the agent logs on the VM (interactive terminal, or Event Viewer if running as service)
- Path inside the VM should be `<base_path>\<client>\<filename>`
- Write permissions: if `base_path` points to a network share, the user the service runs as needs access

## What differs from production

| Aspect | Dev (this doc) | Production |
|---|---|---|
| SaaS-side adapter | `saas-stub` reference impl | Your real backend (custom code) |
| Stub host | `host.docker.internal:9000` | Public URL with TLS |
| Token | `dev-token` literal | Per-environment secret |
| Data dir | `./data/` on the Mac | Persistent Docker volume |
| Agent host | UTM VM on the Mac | A customer's on-prem server |
| Retention | 30 days (local) | 30 days (configured per env) |
