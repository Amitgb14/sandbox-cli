# Platform support

sandbox-cli runs anywhere Docker or [Podman](podman.md) does. Almost everything
works identically across platforms; the differences are all about the boundary
the host can provide.

| Capability | macOS (Docker Desktop) | Linux (native Docker) | Windows (Docker Desktop / WSL2) |
|---|---|---|---|
| Core: `run` + the agent wrappers, mounts, env, hardening, metrics | ✅ | ✅ | ✅ |
| `--cache`, `--secret`, `--worktree`, `--git`, `--share` | ✅ | ✅ | ✅ |
| Egress allowlist (`--allow`) | ✅ ¹ | ✅ | ✅ ¹ |
| `--host-gateway` | auto ² | ✅ (needed) | auto ² |
| `/workspace` file ownership | virtualized to you | container uid ³ | virtualized to you |
| `--runtime kata-runtime` / `runsc` (microVM / gVisor) | ❌ ⁴ | ✅ ⁵ | ❌ ⁴ |

1. The firewall runs `iptables` inside the (Linux) container, so it works wherever
   the container kernel is Linux — including Docker Desktop. Verified in CI on
   native Linux; not yet independently verified on Docker Desktop.
2. `host.docker.internal` resolves automatically on Docker Desktop, so the flag is
   optional there; it's required on native Linux.
3. On native Linux **with Docker**, `/workspace` files are owned by the container
   user's uid — use `--user "$(id -u):$(id -g)"` if that matters. (The container
   does take *your group* there, so sandbox-cli's own state dirs — the persisted
   agent login above all — are reachable from both sides.) Docker Desktop
   virtualizes this. Under **rootless Podman that advice inverts**: your host uid
   maps into the subuid range, so passing it makes the workspace unreadable.
   sandbox-cli maps the container user onto you automatically there
   (`--userns=keep-id`), and files land as your own uid:gid — pass nothing.
4. Docker Desktop runs containers inside its own managed Linux VM and doesn't allow
   registering custom OCI runtimes — so you can't *select* Kata/gVisor. (You already
   get a VM boundary from Docker Desktop itself.)
5. Requires the runtime registered with the daemon; Kata additionally needs KVM /
   nested virtualization.

`--profile prod` follows that last row: on Linux it **requires** a runtime with a
kernel of its own and refuses without one; on Docker Desktop it accepts the VM
the engine already puts every container in. See
[prod demands a kernel of its own](../security/README.md#prod-demands-a-kernel-of-its-own-where-one-can-exist).

**In short:** on macOS/Windows everything works except *selecting* a microVM
runtime — and Docker Desktop already sandboxes containers in a Linux VM. For a
hardware microVM boundary you choose per run, use native Linux with Kata or
gVisor — see [Stronger isolation](../security/README.md#stronger-isolation-microvm--gvisor).

## Per-platform pages

- [Linux](linux.md) — Docker Engine setup, the `docker` group, container DNS,
  file ownership and persisted logins
- [Podman](podman.md) — either platform, rootless, and the five differences worth
  knowing

---

Back to the [documentation index](../README.md).
