# Running sandboxd locally on a Mac

On macOS 26 or later, on an arm64 Mac, each sandbox is a lightweight VM of the
native `container` runtime: a kernel of its own, one per sandbox. The API is the
same as on a self-hosted Linux machine or in the cloud. The local endpoint is a
unix socket that only you can open.

> **Status:** written and tested on Linux, against a fake runtime that runs the
> real guest agent. It has **not yet run on a Mac**. `scripts/m3/macos/run-all.sh`
> checks everything the backend relies on; its results decide the open points
> below.

## Install

```sh
container system start                      # the runtime's own service
install -m 0755 sandboxd /usr/local/bin/
install -m 0755 sandbox-guestd /usr/local/bin/   # the linux/arm64 build, beside sandboxd
cp packaging/launchd/dev.sandbox.sandboxd.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.sandbox.sandboxd.plist
```

If you installed the launch agent from an earlier build, copy it again: that one
passes `--allow-bind`, which `sandboxd` no longer has, so it refuses to start
rather than run with a flag it does not understand.

The guest agent is mounted read-only into every sandbox from beside
`sandboxd`, so any image works and the agent always matches the server.

## What is different from Linux

| | macOS (local) | Linux (self-hosted, cloud) |
|---|---|---|
| Sandbox | VM of the `container` runtime | Firecracker microVM |
| Egress | `none`, or `open` if your policy allows it | `none` or an allowlist, enforced on the host |
| Live network policy change | no | yes |

**Egress.** The runtime's own network is open NAT. Until it is measured whether
an allowlist can be enforced here, this backend does not claim one. A request
for an allowlist is refused, never served open, and the default is `none`. To
let sandboxes reach the network at all, set `ceiling: open` in a policy file you
pass with `--policy`, knowing that is what it means.

**No host directory.** A sandbox has no repository, and nothing of your Mac is
mounted into it but the guest agent, read-only; every process starts in `/sandbox/home`. The bind mount this
backend used to offer (`--allow-bind`) was removed with the repository model.

## Open points, decided by the M3 macOS run

Seen on a Mac with macOS 26.1 (2026-10-03), `alpine:3.20`: a sandbox with
`--network none` comes up in under a second. Everything below is still to be
checked.

- Whether the runtime supports `--network none` as rendered. If it does not,
  creating a sandbox fails, rather than running one with a network.
- Whether an allowlist can be enforced: in the guest (iptables or nftables), or
  outside it.
- The shape of `container ls --format json`, which is how leftover sandboxes are
  found after a restart.
