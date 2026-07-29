# Task 1 — Better local / dev agent experience

**Goal.** Make the everyday loop pleasant and reliable: clear status, sessions you can
list, attach to, follow and stop, good defaults, a helpful `doctor`, and agents that are
easy to run.

**Branch.** `feature/local-agent-experience`.

This is the task that decides whether the tool gets used. Everything in it is about the
minutes between "I want an agent to do this" and "I have read what it did".

---

## What exists today

Verified against the code, so this task does not rebuild it.

| Capability | Where | State |
|---|---|---|
| Agent wrappers (claude, codex, gemini, opencode, aider, …) | `internal/cli/*.go`, `agentCmds()` | Good |
| Isolation model (only `/workspace` mounted, fake ephemeral `HOME`) | `internal/sandbox`, `internal/runtime` | Good |
| Security profiles `dev` / `prod` | `internal/config/profile.go` | Good |
| `doctor` | `internal/cli/doctor.go` | Prod-readiness only |
| Live resource table `stats` | `internal/cli/stats.go` | Works; filters by container *name* prefix, no ID column |
| Listing containers `ps` | `internal/cli/ps.go` | Works; name/agent/branch/status, no ID, no uptime |
| Reaping exited containers `clean` | `internal/cli/ps.go` | Good |
| Conversation history + resume (`context list`, `--resume`) | `internal/agentctx` | Good |
| Crash recovery (`recover`) | `internal/rescue` | Good |
| Worktrees (`--worktree`, `worktree` subcommands) | `internal/worktree` | Good |
| Config (`.sandbox.yaml` + global, layered) | `internal/config` | Good |
| Detached runs (`--detach`) | `sandbox.BuildSpec` | Good |
| `--secret`, `--cache`, `--share`, `--dry-run`, `usage` | various | Good |
| Container labels as the state store (`sandbox.cli/repo/branch/agent/base/fleet/verify`) | `internal/sandbox/labels.go` | Good — this is what makes all of the below possible |

**The gap is live session control.** A container outlives the process that started it —
`kill -9` on sandbox-cli leaves the agent running and still writing to `/workspace`, and
`--detach` means to leave it running — so "what is running right now, and how do I get at
it" is a question only the daemon can answer. Today it can only be asked with `docker`
directly.

| Need | Today | Gap |
|---|---|---|
| See running sandboxes | `ps` / `stats` | No stable ID to address one by; two commands, two column sets |
| Attach to a running agent | — | Not supported at all |
| Follow one run's output | `fleet logs BRANCH` only | Nothing for an ordinary or detached run |
| Stop one run | `docker rm -f` by hand | Nothing at the sandbox-cli level |
| "Is my setup good?" | `doctor --profile prod` | `doctor` answers a production question, not an everyday one |

---

## Required features

### 1. Session commands (the core of this task)

A **session** is a container sandbox-cli started: an ordinary `run`, an agent wrapper, a
`--detach`, or a fleet task. Four commands, one shared way of naming one.

```sh
sandbox-cli list                  # what is running right now (alias: ps)
sandbox-cli attach <session>      # put my terminal on a running one
sandbox-cli logs <session> [-f]   # read what it has written
sandbox-cli kill <session>        # stop it
```

**1.1 `list`** (alias `ps`, so the muscle memory keeps working)

- Columns: `ID`, `NAME`, `AGENT`, `BRANCH`, `STATUS`, `UPTIME`.
- `ID` is the container id abbreviated to 12 characters — the same identity `attach`,
  `logs` and `kill` accept, and the same one `stats` shows.
- `STATUS` distinguishes `running` from `exited (N)`; `UPTIME` is how long it has been
  running, or how long it ran.
- Flags: `--all` (include exited — detached runs are kept on purpose), `--quiet` (ids
  only, for scripting), `--engine`, `--config`.
- Empty state says what to do next, not just "none".

**1.2 `attach <session>`**

- Connects the terminal to a running session.
- **Ctrl-C detaches and never kills.** Attaching is a way to *look*, and looking must not
  be able to end someone's run — `kill` is a separate word on purpose. (`docker attach
  --sig-proxy=false`.)
- A `--detach`ed run was started without stdin, so attaching to it shows output and
  cannot type at it. The command says so up front rather than leaving the user typing
  into something that is not listening.
- Refuses a session that is not running, and points at `logs` instead.

**1.3 `logs <session> [--follow]`**

- Streams a session's output, for finished runs too — a detached container is retained
  precisely so its logs and exit code survive.
- Container stdout and stderr stay on their own streams.

**1.4 `kill <session>… [--all] [--force]`**

- Graceful by default: SIGTERM and docker's grace period, so an agent mid-write gets to
  finish the file it was writing. `--force` is SIGKILL and has to be asked for by name.
- Takes several sessions, or `--all` for every running sandbox on the machine.
- Reports what it actually stopped, by name.

**1.5 One way of naming a session — and it never reaches a container we did not start**

All four commands resolve a reference the same way, against a listing filtered by the
`sandbox.cli` label:

- exact container name, or full id
- unique id prefix
- branch name (the `sandbox.branch` label) — how you address a detached or fleet run

Two rules make it safe and predictable:

- **The reference is matched against sandbox-cli's own containers, never handed to the
  engine to resolve.** `sandbox-cli kill postgres` must not reach a container this tool
  did not start, and matching a labelled listing is what guarantees that.
- **Ambiguity refuses and lists the candidates.** Stopping the wrong agent is not
  recoverable by re-running the command.

Where exactly one session is running, `logs` and `attach` may be used with no argument.
`kill` may not: a destructive command does not get to guess its target.

### 2. One consistent identity across every command

The fragmentation is as much of a problem as the missing commands. After this task, `list`,
`stats` and `fleet status` describe the same things the same way:

- `stats` filters on the `sandbox.cli` label rather than on a `sandbox-` name prefix — the
  label is authoritative and the name is not — and grows the same short `ID` column, so a
  row in `stats` can be pasted into `attach`.
- `fleet status` keeps addressing work by branch (that is the fleet's unit), but a fleet
  container is a session like any other and appears in `list`.

### 3. An everyday `doctor`

Today `doctor` answers "can this host deliver what the prod profile promises". That is the
right question at 3am and the wrong one on a laptop. Without `--profile prod` it should
answer **"is my setup ready for normal use?"**:

- Is the engine installed and the daemon reachable? (exists)
- Is the base image built, or will the first run spend minutes building it? (new)
- Is a syscall filter applied, can a container program the egress firewall, which OCI
  runtimes are registered? (exists — kept, since these are what `dev` warns about)
- A closing line that says plainly whether everyday use is ready, and how many things are
  weaker than prod would allow.

The dev/prod asymmetry stays exactly as it is: dev warns, prod refuses with a non-zero
exit. This adds an everyday reading of the same checks; it does not soften prod.

### 4. Clearer failure messages

The three failures a new user actually hits:

- **Engine missing** — name the binary that was looked for and the alternative
  (`--engine podman`), not a bare exec error.
- **Daemon down** — say that, rather than an empty table that reads as "nothing running".
- **Mount refused** — `RefuseUnsafeHostPath` is right to refuse; the message should say
  which path and why (it is the home directory, or an ancestor of it).

### 5. Naming: "context" is not "session"

`context list` lists *conversations an agent has had*. `list` lists *containers running
now*. Those are different things and the help text should stop implying otherwise —
short descriptions, `Long` text, and the README/GUIDE wording all made consistent.

### 6. Tests and documentation

- Unit tests for reference resolution (exact / prefix / branch / ambiguous / not ours),
  for the listing's rendering, and for the new doctor check. No Docker required — the
  runtime capabilities are interfaces, and the existing `newDoctorRuntime` var is the
  pattern for substituting a host.
- The label-forging regression currently pinned by `TestParsePsRows` keeps equivalent
  coverage: a hostile branch label must not be able to invent or hide a row.
- `README.md`, `docs/GUIDE.md` and `CHANGELOG.md` updated with the four commands.

---

## Not in this task

- Anything in [task 2](task-2-multi-agent.md) (fleet features, orchestration) or
  [task 3](task-3-stronger-isolation.md) (Kata, runtime abstraction).
- A TUI or dashboard. `list` in one terminal and `stats` in another is the model.
- Structured event streams, metrics export, remote/centralized logging.
- Any change to the isolation boundary. `runtime.BuildArgs` and
  `sandbox.ResolveWorkspace` are where isolation lives; nothing here should need to touch
  them, and the `--dry-run` golden test is the tripwire if something does.
- Changing `--detach`'s shape (`-d` replaces `-i`/`-it`, `Remove` stays false). `attach`
  is built to be honest about that constraint, not to remove it.

---

## Done when

1. `list`, `attach`, `logs` and `kill` all exist, share one way of naming a session, and
   refuse to touch a container sandbox-cli did not start.
2. An id copied from `list` works in all three of the others, and in `stats`.
3. `doctor` with no flags reads as an everyday health check; `doctor --profile prod` is
   unchanged in behaviour and exit code.
4. `make test` passes with no Docker daemon; the `--dry-run` golden test is untouched.
5. README, GUIDE and CHANGELOG describe the commands as shipped.

Then stop, ship it, and use it for a while before starting task 2.
