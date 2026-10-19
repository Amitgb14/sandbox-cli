# Task 9: GPU sandboxes

**State:** proposed 2026-10-18; phases 1 to 5 built and checked without a
card the same day. The operator's page is [docs/gpu.md](../gpu.md). Decided: Cloud Hypervisor as the VMM for GPU sandboxes; CUDA compute
as the first milestone, a GPU desktop after.

## Goal

A sandbox that owns a whole physical GPU:

```sh
sandbox-cli run --gpu -- nvidia-smi
sandbox-cli agent claude --gpu --image <a CUDA image> -- -p "train the model in ~/proj"
```

`nvidia-smi` in the guest lists the card; a CUDA program runs on it. Everything
else about the sandbox is as today: the same API, guest agent, network policy,
files, processes, events and Studio.

## Why not Firecracker

Firecracker cannot give a VM a host device. `--enable-pci` (1.13 and later) is
virtio over PCIe only; VFIO passthrough is an open feature request
([firecracker#5679](https://github.com/firecracker-microvm/firecracker/issues/5679))
in no release up to 1.17, and the community implementation
([#6245](https://github.com/firecracker-microvm/firecracker/pull/6245)) was
closed unmerged. Waiting has no date.

**Cloud Hypervisor** passes PCI devices through with VFIO (`--device
path=/sys/bus/pci/devices/…`), boots the same `vmlinux` directly, and its
virtio-vsock is Firecracker's design: a Unix socket on the host and `CONNECT
<port>`, so `sandbox-guestd` and its host side work unchanged. It is one static
binary (v54.0, 2026-10-14). QEMU was the alternative: the most proven GPU
passthrough, but a far larger device model and a different agent transport.

Firecracker stays the VMM for every sandbox without a GPU. A GPU sandbox is the
exception, not a second default.

## What a GPU sandbox costs

- **A whole GPU, one sandbox.** No sharing, no slicing (consumer cards have no
  SR-IOV or MIG). The node's `--gpu` list is its GPU capacity.
- **No suspend, snapshot or fork.** Cloud Hypervisor cannot save a VFIO
  device's state. A GPU sandbox refuses them with `unsupported`, and an idle
  policy of `suspend` ends it instead (said at create).
- **Guest memory is pinned.** VFIO maps all of it for DMA up front, so a GPU
  sandbox's memory is reserved on the host for its whole life, not faulted in.
- **No jailer.** Cloud Hypervisor has no Firecracker-style jailer. It runs as
  the sandbox's unprivileged uid in its network namespace, with its own seccomp
  filters (Landlock not yet), the device's VFIO group the only device it is
  given, and no chroot.
- **The host gives up the card.** It is bound to `vfio-pci` while the node
  serves GPUs; nothing on the host can use it.

## The GPU kit: the driver is the operator's, not the image's

NVIDIA's kernel modules must match the guest kernel build exactly, and its
userspace driver (`libcuda`, `libnvidia-ml`, `nvidia-smi`) must match the
modules exactly. An image cannot know which kernel a node boots, so images do
not carry the driver. The operator provides a **GPU kit**, built together:

```
<kit>/vmlinux                        the GPU guest kernel (modules enabled)
<kit>/modules/<kernel>/…             nvidia.ko, nvidia-uvm.ko (open kernel modules)
<kit>/driver/…                       the matching userspace driver files
<kit>/kit.json                       kernel release, driver version, file list
```

sandboxd boots GPU sandboxes with the kit's kernel and injects the modules and
driver files into the root disk, as it injects `sandbox-guestd` today (a disk
per image × agent × kit, cached the same way). An image needs only the CUDA
runtime or toolkit it builds against; CUDA's forward compatibility covers a
newer driver. This is the split NVIDIA's container toolkit makes, for the same
reason.

The guest kernel today is Firecracker's CI build, with `# CONFIG_MODULES is not
set` and no DRM, so it cannot load a driver at all. The kit's kernel is that
config with module support (and DRM, for the desktop later); `make gpu-kit`
builds it and the modules.

## Loading the driver in the guest

Every process in a sandbox runs as uid 1001; nothing runs as root after
`sandbox-guestd` starts. Loading a module needs root, so `sandbox-guestd` does
it, as PID 1, before it serves: only when the kernel command line says the
sandbox has a GPU (`sandbox.gpu=1`, set by sandboxd), only modules under the
kit's directory, in the order `kit.json` gives, and it then makes
`/dev/nvidia*` itself (the numbers from `/proc/devices`) so no setuid
`nvidia-modprobe` is needed. A failure is reported through the create, not
left as a sandbox whose `nvidia-smi` says "no devices".

This gives no new power to an image: the modules are the operator's, chosen by
sandboxd, and the guest is the VM in any case.

## Phases

| # | Phase | Proves | Where |
|---|---|---|---|
| 0 | **Host.** IOMMU on; the card and its audio function (one IOMMU group) bound to `vfio-pci`; Cloud Hypervisor installed. A `docs/gpu.md` section and a check script (`sandboxd gpu check <bdf>`): IOMMU group members, driver bound, `/dev/vfio/<group>` | The node can hand the card over | docs, small command |
| 1 | **GPU kit.** `make gpu-kit`: the guest kernel (Firecracker's CI config, plus `CONFIG_MODULES`, DRM), NVIDIA's open kernel modules built against it, the userspace driver files, `kit.json` | `insmod nvidia.ko` succeeds in a hand-booted Cloud Hypervisor VM with the card | build scripts |
| 2 | **Backend.** The Linux backend boots a sandbox whose spec has GPUs on Cloud Hypervisor: the kit's kernel, the kit in its root disk, its devices passed through, the jail's uid under the jailer; processes, files, network, keep and terminate as for any sandbox; suspend, snapshot and resize refused (open question 2 replaced the separate backend first planned) | `Backend.Create` of a sandbox with the GPU, then `nvidia-smi` in it (`TestVMCloudHypervisorBackend`); `sandbox-cli run --gpu` needs phase 3 | sandbox-cli |
| 3 | **API and scheduling.** `gpus: 1` on create; capability `gpu`; `--gpu BDF` (repeatable), `--cloud-hypervisor` and `--gpu-kit DIR` on sandboxd; whole-GPU allocation and release; policy `max_gpus`; refusals (suspend, snapshot, fork, resize); the gateway places GPU sandboxes on nodes with a free GPU; events record the device; `--gpus` on the CLI and in the SDKs | A second create is refused a GPU with a clear error (`503`); the gateway path works | sandbox-cli |
| 4 | **Studio, doctor.** Studio's launch form and a GPU chip on the sandbox; `sandbox-cli doctor` reports GPUs | — | sandbox-cli |
| 5 | **A CUDA image.** Base image plus the CUDA runtime; a smoke that runs a CUDA sample | A real workload | images |
| 6 | **GPU desktop.** Xorg on the NVIDIA card, served over VNC like today's desktop; then games | Studio's Desktop tab drawn by the GPU | later |

Phase 0 needs root on the node, so it is the operator's; phases 1–3 are the
spike, on one node with one card, before anything is polished.

## Tests

- `backend/fake` gains GPUs (a list of fake BDFs), so allocation, release,
  refusals and the gateway's placement are unit- and e2e-tested without a card.
- `internal/backend/cloudhv` gets the `vm` build tag's real-VM test, as
  Firecracker has, skipped without `SANDBOX_TEST_CLOUDHV` and a free GPU.
- The conformance suite gains a GPU section, run where the endpoint reports
  `gpu`.
- `docs/testing/end-to-end.md`: `nvidia-smi` and a CUDA sample in a GPU
  sandbox on the node; a second request while the GPU is held; terminate frees
  it; the host's `nvidia` driver is not touched by a sandbox.

## What phase 1 found

Checked on the node on 2026-10-18 with Cloud Hypervisor v54.0, the kit's
kernel (6.1.155 with Firecracker v1.17.0's config plus modules) and NVIDIA
610.43.02's open modules, without the card (`TestVMCloudHypervisor`):

- The kit's kernel boots under Cloud Hypervisor and the guest agent answers
  over vsock in about 0.2 s, unchanged.
- `nvidia.ko` loads into it (the kernel and the modules match) and reports
  `NVRM: No NVIDIA GPU found.`; sandbox-guestd turns that into
  `FATAL: GPU: loading nvidia.ko: no such device` and the VM ends.
- Differences from Firecracker, each now in `cloudhv.go`:
  - **No `root=`.** Firecracker adds `root=/dev/vda` for the drive marked
    root; Cloud Hypervisor does not, so the command line names it.
  - **`image_type=raw` on every disk.** v54 refuses a disk without a declared
    format rather than guessing from its contents.
  - **A guest that reboots is reset, not ended.** Firecracker exits; Cloud
    Hypervisor starts the guest again, so a failed boot looped. A Cloud
    Hypervisor guest gets `sbx.halt=poweroff`, and sandbox-guestd powers off
    on a fatal error, which Cloud Hypervisor does exit on. A kernel panic
    still reboots and loops until the create's boot timeout ends the VM; a
    reboot once running (root, by `sudo reboot` in an image with sudo)
    restarts the guest where Firecracker would have ended the sandbox.
    Watching the VMM's event monitor (`--event-monitor`) for a reboot and
    ending the VM then would make the two the same; not done yet.
- `pci=off` is Firecracker's only: here the disks, vsock and GPU are all PCI.
- The kit is 389 MB: kernel, two modules, `nvidia-smi`, `libcuda` and the
  libraries CUDA loads, and the GSP firmware.

Next: phase 0 on the node (`scripts/gpu-vfio.sh`), then the same tests with
`SANDBOX_TEST_GPU=0000:01:00.0`, as root (docs/gpu.md, step 4).

## What phase 2 built

`internal/backend/firecracker/gpu.go`, and the branches in `Create`, the
refusals and keep. Checked on 2026-10-18 without a card and without root:
`TestVMCloudHypervisorBackend` creates a sandbox on Cloud Hypervisor with the
kit through `Backend.Create`, runs a process, writes and reads a file, is
refused suspend and snapshot, and terminates; the guest is ready in about
0.6 s. Firecracker's VM tests pass unchanged.

- **`backend.Spec.GPUs`**: the PCI addresses the server gives a sandbox
  (phase 3 chooses them). A create checks each is bound to `vfio-pci` and
  has a VFIO group before anything is built, and refuses a GPU sandbox from a
  snapshot.
- **`Config.CloudHypervisor` and `Config.GPUKit`**, both or neither. The kit
  is summed once at start; its sum is in the root disk's cache key.
- **Under the jailer** the VMM is not chrooted (Cloud Hypervisor has no
  jailer), but runs as the uid the jailer would have given the sandbox:
  `ip netns exec <ns> setpriv --reuid=<uid> … --ambient-caps=+ipc_lock`, in
  the jail's directory, with the same hard links as a Firecracker jail and the
  devices' `/dev/vfio/<group>` chowned to that uid until it ends.
  `CAP_IPC_LOCK` is the one capability kept: VFIO pins all guest memory, which
  the uid's `RLIMIT_MEMLOCK` would refuse. Checked by the test's root run, not
  yet run.
- **Keep**: a GPU sandbox is kept across a restart like any other.
  `keep.json` carries its GPUs, `vm.args` its command line (read back for the
  root disk it uses), and a stray `cloud-hypervisor` in the state directory is
  swept as a stray `firecracker` is.
- **Refused, as `unsupported`**: suspend, snapshot and resize; a fork needs a
  snapshot, so it cannot be made either.

Not done: Landlock for the VMM (Cloud Hypervisor's `--landlock`, which would
stand in for the chroot); the reboot loop phase 1 found; memory accounting
(open question 4).

## What phase 3 built

The API and scheduling, checked against the fake backend: server tests,
the conformance suite (a GPU section, run with fake GPUs, through a gateway,
and against an endpoint without them), the gateway's scheduler, and the
Python SDK against a `sandboxd --backend fake --gpu`.

- **API.**
  - Create takes `gpus` (a count). The sandbox lists its devices in `gpus`.
  - Capability `gpu`; `limits.max_gpus` (policy key, default 8, 0 without
    the capability, and never more than the node's `--gpu` count).
  - `NodeResources.gpus` in a node's capacity and free.
  - `sandbox.created` names the devices.
  - Refused, `400`: idle suspend, a snapshot schedule or `snapshot_id` with
    `gpus`, and more than the limit.
  - Refused, `501`: suspend, snapshot, schedule, resize and a later idle
    suspend of such a sandbox.
- **Allocation is the node's.** `server.Server.GPUs` is the node's list
  (`sandboxd --gpu`).
  - A create takes free ones under the lock that registers its record, so
    two creates cannot share one. Ten at once for two GPUs get exactly two.
  - What is free is read from the records not yet terminated, as the rest of
    a node's resources are. So a terminate, a reap or a failed create gives
    GPUs back, and a kept sandbox keeps its GPUs across a restart.
  - None free is `503 unavailable`, not a queue: a GPU sandbox runs for as
    long as it runs, and a waiting create would hold a request open for that
    long.
  - A GPU sandbox never claims a pooled VM.
- **The gateway.**
  - `Want.GPUs`: a node needs the capability, a high enough `max_gpus`, and
    as many free GPUs less what it has placed since the node's last status.
  - The fleet offers `gpu` when any node does, not when every node does.
  - A terminate's credit gives back no GPU (the store keeps none), so a GPU
    comes free at the node's next status.
- **sandboxd** takes `--gpu` (repeatable), `--cloud-hypervisor` and
  `--gpu-kit`, all three together or none, and `--gpu` alone with the fake
  backend.
- **Clients.** `--gpus` on `run` and the agent commands, `gpus` in both SDKs.

The CLI flag is `--gpus N`, after the API's field, where the goal above said
`--gpu`.

Not done:
- a non-GPU sandbox is not kept off a GPU node's CPUs and memory;
- an idle policy of suspend is refused with `gpus`, not turned into
  terminate.

## What phase 4 built

- **Studio.**
  - **The Playground:** a GPUs choice in the Size step, offered where the
    endpoint has the capability, up to `max_gpus` and the node's own count,
    with how many are free. With a GPU chosen, a snapshot schedule, starting
    from a snapshot and suspend-when-idle are disabled and say why, rather
    than being refused at launch. The code it shows has `--gpus` and
    `"gpus"`.
  - **A sandbox:** a GPU chip beside its resources, with its devices on
    hover. No Suspend or Snapshot button, Resize says why it cannot, the
    Snapshots tab says so, and "suspend it" is not offered when idle.
  - **The admin Nodes screen** counts GPUs in use.
  - **Studio's server** passes `gpus` to the run, locally and hosted.
- **`sandbox-cli doctor`** prints a `gpus:` line: the limit, and the node's
  free GPUs where the endpoint is a sandboxd.
- **Checked:** the Studio e2e suite, whose plain sandboxd now has a pretend
  GPU. It launches with one and checks the chip and the missing buttons. Go
  tests cover doctor, `run --gpus` and the launch request.

## What phase 5 built

`ghcr.io/<owner>/sandbox-cuda`, in sandbox-images (`images/cuda`,
`docs/cuda.md` there): the base image with CUDA 13.3's `nvcc` and runtime,
`python3-venv`, and `sandbox-cuda-check`, which prints the driver and the
device, runs a SAXPY kernel and checks the result (exit 2 with no driver or no
device). amd64 only. It carries no driver: the smoke test fails on a
`libcuda`, `libnvidia-ml` or `nvidia-smi` in the image.

- **CUDA 13.3** is the release driver 610.43.02 came with; the kit's
  `libcuda` reports 13.3 through `cudaDriverGetVersion`.
- **The kit's `libcuda` is found without `ldconfig`.** The root disk is built
  after the image's loader cache, which does not list it, but Debian's
  loader searches `/usr/lib/x86_64-linux-gnu` itself.
- **Checked** on 2026-10-18 without a card: the smoke test with the node's kit
  added as `copyTree` adds it (the driver found, no device), and a Firecracker
  sandbox from the image, without `--gpus`, building with `nvcc` and getting
  exit 2 from the check. With the card: e2e check 87.

## Open questions

1. **Kernel version.** Firecracker's 6.1 config is what boots today; NVIDIA's
   current open modules support it, but 6.18 (Firecracker's newer CI config)
   may be the better base for a new kernel. Decide in phase 1 by what builds.
2. ~~One process or two VMMs per node.~~ **Decided:** one backend, two VMMs.
   The Linux backend runs Cloud Hypervisor for a sandbox with a GPU and
   Firecracker for the rest; disks, network, egress and the guest agent are
   the backend's already and do not depend on the VMM
   (`internal/backend/firecracker/cloudhv.go`). No routing between backends is
   needed.
3. **Driver distribution.** NVIDIA's userspace driver may be redistributed under
   its license only as NVIDIA ships it; the kit is built on the node from
   NVIDIA's own files, never published by us.
4. **Memory accounting.** Pinned memory should count against the node's
   capacity differently from Firecracker's overcommitted memory.
