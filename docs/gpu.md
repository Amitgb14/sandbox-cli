# GPU sandboxes

A GPU sandbox owns a whole physical GPU: `nvidia-smi` in it lists the card,
and a CUDA program runs on it. Apart from the GPU, it is like any other
sandbox, with the same API, guest agent, network policy, files, processes and
Studio.

**State: being built.** A node can boot a GPU sandbox's VM, but no client can
ask for one yet:

| | |
|---|---|
| Built and tested without a card | The kit; the VM, through sandboxd's Linux backend; the refusals below |
| Not yet tried | A real card passed through. The node's setup below needs root on it |
| Not yet built | `gpus` on create, `sandboxd --gpu`, `sandbox-cli run --gpu`, Studio, a CUDA image |

The plan, with what each phase proved, is
[task 9 in the roadmap](roadmap/task-9-gpu-sandboxes.md).

## How it works

Firecracker, which runs every other sandbox, cannot pass a host device to a
VM. A sandbox with a GPU runs on **Cloud Hypervisor** instead. Cloud
Hypervisor passes the card through with VFIO, and its vsock works the way
Firecracker's does. Everything else is sandboxd's Linux backend as for any
sandbox:

- the root and scratch disks;
- the sandbox's network namespace, its tap and egress rules;
- the guest agent;
- keeping the sandbox running across a sandboxd restart (`--keep-sandboxes`).

```
sandboxd ── Firecracker ──────────────── a sandbox
         └─ Cloud Hypervisor ── VFIO ─── a sandbox with the GPU
                                  │
                         the card, bound to vfio-pci
```

**The driver is the node's, not the image's.** NVIDIA's kernel modules must
match the guest kernel exactly, and an image cannot know which kernel a node
boots. The operator builds a **GPU kit** on the node. It has:

- a guest kernel that can load modules;
- NVIDIA's open kernel modules, built against it;
- the matching `nvidia-smi`, `libcuda` and firmware.

sandboxd adds the kit to a GPU sandbox's read-only root disk, as it adds the
guest agent. At boot, the guest agent loads the modules as root, before
anything else runs, and makes `/dev/nvidia*` itself. An image needs only the
CUDA runtime it builds against.

**Under the jailer**, Cloud Hypervisor runs as the sandbox's own unprivileged
uid, the one the jailer would have given it, in the sandbox's network
namespace. It keeps one capability, `CAP_IPC_LOCK`, which it needs to pin the
guest's memory for DMA, and, where `/dev/kvm` is not open to all (`0660
root:kvm` on Debian and Ubuntu), the group that owns it. The device's
`/dev/vfio/<group>` belongs to that uid while the sandbox runs and goes back
to root when its VMM has exited, or when sandboxd removes a sandbox an earlier
run left. A terminate waits for the VMM to exit, up to two minutes, so the GPU
is free when it returns; one that is not answers with an error and can be
retried. Unlike Firecracker,
it is not chrooted, because Cloud Hypervisor has no jailer of its own.

## What a GPU sandbox costs

- **A whole GPU, one sandbox.** A GPU is never shared or sliced.
- **No suspend, snapshot, fork or resize.** Cloud Hypervisor cannot save a
  passed-through device's state, so these are refused as `unsupported`.
- **Its memory is reserved on the host for its whole life.** VFIO pins all of
  it at boot. A Firecracker sandbox's memory is only used as the guest touches
  it.
- **The host gives up the card.** While the node serves the GPU, the card is
  bound to `vfio-pci`, so nothing on the host can use it. A display has to run
  on another GPU.

## Preparing a node

You need an amd64 Linux node with KVM, the IOMMU on (`amd_iommu` is on by
default; Intel needs `intel_iommu=on`), and a GPU the host's display does not
use. The steps need root.

**1. Build the kit.** This downloads the kernel source and NVIDIA's driver,
checks each against a pinned checksum, and builds the modules. It needs a
kernel build toolchain (gcc, make, flex, bison, perl, and the libelf and
openssl headers) and about 6 GB of working space while it builds. It leaves a
kit of about 400 MB.

```sh
scripts/gpu-kit.sh /var/lib/sandboxd/gpu-kit
```

**2. Install Cloud Hypervisor**, the static binary from its v54.0 release.
Check it against the release's checksum, then
`sudo install cloud-hypervisor /usr/local/bin/`.

**3. Hand the card over.** VFIO gives a VM a whole IOMMU group, so the card's
audio function goes with it. `status` lists the group and anything on the host
that still has the card open. `bind` refuses until nothing does.

```sh
sudo scripts/gpu-vfio.sh status 0000:01:00.0
sudo systemctl stop nvidia-persistenced     # and anything else status lists
sudo scripts/gpu-vfio.sh bind 0000:01:00.0
```

The binding lasts until the next boot. `sudo scripts/gpu-vfio.sh unbind
0000:01:00.0` gives the card back to the host's driver.

**4. Check it.** The backend's VM test boots a GPU sandbox the way sandboxd
does and runs `nvidia-smi` in it. It needs Go, and root for the card:

```sh
go build -o /tmp/guestd ./cmd/sandbox-guestd
(cd internal/backend/firecracker && go test -c -tags vm -o /tmp/chvm.test)
cd internal/backend/firecracker && sudo SANDBOX_TEST_CLOUDHV=/usr/local/bin/cloud-hypervisor \
  SANDBOX_TEST_GPU_KIT=/var/lib/sandboxd/gpu-kit SANDBOX_TEST_AGENT=/tmp/guestd \
  SANDBOX_TEST_GPU=0000:01:00.0 /tmp/chvm.test -test.run 'TestVMCloudHypervisor' -test.v
```

The output should include `nvidia-smi -L` listing the card, followed by
`PASS`.

## When a GPU sandbox does not start

A create fails with what went wrong, and sandboxd's log has the guest's
console:

| Error | Cause |
|---|---|
| `this node serves no GPUs` | sandboxd was started without a GPU kit |
| `GPU 0000:01:00.0 is not bound to vfio-pci` | Step 3 was not run, or the node has rebooted since |
| `GPU … has no IOMMU group` | The IOMMU is off |
| `sandbox-guestd: FATAL: GPU: loading nvidia.ko: …` on the console | The driver did not load in the guest. `no such device` means no card was passed through. Any other error usually means the kit was built for another kernel: rebuild it |
| `the VMM exited before the guest came up` | Cloud Hypervisor refused the device. Its own error is in sandboxd's log. Often something on the host still has the card open, or the group's other function is not bound |
