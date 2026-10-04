# sandbox-cli

Isolated microVM sandboxes for any command — on your Mac, on a Linux machine you
control, or in the cloud, behind one API. Coding agents are a layer on top
(`sandbox-cli agent`), not a requirement.

Every sandbox is a VM with its own kernel. A sandbox is not built around your
repository: every process starts in the sandbox user's home, `/sandbox/home`,
and code gets in the way it gets into any machine, by `git clone` inside the
sandbox or through the files API. None of your files are mounted into the
guest, and nothing comes back to your machine except an agent's saved login. Egress is
open by default; ask for an allowlist (`--network allowlist`, or the `prod` profile) and
it is enforced outside the guest, where the agent cannot reach it, with the agent's own
API always on it.

```
  sandbox VM  (its own kernel; processes start in /sandbox/home)
  code:     git clone inside the sandbox, or the files API; volumes keep data between runs
  network:  open by default; or an allowlist of names, enforced on the host (prod: always)
  logins:   copied in and out per run, never a mounted host directory
```

> **This is the rewrite.** The beta.15 tool ran agents in docker containers;
> this one runs microVMs, and it drops docker, podman and Windows. See
> [CHANGELOG.md](CHANGELOG.md) and [docs/rewrite/PLAN.md](docs/rewrite/PLAN.md).

## Three ways to run it

| | Where | Isolation | Guide |
|---|---|---|---|
| **Local** | your Mac (macOS 26+, arm64) | a VM of the native `container` runtime | [docs/local-macos.md](docs/local-macos.md) |
| **Self-hosted** | a Linux machine with KVM, or many behind one gateway | Firecracker microVMs, egress enforced on the host | [docs/self-hosting.md](docs/self-hosting.md), [docs/fleet.md](docs/fleet.md) |
| **Cloud** | hosted | the same as self-hosted | (coming) |

`sandboxd` serves the API on each machine. The CLI and the SDKs are clients; they cannot tell which of the three they are talking to,
beyond what `sandbox-cli doctor` reports. `sandbox-gateway` serves the same API in front of many machines, with a key per
user, sandboxes only their owner can see, and SSH on one port ([docs/fleet.md](docs/fleet.md)).

## Use it

```sh
sandbox-cli context add box https://sandbox.example.internal:7443 --token-file ~/box.token --ca ~/box-ca.pem
sandbox-cli context use box          # or stay on "local"

sandbox-cli run -- uname -a         # a fresh VM; its exit code is yours
sandbox-cli run -- sh -c 'git clone https://github.com/you/app && cd app && npm test'   # needs a network that reaches github.com
sandbox-cli run --network none -- make
sandbox-cli run --keep --name dev -- bash
sandbox-cli run --detach --name dev -- sleep infinity
sandbox-cli shell dev               # a shell in a running sandbox; exit leaves it running
sandbox-cli exec dev -- git clone https://github.com/you/app   # one command in it; its exit code is yours
sandbox-cli list · logs ID · attach ID · kill ID
sandbox-cli snapshot · suspend · resume · tunnel
sandbox-cli events ID                # what the sandbox was asked to do, and how it ended
sandbox-cli volume create cache; sandbox-cli run --volume cache:/sandbox/home/.cache -- npm ci
sandbox-cli run --label team=infra -- make; sandbox-cli list --label team=infra
```

Work done in a sandbox stays there. To keep it, push it from inside (a commit
made in a sandbox carries a neutral `sandbox` identity), read it out through the
files API, or write it to a volume, which outlives the VM.

### Through a gateway

A gateway puts many sandboxd nodes behind one address, with a user for every
API key and one SSH port for every sandbox. The CLI uses it like any other
context:

```sh
sandbox-cli context add fleet https://gateway.example.internal --token-file ~/fleet.key
sandbox-cli context use fleet
sandbox-cli whoami                   # user, tenant, scopes and key id
sandbox-cli run --keep --name demo -- sleep infinity
sandbox-cli ssh demo                 # registers ~/.ssh/id_ed25519.pub, pins the host key, runs ssh
sandbox-cli ssh demo -- uname -a

sandbox-cli ssh-key add              # or register a key yourself; then plain ssh works:
ssh demo@gateway.example.internal -p 2222
sandbox-cli ssh-key list · ssh-key rm ID

sandbox-cli ssh-access demo --ttl 15m   # a short-lived `ssh TOKEN@gateway …` line, no key needed
```

The token in an `ssh-access` line is the whole credential until it expires;
hand it only to whoever should have that access. Every SSH login needs an
active API key with the `sandbox:ssh` scope, and revoking it closes open
sessions too ([docs/operations.md](docs/operations.md#revoking); all of SSH is in [docs/ssh.md](docs/ssh.md)). Against a plain sandboxd,
`sandbox-cli ssh` opens the session through the API instead, since a sandboxd
has no SSH server.

### Agents and jobs on a fleet

A gateway also runs work after you have gone. A job is a command, or an agent
and a prompt, run in a fresh sandbox per run: the gateway places it, starts
it, waits with a timeout, keeps its output (1 MiB per stream) and the files
you name (8 MiB each), terminates the sandbox, and retries a run that failed.
`prompts` makes a batch, one run per prompt; `parallelism` bounds how many
run at once. A run that does not fit the tenant's quota waits for room.

```sh
sandbox-cli secret set ANTHROPIC_API_KEY < key.txt      # sealed on the gateway, never shown again
sandbox-cli agent-run claude "fix the failing test" --secret ANTHROPIC_API_KEY --wait

cat > job.yaml <<'YAML'
agent: claude
prompts: ["review internal/api", "review internal/gateway"]
parallelism: 2
retries: 1
timeout_secs: 1800
secrets: [ANTHROPIC_API_KEY]
keep: {files: [/sandbox/home/review.md]}
notify: https://hooks.example.com/sandbox    # POSTed {job, run, state}; never output
YAML
sandbox-cli job run -f job.yaml
sandbox-cli job ls · job get ID · job output ID 0 [--file PATH] · job cancel ID
```

An agent runs in its verified headless mode (claude, codex, gemini, opencode,
cline). Your saved login does not come along — the CLI's run copies it in
from your machine, and a job has none — so the agent authenticates with an
API key you keep as a secret: `ANTHROPIC_API_KEY` for claude, `OPENAI_API_KEY`
for codex, `GEMINI_API_KEY` for gemini, the provider's for opencode and cline.
The agent must be in the image or installable from it. A secret goes into a
run's environment by name, only for jobs that name it; the API never returns
it. Secrets need the gateway started with `--secrets-key-file` (32 random
bytes, mode 0600), and setting one needs a key with the `secrets:write` scope.
A finished job is kept for a day (`--job-retention`). A restarted gateway
picks its jobs up again: a running command is followed where it runs. Jobs,
batches, secrets and notify webhooks are in [docs/jobs.md](docs/jobs.md);
services, which keep a count of sandboxes running, in
[docs/services.md](docs/services.md).

## Coding agents

`sandbox-cli agent <name>` is `run` with a coding agent's conveniences on top:
its login is restored into the sandbox and saved again afterwards, and its own
environment variables are forwarded when set.

```sh
sandbox-cli agent ls                                  # the agents, and whose login is saved
sandbox-cli agent claude                              # Claude Code in a fresh VM
sandbox-cli agent claude --network none -- --resume   # sandbox flags first, then the agent's
sandbox-cli agent codex --detach -- exec "fix the failing test"
```

Agents: claude, codex, gemini, opencode and cline, which can also run
unattended, and copilot, cursor, devin, goose, kilocode, openhands and qwen.

When a provider is down, a run can fall through to another agent:

```sh
sandbox-cli agent claude --fallback codex -p "fix the failing test"
```

Each provider is probed before a sandbox is made for it, and one that is down
is skipped for the next. A run that started is never retried with another
agent: it may have done work, and that work must not be done twice. Put
`routing: [claude, codex]` in `~/.config/sandbox/config.yaml` to make a chain
the default; a project's `.sandbox.yaml` cannot set it.

With several agents going, `sandbox-cli agent state` says which one needs
you. It reports working, blocked (quiet at a terminal, so waiting for an
answer), idle, done or failed. It decides from the agent's process and its
conversation, never from what the agent wrote. `agent wait` blocks until an
agent gets to one of the states you name:

```sh
sandbox-cli agent state
sandbox-cli agent wait fix-auth --state blocked --state done --state failed --timeout 30m
```

## Studio

```sh
sandbox-cli studio          # prints http://127.0.0.1:7080/#token=…
```

The same sandboxes in a browser: launch a command or an agent, use its
terminal, watch its output, files and audit events. Studio is served by
`sandbox-cli` itself on a loopback port, needs the token it prints, and talks to
the current context's `sandboxd` without handing that `sandboxd`'s token to the
browser. See [docs/studio.md](docs/studio.md).

## Build

```sh
make build      # bin/sandbox-cli, bin/sandboxd, bin/sandbox-guestd (linux, for the guest)
make test       # unit tests and the API conformance suite against an in-memory backend
```

The microVM tests need `/dev/kvm`; see [AGENTS.md](AGENTS.md) and
[docs/testing/end-to-end.md](docs/testing/end-to-end.md).

## Documents

- [docs/api/v1.md](docs/api/v1.md): the API every mode serves.
- [docs/self-hosting.md](docs/self-hosting.md), [docs/local-macos.md](docs/local-macos.md): running sandboxd.
- [docs/fleet.md](docs/fleet.md): many sandboxd nodes behind one `sandbox-gateway`, for one machine or many;
  with [SSH](docs/ssh.md), [organizations](docs/organizations.md), [jobs and secrets](docs/jobs.md),
  [services](docs/services.md) and [operations](docs/operations.md) on pages of their own.
- [docs/cli.md](docs/cli.md), [docs/sandboxd.md](docs/sandboxd.md), [docs/studio.md](docs/studio.md): the CLI's every command, the server's flags and policy file, Studio.
- [docs/README.md](docs/README.md): every page, grouped as on the website's `/docs`.
- [docs/rewrite/PLAN.md](docs/rewrite/PLAN.md): the plan, milestones and what was measured.
- [docs/security/](docs/security/): the audit ledger and open items.
- [AGENTS.md](AGENTS.md): for anyone, human or agent, changing this repository.
