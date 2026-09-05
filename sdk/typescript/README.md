# @sandbox-cli/sdk

A typed client for the [sandbox-cli](https://github.com/Amitgb14/sandbox-cli)
control plane: run agents and commands in isolated containers, from a program
instead of a terminal.

It talks to a daemon and does not start one, so the daemon comes first — on the
machine that will run the containers, from the repository you want to work in:

```sh
cd ~/code/my-app
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh
```

That installs sandbox-cli and the daemon if they are missing, starts both halves,
and registers *that* repository. It also writes the port and a token into
`~/.config/sandbox/studio`, which is what lets `connect()` take no arguments.
Docker is the one thing it will not install for you. Then, in your own project:

```sh
npm install @sandbox-cli/sdk
```

Your file needs to be an ES module, because the examples use top-level `await`:
either `"type": "module"` in your package.json, or name the file `.mts` and run
it with `npx tsx agent.mts`. Without one of those, tsx compiles it as CommonJS
and stops at *"Top-level await is currently not supported"*, which is a fact
about your project rather than about this package.

```ts
import { Studio } from "@sandbox-cli/sdk";

const studio = await Studio.connect();            // finds the local daemon
const repo = await studio.project("my-app");
const ws = await repo.workspace("agent-42");      // a branch's worktree

await ws.run(["npm", "ci"]);
const tests = await ws.run(["npm", "test"], { env: { CI: "true" } });

console.log(tests.exitCode, tests.stdout);
```

## Where you run this, and what it works on

Anywhere. This is an HTTP client, so the script's own directory and the
repository the agent works in are two different things — often on two different
machines. With no argument, it assumes they are the same one:

```ts
const repo = await studio.project();          // the repository this script is in
const named = await studio.project("my-app"); // or by name, or by id
const there = await studio.project("../api"); // or by path, relative to here
```

The no-argument form asks git — `rev-parse --git-common-dir`, the same question
the daemon asks of the path it is given — so running from `scripts/` finds the
repository rather than the subdirectory, and running from a **linked worktree**
finds the main repository rather than the worktree. That second one is the reason
git answers rather than a search for `.git`: Studio addresses work by branch
within a repository, so a worktree that resolved to itself would look
unregistered while sitting inside a repository that is. It is a **lookup**: the root is matched against the repositories the daemon has been told
about, and a directory nobody registered is refused, saying which roots the
daemon does know. That is also what makes the answer honest against a remote
daemon — the path is resolved here, so the daemon on another machine correctly
reports it has nothing at it, instead of this package pretending distance does
not exist.

To register a directory **on the daemon's machine**:

```ts
await studio.addProject();                      // the repository this script is in
await studio.addProject("/home/you/code/api");  // or any path there
```

If the directory is not a repository yet, `addProject` says so rather than
guessing. `{ init: true }` runs `git init` there first:

```ts
await studio.addProject(undefined, { init: true });   // this directory
```

It is opt-in and stays that way, for two reasons. Creating a repository is a
larger side effect than registering one, and a mistyped path would otherwise
leave an empty repository somewhere nobody meant. And the path belongs to the
**daemon's** machine while `git init` necessarily runs on this one — against a
remote daemon, doing it automatically would create a repository here, silently,
and still fail to register there.

`git init` alone is also not enough to be useful, and this is the part worth
knowing: Studio works from **committed** state. A repository with files and no
commits makes *empty* worktrees — git creates an orphan worktree, the daemon
registers it happily, the run starts, and the agent finds nothing in
`/workspace`. So `addProject` refuses that state and tells you what to run. It
does not commit for you: a directory that has never been a repository usually has
no `.gitignore`, which is exactly where `node_modules`, a `.env` and a stray key
get committed by a helpful tool.

That is the only call here that hands over a path, mirroring the one endpoint
that accepts one: the checks a directory has to pass — absolute, on disk, a git
repository, not your home or an ancestor of it — are applied there, once, by the
daemon. Adding a repository that is already registered returns the existing row,
so it is safe on every start. Registering is never implicit: a lookup that
quietly added what it failed to find would turn a typo into a permanent entry in
the list of directories that daemon will touch.

## What this is, and what it is not

It is a **client**. Every gate that makes a sandbox a sandbox — the workspace
refusals, the fake HOME, default-deny environment, the egress allowlist — is
applied where the container is built, on the machine running the daemon. This
package holds no docker socket, shells out to nothing, and assembles no argv. If
it wants a capability the daemon does not expose, the daemon grows an endpoint
and the gate is written once, in Go, with a test.

There is **no mock mode**. A fake `run()` returning `exitCode: 0` is the worst
possible default for a library whose whole job is telling you what happened; a
test double belongs in your test suite, where you can see it.

## The model

Three nouns, and they are the daemon's rather than this package's.

| | |
|---|---|
| **Studio** | a daemon — one machine's control plane |
| **Project** | a repository that daemon has been told about |
| **Workspace** | a branch's worktree inside one, and the isolation unit |

A run is a **container**, not a session you exec into repeatedly, and the
**worktree is what persists**: `npm ci` then `npm test` works because
`node_modules` was written to disk, not because a process stayed alive. Two
agents in one tree is a data race with a filesystem in the middle, which is why
a workspace is the only way to get somewhere to run.

## Connecting

`Studio.connect()` with no arguments finds the daemon `studio.sh` started: the
API port from `~/.config/sandbox/studio/ports`, and the token it generated from
`~/.config/sandbox/studio/token`. Explicit arguments win, then
`SANDBOX_API_URL` / `SANDBOX_STUDIO_TOKEN`, then those files.

```ts
const studio = await Studio.connect({ url: "https://api.example.com", token });
```

Connecting makes one round trip to `/v1/health`, so a wrong URL or a missing
token is reported there rather than as a failure of whatever you ran first.

### A daemon on another machine

The containers run where the daemon runs, so a Linux box is often the point.
Start it there, bound to an address you can reach:

```sh
cd ~/code/your-repo
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh -o studio.sh
sh studio.sh up --api-only --bind 10.0.0.5
```

It prints a **Daemon URL** and a **Token** — the token belongs to that machine,
not to you. Open the port (`firewall-cmd --add-port=8787/tcp`, or `ufw allow`),
check it with `curl http://10.0.0.5:8787/v1/health`, which needs no token, then:

```ts
const studio = await Studio.connect({
  url: "http://10.0.0.5:8787",
  token: process.env.SANDBOX_STUDIO_TOKEN,
});
```

No tunnel, and none of the CORS or Host flags the browser needs: those checks
fire on an `Origin` header, which browsers send and scripts do not, so a script
is governed by the token alone. There is no TLS yet — on a bound address the
token crosses in cleartext, so this is for a network you already trust, or put a
reverse proxy in front and dial its name (the daemon needs `-allow-host` for it).

## Running things

```ts
const out = await ws.run(["npm", "ci"], { timeoutMs: 10 * 60_000 });
const done = await ws.agent("claude", "make the failing test pass", {
  fallback: ["codex"],
});
```

Every outcome carries `exitCode`, `stdout`, `stderr` — and `agent`,
`routedFrom`, `routeReason`, `handoffFrom`, because **a script that cannot see a
failover attributes one agent's work to another**, under the wrong login and the
wrong bill.

The wait is bounded (30 minutes by default). When the deadline passes the run is
**stopped**, and the outcome says `stopped: true` rather than reporting a verdict
on a container that was interrupted. A deadline that only stopped waiting would
leave a container holding a CPU with nobody watching it — so if the *stop* is
refused, that surfaces rather than being swallowed: claiming a run was stopped
while it is still running would announce the outcome the deadline exists to
prevent as though it had been prevented.

If anything goes wrong after the launch — a daemon restart mid-poll, a cancel —
you get a `WaitError` carrying the run. The container exists whatever happened,
and a detached run holds `sandbox-<repo>-<branch>`, which docker will not
duplicate, so an error without the id would leave the branch blocked by something
you cannot name:

```ts
try {
  await ws.run(["npm", "test"], { timeoutMs: 60_000, signal });
} catch (err) {
  if (err instanceof WaitError) await ws.stop(err.run.id);
}
```

Errors are typed and distinct on purpose: `ApiError` (the daemon refused, with
its own message verbatim), `ConnectionError` (nothing answered), `TimeoutError`
(it answered too slowly — reachable, not down), and a cancel that arrives as
`err.name === "AbortError"`, the check callers already write.

`stop()` and `remove()` are separate calls and neither happens for you: a
finished run's logs are the evidence for what it did, so tidying up on the way
out would discard that on every happy path.

## Following a run

```ts
for await (const event of ws.follow(run.id)) {
  if (event.type === "log") process.stdout.write(event.data + "\n");
}
```

Server-sent events, because the daemon offers SSE and WebSocket carrying the
identical payload and SSE needs nothing this runtime does not already have. The
stream ends on the daemon's `end` event; leaving early closes the connection,
which is what stops the `docker logs --follow` behind it.

## Handing work to another agent

```ts
await ws.handoff("codex", { agent: "claude", sessionId }, "finish the migration");
```

A **briefing, not a resume**: a session id is a primary key into one vendor's
private store, so what crosses is `HANDOFF.md`, a vendor-neutral transcript and a
file ledger derived from git — mounted read-only, with a prompt that tells the
target it is reading a briefing rather than its own history.

## Snapshots

Checkpoint the working tree before something risky, and put it back if it goes
wrong:

```ts
const before = await ws.snapshot({ label: "before the migration" });

const out = await ws.agent("claude", "migrate the schema");
if (out.exitCode !== 0) {
  await ws.restore(before.id);            // a new branch at the snapshot
  await ws.restore(before.id, { mode: "worktree" }); // or the files, in place
}
```

A snapshot is a commit of the working tree under `refs/sandbox/snapshots/`,
written through a private index — your own index, `HEAD`, branches and working
tree are never touched. It holds **files**: no container, no image, and no
credential, which is why restoring one is cheap and why it is not a way to
resume a stopped machine.

Three restore modes. `branch` is the default and the only one that cannot
destroy anything — it points a new branch at the snapshot and leaves the tree
alone. `worktree` writes the files back, and is refused on a dirty tree rather
than offering a `--force`. `patch` returns a diff and touches nothing.

An unchanged tree throws `NothingToSnapshotError` rather than returning an id
that points at no commit, so the common shape is a `catch` and not an `if`:

```ts
import { NothingToSnapshotError } from "@sandbox-cli/sdk";

try {
  await ws.snapshot();
} catch (err) {
  if (!(err instanceof NothingToSnapshotError)) throw err; // nothing new to keep
}
```

Snapshots expire — seven days for one you asked for, configurable per snapshot
with `setSnapshotRetention(id, "72h")` or `""` to return it to the default.

Two things worth knowing. `snapshotRun(id)` checkpoints what a *running* agent
is working in, and takes neither a repository nor a branch: the run already
answers both, and a second answer is refused rather than allowed to decide where
files are written. And a snapshot taken through this SDK is restored through
this SDK — Studio lists it but will not put it back, because a script mid-way
through something is not a thing to undo from a browser tab. Snapshots from a
sandbox run restore in either place.

### Off-machine copies

If the daemon has a bucket configured (`snapshot.s3`, or Studio → Settings →
Snapshot storage), `ws.snapshot()` also uploads a git bundle of the tree — so a
checkpoint survives the machine that took it. Nothing about the call changes; the
result carries a `remote` block saying where it went.

```ts
const before = await ws.snapshot({ label: "before the migration" });
if (!before.remote?.uploaded) {
  // Real and local-only: the snapshot was taken, the copy did not happen.
  console.warn("no off-machine copy:", before.remote?.error ?? "no bucket configured");
}
```

A capture whose upload fails is **not** an error — the checkpoint exists, so you
get the id and the reason rather than neither. `ws.uploadSnapshot(id)` retries
one later, and `ws.verifySnapshot(id)` asks the bucket whether the object is
actually still there, which `remote` cannot answer: it records what the upload
did, and a lifecycle rule leaves a snapshot reading as mirrored when it is not.

`studio.snapshotSettings()` reports the configuration, and
`studio.checkSnapshotStorage()` is the connectivity test. Neither ever carries a
credential: what you get is the *name* of the variable the daemon reads and
whether it currently resolves.

## Secrets

Pass settings in `env`; keep credentials out of it. Values there travel in the
request body to the daemon, and off loopback there is no TLS yet. The posture
this tool is built around is the other direction: `secrets:` in the *daemon's*
config, resolved on that host and forwarded by name, so a value never crosses
the wire and has nowhere to land in a log.

## Reading output, and moving files

`Outcome.stdout` is the run's log **lines**, joined with newlines. That makes it
the right thing for reading what a command said and the wrong thing for copying a
file: a trailing newline cannot survive it (measured — 64 bytes back for 65
written). When an artifact has to move between workspaces, send it base64-encoded
in both directions, as `examples/travel-planner.ts` does. That also removes the
quoting hazard, which matters more than the byte: a file written by an agent is
attacker-controlled, and a heredoc built by string interpolation is one `EOF`
line away from being the next command.

Each run is a **new container**, so nothing outside the worktree survives between
steps — `/tmp` is gone, `/workspace` is not.

## Three whole scripts

`examples/checkpoint.ts` is a snapshot around a risky step, rolled back when the
step fails.

`examples/agent-run.ts` is the end-to-end version of one task — install, hand the
work to an agent, run the tests, and check what the outcome claims.

`examples/travel-planner.ts` is three agents that need each other: two
specialists in parallel, then a coordinator that gets their artifacts handed to
it through the host, because worktrees are isolated and it cannot see them
otherwise.

`examples/workflow.ts` is the same idea widened: three tasks on three branches in
three containers, in parallel, then one gate deciding which are worth a human's
attention. It answers the question people ask before writing anything — whether
orchestrating agents means building an agent. It does not: the control flow is
`Promise.all` and an `if`, and the only model involved is the one working inside
each container.

All three import by package name and are compiled by `npm test`, so they are checked
in the shape you would type rather than in one only this repository can use.

## When an agent is unavailable

`fallback: ["codex"]` lets the daemon route the work when the first agent's
provider is down. The outcome you get back is the run that **finished the work**,
not the attempt that failed: the daemon stamps `routedFrom` on the replacement
and this client follows it, so `agent` and `routedFrom` describe who actually did
it. Reported on every outcome rather than behind an option — a script that cannot
see a failover credits the wrong agent, and bills the wrong account.

## Running the same script twice

Docker refuses a duplicate container name, and that refusal is what enforces one
agent per branch — so a run that has *finished* keeps its branch's name until
somebody reaps it, and a second launch is refused:

```
ApiError: a finished run (8998cd4c631f, exit 0) still holds "docs/readme-changelog"'s
container name; read it with GET /v1/runs/8998cd4c631f/logs, then DELETE … to run again
```

That default is deliberate: the logs are the evidence for what the run did, and
removing them for you would discard that on every second run. When the evidence
is spent, say so:

```ts
await ws.run(["npm", "test"], { replaceFinished: true });
```

or reap it yourself, which also tells you what was removed — **before** the run,
not after, since the launch is what fails:

```ts
await ws.clearFinished();                // null when nothing was holding the name
const out = await ws.run(["npm", "test"]);
```

Both refuse a run that is still going. Stopping somebody else's agent is not a
side effect a convenience flag may have.

## Types

`src/contract.ts` is **generated** from `internal/studioapi/types.go` by
`make contract`, and a test fails when it and the Go types disagree. Everything
this package exports is that contract plus the four classes above.
