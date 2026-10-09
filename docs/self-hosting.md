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
- Room for `/var/lib/sandboxd`, where images, sandboxes' disks, snapshots and
  volumes are kept ([Where it keeps things](#where-it-keeps-things)). It must be
  **one filesystem**: image disks are hard-linked into each sandbox's jail. xfs
  or btrfs make snapshot and fork copies reflinks. To give it a disk of its own,
  mount that disk there before installing.

## Install

On a server, give sandboxes a disk of their own first ([An extra disk for
sandboxes](#an-extra-disk-for-sandboxes), below): images, sandboxes' disks,
snapshots and volumes then never fill the root filesystem. Mount it before
these steps, which put the guest kernel in that directory; a disk mounted
over it afterwards hides it. Every step runs as root.

**1. The binaries: built from a checkout, for now.** No published release has
`sandboxd` yet: 0.0.1 is the last release of the container design, and
`install.sh` refuses it rather than install half of it. Until the rewrite's
first release, build on the server (Go 1.25+) or on any Linux machine of the
same architecture, and copy `bin/` across:

```sh
git clone https://github.com/Amitgb14/sandbox-cli && cd sandbox-cli
make build        # -> bin/sandbox-cli, bin/sandboxd, bin/sandbox-gateway, bin/sandbox-guestd
install -m 0755 bin/sandboxd bin/sandbox-guestd bin/sandbox-cli /usr/local/bin/
```

Once a release has them, `install.sh` does this step instead, with no checkout,
each archive checked against the release's checksums:

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh \
  | sh -s -- --dest /usr/local/bin --no-config
```

`sandbox-guestd` must sit beside `sandboxd` (or be named with `--agent`): it is
put into every image's root disk, so the guest agent always matches the server.

**2. Firecracker, its jailer and a guest kernel.** These come from
Firecracker's own releases, not this repository. The kernel is the one
[What the machine needs](#what-the-machine-needs) describes, such as a CI
kernel from Firecracker's getting-started guide:

```sh
ARCH=$(uname -m)
release_url=https://github.com/firecracker-microvm/firecracker/releases
latest=$(basename $(curl -fsSLI -o /dev/null -w '%{url_effective}' $release_url/latest))
curl -fsSL $release_url/download/$latest/firecracker-$latest-$ARCH.tgz | tar -xz
install -m 0755 release-$latest-$ARCH/firecracker-$latest-$ARCH /usr/local/bin/firecracker
install -m 0755 release-$latest-$ARCH/jailer-$latest-$ARCH /usr/local/bin/jailer

install -d -m 0700 /var/lib/sandboxd
install -m 0644 vmlinux /var/lib/sandboxd/vmlinux
```

**3. The token, TLS, the policy and the unit.** The policy and the unit are
fetched from the repository, so this step needs no checkout either. From one,
`cp packaging/systemd/policy.example.yaml` and `cp
packaging/systemd/sandboxd.service` do the same.

```sh
install -d -m 0700 /etc/sandboxd /etc/sandboxd/tls

# a token clients present; at least 16 characters, readable by root only
head -c 32 /dev/urandom | base64 > /etc/sandboxd/token && chmod 600 /etc/sandboxd/token

# TLS: your CA's certificate for the name clients use
install -m 0600 cert.pem key.pem /etc/sandboxd/tls/

RAW=https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd
curl -fsSL $RAW/policy.example.yaml -o /etc/sandboxd/policy.yaml     # then edit
curl -fsSL $RAW/sandboxd.service -o /etc/systemd/system/sandboxd.service
# --allowed-host: the name clients use
sed -i 's/sandbox.example.internal/box.example.internal/' /etc/systemd/system/sandboxd.service

systemctl daemon-reload && systemctl enable --now sandboxd
journalctl -u sandboxd -f
```

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
disk, a pile of snapshots or a large volume fills that and not `/`. Before
Install, as root:

```sh
lsblk -o NAME,SIZE,TYPE,FSTYPE,MOUNTPOINTS       # find the new disk: no FSTYPE, no mount point (say nvme1n1)
mkfs.xfs /dev/nvme1n1                            # erases it; xfs or btrfs, so snapshot and fork copies are reflinks
mkdir -p /var/lib/sandboxd
echo "UUID=$(blkid -s UUID -o value /dev/nvme1n1) /var/lib/sandboxd xfs defaults,noatime 0 2" >> /etc/fstab
systemctl daemon-reload && mount /var/lib/sandboxd
chmod 0700 /var/lib/sandboxd
restorecon -R /var/lib/sandboxd                  # SELinux hosts (RHEL, Fedora, their rebuilds): /var/lib's label
findmnt /var/lib/sandboxd && df -h /var/lib/sandboxd
```

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
systemctl stop sandboxd
mount /dev/nvme1n1 /mnt/new && rsync -aHAX --sparse /var/lib/sandboxd/ /mnt/new/   # -H keeps the hard links
umount /mnt/new && mount /dev/nvme1n1 /var/lib/sandboxd                            # and add it to /etc/fstab
systemctl start sandboxd
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
install -m 0755 sandboxd sandbox-guestd /usr/local/bin/
systemctl restart sandboxd
journalctl -u sandboxd -n 5      # "keeping sandboxes across restarts; took back N from an earlier run"
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
systemctl stop sandboxd && <install the new sandboxd> && systemctl start sandboxd
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
