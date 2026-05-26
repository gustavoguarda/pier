# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `saas-stub` now persists uploaded files to disk under `STORAGE_DATA_DIR`
  (default `/data`) instead of holding them in memory until the agent fetches
  them. Files survive `agent` re-fetches and are pruned by a background
  cleanup loop after `RETENTION_DAYS` (default 30; set to 0 to disable).
- `safeJoinClientPath` rejects path traversal, drive letters, backslashes,
  and null bytes server-side before writing to disk, complementing the
  agent's own per-segment validator.
- `docker-compose.dev.yml` to spin up `saas-stub` locally with a bind-mounted
  `./data/` volume — usable from another compose project (via
  `host.docker.internal:9000`) or from a Windows VM (via `192.168.64.1:9000`
  on UTM shared networking).
- [`docs/local-development.md`](docs/local-development.md) walks through the
  end-to-end local stack.

### Changed
- `saas-stub` no longer deletes uploads after the agent fetches them — the
  cleanup loop owns deletion now. This trades the previous "nothing on the
  server" property for the ability to satisfy retention requirements on the
  SaaS side without coordinating extra storage. Run with `RETENTION_DAYS=0`
  if you want the old behaviour back.


## [0.1.0] — 2026-05-20

Initial public release. Extracted from a private predecessor used in production
since May 2026 (Lifleg Contabilidade).

### Added
- `cmd/agent` — outbound-only agent. Connects via WebSocket to a SaaS, reads
  `write`/`read` commands, performs safe local file IO under a configured
  `base_path`. Runs as a Windows Service via `kardianos/service` (also installs
  as a launchd / systemd unit on macOS/Linux).
- `cmd/saas-stub` — reference implementation of the SaaS-side HTTP+WS contract.
  Useful for local end-to-end tests and as a starting point for building your
  own SaaS-side adapter. **Not intended for production as-is** (in-memory
  queue, single agent, no persistence).
- `internal/config`, `internal/filestore`, `internal/saas` — supporting libs.
- Docs: install on Windows, end-to-end diagnostics, UTM test VM setup.
- `Makefile` targets for cross-compilation (Windows AMD64/ARM64, Linux, macOS).
- Dockerfile for `saas-stub`.

### Security
- `internal/filestore` rejects path traversal (`..`, drive letters, backslashes,
  null bytes) per path segment.
- Agent only accepts file URLs whose `scheme://host` matches `http_base_url` —
  prevents a compromised SaaS from instructing the agent to download from an
  arbitrary host.
