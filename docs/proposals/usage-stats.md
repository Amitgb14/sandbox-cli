# Design: usage stats on the status line

**Status:** implemented. The `sandbox-statusline` usage segment
(`internal/image/assets/Dockerfile`), `internal/agentusage`, `sandbox-cli usage`.
**Depends on:** the Claude status line (shipped, `internal/cli/claude.go`), the
persisted agent HOME (shipped, `internal/cli/agents.go`).

## Problem

The status line reports what the *container* is doing — `⬢ sandbox · mem 1.2GiB ·
cpu 12%` on the left, `⎇ branch` on the right. Those are the numbers sandbox-cli
owns, and they answer "is this run healthy?".

They do not answer the question that actually ends a session. An agent stops
because the subscription window ran out, and the two facts that matter then —
how much of the window is spent, and when it comes back — are visible only by
breaking out of the work to run `/usage`, and not visible at all once you are
outside Claude's own UI: a second terminal, a finished run, a detached container.

The gap is worse in a sandbox than on a host. A sandbox is disposable and
frequently parallel: several agents on several branches draw on **one** account
quota. The resource the user is closest to running out of is the one thing the
resource gauge did not show.

## What is knowable

Three candidate sources, and they are not equally honest.

**1. Claude's own status-line JSON (in-container).** Claude pipes a documented
object to the `statusLine` hook, and it carries `rate_limits`:

```json
{"rate_limits": {"five_hour": {"used_percentage": 23, "resets_at": 1784957400},
                 "seven_day": {"used_percentage": 49, "resets_at": 1785216000}}}
```

`resets_at` is epoch seconds. Documented as present only for Pro/Max accounts and
only after the first API response — absent under API-key auth, and either window
may be missing on its own.

This is authoritative, documented, and *already arriving on a pipe the script
reads* (it is where the branch comes from). It needs no mount, no new file, and
no knowledge of any format sandbox-cli is not entitled to know.

**2. `~/.claude.json` → `cachedUsageUtilization` (host-side).** The cache Claude
Code keeps for its own `/usage` display: `fetchedAtMs` plus
`utilization.five_hour` / `seven_day`, each `{utilization, resets_at, …}` with
`resets_at` as an RFC3339 string, and a richer `limits[]` array carrying per-model
weekly caps.

Undocumented, and a file another program owns. It is also the **only** source
outside an interactive session: there is no `claude usage` subcommand, no
endpoint, and the docs offer no non-interactive equivalent of `/usage`.

**3. The transcripts.** `~/.claude/projects/*.jsonl` assistant lines carry a real
`message.usage` object (input, output, cache-read, cache-creation tokens) with
`message.model` to price it.

### What the other agents record

Surveyed on disk, because "only claude has this" is the kind of claim that is
true until an agent ships a release:

- **codex — the same figure, on a surface we have not seen carry one.** Its
  session rollouts (`~/.codex/sessions/<y>/<m>/<d>/rollout-*.jsonl`) log
  `token_count` events with a `rate_limits` field, and the binary's own type
  names spell the shape out: a `RateLimitSnapshot` (`limit_name`, `primary`,
  `secondary`, `credits`, `plan_type`, `rate_limit_reached_type`) over windows of
  `{used_percent, window_minutes, resets_at}` — structurally the same two-window
  answer claude gives. Every occurrence of it on the machine this was written on
  is `null`, because that install authenticates with an API key: the same reason
  claude reports nothing under API-key auth. So the field is real and the shape is
  legible, and it has still never been *observed holding a number here*. That is
  precisely the state `agentctx` calls unverified, and the rule there decides this
  too — a parser written against a shape no sample has confirmed is a guess
  wearing a parser's clothes. Codex is left out until there is a populated rollout
  to read, which makes it the one entry below that is provisional rather than
  settled: it needs a sample, not a decision.
- **gemini — nothing.** `~/.gemini` holds credentials, settings, trusted folders
  and a `state.json` that is `{"tipsShown": N}`. No quota, usage or reset key
  anywhere in it.
- **opencode — nothing of this kind.** Its sqlite
  (`~/.local/share/opencode/opencode.db`) carries account, credential, session and
  message tables, with no rate-limit column and no such string in the file. The
  most it holds is per-message tokens and cost — the estimate this design rejects
  below.
- **goose — nothing to record.** It is bring-your-own-provider, so there is no one
  subscription window to report; `rate_limit` appears in its source only in retry
  and HTTP-status paths (`goose-provider-types/src/retry.rs`, `http_status.rs`),
  never persisted.

## Design

Use source 1 in the container and source 2 on the host. They are different
surfaces with different guarantees, and each gets the treatment its guarantee
earns.

### In the container: one more segment on a line that already exists

The status-line script pulls both windows out of the JSON it already has, turns
`resets_at` into a countdown, and appends them — along with the model, from
`model.display_name` in the same object:

```
⬢ sandbox · opus 5 · mem 1.2GiB · cpu 12% · 5h 23% (2h14m) · wk 49%    ⎇ main
```

The model belongs next to the limits rather than anywhere else on the line: a
percentage moves at a rate that depends entirely on what is running, and on a
plan with per-model caps the two are read together.

The weekly window gets a percentage and no countdown: it resets days out, where a
live countdown earns none of the columns it costs.

Three rules make it safe to add to a line that was already tight:

- **Absent means absent.** No `rate_limits` key, API-key auth, before the first
  response, one window and not the other, `jq` failing — every one of those
  prints *nothing*. A placeholder next to two real percentages reads as a number
  that failed rather than a limit that is not being reported, which is the same
  bargain `internal/agentctx` strikes when it prints `untracked` instead of a
  guess.
- **What the agent reports about itself is dropped before the branch**, in a
  fixed order: the usage windows first (they are the widest, and a percentage
  ages out of usefulness in minutes), then the model. The branch outlives both —
  it is why the line was there before either of them, and it is the last thing to
  go. On a 55-column terminal the line is byte-for-byte what it was before this
  feature.
- **Only dropped when the width is known.** With no width to measure against,
  showing it matches how `mem` and `cpu` are already treated — the existing
  width caution is about *padding to the right edge*, which a left segment does
  not do.

`SANDBOX_STATUSLINE_NO_USAGE=1` and `SANDBOX_STATUSLINE_NO_MODEL=1` turn the two
new segments off individually, alongside the existing `SANDBOX_STATUSLINE_COLS` /
`_RESERVE` / `_RULER`. `--no-statusline` still removes the whole line.

### On the host: `sandbox-cli usage`

```
5h            23%  resets in 2h14m  (14:14)
week          49%  resets in 4d     (Wed 12:00)
week (Fable)  25%  resets in 4d     (Wed 12:00)

claude, as of 4m ago — ~/.config/sandbox/agents/claude/.claude.json
```

The third row is a **model-scoped** cap, read from the `limits[]` array
(`kind: weekly_scoped`, `scope.model.display_name`) for plans that meter one model
separately. Only *scoped* entries are taken from that array: its unscoped entries
restate `five_hour` and `seven_day`, and a listing showing the same allowance
twice under two names reads as two allowances. The scope has to be in the label
for the same reason — an unlabelled second weekly row is indistinguishable from a
second weekly window.

Reading a file another program owns is the compromise this half is built on, so
`internal/agentusage` carries the two rules that keep it honest:

- **Read only, always.** Nothing in the package opens that file for writing. It
  is Claude Code's file; we are a spectator.
- **Cached, so always aged.** Every snapshot carries the `fetchedAtMs` it came
  with, and the command always prints it. These numbers refresh when the agent
  talks to the server, so an idle machine holds a figure from hours ago —
  an unlabelled stale percentage is the one way this actively misleads. A shape
  the parser no longer recognizes yields *no windows*, never a zero.

There is a sharper form of that second rule, learned from a cache written 29
minutes before its windows reset and read 16 hours later: it reported `week
(Fable) 25%` when the true figure was `0`. Aging the *reading* was not enough,
because once a window has passed its reset the percentage is not merely old — it
describes the period **before** the reset, and no amount of labelling makes it an
answer to what is left now. Such a row prints `rolled over` and **no percentage**,
the same bargain the parser makes with a shape it cannot read. `--refresh` is the
way out of that state rather than a way to dress it up.

Two candidate paths, resolved by which was refreshed last rather than by
precedence: the sandbox-owned HOME (`~/.config/sandbox/agents/claude/.claude.json`)
and the user's real `~/.claude.json`. Both describe the same account and so the
same server-side quota, so the newer reading is simply the better one — the same
instinct as `agentctx.Probe` picking the location with the newest session.

## Rejected

**Summing tokens from the transcripts.** The tokens are real and per-message, and
this is what the ecosystem's usage monitors do. It was rejected on three counts,
any one of which is disqualifying here:

1. **No limit and no reset.** A transcript records what was spent, never what the
   allowance is or when it renews. Both of the user's questions would be answered
   by inference — and the plan tiers are not published in tokens, so the
   inference is a guess dressed as a measurement.
2. **It would silently undercount in a sandbox.** Only the *current project's*
   bucket is mounted into the container. A user with two projects, or a second
   agent on another branch, would see a fraction of their real spend, and nothing
   in the number would say so.
3. **The format is documented as unstable** ("changes between versions, so
   scripts that parse these files directly can break on any release").

An estimate sitting next to two authoritative percentages is worse than no
number: it inherits their credibility without earning it.

**Reading `cachedUsageUtilization` from inside the container too.** It is
mounted and it would work. It was rejected because the container has a
*documented* source on a pipe it already reads — reaching past that for an
internal file would trade a supported contract for an unsupported one to obtain
the same two numbers.

**Surfacing the per-model caps on the status line.** They are in the listing
(above), where a row can be labelled with the model it applies to. The line is a
different problem: the per-model caps are not in the documented `rate_limits`
object at all, so putting them there would mean the container reading the
internal cache — which is the trade the previous entry rejects. The line names
the model instead, which *is* documented, and leaves the caps to the listing.

**Per-model token totals from `~/.claude/stats-cache.json`.** It carries real
per-model `inputTokens` / `outputTokens`, but its `costUSD` reads `0`, so it
answers "how many tokens" and not "how much of my plan" — a different question
from the one this feature exists to answer, reported in a unit nobody's limit is
denominated in.

**A column in `sandbox-cli stats`.** That table is per-container; usage is
per-account. The same figure would repeat on every row and imply it was that
container's.

**Adding `usage` to `wrapperSubcommands`.** `sandbox-cli claude usage` would then
be answered by sandbox-cli instead of forwarded. Every word on that list is one
that can never again reach an agent without a `--`, and `usage` is too ordinary a
word to spend on a command that is account-scoped rather than agent-scoped.

## Non-goals

- Cost in dollars. The cache carries `used_dollars` / `limit_dollars`, but on a
  subscription plan they are internal accounting, not what the user is spending.
- A live query of our own. There is no supported way to ask, and inventing one by
  replaying the agent's credentials against an endpoint nobody documented is out
  of the question. `--refresh` is not that: it asks the agent, through its own
  supported CLI, to make one ordinary request, and then reads the same file as
  before. Opt-in, because that request is spent from the window being measured,
  and bounded rather than instant — Claude Code refetches on its own interval, so
  a refresh makes a reading minutes old where an idle machine had hours, and the
  printed age still says which.
- Other agents. Only claude's windows are read, because they are the only ones
  that have been seen holding a number (the survey is above: gemini, opencode and
  goose record nothing of the kind; codex records the same shape, but only under a
  ChatGPT plan and never populated here). An agent whose figures have not been
  observed is reported as not recording them rather than guessed at.

## Known consequences

- **On a mid-width terminal the branch can now be pushed off.** Usage is dropped
  first, but once both fit the branch still competes for what remains, exactly as
  it did with `mem` and `cpu`.
- **`sandbox-cli usage` depends on a shape nobody promised.** If Claude Code
  renames the field, the command reports "no usage recorded" — honest, but a
  silent loss of function. The status line is unaffected: it reads the documented
  contract.
- **Two readers of two sources.** The in-container bash and the host-side Go both
  parse "a percentage and a reset time" and neither shares code with the other.
  That duplication is deliberate: they read different inputs with different
  stability guarantees, and merging them would mean giving one of them the
  other's weaknesses.
