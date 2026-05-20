# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
