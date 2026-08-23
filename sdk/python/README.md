# sandbox-cli for Python

Drive [sandbox-cli](https://github.com/Amitgb14/sandbox-cli) from a program: run
commands and agents in isolated containers, and get back the exit code, the
output, and which agent actually did the work.

```python
from sandbox_cli import Studio

studio = Studio.connect()                 # finds the local daemon: port and token
repo = studio.project("my-app")           # or project() for the one you are in
ws = repo.workspace("agent-42")           # a branch's git worktree

print(ws.run(["pytest", "-q"]).exit_code)
```

Async, for the same client:

```python
import asyncio
from sandbox_cli.aio import AsyncStudio

async def main():
    studio = await AsyncStudio.connect()
    ws = await (await studio.project("my-app")).workspace("agent-42")
    a, b = await asyncio.gather(ws.run(["pytest", "-q"]), other_work())

asyncio.run(main())
```

## Before it works

A Studio daemon has to be running: `sh studio.sh up` in a sandbox-cli checkout.
This package finds its port and token in `~/.config/sandbox/studio` — the same
files the daemon writes — so there is nothing to paste. `SANDBOX_API_URL` and
`SANDBOX_STUDIO_TOKEN` override, and explicit arguments override those.

## What this is, and what it is not

It is a **client**. Every gate that makes a sandbox a sandbox — the workspace
refusals, the fake HOME, default-deny environment, the egress allowlist — is
applied where the container is built, on the machine running the daemon. This
package holds no docker socket, shells out to nothing, and assembles no argv.

**No dependencies**, deliberately: it is imported into somebody's agent process,
and an HTTP stack is a bad thing to drag in behind them. The async face runs the
same calls in a thread rather than duplicating them against a second stack —
one implementation, and a test that fails when the two surfaces drift.

## Things that will bite you otherwise

**A repository is named, never located.** `project()` with no argument asks git
which repository the current directory belongs to and matches it against what the
daemon knows; it does not register anything. `add_project(path)` is the sentence
that asks, and `add_project(init=True)` will `git init` a directory that is not a
repository yet.

**Studio works from committed state.** A repository with files and no commits
makes *empty* worktrees, so `add_project` refuses it and tells you what to run
rather than handing an agent a `/workspace` with none of your files in it.

**`stdout` is the run's log lines, joined.** Right for reading output, wrong for
copying a file — a trailing newline cannot survive it. Move artifacts
base64-encoded in both directions.

**Each run is a new container.** Nothing outside the worktree survives between
steps: `/tmp` is gone, `/workspace` is not.

**Error names avoid the builtins.** `TimeoutError` and `ConnectionError` are
Python's own, so this package raises `RunTimeout` and `DaemonUnreachable`
instead; `ApiError` and `WaitError` mean what they do in the TypeScript client.

## Examples

`examples/stock_price.py` — untrusted code fetching a quote, and the two lines
that decide what it can do:

```
$ python3 examples/stock_price.py TSLA
TSLA  362.86 USD  (NasdaqGS)
```

It is worth reading for `allow=` rather than for the price. Naming a host turns
the egress allowlist **on** for that run: measured against a daemon with
unrestricted egress, `example.com` answers 200 without `allow` and is refused
with it. Asking for one host means giving up the rest of the internet, which is
usually what you want for code you did not write.

`examples/travel_planner.py` — three agents that hand work to each other, and a
gate that decides. Two specialists research in parallel, each in its own
worktree, so the coordinator **cannot see** what they wrote: the artifacts cross
through this process, base64-encoded in both directions. The gate asks the
filesystem rather than the agent, because an agent that reports success having
written nothing is the failure it cannot be asked about:

```
OK   agent-flights   claude  ok
SKIP agent-hotels    claude  finished without writing hotels.json
handed over: flights.json
```

## Status

Early. The surface above is stable enough to build on; `run_code`, artifacts and
the code-interpreter face described in `docs/proposals/python-sdk.md` are next.
