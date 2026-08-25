# Many agents at once (`fleet`)

`sandbox-cli fleet` runs several agents from one file — each on its own branch, in
its own git worktree, in its own background container — and then tells you which
of them actually worked:

```yaml
# fleet.yaml
agent: claude
max_parallel: 2
defaults: { memory: 4g, cpus: "2", git: true }

tasks:
  - branch: feature-login
    prompt: Implement the login form in src/auth/. Add tests. Commit when they pass.
    verify: go build ./... && go test ./...

  - branch: feature-ratelimit
    agent: codex        # a different agent for this branch
    memory: 8g          # and its own limits
    prompt: Add per-IP rate limiting to src/server/. Add tests. Commit when they pass.
    verify: go test ./src/server/...
```

```sh
sandbox-cli fleet run                     # fan out
sandbox-cli fleet status --watch          # who is running, what they produced
sandbox-cli fleet logs feature-login      # what one agent actually said
sandbox-cli fleet land --all              # commit + merge every branch that can be
sandbox-cli fleet clean                   # reap the finished containers
```

If part of it goes wrong, you do not re-run the file: `fleet run --only
feature-login` retries the one task, and `fleet run --resume` starts whatever is
not already running or finished.

## What makes it more than a fan-out

The **`verify:`** command runs inside the container after the agent, and its exit
code — not the agent's say-so — decides whether the work is done. `fleet land`
refuses to merge a branch that failed it, along with a handful of other
ambiguities: a still-running agent, a checkout that moved to a different branch
since launch, an agent working in the branch being merged into. It never resolves
a conflict itself.

A fleet can **mix agents**: `agent:` at the top is the default, and a task that
names its own overrides it — put Claude on one branch and Codex on another and
compare. Only agents with a verified headless mode may appear (`claude`, `codex`,
`gemini`, `opencode`, `droid`, `cline`), because an unattended agent that stops to ask
permission hangs rather than fails, and each one you name needs its own login
before the run. Add `--profile prod` for an unattended run: it refuses where dev
warns, which is what you want when nobody is watching.

A fleet container is a session like any other — it appears in `sandbox-cli list`
marked `fleet`, and `logs`, `attach` and `kill` all take its branch name. See
[Sessions](sessions.md).

## More

- Full walkthrough: [Running a fleet](../GUIDE.md#a-fleet)
- Commented example: [`docs/examples/fleet.yaml`](../examples/fleet.yaml)
- Which agents a fleet may run: [Agent reference](../AGENTS.md#agents-a-fleet-can-run)

---

Next: [Worktrees](worktrees.md) · [Sharing files between sandboxes](sharing.md) ·
[documentation index](../README.md)
