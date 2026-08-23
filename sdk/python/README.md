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
Python's own, so this package raises `RequestTimeout` and `DaemonUnreachable`
instead; `ApiError` and `WaitError` mean what they do in the TypeScript client.

**A run outliving its deadline is not an error.** `RequestTimeout` means one HTTP
request was slow. A `timeout=` that expires stops the container and returns an
`Outcome` with `stopped=True` — check that before you read `exit_code`, because
the exit code of a container somebody stopped is not a verdict on the work.

## Adding a repository

Three ways, and the difference is who owns the directory:

```python
studio.project("my-app")                        # already registered
studio.add_project("/home/you/code/my-app")     # a directory on the daemon's machine
studio.add_project(init=True)                   # this one, `git init` first
studio.clone("Amitgb14/sandbox-cli", "/home/you/code")   # clone it there, then register
```

`clone` takes a full git URL or the GitHub shorthand `owner/repo`. Everything else
is passed through untouched for the daemon to accept or refuse — including
`ext::`, which it refuses, because deciding that here would put the refusal in
two places and let them disagree. Private repositories use whatever credentials
the **daemon's** git has; putting a token in the URL would write it into that
machine's remote config.

## Steps and environment

```python
ws = repo.workspace("ci", env={"CI": "true"})     # applies to every run here
ws.steps([
    ["npm", "ci"],
    ["npm", "test"],
    ["npm", "run", "build"],
], env={"NODE_ENV": "test"})                      # merged over the workspace's, per key
```

`steps` stops at the first failure and returns what actually ran. That rule is
the reason it exists rather than a `for` loop: a loop that runs everything
reports the *last* exit code, so a failed install followed by a passing lint
looks like success.

Environment from a file, explicitly:

```python
from sandbox_cli.env import read_env_file
ws.run(["python3", "app.py"], env=read_env_file(".env.local"))
```

Nothing is read unless you name a file, and a malformed line raises with its
number rather than being skipped — a silently ignored line in a credentials file
is how a run goes out without the key it needed. Values travel in the request
body, so against a remote daemon without TLS they cross the network in cleartext.

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
