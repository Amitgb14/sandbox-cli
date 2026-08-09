# Running commands and agents

The two shapes of invocation, how flags reach the agent, and every sandbox flag
`run` and the agent wrappers accept.

- [The basics](#the-basics)
- [Passing flags to the agent](#passing-flags-to-the-agent)
- [Common flags](#common-flags)

## The basics

```sh
# Any command in the sandbox
sandbox-cli run -- bash
sandbox-cli run -- sh -c 'echo $HOME; ls /workspace'

# See the exact docker command without running it
sandbox-cli run --dry-run -- npm test

# AI agents (forward their API key from your host env only if it's set)
ANTHROPIC_API_KEY=... sandbox-cli claude
ANTHROPIC_API_KEY=... sandbox-cli claude --dangerously-skip-permissions
OPENAI_API_KEY=...    sandbox-cli codex exec 'run the tests'
GEMINI_API_KEY=...    sandbox-cli gemini --yolo
ANTHROPIC_API_KEY=... sandbox-cli opencode run 'run the tests'

# See the conversations an agent has had here, and resume one
sandbox-cli context list
sandbox-cli claude --resume 37888763

# Scaffold a project config
sandbox-cli init
```

Per-agent prerequisites, login flows and the domains each needs under `--allow`
are in the [Agent reference](../AGENTS.md).

## Passing flags to the agent

For every agent wrapper (`claude`, `codex`, `gemini`, `opencode`, `cline`, …),
**everything you type is forwarded to the agent** — so `sandbox-cli claude
--dangerously-skip-permissions` just works, and there are no collisions with
sandbox's own flags.

The rule is one sentence: **a leading run of sandbox long-flags is consumed by
sandbox; the first token that isn't one ends the sandbox portion, and everything
from there goes to the agent verbatim.** A short flag, a positional, an unknown
long flag, or an explicit `--` all end it.

```sh
#              ├── sandbox ──┤  ├──── claude ────┤
sandbox-cli claude --worktree feature-a -- -p "implement A"
sandbox-cli claude --worktree feature-a    -p "implement A"   # same thing
```

The `--` is optional here because `-p` is a short flag, which always ends the
sandbox portion. Writing it is still the clearer habit, and it's *required* when
the first agent argument is a positional or would otherwise be ambiguous.

Order is what matters, not the separator. A sandbox flag placed **after** the
boundary is forwarded to the agent instead of being applied to the sandbox:

```sh
sandbox-cli claude --worktree feature-a --model opus     # ✅ worktree applies
sandbox-cli claude --model opus --worktree feature-a     # ❌ --worktree goes to claude
```

`--model` isn't a sandbox flag, so it ends the sandbox portion — and the
`--worktree` after it is passed straight through to Claude, which will reject it.
When in doubt, put every sandbox flag first and confirm with `--dry-run`:

```sh
sandbox-cli claude --worktree feature-a --dry-run -- -p "implement A"
```

`sandbox-cli run` uses the opposite default: sandbox flags first, the command
after `--` (`sandbox-cli run --dry-run -- npm test`).

## Common flags

Accepted by `run` and by every agent wrapper.

| Flag | Meaning |
|---|---|
| `-p, --project` | Host dir mounted at `/workspace` (default: cwd) |
| `-i, --image` | Override the container image |
| `-w, --workdir` | Working dir inside the container |
| `--user` | `sandbox` (non-root default) \| `root` \| `uid:gid` |
| `-m, --mount` | Extra mount `host:container[:ro\|rw]` (repeatable) |
| `-e, --env` | `KEY=VALUE`, or bare `KEY` to forward the host value |
| `--env-allow` | Host env var forwarded only if present (repeatable) |
| `--tty` / `--no-tty` | Force TTY on/off (default: auto-detect) |
| `--dry-run` | Print the docker command and exit |
| `--build` | Force a rebuild of the base image |
| `--no-metrics` | Disable the live resource gauge (non-interactive runs) |
| `--memory` | Container memory limit, e.g. `2g` (default: unlimited) |
| `--cpus` | Container CPU limit, e.g. `1.5` (default: unlimited) |
| `--no-hardening` | Disable the default cap-drop / no-new-privileges / pids-limit (debug) |
| `--allow` | Permit a domain on the egress allowlist, e.g. `--allow example.com` (repeatable; implies allowlist mode, and the baseline registries are always permitted) |
| `--network` | `allowlist`, `default` (unrestricted), or `none` to reach nothing. Built-in default: `allowlist`; the config the installer writes sets `default` — see [Install](../install.md#the-config-the-installer-writes) |
| `--profile` | `dev` (default, warns when a control is unavailable) or `prod` (refuses) |
| `--engine` | `docker` (default) or `podman`. Also `engine:` in your own config — not in a project file, since it chooses which binary runs |
| `--cache` | Persist package-manager caches (npm/pip/cargo/go) in named volumes across runs |
| `--secret` | Brokered credential `NAME=file:PATH \| cmd:COMMAND \| env:VAR`, resolved at run time and kept off the command line (repeatable) |
| `--worktree` | Run in a git worktree for `BRANCH` (created if absent) — parallel per-branch agents |
| `--detach` | Start in the background and print the container name, so one terminal can launch several agents (the guest must exit on its own) |
| `--share` | Mount the shared dir (`~/.config/sandbox/shared`) at `/shared` so agents in different projects can exchange files |
| `--share-name NAME` | With `--share`, mount `~/.config/sandbox/shared/NAME` at `/shared/NAME` instead of the root — avoids collisions between concurrent runs; not an isolation boundary |
| `--paste` | Mount `~/Desktop`, `~/Downloads` and `~/Pictures` read-only at their host paths, so an image path pasted into the agent resolves inside the container |
| `--git` | Forward host git identity and trust the workspace so `git` commits just work in-container |
| `-P, --publish` | Publish a container port to the host, e.g. `-P 3000` or `-P 8080:3000` (repeatable; binds `127.0.0.1` unless you give an address) |
| `--host-gateway` | Map `host.docker.internal` to the host (reach host MCP servers; needed on Linux) |
| `--add-host` | Extra `HOST:IP` mapping passed to docker (repeatable) |
| `--runtime` | OCI runtime for stronger isolation, e.g. `kata-runtime` (microVM) or `runsc` (gVisor) |
| `--no-snapshot` | Disable the crash safety net (periodic snapshots of the workspace under `refs/sandbox`) |
| `--snapshot-interval` | How often to snapshot the workspace, e.g. `30s` (default: 2m) |

Agent wrappers add `--no-persist-auth`, and `claude` adds `--no-sync` and
`--no-statusline` — see [Agent login and history](agent-login.md).

---

Next: [Sessions](sessions.md) · [Configuration](../configuration.md) ·
[documentation index](../README.md)
