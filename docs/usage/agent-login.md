# Agent login, history and past conversations

What each agent wrapper persists on your host, what it deliberately does not
touch, and how to find and resume a conversation.

- [Persistent agent login](#persistent-agent-login)
- [Shared conversation history](#shared-conversation-history)
- [Past conversations (`context list`)](#past-conversations-context-list)

Per-agent detail — prerequisites, login flows, forwarded variables, `--allow`
domains — is in the [Agent reference](../AGENTS.md).

## Persistent agent login

Every agent wrapper **persists the agent's login by default**, so you
authenticate once and it survives the throwaway containers. A dedicated,
sandbox-owned host directory is bind-mounted as the agent's whole home:

```
~/.config/sandbox/agents/claude    ->  /sandbox/home   (sandbox-cli claude)
~/.config/sandbox/agents/codex     ->  /sandbox/home   (sandbox-cli codex)
~/.config/sandbox/agents/gemini    ->  /sandbox/home   (sandbox-cli gemini)
~/.config/sandbox/agents/opencode  ->  /sandbox/home   (sandbox-cli opencode)
~/.config/sandbox/agents/cline     ->  /sandbox/home   (sandbox-cli cline)
~/.config/sandbox/agents/goose     ->  /sandbox/home   (sandbox-cli goose)
~/.config/sandbox/agents/copilot   ->  /sandbox/home   (sandbox-cli copilot)
~/.config/sandbox/agents/cursor    ->  /sandbox/home   (sandbox-cli cursor)
~/.config/sandbox/agents/qwen      ->  /sandbox/home   (sandbox-cli qwen)
~/.config/sandbox/agents/openhands ->  /sandbox/home   (sandbox-cli openhands)
~/.config/sandbox/agents/droid     ->  /sandbox/home   (sandbox-cli droid)
```

The whole home is persisted (not just `~/.claude`) because agents keep their
"onboarding done" flag and account info in `~/.claude.json` — a file in the home
root — and write config via atomic rename, which a single-file bind mount can't
capture. This directory is **separate from your host `~/.claude`** — the sandbox
does not read or write your real Claude/Codex *config* or credentials. (Your host
*conversation history* for the project is shared by default; see below.) The
first `sandbox-cli claude` prompts you to log in; subsequent runs reuse the
stored session. Opt out for a one-off, throwaway session:

```sh
sandbox-cli claude --no-persist-auth
```

The first run builds the `sandbox-base` image (Node + git + common tools, with
Claude Code and Codex pre-installed). Rebuild with `--build`.

**Claude Code stays current.** The baked copy is a fallback; on first use
`sandbox-cli claude` installs Claude Code into the persisted HOME (`~/.local`, via
the official installer) where it is writable, so it self-updates from then on and
matches the version you'd get on the host — no rebuild needed.

> Under `--profile prod` the persisted home is **not mounted at all**: the
> refresh token it holds is a long-lived credential, and prod's answer is that
> there is nothing in the container to steal. See
> [Security profiles](../security/README.md#security-profiles).

## Shared conversation history

`sandbox-cli claude` mounts **your host Claude history for the current project**
into the sandbox by default, so a session works the same on either side of the
boundary:

```
~/.claude/projects/<project>  ->  /sandbox/home/.claude/projects/-workspace   (read-write)
```

That means `claude --resume` inside the sandbox lists and resumes sessions you
started on the host, and sessions you run in the sandbox show up on the host
afterwards. Only the directory for the project you're running in is mounted — not
your whole `~/.claude/projects`.

The mount is **read-write**, so an agent in the sandbox can modify or delete the
host-side transcripts for that one project. If you'd rather keep the sandbox's
history completely separate, opt out:

```sh
sandbox-cli claude --no-sync
```

If the host has no history for the project yet, the bucket is created rather than
skipped — without it every sandbox session would pool into a shared `-workspace`
bucket belonging to no project. History sharing assumes the default `HOME` and
workdir; with `--workdir` overridden, session IDs may not line up.

## Past conversations (`context list`)

Because each agent's home persists, so do the conversations it records there.
`context list` shows them for the project you're in, newest first, with the id
you resume by:

```sh
sandbox-cli context list          # every agent that has a verified session store
sandbox-cli claude context list   # one agent — the same command, spelled per agent
```

```
ID        WHEN    TURNS  TITLE
37888763  2h ago  24     Make the egress allowlist fail closed
9c1a04ef  1d ago  8      Why does worktree rm say "is not a working tree"?

resume: sandbox-cli claude --resume 37888763
```

`TURNS` is prompts you sent, not messages exchanged. The listing ends with the
command that resumes the newest session, spelled the way that agent spells it
(`claude --resume <id>` is a flag, `codex resume <id>` is a subcommand).

```sh
sandbox-cli context list --all      # every project, not just this one
sandbox-cli context list --limit 0  # everything (the default stops at 20 and says so)
sandbox-cli context list --full     # whole ids (-f), needed to resume outside sandbox-cli
sandbox-cli context list -v         # also say where each agent's sessions are stored
sandbox-cli context list --json     # for scripts
```

Ids are printed abbreviated, and sandbox-cli expands one back to the full id
before the agent sees it — but only when exactly one session in this project
matches; anything ambiguous or unrecognised is passed through untouched, since
the agent's own error beats resuming the wrong conversation. Running the agent
*directly* needs the whole id (`claude --resume` refuses anything that isn't a
full UUID), which is what `--full` is for — and because a Claude session recorded
in a sandbox lands in your real `~/.claude` history, plain `claude --resume` on
the host picks up the same conversation.

Four agents have a session store that has actually been located on disk:
`claude`, `codex`, `gemini` and `opencode`. Only `claude`'s transcripts are read
in full — the others list the id and date with `?` for the title and count, and
their stores aren't organised by project, so those listings cover every project
and say so. Every other agent is reported `untracked` rather than guessed at: a
wrong path would make "you have no sessions" look like an answer instead of a
gap. When there's nothing to list, the output names the directories it searched.

An id belongs to the agent that created it — codex can't resume a claude session.
Carrying context *across* agents is a handoff, not a resume; it's the next step in
[docs/proposals/shared-context.md](../proposals/shared-context.md).

---

Next: [Agent reference](../AGENTS.md) · [Sessions](sessions.md) ·
[documentation index](../README.md)
