<p align="center"><img src="https://raw.githubusercontent.com/openweft/brand/main/social/openweft.png" alt="openweft" width="720"></p>

# weft-ha-forgejo

Go-native HA operator for Forgejo (Git forge ; AGPLv3+, soft-fork of
Gitea), packaged as the `forgejo-ha` plugin in openweft's catalogue.

One agent runs alongside every Forgejo replica micro-VM. Together
with the L7 Caddy in `weft-agent`, three replicas spread across DCs
form an active-active install : clients hit the public domain over
HTTPS (port 3000 inside the zone), git push/pull goes through
SSH on port 2222, and Caddy load-balances onto whichever replicas
currently pass the agent's health probe.

## Architecture

| Layer            | Component                                              |
| ---------------- | ------------------------------------------------------ |
| Catalog database | `postgres-ha` (separate plugin — install first)        |
| Object storage   | `versitygw-ha` (or external S3) — attachments + LFS    |
| Forgejo          | 3× upstream `codeberg.org/forgejo/forgejo:10` + agent  |
| Routing          | L7 Caddy in `weft-agent` → 3000/tcp + 2222/tcp         |
| Coordination     | etcd (shared secrets + bootstrap leader election only) |

Forgejo replicas are stateless once :

1. they all agree on `SECRET_KEY`, `INTERNAL_TOKEN`, `LFS_JWT_SECRET`
   (mismatch silently corrupts session cookies + 2FA storage), and
2. the Postgres schema + admin user are in place.

So the agent's job is narrower than `weft-ha-postgresql`'s : no
continuous leader election, just one-shot bootstrap + health probe.

## What the agent does

1. **Bootstrap** — the first replica to acquire the etcd advisory lock :
   - mints `SECRET_KEY`, `INTERNAL_TOKEN`, `LFS_JWT_SECRET` (operator
     can override via plugin inputs ; the agent only mints when the
     input is empty),
   - seeds them into etcd so the other two replicas pick them up
     instead of minting their own (which would split the install),
   - creates the Postgres schema if missing (Forgejo's own
     `forgejo migrate` is idempotent — we just run it),
   - creates the admin user if missing (`forgejo admin user create`).

2. **Config reconciliation** — every reconcile tick, the agent
   pulls the operator's plugin inputs out of env, renders
   `/etc/forgejo/app.ini`, and signals Forgejo to reload if anything
   changed.

3. **Health probe** — runs Forgejo's `/api/healthz` against the
   local process and exposes `/ready` + `/info` on `:3001`. The L7
   Caddy active-probes `/ready` ; failing replicas are drained.

## Layout

```
cmd/weft-ha-forgejo/      cobra entrypoint, agent subcommand
internal/
  api/                    :3001 /ready, /info
  bootstrap/              one-shot install : secrets + schema + admin
  config/                 plugin-input → in-process Config
  dcs/                    etcd-backed key + advisory lock store
  forgejo/                local forgejo process controller (status, reload)
  reconcile/              tick loop : bootstrap once, then config + health
```

## Build

```sh
task build           # host arch
task build-linux     # linux/arm64 + linux/amd64 (what the micro-VM runs)
task test            # go test -race
```

## License

This agent is BSD-3-Clause (openweft contributors). It runs alongside
upstream Forgejo (AGPLv3+) — we do NOT fork Forgejo. If you ship a
modified Forgejo, the AGPL §13 network-source-disclosure obligation
follows your fork ; this agent stays under BSD-3 either way.
