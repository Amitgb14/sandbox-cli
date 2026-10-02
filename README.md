# sandbox-cli

Isolated microVM sandboxes for AI coding agents and any command — on your Mac,
on a Linux machine you control, or in the cloud, behind one API.

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

`sandboxd` serves the API on each machine. The CLI, the agent wrappers and the
SDKs are clients; they cannot tell which of the three they are talking to,
beyond what `sandbox-cli doctor` reports.

## Use it

```sh
sandbox-cli context add box https://sandbox.example.internal:7443 --token-file ~/box.token --ca ~/box-ca.pem
sandbox-cli context use box          # or stay on "local"

cd ~/projects/myapp
sandbox-cli claude                   # Claude Code in a fresh VM, on a clone of this repo
sandbox-cli run -- npm test          # any command; its exit code is yours
sandbox-cli run --network none -- make
sandbox-cli codex --detach -- exec "fix the failing test"
sandbox-cli list · logs ID · attach ID · kill ID · bring-back ID
```

When a run ends, new commits — including anything the agent left uncommitted —
are fetched into `refs/sandbox/<name>`. Your branches are never touched:

```sh
git log -p HEAD..refs/sandbox/sbx_…
git merge refs/sandbox/sbx_…
```

Agents with wrappers: claude, codex, gemini, opencode, droid (these also run
headless), and aider, amp, cline, continue, copilot, crush, cursor, goose,
openhands and qwen. A wrapper consumes its leading sandbox flags and passes
everything else to the agent: `sandbox-cli claude --network none --resume`.

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
