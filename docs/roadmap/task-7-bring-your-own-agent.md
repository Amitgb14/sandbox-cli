# Task 7 — Bring your own agent

**Goal.** Make sandbox-cli as easy to point at *your own* agent — a Python or Node
program you wrote — as it already is to point at Claude Code. Today the boundary works
for any command; the ergonomics only work for the fifteen agents that have a wrapper.

**Branch.** Not started.

```
        the agent wrappers                 your own agent
  ────────────────────────────      ────────────────────────────
  sandbox-cli claude                sandbox-cli run -- python main.py
    image        baked                 image        you build it, tag unknowable
    deps         baked                 deps         no pip in the base image
    argv         a descriptor          argv         you retype it every run
    egress       baseline covers it    egress       you enumerate it
    credentials  persisted HOME        credentials  you write a secrets: block
    deploy       you are watching      deploy       you write the systemd unit
       ↑ one word                         ↑ a day of reading (this task)
```

---

## Why this, and why now

The boundary is already general. `sandbox-cli run -- <anything>` gets the same egress
allowlist, the same `cap_drop: ALL`, the same credential broker and the same audit line
that `sandbox-cli claude` gets. Nothing in `internal/sandbox` or `internal/runtime` knows
what an agent is.

What is not general is everything around it. A user arriving with a Python agent has to
discover the base image tag to write a `FROM` line, learn that `image:` is refused from
the file the tool told them to scaffold, enumerate their own egress domains, and write
their own supervisor — before the first run. Each of those is defensible on its own and
they are individually documented. Together they are the reason the tool reads as
"a wrapper for coding agents" rather than "a sandbox you can put anything in."

A landscape review in August 2026 (Vercel Sandbox, agent-infra/AIO Sandbox, and Mastra's
platform comparison) makes the shape of the expectation concrete. Three things recur, and
only the first two are worth wanting here:

- **A template gets you running before you understand the config.** Vercel's quickstart is
  `Sandbox.create({ image })`. AIO's is a single `docker run`. Neither asks you to read a
  trust model first.
- **An SDK, not only a CLI.** Vercel ships TS and Python; AIO ships Python, TS and Go.
  This is the "Developer Experience" axis Mastra ranks platforms on, and it is the axis
  where a local CLI can most cheaply reach parity, because the server already exists here.
- **Snapshot, fork, resume-by-name.** Mastra calls these table stakes. They are
  [task 5](task-5-checkpoint-and-fork.md), they are not this task, and the reason is in
  *Not in this task* below.

The thing to keep sight of: on Mastra's own criteria, sandbox-cli's egress allowlist is
ahead of all six platforms reviewed — they list "network policy controls" as table stakes
and none of them decide on a hostname per connection. **This task is about lowering the
cost of getting that, not about trading it for parity elsewhere.**

---

## What exists today

Verified against the code, not from memory.

- **`sandbox-cli run -- <cmd>`** already runs any command with the full boundary. So does
  `--detach`, `--allow`, `--secret`, `--memory`, `--worktree`, and every other flag on
  `addRunFlags` — none of them are agent-specific.
- **`POST /v1/runs` already accepts an arbitrary command.** `RunCreateRequest` in
  [`docs/studio-api/types.ts`](../studio-api/types.ts) carries `command?: string[]`
  alongside `agent`/`prompt`, plus `image`, `allow`, `env`, `memory`, `cpus`. Together
  with `GET /runs/{id}/logs` (SSE **and** WebSocket), `/metrics` and `/stop`, most of an
  SDK's surface is already served and enforcing every isolation invariant — it is
  undocumented for non-agent use and has no client library.
- **`sandbox-cli init`** writes one `.sandbox.yaml`, and it is mostly a list of keys that
  are *refused* from that file. It teaches the trust model correctly and gets nobody
  running.
- **`config/trust.go`** refuses `image`, `mounts`, `secrets`, `env`, `env_allow`,
  `security.*`, `cache.paths`, `ports`, `snapshot` and any weakening network setting from
  a discovered project file. This is the constraint that shapes every design below: a
  template that scaffolds those keys into `.sandbox.yaml` makes every later command in
  that directory fail.
- **`fleet.yaml` already solves that problem once.** It carries **CLI-flag trust** — named
  with `-f`, never discovered upward, no `profile:` key — argued in `fleet.Load`'s doc
  comment. It is the existing precedent for "a file that may name the boundary."
- **`internal/fleet/gates_test.go`** classifies every field of `sandbox.Options` as
  `fromSpec`, `gated` or `never`, and fails when the struct grows one that is not in the
  table. Any new caller that builds `Options` inherits that requirement.
- **The base image has `python3` and no `pip`.** `internal/image/assets/Dockerfile:28`
  installs `python3` and `build-essential`; there is no `python3-pip`, no `python3-venv`.
  Node and npm come from the `node:22-bookworm-slim` base.
- **The base image tag is content-addressed** (`image.Ref` hashes the Dockerfile and the
  embedded proxy source), and no command prints it. A user cannot write a `FROM` line
  without reading it out of the README, where it is a snapshot that goes stale.
- **There is no restart policy.** `runtime.BuildArgs` renders `--name` and never
  `--restart`; supervision after a crash is the caller's job, and nothing says so.
- **Detached container names are deterministic only for repo+branch.** `containerName`
  (`internal/sandbox/spec.go:677`) falls back to a timestamp when there is no branch to
  build from — which is every non-worktree workload run. Nothing addressable comes back.

So this is not "make the sandbox general". It is "make the general thing reachable
without reading four documents first".

---

## Required features

### 1. `pip` in the base image

One line in `internal/image/assets/Dockerfile`: `python3-pip`, `python3-venv`. It is
listed first because it is the cheapest item on this roadmap and it blocks the entire
Python story — today every Python user must build a custom image before their first run,
which puts the hardest step first.

Done when `sandbox-cli run -- pip install -r requirements.txt && python main.py` works
against the stock image.

### 2. A workload file with `fleet.yaml`'s trust

The keys a real workload needs — `image`, `command`, `allow`, `secrets` — are exactly the
keys `.sandbox.yaml` refuses, and correctly. The answer already exists in this codebase:
a file named with `-f`, never discovered, carrying CLI-flag trust.

```yaml
# sandbox/workload.yaml
image: ./sandbox/Dockerfile      # built on demand, tagged by content
command: [python, -u, main.py]
allow: [api.anthropic.com, api.example.com]
baseline: false
secrets:
  ANTHROPIC_API_KEY: { file: ~/.secrets/anthropic }
memory: 4g
cpus: "2"
```

```sh
sandbox-cli up -f sandbox/workload.yaml --profile prod
sandbox-cli up -f sandbox/workload.yaml --detach --name my-agent
```

What has to be true for this to count as done:

- `up` composes; it does not reimplement. Same `config.LoadProfile` → `sandbox.BuildSpec`
  → `runtime.BuildArgs` path, no second place isolation lives.
- It gets its own row in `gates_test.go` **in the same change**. A new caller that builds
  `Options` is a new way for a container to differ from the interactive one; `persist_auth`
  is the recorded precedent for that being noticed late.
- It carries no `profile:` key, for the same reason `fleet.yaml` does not.
- `--dry-run` works on it, because the ability to read the boundary as a string before
  trusting it is the thing this tool has that the cloud platforms do not.

### 3. `sandbox-cli init --template <name>`

`init` is the right home; today it scaffolds a config, and it should scaffold a *workload*.

```sh
sandbox-cli init --template python-agent
sandbox-cli init --list-templates
```

A template emits three files at three trust levels, and the split is the lesson rather
than a layout preference:

| file | trust | holds |
|---|---|---|
| `.sandbox.yaml` | untrusted, committed | project keys only — the ones `trust.go` permits |
| `sandbox/Dockerfile` | built, not run on the host | `FROM` the pinned base + your deps |
| `sandbox/workload.yaml` | CLI-flag trust, named with `-f` | image, command, allow, secrets, limits |

Plus a commented `systemd` unit, because feature 6 is otherwise invisible.

Starting set: `python-agent`, `node-agent`. Not a gallery — every template is a claim
about a working setup, and a stale one is worse than none.

### 4. `sandbox-cli image ref` (and `image build`)

`ref` prints the current content-addressed base tag so a `FROM` line can be written or
scripted. `build` builds a workload's Dockerfile against it. Small, and it removes the
one blocker that no amount of documentation fixes, because the tag changes when the
image does.

### 5. Document the API for arbitrary commands, then wrap it

`command[]` is already in the contract and nothing in
[`docs/studio-api/README.md`](../studio-api/README.md) tells a reader that a non-agent
workload is a first-class thing there. Documenting it is most of the work.

A thin client for Python and TypeScript follows, shaped like the SDKs people now expect:

```python
sb = Sandbox.create(image="my-agent:latest", allow=["api.example.com"])
r  = sb.run(["python", "main.py"])
for line in sb.logs(follow=True): print(line)
```

It stays honest about what it is: a client for a **loopback-only, token-authenticated
local** control plane (`studioapi/guard.go` refuses a non-loopback `Host`). An SDK whose
README implies a hosted service would be describing the declined feature.

### 6. Say what deployment is

A workload template ships a `systemd` unit and the docs say plainly that sandbox-cli is a
launcher and an isolation policy, not a supervisor: no `--restart`, no health check, no
reboot survival. Run it in the foreground under a supervisor that has those; use
`--detach` only when sandbox-cli's own `list`/`logs`/`kill` is the supervision you want,
and remember detached containers are deliberately not removed on exit.

### 7. Close the `--publish` edge before advertising prod for workloads

prod's stated guarantee is that nothing is published. `ValidateProfile` asserts
`len(cfg.Ports) == 0`, but `--publish` arrives as `Options.Publish` and is merged into
the resolved ports in `BuildSpec` (`internal/sandbox/spec.go:546`), after the validator
has run on the config — so the config key is refused under prod and the flag is not.

That gap is tolerable while prod is a coding-agent posture. It is not tolerable in a
document that tells people to run services under prod, and this task is that document.
Either the validator learns about the option, or the guarantee is reworded — decide it
deliberately, and record it in [`open-items.md`](../security/open-items.md) either way.

---

## Not in this task

- **Snapshot, fork, resume-by-name.** Mastra calls them table stakes and Vercel makes
  persistence the default; both are [task 5](task-5-checkpoint-and-fork.md). Note the
  local case is genuinely weaker than it looks in a comparison table: `/workspace` is a
  bind mount, so the *work* already survives — `RestoreResult.MatchesWorkingTree` exists
  to report exactly that. What does not survive is `HOME` and installed state. A named
  volume for workload state is a reasonable increment; filesystem forking fights the
  disposable-container premise the rest of the design rests on, and belongs in task 5
  where that argument gets made once.
- **Persistent-by-default.** Long-lived state an injected agent can poison across runs is
  the opposite of what `--rm` buys.
- **An MCP server exposing "run this in a sandbox".** AIO ships one and it is the obvious
  extension of feature 5. It stays **declined** on the grounds already recorded in
  [the roadmap](README.md#considered-and-declined): the boundary is decided by the person,
  in a config the agent may not weaken, and an API that lets an agent choose its own
  policy hands that decision to the process the policy exists to contain. Nothing in this
  task is a step toward it.
- **A fat image** — browser, VNC, code-server, Jupyter, the AIO model. Already declined,
  and it contradicts the minimal-surface argument that keeps agent adapters out of the
  base image.
- **Shareable preview URLs, remote runners, a hosted control plane.** All declined; the
  SDK in feature 5 must not read as a step toward any of them.
- **A workload registry, marketplace, or template gallery.** Two templates that work.

---

## Done when

1. `sandbox-cli init --template python-agent` in an empty directory, followed by one
   `sandbox-cli up -f sandbox/workload.yaml`, runs a Python agent under the egress
   allowlist with a brokered credential — with no other file read and no tag looked up.
2. The same for `node-agent`.
3. `up` has its row in `gates_test.go`, and `--dry-run` renders its argv.
4. `sandbox-cli image ref` prints a tag that a `FROM` line can use, and stays correct
   when the base image changes.
5. `docs/studio-api/README.md` documents `command[]` as a first-class workload, and a
   Python and a TypeScript client exist against it.
6. `pip install` works in the stock image.
7. The `--publish`-under-prod edge is either closed or written down.
8. Nothing above weakened the boundary: `internal/runtime/args_test.go` and the
   `--dry-run` golden test are updated intentionally, or not at all.
