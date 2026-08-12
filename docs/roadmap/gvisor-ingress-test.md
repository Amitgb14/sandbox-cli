# gVisor — ingress containment under `--allow`

**Question.** Under gVisor the container's `INPUT` chain cannot be filtered at all, because
filtering ingress needs connection state and gVisor has no conntrack. `sandbox-egress-setup`
therefore leaves `INPUT` at default-ACCEPT on that path and rests on an *argument*
(`assets/Dockerfile`, above the `CONNTRACK` branch): nothing but the proxy's uid may send
anything outward, so an inbound connection cannot be answered and the handshake never
completes.

That argument was untested, and it guards against an attack that has actually happened —
a co-resident container once read 30 bytes out of an allowlisted sandbox. So it is the gate:
if a stranger can reach a service inside a gVisor sandbox, no amount of fixing gVisor's other
gaps is worth doing.

**Answer: the argument holds.** Measured 2026-08-11. Details below, including what this does
*not* settle.

---

## Measured

Host: Rocky Linux 10.2, Docker with `runsc` registered, image `sandbox-base:0.0.1-cad1bbc0`.
Network `sandbox-cli` (`enable_icc=false`, verified before the run), sandbox at `172.18.0.3`.

| # | what was run | result | what it establishes |
|---|---|---|---|
| — | `network inspect … enable_icc` | `false` | the outer layer is on; without this every row below measures the wrong thing |
| A1 | runc, no allowlist, host → `:9999` | canary returned | **positive control** — the harness can detect a success |
| A2 | runc, `--allow`, host → `:9999` | timed out | `INPUT` default-deny works where conntrack exists |
| B1 | runsc, `--allow`, `iptables-legacy -S INPUT` | `-P INPUT ACCEPT`, no rules | ingress filtering is genuinely absent, not merely different |
| B2 | runsc, `--allow`, host → `:9999` | **no handshake, timed out at 5s** | contained anyway — this is the gate |
| B3 | runsc, **no** allowlist, host → `:9999` | HTTP response returned | **the second control** — gVisor does accept inbound, so B2 was the firewall |
| C | runsc victim, peer container → `:9999` | no data | ICC survives gVisor |

A1 is the row that makes the rest mean anything. Recording B2 as "blocked" without first
proving the test can observe a success is the same error this project has already made three
times — reading a capability off whether a rule *inserted* rather than whether it *behaved*.

B2's `curl -v` showed `Trying 172.18.0.3:9999...` and then nothing. The TCP handshake never
completed, which is precisely the predicted mechanism: the SYN arrives, netstack tries to send
a SYN-ACK from a socket owned by the sandbox user, the `OUTPUT` chain permits only the proxy's
uid, and the reply is dropped. Consistent with the separately measured fact that `-m owner`
matches kernel-generated ACKs and retransmits under gVisor, not only payload-carrying packets.

---

## Why B2 needed a second control

B2 changes two variables against A1 — the runtime *and* the firewall — so on its own it was
consistent with the containment argument and equally consistent with "gVisor accepts no inbound
connection at all", which would be a different fact and would have left the argument untested.

B3 separates them: the same runsc container with no allowlist returned a full HTTP response to
the host. gVisor accepts inbound traffic perfectly well, so the block in B2 was the `OUTPUT`
chain refusing to let a non-proxy uid answer. That is the mechanism the comment claims, and it
is now measured rather than argued.

## Not settled

**Two weaknesses in the procedure itself**, recorded so a re-run does not inherit them:

- The peer-container step originally used `apk add curl`, so a `BLOCKED` result could have
  meant "curl was never installed". Row C avoids this by using busybox `nc`, which needs no
  install — C is the row to cite, not the curl variant.
- Row C has no positive control of its own. It leans on `enable_icc=false` being read from
  Docker's own config and enforced host-side, outside anything gVisor touches.

---

## Found while testing: a detached gVisor sandbox can become unremovable

```
could not kill container: container f1488cd9c901 PID 90915 is zombie and can not be killed.
Use the --init option ...
```

Docker's advice does not apply: `runtime.BuildArgs` already renders `--init` on every run. The
zombie is the host-side `runsc` sandbox process, not a child inside the container, so an init
inside the container cannot reap it.

This is worse than it looks, because three things depend on being able to remove a container:
`kill` cannot stop it, `clean` cannot reap it, and the container **name** stays held — and the
name is the mechanism enforcing one agent per branch, so that branch is locked until someone
intervenes by hand. Clearing it needs `runsc delete --force` before `docker rm` will work.

Unlike the DNS gap, this one has no fix on our side. It belongs on the requirements list next
to DNS, and possibly above it.

---

## Reproduction

```sh
NET=sandbox-cli
CNAME=sandbox-<repo>-<branch>
ip_of() { docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$1"; }

# Stop here if this is not `false` — every result below would measure the wrong thing.
docker network inspect $NET -f '{{index .Options "com.docker.network.bridge.enable_icc"}}'

echo "SECRET-CANARY-9f3a" > ./canary.txt

# A1 — positive control. Must return the canary.
docker rm -f "$CNAME" 2>/dev/null
./bin/sandbox-cli run --network default --detach -- python3 -m http.server 9999
sleep 3; IP=$(ip_of "$CNAME")
curl -s --max-time 5 "http://$IP:9999/canary.txt" || echo BLOCKED
docker rm -f "$CNAME"

# A2 — runc baseline. Must block.
docker rm -f "$CNAME" 2>/dev/null
./bin/sandbox-cli run --allow registry.npmjs.org --detach -- python3 -m http.server 9999
sleep 3; IP=$(ip_of "$CNAME")
curl -s --max-time 5 "http://$IP:9999/canary.txt" || echo BLOCKED
docker rm -f "$CNAME"

# B1/B2 — the gate. INPUT must be open, and the connection must still not form.
docker rm -f "$CNAME" 2>/dev/null
./bin/sandbox-cli run --runtime runsc --allow registry.npmjs.org --detach -- python3 -m http.server 9999
sleep 3; IP=$(ip_of "$CNAME")
docker exec -u root "$CNAME" iptables-legacy -S INPUT
curl -s -v --max-time 5 "http://$IP:9999/canary.txt" 2>&1 | tail -20
docker rm -f "$CNAME"

# C — ICC under gVisor. Must return nothing.
docker run -d --rm --name icc-victim --network $NET --runtime runsc alpine \
  sh -c 'echo CANARY-ICC > /tmp/f; while true; do nc -l -p 9999 < /tmp/f; done'
sleep 2; VIP=$(ip_of icc-victim)
docker run --rm --network $NET alpine sh -c "nc -w 3 $VIP 9999 || echo BLOCKED"
docker rm -f icc-victim

rm -f ./canary.txt
```

Remove the container between every step. A stopped detached container keeps its name
(`Remove` is false for `--detach` by design), and the next run fails with a name conflict
rather than anything explaining why.

---

## What gVisor still needs

In order, cheapest and most decisive first:

1. ~~**Whether the zombie reproduces.**~~ Measured 2026-08-12: five detached runsc sandboxes
   started and removed cleanly. Not a systematic failure, so `--detach` is supported. The case
   that *did* produce a zombie had a listening socket, served connections and had been
   `docker exec`'d into, so this is recorded as an occasional hazard rather than a gate — if it
   recurs, that is the shape to reproduce.
2. ~~**DNS.**~~ Implemented — `internal/sandbox/resolvers.go` and
   `internal/runtime/resolver.go`. Docker's embedded
   resolver at `127.0.0.11` is unreachable under gVisor because the NAT redirect that makes
   that address answer lives in the host kernel's netfilter, which netstack never consults.
   Established with a two-line reproduction involving none of this tool's code — a stock
   `alpine` on a user-defined network under runsc resolves nothing.

   The fix as built: sandbox-cli generates a `resolv.conf` from the host's own routable
   nameservers and mounts it read-only over `/etc/resolv.conf`. The firewall needed **no
   change at all**, because it already reads that file and permits exactly the resolvers
   listed in it.

   A generated file rather than a reserved environment variable, for two reasons. The root
   entrypoint runs in allowlist mode only, so a variable it read would have left
   `--runtime runsc --network default` silently broken. And choosing a container's resolver is
   a redirection primitive — point every name at a resolver you control and the allowlist
   resolves addresses of your choosing while looking entirely correct — which `mounts` being
   refused from a project `.sandbox.yaml` already prevents, so no new reserved name was
   needed. **Still to verify on a real host**: that `--runtime runsc --allow api.anthropic.com`
   now resolves and connects.

Permanent limitations to accept: `--publish` cannot work together with
`--allow` (no conntrack means no reply path for a service running as the sandbox uid, and the
only fix would be granting that uid egress — a hole straight through the allowlist), no
firewall denial logging, and IPv6 egress rejected wholesale.
