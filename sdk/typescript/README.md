# sandbox-cli-sdk

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
npm install sandbox-cli-sdk
```

Your file needs to be an ES module, because the examples use top-level `await`:
either `"type": "module"` in your package.json, or name the file `.mts` and run
it with `npx tsx agent.mts`. Without one of those, tsx compiles it as CommonJS
and stops at *"Top-level await is currently not supported"*, which is a fact
about your project rather than about this package.

```ts
import { Studio } from "sandbox-cli-sdk";

const studio = await Studio.connect();            // finds the local daemon
const repo = await studio.project("my-app");
const ws = await repo.workspace("agent-42");      // a branch's worktree

await ws.run(["npm", "ci"]);
const tests = await ws.run(["npm", "test"], { env: { CI: "true" } });

console.log(tests.exitCode, tests.stdout);
```

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

## Secrets

Pass settings in `env`; keep credentials out of it. Values there travel in the
request body to the daemon, and off loopback there is no TLS yet. The posture
this tool is built around is the other direction: `secrets:` in the *daemon's*
config, resolved on that host and forwarded by name, so a value never crosses
the wire and has nowhere to land in a log.

## A whole script

`examples/agent-run.ts` is the end-to-end version — install, hand the work to an
agent, run the tests, and check what the outcome claims. It imports by package
name and is compiled by `npm test`, so it is checked in the shape you would type
rather than in one only this repository can use.

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
