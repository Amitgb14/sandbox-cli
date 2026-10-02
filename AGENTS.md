# AGENTS.md

Guidance for coding agents working in this repository. Claude Code reads it
through `CLAUDE.md`; other agents read it directly.

Not to be confused with [`docs/AGENTS.md`](docs/AGENTS.md), which is user
documentation for the agents sandbox-cli runs.

## What this is

Isolated microVM sandboxes for AI agents, behind one API, run three ways:

| Mode | Where | Backend |
|---|---|---|
| Local | the user's Mac | the native macOS `container` runtime (macOS 26+, arm64) |
| Self-hosted | a Linux machine the user controls (KVM) | Firecracker |
| Cloud | our hosted fleet | Firecracker |

- `sandboxd` serves the API, one per machine.
- `sandbox-guestd` runs inside every VM and is the only way in.
- The CLI, the agent wrappers, Studio and the SDKs are all clients of the API.
- A conformance suite, run against any endpoint, is what "the same in three
  modes" means.

The plan, milestones and open questions are in
[`docs/rewrite/PLAN.md`](docs/rewrite/PLAN.md). Read it before starting a
milestone.

## State of the tree

This branch (`rewrite`) is mid-rewrite. `main` still ships beta.15, a CLI that
runs agents in docker/podman containers, and takes security fixes only until the
rewrite replaces it.

- `internal/`, `cmd/` — the new tree. `go test ./...` sees only this.
- `_old/` — the beta.15 tree, ignored by the Go tool for its leading underscore.
  It does not compile in place; it is there to be ported from (skill:
  `port-from-old`). It is deleted when the rewrite merges.
- `_old/CLAUDE.md` — the design record of beta.15. Most of its docker mechanisms
  are obsolete; the *reasons* behind its rules are not. Read the relevant section
  before porting or replacing anything it describes.

## Commands

```sh
make build                  # -> bin/sandbox-cli
go vet ./...
go test ./...               # unit tests; no VM, no daemon
go test -race ./...
go test ./internal/policy -run TestProjectConfigRefusesPrivilegedKeys   # one test
go build -o bin/sandboxd ./cmd/sandboxd

# a real microVM: needs /dev/kvm, mkfs.ext4, network, a guest kernel and firecracker
SANDBOX_TEST_KERNEL=/path/to/vmlinux SANDBOX_TEST_FIRECRACKER=/path/to/firecracker \
  go test -tags vm -v ./internal/backend/firecracker

# the conformance suite against a running sandboxd
SANDBOX_CONFORMANCE_ENDPOINT=unix:///run/user/$UID/sandboxd.sock \
SANDBOX_CONFORMANCE_TOKEN=… go test ./internal/api/conformance -run TestEndpoint -v
gofmt -w cmd internal
```

Go 1.25+. Dependencies are the standard library, `cobra` and `yaml.v3` — nothing
else without a decision recorded in the plan.

## Layout

```
cmd/sandbox-cli         client
cmd/sandboxd            the API server
cmd/sandbox-guestd      the guest agent: PID 1 of a Firecracker guest, and the only thing the host talks to
images/base/            the base image's Dockerfile (built in CI only)
internal/
  api/          v1 wire types and client; api/conformance is the suite every endpoint must pass
  server/       HTTP handlers and the request guard
  spec/         requests resolved against the server's policy: tighten, never loosen
  policy/       config schema, layering, profiles, trust refusals, reserved env
  hostpath/     refusals for host paths: never /, never home, never an ancestor
  githard/      every host-side git call goes through this
  creds/        secret broker; values never in an argv, a log or the audit record
  agents/       the agent descriptor table (verified headless modes only)
  egressproxy/  name-based egress allowlist
  audit/        run log — environment variables by name only
  termsafe/     printing repository-controlled text safely
  backend/      Backend interface + capabilities; macos/, firecracker/, fake/
  guestproto/   host <-> guest agent protocol; the host treats the guest as hostile
  vsock/        guest vsock listener, host dial through the VMM's bridge
  image/        OCI pull, safe unpack (paths resolved inside the root), ext4 root disks
packaging/systemd/      sandboxd unit and an example operator policy (docs/self-hosting.md)
packaging/launchd/      the macOS launch agent (docs/local-macos.md)
sdk/                    Python (tested: make test-sdk) and TypeScript clients
  workspace/    clone-in, bring-back
  state/ cli/ version/
```

The skeleton fills in milestone by milestone. A package with only a doc comment
says which milestone fills it.

## The rules that make it safe

Each of these was paid for with a reproduced escape. The ledger is
`docs/security/audit-2026-07-26.md` and the open backlog is
`docs/security/open-items.md`. Skill: `trust-boundary-review`.

1. **Isolation lives in `spec` and in each backend's pure renderer**
   (`BuildArgs` / `BuildConfig`), and nowhere else. `cli` and the server build
   requests; they never build an argv or a VM config. Each renderer has a golden
   test, and a golden file is updated deliberately, never to make a test pass.
2. **Host paths go through `hostpath`.** It refuses the filesystem root, the home
   directory and any ancestor of it, compared by **identity** (device + inode)
   and not by string. No profile, config layer or request relaxes it.
3. **Everything from outside the trusted party is untrusted input:**
   - a project `.sandbox.yaml`;
   - repository content;
   - anything that comes back from a guest: replies from guestd, bundles, synced
     files;
   - every API request.

   Untrusted input may **tighten** what is in force, never loosen it. Privileged
   config keys are refused from a project config; a new key decides its side in
   `policy/trust.go`, and `TestProjectConfigRefusesPrivilegedKeys` is where that
   is pinned.
4. **Host-side git goes through `githard`.** git runs commands named in files the
   agent can write: hooks, filters, merge drivers, `gpg.program`, fsmonitor. The
   one exception is a git command the user typed themselves.
5. **Some environment variables are instructions, not settings**
   (`policy.IsReservedEnv`). They cannot be set or forwarded from outside.
6. **Secrets travel by reference.** A value never appears in an argv, a log or
   the audit record. The record stores names only.
7. **Fail closed.** A control that was asked for and cannot be delivered refuses
   the run; it never runs without it. A backend that cannot honour a request
   says so through `Capabilities`, and the request is refused rather than
   weakened. Under the dev profile a *degradation* warns; under prod it refuses.
8. **`sandbox-guestd` does what the host asks and nothing else.** No command
   reads an arbitrary guest path back to the host on the guest's initiative.
9. **A reference to a sandbox is matched against our own listing,** never handed
   to a backend to resolve, so `kill postgres` cannot reach someone's database.
10. **A security fix lands with a test that fails on the code before it.** Check
    by putting the old file back and running the test.

## Conventions

- **Comments explain why.** This repository keeps its reasoning in code comments
  and in `docs/`. Match the density of the code around you.
- **No company names in anything committed** — docs, plans, comments, commit
  messages. Describe a vendor by its role ("a hosted microVM sandbox service").
  Tool and domain names that are technical identifiers are fine.
- **Commits:** a plain-sentence title saying what changed, then prose explaining
  why. No `Co-Authored-By` trailer. Skill: `commit-and-pr`.
- **CHANGELOG.md:** user-facing changes go under `Unreleased`; a release moves
  them under a dated version heading.
- **Verification on real hosts** (a Mac with macOS 26, a KVM Linux machine) is
  run by the maintainer. Hand over the exact commands and what a pass proves; do
  not build CI to run them. After a push, report and stop — do not poll CI.
- **Finishing a milestone:** skill `finish-milestone`.

## Skills

Project skills live in `.claude/skills/<name>/SKILL.md`. Agents without skill
support can read them as plain instructions.

| Skill | Use it when |
|---|---|
| `port-from-old` | moving a package or file from `_old/` into the new tree |
| `trust-boundary-review` | a change touches host paths, the host↔guest channel, config keys, environment variables, host-side git, credentials or API request handling |
| `conformance-test` | adding or changing API behaviour, or a backend |
| `commit-and-pr` | committing, pushing, opening a pull request |
| `finish-milestone` | closing a milestone in `docs/rewrite/PLAN.md` |

## Before you finish

- [ ] Tests added or updated for the change, covering edge cases
- [ ] `gofmt -l cmd internal` prints nothing; `go vet ./...` is clean
- [ ] `go test -race -count=1 ./...` passes
- [ ] Help text, CLI usage and web docs updated for user-visible changes
- [ ] Summary says what was tested, what wasn't, and any skipped bug tests
- [ ] A new agent's sessions show, resume and delete in the Sessions view
      *(from M10, when Studio is back; until then, n/a)*
- [ ] Anything fakes can't prove has a row in
      [docs/testing/end-to-end.md](docs/testing/end-to-end.md)
