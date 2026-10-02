# Making git, MCP and SSH "just work"

Three things people reach for on day one, and the flag each one needs.

- **git** — `--git` forwards your host `user.name` / `user.email` (so commits are
  attributed to you) and marks the mounted workspace as trusted, avoiding git's
  "dubious ownership" refusal when the container user's uid differs from the
  host's. Pairs naturally with [`--worktree`](worktrees.md).
- **Host MCP servers** — an agent inside the container reaches services on your
  host via `host.docker.internal`. That name resolves automatically on Docker
  Desktop; on Linux add `--host-gateway` (it maps `host.docker.internal` to the
  host gateway). Use `--add-host HOST:IP` for any other host mapping. Traffic to
  it still meets the egress firewall and the name-matching proxy, so an allowlist
  applies to a host service exactly as it does to the internet.
- **SSH (manual)** — to push over SSH, forward your agent socket:
  `--mount "$SSH_AUTH_SOCK:/ssh-agent" --env SSH_AUTH_SOCK=/ssh-agent` (on macOS
  Docker Desktop use the socket path `/run/host-services/ssh-auth.sock`).

File ownership differs per platform and is covered where it belongs:
[Linux](../platforms/linux.md#file-ownership-and-persisted-logins),
[Podman](../platforms/podman.md#what-differs-from-docker).

---

Next: [Common flags](flags.md) · [Configuration](../configuration.md) ·
[documentation index](../README.md)
