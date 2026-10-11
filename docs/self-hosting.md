# Self-hosting sandboxd on Linux

One Linux machine serves the sandbox API. Every sandbox is a Firecracker
microVM with its own kernel, and egress is enforced on the host, where the guest
cannot reach it. The same API is served by the local macOS backend and by the
hosted cloud; clients cannot tell which they are talking to beyond
`/v1/capabilities`.

## What the machine needs

- Linux with KVM (`/dev/kvm`); x86_64 or arm64.
- `firecracker` and `jailer` from the project's releases
  (<https://github.com/firecracker-microvm/firecracker/releases>), 1.17.0
  (below).
- A guest kernel (`vmlinux`) built with `CONFIG_IP_PNP`,
  `CONFIG_VIRTIO_VSOCKETS`, `CONFIG_OVERLAY_FS`, `CONFIG_EXT4_FS`,
  `CONFIG_VIRTIO_BLK` and `CONFIG_VIRTIO_NET`. Firecracker's CI kernel 6.1.155
  has them all, for x86_64 and arm64; Install fetches it.
- `mkfs.ext4` (e2fsprogs 1.43+), `ip` (iproute2), `nft` (nftables).
- **Disk space** where sandboxd keeps its state: `/var/lib/sandboxd` as root,
  `~/.local/share/sandboxd` as a user (the quick try). Images, sandboxes'
  disks, snapshots and volumes are all kept there ([Where it keeps
  things](#where-it-keeps-things)). Measured on a real host:

  | What | Takes |
  |---|---|
  | the base image, installed (layers and its root disk) | 2.5 GiB |
  | a small image, e.g. `python:3.13-slim` | about 120 MiB |
  | each sandbox | what it writes, up to its `disk_mb` (10 GiB unless the policy says otherwise); the disk is sparse |
  | each snapshot | the sandbox's memory plus its written disk: 1 GiB for an idle 1 GiB sandbox |
  | each volume | what was written to it |
  | an upgrade | a second root disk per image until the next start removes the old one |

  **20 GiB free** is enough to try it with the base image and a few
  sandboxes; a server needs a disk of its own ([In production](#in-production)).
  Building from a checkout also takes a few GiB in your home: Go's caches
  (`~/.cache/go-build`, `~/go/pkg/mod`) and about 0.8 GiB of `node_modules`
  for Studio's UI.
- That state directory must be **one filesystem**: image disks are hard-linked
  into each sandbox's jail. xfs or btrfs make snapshot and fork copies
  reflinks. To give it a disk of its own, mount that disk there before
  installing.

### Versions checked

What sandboxd has been run with on a real host, and what has not. A version
not listed may well work; it has not been checked.

| | Checked | Not yet checked |
|---|---|---|
| Firecracker and jailer | 1.17.0 | other releases. Install pins 1.17.0 for that reason |
| Guest kernel | Firecracker CI kernel 6.1.155 (x86_64) | other kernels; the arm64 build of the same kernel |
| Host CPU | x86_64 | arm64: built and released for, never run |
| Host OS and kernel | an EL10 distribution, kernel 6.12, xfs, firewalld active | Debian, Ubuntu and others; kernels before 6.12; btrfs, ext4 |
| Host tools | e2fsprogs 1.47.1, iproute2 6.17.0, nftables 1.1.5; Go 1.25 to build | older versions (e2fsprogs must be 1.43+) |
| Privilege | root under systemd, with the jailer and the egress firewall; and as a user, with no network | — |
| Base image | `ghcr.io/amitgb14/sandbox-base:edge` | other images (the guest agent is put into each, so any Linux image may run) |

The guest kernel is independent of the host's: each sandbox boots the
`vmlinux` sandboxd was given, whatever the host runs. The host needs KVM,
`tun` (a tap device per sandbox, as root) and nftables; any distribution kernel
from recent years has them. Each real-host check, and the version it ran on,
is in [testing/end-to-end.md](testing/end-to-end.md).

macOS is a different backend with its own requirements (macOS 26 on Apple
silicon, the `container` runtime): [local-macos.md](local-macos.md).

## Install

Seven steps, from a bare machine to a sandbox that ran. Each says what it
needs and which version was checked on a real host ([Versions
checked](#versions-checked) has them together), and ends with a command that
shows the step worked. A command that needs root says `sudo`.

**1. Check the machine.** Linux with KVM on x86_64 (checked) or arm64 (built,
not yet run). Checked on an EL10 distribution with host kernel 6.12; the host
kernel only needs KVM, `tun` and nftables, which any recent distribution
kernel has.

```sh
uname -m                     # x86_64 (checked) or aarch64
ls -l /dev/kvm               # must exist; on a cloud VM, nested virtualisation must be on
uname -r                     # checked: 6.12

# the tools sandboxd runs: mkfs.ext4 (e2fsprogs 1.43+; checked 1.47.1),
# ip (iproute2; checked 6.17.0), nft (nftables; checked 1.1.5); and git, make, curl, file to build
sudo dnf install -y e2fsprogs iproute nftables git make curl file      # Fedora, RHEL and rebuilds
sudo apt-get install -y e2fsprogs iproute2 nftables git make curl file # Debian, Ubuntu (not yet checked)

# disk space where the state will be: 20 GiB free to try it, a disk of its own for a server
df -h /var/lib 2>/dev/null; df -h ~     # /var/lib/sandboxd as root, ~/.local/share/sandboxd as a user
```

Go 1.25 or later builds the binaries (`go version`). A distribution's Go is
often older; <https://go.dev/dl/> has the current one. If the root
filesystem is the one nearly full, as is common with a small `/` and a large
`/home`, put the state directory on the larger one: mount a disk at
`/var/lib/sandboxd`, or bind-mount a directory of the larger filesystem
there (step 2), or run sandboxd with `--state-dir` naming it.

**2. Where the state goes: a drive of its own.** Images, sandboxes' disks,
snapshots and volumes live under `/var/lib/sandboxd`. On a server, give them a
drive of their own, so they never fill the root filesystem
([why](#an-extra-disk-for-sandboxes)). Do it now: the next steps put the guest
kernel in that directory, and a drive mounted over it afterwards hides it.
Choose one:

*A. A spare drive, or a partition or logical volume, for the state alone.*
Name it once, check it holds nothing, then format and mount it:

```sh
lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS   # the spare one has no FSTYPE and no MOUNTPOINTS
DISK=/dev/nvme1n1                             # yours, from the list above
lsblk -f $DISK                               # must show no filesystem and no mount point
sudo wipefs -n $DISK                         # must print nothing: no signature on it
sudo mkfs.xfs $DISK                          # ERASES IT; xfs (checked) or btrfs, for reflink copies
sudo install -d -m 0700 /var/lib/sandboxd
echo "UUID=$(sudo blkid -s UUID -o value $DISK) /var/lib/sandboxd xfs defaults,noatime 0 2" \
  | sudo tee -a /etc/fstab                   # by UUID, and without nofail
sudo systemctl daemon-reload && sudo mount /var/lib/sandboxd
sudo chmod 0700 /var/lib/sandboxd
command -v restorecon >/dev/null && sudo restorecon -R /var/lib/sandboxd   # SELinux hosts only
```

*B. No spare drive, but a larger filesystem*, `/home` say. Bind-mount a
directory of it at `/var/lib/sandboxd`. It is one filesystem, as the jail
needs, and the unit waits for it as for a drive:

```sh
sudo install -d -m 0700 /home/sandboxd /var/lib/sandboxd
echo "/home/sandboxd /var/lib/sandboxd none bind 0 0" | sudo tee -a /etc/fstab
sudo systemctl daemon-reload && sudo mount /var/lib/sandboxd
```

*C. A machine just for trying, with room on `/`* (20 GiB free, [What the
machine needs](#what-the-machine-needs)): skip this step.

Then, for A or B:

```sh
findmnt /var/lib/sandboxd    # the drive (A) or /home[/sandboxd] (B)
df -h /var/lib/sandboxd      # the free space is that filesystem's, not /'s
```

If `findmnt` prints nothing after a reboot, the drive did not mount, and
sandboxd will not start until it does (`Dependency failed for sandboxd`):
check the UUID in `/etc/fstab` against `sudo blkid`.

**3. sandboxd and the guest agent: built from a checkout, for now.** No
published release has `sandboxd` yet: 0.0.1 is the last release of the
container design, and `install.sh` refuses it rather than install half of it.
Until the rewrite's first release, build on the server (Go 1.25+) or on any
Linux machine of the same architecture, and copy `bin/` across:

```sh
git clone https://github.com/Amitgb14/sandbox-cli && cd sandbox-cli
make build        # -> bin/sandbox-cli, bin/sandboxd, bin/sandbox-gateway, bin/sandbox-guestd
sudo install -m 0755 bin/sandboxd bin/sandbox-guestd bin/sandbox-cli /usr/local/bin/
sandboxd --version           # sandboxd 0.0.1-<commits>-g<commit>: the commit you built
```

`sandbox-guestd` must sit beside `sandboxd` (or be named with `--agent`): it is
put into every image's root disk, so the guest agent always matches the server.
Once a release has them, `install.sh` does this step instead, with no checkout,
each archive checked against the release's checksums:

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh \
  | sudo sh -s -- --dest /usr/local/bin --no-config
```

**4. Firecracker and its jailer, 1.17.0.** From Firecracker's own releases, not
this repository. 1.17.0 is the version checked, and it is pinned: a newer one
may work, but has not been run.

```sh
ARCH=$(uname -m)
release_url=https://github.com/firecracker-microvm/firecracker/releases
latest=v1.17.0      # the version checked
curl -fsSL $release_url/download/$latest/firecracker-$latest-$ARCH.tgz | tar -xz
sudo install -m 0755 release-$latest-$ARCH/firecracker-$latest-$ARCH /usr/local/bin/firecracker
sudo install -m 0755 release-$latest-$ARCH/jailer-$latest-$ARCH /usr/local/bin/jailer
firecracker --version        # Firecracker v1.17.0
```

**5. The guest kernel, 6.1.155.** Every sandbox boots this kernel, whatever
the host runs. It must be built with `CONFIG_IP_PNP`,
`CONFIG_VIRTIO_VSOCKETS`, `CONFIG_OVERLAY_FS`, `CONFIG_EXT4_FS`,
`CONFIG_VIRTIO_BLK` and `CONFIG_VIRTIO_NET`. Firecracker's CI kernel 6.1.155
has them all, for x86_64 (checked) and arm64. It is pinned: the CI kernels are
published per Firecracker release, and the newest release does not always
have them yet. Any kernel with those options does instead.

```sh
ARCH=$(uname -m)
sudo install -d -m 0700 /var/lib/sandboxd
sudo curl -fsSL -o /var/lib/sandboxd/vmlinux \
  https://s3.amazonaws.com/spec.ccfc.min/firecracker-ci/v1.15/$ARCH/vmlinux-6.1.155
sudo chmod 0644 /var/lib/sandboxd/vmlinux
file /var/lib/sandboxd/vmlinux      # must say ELF 64-bit; anything else is an error page
```

**6. The token, TLS, the policy and the unit.** The policy and the unit are
fetched from the repository, so this step needs no checkout. From one, `cp
packaging/systemd/policy.example.yaml` and `cp
packaging/systemd/sandboxd.service` do the same. The unit runs sandboxd as
root, which the tap devices, nftables and the jailer need; each VM runs under
the jailer with a uid of its own.

```sh
sudo install -d -m 0700 /etc/sandboxd /etc/sandboxd/tls

# a token clients present; at least 16 characters, readable by root only
# (umask 077: the file is never readable by others, not even for a moment)
sudo sh -c 'umask 077; head -c 32 /dev/urandom | base64 > /etc/sandboxd/token'

# TLS: your CA's certificate for the name clients use
sudo install -m 0600 cert.pem key.pem /etc/sandboxd/tls/

RAW=https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd
sudo curl -fsSL $RAW/policy.example.yaml -o /etc/sandboxd/policy.yaml     # then edit
sudo curl -fsSL $RAW/sandboxd.service -o /etc/systemd/system/sandboxd.service
# --allowed-host: the name clients use
sudo sed -i 's/sandbox.example.internal/box.example.internal/' /etc/systemd/system/sandboxd.service

sudo systemctl daemon-reload && sudo systemctl enable --now sandboxd
sudo journalctl -u sandboxd -n 20   # "sandboxd: <version> serving API v1 on …; backend firecracker; …"
```

**7. Check it from a client.** On your laptop, or on the server itself, with
the token and the CA's certificate copied across:

```sh
sandbox-cli context add box https://box.example.internal:7443 --token-file box.token --ca box-ca.pem
sandbox-cli context use box
sandbox-cli doctor           # backend firecracker, the network ceiling, the capabilities
sandbox-cli run -- uname -r  # 6.1.155+: the guest kernel, not the host's
```

The first run pulls the base image and builds its root disk, a minute or more;
`sandbox-cli image pull` does that ahead ([Images](#images)). [Checking it
works](#checking-it-works) runs the conformance suite against the server.

## On an IP address instead of a socket

sandboxd listens where `--listen` says. Started with no `--listen`, as in the
quick try, it serves a unix socket only its owner can open,
`$XDG_RUNTIME_DIR/sandboxd.sock`, which is the CLI's `local` context. Given
`HOST:PORT`, it serves TCP, and what it requires depends on who can reach that
address:

| `--listen` | Reachable by | sandboxd requires | The client uses |
|---|---|---|---|
| none (a unix socket) | you alone | nothing more | the `local` context |
| `127.0.0.1:PORT` | every user on this machine | `--token-file` | `http://127.0.0.1:PORT` and the token |
| any other address, `0.0.0.0` included | other machines | `--token-file`, `--tls-cert` and `--tls-key`, and `--allowed-host` for each name or IP clients use | `https://ADDRESS:PORT`, the token and the CA's certificate |

Without them it does not start, and says which is missing:

```text
sandboxd: --listen 10.0.0.17:7443 is reachable from other machines; refusing to serve it without --token-file
sandboxd: --listen 10.0.0.17:7443 is reachable from other machines; refusing to serve it without --tls-cert and --tls-key
```

**On loopback**, for other programs or users on the same machine. As yourself,
with the kernel and Firecracker from the quick try:

```sh
sh -c 'umask 077; head -c 32 /dev/urandom | base64 > ~/sandboxd.token'
sandboxd --backend firecracker \
  --kernel ~/.local/share/sandboxd/vmlinux --firecracker ~/.local/bin/firecracker \
  --listen 127.0.0.1:7443 --token-file ~/sandboxd.token
# its last line: "serving API v1 on tcp://127.0.0.1:7443; … token required"

sandbox-cli context add lo http://127.0.0.1:7443 --token-file ~/sandboxd.token
sandbox-cli context use lo && sandbox-cli doctor
```

**On an IP address**, for other machines. The certificate must name the IP
clients dial (an IP SAN). [`packaging/fleet/make-certs.sh`](../packaging/fleet/make-certs.sh)
makes one, with a private CA to sign it; a certificate from a CA your clients
already trust works as well. Here the server's address is `10.0.0.17`:

```sh
IP=10.0.0.17
curl -fsSLO https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/fleet/make-certs.sh
sh make-certs.sh -o certs $IP     # certs/ca.pem, certs/node-$IP.pem and its key
                                  # (and a gateway client certificate, unused here)
sh -c 'umask 077; head -c 32 /dev/urandom | base64 > ~/sandboxd.token'

sandboxd --backend firecracker \
  --kernel ~/.local/share/sandboxd/vmlinux --firecracker ~/.local/bin/firecracker \
  --listen $IP:7443 --token-file ~/sandboxd.token \
  --tls-cert certs/node-$IP.pem --tls-key certs/node-$IP-key.pem \
  --allowed-host $IP
# its last line: "serving API v1 on https://10.0.0.17:7443; … token required"

# if a firewall is on, open the port (not part of the check below)
sudo firewall-cmd --add-port=7443/tcp --permanent && sudo firewall-cmd --reload   # firewalld
sudo ufw allow 7443/tcp                                                            # ufw
```

On each client, with `~/sandboxd.token` and `certs/ca.pem` copied across
privately (the token is the whole credential):

```sh
sandbox-cli context add box https://10.0.0.17:7443 --token-file sandboxd.token --ca ca.pem
sandbox-cli context use box && sandbox-cli doctor
sandbox-cli run -- uname -r       # 6.1.155+
```

- **`--allowed-host`** lists every name or IP clients put in the URL. A request
  naming anything else is refused, which stops DNS rebinding. With
  `--listen 0.0.0.0:7443`, which serves every address of the machine, give one
  `--allowed-host` for each address clients use, and a certificate naming each.
- **Keep `certs/ca-key.pem` off the server** once the certificate is made: it
  can sign a certificate for any address, which every client given `ca.pem`
  would trust.
- **Run as yourself, sandboxes get no network**, as in the quick try. Serving
  on an IP changes who reaches the API, not what a sandbox reaches. For an
  enforced egress allowlist, run it as root under systemd, as in Install,
  where step 6 sets the same flags in the unit: `--listen 0.0.0.0:7443` and
  `--allowed-host`, which `sed` sets to the name or IP clients use.

Checked on a real host (x86_64 EL10, Firecracker 1.17.0, sandboxd as a user):
both refusals above; loopback with a token; the LAN address with a token, a
`make-certs.sh` certificate and `--allowed-host`, from `context add` through
`doctor` and a sandbox's `uname -r`, and 401 without the token. Not checked:
the firewall lines, and a client on another machine.

## Where it keeps things

Everything sandboxd keeps is under its state directory: `--state-dir`, by
default `/var/lib/sandboxd` as root (`~/.local/share/sandboxd` otherwise). The
binaries, `/etc/sandboxd` (token, TLS, policy) and the unit file are the only
things elsewhere.

| Path under the state directory | What | Size |
|---|---|---|
| `vmlinux` | the guest kernel (Install, above) | tens of MiB |
| `images/` | image layers and manifests pulled from registries, shared between images | the images, compressed |
| `rootfs/<key>/` | each image's root disk (`rootfs.ext4`), built once per image and guest agent and read-only to every sandbox, beside `image.json`: the image names it serves, its digest and its layers, which is how the installed images are listed after a restart | a few GiB each; an upgrade's first sandbox of an image builds a new one, and the old are removed at the next start |
| `sandboxes/<id>/` | a sandbox's own files: its writable disk (`scratch.ext4`, its `disk_mb`, sparse) and sockets | what each sandbox writes, up to its `disk_mb` |
| `jail/` | with `--jailer`: each VM's chroot, holding hard links to the kernel and root disk, and its writable disk | as `sandboxes/`; the links take no space |
| `snapshots/<id>/` | a snapshot: the sandbox's disk, VM state and memory (`snap.mem`) | the sandbox's memory plus its written disk |
| `volumes/<name>.ext4` | named volumes, sparse | what was written to each, up to its size |
| `records/` | with `--keep-sandboxes`: each sandbox's record, environment values included, so they survive a restart | small; **sensitive**, readable by root only |
| `audit/events.jsonl` | the audit log, unless `--audit-log` puts it elsewhere | grows with use; [rotate it](#the-audit-log) |

What it takes to plan a disk: a few GiB per image you run, plus each running
sandbox's written disk, plus each snapshot's memory and disk, plus volumes.
The fleet's `Disk given` in Studio, and `sandbox-cli list`, show what
sandboxes were given; `du -sh /var/lib/sandboxd/*` shows what is used.

### An extra disk for sandboxes

What a Linux server running sandboxes for others should have: a disk, or a
logical volume, mounted at the state directory, so a sandbox that fills its
disk, a pile of snapshots or a large volume fills that and not `/`. The
commands, for a spare drive or a bind mount of a larger filesystem, are
[Install, step 2](#install); mount it before the rest of Install. Why they
are written as they are:

- **By UUID, not by name.** `nvme1n1` and `sdb` can swap between boots; a
  UUID names the filesystem.
- **Not `nofail`.** The unit (`packaging/systemd/sandboxd.service`) has
  `RequiresMountsFor=/var/lib/sandboxd`, so if the disk does not mount,
  sandboxd does not start. With `nofail` and without that line, it would start
  on the empty directory underneath and fill `/` instead, and the sandboxes it
  kept across a restart would be missing. A state directory elsewhere
  (`--state-dir`) needs the same line naming it.
- **The whole directory**, never one of its subdirectories (below).
- **Nodes behind a gateway** each get their own, the same way: a node's state
  is its own and is never shared ([fleet.md](fleet.md#nodes)).

### In production

The extra disk is not optional on a server others depend on, and these come
with it:

- [ ] **A local disk of its own at the state directory**, mounted by UUID
  before sandboxd first starts, the whole directory on it
  ([above](#an-extra-disk-for-sandboxes)). Local, not a network filesystem:
  every sandbox's disk reads and writes go to it.
- [ ] **The unit waits for it**: `RequiresMountsFor=` naming the state
  directory, as `packaging/systemd/sandboxd.service` has, and no `nofail`.
- [ ] **Room kept back for what sandboxes are not given.** sandboxd offers the
  whole filesystem by default, and counts against it only the `disk_mb` each
  sandbox was given. Images and their root disks (a few GiB each, twice over
  during an upgrade), snapshots (each one's memory plus disk) and volumes come
  out of the same disk without being counted. Offer less with
  `--capacity-disk-mb`: on a 1 TiB disk with a handful of images and scheduled
  snapshots, `--capacity-disk-mb 700000` is a starting point.
- [ ] **An alert on the disk's real free space**, not on sandboxd's metrics.
  `sandboxd_free_disk_mb` is disk not yet given to a sandbox, not space left
  on the filesystem. Watch the filesystem itself, e.g. node_exporter's
  `node_filesystem_avail_bytes{mountpoint="/var/lib/sandboxd"}`, and alert
  well before it runs out (15% left, say): a full disk fails new sandboxes,
  snapshots and every write inside running ones.
- [ ] **Growing it needs a restart.** sandboxd measures the filesystem once,
  at start. Grow the volume (LVM, or the cloud disk), `xfs_growfs
  /var/lib/sandboxd`, raise `--capacity-disk-mb` if it is set, then
  `systemctl restart sandboxd`. With [`--keep-sandboxes`](#upgrading-without-stopping-sandboxes)
  running sandboxes carry on.
- [ ] **The audit log rotated**, or kept on another disk with `--audit-log`
  ([The audit log](#the-audit-log)); it grows with every request.
- [ ] **Backups of what cannot be rebuilt.** Images and root disks are
  pulled and built again; volumes and snapshots are not. Copy a volume's
  `.ext4` while no sandbox has it mounted ([Volumes](#volumes)). `records/`
  holds environment values: back it up only where the backup is as private as
  `/etc/sandboxd`.
- [ ] **One disk per node.** Behind a gateway, each node's state directory is
  its own and is never shared between nodes.

**Keep it one filesystem.** Root disks, the kernel and volumes are hard-linked
into each jail, so mounting `jail/`, `rootfs/` or `volumes/` separately breaks
every start (`linking … into the jail (the jail must be on the same filesystem
as the image cache)`). Put the whole state directory on the disk instead. The
audit log alone can go elsewhere, with `--audit-log`.

**Moving an existing install** to a new disk: the VMs run from files in the
state directory, so end them first. With `--keep-sandboxes`, stopping
sandboxd leaves them running from the old disk, and a copy taken under them is
not a consistent one.

```sh
sandbox-cli list                              # what is running; finish what matters first
sandbox-cli kill SANDBOX...                   # every live one; snapshots and volumes are files, and move with the rest
sudo systemctl stop sandboxd
sudo mount /dev/nvme1n1 /mnt/new && sudo rsync -aHAX --sparse /var/lib/sandboxd/ /mnt/new/   # -H keeps the hard links
sudo umount /mnt/new && sudo mount /dev/nvme1n1 /var/lib/sandboxd                            # and add it to /etc/fstab
sudo systemctl start sandboxd
```

## The network default

A Firecracker sandbox's egress is always by name: there is no open mode on
this backend, because everything a guest sends goes through sandboxd's proxy
and resolver on the host (below), and they let through only names on the
sandbox's allowlist. So with no policy file — whose built-in default is open —
or with a policy that asks for `open`, the default becomes the **allowlist**
and so does the ceiling; the startup line says so. A run adds the names it
needs with `--allow host` (`--allow '*.example.com'` for a domain and every
name under it), and the built-in `may_allow: ["*"]` lets it add any. The
`prod` profile always uses an allowlist.

An allowlist entry is a name, never a bare `*`, so "every site" is not
something a run can ask for. A browser in a sandbox needs the page's own
host and every host it loads from — its CDN, fonts, sign-in provider. A name
that is refused is logged (`egress denied: …` in sandboxd's log). An agent run always has its own API on the list, because an
agent that cannot reach its model cannot run at all, so claude reaches
api.anthropic.com, codex api.openai.com, and so on. Every other name has to be
asked for, and under `prod` the built-in list of registries is off too.

A machine shared by a team usually wants to choose what every run may reach:
`packaging/systemd/policy.example.yaml` sets the default list, and
`may_allow` narrows the names a run may add. Without root,
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
traffic you have. A restart of sandboxd discards them, with every other VM
unless [`--keep-sandboxes`](#upgrading-without-stopping-sandboxes) is on; even
then pooled sandboxes are discarded, and the pool refills.

## Upgrading without stopping sandboxes

By default, stopping sandboxd ends every sandbox it runs, and starting it
removes any it finds left over. With `--keep-sandboxes`, it leaves them
running when it exits and takes them back when it starts again on the same
state directory. So installing a new sandboxd, or restarting it, interrupts
no VM:

```sh
git pull && make build                       # in the checkout, until a release has sandboxd
sudo install -m 0755 bin/sandboxd bin/sandbox-guestd /usr/local/bin/
sudo systemctl restart sandboxd
sudo journalctl -u sandboxd -n 5      # "keeping sandboxes across restarts; took back N from an earlier run"
```

**What carries on:** each VM, with its memory, its disk and every file in
it, a desktop and what it shows, its network policy, labels, name, snapshot
schedule and environment, and every snapshot, still listed and still usable
to start a sandbox from. A suspended sandbox stays suspended. The VM is not
rebooted.

Its running processes carry on too, under the same numbers: commands,
terminals and agent sessions. With the flag on, sandboxd starts every process
as a *kept* process. If the host's connection to it goes, the guest agent
keeps it running instead of ending it, and holds the newest 1 MiB of its
output. The next sandboxd re-attaches each process its records name. A
terminal can then attach again (`sandbox-cli attach`, Studio), and the
output replays from what the guest held. Following a process's output after
a restart shows that 1 MiB at most, and the log says when older output was
dropped. A process that finished while sandboxd was down is listed with its
exit code. One whose session the guest no longer holds is listed as exited
with -1.

**What ends:** for about a second, a sandbox can reach nothing: connections
through the egress proxy drop, and new ones succeed once the new sandboxd is
serving. Metrics restart from none. A VM started by a sandboxd from before kept
processes has the guest agent of that time, so its processes end at the
first upgrade; from then on they carry on. A client connected to a process
(an attached terminal, a followed output) is disconnected by the restart and
attaches again.

**What it needs:**

- The systemd unit must say `KillMode=process`, as the packaged one does.
  The default, `control-group`, kills every process of the unit when it
  stops, VMs included.
- **Environment values and running processes' command lines are written to
  disk.** A sandbox taken back needs the environment it was created with,
  secrets included, and its processes are listed with their command lines,
  which can hold secrets too (an agent's argument is its prompt). They are kept in
  `<state-dir>/records/`, one file per sandbox, mode 0600 in a 0700
  directory, like the token. sandboxd refuses to start if others can read
  that directory. A record is deleted when its sandbox ends, and starting
  sandboxd without `--keep-sandboxes` deletes them all. Without the flag,
  environment values are only ever held in memory.
- Only the Firecracker backend can keep sandboxes. Asked of another,
  sandboxd refuses to start rather than stop them anyway.

**What is checked before a sandbox is taken back.** A VM must be
provably the one an earlier sandboxd left. Its record is intact, its VMM is
the same process (pid and start time, so a recycled pid is never mistaken
for it), its network device still exists, and its guest agent answers. It
must also be one sandboxd has a record for, and its network policy must
still be allowed by the current policy file. Anything else is ended and
removed, as it is without the flag. So tightening the policy and then
restarting does not let an older, wider sandbox carry on. Turning
networking or the jailer on or off between runs ends the sandboxes that
were started without it.

**Nothing is left behind unmanaged.** Every VM is checked at once, and each
guest gets the boot timeout (30 s) to answer, so a guest slow on a loaded
host is not mistaken for a broken one. After that, sandboxd sweeps its state
directory, with or without the flag. It ends any firecracker process working
there that no taken-back sandbox accounts for, such as one a crash
mid-resume left unrecorded. It also deletes `sbx` network devices and jail
directories that nothing owns. Only firecracker processes whose working
directory is in this state directory are touched. One line says what
happened: `after the restart: N sandbox(es) taken back, M stray VMM(s)
ended`, and each sandbox removed is logged with the reason.

To stop everything for good, end the sandboxes first (`sandbox-cli kill`),
or restart once without `--keep-sandboxes`.

## As one node behind a gateway

Several machines can serve one API: a gateway in front, each `sandboxd` a node
behind it ([fleet.md](fleet.md) sets up both). Users reach only the gateway. A node should listen **only on the
private network** the gateway shares with it, never on an address users can
reach, and accept only the gateway:

```sh
sudo sandboxd --backend firecracker --listen 10.0.0.17:7443 \
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

`--metrics-listen 127.0.0.1:9100` serves Prometheus metrics at `/metrics`:
sandboxes by state, processes running, pool sizes by image, capacity and
free, cordon, and creates by status with a latency histogram. It has no
credential, so `sandboxd` refuses any address but loopback; put a proxy with
its own authentication in front if the scraper is elsewhere.

### Upgrading nodes behind a gateway

One node at a time, from a machine whose current context holds a gateway
admin key:

```sh
sandbox-cli gateway drain n17               # cordon; prints how many sandboxes still run there
sandbox-cli gateway drain n17               # again, until it says 0 — or end them now:
sandbox-cli gateway drain n17 --terminate
sudo systemctl stop sandboxd && <install the new sandboxd> && sudo systemctl start sandboxd
sandbox-cli gateway nodes                   # n17 healthy, cordoned, on the new version
sandbox-cli gateway uncordon n17
```

A node running with `--keep-sandboxes` can be upgraded without draining:
its sandboxes answer `503 unavailable` while it restarts, then the gateway
finds them again in the node's listing, with their running processes
([above](#upgrading-without-stopping-sandboxes)).

The gateway remembers that it cordoned the node: the restart clears the
node's own cordon, and the gateway puts it back on its next poll and places
nothing there in between, so the node takes new sandboxes only once you
uncordon it. Without the CLI, the same calls are `POST
/v1/admin/nodes/{name}/drain` with `{"terminate": false|true}` and `POST
/v1/admin/nodes/{name}/cordon` with `{"cordoned": false}`.

A node that stops answering is not drained: calls on its sandboxes answer
`503 unavailable`, and after `--node-lost-after` (5 minutes by default) its
sandboxes are listed by `sandbox-cli gateway lost` and stop counting against
their tenants' quotas. They are not reported terminated, because the node may
come back with them running; when it answers again they are reconciled from
its own listing. Running several gateway replicas, and network isolation
between tenants across nodes, are not done yet: today one gateway process
holds its state file.

## Snapshots and restarts

The list of snapshots is sandboxd's own. Their files, a guest's memory and
disk taking gigabytes each, are under `<state-dir>/snapshots/`. With
`--keep-sandboxes`, each snapshot's record is kept in
`<state-dir>/records/snapshots/`, and a restart lists the snapshots again.
Files no record names are deleted, since nothing could use or remove them.
Without the flag, a restart deletes every snapshot an earlier run left, for
the same reason, and the log says how many.

## Images

### Choosing the image a sandbox starts from

Every sandbox starts from an image. Name one with `--image` when it is
created; without it, the sandbox gets the server's default
(`ghcr.io/amitgb14/sandbox-base:edge` unless the operator set another):

```sh
sandbox-cli run --image python:3.13-slim -- python3 -V            # a public image from Docker Hub
sandbox-cli run --image ghcr.io/amitgb14/sandbox-desktop:edge --memory 2048 -- true
sandbox-cli run --keep --name dev --image node:22 -- sleep infinity
sandbox-cli agent claude --image ghcr.io/you/agent-image:1         # an agent needs its tools in the image
sandbox-cli list                                                   # the IMAGE column says what each started from
```

The same choice elsewhere: the Playground's **Start from → An image** in
Studio; `"image"` in `POST /v1/sandboxes` ([api/v1.md](api/v1.md));
`create_sandbox(image=…)` in the Python SDK and `createSandbox({image})` in
the TypeScript one; `image:` in a job or a service's spec
([jobs.md](jobs.md), [services.md](services.md)).

**Which names work.** Any public Linux image, for the server's architecture,
from any registry that serves anonymous pulls:

| You write | It pulls |
|---|---|
| `alpine`, `alpine:3.20`, `python:3.13-slim` | Docker Hub's official image; the tag is `latest` when none is given |
| `team/app:1` | Docker Hub, `team/app` |
| `ghcr.io/owner/name:tag` | that registry, that repository |
| `name@sha256:…` | exactly that build, whatever its tags point at now |

Lowercase only, as registries require. sandboxd pulls **without
credentials**, so a private image cannot be used: the registry refuses it,
as it does a name that does not exist (`token request: 403 Forbidden` on
ghcr.io). Check the spelling, or make the image public.

**What the image needs.** Nothing of ours: the guest agent is put into every
image's root disk. Processes run as the sandbox user (uid 1001) and start in
`/sandbox/home`, whichever image it is. The image's `ENV` applies, so a `PATH`
of its own keeps working, except `HOME` and `USER`; its `USER`, `WORKDIR` and
entrypoint are not used: a sandbox runs the command you give it. That command,
or the agent, must be in the image or installable from it (an agent that is
not in it is installed once per endpoint, [README](../README.md#coding-agents)).
The base image is `node:22` with Python 3, git, curl, build tools, ripgrep,
jq and rsync, and four agents ready to run: claude, codex, gemini and
opencode.

**The first sandbox of an image waits.** It is pulled, and on Linux built
into a root disk, the first time a sandbox asks for it: seconds for a small
image, a minute or more for a large one. Later sandboxes start at once.
`sandbox-cli image pull IMAGE` does it ahead (below).

**What the server allows.** Where the operator set an `images:` list in the
policy, only those may be named, written exactly as listed; any other is
refused (`image "…" is not one this server permits`). `sandbox-cli doctor`
shows the server's limits; the operator's settings are in
[sandboxd.md](sandboxd.md).

Checked on a real host (Firecracker 1.17.0, as a user): `python:3.13-slim`
from Docker Hub pulled in 3 s and ran `python3` as uid 1001 in
`/sandbox/home`; a misspelt ghcr.io name was refused at the token request.

### Installing and removing images

A sandbox's image is pulled, and on Linux built into a root disk, the first
time a sandbox asks for it; that first sandbox waits for both, a minute or
more. The operator can do it ahead of time, and see and clear what is there:

```sh
sandbox-cli image pull ghcr.io/amitgb14/sandbox-desktop:edge   # waits; --no-wait returns at once
sandbox-cli image ls                                            # state, size, sandboxes using each
sandbox-cli image rm python:3.13-slim                           # refused while a sandbox uses it
```

Studio's Images screen does the same, with each install's progress. The
policy's `images:` list, where there is one, limits what may be installed as it
limits what may run. The default image and pooled ones are not removed. A
removal frees the image's root disk once no other tag of it needs it, and the
cached layers no installed image still uses, leaving any written in the last
ten minutes, which a create may be about to use. These are the operator's:
whoever holds this sandboxd's token. Behind a gateway they stay on each node
([fleet.md](fleet.md)).

## Root disks and upgrades

Each image's root disk is built once and cached under
`<state-dir>/rootfs/`, a few gigabytes each. The guest agent is part of the
disk, and every sandboxd build has its own, so after an upgrade the first
sandbox of each image builds a new disk (about a minute). At every start,
sandboxd removes the disks built for any other guest agent, except those a
sandbox it took back is still running or suspended from. The log says how
many it removed and how much space that freed. So the cache holds the
current build's disks and not one set per upgrade.

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

**The host ports.** The proxy listens on TCP 3128 and the resolver on UDP
7353, on every host address (a connection that was not redirected is
dropped, as above). If another program on the host already holds one —
3128 is also Squid's port — sandboxd refuses to start and names the port;
move it with `--egress-proxy-port` or `--egress-dns-port`. The guest never
sees either: it sends to 53, 80 and 443 and is redirected.

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
