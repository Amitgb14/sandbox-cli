# Open security items

Companion to [`audit-2026-07-26.md`](audit-2026-07-26.md), which records what was
found and fixed. This file is the backlog: what is still open, why it matters,
and what a fix looks like. Every item below was **reproduced by execution** during
the audits — none is speculative.

Ordered by what I would do next, not by severity alone: an item that is cheap and
self-contained beats one that is severe but blocked on a decision.

---

## 1. Egress matches resolved IPs, not names

**Severity: high. Effort: large. Blocked on: nothing.**

`sandbox-egress-setup` resolves each allowlisted domain at container start and
permits those addresses. Two consequences, both confirmed:

- **Unlisted domains sharing an address are reachable.** `gist.github.com` — a
  write endpoint, and so an exfiltration channel — answers under the baseline
  because it shares `github.com`'s address. An auditor reached `docs.python.org`
  the same way, through `pypi.org`'s Fastly anycast IPs. Nothing inspects SNI or
  `Host`, and which domains share a CDN address is something an attacker can shop
  for.
- **Names resolve once.** A rotating record breaks a domain the user *did*
  allowlist, mid-session, with an error that looks like a network outage.

### What the fix is

A `CONNECT` proxy, with all container egress forced through it by
`iptables REDIRECT`/`TPROXY` rather than `HTTP_PROXY` (an agent can unset an
environment variable; it cannot unset a netfilter rule).

**This half does not require TLS interception.** The proxy sees
`CONNECT host:443` and enforces the allowlist by *name*, which:

- fixes both defects above;
- gives genuine per-hop redirect re-validation, because the agent's client opens
  a **new** `CONNECT` for a redirected host — the original stress-test scenario
  that started this whole review;
- narrows DNS further, since name resolution can move into the proxy.

Ship it independently of item 2. It is the larger part of the value and carries
none of the risk.

### Open questions

- `--detach` returns as soon as the container is up, so nothing on the host
  survives to hold a proxy. Needs a sidecar container or a daemon.
- sandbox-cli currently creates **no** per-run docker network, which is why
  "no orphaned networks" is free today. A proxy sidecar changes that — see item 4,
  which wants a per-run network anyway. Do them together.
- Non-HTTP egress (ssh-based git, arbitrary TCP) has no `CONNECT` to inspect.
  Decide whether that is refused or falls back to the address-based rules.

---

## 2. The agent still holds raw credentials

**Severity: high. Effort: large. Blocked on: a decision that is yours.**

This is the original request that began the review. `internal/creds` resolves
secret *references* on the host and forwards the values into the container, so
the agent process can read them with `printenv`.

### The decision to make first

Injecting a header into HTTPS **requires terminating TLS**. A `CONNECT` proxy
sees an opaque tunnel: it cannot inject `Authorization`, cannot read a `302`,
cannot see `Location`. So this needs a sandbox-cli CA in the container's trust
store, and that means:

- the proxy sees **every** prompt and response in plaintext, and holds both the
  real credentials and the CA private key — the highest-value target in the
  system, by a distance;
- `node:22-bookworm-slim` ignores the system trust store unless
  `NODE_EXTRA_CA_CERTS` is set, and anything pinning certificates breaks;
- if the container can ever read the CA key, the whole thing inverts into a way
  to MITM the *user*.

### What it does and does not buy

A sentinel stops the key *bytes* being exfiltrated for use elsewhere or later.
It does **not** stop a compromised agent **using** the credential — any process
in the container can still make requests through the proxy and have the real
value attached. That is a real gain; it should not be described as more.

---

## 3. The agent writes `.git/config` and `.git/hooks`, and the *user's* git runs them

**Severity: high. Effort: medium. Blocked on: a design call.**

`internal/githard` protects the git commands **sandbox-cli** runs. It does
nothing about the git the user runs afterwards, in their own shell:

```sh
# inside the sandbox, as the unprivileged agent
git config --file /workspace/.git/config core.fsmonitor /workspace/.git/hooks/fsmon
printf '#!/bin/sh\ntouch /tmp/HOST-RCE\necho ""\n' > /workspace/.git/hooks/fsmon
# later, on the host, in the user's own repo
git status        # -> /tmp/HOST-RCE created, as the host user
```

`.git/hooks/pre-commit` is the same door on the next commit. With `--worktree`
the parent repository's whole `.git` is bind-mounted read-write, so this reaches
a repository the sandbox is not even editing.

The agent must be able to edit project files — but `.git/config` and
`.git/hooks` are not project source, and `git status` is not "running the
project's code". This is the shortest path out of the advertised blast radius.

### Options

- **Mount `.git/config` and `.git/hooks` read-only** over the writable `.git`.
  Cleanest, but breaks in-container `git config` writes, which agents do use.
- **Snapshot and diff them around a run**, reporting changes at exit. `rescue`
  already walks the repository, so the machinery exists. Detects rather than
  prevents.
- **Document it**, and treat "the workspace `.git` is agent-controlled" as a
  stated property rather than a defect.

Pick one deliberately; the current state is the third by accident.

---

## 4. Sandboxes are not isolated from each other

**Severity: medium. Effort: medium. Blocked on: nothing.**

Every sandbox lands on the default bridge, where `icc=true` lets containers
reach each other on any port. Confirmed: a peer container read workspace data
from another sandbox on both a published and an unpublished port.

Running several agents in parallel is the advertised workflow (`--detach`, one
container per branch), so a compromised agent in repo A can dial into the agent
working on repo B.

**Partly closed already.** In allowlist mode the default-deny `INPUT` chain stops
this, and published ports are now scoped to the bridge gateway. The gap is
**default mode**, where no firewall runs at all.

### What the fix is

A per-run docker network. That reintroduces the cleanup burden the original
review was pleased to be free of — an orphaned network after a crash — so it
wants `sandbox-cli clean` extended to reap networks too. Do it alongside item 1,
which needs a network anyway.

Filtering ingress in default mode via the existing firewall is **not** the
answer: it would make every run take the root-entrypoint path with `NET_ADMIN`,
which is a worse default than the one it protects.

---

## 5. `host.docker.internal` resolves without `--host-gateway`

**Severity: medium. Effort: small. Blocked on: nothing.**

`spec.go` treats `host.docker.internal` as something `--host-gateway` opts into.
On Docker Desktop it resolves unconditionally. Confirmed: a sandbox with no flags
read a host service bound to `127.0.0.1:8931` — a service the user bound to
loopback *specifically* so nothing else could reach it.

The absence of the flag is not a defence on macOS. Either block the gateway
address by default (and let `--host-gateway` unblock it), or stop describing it
as opt-in. The first is the honest reading of what the flag is for.

---

## 6. No seccomp profile is applied, and resource limits are unbounded

**Severity: medium. Effort: small–medium. Blocked on: nothing.**

`Seccomp: ""` means "whatever the daemon does". On the audited machine the daemon
did nothing:

```
/proc/self/status -> Seccomp: 0
add_key(2)        -> succeeded          (docker's default profile returns EPERM)
unshare -r id     -> uid=0(root)        (unprivileged user namespaces available)
```

The cause is environmental, but the consequence is sandbox-cli's: the hardening
story rests on a profile that can be silently absent, leaving the full syscall
table plus `CLONE_NEWUSER` as kernel attack surface.

Separately, `memory` and `cpus` default to empty. `--pids-limit 1024` holds;
nothing else does. An agent can exhaust host RAM and CPU, and can fill the Docker
Desktop disk image — a sparse file on the user's real disk that does **not**
shrink when the container is `--rm`'d.

Fix: pin a profile with `--security-opt seccomp=<file>` (or detect its absence
and say so), and consider a default memory cap. Note the precedent — resource
limits are opt-in deliberately, because an unexpected OOM-kill is worse than an
unbounded-but-observed container. Changing that is a real trade, not a bug fix.

---

## 7. Denial logging is unreadable on macOS

**Severity: low. Effort: small. Blocked on: nothing.**

The firewall's rate-limited `LOG` rules (`sandbox-cli DENY-OUT` / `DENY-IN`) are
correct on Linux, where they reach the host's kernel log. Under Docker Desktop
they land in the VM's ring buffer, which is impractical to read from the host —
so on the platform most users are on, a blocked connection still leaves no trace
they can find.

The run log at `~/.config/sandbox/audit/sessions.jsonl` is the portable half, but
it records the *policy*, not the *denials*.

Cheapest fix: read the REJECT rules' packet counters (`iptables -L -v -n`) at
container exit and record the counts in the audit line. That answers "did this
run try to reach something it was refused?" without needing the kernel log.
`NFLOG` to a userspace reader is the thorough version.

---

## 8. Cross-project persistence channels

**Severity: medium. Effort: small (mostly a decision). Blocked on: a decision.**

Two surfaces let an agent compromised in project A affect project B:

- **The persisted agent HOME** (`~/.config/sandbox/agents/<name>`) is mounted as
  `HOME` in *every* project, and the bootstraps exec from `$HOME/.local/bin`. An
  agent that overwrites that binary controls every future session for that agent,
  in every project — alongside the stored OAuth credentials and every project's
  transcripts.
- **`--cache` named volumes** are shared across agents *and* projects, so content
  planted in one is consumed at build time in another.

Both are deliberate features with real value, and per-agent HOME isolation *does*
hold (each wrapper mounts only its own directory, `0700`). Neither is documented
as a risk.

Options: document it; scope the HOME per project (losing "log in once"); or
verify the bootstrap binary before exec. Decide rather than leave it implicit.

---

## Not on this list

Things an audit raised that were judged correct as they are, so nobody
re-litigates them later:

- **`worktree.Git` is not hardened.** `sandbox-cli worktree git …` is the user
  running their own git in their own repository; hooks firing is expected there.
- **The firewall runs in allowlist mode only.** Making it unconditional would
  mean every run taking the root-entrypoint path with `NET_ADMIN`.
- **`--config <path>` is trusted.** Typing the path is the deliberate act that
  discovery never involves.
- **git-lfs repositories snapshot real content** rather than pointers. Larger,
  but the more useful thing for a rescue copy.
- **DNS name-based exfiltration** through the container's real resolver remains
  possible. The arbitrary-peer tunnel on port 53 is closed; encoding data into
  qnames sent to the configured resolver is not a firewall problem. Item 1 is
  where that gets addressed, if at all.
