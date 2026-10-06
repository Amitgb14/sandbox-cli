# sandboxd reference

`sandboxd` serves the Sandbox API on one machine, one per machine. Every
client — the CLI, Studio, the SDKs, a gateway in front of many machines —
reaches sandboxes only through it.

This page is the reference: its flags, the policy file, node mode and
metrics. Setting a machine up is [self-hosting.md](self-hosting.md) on Linux
and [local-macos.md](local-macos.md) on a Mac; the API it serves is
[api/v1.md](api/v1.md).

```sh
sandboxd --backend firecracker --kernel /var/lib/sandboxd/vmlinux \
  --firecracker /usr/local/bin/firecracker --jailer /usr/local/bin/jailer \
  --policy /etc/sandboxd/policy.yaml \
  --listen 0.0.0.0:7443 --token-file /etc/sandboxd/token \
  --tls-cert /etc/sandboxd/tls/cert.pem --tls-key /etc/sandboxd/tls/key.pem \
  --allowed-host sandbox.example.internal
```

## Flags

### Serving

| Flag | Default | |
|---|---|---|
| `--listen` | a unix socket: `$XDG_RUNTIME_DIR/sandboxd.sock`, else `~/.config/sandbox/sandboxd.sock` | `unix:///path/to/socket`, or `host:port`. A unix socket is made owner-only: only its owner can open it, and that is the access control. |
| `--token-file FILE` | | the bearer token clients must present; at least 16 characters, in a file other users cannot read. Required on a network address. |
| `--tls-cert`, `--tls-key` | | TLS certificate and key (PEM). Required for an address that is not loopback: a token over plain HTTP is a token anyone on the path can reuse. |
| `--allowed-host NAME` | | a `Host` name to answer besides loopback (repeatable). Clients reach the server by this name; any other `Host` is refused, which stops DNS rebinding. |
| `--policy FILE` | the built-in policy | the operator policy ([below](#the-policy-file)). |
| `--audit-log FILE` | `<state-dir>/audit/events.jsonl` | every sandbox's events, as JSONL; `none` keeps no log ([self-hosting.md](self-hosting.md#the-audit-log)). |
| `--metrics-listen HOST:PORT` | off | Prometheus metrics at `/metrics`, on loopback only ([below](#metrics)). |
| `--version` | | print the version and exit. |

### The backend

| Flag | Default | |
|---|---|---|
| `--backend` | | `firecracker` (Linux, KVM), `macos` (the native `container` runtime) or `fake` (in memory, for tests). |
| `--state-dir DIR` | `/var/lib/sandboxd` as root, else `$XDG_DATA_HOME/sandboxd` or `~/.local/share/sandboxd` | images and per-sandbox state. Under the jailer it must share a filesystem with the jail: image disks and volumes are hard-linked in. |
| `--default-image REF` | | the image for requests that name none; overrides the policy file. |
| `--insecure-registry HOST:PORT` | | a registry to pull from over plain HTTP — a local one (repeatable). |
| `--kernel FILE` | | firecracker: the guest kernel (`vmlinux`). |
| `--firecracker FILE` | `firecracker` | firecracker: the VMM binary. |
| `--jailer FILE` | | firecracker: the jailer binary; enables it (root only). |
| `--agent FILE` | `sandbox-guestd` beside `sandboxd` | firecracker: the guest agent put into every image's root disk, so it always matches the server. |
| `--network` | on as root | firecracker: host-enforced egress (root only). Without it every sandbox has no network ([self-hosting.md](self-hosting.md#how-egress-is-enforced)). |
| `--container FILE` | `container` | macos: the `container` CLI. |

### As a node behind a gateway

| Flag | Default | |
|---|---|---|
| `--node-id NAME` | | this `sandboxd`'s name as one node behind a gateway. Every sandbox id it makes carries it (`sbx_n17_0123456789abcdef`), so the gateway routes each later call by the id alone. Lowercase letters, digits and `-`, at most 31. Must equal the name the node is given at the gateway. Without it the node is a standalone `sandboxd`. |
| `--client-ca FILE` | | mutual TLS: a connection must present a certificate this CA signed, or the handshake fails before a request is read. The token is still required. Needs `--tls-cert` and `--tls-key`. |
| `--node-label key=value` | | describes the node at `GET /v1/node` (repeatable). |
| `--capacity-cpus`, `--capacity-memory-mb`, `--capacity-disk-mb` | every CPU; all the memory; the size of the state directory's filesystem | what the node offers sandboxes, and what a gateway's scheduler may place there. Set them lower to leave room for the host. |

What a node is told and how a gateway reaches it is in
[fleet.md](fleet.md#nodes) and
[self-hosting.md](self-hosting.md#as-one-node-behind-a-gateway).

## The policy file

`--policy /etc/sandboxd/policy.yaml` is what requests may ask for. A request
may tighten any of it and never loosen it. Every key is optional — what is
absent keeps the built-in value — and an unknown key is an error, so a
misspelt `celing: none` cannot leave the server more open than its operator
thinks.
[`packaging/systemd/policy.example.yaml`](../packaging/systemd/policy.example.yaml)
is a starting point for a machine shared by a team.

```yaml
default_image: ghcr.io/you/sandbox-base:1
images: [ghcr.io/you/sandbox-base:1]       # only these may be requested
defaults: {cpus: 2, memory_mb: 2048, disk_mb: 20480, idle_timeout_secs: 3600}
limits:   {max_cpus: 8, max_memory_mb: 16384, max_disk_mb: 102400, max_idle_timeout_secs: 86400}
network:
  default: {mode: allowlist, allow: [github.com, registry.npmjs.org], deny: []}
  ceiling: allowlist
  may_allow: ["*.internal.example.com"]
pools:                                     # sandboxes booted ahead of time
  - {image: ghcr.io/you/sandbox-base:1, size: 2}
```

| Key | Built in | |
|---|---|---|
| `default_image` | the project's base image | the image for a request that names none. |
| `images` | any | when set, the only images a request may name. |
| `defaults.cpus`, `.memory_mb`, `.disk_mb`, `.idle_timeout_secs` | 1, 1024, 10240, 1800 | what a request that asks for nothing gets. Each must be within the limits. |
| `limits.max_cpus`, `.max_memory_mb`, `.max_disk_mb`, `.max_idle_timeout_secs` | 8, 16384, 102400, 604800 (a week) | the most a request may ask for. |
| `limits.min_snapshot_every_secs`, `.max_snapshot_keep` | 300 (five minutes), 5 | how often at most a sandbox's schedule may snapshot it, and how many of its scheduled snapshots it may keep. Each is a copy of the sandbox on this machine's disk (about 2 GB of the base image on macOS), so these bound what schedules can cost. At least 1 second. |
| `network.default` | `{mode: open}` | `mode` is `none`, `allowlist` or `open`; `allow` and `deny` are names, wildcards allowed. `deny` wins over `allow`. The default must itself be something the ceiling permits. |
| `network.ceiling` | `open` | the most open mode a request may ask for. |
| `network.may_allow` | `["*"]` | the names a request may add to an allowlist; `[]` allows only the default list. |
| `pools` | none | `{image, size}` per image (image omitted: the default one), size 1 to 32 — sandboxes kept booted so a create is a claim ([self-hosting.md](self-hosting.md#pools)). |

Where a backend cannot deliver what the policy asks — no root, so no network
devices — the ceiling becomes `none` and the startup line says so, and a
request for an allowlist is refused, never served open. How the network
default and the profiles interact is in
[self-hosting.md](self-hosting.md#the-network-default) and
[security/README.md](security/README.md#security-profiles).

## What sandboxd refuses at startup

- **A network address without a token, or without TLS.**
- **A token file other users can read.**
- **A policy file with an unknown key**, or one that does not hold together:
  a default outside the limits, a default network the ceiling would refuse, a
  pool for an image the policy does not permit.
- **`--client-ca` without `--tls-cert` and `--tls-key`.**
- **A `--metrics-listen` address that is not loopback.**

## Node endpoints

`GET /v1/node` reports the node's capacity, what is free (capacity less what
every sandbox not terminated, pooled ones included, has been given), how many
run, the pools by image, the images whose disks are already built, the labels
and whether it is cordoned. `POST /v1/node/cordon` with `{"cordoned": true}`
stops new sandboxes landing on the node (`503 unavailable`) while those
already there carry on. A cordon is held in memory, so a restart clears it.
Both are for a gateway; a gateway does not proxy them to users
([api/v1.md](api/v1.md#node)).

## Metrics

`--metrics-listen 127.0.0.1:9100` serves Prometheus metrics at `/metrics`:
sandboxes by state, processes running, pool sizes by image, capacity and
free, cordon, and creates by status with a latency histogram. It has no
credential, so `sandboxd` refuses any address but loopback; put a proxy with
its own authentication in front if the scraper is elsewhere. The names are
listed in [operations.md](operations.md#metrics).

## Files

| Path | |
|---|---|
| `<state-dir>/audit/events.jsonl` | the audit log, 0600, rotated at 8 MiB with five old generations |
| `<state-dir>/volumes/` | volumes, as sparse ext4 files; never mounted on the host |
| `/etc/sandboxd/token`, `/etc/sandboxd/tls/` | the token and TLS files of the packaged unit |
| [`packaging/systemd/sandboxd.service`](../packaging/systemd/sandboxd.service) | the systemd unit |
| [`packaging/launchd/dev.sandbox.sandboxd.plist`](../packaging/launchd/dev.sandbox.sandboxd.plist) | the macOS launch agent |
