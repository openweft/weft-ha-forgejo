# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Initial scaffold for the `weft-ha-forgejo` agent — the per-replica
  Go operator behind the `forgejo-ha` catalogue plugin.
- `cmd/weft-ha-forgejo` cobra CLI : `version` + `agent` subcommands.
- `internal/config` : typed Config struct + Validate().
- `internal/dcs` : etcd-backed key store + bootstrap advisory lock
  (MemStore scaffold today ; etcd-backed Store in the live milestone).
- `internal/bootstrap` : shared-secret minting + seeding under an
  advisory lock ; idempotent across replicas (the second + third
  replica observe the secrets already present and skip the mint).
- `internal/api` : role API (`/ready`, `/info`) on the port the L7
  Caddy in `weft-agent` active-probes.
- `internal/forgejo` : thin wrapper around the local forgejo process
  (status, reload) ; FakeController for unit tests.
- `internal/reconcile` : tick loop running every 5s by default, exits
  on SIGINT / SIGTERM.
- Cross-builds : linux/arm64 + linux/amd64.

### Status

This is the initial scaffold. The agent compiles, runs, and exposes
its health surface against the FakeController. End-to-end integration
with a live Forgejo + Postgres + S3 backend is the v0.2 milestone.
