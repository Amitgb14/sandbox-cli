# Sessions: what is running, and how to get at it

A **session** is a container sandbox-cli started — an ordinary `run`, an agent
wrapper, a `--detach`, or a fleet task. Two of those outlive the terminal that
started them: the daemon owns the container, not the `docker run` client, so a
`kill -9` on sandbox-cli leaves the agent working in your project with nothing
attached to it, and `--detach` does the same deliberately.

```sh
sandbox-cli list                  # what is running right now (alias: ps)
sandbox-cli list --all            # including sessions that have finished
sandbox-cli attach <session>      # put this terminal on a running one
sandbox-cli logs <session> -f     # follow what it is writing
sandbox-cli kill <session>        # ask it to stop
sandbox-cli clean                 # remove the containers of finished sessions
```

```
ID            NAME                     KIND         AGENT   BRANCH     STATUS      ELAPSED
a1b2c3d4e5f6  sandbox-myapp-feature-a  interactive  claude  feature-a  running     4m12s
9f8e7d6c5b4a  sandbox-myapp-feature-b  fleet        codex   feature-b  exited (0)  11m3s
```

A `RUNTIME` column appears — for every row, and in `fleet status` too — as soon
as one session is on a runtime that is not one of the ordinary shared-kernel ones
(`runc`, `crun`, `youki`). So you can see at a glance which runs got a kernel of
their own and, more usefully, which did not. On a machine where everything is
`runc` it stays out of the way, and a runtime sandbox-cli does not recognise is
shown by name rather than judged. See
[Stronger isolation](../security/README.md#stronger-isolation-microvm--gvisor).

`KIND` separates a fleet container from an interactive `--detach` session,
because that distinction is already load-bearing everywhere else: `fleet stop
--all` does not reach an interactive session, `fleet clean` does not reap one,
and `max_parallel` does not count one.

## Naming a session

**A session can be named three ways** — the `ID`, the container name, or the
branch — because those are the three things you actually have in front of you.
An ambiguous name is refused with the candidates listed rather than guessed at;
the one exception is that a branch with several containers resolves to the one
still running, since a name for work in progress cannot mean the container that
finished yesterday.

**A reference only ever reaches containers sandbox-cli started.** It is matched
against a listing filtered by our own label and is never handed to docker to
resolve, so `sandbox-cli kill postgres` finds nothing rather than your database.

With exactly one sandbox running you can leave the name out of `logs` and
`attach`. Not out of `kill`: reading the wrong session costs a second, stopping
the wrong agent costs its work.

## Four things worth knowing

- **`attach` cannot kill.** Ctrl-C detaches and leaves the agent running — the
  signal is not proxied into the container. `kill` is a separate word on purpose.
- **A `--detach`ed session has no keyboard.** It was started without stdin,
  deliberately, because nothing was attached to it; `attach` shows you its output
  and tells you it cannot type at it. `logs --follow` is usually what you wanted.
- **`kill` is graceful.** SIGTERM and docker's grace period, so an agent gets to
  finish the file it was writing. `--force` is SIGKILL and has to be asked for.
- **Finished sessions are kept.** Detached and fleet containers are not removed
  when they exit, because their exit code and logs are the only record the run
  happened. `sandbox-cli clean` reaps them once you have read what you needed.

The `ID` is the same one [`sandbox-cli stats`](monitoring.md#watching-any-run)
prints, so a row there can be handed straight to `attach`, `logs` or `kill`.

Under Podman these commands need to be told which engine to look at
(`--engine podman`, or the config key) — see
[Podman's known limits](../platforms/podman.md#known-limits).

---

Next: [Monitoring a run](monitoring.md) · [Worktrees](worktrees.md) ·
[documentation index](../README.md)

## The session server, and the two things called a snapshot

`sandbox-cli serve` is a second way to look at the same containers, and the piece that
makes a detached run survivable. One daemon per repository catalogs its sandboxes —
which worktrees exist, which containers are in them, how each is doing — instead of
every command re-deriving the grouping from labels.

```sh
sandbox-cli serve              # foreground, one per repository
sandbox-cli pane list          # the containers, grouped by worktree
sandbox-cli pane wait p_3f21 --state blocked --state done
sandbox-cli session snapshot   # the whole catalog, as the protocol has it
```

Two things it adds that `sandbox-cli list` cannot:

- **Detached runs get snapshotted.** A foreground `sandbox-cli claude` snapshots every
  two minutes; a `--detach` run and a Studio run never did. With a daemon running, every
  pane is, so work an agent had not committed is recoverable.
- **Pane state comes from the agent's conversation**, not only its container, so
  `blocked` (the agent is waiting for you) is distinguishable from `working`.

Stopping the daemon leaves every container running. The engine owns them, which is the
same reason `sandbox-cli list` survives a killed CLI — and restarting `serve` rebinds the
live ones from their labels and marks the rest `stopped`. It starts nothing: a restart
that respawned agents would be a restart that could double one.

### Layout and files are different layers

The word "snapshot" means two unrelated things in this tool, and the one you want when
work has gone missing is always the second:

| | `session snapshot` | `recover` |
|---|---|---|
| holds | **layout** — worktrees, panes, their state | **files** — the workspace, untracked ones included |
| lives in | `~/.config/sandbox/sessions/<repo>/session.json` | git, under `refs/sandbox/snapshots/` |
| restoring it | starts no agent, changes no file | gives you a branch with your work on it |
| survives a reboot | yes, as a record; the containers do not | yes |

A pane's `last_snapshot` is a pointer from the first layer into the second: the **newest**
snapshot taken of that pane's workspace. Its `baseline` is a different pointer — the
workspace as the run *started*, recorded before the agent did anything. Restoring a
baseline gives back the state before the work, which is worth knowing before you reach
for one.

`sandbox-cli doctor` says whether a server is running for the repository you are in, and
what is lost when one is not.
