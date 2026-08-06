# Podman

Docker is the default; Podman is a supported alternative and needs no other
change. Rootless Podman in particular is a stronger starting point than rootful
Docker — the engine itself runs as you rather than as root.

```sh
sandbox-cli claude --engine podman
```

To stop typing it, put it in **your own** config (`~/.config/sandbox/config.yaml`):

```yaml
engine: podman
```

It is deliberately not a project key. A committed `.sandbox.yaml` choosing which
binary sandbox-cli executes would be choosing what runs on your machine, so it is
refused there for the same reason `runtime` and `image` are — see
[Configuration](../configuration.md#a-project-config-is-untrusted).

## Setting up

**macOS and Windows** need a VM, which Podman manages for you:

```sh
brew install podman        # or your platform's package
podman machine init
podman machine start
```

**Native Linux** talks to the host directly — no machine step.

Then check the host can actually deliver the isolation, rather than finding out
from a run:

```sh
sandbox-cli doctor --engine podman
```

## What differs from Docker

Nothing you have to do, but five things worth knowing:

- **The first run rebuilds the base image.** Podman keeps its own image store, so
  it will not reuse Docker's. One wait, not a recurring cost.
- **Each sandbox gets its own network.** Docker shares one network with
  inter-container communication switched off; Podman's netavark has no
  equivalent, and its `isolate` option blocks traffic between *different*
  networks while leaving same-network peers reachable — so sandbox-cli gives
  each run an isolated network of its own instead. `sandbox-cli clean` reaps any
  a killed run left behind.
- **The egress allowlist works identically**, including rootless. Programming the
  firewall from inside the container needs `NET_ADMIN`, and a rootless container
  has it within its own network namespace.
- **Do not pass `--user`.** On native Linux, rootless Podman maps your host user
  to container uid 0, and passing `--user "$(id -u):$(id -g)"` maps it into the
  subuid range instead, which makes `/workspace` unreadable. sandbox-cli handles
  the mapping (`--userns=keep-id`) and relabels bind mounts for SELinux, so files
  the agent writes come back owned by your own uid:gid.
- **The Docker-on-Linux group fix does not apply here, and does not need to.**
  Under Docker the container joins your primary group so the persisted agent
  login is reachable from both sides; `keep-id` already makes container uid 1001
  *you*, so Podman gets none of it. One consequence if you switch a machine
  between the two engines: expect to log the agent in once per engine, since each
  writes those credentials as a different id.

## Known limits

- The session commands (`list`, `logs`, `attach`, `kill`, `clean`) and `stats`
  need to be told which engine to look at — `--engine podman`, or the config key.
  They do not search both.
- Verified on macOS (Podman machine) and on Fedora with SELinux enforcing.
  Other rootless Linux setups should behave the same, but have not been measured.

---

Next: [Linux setup](linux.md) · [platform support matrix](README.md) ·
[documentation index](../README.md)
