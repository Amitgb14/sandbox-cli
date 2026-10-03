# M3 — measure before building

These scripts answer the questions in `docs/rewrite/PLAN.md` (M3) on real
hardware, before the backends are built on assumptions. They change nothing on
the host that outlives them: everything is under `/tmp/m3`, and the network
script removes its tap device, nftables table and proxy on exit, whatever
happens.

Each run writes one results file and prints it at the end. **That file is what to
send back.** It records the host's OS, CPU and versions; read it before sending.

## Linux (self-hosted and cloud backend): Firecracker

**Needs:**
- `/dev/kvm` readable and writable by you (the `kvm` group);
- Go, `curl`, `mkfs.ext4` (e2fsprogs 1.43+);
- for the root-only steps, `nft` and `ip`;
- a guest kernel.

**Guest kernel:** use the one the Firecracker getting-started guide downloads:
<https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md>.
It needs `CONFIG_IP_PNP` and `CONFIG_VIRTIO_VSOCKETS`, which those kernels have.
The guide's CI kernel listing lags the newest release, so if the latest version
shows no kernel, use the newest version that has one.

Firecracker itself is downloaded from the project's release page: the latest, or pin one
with `FC_VERSION=1.15.0`.

```sh
# as yourself: boot time, memory, vsock, snapshot/restore, disk copies
KERNEL=/path/to/vmlinux ./scripts/m3/linux/run-all.sh

# as root: everything above, plus the jailer and host-enforced egress
sudo -E KERNEL=/path/to/vmlinux ./scripts/m3/linux/run-all.sh
```

Each step can also run on its own (`10-boot.sh`, `20-jailer.sh`, …).

| Script | Answers | Root |
|---|---|---|
| `10-boot.sh` | boot time under three kernel command lines, and the VMM's memory with an idle guest at 512 MiB and 2 GiB | no |
| `20-jailer.sh` | whether the guest boots under the jailer (chroot, namespaces, non-root uid), and how long it takes | yes |
| `30-network.sh` | host-enforced egress: tap + nftables redirect of tcp/80 and tcp/443 to the name-based proxy, everything else dropped. Eleven probes from inside the guest. | yes |
| `40-vsock.sh` | host↔guest channel: connection round trip and throughput each way | no |
| `50-snapshot.sh` | full snapshot and restore: time, file sizes, and whether the restored guest answers | no |
| `60-disk.sh` | the cost of a per-sandbox disk on this filesystem: full copy vs reflink | no |

**How the guest is built:** its whole root filesystem is `m3tool` (this
directory) as `/init`, plus a CA bundle. There is no distribution image, so every
result is printed from inside the VM by code in this repository.

**Reading the network results:** each probe line says what was expected
(`reach` / `blocked`), what happened, and `PASS` or `UNEXPECTED`. Every
`UNEXPECTED` is a finding. The allowlist is `github.com` and `example.com`.

**Checking the mechanics without root:** `unshare -rn` gives a user and network
namespace where you count as root. That runs `30-network.sh` far enough to check
the tap, the redirect and every *blocked* probe. The *reach* probes fail there,
because the namespace has no route out.

## macOS (local backend): the native `container` runtime

**Needs:** macOS 26 or later on an arm64 Mac, the `container` CLI installed,
and network access (the test installs packages inside the guest).

```sh
./scripts/m3/macos/run-all.sh
```

Every step records its own outcome and carries on. It measures:
- boot time;
- the memory of an idle guest;
- whether read-only binds are enforced;
- who owns what the guest writes, and whether the host can still edit it and
  `git commit` afterwards;
- whether the guest can program iptables or nftables;
- what the guest reaches by default, including the host itself;
- whether labels survive a runtime restart;
- whether `exec -i` carries binary data intact, and how fast.

## When the results are back

They replace the M3 section of `docs/rewrite/PLAN.md` (skill:
`finish-milestone`), and each answer either confirms the backend design or
changes it before M5/M6 build on it.
