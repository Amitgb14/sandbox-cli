# sandbox-cli documentation

Everything beyond the [README](../README.md), grouped by the question you arrived
with. Each page stands on its own; nothing here needs to be read in order.

## Start here

| Page | What it answers |
|---|---|
| [Install](install.md) | Requirements, the one-line install, what config it writes, every other route, uninstall |
| [User guide](GUIDE.md) | The narrative walkthrough: first run, everyday use, a tour of every feature |
| [Agent reference](AGENTS.md) | All 18 agents — prerequisites, how to log in without a browser, forwarded variables, `--allow` domains |

## Using it

| Page | What it answers |
|---|---|
| [Running commands and agents](usage/flags.md) | The two invocation shapes, how flags reach the agent, and every sandbox flag |
| [Sessions](usage/sessions.md) | `list`, `attach`, `logs`, `kill`, `clean` — what is running and how to get at it |
| [Watching a run](usage/monitoring.md) | The live gauge, Claude's status line, `stats`, `usage` |
| [Agent login and history](usage/agent-login.md) | What persists on your host, the shared Claude history, `context list` |
| [One agent per branch](usage/worktrees.md) | `--worktree`, the full cycle, `--detach`, the `worktree` subcommands |
| [Many agents at once](usage/fleet.md) | `fleet.yaml`, `verify:`, `land`, mixing agents |
| [Sharing files between sandboxes](usage/sharing.md) | `--share`, `--share-name`, and what a namespace is not |
| [Crash recovery](usage/recovery.md) | What to run when a sandbox died mid-write, and the snapshots behind it |
| [git, MCP and SSH](usage/integrations.md) | `--git`, `--host-gateway`, forwarding an SSH agent |

## Reference

| Page | What it answers |
|---|---|
| [Configuration](configuration.md) | The two config files, precedence, and which keys a project may not set |
| [Security](security/README.md) | The two profiles, `doctor`, the security model, stronger isolation |
| [Platform support](platforms/README.md) | The capability matrix, plus [Linux](platforms/linux.md) and [Podman](platforms/podman.md) setup |
| [Alternatives and prior art](alternatives.md) | How this compares, including where it loses |
| [Studio](studio-api/README.md) | The browser control plane: the one-command install, the HTTP API, and who may drive it |
| [Development](DEVELOPMENT.md) | Make targets, single tests, release engineering, invariants to keep honest |

## Project direction

| Page | What it answers |
|---|---|
| [Roadmap](roadmap/README.md) | Six pieces of work in order, what is deferred, and what was considered and declined |
| [Security audit](security/audit-2026-07-26.md) | The ledger: findings, threat model, per-round counts |
| [Open security items](security/open-items.md) | The live backlog, each item reproduced by execution |
| [Secrets posture](security/secrets.md) | What sandbox-cli protects about a secret, and what it does not |
| [`proposals/`](proposals/) | Design notes and rejected alternatives, per feature |

## Examples

- [`examples/fleet.yaml`](examples/fleet.yaml) — a commented fleet file
