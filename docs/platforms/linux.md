# Linux

Docker Engine with the default `runc` runtime is the path that works, and it
needs two things macOS doesn't. Both are Docker's model rather than
sandbox-cli's, and both are easy to get wrong in a way that looks like a broken
install.

- [Setting up](#setting-up)
- [The first run](#the-first-run)
- [Things that are the host's, not sandbox-cli's](#things-that-are-the-hosts-not-sandbox-clis)
- [File ownership and persisted logins](#file-ownership-and-persisted-logins)
- ["the container … cannot write"](#the-container--cannot-write)

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

## "the container … cannot write"

Before a run starts, sandbox-cli checks that the container user can write the
host directories it is about to mount, and says so if it cannot:

```
sandbox-cli: the container runs as uid 1001 gid 1000, which cannot write:
  /home/you/project/bin is mode 0755 owned by 1000:1000
  /home/you/project/.git/objects/05 is mode 0755 owned by 1000:1000
  the agent will report that it cannot edit files, and git will refuse to write objects
  fix: chmod -R g+w /home/you/project
  and, so git keeps making them that way: git config core.sharedRepository group
```

This is not a broken install. It is a true statement about your filesystem, and
without it the failure arrives much later and much less legibly — as an agent
saying it could not save a file, or as git refusing to write an object and
naming the store without naming which of its 256 fan-out directories is at
fault.

### Why it happens

The container runs as **uid 1001 with your primary gid**, so it reaches your
files through the *group* permission bits. Whether those bits are open is
decided by the umask in effect when each directory was created — which nothing
tells you, and which differs between distributions and between a login shell and
a service:

| umask | directories | the group gets | result |
|---|---|---|---|
| `002` | `0775` | `rwx` | works |
| `022` | `0755` | `r-x` | **read-only workspace** |

Both are ordinary. A repository cloned under one umask and built under another
ends up mixed, which is why the message often names a subdirectory — `bin/` from
a build, one `.git/objects/xx` from a single commit — while the project root is
fine.

### Fixing it

Run what the message printed. It names the mount root and is recursive, because
the umask that left one directory wrong left the ones under it wrong too:

```sh
chmod -R g+w /home/you/project
```

If the message says `chgrp` first, the directory belongs to a group you are not
sharing with the container, and opening the group bits alone would change
nothing:

```sh
chgrp -R "$(id -g)" /home/you/project && chmod -R g+w /home/you/project
```

### Stopping it coming back

The `chmod` fixes what exists; these decide what gets created next.

**For a repository** — the durable half, and the one that matters most, because
git creates object directories on demand for years after you clone:

```sh
git config core.sharedRepository group
```

git then creates them group-writable itself, whatever your umask is.

**For everything else**, set a umask that leaves the group bits alone. Check
yours first:

```sh
umask            # 0002 is what you want; 0022 is what produces this
```

On a distribution using user-private groups — your primary group is named after
you, `id -gn` matches `id -un` — `umask 002` is safe: the only account in that
group is you. Where the primary group is shared between accounts (`users` on
some setups), it is not, and the `chmod` above is the better tool.

### What sandbox-cli will not do

It never repairs your tree. `/workspace` is your project, and changing its mode
on your behalf is not a decision a sandbox gets to make — the same reason it
does not chown your files. It detects, reports, and leaves the command to you.

Two consequences worth knowing:

- **Under `--profile prod` this is a refusal, not a warning.** Nobody is
  watching an unattended run, and an agent that quietly cannot write is exactly
  the failure that profile exists to prevent.
- **Directories with a POSIX ACL are not reported at all.** The mode bits do not
  describe access there, so judging by them would refuse a workspace for a
  permission it actually has.

Worktrees that sandbox-cli creates itself are the exception to all of this: it
makes those with the group bits already open, since a tree it created for a
container to work in should be one the container can use.

---

Next: [Podman](podman.md) · [platform support matrix](README.md) ·
[documentation index](../README.md)
