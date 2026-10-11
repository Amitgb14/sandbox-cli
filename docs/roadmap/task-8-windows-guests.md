# Task 8 — Windows guests: a sandbox that runs Windows, for code that has to be tested on it

**Status:** proposed 2026-10-10, not scheduled. Nothing here is built.
**Answers:** open question 5 in [`../rewrite/PLAN.md`](../rewrite/PLAN.md),
*"Windows guests: does a sandbox ever run Windows, and on which backend?"*
**Relates to:** the platform-loss risk in the plan, which is about Windows
*hosts* and is unchanged by this. Windows hosts keep no local mode. This page is
about Windows *guests*, reached from any client through a Linux endpoint.

## The requirement

A user or an agent runs tests that only mean something on Windows:

- path handling (drive letters, `\`, case-insensitive names, reserved names
  such as `NUL`), CRLF line endings, file locking that refuses to delete an open
  file;
- PowerShell and `cmd` scripts, installers, services, the registry;
- toolchains and runtimes that exist only there, or behave differently there:
  the Windows C/C++ toolchain, .NET Framework, native modules for Node or
  Python built against the Windows ABI.

Cross-compiling, or running Windows binaries under a compatibility layer on
Linux, does not prove any of these. The test has to run on Windows, so a sandbox
has to be able to run Windows. The use case is real. The question is where it
runs and what it costs.

## Why no current backend can do it

| Piece | Today | What blocks Windows |
|---|---|---|
| Firecracker (self-hosted, cloud) | Linux guests, booted straight into a kernel | no UEFI, no ACPI device model, virtio-mmio only; Windows does not boot on it |
| the macOS `container` runtime (local) | Linux guests only | it runs Linux guests and nothing else; an arm64 Mac would also need arm64 Windows |
| `internal/image` | OCI image → ext4 root disk | Windows needs an NTFS system disk with a bootloader and drivers, which is not an OCI image |
| `sandbox-guestd` | PID 1 of a Linux guest, over vsock | a Windows guest needs a Windows service, a vsock driver, ConPTY for terminals and Job Objects for process trees |
| licensing | Linux images we build and ship | we cannot redistribute a Windows image |

## What does not change

Most of the host side is the same for any guest, because it does not depend on
what runs inside:

- the API, the request guard, `spec`, `policy`, `audit`, the gateway and its
  keys and ownership;
- egress: tap + nftables on the host and `egressproxy`. Enforcement is outside
  the guest, so a Windows guest gets exactly what a Linux guest gets;
- treating the guest as hostile: `guestproto`'s host side already parses every
  reply as untrusted, and that does not care which OS sent it.

## Options

### A. A Cloud Hypervisor backend on KVM, for Windows guests only (recommended)

A new `backend/chv` next to `backend/firecracker`, on the same Linux hosts.
Firecracker stays the backend for Linux guests.

- Cloud Hypervisor is a small Rust VMM in the same family as Firecracker. It
  boots Windows with UEFI firmware, has virtio-net, virtio-blk and virtio-vsock,
  and can snapshot and restore.
- Networking is the tap device Firecracker already uses, so nftables and
  `egressproxy` carry over unchanged.
- It has no jailer. `backend/chv` has to confine each VMM itself: an
  unprivileged uid, its own mount and network namespaces, a cgroup, the
  chroot. `jailer.go` is the model. This is the main trust-boundary work.
- It brings one more external binary and a firmware file for the operator to
  install, as Firecracker does. No Go dependency comes with it.

### B. QEMU with KVM

QEMU supports Windows best of all: every device, emulated TPM, any edition. It
is also a large device model running as a host process, which is the attack
surface the microVM design exists to avoid. Keep it as a fallback if Phase 0
shows Cloud Hypervisor cannot run the editions people need.

### C. Rent Windows VMs from a hosted provider behind our API (rejected)

That breaks "the same API on a machine you control" and self-hosting with no
phone-home, and moves the isolation boundary outside our code and tests.

### D. Windows hosts running sandboxd with Windows containers (rejected)

This brings back a Windows host platform, which the plan dropped at M7. It also
brings back containers, which "VMs only" ruled out.

## The design, under option A

**Which Windows.** Windows Server Core. Client editions' licences do not allow
hosting for other people. Server has a service-provider licensing route for the
cloud, and self-hosted operators bring their own licence. Server Core has no
desktop, which keeps the image and memory smaller.

**Images.** Not OCI. We ship a recipe, `images/windows/`: an unattended-install
answer file, the virtio drivers, `sandbox-guestd.exe` installed as a service,
and a generalisation step. The operator runs it against their own installation
media and gets a raw disk image, then registers it with sandboxd. Each sandbox
boots from a copy-on-write overlay of that image, as Firecracker sandboxes do
from the cached disk.

- An image carries its guest OS in its metadata.
- A request for a Windows image on an endpoint that cannot run one is refused,
  never silently given a Linux sandbox.

**Capabilities.** A new `guest_windows` capability. The gateway's scheduler
places a Windows request only on nodes that report it. The conformance suite
skips its Windows tests where the capability is off, as it does for snapshots.

**`sandbox-guestd` on Windows.** It is the same program built with
`GOOS=windows`. `guestproto` already splits its OS-specific parts (`pty_linux.go`,
`server_unix.go`, `*_other.go`), so the Windows parts are new files:

| Operation | Linux | Windows |
|---|---|---|
| transport | vsock (`AF_VSOCK`) | virtio-vsock through the virtio driver's socket provider; virtio-serial if that driver is not good enough (Phase 0 decides) |
| terminal | pty | ConPTY (`CreatePseudoConsole`) |
| process tree, signals | process groups, `SIGTERM` / `SIGKILL` | a Job Object per process. TERM sends a console break; KILL terminates the job |
| the sandbox user | uid in `/etc/passwd` | a local account without admin rights, created on first boot |
| files API root | `/sandbox/home` | `C:\sandbox\home` |
| start | PID 1 | a service set to start automatically |

**Trust boundary: what Windows changes.** Each item needs a test that fails
without its fix (rule 10):

- **Environment names are case-insensitive on Windows.** `policy.IsReservedEnv`
  compares names exactly, so a request setting `path` would get past a rule
  written for `PATH`. For a Windows guest the comparison has to fold case. The
  reserved list also grows by the variables that are instructions on Windows:
  `PATHEXT`, `COMSPEC`, `PSModulePath`, `windir`, `SystemRoot`.
- **Guest paths.** The files API takes a path from the request and guestd
  resolves it inside its root. On Windows, these must not escape the root or
  name something else:
  - drive letters and UNC paths (`\\server\share`);
  - device paths (`\\?\`, `\\.\`);
  - alternate data streams (`file.txt:stream`);
  - reserved device names (`CON`, `NUL`, `COM1`), short 8.3 names;
  - junctions and symbolic links.

  This is inside the guest, so an escape only reaches that guest. It is still
  the files API's contract, and the conformance suite should hold it.
- **Agent logins in `agenthome`.** The paths are different, and so are the
  tools that read them. Phase 1 has no agents, so this waits.

**What it costs to run.** Expect a cold boot of tens of seconds, not
Firecracker's sub-second, a memory floor near 1 GiB, and a system disk of
10 GiB or more. Pools of booted sandboxes and restoring from a memory snapshot
are what make that usable, so they come early (Phase 2), not last.

## Phases

0. **Measure, on a real KVM host (maintainer).** Cloud Hypervisor boots Windows
   Server Core from an image built by the recipe. Record the cold boot time,
   the restore-from-snapshot time and the idle memory. Confirm the vsock driver
   carries a guestd connection under load. *Exit:* the numbers are on this page,
   and the transport and VMM decisions below are made from them.
1. **Exec and files, self-hosted only.** `backend/chv` with its confinement and
   a golden test for its renderer. `sandbox-guestd.exe` handles run, capture and
   the files API. The image recipe and registration. The `guest_windows`
   capability, and conformance tests for exec and files that run against a
   Windows image. This is enough for "run my test suite on Windows".
2. **Interactive and fast.** ConPTY terminals, tunnels, pools, snapshots and
   suspend.
3. **Fleet and cloud.** Gateway scheduling by guest OS. Cloud licensing, which
   is a business decision as much as a technical one.

Agents running inside a Windows guest come after all of these and are not part
of this task.

## Decisions needed

1. **Whether, and when.** Before M10 is finished, after it, or not at all.
   Recommended: after M10, starting with Phase 0 only, because Phase 0 costs a
   day on a host and settles every other decision here.
2. **VMM.** Cloud Hypervisor (recommended) or QEMU. Phase 0 can overturn this.
3. **The guest agent's Windows service.** Registering and running as a Windows
   service without `golang.org/x/sys/windows/svc` means calling the service
   control manager through `syscall` by hand. Allowing `golang.org/x/sys` **for
   `sandbox-guestd`'s Windows build only** follows the precedent of
   `x/crypto/ssh` for the gateway. It is already in the module graph as an
   indirect dependency. Recommended: allow it, and record the decision in the
   plan.
4. **Cloud licensing.** Bring your own licence only, or service-provider
   licensing priced into Windows sandboxes. This does not block phases 0–2.
5. **Local.** Mac users reach Windows guests through a self-hosted or cloud
   endpoint, never locally. Recommended as stated. The macOS runtime cannot
   run Windows, and arm64 Windows would test a different architecture from the
   one most Windows tests target.
