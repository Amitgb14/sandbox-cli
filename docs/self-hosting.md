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
