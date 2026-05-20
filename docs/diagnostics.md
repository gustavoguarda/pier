# Diagnostics — end-to-end observability for pier uploads

How to follow a file moving through the pier pipeline in real time, verify health, and debug failures.

The pipeline has 3 moving parts:

```
Your SaaS                       saas-stub (or your own saas)        agent.exe
(receives upload from end user) ────────────────────────────────▶  on Windows
                                  HTTP POST /upload                  receives WS command
                                                                     fetches file → writes to disk
                                                                     <base_path>/<client>/<filename>
```

Each part has its own log. To debug well, watch them in parallel.

## Follow an upload live (3 terminals)

Open three terminals side by side **before** triggering the upload from your SaaS UI / API client.

### Terminal 1 — your SaaS logs

Whatever you use. The relevant lines are the `POST /upload` (or equivalent) and any errors after that.

### Terminal 2 — saas-stub / your SaaS-side adapter

If you're running `saas-stub`:

```bash
# In Docker:
docker logs -f <saas-stub-container>

# Or locally if you built/run it directly:
./dist/saas-stub
```

Expected log lines:
```
INFO agent connected remote=<ip>:<port>
INFO queued write command client=<client_name> filename=<file> file_id=<n>
```

### Terminal 3 — Windows VM / server (PowerShell)

```powershell
# Service status
Get-Service pier-agent
# Status   Name        DisplayName
# ------   ----        -----------
# Running  pier-agent  Pier Agent

# List everything under base_path
dir <base_path> -Recurse |
  Select-Object FullName, Length, LastWriteTime

# Files written today, newest first
dir <base_path> -Recurse -File |
  Where-Object { $_.LastWriteTime -ge (Get-Date).Date } |
  Sort-Object LastWriteTime -Descending |
  Select-Object FullName, Length, LastWriteTime

# Recent agent events (Application log)
Get-EventLog -LogName Application -Source pier-agent -Newest 20 |
  Format-Table TimeGenerated, EntryType, Message -AutoSize -Wrap

# How much disk is used under base_path
$total = (dir <base_path> -Recurse -File | Measure-Object -Sum Length).Sum
"{0:N2} MB" -f ($total / 1MB)
```

> ⚠️ PowerShell: always prefix executables in the current directory with `.\` (`.\agent.exe`). PowerShell does not look in `cwd` by default — without it you'll see `command not found`.

## Anatomy of a successful upload

Sequence (timestamps within seconds of each other):

1. **Your SaaS**: receives upload from end user, returns 2xx
2. **SaaS-side adapter**: logs `queued write command client=... filename=... file_id=...`
3. **agent.exe**: pulls `file_url` and writes
4. **Windows disk**: file appears at `<base_path>/<client>/<filename>` with the expected size

If something breaks, it's almost always at one of the boundaries between these.

## Spot checks

### Agent connection status (if your SaaS exposes a healthcheck)

`saas-stub` does:
```bash
curl https://<your-saas-host>/healthz
# {"status":"ok","agent_connected":true}
```

`agent_connected:true` confirms the WebSocket is up at this instant.

### Agent service status (Windows)
```powershell
Get-Service pier-agent
```
Expected: `Running`. Any other state means the OS terminated the service or it never started cleanly.

### Who's connected to saas-stub
```bash
docker logs --tail 30 <saas-stub-container> | grep -E "agent (connected|disconnected)"
```

## Troubleshooting by symptom

### Your SaaS gets 5xx when calling `/upload` on the SaaS-side adapter

Check the adapter's logs first. Common causes:

- **`no agent connected`** — the WebSocket from the agent is not active. Confirm agent is `Running` on the Windows server. If yes, the network path between agent ↔ adapter may be broken (firewall, DNS).
- **`upload to agent failed`** — adapter received the file but couldn't push the `write` command. Same diagnosis.

### Upload returns success but file never appears on disk

The adapter accepted the file but the agent failed to write. Check Windows Event Viewer:
```powershell
Get-EventLog -LogName Application -Source pier-agent -EntryType Error -Newest 10
```

Common causes:
- Service runs as `LocalSystem` and `base_path` is on a mapped network drive only visible to a user account → change service `Log On` to a domain account, or use a UNC path (`\\server\share`)
- Disk full
- Filename has invalid characters (path traversal protection in `internal/filestore`)

### Agent reconnects every ~2 minutes

If the WebSocket logs show `agent disconnected (1006 unexpected EOF)` followed by `agent connected` in a 1–2s loop:

**Cause**: a proxy between agent and SaaS kills idle WebSockets. Cloudflare proxy (orange cloud) does this at ~100s on the free plan, ~600s on paid. The agent re-establishes automatically and uploads work fine in steady state, but there's a brief window per cycle where a fresh upload could 503.

**Fix options**:
- DNS-only mode on the SaaS subdomain (no Cloudflare proxy) — trivial fix on the Cloudflare side
- Implement heartbeat ping every 30s on the agent — code change

### `Failed to start... service did not respond in timely fashion`

Almost always one of:
1. **`-config` was a relative path** during `service install` and `cwd = C:\Windows\System32` at boot. Reinstall with an absolute path.
2. **Binary corrupted or arch mismatch** (e.g. you copied an ARM64 build to an AMD64 server, or vice-versa).
3. **Config file invalid YAML** — run interactively first (`.\agent.exe -config <path>`) to see the error.

### `Failed to status` with `Unknown action status`

`status` is **not** a valid `-service` action in this binary. Use `Get-Service pier-agent` instead.

## Where each part lives — generic map

| Piece | Where | How to see logs |
|---|---|---|
| Your SaaS | Wherever you deploy it | Your platform's logs |
| SaaS-side adapter (or `saas-stub`) | Docker / VM / serverless | Container logs / stdout |
| `agent.exe` | Windows Service on the customer's on-prem server | `Get-EventLog -Source pier-agent` |
| Output files | Disk on the customer's server | `dir <base_path> -Recurse` |
