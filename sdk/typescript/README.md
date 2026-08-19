# @sandbox-cli/sdk

A typed client for the [sandbox-cli](https://github.com/Amitgb14/sandbox-cli)
control plane: run agents and commands in isolated containers, from a program
instead of a terminal.

```ts
import { Studio } from "@sandbox-cli/sdk";

const studio = await Studio.connect();            // finds the local daemon
const repo = await studio.project("my-app");
const ws = await repo.workspace("agent-42");      // a branch's worktree

await ws.run(["pnpm", "install"]);
const tests = await ws.run(["pnpm", "test"], { env: { CI: "true" } });

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
**worktree is what persists**: `pnpm install` then `pnpm test` works because
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
leave a container holding a CPU with nobody watching it.

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

## Types

`src/contract.ts` is **generated** from `internal/studioapi/types.go` by
`make contract`, and a test fails when it and the Go types disagree. Everything
this package exports is that contract plus the four classes above.
