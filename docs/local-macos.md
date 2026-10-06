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

You need macOS 26 or later on an arm64 Mac, Go 1.25+ and the `container`
runtime (its signed installer package is on
<https://github.com/apple/container/releases>).

**Build from source.** No published release has `sandboxd` yet: 0.0.1 is the
last release of the container design, and `install.sh` refuses it rather than
install half of it. Until the rewrite's first release, build all three
binaries from a checkout:

```sh
git clone https://github.com/Amitgb14/sandbox-cli && cd sandbox-cli
make build        # -> bin/sandbox-cli, bin/sandboxd, bin/sandbox-gateway, bin/sandbox-guestd
file bin/sandbox-guestd   # must say: ELF 64-bit LSB executable, ARM aarch64
```

For [Studio](studio.md), the browser view, run `make studio` (Node 20+)
*before* `make build`: the UI is built into `sandbox-cli`, so a client built
first serves only Studio's API and a page saying how to build the rest.

`sandbox-guestd` is a Linux binary even on a Mac: it runs inside the VM, which
is linux/arm64. `make build` cross-compiles it for Linux on your Mac's
architecture, which must be arm64.

**Install the binaries.** The launch agent runs `/usr/local/bin/sandboxd`, and
`sandboxd` finds the guest agent beside itself, so both go there. That directory
belongs to root and may not exist yet on a new Mac:

```sh
sudo install -d -m 0755 /usr/local/bin
sudo install -m 0755 bin/sandboxd bin/sandbox-guestd /usr/local/bin/
install -m 0755 bin/sandbox-cli ~/.local/bin/   # or anywhere on your PATH
```

**Start the runtime, then sandboxd:**

```sh
container system start        # the runtime's own service; the first run offers to install its kernel
cp packaging/launchd/dev.sandbox.sandboxd.plist ~/Library/LaunchAgents/
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.sandbox.sandboxd.plist
```

If the launch agent was already loaded (you ran `bootstrap` before the binary
was in place, or you are upgrading), restart it instead; `bootstrap` on a loaded
agent fails with `Bootstrap failed: 5: Input/output error`:

```sh
launchctl kickstart -k gui/$(id -u)/dev.sandbox.sandboxd
```

If you installed the launch agent from an earlier build, copy it again. An
older plist sets no `PATH`, so under launchd `sandboxd` cannot find the
`container` CLI in `/usr/local/bin` and exits, and `/tmp/sandboxd.log` fills
with `the container CLI: executable file not found in $PATH`. An even older one
passes `--allow-bind`, which `sandboxd` no longer has, so it refuses to start
rather than run with a flag it does not understand.

**Check it.** `sandboxd` listens on `~/.config/sandbox/sandboxd.sock` (a launch
agent has no `$XDG_RUNTIME_DIR`), which is where `sandbox-cli` looks by default,
so no context needs adding:

```sh
sandbox-cli doctor                          # reaches sandboxd and lists what it offers
sandbox-cli run --network none -- uname -a  # a first sandbox: prints an aarch64 Linux kernel
tail -f /tmp/sandboxd.log                   # sandboxd's log, if either fails
```

**Another sandboxd beside it** (a dev build, say) needs its own
`--state-dir` and `--listen`; *More than one sandboxd*, below, says why.

**Upgrading.** `git pull && make build`, install the two binaries again as
above, then `launchctl kickstart -k gui/$(id -u)/dev.sandbox.sandboxd`.

The guest agent is mounted read-only into every sandbox from beside
`sandboxd`, so any image works and the agent always matches the server.

## What is different from Linux

| | macOS (local) | Linux (self-hosted, cloud) |
|---|---|---|
| Sandbox | VM of the `container` runtime | Firecracker microVM |
| Egress | `none`, or `open` if your policy allows it | `none` or an allowlist, enforced on the host |
| Live network policy change | no | yes |
| Snapshots | the files only (`disk_snapshot`) | whole: memory, processes and disk (`memory_snapshot`) |

**Snapshots.** The runtime cannot capture a running VM's memory, so a snapshot
here is the sandbox's files: exported, wrapped as an image named for this
sandboxd's owner (`sbx-snapshot/<owner>/<id>`) and loaded into the runtime's
store. A sandbox started from one boots afresh with those files, and may ask
for other resources than the original's. Taking one takes about a minute and,
briefly, the size of the sandbox's files in `--state-dir`; the image then
stays until the snapshot is deleted. sandboxd keeps no snapshot records across
a restart, so on start it removes its own leftover snapshot images, and no one
else's.

**Egress.** The runtime's own network is open NAT. Until it is measured whether
an allowlist can be enforced here, this backend does not claim one. A request
for an allowlist is refused, never served open. The default is open, as on
Linux; ask for `--network none` for a sandbox with no network, or set the
default to `none` in a policy file you pass with `--policy`.

**No host directory.** A sandbox has no repository, and nothing of your Mac is
mounted into it but the guest agent, read-only; every process starts in `/sandbox/home`. The bind mount this
backend used to offer (`--allow-bind`) was removed with the repository model.

**More than one sandboxd.** The `container` runtime is one per Mac and shared.
Each sandbox carries its sandboxd's owner label, from its `--state-dir`, and a
sandboxd starting removes only what it left itself; give each sandboxd its own
`--state-dir` (and `--listen`), and they leave each other alone. A sandbox
from a build before owner labels is named in the log and left for you to
remove.

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
