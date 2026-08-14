# gVisor and the egress allowlist: what was measured, and the decision it forces

Findings from running [#89](https://github.com/Amitgb14/sandbox-cli/issues/89)
on a real host with gVisor installed, and the design question they leave open.
Nothing here is decided. It is written down because the measurements cost a day
and the decision should not be made from memory.

Task 3 §1 predicted this: *"The egress firewall is the one to watch: it is
programmed as root inside the guest before the privilege drop, and a microVM
changes what that guest is."* It was right, for gVisor. Kata is untested.

- [The short version](#the-short-version)
- [What was measured](#what-was-measured)
- [The blocker is not iptables — it is DNS](#the-blocker-is-not-iptables--it-is-dns)
- [Why the two demands conflict](#why-the-two-demands-conflict)
- [The options](#the-options)
- [What #88 should ship as](#what-88-should-ship-as)
- [A fourth possibility — now measured for the sender](#a-fourth-possibility--now-measured-for-the-sender)
- [How to settle what is left](#how-to-settle-what-is-left)

## The short version

`--profile prod` requires two things:

1. **A kernel of its own** — so a kernel bug is not a way out of the sandbox.
2. **An egress allowlist** — so a leaked credential cannot be sent anywhere.

They defend different things: the first against *escape*, the second against
*exfiltration*.

On a host whose stronger runtime is gVisor, **only the first is available**. So
prod, as specified, cannot run there at all.

*Why* the second is unavailable changed as the measurements came in, and the
current answer is not the one this document started with. It is no longer the
firewall: [#108](https://github.com/Amitgb14/sandbox-cli/pull/108) builds the
allowlist without connection tracking, and it programs cleanly under gVisor. It
is **DNS** — a container on a docker user-defined network cannot resolve a
hostname under gVisor, with or without a firewall, so there is nothing for an
allowlist to permit. That is not a limitation sandbox-cli can code around.

## What was measured

Rocky Linux 10.2, Docker with the containerd shim, gVisor installed via
`runsc install`. Full log on [#89](https://github.com/Amitgb14/sandbox-cli/issues/89).

**gVisor gates iptables behind a flag its installer leaves off.** With a stock
`runsc install`, no iptables backend works at all:

```
iptables: FAILS          ip6tables: FAILS
iptables-legacy: FAILS   ip6tables-legacy: FAILS
```

After `runsc install -- --net-raw`, the *legacy* backend works and the nft one
does not — Debian points bare `iptables` at nft, which is why the entrypoint
appeared to fail outright:

```
iptables: FAILS          ip6tables: FAILS          ← nft
iptables-legacy: ok      ip6tables-legacy: ok      ← legacy
```

That half is fixed: [#101](https://github.com/Amitgb14/sandbox-cli/pull/101)
picks the backend the kernel will serve. The `--net-raw` requirement is a host
prerequisite no code change can remove.

**What gVisor's iptables can and cannot do**, with `--net-raw` on:

| capability | works? | how it was established | the firewall uses it for |
|---|---|---|---|
| `-m owner --uid-owner` | **yes** | two-sided: allowing the running uid reached the network, allowing a different one was blocked | permitting the egress proxy's uid |
| nat `-N`, `-j REDIRECT` | **yes** | behavioural: a listener on 127.0.0.1 accepted a redirected connection | redirecting guest 80/443 into the proxy |
| `-m conntrack --ctstate` | **no** | rule insertion refused | accepting replies to our own connections |
| `-m state --state` | **no** | rule insertion refused | (the older spelling of the same thing) |
| `-m limit` | **no** | rule insertion refused | rate-limiting the log of denied packets |
| `-j LOG` | **no** | rule insertion refused | logging denied packets |
| `-j REJECT` | **yes** | rule insertion accepted | denying with an immediate error rather than a hang |

There is no third spelling of conntrack to fall back on. gVisor's netstack simply
does not track connections.

Two of these rows were wrong in the first version of this document, both in the
same way: the rule was **inserted** and that was recorded as the capability
working. Insertion is not behaviour — gVisor's iptables accepts rules its
netstack will not act on. `REDIRECT` was recorded from an insertion test and
later confirmed behaviourally (it does work); `owner` was too, and is confirmed
above. The lesson is in the third column, which is why the third column exists.

One trap when reading a chain back: `iptables-legacy -S` renders the working
owner rule as

```
-A OUTPUT -m owner [unsupported revision] -j ACCEPT
```

That string is a **display artefact** — iptables-legacy asking gVisor about a
match revision it reports differently — and not a statement about whether the
rule matches. It does match. Do not re-derive a capability from a listing; the
whole table above is behavioural for the rows that matter.

## The blocker is not iptables — it is DNS

Once the conntrack rules were made conditional
([#108](https://github.com/Amitgb14/sandbox-cli/pull/108)) the firewall
programmed cleanly under gVisor: all three degradation notices printed, the
proxy started, the chains were exactly as intended. The request still failed,
with `curl` reporting:

```
* Could not resolve host: registry.npmjs.org
```

**The container cannot resolve names under gVisor, and no firewall is involved.**
The control run proves it — no allowlist, no firewall, nothing programmed:

```console
$ sandbox-cli run --runtime runsc --network default -- \
    sh -c 'cat /etc/resolv.conf; getent hosts registry.npmjs.org && echo RESOLVED'
nameserver 127.0.0.11
options ndots:0
# ExtServers: [host(75.75.75.75) host(75.75.76.76) ...]
```

No `RESOLVED`. The lookup fails on its own.

`127.0.0.11` is docker's **embedded resolver**, which docker uses on
*user-defined* networks and implements with NAT plumbing inside the container's
network namespace. gVisor's netstack does not carry that plumbing — the
container's `nat OUTPUT` contains only sandbox-cli's own jump, with docker's
DNAT rules for `127.0.0.11` absent entirely. So the address is there in
`resolv.conf` and nothing answers on it.

sandbox-cli always runs its containers on its own network, so **every**
sandbox-cli run under gVisor hits this, allowlist or not.

### Why this was invisible for so long

Every hand-written probe in this document used `docker run` with no `--network`,
which lands on the **default bridge** — where `resolv.conf` lists the host's real
resolvers and DNS works normally. That is why probe after probe returned `200`
while the real thing failed. The one network shape the probes never used is the
only one sandbox-cli uses.

This is also a direct vindication of the `container DNS` check added in
[#106](https://github.com/Amitgb14/sandbox-cli/pull/106): it compares a
sandbox-shaped network against the engine's default, and that differential *is*
this failure. It would have reported `DNSSandboxBroken` in one line. It does not
catch it yet only because the DNS probe still runs on the engine's default
runtime — it needs the same per-runtime threading
[#102](https://github.com/Amitgb14/sandbox-cli/pull/102) gives the firewall
probe. That is a small, well-defined follow-up.

Whether this is gVisor's limitation in general or specific to this host's
docker/gVisor pairing is not established. It was measured on one host.

## Why the two demands conflict

*This section describes the ruleset as it stood before
[#108](https://github.com/Amitgb14/sandbox-cli/pull/108), which is what made the
conflict look like a conntrack problem. Kept because the reasoning is what led
to the conditional ruleset.*

The allowlist was built as:

```
OUTPUT  -j DROP                                  (default)
OUTPUT  -o lo                          -j ACCEPT
OUTPUT  -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT   ← unavailable
OUTPUT  -m owner --uid-owner <proxy>   -j ACCEPT
nat     REDIRECT tcp/80,443 → proxy

INPUT   -j DROP                                  (default)
INPUT   -i lo                          -j ACCEPT
INPUT   -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT   ← unavailable
```

The two `conntrack` lines are what let a reply come back to a connection the
sandbox opened. Without them the proxy can send but never receive, so no traffic
completes and the allowlist cannot function.

sandbox-cli fails closed here, which is correct: a run that asked for an
allowlist and could not program one **refuses to start** rather than running
unfiltered. The consequence is that prod + gVisor refuses.

## The options

**Give up the gate.** Run on gVisor with no egress filtering. **Rejected.** It
fails open on the control that protects credentials, and contradicts the
project's stated rule that an unprogrammable allowlist refuses rather than
degrades. Recorded only so nobody proposes it again.

**Give up the kernel demand, loudly.** Where selecting the stronger runtime
would make the allowlist unenforceable, prod does not demand it: the run
proceeds on runc with the allowlist intact, and both `doctor` and the run say
that a stronger runtime was available and why it was not used.

- *For:* never fails open; keeps prod usable; the weaker boundary is reported
  rather than silently taken.
- *Against:* prod's promise about the kernel becomes conditional, and a promise
  with a condition is one people misremember.

**Refuse, with an explicit acknowledgement.** prod keeps demanding a kernel and
refuses such hosts. A new key (`security.shared_kernel_ack: true` or similar)
lets an operator accept a shared kernel deliberately.

- *For:* strictest reading; nothing is waived without someone saying so.
- *Against:* it adds a way to weaken prod, and every such key is one a hostile
  repository will eventually try to set — so it would need to join the refused
  list in `internal/config/trust.go`, with the usual argument about which layers
  may set it.

**Demand only runtimes that can filter.** The kernel demand applies to runtimes
proven able to program the firewall; gVisor does not count as "a boundary is
available" until it can.

- *For:* no new config, no waiver, no conditional promise — the gate's evidence
  just gets stricter about what counts as evidence.
- *Against:* it needs a firewall probe *per runtime* on the run path, which is a
  container start, not a `docker info` read. `doctor` can afford that; a run
  cannot do it on every launch.

## What #88 should ship as

The options above are about what prod *does*. This is the narrower question in
front of us. #88 adds the kernel demand and is **not merged**, so today prod has
no opinion about runtimes at all: it runs on runc with the allowlist, which
works — verified on the gVisor host itself:

```console
$ sandbox-cli run --profile prod --allow api.anthropic.com -- echo "Hi"
sandbox-cli: egress enforced by name (proxy on 127.0.0.1:3128)
sandbox-cli: egress proxy on 127.0.0.1:3128 enforcing 1 name(s)
Hi
```

So merging #88 turns a working run into some other outcome. Which one is the
decision.

### Ship it as a refusal

prod demands a kernel of its own. A host with a stronger runtime registered and
nothing selecting it is refused, non-zero exit, as with seccomp and the firewall.

- **For:** the rule means what it says. An unattended run on a shared kernel is
  what the profile exists to prevent, and a demand that yields when inconvenient
  is not a demand.
- **Against — this is the gVisor limitation biting directly:** on a host whose
  only stronger runtime is gVisor, prod becomes **unusable**. Not degraded,
  unusable. Not selecting runsc is refused for not selecting it; selecting runsc
  gives a container that cannot resolve a hostname, so the allowlist has nothing
  to permit and the agent reaches nothing. There is no third choice on such a
  host short of installing Kata or removing gVisor, and the run quoted above
  stops working the day this merges.

  The *reason* in that bullet changed once #108 landed the conditional ruleset:
  it used to be "the allowlist cannot be programmed inside gVisor, because there
  is no connection tracking." It can now be programmed. The obstacle moved to
  DNS, which is both more fundamental — it defeats a plain `--network default`
  run with no firewall at all — and not ours to fix. A refusal message should
  name DNS, not conntrack.
- Needs a deliberate escape hatch designed alongside it, or the first person to
  hit this invents one. Any such key is privileged and belongs on
  `internal/config/trust.go`'s refused list.

### Ship it as a warning

prod reports that a stronger runtime was available and unused, and runs.

- **For:** preserves the behaviour verified above; makes visible a gap that is
  currently invisible; and the demand can be tightened to a refusal later, once
  a host exists that satisfies *both* requirements. A warning naming a real gap
  is worth more than a refusal nobody can satisfy.
- **Against:** prod's stated promise is that it refuses where dev warns — that
  asymmetry is the profile's whole point, and an exception to it is a precedent.
  A warning in an unattended run goes into a log nobody reads, which is the
  argument the profile was built on.
- The gVisor limitation has to be stated in the warning itself, or it misleads:
  "a stronger runtime is available" would be true only in the sense that it is
  registered, and acting on that advice trades the allowlist for the kernel.

### Hold it

Neither, until the untested possibility below is checked, or Kata is measured on
a real host.

- **For:** a refusal only makes sense once some host can satisfy both demands,
  and none of the measured ones can. Merging now turns a working setup into a
  broken one to enforce a rule nothing can currently meet. If the allowlist can
  be rebuilt without connection tracking, the conflict disappears and the demand
  ships as a refusal at no cost.
- **Against:** #88 is written and reviewed, and holding finished work has its own
  cost.

## A fourth possibility — now measured for the sender

The conflict may be an artefact of how the allowlist is written rather than a
property of gVisor. **This is reasoning, not a measurement.**

The two unavailable rules exist to admit *replies*. But the OUTPUT chain already
has a stricter rule beside them: only the proxy's uid may send anything. If
OUTPUT is uid-based rather than state-based, then:

- the proxy's own packets — including its replies within a connection it opened
  — are permitted by the `owner` match, which **does** work under gVisor;
- nothing else in the container can send anything outward at all;
- so an attacker connecting *inward* gets no reply, because the reply would be
  outbound from a uid that is denied — the TCP handshake cannot even complete;
- which means the INPUT chain may not need to filter at all, and the
  co-resident-container exfiltration hole the INPUT chain was added to close may
  be closed by the OUTPUT rule instead.

If that holds, gVisor gets both the strong wall *and* the allowlist, and the
decision above is moot.

What would disprove it: replies to the proxy's own outbound connections arriving
on INPUT and being dropped, or `owner` matching failing for a socket whose
packets are generated by the kernel rather than by the process (TCP ACKs,
retransmits, ICMP). That last one is the likeliest failure and is exactly what a
test would find out.

### Measured: the uid rule is enough for the sender

Rocky Linux 10.2, gVisor with `--net-raw`, `OUTPUT` at default-DROP, no
connection tracking anywhere:

```console
$ docker run --rm --runtime runsc --cap-add NET_ADMIN --cap-add NET_RAW \
    sandbox-base:0.0.1-ed5b8ed3 sh -c '
      iptables-legacy -P OUTPUT DROP
      iptables-legacy -A OUTPUT -o lo -j ACCEPT
      iptables-legacy -A OUTPUT -m owner --uid-owner 0 -j ACCEPT
      curl -s -o /dev/null -w "%{http_code}\n" -m 10 https://registry.npmjs.org'
200
```

A complete HTTPS request: DNS lookup, TCP handshake, TLS, response. So the
likeliest objection is answered — **kernel-generated packets do match `-m owner`
under gVisor**, and a uid rule carries a whole connection without the
accept-replies rule the current design leans on.

The allowlist therefore does not need connection tracking in principle. It needs
it the way it is currently written.

### The conditional ruleset was built, and it works

[#108](https://github.com/Amitgb14/sandbox-cli/pull/108) makes the conntrack
rules conditional in `sandbox-egress-setup`: a generic `rule_ok` probe writes
each candidate rule into a scratch chain and the ruleset is assembled from what
the backend will actually serve. Where conntrack is absent the accept-replies
rules are omitted and `INPUT` is left accepting, since its default-deny form
depends on them.

Under gVisor the firewall now programs completely — the three degradation
notices print, the chains render as designed, and the proxy starts. Under runc
nothing changes: same backend, no notices, `200`, so the fallback costs the
common path nothing.

What it does **not** do is make the allowlist work under gVisor, because the
guest cannot resolve names there for reasons that have nothing to do with the
firewall. See [the DNS section](#the-blocker-is-not-iptables--it-is-dns). The
change stands on its own — a firewall that degrades to what the backend supports
is right regardless — but it cannot be verified end to end on gVisor until DNS
is solved.

### Still unmeasured

1. **Ingress containment.** The probe left `INPUT` at its default (accept). The
   real design has `INPUT` default-deny, whose purpose is the demonstrated hole
   where a co-resident container dials in and data leaves on the reply path. The
   argument that uid-locked OUTPUT closes that hole on its own — an inbound
   connection cannot be answered, because the answer would be outbound from a
   uid that is denied, so the handshake never completes — is reasoning, not a
   measurement. It needs two containers on one network to settle, and it matters
   more now: without conntrack, #108 leaves `INPUT` accepting under gVisor, so
   the uid rule is the *only* thing standing where the INPUT chain used to.
2. **The composed shape.** `owner` and `REDIRECT` are each confirmed
   behavioural under gVisor, but never composed — proxy uid permitted, agent on
   a different uid, agent's 80/443 redirected into the proxy over loopback. DNS
   fails before that composition is exercised, so it stays untested.
3. **Whether the DNS failure is gVisor's or this host's.** One host, one
   docker version.

## How to settle what is left

The iptables question is settled: the ruleset can be built without conntrack,
and #108 builds it. Three things remain, in the order they are worth doing.

**1. Is the DNS failure gVisor's, or this host's?** Cheapest first — run any
container, not sandbox-cli, on a user-defined network under runsc:

```sh
docker network create probe-net
docker run --rm --runtime runsc --network probe-net alpine \
  sh -c 'cat /etc/resolv.conf; getent hosts example.com && echo RESOLVED'
docker network rm probe-net
```

No `RESOLVED` means it is gVisor's embedded-DNS gap and not anything sandbox-cli
does — which is what the evidence so far says, since the control run used
`--network default` and no firewall.

**2. Ingress containment without conntrack.** Two containers on one network:
one running the reduced ruleset with `INPUT` accepting, the other dialling in.
If the handshake never completes — because the reply would be outbound from a
denied uid — then uid-locked OUTPUT closes the co-resident hole on its own and
#108's `INPUT`-accepting fallback is sound. If data comes back, the fallback is
a hole and needs a different answer.

**3. Kata, which nobody has tested.** Its value rose with these findings: a real
kernel should have conntrack *and* working DNS, so if it does, "install Kata" is
a genuine remedy and gVisor becomes a documented limitation rather than a case
to design around.

## Status

- Measured: gVisor's iptables surface, above — behaviourally for the rows that
  decide anything.
- Measured: **DNS does not work under gVisor on a docker user-defined network**,
  which is what actually blocks the allowlist there. Independent of the
  firewall.
- Fixed: backend selection ([#101](https://github.com/Amitgb14/sandbox-cli/pull/101)),
  runtime-scoped firewall probe in `doctor`
  ([#102](https://github.com/Amitgb14/sandbox-cli/pull/102)), conntrack-optional
  ruleset ([#108](https://github.com/Amitgb14/sandbox-cli/pull/108)).
- Follow-up: thread the selected runtime through `doctor`'s DNS probe as #102
  does for the firewall probe, so this failure is reported rather than
  investigated.
- Blocked on this decision:
  [#88](https://github.com/Amitgb14/sandbox-cli/pull/88) and the parts of task 3
  §2 that give prod teeth. The gate itself is verified (#89 phase 3); what is
  open is whether it ships as a refusal, as a warning, or waits. The measurements
  now point at a refusal: a gVisor host cannot satisfy prod's second demand for
  a reason no code change of ours removes.
- Still open: the fourth possibility. The uid rule carries a connection under
  gVisor with no conntrack at all, and `owner`/`REDIRECT` are individually
  confirmed — but ingress containment and the composed proxy shape are untested,
  and DNS prevents testing the latter today.
- Untested: Kata. Its value rose: a real kernel should have both conntrack and
  working DNS, which would make "install Kata" a genuine remedy and gVisor a
  documented limitation rather than a case to design around.
