# Linux

Docker Engine with the default `runc` runtime is the path that works, and it
needs two things macOS doesn't. Both are Docker's model rather than
sandbox-cli's, and both are easy to get wrong in a way that looks like a broken
install.

- [Setting up](#setting-up)
- [The first run](#the-first-run)
- [Things that are the host's, not sandbox-cli's](#things-that-are-the-hosts-not-sandbox-clis)
- [File ownership and persisted logins](#file-ownership-and-persisted-logins)

## Setting up

```sh
# 1. Docker Engine
curl -fsSL https://get.docker.com | sh
sudo systemctl enable --now docker

# 2. Let your user talk to the daemon
sudo usermod -aG docker "$USER"
```

**Group membership only applies to a new login session.** After `usermod`, your
current shell still has the old groups, so sandbox-cli keeps reporting
`permission denied … /var/run/docker.sock` no matter how many times you restart
the daemon — the missing piece is on the *client* side. Either log out and back
in, or start a shell that has the group now:

```sh
newgrp docker            # this shell only
sandbox-cli doctor
```

Check it took: `id -nG | tr ' ' '\n' | grep -qx docker && echo ok`.

> Membership of the `docker` group is **root-equivalent** on the host — anyone in
> it can start a privileged container that mounts `/`. That is Docker's design,
> and it is worth deciding on rather than pasting. [Rootless
> Docker](https://docs.docker.com/engine/security/rootless/) avoids it, and so
> does [Podman](podman.md).

## The first run

Run an agent from inside a project — never from `$HOME`, which sandbox-cli
refuses to mount:

```sh
cd ~/your-project
sandbox-cli claude
```

**The first run is slow, twice over, and mostly silent.** It builds the base
image (a few minutes), and the `claude` wrapper then downloads a self-updating
copy of Claude Code into the persisted agent home — a large binary, with no
progress shown. Interrupting either one throws the work away and the next run
starts over. Let the first run finish; every later one starts immediately.

To watch what it is doing, run the same install with its output visible:

```sh
sandbox-cli run -- sh -c 'curl -fsSL https://claude.ai/install.sh | bash'
```

## Things that are the host's, not sandbox-cli's

- **Container DNS.** On distributions using nftables-backed firewalld (RHEL 10
  and relatives), the firewall can drop the rules the engine installs, leaving
  containers with an interface and no working resolver. It surfaces far from the
  cause — as an agent hanging at login, or `getaddrinfo ETIMEOUT`. Check it
  directly with `sandbox-cli run -- sh -c 'getent hosts api.anthropic.com'`; if
  that fails, so will everything else.
- **Stronger runtimes are opt-in and unvalidated by the host.** `--runtime runsc`
  and `--runtime kata-runtime` need the runtime installed *and* registered with
  the daemon — see [Stronger isolation](../security/README.md#stronger-isolation-microvm--gvisor).
  gVisor replaces the network stack, and some workloads (Claude Code included,
  in one report) fail to reach an API through it; drop the flag to confirm before
  chasing it elsewhere.

## File ownership and persisted logins

- **File ownership.** Files written to `/workspace` are owned by the container
  user's uid. On macOS Docker Desktop this is virtualized to your host user
  automatically; on native Linux, run as your own uid with
  `--user "$(id -u):$(id -g)"` if ownership matters (note: the agent's ephemeral
  HOME is owned by the image's `sandbox` user, so prefer this for non-agent runs).
  Under rootless Podman **that advice inverts** — see [Podman](podman.md).
- **Persisted logins.** The same arithmetic used to break the thing you notice
  first: the container user is uid 1001, the persisted agent HOME is a host
  directory you own at mode 0700, so the agent could neither read the credentials
  nor write the ones it had just obtained — you logged in, and the next run asked
  again. sandbox-cli now runs the container as `1001:<your gid>` on Linux and
  gives its own state dirs (the persisted HOME, and claude's history bucket)
  group access, so both sides reach the same files. Nothing is chowned and
  nothing else about the container changes; on macOS and under Podman it renders
  nothing, because bind ownership is already handled there.

---

Next: [Podman](podman.md) · [platform support matrix](README.md) ·
[documentation index](../README.md)
