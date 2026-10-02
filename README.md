# sandbox-cli

Isolated microVM sandboxes for any command — on your Mac, on a Linux machine you
control, or in the cloud, behind one API. Coding agents are a layer on top
(`sandbox-cli agent`), not a requirement.

Every sandbox is a VM with its own kernel. Your repository goes in as a git
bundle and the agent's work comes back as one, into `refs/sandbox/<name>`, for
you to review and merge. Nothing on your machine is mounted into the guest
unless you ask (and never your home directory). Egress is an allowlist of names,
enforced outside the guest where the agent cannot reach it.

```
  your repo ── git bundle ──►  sandbox VM  (/workspace, its own kernel)
            ◄── git bundle ──  the agent's commits → refs/sandbox/<name>

  network:  only names on the allowlist; DNS answers nothing else
  logins:   copied in and out per run, never a mounted host directory
```

> **This is the rewrite.** The beta.15 tool ran agents in docker containers;
> this one runs microVMs, and it drops docker, podman and Windows. See
> [CHANGELOG.md](CHANGELOG.md) and [docs/rewrite/PLAN.md](docs/rewrite/PLAN.md).

## Three ways to run it

| | Where | Isolation | Guide |
|---|---|---|---|
| **Local** | your Mac (macOS 26+, arm64) | a VM of the native `container` runtime | [docs/local-macos.md](docs/local-macos.md) |
| **Self-hosted** | a Linux machine with KVM | Firecracker microVMs, egress enforced on the host | [docs/self-hosting.md](docs/self-hosting.md) |
| **Cloud** | hosted | the same as self-hosted | (coming) |

`sandboxd` serves the API on each machine. The CLI and the SDKs are clients; they cannot tell which of the three they are talking to,
beyond what `sandbox-cli doctor` reports.

## Use it

```sh
sandbox-cli context add box https://sandbox.example.internal:7443 --token-file ~/box.token --ca ~/box-ca.pem
sandbox-cli context use box          # or stay on "local"

cd ~/projects/myapp
sandbox-cli run -- npm test          # a fresh VM on a clone of this repo; its exit code is yours
sandbox-cli run --network none -- make
sandbox-cli run --keep --name dev -- bash
sandbox-cli list · logs ID · attach ID · kill ID · bring-back ID
sandbox-cli snapshot · suspend · resume · tunnel
sandbox-cli events ID                # what the sandbox was asked to do, and how it ended
sandbox-cli volume create cache; sandbox-cli run --volume cache:/sandbox/home/.cache -- npm ci
sandbox-cli run --label team=infra -- make; sandbox-cli list --label team=infra
```

When a run ends, new commits — including anything left uncommitted — are
fetched into `refs/sandbox/<name>`. Your branches are never touched:

```sh
git log -p HEAD..refs/sandbox/sbx_…
git merge refs/sandbox/sbx_…
```

While a run is attached, its working tree is also checkpointed to
`refs/sandbox/checkpoints/<id>` every five minutes (`--checkpoint-every`). That
doesn't touch the sandbox's own index or branches. If the CLI is killed, the
machine sleeps or the VM dies, `sandbox-cli recover` says where each run's work
still is: in a sandbox you can still bring back, in its last checkpoint, or
nowhere.

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

Agents: claude, codex, gemini, opencode and droid, which can also run
unattended, and aider, amp, cline, continue, copilot, crush, cursor, goose,
openhands and qwen.

When a provider is down, a run can fall through to another agent:

```sh
sandbox-cli agent claude --fallback codex -p "fix the failing test"
```

Each provider is probed before a sandbox is made for it. A run that fails
having changed nothing is retried with the next agent in a fresh sandbox, with
a briefing from the first agent at `/sandbox/context`. That briefing is not a
resumed conversation. A run that changed files is never retried. Put
`routing: [claude, codex]` in `~/.config/sandbox/config.yaml` to make a chain
the default; a project's `.sandbox.yaml` cannot set it.

```sh
sandbox-cli agent fleet run -f fleet.yaml  # one agent per branch, in parallel sandboxes
sandbox-cli agent fleet status
sandbox-cli agent fleet land --all         # merge what verified
```

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
- [docs/rewrite/PLAN.md](docs/rewrite/PLAN.md): the plan, milestones and what was measured.
- [docs/security/](docs/security/): the audit ledger and open items.
- [AGENTS.md](AGENTS.md): for anyone, human or agent, changing this repository.
