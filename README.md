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

## Secrets

A flag value is visible in `ps` and in `/proc/<pid>/cmdline`, so every
secret is taken **by reference**. Each of `--admin-password`,
`--db-password`, `--s3-secret-key`, `--secret-key`, `--internal-token`
and `--lfs-jwt-secret` has a `--<name>-from <ref>` twin, resolved with
[go-authn/secretsource](https://github.com/go-authn/secretsource)
(`systemd-creds:<name>`, `keychain:`, `keepassxc:`, `file:` — mode
checked, at most 4096 bytes). The plain flag still works for one minor
release, prints a deprecation warning on stderr, and is refused together
with its twin. It will be removed in the next minor.

The agent never starts Forgejo and never passes a secret to a child by
argv or environment: it keeps them in memory and, sealed, in etcd, and `weft-init`
renders `app.ini`. When a renderer hands a secret to Forgejo, use
Forgejo's file-based settings (`SECRET_KEY_URI = file:/path`,
`INTERNAL_TOKEN_URI`, `LFS_JWT_SECRET_URI`) with the file in a private
`0700` directory, never an `--option value` or an environment variable.

### Shared secrets in etcd are sealed

`SECRET_KEY`, `INTERNAL_TOKEN` and `LFS_JWT_SECRET` are shared between the
replicas through etcd, but never stored there in plaintext. Every replica
takes the same **cluster key** (at least 32 bytes) by reference with
`--etcd-seal-key-from` (required), e.g. `systemd-creds:etcd-seal`, and
seals each value with AES-256-GCM (key derived by HKDF-SHA256, the etcd
path as additional data) before writing it. etcd holds
`weft-seal:v1:<key id>:<ciphertext>`; the cluster key itself is never in
etcd. Provision it on each node with go-ansible's `systemd_creds_encrypt`
module (or `systemd-creds encrypt`).

- **Upgrading a running cluster**: start the agents with
  `--etcd-seal-key-from`. The first read of a plaintext value seals it
  (compare-and-swap, idempotent, safe with several replicas) and compacts the
  etcd history so the old plaintext revision is dropped. Upgrade every
  replica in the same window: an agent from before this release cannot read
  sealed values.
- **Rotation**: pass the new key as `--etcd-seal-key-from` and the old one as
  `--etcd-seal-old-key-from` (repeatable); values are re-sealed under the new
  key on first read. Drop the old flag once every node has restarted.
- **Not covered**: an etcd member's WAL and snapshots written before the
  upgrade still hold the plaintext until the member is defragmented and the
  snapshots are rotated; a node (or anyone) that holds the cluster key reads
  the secrets.

### etcd connection: TLS and authentication

Sealing protects confidentiality; TLS and authentication protect the
integrity and availability of etcd (the bootstrap lock, the seeded keys).
The agent **refuses a plaintext endpoint** (`http://`, or a bare `host:port`
without TLS material) unless `--etcd-insecure-plaintext` is given, which is
for a throwaway development etcd only.

| Flag | Meaning |
|---|---|
| `--etcd https://host:2379,...` | `https://` endpoints (or bare `host:port` with `--etcd-ca`/`--etcd-cert`) |
| `--etcd-ca <pem>` | CA that signed the etcd servers (default: system roots); public path |
| `--etcd-cert <pem>` | client certificate for mutual TLS; public path |
| `--etcd-client-key-from <ref>` | client private key (PEM), by secretsource reference, e.g. `systemd-creds:etcd-client-key`; needs `--etcd-cert` |
| `--etcd-user <name>` | etcd user for etcd authentication |
| `--etcd-password-from <ref>` | its password, by reference; needs `--etcd-user` |
| `--etcd-insecure-plaintext` | allow plaintext etcd (development only) |

TLS is 1.2 or later, and the server certificate is always verified. The key
and the password are never accepted as plain flag values. Upgrading: switch
the etcd members to TLS (and, for mutual TLS, `--client-cert-auth`), then
restart the agents with the flags above.

### systemd credentials

```ini
[Service]
LoadCredentialEncrypted=admin-password:/etc/credstore.encrypted/admin-password
LoadCredentialEncrypted=db-password:/etc/credstore.encrypted/db-password
ExecStart=/usr/local/bin/weft-ha-forgejo agent \
  --admin-password-from systemd-creds:admin-password \
  --db-password-from systemd-creds:db-password
```

Seal with `systemd-creds encrypt --name=admin-password - admin-password.cred`.
**TPM caveat**: do not seal with a bare `--tpm2-pcrs=7`: a Secure Boot
db/dbx or firmware update changes PCR 7 and the credential no longer
opens. Seal to `--with-key=host+tpm2` (the host key is the recovery
path), or to a signed PCR policy (`--tpm2-public-key=`), or manage the
policy with `systemd-pcrlock`; otherwise keep a plaintext copy offline
and re-seal after each firmware change.

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
