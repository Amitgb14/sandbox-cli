# Open security items

Companion to [`audit-2026-07-26.md`](audit-2026-07-26.md), which records what was
found and fixed. This file is the backlog: what is still open, why it matters,
and what a fix looks like. Every item below was **reproduced by execution** during
the audits — none is speculative.

Ordered by what I would do next, not by severity alone: an item that is cheap and
self-contained beats one that is severe but blocked on a decision.

---

## 1. Egress matches resolved IPs, not names — **DONE**

**Closed.** `internal/egressproxy` enforces the allowlist by hostname, read from
the TLS SNI, an explicit `CONNECT`, or an HTTP `Host` header, and resolved fresh
per connection.

Verified live, in a sandbox allowing only `example.com` plus the baseline:

```
github.com           reachable (200)     <- on the list
gist.github.com      BLOCKED             <- shared github.com's address
pypi.org             reachable (200)     <- on the list
docs.python.org      BLOCKED             <- shared pypi.org's address
```

And that the enforcement is the redirect rather than the environment: unsetting
`HTTPS_PROXY` still refused, a non-80/443 port still refused, and a connection to
a bare address with no SNI refused rather than resolved from its destination.

The proxy runs inside the container as its own uid; the firewall permits outbound
traffic only from that uid and REDIRECTs everything else into it, so "going
around it" would mean being a different user. Item 4 below no longer needs a
per-run network on this account.

Item 2 (credential injection) is unaffected and still open — this deliberately
does not terminate TLS.

<details><summary>Original entry</summary>

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

</details>

### How the open questions resolved

- **`--detach` and sidecar lifetime: moot.** The proxy runs inside the sandbox
  container, started by the entrypoint before privileges are dropped, so it lives
  exactly as long as the run it belongs to. Nothing on the host has to survive.
- **No per-run docker network is needed**, so "no orphaned networks" stays free.
- **Non-HTTP egress falls through to the address rules**, unchanged. Only tcp/80
  and tcp/443 are redirected into the proxy; everything else meets the existing
  allowlist and the final REJECT. That narrows what is reachable and never widens
  it. ssh-based git is therefore still address-matched — the residual case, and a
  much smaller one than the CDN-sharing problem this closed.

---

## 2. The agent still holds raw credentials

What is true *today*, written for someone deciding what to hand an agent, is
[`secrets.md`](secrets.md). This entry is the backlog for what is still open.


**Severity: high. Effort: large. Decided 2026-08-04 — see below. Stays open.**

This is the original request that began the review. `internal/creds` resolves
secret *references* on the host and forwards the values into the container, so
the agent process can read them with `printenv`.

### The decision — B is rejected; minimise the loss instead

**Decided 2026-08-04.** The TLS-terminating proxy (option B) will not be built.
The combination below is adopted in its place, and this item stays **open**
rather than closing, because none of it stops the agent holding a value it can
read: what changes is what that value is worth.

The reasoning is the blast-radius ranking further down, and it is the whole
argument in one line: **a leak is assumed, not prevented**, so the question is
what it costs — and B answers that question worst, by concentrating every token,
every prompt in plaintext and the CA private key into a single process. What is
adopted, in the order value arrives per unit of work:

1. **`--profile prod` when the credentials matter.** Removes the account-wide,
   never-expiring, cross-project refresh token from the container and empties
   the egress baseline. No code: `PersistAuth=false`, held by `ValidateProfile`.
2. **Short-lived brokered secrets.** `secrets:` already runs a `Command`, so this
   is configuration today.
3. **A distinct credential per project**, so one leak reaches one repository.
4. **A minimal `--allow` list**, free under prod since the baseline is empty.

Residual worst case: *a ten-minute token, scoped to one repository, leaked from
one run.* Rotate it, or wait.

**The one piece of code this decision required** — because 2 is a practice with
nothing behind it, and someone writing `gh auth token` gets a credential lasting
months while believing they brokered one — is a **warning when a brokered secret
resolves to something long-lived**. Built: `creds.Classify` reads what a
credential's own shape says and `sandbox.warnLongLivedSecrets` names it once per
run.

The two signals it reads are not equally sound, and the design turns on saying
so. A **JWT's `exp`** is a measurement — issuer-agnostic, valid for issuers that
do not exist yet, and it cannot go stale; it is also the direction credentials
are moving, since STS, OIDC and workload-identity tokens are JWTs. A **prefix**
is a lookup against a list of claims about other vendors' formats, which can be
neither completed nor kept current — a user may hold a credential from an issuer
nobody here has heard of. Rather than pretend otherwise, the list stays short and
admits a prefix only if it is long, distinctive and documented as non-expiring,
and the warning **reports the evidence rather than an identification** ("begins
with `ghp_` — GitHub personal access tokens…"). A prefix reused by another issuer
then still yields a sentence that is literally true and dismissible, instead of
sending someone to the wrong vendor's dashboard. `sk-` was rejected on this rule
and AWS `AKIA`/`ASIA` on a second one: they match the access key *id*, not the
secret, so they would warn about the wrong value.

It **warns and never refuses**: for some credentials the long-lived form
is the only form there is — `ANTHROPIC_API_KEY` has no ten-minute variant — so
refusing would refuse the ordinary case. And **silence is not approval**: most
credentials are opaque strings carrying no lifetime and the prefix list will
never be complete, so an unrecognized value prints nothing, which means "nothing
was recognized" rather than "this one is fine".

What still survives all of it, and is the reason the item stays open: **DNS
exfiltration** (bottom of this file) and **misuse of a credential the agent
legitimately holds**. Those are the argument for keeping the *authority* small
rather than the secret hidden.

The analysis that produced this decision follows, kept in full so the next reader
can check it rather than take it.

### The decision as it was originally framed

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

### Two mitigations that are not the fix, and should not be mistaken for it

The decision above is framed as do-nothing or terminate-TLS, and that is a false
binary. Neither of these closes the item — under both, the agent still reads the
value with `printenv` — but both reduce what a leak is worth, which is the same
bargain the container boundary itself makes.

**C. Short-lived, narrowly-scoped credentials.** This needs *no code*: a
`secrets:` entry already takes a `Command` run via `sh -c`, so a broker that
mints a ten-minute token is a configuration choice today. What changes is the
value of what leaks — minutes and one scope, rather than a login that lasts
months.

It is a practice rather than a feature, and that is its weakness: nothing
enforces it, and a user who writes `gh auth token` gets a long-lived credential
while believing they brokered one. If this is the direction, the honest version
is documentation plus, perhaps, a warning when a brokered value looks
long-lived — not a claim in this file that the item is handled.

Note the interaction with the compose deployment: a `Command` runs wherever the
API process runs, so under `docker compose --profile api` it runs in a container
with neither `gh` nor your login. Brokered secrets belong to a host process.

**D. Let a declared secret constrain that container's egress.** Note what this
is *not*: the proxy cannot attach anything to a request. `sni.go` peeks at the
ClientHello to read the server name and then tunnels — `server.go` says why, a
byte written into that stream reaches the client as garbage inside its TLS
session. Injection is B's mechanism and needs B's CA. What this proxy can do is
decide **whether the connection opens at all**, by name, before any bytes move.

So D is: declaring `GITHUB_TOKEN` says something about where this container may
reach. The agent still reads the value with `printenv`; it simply cannot open a
socket to somewhere the credential has no business going, and exfiltration fails
at connect time.

The obvious form of that does not work. A run also needs `registry.npmjs.org`,
`api.anthropic.com`, `pypi.org` — narrowing egress to github.com alone breaks
it. So the useful shape is a **declared conflict** rather than a narrowing: a
container holding a credential *and* permitted to reach two hundred hosts is an
exfiltration channel, and the tool should say so rather than allow it silently.
That is a smaller claim than "the credential is scoped", and it is the one the
existing machinery can actually keep.

What it does not stop, either way, is exfiltration through a *permitted* host —
for `GITHUB_TOKEN` that means github.com, a write endpoint. Worth setting
against B directly: **B stops the key material leaving and still lets the agent
use the credential; D leaves the material in reach and stops the destination
being reachable.** They close different halves, and neither stops a compromised
agent pushing a poisoned commit through the host it is *supposed* to talk to —
which is the realistic attack once prompt injection is assumed.

The difference that decides it is cost. B creates the highest-value target in
the system: one process holding every prompt in plaintext, the real
credentials, and the CA private key. D adds a check to a component already in
the path, and introduces no new secret material anywhere.

**None of A–D make the agent stop holding a credential.** B comes closest and
only for the bytes. Recorded so the next person reading this does not conclude
that a cheap option was overlooked, or that an expensive one is a solution.

### Rank them by what a leak costs, not by how well it is prevented

The options above were first weighed on how well each keeps a secret *in*. That
is the wrong axis. Prompt injection is the threat model, so a leak is assumed
rather than avoided, and the question that decides the design is: **when a
secret leaves the container, how much damage is possible, for how long, and
across how many things?**

Four factors: **scope**, **lifetime**, **breadth**, **recoverability**.

Today's default credential is the worst possible shape on all four. It is an
OAuth refresh token, so its scope is the whole account; it has no natural
expiry; the persisted HOME holding it is *the same directory in every project*
(item 8), so one repository's compromise is every repository's; and recovery
needs a human to notice and revoke.

| option | scope | lifetime | breadth | worst case |
|---|---|---|---|---|
| today | whole account | until revoked | every project | account taken over, silently |
| B | none in container | — | — | **the proxy**: every token, every prompt in plaintext, the CA key |
| D | unchanged | unchanged | unchanged | same as today; D reduces opportunity, not consequence |
| prod | what you forwarded | yours to choose | one run | bounded by your own decision |
| C | one action | minutes | one run | a token that has already expired |

Two conclusions that reverse the earlier reading:

**B is the worst option on this axis, not the best.** It makes the usual case
harmless and the tail catastrophic: one process holding every token, the CA
private key, and every prompt in plaintext. Compromise it and the loss is not
one credential but all of them, plus the ability to impersonate any TLS server
to the container. Trading a frequent small loss for a rare total one is a real
strategy, but it should be chosen deliberately and not because B sounded
strongest per-token.

**C is the best**, because it is the only one that attacks consequence
directly. The leak still happens; the thing that leaks is worth almost nothing
by the time anybody uses it.

### The combination worth building, and in what order

Nothing here needs a CA, a plaintext chokepoint, or a new place for secrets to
accumulate. In order of what it buys per unit of work:

1. **`--profile prod`.** Removes the account-wide, never-expiring,
   cross-project credential from the container entirely — `PersistAuth=false`,
   enforced by `ValidateProfile`. It also empties the egress baseline, so the
   permitted set is exactly what you typed rather than a list that includes
   github.com by default. Cost is convenience: authenticate per run, no history
   sync, name every domain.
2. **Short-lived brokered secrets (C).** `secrets:` already runs a `Command`, so
   this is configuration today. What is missing is anything that *encourages*
   it — someone writing `gh auth token` gets a months-long credential believing
   they brokered one. The smallest useful change is a warning when a brokered
   value looks long-lived, not a new mechanism. **Built** (`creds.Classify`); see
   the decision at the top of this item for what it does and does not claim.
3. **A distinct credential per project.** The breadth multiplier is item 8's
   shared HOME; under prod that HOME is gone, but a hand-forwarded token shared
   between projects reintroduces it. Fine-grained per-repository tokens make one
   leak reach one repository.
4. **A minimal `--allow` list.** Free under prod, since the baseline is already
   empty. Fewer permitted hosts is fewer places a leaked secret can be posted —
   the useful half of D, without building D.

The realistic worst case that leaves: *a ten-minute token, scoped to one
repository, leaked from one run.* Rotate it, or wait.

What it does not address, and neither does B: **DNS exfiltration** (see the
bottom of this file) and **misuse of a credential the agent legitimately
holds**. Those are the two that survive every option here, and they are the
argument for keeping the authority small rather than the secret hidden.

## 3. The agent writes `.git/config` and `.git/hooks` — **DONE**

**Closed**, with the split the design call chose: prevent what has no legitimate
use, report what does.

- **`.git/hooks` is mounted read-only** over the read-write workspace, at the
  container path (and for a worktree run, over the parent repository's hooks at
  its host path, since a linked worktree runs the common directory's hooks).
  Planting a hook now fails with `Read-only file system`. Verified that ordinary
  work is untouched: `git config`, `git add` and `git commit` all still succeed.
- **`.git/config` stays writable and is watched.** It is recorded when the run
  starts and the difference reported when it ends — before the user next runs git
  there. Keys whose values git *executes* (`core.fsmonitor`, `core.hooksPath`,
  `core.sshCommand`, `credential.helper`, …) are marked and sorted first, so the
  news that matters is not buried under a renamed remote:

```
sandbox-cli: the agent changed this repository's git config:
  ! core.fsmonitor = /workspace/.git/evil
    user.nickname = bob
  the lines marked ! name a program your own git will run; review before using git here
```

Config was not made read-only because agents legitimately run `git config`, and
breaking that costs more than it gains once hooks — the vector with no honest use
— are already sealed.

<details><summary>Original entry</summary>

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

</details>

---

## 4. Sandboxes are not isolated from each other — **DONE**

**Closed.** Every sandbox now joins one shared docker network created with
`com.docker.network.bridge.enable_icc=false`, instead of the default bridge where
every container can reach every other on any port.

```
peer sandbox -> another sandbox:9000    blocked (TimeoutError)
egress                                   works
published port from the host             works
allowlist mode                           works
```

One shared network rather than one per run, which is the point: a per-run network
is a docker object that outlives a crash, so it would have reintroduced exactly
the orphaned-state cleanup this tool is otherwise free of. `sandbox-cli clean`
reaps it once no sandbox containers remain.

`network: none` still means none — asking for no network does not quietly get one.

**Residue:** the bridge *gateway* is still reachable in default mode, where no
firewall runs. That is the other half of item 5's residue; closing it would mean
every run taking the root-entrypoint path with `NET_ADMIN`, which is a worse
default than the one it would protect.

<details><summary>Original entry</summary>

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

</details>

---

## 5. `host.docker.internal` resolves without `--host-gateway` — **DONE**

**Closed.** Without the flag, `host.docker.internal` and `gateway.docker.internal`
are now mapped to the container's own loopback, so the documented and
discoverable route to the host resolves to nothing useful. With `--host-gateway`
the behaviour is unchanged, which is how an agent reaches an MCP server on the
host.

Confirmed against a service bound to `127.0.0.1:8931`:

```
default mode, no flags      blocked        (was: read the file)
--host-gateway              reads the file (unchanged, opt-in)
allowlist mode              blocked        (already, via the firewall and proxy)
```

An explicit `--add-host host.docker.internal:…` still wins: `/etc/hosts` takes the
first match, so sandbox-cli adding its own entry for a name the caller mapped
would silently discard what they asked for.

**Residue:** in default mode there is no firewall, so the gateway's raw *address*
is still reachable. Blocking the name closes the discoverable path; closing the
address needs the default-mode work in item 4.

<details><summary>Original entry</summary>

**Severity: medium. Effort: small. Blocked on: nothing.**

`spec.go` treats `host.docker.internal` as something `--host-gateway` opts into.
On Docker Desktop it resolves unconditionally. Confirmed: a sandbox with no flags
read a host service bound to `127.0.0.1:8931` — a service the user bound to
loopback *specifically* so nothing else could reach it.

The absence of the flag is not a defence on macOS. Either block the gateway
address by default (and let `--host-gateway` unblock it), or stop describing it
as opt-in. The first is the honest reading of what the flag is for.

</details>

---

## 6. No seccomp profile is applied, and resource limits are unbounded — **DONE**

Two halves, two different answers.

**Seccomp: now reported.** sandbox-cli ships no profile of its own — docker's
default is good, and maintaining a custom one is a large ongoing cost for a small
gain — so `Seccomp: ""` means "whatever the daemon does". The daemon may do
nothing, silently: on the machine where this was found, `docker info` said
`profile=unconfined`, a container showed `Seccomp: 0`, and `unshare -r` gave uid
0. Every claim about hardening still read as true while one layer was simply
absent. A run on such a daemon now says so, once, with the setting to change:

```
sandbox-cli: this docker daemon applies no seccomp profile, so the container has the full syscall table
  the other hardening still applies (non-root, cap-drop, no-new-privileges), but this layer is absent
  Docker Desktop: Settings > Docker Engine, remove "seccomp-profile": "unconfined"
```

Reported rather than refused: seccomp being off is a property of the user's
docker installation, fixable in its settings. Refusing would make the tool
unusable on a machine that is merely configured badly — a different trade from
the firewall's fail-closed rule, where the thing that failed was something
sandbox-cli itself had asked for.

**Resource limits: unchanged, deliberately.** Memory and CPU stay unbounded by
default. The reasoning already in `config.go` holds: agents legitimately spike
memory during builds and test runs, and an OOM-kill mid-task destroys work in a
way an unbounded-but-observed container does not. `--pids-limit 1024` remains the
one default guard, because a fork bomb has no legitimate version.

For untrusted work, ask for limits explicitly:

```sh
sandbox-cli run --memory 4g --cpus 2 -- ...
```

or set `security.memory` / `security.cpus` in your own config. Note the exposure
this leaves: an agent can exhaust host RAM and CPU, and can fill the Docker
Desktop disk image — a sparse file on the real disk that does **not** shrink when
the container is `--rm`'d. `docker system df` is where that shows up.

<details><summary>Original entry</summary>

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

</details>

---

## 7. Denial logging is unreadable on macOS — **DONE**

Substantially closed by the name-enforcing proxy, which arrived after this item
was written and changed what the gap actually is.

**Now visible on screen, on every platform**, because the proxy writes to stderr
and docker carries it:

```
sandbox-cli: egress DENY gist.github.com:443 (not on the egress allowlist)
sandbox-cli: egress DENY :0 (connection carries no hostname to check)
```

The second line is a connection straight to an address with no name — the obvious
way to try to evade a name-based allowlist, and previously silent.

Since all of an agent's HTTP and HTTPS is redirected through the proxy, that
covers essentially every denial worth seeing.

**Still invisible: denials that never reach the proxy.** Only tcp/80 and tcp/443
are redirected; anything else meets the address rules and the final `REJECT`,
whose `LOG` lands in the VM's kernel buffer under Docker Desktop. So a blocked
connection on, say, tcp/9999 shows the agent a refusal and tells the user nothing.

**Also added:** the run log now records *which regime* enforced a run —

```json
{"network":"allowlist","egress_enforcement_requested":"name", …}
```

`network: allowlist` alone could not distinguish an address-matched run (which
permitted every host sharing an allowlisted address) from a name-matched one.
After the fact, that is the difference that matters.

**The remainder is now done.** The proxy's denial lines are counted on their way
past and recorded in the run log, so `sessions.jsonl` answers "did this run try to
reach something it was refused?" without scrollback:

```json
{"network":"allowlist","egress_enforcement_requested":"name",
 "egress_denied_reported":4,
 "egress_denied_hosts_reported":["gist.github.com","docs.python.org"], …}
```

Three things about the shape, all deliberate:

- **Named for a report, not a fact.** The lines arrive on the container's stderr,
  which the agent writes to as well, so any process in the sandbox can print one —
  and can bury real ones in noise. `runtime.TestDenialsAreForgeableByTheGuest`
  asserts that limitation on purpose; when a channel the guest cannot write to
  exists, the field gets renamed in the same change. That is roadmap
  [task 4](../roadmap/task-4-run-provenance.md), and it is the first required
  feature there.
- **Absent rather than zero** where the question was never asked: a run with no
  allowlist has no proxy to refuse anything, and a detached run has nothing on the
  host holding its stderr.
- **Bounded**, because both inputs are the guest's to choose. The count is exact;
  the host list is deduped and capped, so a loop over generated names cannot
  decide how large one JSONL line gets.

What remains genuinely open is the same thing as before, and it is smaller: a
denial on a port other than 80 or 443 never reaches the proxy, so it is still
counted by nothing and logged only to the VM's kernel buffer.

<details><summary>Original entry</summary>

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

</details>

---

## 8. Cross-project persistence channels — **narrowed; the rest is documented**

The sharpest edge is fixed. The persisted agent HOME
(`~/.config/sandbox/agents/<name>`) is the *same* directory in every project and
is writable by the agent — and the bootstrap **prepended** `$HOME/.local/bin` to
`PATH`. So an agent compromised in one repository could drop a file named `git`,
`node` or `sh` there and shadow that command in every future session, in every
project.

Demonstrated, and fixed:

```
prepend (old):  git --version  ->  HIJACKED-GIT
append  (now):  git --version  ->  git version 2.39.5   (/usr/bin/git)
                agent binary   ->  still the persisted, self-updating one
```

The directory is now appended, and the agent is exec'd from it by absolute path —
so it keeps self-updating (the reason it lives there at all) while system
binaries win.

**What remains, deliberately:**

- **The agent's own binary is still replaceable.** It has to be: the whole point
  of the persisted HOME is that Claude Code updates itself between runs, so
  pinning a hash would break the feature it exists for. An agent that overwrites
  `.local/bin/claude` still affects future `sandbox-cli claude` runs.
- **The stored credential lives there** (`.claude/.credentials.json`, `0600` on
  the host). Each wrapper mounts only its own directory, so agents cannot read
  each other's — verified — but a compromised agent can read its own.
- **`--cache` volumes are shared across agents and projects** by design, so
  content planted in one is consumed at build time in another. Opt-in, and the
  cost of making them per-project is the reason they exist.
- **`--share` is a cross-project channel, and `--share-name` does not partition
  it.** The shared directory (`~/.config/sandbox/shared`) is mounted read-write
  into every sandbox using a bare `--share`, so **its contents are
  attacker-controlled**. Two escapes through that were found and fixed (below);
  what remains is stated here rather than only in a README the container can
  rewrite:
  - A namespace prevents *collisions*, not access. Any bare-`--share` peer reads
    and writes every namespace beneath the root. It is not an isolation
    boundary, and the docs no longer imply it is.
  - **TOCTOU between the identity check and docker's mount.** `shareNamespaceDir`
    resolves the namespace, refuses a symlink, and asserts the resolved path *is*
    the namespace directory — then returns a path string that docker resolves
    again when it mounts. A concurrently running bare-`--share` container can
    `rmdir` the leaf and replace it with a symlink in that window, redirecting
    the bind mount past both the identity check and `RefuseUnsafeHostPath`.
    Not cheaply closable: docker takes a path, not an fd, so there is no handle
    to pass and no way to make check and use atomic. Mitigated by returning the
    *resolved* path (removing the symlinked-leaf variable at check time) and
    bounded by the fact that the attacker must already have `--share` and be
    running concurrently. **Residual, accepted, recorded.**

  Fixed in the same change: a relative in-root symlink (`ln -s . NAME`) resolved
  to the shared root and mounted every namespace; and the seeded `README.md` was
  written with Stat-then-write, so a dangling symlink was followed and created a
  host file at a container-chosen path outside the shared directory.

Use `--no-persist-auth` for a genuinely untrusted run: the HOME is then ephemeral
and none of the above applies, at the price of logging in again. For `--share`,
the equivalent is not passing it — it is off by default for this reason.

<details><summary>Original entry</summary>

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

</details>

---

## 9. Agents are downloaded from vendor hosts on first run — **narrowed**

**Severity: medium. Effort: small for the half that is done; large for the rest.**

Eleven of the fifteen wrappers install their agent on first run, from a vendor
host, into the persisted agent HOME — which is the same directory in every
project, is writable by the agent, and holds its stored credential (item 8).
Until recently each of those installs resolved *whatever the vendor served that
day*: `npm install -g <pkg>` with no version, a `curl | bash` whose script is
regenerated per release, a GitHub API call for "latest".

This was not on the list before the July 2026 OpenAI disclosure made the shape of
it concrete. Their models escaped an isolated evaluation environment through
zero-days in a **self-hosted package-registry proxy that was part of that
environment**, then escalated and moved laterally to an internet-reachable node.
The support infrastructure of a sandbox is inside the sandbox's trust boundary,
and ours includes npm.

**Closed:** the first-run version is now pinned in one table
(`internal/agents/pins.go`), announced as it installs, and enforced by
`TestEveryLazyInstalledAgentIsPinnedOrSaysWhy` — a new adapter with no recorded
pin fails rather than shipping. `openhands` no longer resolves "latest" at run
time at all. That closes the ordinary case: a hijacked or typosquatted *publish*
does not reach a sandbox until someone bumps a line here.

**Still open, and it is the harder half:**

- **A compromised registry** can serve different bytes for a version it already
  published. Closing that needs integrity hashes, which the npm CLI verifies only
  from a lockfile and a `-g` install has none of. No cheap fix; recorded rather
  than solved.
- **`curl | bash` installers** (`cursor`, `goose`, `claude`, `devin`) execute
  arbitrary remote shell inside the container. The
  blast radius is the sandbox and the persisted HOME, which is the radius item 8
  already describes — but the code is fetched fresh each first run and is not
  pinnable for three of them.
- **`cursor`, `claude` and `devin` are deliberately unpinned**, with the reason
  recorded in the table rather than left implicit. Devin's is the newest and the
  most recoverable: its installer *does* honour a pinned version, but only through
  a versioned `cli/<version>/setup.sh` URL, and Cognition publishes no index of
  versions to choose one from. If such an index appears, this becomes pinnable.

---

## 10. The prompt is stored in a container label

**Severity: low-to-medium. Effort: small. Not yet reproduced as a leak — recorded
because it inverts a rule the rest of the system keeps.**

`sandbox.prompt` carries what the agent was asked to do, truncated to 512 bytes
(`internal/sandbox/labels.go`), and is stamped on every container that was given
one. It arrived with the Studio work, where the run list needs a line that says
which run is which.

Docker labels are not a private channel. They live in the container config, are
returned by `docker inspect` to anything that can reach the socket, and persist
for the life of the container — which for `--detach` and fleet runs is until
someone reaps it, deliberately, because the exit code and logs are the whole
supervision story.

**Why it is on this list at all:** `internal/audit` was built around the opposite
rule, and says so —

> environment variables are recorded **by name only**. The credential broker
> exists to keep secret values off the argv and out of config files, and a log is
> a file — so `SessionMeta` has nowhere to put a value, deliberately.

A prompt is not a credential, but it routinely contains one: a token pasted in to
"try this API", a connection string, an internal hostname. So the system takes
care to keep secrets out of one file and then writes them into container metadata
that outlives the run, and nothing in the design says that was weighed.

**What is not wrong today:**

- The label is read in two places (`studioapi/resolve.go` into JSON,
  `studioapi/console.go` for transcript correlation) and printed to a terminal by
  none, so it needs no `termsafe.Clean` yet. Every *other* label the session
  listing prints does go through it, precisely because label values are
  attacker-influenced text — so a future `PROMPT` column in `sandbox-cli list`
  arms that trap. `truncatePrompt` bounds length and strips nothing.
- Anything that can read these labels can already reach the docker socket, which
  is root on the host. This is not a privilege boundary being crossed; it is a
  secret being written somewhere nobody expected it and left there.

**What a fix looks like** — pick one, they are alternatives, not steps:

- Stop stamping it and have Studio read the prompt from the run's transcript,
  which it already correlates for the console.
- Keep it, and say plainly in `docs/AGENTS.md` that prompts persist in container
  metadata until reaped, so a reader can decide what to paste.
- Keep it behind a flag, off by default, on the same reasoning `--share` is a
  flag rather than a `fleet.yaml` key: the wider reach stays something you type.

Not obviously worth doing before item 2, which is the same question with real
credentials rather than incidental ones.

---

## The egress proxy is unsupervised — known, and fail-closed

The proxy is started with `&` and the entrypoint then `exec`s the guest, so
nothing restarts it. If it dies mid-session every tcp/80 and tcp/443 connection
fails closed, which is the right direction — but it fails *silently*, and the
symptom (an allowlisted host suddenly unreachable) does not name its cause.

Recorded rather than fixed because a supervisor in the entrypoint is a second
long-lived root-adjacent process, and the failure it guards against has not been
observed. If you see an allowlisted domain stop resolving part-way through a run
under `--allow`, check whether `sandbox-egress-proxy` is still alive in the
container before suspecting the allowlist.

Note also that the guest becomes PID 1 and so inherits reaping duty for the
proxy's process.

---

## Detached runs take no periodic snapshots — known, and deliberate for now

Three paths, three behaviours, and only the first is the safety net people
picture:

- A **foreground CLI run** snapshots every two minutes (`Begin`/`Start`/`Stop`).
- A **detached CLI run** snapshots not at all: `internal/cli/run.go` returns via
  `startDetached` before `beginRescue` is reached, and there is no process left
  to hold a ticker.
- A **daemon run** — every Studio run — records one **baseline** before launch
  and closes it immediately. A before-image, not a net.

So work done by a detached agent is protected by the bind mount and by whatever
snapshots somebody takes on purpose (`POST /v1/snapshots`), and not by a timer.

The fix is a snapshot loop owned by `studioapi/supervisor.go`, which already
polls running containers. It is not obviously right: a long-lived daemon writing
into the user's repository on a schedule is a different proposition from a
foreground command doing it for the length of one run, and the supervisor's own
watch set is in memory, so a restart would silently stop protecting runs that are
still going. Weighed and deferred rather than missed.

Fixed alongside this, and worth recording because the failure mode was the quiet
kind: `POST /v1/runs/{id}/recover` used to find the *baseline* for a Studio run —
same branch, same agent, most recent — and restore the state the run started
from. Not an error; a restore that looked like it worked. Baselines are now
excluded there and from the snapshot listing.

## Mirroring snapshots to S3 moves the working tree off the machine

`snapshot.s3` uploads a git bundle of the workspace to object storage. That is
the whole point of it and it is also, plainly, the repository's contents leaving
the host — so the decisions around it are written down rather than assumed.

**The key is named, not held.** `access_key_env:` is the name of an environment
variable read on the daemon's machine. There is no field anywhere in this feature
that takes a secret *value*: not the config file, not Studio's settings file, not
the API response, not the browser. The daemon reports whether the named variable
resolves, and nothing more. This is the same shape `gateway:` uses and the same
reason `audit.SessionMeta` has nowhere to put a value.

**The whole `snapshot` key is refused from a project `.sandbox.yaml`**, and `s3`
would be refused on its own merits: it names a network destination *and* which of
this machine's credentials is read, so a hostile repository that could set it
would be handed an exfiltration target, the means to authenticate to it, and the
working tree already bundled for the trip. That it holds no secret value is not a
mitigation — naming somebody else's variable is the attack.

**A returned bundle is verified twice, and the second check is the one that
matters.** `git bundle verify` establishes that a bundle is internally
consistent; it would pass just as happily on a well-formed bundle of somebody
else's commit, served under this key by a bucket that was tampered with, shared
by mistake, or written by another machine using the same prefix. So `Fetch` also
compares the fetched sha against the one the local manifest recorded when the
snapshot was taken, and rolls the ref back on a mismatch. A restore that quietly
returns the wrong tree is the failure this feature was built in the shadow of.

**The connectivity check asks about the daemon's own configuration, never the
request's.** `POST /v1/snapshots/s3/check` takes no bucket and no endpoint. One
that did would be a server-side request forgery with a Test button in front of
it: the daemon signing a request to any host a caller names and reporting whether
it answered.

Two costs accepted rather than solved:

- **Retention does not reach the bucket.** The windows prune the local ref;
  objects in S3 are left to the bucket's own lifecycle rules. Deleting somebody's
  off-machine backup on a timer that only runs while their laptop is open is a
  way to lose the copy that was supposed to survive the laptop — but it does mean
  storage grows until a lifecycle rule is configured, and the Studio card says so
  rather than leaving it to be discovered from a bill.
- **The bundle is self-contained**, so it carries history from the root and is
  sized like a clone rather than like a diff. That is why `upload: manual` is the
  default and why `max_object_mb` refuses a bundle up front: this client does no
  multipart, and S3 caps a single PUT at 5 GiB.

---

## Snapshot provenance is a scoping rule, not a boundary

A snapshot records whether it was taken through Studio (`run`) or the SDK
(`sdk`), derived from whether the request carried an `Origin` header, and the
daemon refuses a browser-origin restore of an SDK-made snapshot.

**This is not a security control and must not be counted as one.** Anything able
to omit a header — curl, a script, another local process — can restore anything.
What actually governs who may call this API at all is the loopback binding, the
`Origin` refusal in `guard.go`, and the bearer token.

What the split buys is that the two surfaces do not silently undo each other's
work: a pipeline's checkpoints are not restorable by somebody clicking in a tab
who cannot see what the script was doing halfway through. It is a usability
boundary wearing an enforcement mechanism, and it is written down here so nobody
later reads the 403 as protection.

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
