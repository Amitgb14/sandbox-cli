# Watching a run

Four places sandbox-cli reports what a container is doing: a live gauge on
non-interactive runs, a status line inside Claude, a peak summary after every
run, and two commands you can use from anywhere.

- [The live resource gauge](#the-live-resource-gauge)
- [The status line inside Claude](#the-status-line-inside-claude)
- [Watching any run (`stats`)](#watching-any-run)
- [Subscription windows (`usage`)](#subscription-windows)

## The live resource gauge

For **non-interactive** runs (`--no-tty`, or piped/redirected stdio), sandbox-cli
pins a live resource gauge to the bottom of the terminal showing the container's
memory, CPU, and elapsed time, with the workspace's git branch on the right —
output scrolls above it, and it's erased when the run ends:

```
work line 3
 sandbox-cli │ mem 512MiB/7.6GiB ▕▓░░░░░░▏ cpu 82% · 0m47s        ⎇ feature/login
```

The branch is what tells parallel `--worktree` sandboxes apart at a glance. It is
dropped when the terminal is too narrow to fit both halves on one line, and absent
when the project isn't a git repository (a detached HEAD shows the commit id).

It is intentionally **not** drawn during an interactive agent session (Claude/Codex
own the full screen). Instead, **every** run (interactive included) prints a one-line
peak-usage summary after it exits — so you still get the numbers for a Claude session:

```
sandbox-cli: peak mem 412MiB · cpu peak 138% · 12m04s · ⎇ feature/login
```

The summary is sampled in the background without touching the screen, and is skipped
for containers too short-lived to sample. Disable all of this with `--no-metrics`.
Measurement only — no limits are placed on the container.

## The status line inside Claude

**Inside a `sandbox-cli claude` session**, a status line at the bottom of the Claude
UI shows the container's live memory/CPU, plus how much of your subscription window
is spent and when it resets (via Claude Code's `statusLine`, injected through a
managed-settings file that never touches your own Claude settings):

```
⬢ sandbox · opus 5 · mem 412MiB · cpu 82% · 5h 23% (2h14m) · wk 49%   ⎇ feature/login
```

`5h` is the 5-hour session window, with the time until it resets; `wk` is the weekly
one; and `opus 5` is the model answering — the three are read together, since how fast
a window drains depends on what is running. Those come from Claude Code itself, which
reports the windows only on a Claude.ai plan and only once it has made a request —
under API-key auth, or before the first response, that part of the line is simply
absent rather than showing a placeholder. `--env SANDBOX_STATUSLINE_NO_USAGE=1` and
`--env SANDBOX_STATUSLINE_NO_MODEL=1` leave them out individually.

The branch sits at the right edge, padded against the terminal width read from
`/dev/tty`. If the width can't be determined, or the line is too narrow for both
halves, the branch is dropped rather than shown truncated. On a narrow terminal what
the agent reports about itself goes first — the usage figures, then the model — and
the branch is the last to go: a percentage is stale within the hour, the branch you
are on is not. Two escape hatches, in case Claude renders the status line in an area
narrower than the terminal:

```sh
sandbox-cli claude --env SANDBOX_STATUSLINE_RULER=1     # print a column ruler instead: read off the real width
sandbox-cli claude --env SANDBOX_STATUSLINE_COLS=76     # align against that width instead of the terminal's
sandbox-cli claude --env SANDBOX_STATUSLINE_RESERVE=8   # keep more columns free (default 4)
```

Start with the ruler: the last number still on screen *is* the usable width. Claude's
status-line JSON carries no terminal geometry (it reports `worktree.branch`, `model`,
`pr`, … but no width), so the alignment has to work from the tty width, and these
exist for when Claude's frame is narrower than the terminal.

Disable it with `--no-statusline`. Only `claude` gets one: no other agent has a
status-line hook, and running them under tmux to fake one was tried and reverted
because it made their TUIs render badly.

## Watching any run

`sandbox-cli stats` in a second terminal — a refreshing table of all running
sandbox containers:

```sh
sandbox-cli stats            # live table, refreshes every 2s, Ctrl-C to exit
sandbox-cli stats --once     # a single snapshot (scriptable)
sandbox-cli stats --interval 1s
```

```
sandbox-cli — live stats  15:04:05  (Ctrl-C to exit)

ID            CONTAINER             MEM                CPU     PIDS
a1b2c3d4e5f6  sandbox-dk0gtrd15s2g  412MiB / 7.6GiB   82.00%  24
```

The `ID` is the same one `sandbox-cli list` prints, so a row here can be handed
straight to `attach`, `logs` or `kill` — see [Sessions](sessions.md).

## Subscription windows

`sandbox-cli usage` prints the same two windows the status line shows, from
anywhere — a second terminal, or after a run has ended:

```sh
sandbox-cli usage
sandbox-cli usage --json      # scriptable
sandbox-cli usage --refresh   # spend one throwaway turn to make the reading current
```

```
5h            23%  resets in 2h14m  (14:14)
week          49%  resets in 4d     (Wed 12:00)
week (Fable)  25%  resets in 4d     (Wed 12:00)

claude, as of 4m ago — ~/.config/sandbox/agents/claude/.claude.json
```

A row named with a model in parentheses is a cap on **that model alone**, which some
plans meter separately alongside the account-wide window.

There is no way to ask for a live reading without an interactive session, so this
reads the numbers Claude Code caches for its own `/usage` display — which is why it
always tells you how old the reading is. They refresh when the agent talks to the
server, so an idle machine can hold a figure from hours ago. If claude has not run
signed in to a Claude.ai plan there is nothing to read, and the command says where
it looked rather than printing a zero.

---

Next: [Sessions](sessions.md) · [Agent login and history](agent-login.md) ·
[documentation index](../README.md)
