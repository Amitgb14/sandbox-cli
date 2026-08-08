# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`sandbox-cli` runs AI coding agents (Claude Code, Codex, Gemini, OpenCode, Cline, Goose, Crush, Aider,
Copilot CLI, Cursor, Qwen, Amp, Continue, OpenHands, Droid) or any command inside a disposable,
isolated Docker container. Only the chosen project is bind-mounted at `/workspace`; `HOME` is a
fake ephemeral path (`/sandbox/home`) and the container is `--rm` (the single exception is
`--detach`, below). The goal is to give an agent "Allow All" autonomy while limiting the blast
radius to the project it is editing.

## Commands

```sh
make build              # -> bin/sandbox-cli (embeds version via -ldflags)
make install            # go install ./cmd/sandbox-cli
make test               # unit tests, no Docker required
make test-integration   # end-to-end tests; requires a running Docker daemon (builds base image on first run)
make fmt                # gofmt -w .

# Run a single test
go test ./internal/runtime -run TestBuildArgs
go test -tags docker_integration -run TestClaude ./internal/cli   # a single integration test
```

Integration tests are gated behind the `docker_integration` build tag, so `make test` (plain
`go test ./...`) never touches Docker. Go 1.25+.

## Architecture

Data flows one direction through layered packages, each behind an interface so backends can be
swapped without touching callers:

```
cmd/sandbox-cli  →  internal/cli  →  config.Load + sandbox.BuildSpec  →  runtime.BuildArgs  →  docker
```

- **`internal/config`** — the layered config schema and merge. Precedence (later wins): built-in
  `Default()` → `~/.config/sandbox/config.yaml` → nearest `.sandbox.yaml` (walking up from cwd) →
  CLI flags. Mount host paths are resolved to absolute relative to the file that declared them.
- **`internal/sandbox`** — composition layer. `BuildSpec(cfg, opts)` folds config + per-invocation
  `Options` into a fully-resolved `runtime.RunSpec`. `mounts.go/ResolveWorkspace` enforces the
  **non-overridable safety refusals**: never mount `/`, the host home, or an ancestor of it.
  `timezone.go` forwards the host zone as `TZ` so timestamps written in the container (a git
  commit above all) carry the user's offset instead of the image's UTC — a **name**, never a
  mount of `/etc/localtime`, since a name is a string and a mount is another host path. It
  yields to any `TZ` the user set themselves, and an unresolvable zone forwards nothing rather
  than guessing. `hostTimezone` is a var so tests can pin the one input that differs per machine.
- **`internal/runtime`** — `BuildArgs(RunSpec) []string` is a **pure, deterministic function** that
  produces the `docker` argv. `engine.go` holds the **podman dialect**: the engines differ in only
  three places — how they answer questions about the host, how they isolate containers from each
  other, and the binary name — so it is a dialect rather than a second backend. Two facts there were
  measured, not assumed, and both are pinned by test. Rootless podman **can** program the egress
  firewall from inside the container (nat, owner, conntrack and REDIRECT all succeed), so the
  allowlist needs no weaker mode. And netavark has no `enable_icc`: its `isolate=true` blocks traffic
  between *different* networks while leaving same-network peers reachable — confirmed by reading one
  container's data from another — so podman gets **one isolated network per sandbox**, where docker
  shares one. Podman answers `info` with different shapes too, and via JSON keys rather than template
  field names, because its Go struct names and JSON keys disagree. A **fourth** difference showed up
later and only on native Linux, which is why the first version looked complete: rootless podman maps
the host user to container **uid 0**, so a bind-mounted workspace appears root-owned and the sandbox
user cannot write to it — and SELinux denies the bind outright, so it cannot read it either.
`RunSpec.HostUserMapping` renders `--userns=keep-id:uid=1001,gid=1001` plus `relabel=shared`, and the
container user goes numeric so the *group* maps too (`--user sandbox` left files owned by
host-uid:subgid). Docker gets none of it, which is what keeps the golden `--dry-run` test meaningful
rather than merely passing.

  Docker on native Linux has the *same* problem from the other end, and it surfaced as a bug about
  logins rather than about ownership: bind mounts there carry real uids, the container user is 1001,
  and sandbox-cli's own state dirs are created by the host user at 0700 — so the persisted agent HOME
  was unreadable **and** unwritable, the agent found no credentials, and the login it then completed
  died with the container. `sandbox/hostgroup.go` answers it with a shared **group** rather than a
  chown or a uid remap, and the rejected alternatives are why: chowning to 1001 breaks the host side
  (`context list`, `usage`, and the host's own Claude Code writing the history bucket), while running
  as the host uid leaves an unwritable HOME on every run that does not mount a persisted one.
  `sharedGroupUser` renders `--user 1001:<host gid>` — applied to `SANDBOX_RUN_AS` too, since in
  allowlist mode the entrypoint is what the drop lands on — and `ShareWithSandboxGroup` opens the
  group bits with setgid, non-recursively (the tree inside is the container's own, and this runs on
  every launch). `hostPrimaryGID` is a var for the same reason `hostTimezone` is.

  This is the single choke point for the isolation invariants (only
  declared mounts are host-connected; `HOME` is always the fake path; host home is never mounted)
  and is exhaustively unit-tested. `docker_cli.go` is the only backend today, hidden behind the
  `Runtime` interface.
- **`internal/image`** — lazily builds the embedded base image (`assets/Dockerfile`, `//go:embed`)
  on first use via the `Runtime`'s builder hook.
- **`internal/metrics`** — the sticky-footer live resource gauge for non-interactive runs only.
- **`internal/worktree`** — `--worktree <branch>` and the `worktree` subcommands. A worktree is
  addressed by **branch**, never by a directory name derived from one: an agent that runs
  `git checkout -b` inside its worktree puts the two out of sync, so `Resolve`, `Path` and `List`
  ask git which worktree has the branch checked out. The path is symlink-resolved as soon as the
  directory exists, so the string handed out when a worktree is created is the same one git reports
  when it is reused (`/var` vs `/private/var` on macOS) — the mount, `worktree rm` and
  `worktree git` all address it by that string, so the three must agree. The worktree is mounted at
  its own host path so git cannot prune it away mid-session.
- **`internal/netpolicy`** — a vestigial seam that only turns `network: none` into
  `--network none`. The egress allowlist (`network: allowlist` / `--allow`) does **not** live here:
  `config.NetworkSpec.EgressDomains` resolves it (baseline domains ∪ configured ones),
  `sandbox.BuildSpec` passes it in as `SANDBOX_EGRESS_ALLOW`, and it is enforced *inside* the
  container by `sandbox-firewall` / `sandbox-egress-setup` (`assets/Dockerfile`), which programs a
  default-deny firewall as root and then drops to the sandbox user. Keep it failing closed: a run
  that asks for an allowlist and cannot program it refuses to start rather than running open.
  `baseline: false` drops the built-in domains so `allow` is the whole list — it exists because
  `allow` could only ever *add*, leaving no way to decline `github.com`, which is a write endpoint
  and so an exfiltration channel for any token the agent holds. It is tri-state (`*bool`) for the
  same reason the security fields are: a nearer config must be able to turn it back **on**. The
  edge that matters is the empty one — the firewall is wired only when there are domains to
  permit, so `BuildSpec` **refuses** an allowlist that resolved to nothing rather than handing back
  a container with no filtering at all, which is the strictest request producing the weakest
  result. `mode: none` is how you ask to reach nothing.

  Two limits worth knowing before trusting the mode. The rules match resolved **IPs**, not names,
  so a host sharing an allowlisted address rides in on it (`gist.github.com` is reachable under the
  baseline via `github.com`'s IP, and it was never listed) — and names are resolved **once** at
  container start, so a rotating record can break a domain the user did allowlist, mid-session. It
  Ingress is filtered too, and has to be: the `OUTPUT` chain accepts `ESTABLISHED,RELATED`, so a
  connection dialled *in* got a working reply path and carried data straight back out past the
  allowlist (demonstrated — a co-resident container pulled 30 bytes out of an allowlisted sandbox
  that could not reach `1.1.1.1` itself). `INPUT` is now default-deny with three exceptions:
  loopback, `ESTABLISHED,RELATED`, and the container ports named by `--publish`
  (`SANDBOX_INGRESS_PORTS`, from `sandbox.IngressPorts`) — publishing *is* an explicit request for
  ingress, and without the carve-out a dev server would silently stop answering the moment someone
  added `--allow`. `FORWARD` is left alone: a container netns has one interface and routes nothing.
  The IP-vs-name gap is closed by `internal/egressproxy`: a proxy running **inside** the
  container as its own uid, which the firewall permits to egress while REDIRECTing everyone
  else's tcp/80 and tcp/443 into it. It reads the hostname from the TLS SNI, an explicit
  `CONNECT`, or an HTTP `Host` header, resolves fresh per connection, and decides on the
  name — so `gist.github.com` no longer rides in on `github.com`'s address and a rotating
  record no longer breaks an allowlisted domain. Its source is embedded in that package,
  compiled by a builder stage (users have no Go toolchain), and `image.Ref` hashes it so a
  changed proxy produces a new image tag. It deliberately does **not** terminate TLS; that
  is the credential-injection work, and a separate decision.

  The whole firewall, ingress included, runs in allowlist mode only. Filtering ingress on every run
  would mean every run taking the root-entrypoint path with `NET_ADMIN`, which is a worse default
  than the one it would be protecting.
- **`internal/rescue`** — the crash safety net and `sandbox-cli recover`. Recovering the
  *work* and recovering the *conversation* are two different things, and the output says
  both: after a crash the files are usually already on disk (a bind mount), so
  `RestoreResult.MatchesWorkingTree` reports when nothing was actually missing, and
  `cli/recover_resume.go` correlates the run with its agent transcript — by agent, project
  and time window, all three already in the manifest — to print the resume command. It
  declines to guess: no agent, no verified store, or nothing in the window all print how to
  look rather than an id, because resuming the *wrong* conversation is worse than offering
  none. Snapshots the workspace
  into `refs/sandbox/snapshots/<session>` while a run is in flight, using a **private
  `GIT_INDEX_FILE`** so the user's index, `HEAD`, branches and working tree are never written.
  Session manifests live outside every repo (`~/.config/sandbox/rescue/<repo-id>/`) because the
  repo is often the broken thing. Keep the rule: rescue only ever *creates* objects and refs
  under `refs/sandbox/`. Design and rejected alternatives: `docs/proposals/crash-recovery.md`.
- **`internal/agentctx`** — where each agent keeps its conversation transcripts, and the
  persisted record of what has actually been confirmed. The paths in `stores.go` are
  *candidates*, not facts: `Probe` looks for them on this machine and the `Registry`
  (`~/.config/sandbox/contexts/stores.json`) writes down what was found. The merge is
  deliberately **sticky** — a probe that finds nothing never erases a store verified
  earlier, because an agent HOME that is not mounted today is not the same as a store that
  never existed. `sessions.go` reads a verified store into `Session` values —
  the claude-jsonl reader is the only one written against a confirmed format, so
  everything else lists `Partial` (id and dates real, title and turn count shown as
  `?`). Surfaced by the single command `sandbox-cli context list` — where a store
  lives is reported *inside* that listing (inline when it is empty, under `--verbose`
  when it is not) rather than by a second command, which read as two overlapping
  things to learn. First step of
  `docs/proposals/shared-context.md`; keep the two rules that make it honest — an
  agent with no verified descriptor is reported `untracked` rather than guessed at,
  and a *user* turn is a prompt someone typed, never a tool result coming back as a
  user message (they outnumber real prompts ~30:1).
  The listing prints ids abbreviated, so `resume.go/expandResumeID` grows one back into the full id
  before the agent sees it (the agents resolve no prefixes; Claude Code rejects anything that is not
  a whole UUID). That rewrite is deliberately timid — it fires only on the token after a known
  resume argument from the verified descriptor, only within *this* project's history, and only when
  exactly one session matches; anything else is forwarded untouched, because the agent's own error
  beats resuming the wrong conversation.
- **`internal/agentusage`** — how much of the subscription window is spent and when it
  resets, for the host side (`sandbox-cli usage`). The container does not use it: Claude
  pipes the status line a documented `rate_limits` object, and reaching past a supported
  contract for an internal file to get the same two numbers would be a bad trade. Off-line
  there is no such pipe and no supported query at all, so this reads the cache Claude Code
  keeps for its own `/usage` (`~/.claude.json` → `cachedUsageUtilization`) — which makes the
  two rules non-negotiable: **read only** (nothing here ever opens that file for writing;
  it belongs to Claude Code), and **always aged** (every `Snapshot` carries the `fetchedAtMs`
  it came with and the command always prints it — these refresh only when the agent talks to
  the server, so an unlabelled percentage can be hours stale). A shape the parser no longer
  recognizes yields *no windows* rather than a zero, the same bargain `agentctx` makes with
  transcripts — and a window **past its reset** prints no percentage either, because the
  cached figure then measures the period before the reset rather than a stale amount of the
  current one. Two candidate paths — the persisted agent HOME and the user's real home —
  resolved by whichever was refreshed last, since both describe the same account.
  `usage --refresh` (`refresh.go`) is the only way to make a reading current: it runs one
  throwaway `claude -p` turn, in a scratch cwd so the turn stays out of the project's
  transcript history, and re-reads. That keeps both rules — Claude Code still writes its own
  file, we only give it a reason to — and it stays opt-in because the request is spent from
  the window being measured. It bounds staleness to Claude Code's own refetch interval; it
  does not stamp the reading now, so the printed age still governs.
  Design: `docs/proposals/usage-stats.md`.
- **`internal/audit`** — the run log (`~/.config/sandbox/audit/sessions.jsonl`), one line per
  run: image, workspace, branch, agent, command, network posture, resolved egress allowlist,
  exit code, duration. Written *after* the run, because "how did it end" is the point. The rule
  that shapes it: environment variables are recorded **by name only**. The credential broker
  exists to keep secret values off the argv and out of config files, and a log is a file — so
  `SessionMeta` has nowhere to put a value, deliberately. Best-effort and silent on failure: the
  run is what the user asked for, the record is a courtesy.
- **`internal/agents`** — the agents the fleet knows how to start, as data: guest argv,
  `EnvAllow`, container `Env`, persisted-HOME name, and `Autonomous(prompt)`. Only agents with
  a **verified headless mode** are in it (claude, codex, gemini, opencode, droid), because a
  fleet is unattended and an agent that stops to ask permission does not fail — it hangs.
  `TestEveryAgentHasAVerifiedHeadlessArgv` is where that stops being a convention: a new
  descriptor with no recorded non-interactive argv fails the test rather than quietly widening
  what a `fleet.yaml` may name. The wrappers for those five read the same table
  (`cli/descriptors.go`), and that is the whole point: the table was introduced alongside a
  second copy of Claude's bootstrap script and the two had already diverged, one *prepending*
  the agent-writable HOME to `PATH` where the other appended. Two copies of a
  security-relevant script drift silently — which is also why `Bootstrap`/`NpmBootstrap` live
  here rather than in `cli`, and why `Env` does: droid's `FACTORY_DISABLE_KEYRING` sat in the
  wrapper, so a fleet running droid would have got an agent looking for a keyring the
  container does not have, with nobody there to log in again. Deliberately **not** in a
  descriptor: anything producing host paths — the status-line mount, the history mount, the
  persisted HOME itself. A descriptor says what runs inside the container and which host
  variable *names* may cross.
- **`internal/fleet`** — one agent per branch, each in its own worktree and its own detached
  container, from one `fleet.yaml`. It owns **no isolation policy**: every task becomes the
  same `sandbox.Options` a `--worktree` run produces, with `Detach` set, so the boundary is
  defined in one place for both. That inheritance is a rule with teeth — it means every gate
  on the run path must be repeated here, and the one that was missed was `persist_auth`:
  `BuildSpec` mounts `AuthPersistDir` whenever it is non-empty, so prod's "the refresh token
  is never mounted" held for `run` and not for `fleet`. `ValidateProfile` cannot catch that
  class; it validates the resolved `Config`, and the leak is in the `Options`. So the rule is
  now a table rather than a comment: `gates_test.go` classifies **every field of
  `sandbox.Options`** as `fromSpec`, `gated` or `never`, and fails when the struct grows one
  that is not in it. A new field is a new way for a fleet container to differ from the
  interactive container it is supposed to be identical to, and that is where the decision gets
  made rather than noticed later.
  `verify` is what makes a run *autonomous* rather than merely headless: a shell command,
  wrapped around the agent's argv by `withVerify`, whose exit code becomes the container's
  (`0`, or `VerifyFailedExit`). In the container because a verify running on the host would
  be host code selected by a file the agent can write; through `"$@"` because the argv
  carries the prompt, which would otherwise rewrite the script judging it. `land` is the only
  operation that writes to the base branch and refuses on every ambiguity — see below.
  `fleet.yaml` has **CLI-flag trust**, argued in `Load`'s doc comment: it is named with `-f`,
  never discovered upward, and carries no `profile:` key. What it also carries no key for is
  `mounts:` — `--share` reaches the fleet as a **launch option** (`LaunchOptions.ExtraMounts`,
  via the same `cli.shareMount` the run path uses), because a cross-project directory should
  stay something you type rather than something a file copied between repositories turns on.

  A task may name its own `agent:`, and its own `memory`/`cpus`/`allow`. Two resolution rules,
  both in `spec.go` and both tested: caps **replace** the fleet-wide value (with `"0"` meaning
  uncapped at either level), and `allow` **adds** to it — a task that could subtract would be
  asking for a narrower allowlist than the file's author wrote one line above, and the way to
  want less egress is to move the domain onto the tasks that need it. Mixing agents changes
  nothing at the boundary; it changes the *setup*, since each agent named needs its own login
  before an unattended run, which is why `--dry-run` says so for a mixed file.

  `max_parallel` counts through `containers()`, which filters on `sandbox.fleet`. It used to
  filter on repo alone, which meant one open `sandbox-cli claude --detach` session held a slot
  it never freed — the exact failure the label exists to prevent, in the one place that did
  not use it. And `checkCapacity` refuses a fleet whose **concurrent** agents (not its task
  count — bounding concurrency is what `max_parallel` is *for*) cannot fit in the host's
  memory, asked of the engine rather than of `/proc` since on macOS the daemon's VM budget is
  the number that matters. A host that cannot be measured proceeds: this is resource sanity,
  not a boundary control, so prod's "an unanswerable question is a failure" rule deliberately
  does not apply.

  `land --all` classifies refusals rather than collecting them: a refusal about **the branch**
  (`ErrAgentRunning`, `ErrNotVerified`, `ErrNoWorktree`, `ErrNothingToLand`) skips that branch
  and the rest carry on; a refusal about **the base** stops there, because it will be just as
  wrong for the next branch. The sentinels carry no user-facing text — `branchRefusal` pairs
  each with a message written for the person reading it, since classifying an error and
  explaining it are different jobs.
- **`internal/githard`** — neutralises the parts of a repository's git config that make git *run
  commands*, for every git call sandbox-cli makes on its own behalf. See the trust-boundary
  section below.
- **`internal/creds`** — the credential broker. It resolves secret *references* on the host; the
  values reach the container via `RunSpec.ForwardedEnv`, which the docker child gets and
  `BuildArgs` never renders. Keep it that way: they used to travel through sandbox-cli's own
  environment, where a secret named `PATH` redirected the subprocesses spawned next.

  It is **not** a seam for a future header-injecting proxy — that option was
  weighed and **rejected** (open-items.md item 2, decided 2026-08-04). Injection
  needs terminating TLS, which needs a CA in the container, which makes one
  process hold every token, every prompt in plaintext and the CA private key: on
  a threat model where a leak is assumed rather than avoided, that trades a
  frequent small loss for a rare total one. The posture is instead to make a leak
  **cheap** — prod's credential-free container, short-lived brokered values, one
  credential per project, a short allowlist.

  `lifetime.go` is the only code that decision needed, and it exists because the
  practice was otherwise unenforced: someone writing `gh auth token` gets a
  months-long credential believing they brokered one. `Classify` reads what a
  value's own shape says and `sandbox.warnLongLivedSecrets` names it once per run,
  from the last point where a secret's name and value are both in hand.

  The two signals are **not** equally trustworthy and the code is arranged around
  that. A **JWT's `exp`** is a measurement — issuer-agnostic, correct for issuers
  that do not exist yet, and it never rots; it is also where the world is going,
  since STS/OIDC/workload-identity tokens are JWTs. A **prefix** is a lookup
  against a list of claims about other people's products, which can be neither
  completed nor kept current. So the list stays short, admits a prefix only if it
  is long, distinctive and documented as non-expiring, and — the rule that makes
  the rot harmless — the warning **reports the evidence, never an
  identification**: "begins with `ghp_` — GitHub personal access tokens…", so a
  prefix reused by another issuer later still yields a true sentence. `sk-` was
  dropped for failing this (three characters, and it named a vendor while
  matching anything); AWS `AKIA`/`ASIA` were never added, because they match the
  key *id* rather than the secret and would point the warning at the wrong value.

  It is said **once per secret name, per process** — a fleet resolves the same
  `secrets:` block per task and repeated itself twenty times for a twenty-task
  fleet, which is how a warning becomes wallpaper. The mutex guarding that state
  is not for the fleet, whose launches are sequential; it is for `studioapi`,
  which calls `Session.Start` from an HTTP handler.

  Three rules make it honest, all pinned by test: it **warns and never refuses**
  (`ANTHROPIC_API_KEY` has no ten-minute form, so refusing would refuse the
  ordinary case — the one place prod's asymmetry deliberately does not apply);
  **`Unknown` is not `ShortLived`** — an opaque value prints nothing, which means
  "nothing was recognized", never "this one is fine"; and a warning **carries no
  part of a value** beyond the public format marker, for the reason
  `audit.SessionMeta` has nowhere to put one. It covers `secrets:` only —
  `--env`/`EnvAllow` values are deliberately unexamined, since warning on every
  `ANTHROPIC_API_KEY` is how a warning becomes wallpaper.
- **`internal/studioapi`** — the local HTTP control plane (`cmd/sandbox-studio-api`) a frontend
  talks to instead of shelling out to the CLI. It owns **no container logic**: `POST /runs` builds
  the same `sandbox.Options` a `--worktree --detach` run does and hands them to `sandbox.Session`,
  which is what makes every isolation invariant above hold here unchanged — and which means it
  inherits `internal/fleet`'s rule with teeth, that **every gate on the run path must be repeated
  by every caller that builds `Options`** (`persist_auth` is re-checked in `buildRunOptions` for
  exactly that reason). Runs are always detached: an HTTP request/response cycle has nowhere to
  hold a pty.

  What is genuinely new here is a question the CLI never had to answer, because a terminal
  answered it: **who may ask this process to start a container?** CORS alone answers it wrong —
  refusing to *reflect* an origin only stops a page reading the response, while a cross-origin
  `POST` with a `text/plain` body is a "simple request" that skips preflight entirely and starts
  the container anyway. So `guard.go` refuses an unlisted `Origin` outright, and requires the
  `Host` header to name a loopback address (a page whose own hostname resolves to `127.0.0.1`
  satisfies the browser's same-origin policy, so its `Origin` looks legitimate — the name it
  dialled is what gives it away). Order matters and `withMiddleware` says why. The bearer token,
  constant-time compared, is the answer for non-browser callers, who send no `Origin` at all.

  `websocket.go` is a hand-rolled RFC 6455 subset rather than a dependency (handshake, unmasked
  server text frames, ping/close handling — no deflate, no subprotocols, no fragment reassembly).
  Two things it exists for: `EventSource` cannot carry an `Authorization` header, and a hijacked
  connection is no longer watched by `net/http`, so its read loop is the only thing that notices a
  closed tab and stops `docker logs --follow`. SSE remains the default for a plain `GET`, carrying
  the identical `LogEvent` payload. Contract and trust model: `docs/studio-api/`.

### Container labels, and the two `land` invariants

Every container is stamped with `sandbox.cli` plus, when there is something true to say,
`sandbox.repo`/`branch`/`agent`/`base`/`fleet`/`verify` (`internal/sandbox/labels.go`). Docker
is the state store, so **a fact not stamped is one no later command can recover** — and the
keys are constants because three packages now read them. Two are easy to get wrong:

- `sandbox.repo` is `worktree.RepoID`, an **id, not a path**: two clones of a same-named repo
  would otherwise share a label namespace. A `Runner` therefore carries both (`Repo` for git,
  `RepoID` for labels).
- `sandbox.fleet` separates a fleet container from an interactive `--detach` session in the
  same repo. Without it `fleet stop --all` reaches someone's live session, `fleet clean` reaps
  it, and `max_parallel` counts it — one open interactive session would block a
  `max_parallel: 1` fleet forever on a slot that never frees.

`land` merges into the checked-out branch, and two refusals are load-bearing rather than
cautious. **The recorded base and `HEAD` are not competing answers**: the label is the intent,
`HEAD` is the only branch git can merge into, so a disagreement is a refusal (`--onto` to
override) and never a preference. And **the worktree must still be on the branch being
landed**: `worktree.Path` falls back to a name-derived directory, so an agent that ran
`git checkout -b` inside its worktree would otherwise have `add -A` commit the work onto the
branch it moved to, and the merge would take the untouched original.

### Sessions (`list` / `logs` / `attach` / `kill`)

`internal/cli/session.go` is the supervision layer, and it exists because a container
outlives the process that started it: the daemon owns it, so a `kill -9` on sandbox-cli
leaves the agent running and still writing to `/workspace`, and `--detach` means to. It is
built on the labels above via `runtime.Inspector`/`Controller`/`Attacher` — `clean` and
`stats` were moved onto the same label filter so the listing and the reaper cannot disagree
about what exists.

The rule that makes it safe is one line in `resolveSession`: **a reference is matched
against a listing filtered by `sandbox.cli` and is never handed to the engine to resolve.**
`sandbox-cli kill postgres` must find nothing rather than somebody's database, and passing
the string through to `docker kill` would fail toward more reach. Ambiguity refuses and
lists the candidates — the one exception is liveness, since a branch with an old container
and a live one can only mean the live one.

Two asymmetries are deliberate and worth keeping. `logs` and `attach` infer their target
when exactly one sandbox is running; `kill` does not, because reading the wrong session
costs a second and stopping the wrong agent costs its work. And `attach` renders
`--sig-proxy=false`, so the Ctrl-C that stops you *looking* at an agent cannot stop the
agent — `Controller.Kill` is reachable only through `kill --force`.

The `KIND` column (`sessionKind`, from `sandbox.fleet`) exists because that distinction was
already load-bearing everywhere else — `fleet stop --all` does not reach an interactive
session, `fleet clean` does not reap one, `max_parallel` does not count one — and the listing
was the one place it was invisible, which is exactly where somebody decides what to kill.
`fleet status` carries the same session id in its own `ID` column for the mirror reason: a
fleet agent is a session, and the two tables must not name it differently.

Label values are printed through `termsafe.Clean`: they are text from the repository, and a
tab-separated table should not be forgeable by a branch name. That is the same regression
the old text-parsing `ps` was built around, carried forward.

### Security profiles

`--profile dev` (default) and `--profile prod` (`internal/config/profile.go`). The split is
deliberately **not** lax/strict: local development is where a prompt-injected agent has the most
valuable thing in reach, so no profile relaxes the host boundary. Both are secure; they differ in
what they optimise within a secure baseline, and in one thing of kind rather than degree — dev
**warns** when a control cannot be satisfied, prod **refuses**, because nobody is watching a
production run and one that degraded quietly is the failure the profile exists to prevent.

Three rules hold it together. A profile is the **base** config layer, under the user's own config
rather than over it, so a trusted config can still tune a setup — a profile that cannot be adjusted
gets abandoned. What stops that hollowing prod out is `ValidateProfile`, which asserts the settings
that *define* prod against the configuration that will actually run. And selection is the
security-critical part: a project `.sandbox.yaml` may **raise** the profile and never lower it,
because a repository that could select the weaker one would drop the run out of prod and leave
every other refusal in `trust.go` decorative.

prod turning persisted auth off is the substantive answer to the credential problem, and worth
knowing why: the default auth path is not an API key but an **OAuth refresh token** in the
persisted HOME, readable by the agent. prod does not mount it, so there is nothing to steal — no
TLS-terminating proxy required. Design: `docs/proposals/security-profiles.md` (gitignored).

Dev's egress default is `allowlist` with the baseline on, changed from unrestricted for the same
credential reason. The recorded objection — that allowlist mode puts every run through the root
entrypoint with `NET_ADMIN` — predates the root-phase hardening and costs ~166ms. `--user root` and
`--no-hardening` contradict the allowlist, so the *default* yields to them with a warning while an
explicitly requested allowlist still refuses.

`sandbox-cli doctor` (`internal/cli/doctor.go`) is the profile's preflight: it asks whether the
*host* can deliver what the profile promises — seccomp actually applied, a container able to
program iptables (tried, not queried, since rootless and userns-remapped daemons cannot), which
runtimes are registered — and applies the same asymmetry, warning under dev and failing with a
non-zero exit under prod. A question that could not be *asked* counts as a failure under prod
too: it does not get to assume the answer it would prefer.

The **runtime check now refuses under prod, but only what it can prove**, and the shape of that is
worth keeping. prod demands a kernel of its own; a run that names any non-default runtime satisfies
it, and an engine that *reports* a stronger runtime while nothing selected it is refused — the
boundary was there and unused. Everything else warns: an engine that reported none has not shown
there are none (podman answers with its **active** runtime, not its registered set), and no signal
distinguishes a Linux host that could install Kata from a VM image its user does not compose. The
first version tried to infer that from the daemon's product name and podman's `serviceIsRemote`, and
was wrong in both directions — refusing colima and OrbStack users who could not comply, waiving the
demand for a podman client talking to bare metal. The verdict is one shared function,
`runtime.ClassifyRuntimeGap`, called by `doctor` and by `sandbox.enforceKernelBoundary`, so the
preflight and the launch cannot disagree; `doctor --runtime` exists so the preflight can be asked
about the run you are about to make, since `--runtime` on the run outranks the config.

The demand is enforced on `spec.Runtime` in `internal/sandbox`, **not** in `ValidateProfile`: a check
against the resolved `Config` is not a check on the run, because `--runtime` arrives through
`sandbox.Options` and wins. That is the `persist_auth` class of leak, one layer up.

### The trust boundary (read before touching config, mounts, or the entrypoint)

An audit found the container→host boundary did not hold — 22 issues, all reproduced end to end,
from host code execution to mounting `/` read-write. A same-day re-audit of those fixes, and a
later external review of the pull request, each found more. All are fixed;
`docs/security/audit-2026-07-26.md` is the ledger and carries the per-round counts.
`docs/security/audit-2026-07-26.md` is the tracked record (findings, threat model, what was done)
and `docs/security/open-items.md` is the live backlog of what is still open (the allowlist matches resolved **IPs** rather than names, and the agent
still holds raw credentials — both need the egress proxy). `docs/proposals/security-hardening.md`
has the phased design notes, and is gitignored. The rules that follow from it:

- **A project `.sandbox.yaml` is untrusted input** and the privilege-relevant keys are
  *refused* from it (`internal/config/trust.go`): `image`, `workdir`, `user`, `home`,
  `runtime`, `mounts`, `secrets`, `env`, `env_allow`, `security.*`, `cache.paths`, and
  any `network.mode`/`network.baseline` that **weakens** what is already in force. A
  project may tighten (`default` → `allowlist` → `none`), never loosen. The escape
  hatches are the user's own config and an explicit `--config <path>`, where typing the
  path is the deliberate act discovery never involves. Discovery is also bounded — it
  stops at the repository root, else the home directory, else the starting directory —
  so a `.sandbox.yaml` in a shared parent like `/tmp` is no longer picked up.
  When adding a config key, decide which side it is on: if a hostile repo setting it
  could widen what the container reaches or reach the host, it belongs on the refused
  list, and `TestProjectConfigRefusesPrivilegedKeys` is where that gets pinned.
- **Anything running as root before the privilege drop must resolve names from the
  image only.** The entrypoint scripts pin an absolute interpreter and reset `PATH`
  as their first statement, and the agent's writable HOME is deliberately *not* on
  the image `PATH` (`assets/Dockerfile`). This is why an agent can no longer plant a
  `bash` in its own HOME and have root run it.
- **Every host path that gets bind-mounted goes through `sandbox.RefuseUnsafeHostPath`**
  — never `/`, never the host home, never an ancestor of it. It compares by
  **identity (device+inode), not by string**: `EvalSymlinks` preserves the caller's
  casing and APFS/NTFS are case-insensitive, so `--project /Users/AmitGhadge` used to
  mount the home directory and `--project /USERS` bypassed the ancestor check entirely.
  The second caller is the worktree `.git` mount, whose location comes from a `.git`
  **pointer file inside the workspace** that the agent can rewrite — `GitCommonDir`
  additionally requires the target to look like a real git directory (`HEAD` +
  `objects/`), because its fallback takes *two directories up* from that string and
  used to hand back `/` for `gitdir: $HOME`.
- **Some environment variables are instructions, not settings** (`config.IsReservedEnv`).
  They cannot be set or forwarded from outside. Three groups, and a new variable should be
  matched against the reason for whichever it resembles:
  - *sandbox-cli's own control variables* — `SANDBOX_RUN_AS`, `SANDBOX_EGRESS_ALLOW`,
    `SANDBOX_INGRESS_PORTS`, `SANDBOX_PROXY_PORT`. The list is exact names, not a
    `SANDBOX_*` prefix, because `SANDBOX_STATUSLINE_*` is a documented user knob read
    *after* the privilege drop — check which side of the drop a new variable lands on
    before adding it.
  - *interpreter and loader controls* — `BASH_ENV`, `ENV`, `LD_PRELOAD`, `LD_AUDIT`,
    `LD_LIBRARY_PATH`, `SHELLOPTS`, `BASHOPTS`, `PS4`, `IFS`, `GLOBIGNORE`. Not ours, but
    they decide what the container's root phase *executes* before its first line runs.
  - *docker client controls* — `DOCKER_HOST`, `DOCKER_CONFIG`, `DOCKER_CERT_PATH`,
    `DOCKER_TLS_VERIFY`, `DOCKER_CONTEXT`. These never reach the container; they steer the
    docker binary sandbox-cli runs, which is the one child that still receives forwarded
    values (`runtime.childEnv`).

- **Host-side `git` runs inside a repository the agent controls**, so every git
  invocation sandbox-cli makes on its own behalf goes through `internal/githard`
  (`internal/rescue`, `internal/worktree`). git is a programmable tool: `add -A`
  runs `filter.*.clean` and `core.fsmonitor`, `update-ref` runs the
  `reference-transaction` hook, `worktree add` runs smudge filters and
  `post-checkout`, `show`/`diff` run `diff.*.textconv` — every one of them naming
  a command from a file the agent can write. `githard.Args()` overrides the
  config keys (`-c` outranks the repo's local config, which
  `GIT_CONFIG_GLOBAL/SYSTEM` do **not** cover); `githard.Env()` points
  `GIT_ATTR_SOURCE` at an empty tree, because filter driver names come from
  `.gitattributes` and cannot be enumerated ahead of time. The deliberate
  exception is `worktree.Git` — `sandbox-cli worktree git …` is the user's own
  command in their own repo. Adding a git call that bypasses `rescue.run`/
  `runGit` reopens this; `internal/rescue/hostile_repo_test.go` is the guard.

### Two invariants to preserve when changing behavior

1. **Isolation lives in `runtime.BuildArgs` and `sandbox.ResolveWorkspace`.** Any change that could
   affect what the container can reach must keep `internal/runtime/args_test.go` and the `--dry-run`
   golden test (`internal/cli/dryrun_test.go`) honest — update the golden output intentionally, never
   just to make the test pass.

2. **The two subcommand flag-parsing modes are different on purpose** (`internal/cli`):
   - `run` — sandbox flags first, guest command after `--` (`sandbox-cli run --dry-run -- npm test`).
   - agent wrappers (one per agent, listed in `agentCmds()`) — `DisableFlagParsing: true`; `splitWrapperArgs` consumes a *leading*
     run of recognized sandbox long-flags, then forwards **everything else verbatim** to the agent, so
     `sandbox-cli claude --dangerously-skip-permissions` just works and agent short flags never collide.
     A sandbox option after the boundary needs a `--` separator.
     The **one exception** to "everything else is forwarded" is `wrapperSubcommands`
     (`context.go`): a leading token naming one of sandbox-cli's own subcommands is answered
     by sandbox-cli, so `sandbox-cli claude context list` works. It fires only without an
     explicit `--`, which is the escape hatch (`sandbox-cli claude -- context …` still goes
     to the agent). Adding a word to that list makes it un-forwardable — do it rarely.

### Detached runs

`--detach` (`run` and every wrapper) is the one case that breaks the container's usual shape, and
each break is load-bearing: `-d` **replaces** `-i`/`-it` (nobody is attached, and an agent handed a
pty draws its TUI for an audience of none), and `Remove` is false — the exit code and `docker logs`
are the entire supervision story, so `--rm` would discard both at the moment they become
interesting. Those two are resolved in `sandbox.BuildSpec`; `BuildArgs` only renders what it is
handed. Docker is the state store: a detached container is named `sandbox-<repo>-<branch>` (so
docker's own duplicate-name refusal enforces one agent per branch) and labelled with its repo,
branch, agent and base branch — a fact not stamped as a label is one no later command can recover.

`sandbox.Options.Console` is the **one** exception to the pty rule, and it is a different claim
rather than a weaker one: not "give this run the terminal that launched it" — there isn't one — but
"create a console on the container so a later `attach` has somewhere to type", which renders `-dit`.
It exists because Studio launches every run detached, so an agent could be watched and never
answered. The half that is easy to miss is that a console is useless alone: an agent started in its
**headless** argv (`claude -p`) produces one final answer and never asks anything, so `Console` also
selects the descriptor's interactive `Command`, and the two are decided together where the argv is
built (`studioapi/buildRunOptions`) rather than separately. It is refused with `verify` — verify's
exit code is the answer it exists to give, and an interactive session's exit code is whenever
somebody quit — and `fleet` may never set it (`gates_test.go` classifies it `never`): a fleet is
unattended, which is the same reason `internal/agents` only admits agents with a verified headless
mode. An agent that stops to ask does not fail, it hangs, holding a `max_parallel` slot.

Reading and answering a console run over HTTP is `internal/studioapi/console.go`.
Two halves, two mechanisms, and the split is the point: **reading** comes from the
agent's transcript (`agentctx.Transcript`) because a TUI's stdout is repaints and
scraping it yields plausible nonsense, while **answering** goes to the container's
stdin over the engine's API socket (`internal/runtime/console.go`) because
`docker attach` refuses a client with no tty and a web server has none. That file
is the only place sandbox-cli talks to the socket instead of the binary, and it
keeps one rule: read output, write stdin, nothing else. Two correlation filters
decide which transcript belongs to a run, and both were learned the hard way —
only the **sandbox-owned** store is searched (the claude wrapper has two, and the
other is the user's own `~/.claude`), and the window matches when a session
*started*, not when it was last modified. Without either, the developer's own live
Claude Code session — by definition the most recently modified transcript on the
machine — showed up as a three-minute-old sandbox run's conversation. `pickSession`
and `sandboxStore` exist as separate functions so that stays pinned by test.
Studio can also **attach a real terminal** in the browser (`attached-terminal.tsx`,
xterm.js, loaded on demand) over the same transport. Two things there were learned
by measurement and are easy to get wrong again. A full-screen agent renders
**nothing** until it is told its terminal size — a console container that had
written zero bytes in ten minutes painted its whole UI within a second of the
first `console/resize`, which is why attaching from a real terminal always worked
(`docker attach` sends one) and the first browser version showed a blank
rectangle; and since SIGWINCH only fires on a *change*, re-attaching at the same
size needs a nudge (one column narrower and back). Keystrokes must also be
**serialized** — one request per keypress races, and `What is 12 times 12?`
arrived as `rtWha is21 t ime1 2s?`.

Typing at a running agent is also the one endpoint that **requires `-token` even
when the rest of the server does not**: launching is a capability the API already
had, a keyboard on a live session is not.

### Agent wrappers

Each wrapper is one file in `internal/cli` (`claude.go`, `gemini.go`, `aider.go`, …), listed in
`agentCmds()`, carrying a suggested opt-in env allowlist (e.g. `ANTHROPIC_API_KEY`, applied only if set) and ending
in `finishAgentCmd(cmd, rf, "<agent>")` (`agents.go`), which adds the shared sandbox flags and
**persists the agent login by default** by bind-mounting a sandbox-owned host dir
(`~/.config/sandbox/agents/<name>`) as the agent's whole HOME. This is separate from the host's real
`~/.claude`. `--no-persist-auth` opts out. Agents that may be missing from the baked base image use
`npmAgentBootstrap(bin, pkg)`, which installs into the persisted HOME on first run.

`TestAgentWrappersShareTheContract` pins that shape for every wrapper. Adding an agent means: a file
in `internal/cli`, a line in `NewRootCmd`, and an entry in the test table — **no Dockerfile change**.
New agents are installed lazily by `agentBootstrap` on first use, not baked into the base image:
baking every adapter would put hundreds of megabytes in front of every user for agents most of them
will never run, while a lazily-installed adapter costs the image nothing. The four the image already
carries (claude, codex, gemini, opencode) stay baked so today's users see no change. The queue of
agents still to adapt, ordered by popularity, plus the full checklist, is in
`docs/proposals/agent-adapters.md`. User-facing setup for each agent — prerequisites, login
flow, forwarded variables, `--allow` domains — is `docs/AGENTS.md`; a new adapter needs a
section there.

### The memory/CPU/usage status line

Only `claude` gets one on screen, and that is a deliberate limit, not an oversight:

- **`claude`** — Claude Code's `statusLine` hook runs `sandbox-statusline` (baked in the image)
  and renders it in its own UI. Injected via a managed-settings.json mounted read-only, which
  never touches the user's own Claude settings. `--no-statusline` opts out.
- **every other agent** — nothing on screen. Neither Gemini CLI nor OpenCode has a
  status-line hook (verified upstream; see `docs/proposals/agent-adapters.md`). Running them
  inside tmux to get one was tried and reverted: it made the agents' TUIs render badly, which is
  a bad trade for a gauge. `sandbox-cli stats` in a second terminal is the answer for these.

Don't reach for a terminal multiplexer here again without checking that the agent's UI survives it.

The line also carries the subscription windows and the model
(`· opus 5 · mem … · 5h 23% (2h14m) · wk 49%`), both from the JSON Claude already pipes to
the hook — `rate_limits` and `model.display_name`, documented fields, so the script needs no
mount and no file whose shape it is not entitled to know. Two rules hold it together:
**absent means absent** (no `rate_limits`, API-key auth, before the first response, one
window and not the other — all print nothing, never a placeholder), and **what the agent
reports about itself is dropped before the branch** when the row is too narrow — usage
first, then the model, so a terminal that fit the line before this feature still shows
exactly what it did. `SANDBOX_STATUSLINE_NO_USAGE=1` / `_NO_MODEL=1` opt out individually.
Design and rejected alternatives: `docs/proposals/usage-stats.md`.

**A mount into the agent's HOME needs its target created first.** A bind mount
whose target does not exist is created by the container *runtime*, as root — and
under rootless podman that root is a subordinate uid on the host (`keep-id` maps
container 0 into your subuid range), so the directory comes back owned by e.g.
`524288` at mode 0755. Inside the container that reads as root-owned and not
group-writable, so the agent (uid 1001) cannot write beside it. That is how the
claude history mount left `~/.claude` unwritable and cost a login on **every**
run under podman, after beta.10 had fixed the same symptom for Docker.
`sandbox.EnsureGuestDir` creates the chain on the host first, and each level, since
the container must *traverse* every component. `ShareWithSandboxGroup` cannot fix
it afterwards — its `chown`/`chmod` run as the invoking user, who does not own a
subuid-owned path, so they fail with `EPERM` and do it silently. A level that is
already foreign-owned is therefore reported by name, with the `podman unshare rm
-rf` that clears it, because that state can be detected and not repaired.

`claude` additionally read-write mounts the host's Claude history for the current project
(`~/.claude/projects/<bucket>`) into the persisted HOME by default, so host sessions resolve
inside the sandbox and vice versa. `--no-sync` opts out. This is the one default that reaches a
host path outside the workspace — keep it scoped to the single project bucket.

The bucket is **created when it does not exist yet**, and that is load-bearing rather than
tidiness. Claude Code names the directory after its working directory, which inside the sandbox is
always `/workspace`; the mount is what redirects those writes into the host's per-project bucket.
Skipping the mount when the host directory was absent made it a chicken-and-egg — the host bucket
only appears once Claude Code has run on the *host* in that project, so a project used only in the
sandbox never got one, and every session pooled into the persisted HOME's shared `-workspace`
bucket, findable by no project and one id away from resuming another repository's conversation.
`agentctx.PooledSessions` reports whatever is already in that shared bucket, without guessing which
project it belonged to, since the transcripts record only `/workspace`.

Relatedly, `agentctx.List` walks **every** verified location, not just the one that won the
most-recent-activity tie-break: the claude wrapper genuinely has two, and a session is no less real
for living in the loser.

## Conventions

- Non-root by default (`user: sandbox`): agents refuse `--dangerously-skip-permissions` as root, and
  on macOS Docker Desktop bind-mount ownership is virtualized so files are still written as the host user.
- Module path is `github.com/Amitgb14/sandbox-cli`. Standard library + `cobra` + `yaml.v3` only.
- Do not add a `Co-Authored-By` trailer to commit messages.
- After every release (tagging a new version), update `CHANGELOG.md`: move the
  `Unreleased` entries under a new version heading dated with the release date.
