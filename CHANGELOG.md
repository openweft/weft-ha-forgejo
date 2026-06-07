# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `internal/api.ready` (serving both `/ready` and `/health`) now emits a
  JSON body in the IETF Health Check Response Format
  (draft-inadarei-api-health-check) : `{"status":"pass"}` on healthy,
  `{"status":"fail","reason":...}` on drain. Previously the endpoint
  returned a plain-text `ok` / `forgejo not up` body, which forced the
  webui dashboard to special-case forgejo-ha against the postgres-ha
  sibling. Same vocab as the upstream Forgejo `/api/healthz` we probe.

## [v0.2.0-rc1] — 2026-06-05

This release candidate turns the v0.1 scaffold into an operational
agent : the Forgejo health probe is now a real HTTP client against the
loopback Forgejo, the DCS is etcd-backed under the same key prefix the
weft-ha-postgresql sibling uses, and the whole thing ships as a
multi-arch OCI image on top of the upstream Forgejo base.

### Added

- `internal/forgejo` : `HTTPController`, a production-ready
  `Controller` implementation that probes `/api/healthz` on
  `127.0.0.1:3000` and best-effort enriches `Status.Version` from
  `/api/v1/version`. 5-second client timeout, zero retries — the
  reconcile loop's 5-second tick is the retry policy. Failed dials
  surface as `Up=false` with a descriptive `Reason` instead of
  bubbling up an error, so the L7 pool sees a clean drain signal.
  `FakeController` stays exported for unit tests + smoke dev.
- `internal/dcs/etcd.go` : `EtcdStore` implementing `Store` against
  `go.etcd.io/etcd/client/v3`. Lazy-opens a client + `concurrency.Session` on
  first use ; `GetKey` is `client.Get`, `PutKeyIfAbsent` is a single-shot
  `Txn` comparing `CreateRevision == 0`, `AcquireBootstrapLock` is a
  `concurrency.NewMutex` at `/weft/forgejo/<install>/bootstrap-lock`.
  Closing the store drops the lease so a fenced agent releases the
  lock within session TTL.
- `cmd/weft-ha-forgejo/main.go` : runtime DCS + Controller selection.
  `WEFT_HA_FORGEJO_ETCD=host:2379[,...]` switches to `EtcdStore` (also
  honours `--etcd` flags as a fallback) ; default stays `MemStore` for
  single-host smoke. `WEFT_HA_FORGEJO_USE_REAL_CONTROLLER=1` switches
  to `HTTPController` (with `WEFT_HA_FORGEJO_FORGEJO_URL` to override
  the base URL) ; default stays `FakeController`. Same binary,
  zero build-time toggles.
- `Dockerfile` : multi-stage build. Stage 1 (`golang:1.26-alpine`)
  cross-builds the agent pure-Go (CGO=0) for `$TARGETARCH`. Stage 2
  (`codeberg.org/forgejo/forgejo:10`) drops the agent binary into
  `/usr/local/bin/` and wires it as the entrypoint.
- `docker/entrypoint.sh` : spawns `forgejo web` in the background,
  execs `weft-ha-forgejo agent` in the foreground. A watcher
  goroutine signals the entrypoint when Forgejo exits so the
  container terminates as a whole — the L7 pool drains the replica
  and `weft-agent` reschedules.
- `.github/workflows/release.yml` : `workflow_dispatch` +
  `push: tags ['v*']` only (per the openweft no-autopublish policy).
  Builds + pushes a multi-arch (`linux/amd64`, `linux/arm64`) image
  to `ghcr.io/openweft/forgejo-ha:{tag,latest}`.

### Notes

The v0.2 milestone leaves Postgres schema migration + admin-user
create out of scope ; Forgejo's own `forgejo migrate` runs
idempotently on startup so the only remaining advisory-locked step
(secret minting) is already implemented. Live integration testing
against a 3-DC etcd quorum lands in v0.2 final.

## [v0.1.0] — 2026-06-05

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
