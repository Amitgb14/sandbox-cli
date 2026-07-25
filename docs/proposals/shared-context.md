# Design: shared context between sandboxes

Status: in progress. Steps 0–3 are implemented — the stores are verified and recorded
(`sandbox-cli context stores`) and the sessions in them are listed
(`sandbox-cli context list`). Everything from step 4 on — the context manifest that
binds several agents' sessions into one thread, and the handoff — is still proposal.

## Problem

A sandbox is disposable; the conversation that happened inside it should not be.
Three separate wants, which are usually asked as one question:

1. **Resume the same agent later.** `sandbox-cli claude`, quit, come back tomorrow,
   `sandbox-cli claude --resume <id>` and pick the thread back up.
2. **Carry the same context to a *different* agent.** The Claude Code session ran out
   of road; continue the same work in Codex, with the same history.
3. **See what exists.** There is no way to ask sandbox-cli what conversations it is
   holding — the ids live only inside each agent's own store.

### Can (2) actually work?

**Not as a literal resume, and it is worth being blunt about why.** A session id is not
a portable handle; it is a primary key into one vendor's private store:

- **Different namespaces.** Claude Code names sessions with a UUID under
  `~/.claude/projects/<bucket>/<uuid>.jsonl`. Codex writes rollout files under
  `$CODEX_HOME/sessions/<date>/rollout-*.jsonl`. Neither will ever look up the other's id.
- **Different schemas.** Both are JSONL, and that is where the resemblance ends: message
  envelopes, ids, parent links, and metadata differ entirely.
- **Different semantics.** A Claude transcript is full of `tool_use` blocks naming tools
  (`Edit`, `Bash`, `TodoWrite`) that the target agent does not have, plus provider-specific
  reasoning blocks. Replaying it into another agent hands it a past it could not have had.

So `sandbox-cli codex --resume <claude-session-id>` is not a thing that can be built
honestly. What *can* be built is a sandbox-cli-owned **context** that both runs belong to,
with a lossless path within one agent and a distilled handoff across agents.

## What is possible, in three tiers

| Tier | Scope | Fidelity | Mechanism |
|---|---|---|---|
| 1. Resume | same agent, later run | lossless | the agent's own store, persisted on the host |
| 2. Handoff | claude → codex, etc. | distilled, lossy | normalize → brief → seed the target |
| 3. Live share | two agents at once | — | out of scope (see Non-goals) |

Tier 1 already half-works today and nobody can see it. Tier 2 is the new build.

## Design

### The context, and its manifest

One JSON manifest per context, outside every repository, mirroring `internal/rescue`:

```
~/.config/sandbox/contexts/<repo-id>/<context-id>.json
```

```jsonc
{
  "id": "20260725-141203-a91f",
  "repo": "/Users/x/app", "workspace": "/Users/x/app", "branch": "feature/api",
  "title": "add pagination to /orders",        // first user turn, truncated
  "created_at": "...", "updated_at": "...",
  "legs": [
    {"agent": "claude", "native_id": "3f2a…", "store": "~/.config/sandbox/agents/claude/.claude/projects/-workspace/3f2a….jsonl",
     "format": "claude-jsonl", "turns": 41, "started_at": "...", "ended_at": "..."},
    {"agent": "codex",  "native_id": "0c81…", "format": "codex-rollout", "turns": 12, "seeded_from": "claude"}
  ]
}
```

A **leg** is one agent's participation. A context is the thread; legs are how each agent
remembers its share of it. Repo-scoped for the same reason rescue is: identity comes from
`worktree.RepoID`, and a conversation belongs to a project.

Reuse rescue's proven shape verbatim — atomic `Save`, tolerant listing that skips a corrupt
file, prefix-matching id lookup. Same ergonomics, no new concepts to learn.

### Learning the native session id

sandbox-cli does not control the agent's id, so two mechanisms, preferring the first:

- **Mint.** Where the agent accepts an id (Claude Code's `--session-id <uuid>`), sandbox-cli
  mints the context id as a UUID and passes it. The context id *is* the native id — nothing
  to reconcile. Only injected when the user did not pass their own.
- **Observe.** Otherwise, list the agent's session directory inside the persisted HOME before
  the run and again after; files that are new or whose mtime moved are this run's session.
  Deterministic, needs no cooperation from the agent, and works for every agent whose store
  we know.

### Per-agent store descriptors — `internal/agentctx` *(implemented)*

A descriptor is a directory to look in, a pattern that matches a session file, and how deep
below that directory those files sit — enough to verify a layout without encoding a vendor's
scheme into the probe:

```go
type Store struct {
    Agent, Format string
    Candidates    []Candidate // {Root: "agent"|"home", Rel: ".claude/projects"}, best-known first
    Glob          string      // "*.jsonl", "rollout-*.jsonl"
    MaxDepth      int         // 1 for a per-project dir, 3 for a date-sharded tree
    Resume        []string    // {"--resume"} / {"resume"}
    MintIDFlag    string      // "--session-id", or "" if unsupported
}
```

Two roots, not one, because both are real: most agents keep sessions in the sandbox-owned
HOME that persists their login, but the claude wrapper mounts the host's own project history
over that path by default, so claude's transcripts land in the user's real home instead
(`--no-sync` puts them back). The probe checks both, records every location it finds, and
picks the **most recently used** one — "most sessions" would be wrong for anyone who
switched `--no-sync` yesterday and has a large stale store next to the live one.

`claude`, `codex`, `gemini` and `opencode` have descriptors; every other adapter is
**untracked**, and is listed as untracked rather than pretended about.

#### What step 0 actually verified

Confirmed first-hand against Claude Code 2.1.219 (the rest await a machine that has run them):

- Transcripts are `$HOME/.claude/projects/<bucket>/<session-uuid>.jsonl`, bucket = the
  absolute workspace path with `/` and `.` replaced by `-`. 33 sessions found where `find`
  agrees there are 33.
- A session's **subagent transcripts** sit one level deeper, at
  `<session-uuid>/subagents/agent-*.jsonl`. They are sidechains of a session, not sessions;
  `MaxDepth: 1` is what excludes them, and the test pins it (10 were present and excluded).
- `$HOME/.claude/sessions/<pid>.json` is a separate **live** registry — `sessionId`, `cwd`,
  `startedAt`, `version`, `kind`, `status`, and a derived `name`. That is a better source
  than mtime-diffing for "which session is this run writing", and a free title for
  `context list`. Worth using at step 3.

### Tier 1 — resume within one agent

Mostly already true, and worth stating because it is the cheapest win here: `finishAgentCmd`
sets `AuthPersistDir` to `~/.config/sandbox/agents/<agent>`, mounted as the container HOME,
so `~/.codex/sessions` and friends **already survive the container**. Gaps to close:

- Nothing indexes them, so the ids are undiscoverable — that is what `context list` fixes.
- `--no-persist-auth` throws the store away with the login. Say so at the point of use.
- The claude wrapper has *two* possible homes for history (the persisted HOME, plus the host
  `~/.claude/projects/<bucket>` mounted by default unless `--no-sync`). The manifest records
  which one a leg landed in; without that, `--resume` looks broken to the user when they flip
  the flag between runs.
- `--workdir` overrides change the project bucket and quietly break resume-by-id. Already
  documented in `claudeHistoryMount`; the context lookup should warn instead of returning
  nothing.

Sugar: `sandbox-cli claude --resume-context <ctx>` looks up the claude leg and expands to that
agent's native resume argv, so the native id never has to be typed or even known.

### Tier 2 — handoff to another agent

`sandbox-cli context export <id> --for codex` writes a sandbox-owned directory, mounted
**read-only** at `/sandbox/context` in the target run:

```
HANDOFF.md       the brief: task, decisions taken, constraints, open threads, next step
transcript.jsonl neutral normalized form — {role, text, tool, args_digest, ts}
files.md         the file ledger: what the previous session actually touched
```

`files.md` is the part that carries the most signal per byte, and it does not come from prose:
it is derived from the transcript's tool calls **and** cross-checked against the rescue
snapshot diff for that session. "These 9 files changed, here is the diffstat" survives
translation perfectly, unlike a summary of intent.

`HANDOFF.md` has two producers:

- **Deterministic (default).** Extracted from the transcript: user turns verbatim, assistant
  turns reduced to their headings and conclusions, tool calls collapsed into the ledger. No
  network, no API key, no token cost, and reproducible — therefore testable in `make test`.
- **`--distill` (opt-in).** A headless pass by the source agent (`claude -p …`, `codex exec …`)
  that writes a better brief. Costs tokens and needs credentials, so it is never the default.

**Seeding the target.** `sandbox-cli codex --context <id>` mounts the export and points the
agent at it. Interactive runs get the pointer through the agent's project-instructions file
**inside the container HOME** (the persisted `AGENTS.md`/`CLAUDE.md`), never by writing into
the user's repository — a handoff must not dirty the worktree it is handing over.
Non-interactive runs get it prepended to the prompt. On exit, the new leg registers itself
onto the same context, so a context accumulates legs and `context show` reads as one thread
across agents.

The screen tells the truth at the moment it matters:

```
sandbox-cli: seeding codex from context 20260725-141203 (claude, 41 turns)
sandbox-cli: this is a briefing, not a resume — codex has the summary, files and
             decisions, not the original conversation.
```

## CLI surface

Canonical, agent-neutral, shaped like `recover`:

```
sandbox-cli context                       # == context list
sandbox-cli context list [--all] [--project DIR] [--limit N] [-v] [--json]   # implemented
sandbox-cli context show <id>             # legs, turns, files touched, how to resume
sandbox-cli context export <id> --for codex [--distill] [-o DIR]
sandbox-cli context rm <id> | context prune [--older-than 30d]
```

Agent-scoped alias, as asked for — the same tree with the agent already chosen, so the
two spellings cannot drift:

```
sandbox-cli claude context list
sandbox-cli codex  context list
```

Today `list` lists **sessions**, one row per conversation in the agent's own store:

```
ID        WHEN      TURNS  TITLE
37888763  just now      4  Share context between sandbox instances with resume
95ad79ff  35m ago      17  sandbox-run-signal-handling

resume: sandbox-cli claude --resume 37888763
```

Once the context manifest lands (step 5) the same command gains a context id and an
AGENTS column, and a session becomes a leg of a context rather than the top-level thing.
The row shape is deliberately close already, so that change adds columns rather than
replacing a command people have learned. Three details worth keeping:

- **TURNS counts prompts the user sent.** In a Claude transcript, tool results come back
  as `user` messages and outnumber real prompts about thirty to one; counting lines would
  make every session look enormous and rank them by tool chatter.
- **The title is the agent's own** (`ai-title`), falling back to the first prompt for
  sessions too short to have earned one. 28 of 33 real sessions had one.
- **The resume line is derived from the verified descriptor**, not hardcoded, so it stays
  right per agent.

**One command, not two.** An earlier cut of this shipped `context stores` beside `context
list` — where the sessions are, next to what they are. Two commands covering one idea, and
the first question anyone asked was which was which. Where a store lives is real
information, but it is wanted only when a listing comes up empty, so it is now reported
*inside* the listing: the directories searched, inline, on the empty path, and behind
`--verbose` when there is something to show. Resist adding the second command back; add
detail to the empty path instead.

**One wiring problem this creates, and its answer** *(implemented)*. Agent wrappers run with
`DisableFlagParsing: true` and forward every non-sandbox token verbatim — that is the
documented second mode in CLAUDE.md, and `sandbox-cli claude context list` would today reach
`claude` as arguments. Intercepting a leading `context` token is a deliberate exception to
that rule, safe now (no supported agent has a `context` subcommand) but not safe forever.
So: make `splitWrapperArgs` also report whether the boundary came from an explicit `--`, and
intercept only when it did not. `sandbox-cli claude -- context list` then still forwards
verbatim, which keeps the two-mode contract intact and leaves an escape hatch for the day an
agent grows its own `context`.

New wrapper flags: `--context <id|new>` (bind this run to a context, minting if `new`) and
`--resume-context <id>` (native resume if that agent already has a leg, otherwise handoff).

## Implementation order

0. ~~**Verify the four session stores**~~ — **done, and made permanent.** Verification is
   not a one-off check that ends in a comment: `sandbox-cli context stores` probes the
   machine and records the result in `~/.config/sandbox/contexts/stores.json`, so the paths
   this feature stands on are re-confirmable on any machine and re-usable later without
   another look. Candidate layouts ship in the code; only a probe promotes one to a fact.
1. ~~`internal/agentctx`: descriptors, probe, sticky registry, table tests.~~ **Done.**
2. ~~A user-visible way to run and read the verification.~~ **Done** — `context stores`,
   `--cached`, `--json`, `--agent`, plus the `sandbox-cli <agent> context …` alias and its
   `--` escape hatch.
3. ~~Read a verified store into sessions and list them.~~ **Done** — `context list`, the
   claude-jsonl reader, project scoping via the derivable bucket, and `Partial` sessions for
   stores that can be found but not yet parsed. This answers question (3).
4. The context manifest (`~/.config/sandbox/contexts/<repo-id>/<id>.json`), modelled on
   `internal/rescue/session.go`, binding several agents' sessions into one thread.
5. Recording: start a leg beside `beginRescue` in `execute()`, close it after the run, fill
   the native id by mint-or-observe (`~/.claude/sessions/<pid>.json` makes this exact for
   claude). `context list` grows a context id and an AGENTS column; `context show` lands.
6. `--context` / `--resume-context`, native resume path (Tier 1 complete).
7. `context export`, deterministic `HANDOFF.md`, mount + seed (Tier 2 complete); then
   `--distill`.
8. Docs, kept current with each step: `docs/AGENTS.md` per agent, the `GUIDE.md`
   walkthrough, the architecture bullet in `CLAUDE.md`, `CHANGELOG.md`.

Steps 1–6 answer questions (1) and (3) completely; 7 is the part that answers (2) as well as
it can be answered.

### What the next reader-writer should know

Verified against a real store, and encoded in the tests rather than only here:

- A `user` line is **not** a user turn when it carries a `tool_result` block, when
  `isMeta` is set (injected caveats), or when `isSidechain` is set (a subagent's own
  conversation). Across 33 real transcripts: 2512 tool-result user messages against ~20
  text-block prompts and a few dozen plain ones.
- A prompt that carried an image is a block array with a `text` block — a real turn, not a
  tool result. Do not treat "array content" as "not a prompt".
- Sessions are read while they are being written. The reader tolerates a truncated final
  line, because the session the user most wants to see is the one running right now.

## Rejected

- **Transcribing a Claude transcript into a Codex rollout ("fake resume").** It is the thing
  the feature request literally asks for, and it is the one option with an unbounded failure
  mode: the formats are private and change without notice, tool ids and names do not map, and
  the target agent ends up believing a fabricated history — confidently, and with file-writing
  tools. The first upstream schema bump corrupts sessions silently. A brief that is honestly
  labelled a brief is worth more than a resume that is quietly wrong.
- **One shared transcript both agents append to.** Same schema problem, plus two writers.
- **Keeping contexts inside the repo (`.sandbox/contexts/`).** Rescue already settled this: the
  repository is often the broken thing, and this would dirty the very worktree under review.
- **Reusing `--share` / `/shared` as the handoff channel.** That is deliberately an
  unmanaged scratch space with no repo scoping and no manifest; a context needs identity,
  listing and retention. It remains the right tool for "hand this file over", not "hand this
  conversation over".
- **Tracking contexts for every adapter regardless of store support.** Would list contexts
  that cannot be resumed, which is worse than listing none.

## Non-goals

- Two agents live in one context at the same time.
- Migrating a session mid-turn.
- Preserving provider-specific reasoning/thinking across vendors.

## Known consequences

- **Handoff is lossy and must always say so** — on export, and again on seed.
- `--no-persist-auth` means no context at all; the run is untracked by construction.
- With observe-only agents a context is known only after the run ends.
- A context carries conversation content, so pulling one into a *different* repository's
  sandbox crosses a project boundary. Contexts are listed per repo and `--context` from
  elsewhere is explicit and announced.
- Disk: transcripts already live in the persisted HOME; exports add one small directory per
  handoff, covered by `context prune`.
