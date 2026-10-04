# Self-hosting sandboxd on Linux

One Linux machine serves the sandbox API. Every sandbox is a Firecracker
microVM with its own kernel, and egress is enforced on the host, where the guest
cannot reach it. The same API is served by the local macOS backend and by the
hosted cloud; clients cannot tell which they are talking to beyond
`/v1/capabilities`.

## What the machine needs

- Linux with KVM (`/dev/kvm`); x86_64 or arm64.
- `firecracker` and `jailer` from the project's releases
  (<https://github.com/firecracker-microvm/firecracker/releases>).
- A guest kernel (`vmlinux`) with `CONFIG_IP_PNP`, `CONFIG_VIRTIO_VSOCKETS` and
  overlayfs. The CI kernels from Firecracker's getting-started guide have all
  three.
- `mkfs.ext4` (e2fsprogs 1.43+), `ip` (iproute2), `nft` (nftables).
- The `/var/lib/sandboxd` filesystem: image disks are hard-linked into each
  sandbox's jail, so the jail and the image cache must share it. xfs or btrfs
  make per-sandbox disks reflinks.

## Install

```sh
install -m 0755 sandboxd sandbox-guestd /usr/local/bin/
install -m 0755 firecracker jailer /usr/local/bin/
install -d -m 0700 /etc/sandboxd /etc/sandboxd/tls /var/lib/sandboxd
install -m 0644 vmlinux /var/lib/sandboxd/vmlinux

# a token clients present; at least 16 characters, readable by root only
head -c 32 /dev/urandom | base64 > /etc/sandboxd/token && chmod 600 /etc/sandboxd/token

# TLS: your CA's certificate for the name clients use
install -m 0600 cert.pem key.pem /etc/sandboxd/tls/

cp packaging/systemd/policy.example.yaml /etc/sandboxd/policy.yaml   # then edit
cp packaging/systemd/sandboxd.service /etc/systemd/system/           # set --allowed-host
systemctl daemon-reload && systemctl enable --now sandboxd
journalctl -u sandboxd -f
```

`sandbox-guestd` must sit beside `sandboxd` (or be named with `--agent`): it is
put into every image's root disk, so the guest agent always matches the server.

## The network default

With no policy file, a sandbox gets **open** egress unless the run asks for
less. That is the easy default for one person's machine. A run asks for an
allowlist with `--network allowlist` (or `--allow host`), and the `prod`
profile always uses one: then only the names on it get through, checked on
the host by name. An agent run always has its own API on the list, because an
agent that cannot reach its model cannot run at all, so claude reaches
api.anthropic.com, codex api.openai.com, and so on. Every other name has to be
asked for, and under `prod` the built-in list of registries is off too.

A machine shared by a team usually wants the allowlist as the floor:
`packaging/systemd/policy.example.yaml` sets it as the default with an
`allowlist` ceiling, so no request can ask for open. Without root,
`sandboxd` has no network devices at all, and every sandbox gets none.

## What sandboxd refuses

- **A network address without a token, or without TLS.** A bearer token over
  plain HTTP is a token anyone on the path can reuse.
- **A token file other users can read.**
- **A policy file with an unknown key,** so a misspelt `celing: none` cannot
  leave the server more open than its operator thinks.
- **Egress it cannot enforce.** Without root there are no tap devices, so the
  ceiling becomes `none` and the startup line says so. A request for an
  allowlist is then refused, never served open.

## Pools

`pools: [{size: 2}]` in the policy file keeps two sandboxes of the default image
booted ahead of requests. A create that names nothing fixed at boot beyond
that image is then a claim rather than a boot, under a millisecond rather than
about 100 ms, and the pool refills behind it. Requests may still differ in
environment, name, labels and idle timeout, because the server applies those.
Anything else boots fresh: other resources, another network policy, volumes or
a snapshot.

```yaml
pools:
  - {size: 2}                                   # the default image
  - {image: ghcr.io/you/sandbox-base:1, size: 1}
```

Every pooled sandbox holds its memory while it waits, so size pools to the
traffic you have. A restart of sandboxd discards them with every other VM.

## As one node behind a gateway

Several machines can serve one API: a gateway in front, each `sandboxd` a node
behind it ([fleet.md](fleet.md) sets up both). Users reach only the gateway. A node should listen **only on the
private network** the gateway shares with it, never on an address users can
reach, and accept only the gateway:

```sh
sandboxd --backend firecracker --listen 10.0.0.17:7443 \
  --token-file /etc/sandboxd/token \
  --tls-cert /etc/sandboxd/tls/cert.pem --tls-key /etc/sandboxd/tls/key.pem \
  --client-ca /etc/sandboxd/tls/gateway-ca.pem \
  --node-id n17 --node-label region=west --node-label disk=nvme \
  --allowed-host 10.0.0.17
```

- **`--node-id`** names the node: every sandbox id it makes carries it
  (`sbx_n17_0123456789abcdef`), so the gateway routes each later call by the
  id alone. Lowercase letters, digits and `-`, at most 31. Without it the node
  is a standalone `sandboxd` and its ids are as before.
- **`--client-ca`** turns on mutual TLS: a connection must present a
  certificate this CA signed, or the handshake fails before a request is read.
  The token is still required on every request. It needs `--tls-cert` and
  `--tls-key`; `sandboxd` refuses to start with it alone.
- **`--node-label key=value`** (repeatable) describes the node to the gateway.
- **`--capacity-cpus`, `--capacity-memory-mb`, `--capacity-disk-mb`** are what
  the node offers sandboxes. The defaults are every CPU, all the memory
  (`/proc/meminfo`) and the size of the state directory's filesystem; set them
  lower to leave room for the host.

`GET /v1/node` reports the node's capacity, what is free (capacity less what
every sandbox not terminated, pooled ones included, has been given), how many
run, the pools by image, the images whose disks are already built, the labels
and whether it is cordoned. `POST /v1/node/cordon` with `{"cordoned": true}`
stops new sandboxes landing on the node (`503 unavailable`) while those
already there carry on. A cordon is held in memory, so a restart clears it;
the gateway sees that on its next poll and cordons the node again if it still
means to.

## Volumes

Volumes are sparse ext4 files under `<state-dir>/volumes/`, attached to a VM as
one more drive. **sandboxd never mounts one on the host.** It is a filesystem a
guest wrote, and parsing it is a guest kernel's job, where a malformed one costs
that VM and nothing else. Back them up by copying the `.ext4` file while no
sandbox has it mounted (`sandbox-cli volume ls` shows which do). Under the
jailer, the state directory must be on the same filesystem as the jail, because
each volume is hard-linked in.

## The audit log

Every sandbox's events are appended to `<state-dir>/audit/events.jsonl` (mode
0600): each create with its policy, labels and environment variable **names**,
every process with its program, argument count, a SHA-256 of its arguments
and its exit code, files read and written, network changes, and how the
sandbox ended. Clients read a sandbox's events with
`GET /v1/sandboxes/{ref}/events`, or `sandbox-cli events <id>`. Environment
values and a process's arguments are never written: an agent's arguments are
its prompt. The hash still matches a known command (`docs/api/v1.md`).

- `--audit-log /var/log/sandboxd/events.jsonl` puts it elsewhere;
  `--audit-log none` keeps no log, and capabilities then say `audit: false`.
- It rotates at 8 MiB and keeps five old generations (`events.jsonl.1` … `.5`),
  about 40 MiB in all. Ship it elsewhere if you need more history than that.
- It is **best-effort**: if the file cannot be written, the request still
  succeeds. If your compliance rules say an unrecorded action must not happen,
  that is the wrong trade for you. Tell us, because it would be a flag, not a
  rewrite.

## How egress is enforced

Each sandbox gets a tap device on its own /30. In nftables table
`inet sandboxd`:

- the guest's tcp/80 and tcp/443 are redirected to one proxy in sandboxd. It
  decides by the name the client sends (TLS SNI, or the HTTP Host header),
  against that sandbox's allowlist, and then resolves the name itself;
- the guest's DNS is redirected to a resolver in sandboxd. It answers
  allowlisted names with the host's address and refuses every other name. It
  forwards nothing, so DNS is not a way out;
- everything else from the tap is dropped, and nothing is ever forwarded.

`deny` wins over `allow`, wildcards included. A policy update on a running
sandbox is one atomic change in each place.

**Coexisting with a host firewall.** Every packet the guest may send the host
arrives DNAT'd. firewalld and similar firewalls typically accept DNAT'd traffic
and reject the rest, so they let exactly that through. sandboxd never assumes
its table is the only one. A connection to the proxy or resolver port that was
*not* redirected is dropped, so neither is reachable from other machines.

**What allowlist mode does not cover:** traffic that is not on tcp/80, tcp/443
or DNS. `ssh` to a git host, for example, is dropped.

## Checking it works

From a client with the token:

```sh
SANDBOX_CONFORMANCE_ENDPOINT=https://sandbox.example.internal:7443 \
SANDBOX_CONFORMANCE_TOKEN=$(cat token) \
  go test ./internal/api/conformance -run TestEndpoint -v
```

On the host, the microVM tests (as root, for networking and the jailer):

```sh
sudo -E SANDBOX_TEST_KERNEL=/var/lib/sandboxd/vmlinux \
  SANDBOX_TEST_FIRECRACKER=$(command -v firecracker) \
  SANDBOX_TEST_JAILER=$(command -v jailer) \
  SANDBOX_TEST_NETWORK=1 SANDBOX_TEST_INTERNET=1 \
  SANDBOX_TEST_IMAGE=ghcr.io/amitgb14/sandbox-base:edge \
  go test -tags vm -v ./internal/backend/firecracker
```
