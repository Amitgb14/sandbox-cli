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
- [Why the two demands conflict](#why-the-two-demands-conflict)
- [The options](#the-options)
- [What #88 should ship as](#what-88-should-ship-as)
- [A fourth possibility — now half-measured](#a-fourth-possibility--now-half-measured)
- [How to settle it](#how-to-settle-it)

## The short version

`--profile prod` requires two things:

1. **A kernel of its own** — so a kernel bug is not a way out of the sandbox.
2. **An egress allowlist** — so a leaked credential cannot be sent anywhere.

They defend different things: the first against *escape*, the second against
*exfiltration*.

On a host whose stronger runtime is gVisor, **only the first is available**. The
allowlist cannot be programmed inside gVisor. So prod, as specified, cannot run
there at all.

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

| capability | works? | the firewall uses it for |
|---|---|---|
| `-m owner --uid-owner` | **yes** | permitting the egress proxy's uid |
| nat `-N`, `-j REDIRECT` | **yes** | redirecting guest 80/443 into the proxy |
| `-m conntrack --ctstate` | **no** | accepting replies to our own connections |
| `-m state --state` | **no** | (the older spelling of the same thing) |

There is no third spelling to fall back on. gVisor's netstack simply does not
track connections.

## Why the two demands conflict

The allowlist is currently built as:

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
  is refused because the allowlist cannot be programmed inside it — gVisor has
  no connection tracking, so the accept-replies rules cannot be written. There
  is no third choice on such a host short of installing Kata or removing gVisor,
  and the run quoted above stops working the day this merges.
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

## A fourth possibility — now half-measured

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

### Still unmeasured

Two things, and the first is the one that decides whether this is a real design
or only a promising result.

1. **Ingress containment.** The probe left `INPUT` at its default (accept). The
   real design has `INPUT` default-deny, whose purpose is the demonstrated hole
   where a co-resident container dials in and data leaves on the reply path. The
   argument that uid-locked OUTPUT closes that hole on its own — an inbound
   connection cannot be answered, because the answer would be outbound from a
   uid that is denied, so the handshake never completes — is reasoning, not a
   measurement. It needs two containers on one network to settle.
2. **The full shape.** The probe permitted uid 0 and sent traffic as uid 0. The
   real arrangement permits the *proxy's* uid, runs the agent as a different one,
   and REDIRECTs the agent's 80/443 into the proxy over loopback. `REDIRECT` and
   `owner` were both measured working earlier, but not composed like that under
   gVisor.

The cheapest way to settle both is to make the conntrack rules conditional in
`sandbox-egress-setup` — omitted when the backend cannot serve them — and run a
real `--allow` run under runsc. That is the fix and the test at once.

## How to settle it

On a host with gVisor and `--net-raw` enabled, program the reduced ruleset by
hand and see whether a request completes:

```sh
docker run --rm --runtime runsc --cap-add NET_ADMIN --cap-add NET_RAW \
  sandbox-base:<tag> sh -c '
    IPT=iptables-legacy
    $IPT -P OUTPUT DROP
    $IPT -A OUTPUT -o lo -j ACCEPT
    $IPT -A OUTPUT -m owner --uid-owner 0 -j ACCEPT   # stand-in for the proxy uid
    # No conntrack rule at all, and INPUT left open.
    curl -s -o /dev/null -w "%{http_code}\n" -m 10 https://registry.npmjs.org
  '
```

A `200` means replies arrive without conntrack and the fourth option is real. A
timeout means they do not, and the decision above has to be made.

Worth running the same probe under **Kata**, which nobody has tested. Kata is a
real kernel, so conntrack should simply work — if it does, "install Kata" is a
genuine remedy and gVisor is a documented limitation rather than a case to
design around.

## Status

- Measured: gVisor's iptables surface, above.
- Fixed: backend selection ([#101](https://github.com/Amitgb14/sandbox-cli/pull/101)),
  runtime-scoped firewall probe in `doctor`
  ([#102](https://github.com/Amitgb14/sandbox-cli/pull/102)).
- Blocked on this decision:
  [#88](https://github.com/Amitgb14/sandbox-cli/pull/88) and the parts of task 3
  §2 that give prod teeth. The gate itself is verified (#89 phase 3); what is
  open is whether it ships as a refusal, as a warning, or waits.
- Half-measured: the fourth possibility. A uid rule carries a connection under
  gVisor with no conntrack at all (200 above); ingress containment and the full
  proxy shape are still untested.
- Untested: Kata.
