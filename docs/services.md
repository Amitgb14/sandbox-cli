# Services and the router

A service is a sandbox spec and a count the gateway keeps true: a stateless
microservice, or a long-lived agent. Its replicas are ordinary sandboxes,
owned by the user who deployed it and made by the same create path as theirs
— placed by the scheduler, counted against the tenant's quota, labelled
`gateway.owner` — with two more labels the gateway sets and a request may
not: `gateway.service=<name>` and `gateway.service.rev=<revision>`.


Services exist only on a gateway ([fleet.md](fleet.md)); a single `sandboxd`
has none. The endpoints are in [api/v1.md](api/v1.md#services).

## A service

```yaml
# service.yaml
name: review-bot                  # a DNS label with no "--"; unique in the tenant
image: ghcr.io/you/review-bot:1.4.2
command: [./serve, --port, "8080"] # started in each replica, detached
replicas: 3
resources: { cpus: 1, memory_mb: 1024, disk_mb: 4096 }
port: 8080                        # on the replica's own loopback
health: { http: /healthz, every_secs: 10, timeout_secs: 5, failures: 3 }
env: { MODE: prod }
network: { mode: allowlist, allow: [api.github.com] }
placement: { spread: node }
public: true                      # served by the router (below)
```

```sh
sandbox-cli service deploy -f service.yaml   # creates it, or updates it if it exists
sandbox-cli service ls
sandbox-cli service get review-bot           # each replica: sandbox, node, state, last check, restarts
sandbox-cli service scale review-bot 5
sandbox-cli service rm review-bot            # terminates the replicas
```

The API is `POST /v1/services`, `GET /v1/services`,
`GET|PUT|DELETE /v1/services/{name}` and `POST /v1/services/{name}/scale`
(types in [`internal/api/services_types.go`](../internal/api/services_types.go));
the Python and TypeScript SDKs have `deploy_service`/`deployService`,
`update_service`, `services`, `service`, `scale_service` and `delete_service`.
Creating, changing and scaling need `sandbox:create`; deleting needs
`sandbox:delete` as well; reading needs `sandbox:read`. Another user's service
is not found. An admin sees every service and names one in another tenant
with `?tenant=T`. A `GET` shows env names, not values, as for a sandbox; the
values are kept in the state file, which is why it stays 0600. A value that
must stay secret belongs in the secret store instead: `secrets: [NAME]` sets
each of the tenant's secrets (`sandbox-cli secret set NAME`) in every
replica's environment, opened as the replica is made and kept nowhere else.
A name the tenant has no secret for is refused, a gateway started without
`--secrets-key-file` refuses `secrets:` with `501`, and a secret removed later
stops new replicas (the service's `error` says which) rather than starting
one without it.

In Studio, the **Services** screen lists them, deploys a JSON spec, scales
and removes them, and shows each replica with its health and the rollout
([studio.md](studio.md)).


## Health

`health.http` is a `GET` on `port` through the node's tunnel —
the guest needs no network — and a 2xx or 3xx within `timeout_secs` is
healthy; `health.command` runs in the replica and exit 0 is healthy. Checks
run every `every_secs` (10), and `failures` (3) in a row replace the replica.
Without a health check a replica is healthy while its sandbox lives and its
command runs. A replica whose command exits, or whose sandbox is gone (an
idle timeout, someone deleting it), is replaced at once. Replacements after
repeated failures back off, up to a minute apart; each replica shows how many
replacements came before it (`restarts`).


## Placement and lost nodes

**Placement.** `spread: node` puts each new replica on a node holding the
fewest of the service's replicas among those with room, so losing a machine
costs as few as it can; two replicas share a node only when no other fits.

**A lost node.** When the gateway marks a node unhealthy (three failed polls
by default), its replicas are replaced elsewhere at once. They are queued for
termination, and terminated when the node answers again, so a node that comes
back does not run a second copy.


## Rollouts

A `PUT` that changes what a replica is — image, command,
resources, port, health, env, network — is a new revision. The gateway starts
one replica of it, waits until it is healthy, retires one old replica, and
repeats; while it runs `rollout.state` is `in_progress`, and at the end
`done`. A change to only `replicas`, `public` or `placement` applies to the
replicas there are, with no new revision. If a new replica fails its health
check `failures` times in a row, or a node refuses to create one (a bad
image, a reserved variable, over quota), the rollout stops: `rollout.state`
is `failed` with the reason, the old revision's replicas keep serving, and
any new replicas are replaced by old ones. Deploy a fixed spec to try again.


## Restarts and owners

**Restarts.** The controller's state — specs, revisions, rollouts, which
sandbox is which replica, replicas waiting to be terminated — is in the state
file, so a restarted gateway resumes each service where it was, with the same
replicas, after checking each once.

**Owners.** A service runs in its owner's name. Once the owner holds no
active API key, no new replica is made and the router stops serving it; what
runs stays until an admin deletes it (`DELETE /v1/services/{name}?tenant=T`).


## The router


```sh
sudo -u sandbox-gateway sandbox-gateway serve … \
  --router-listen 0.0.0.0:443 --router-domain apps.example.com \
  --router-tls-cert /etc/sandbox-gateway/tls/apps.pem \
  --router-tls-key /etc/sandbox-gateway/tls/apps-key.pem
```

| Flag | Default | |
|---|---|---|
| `--router-listen HOST:PORT` | off | the HTTP router for public services. Off loopback, refused without `--router-tls-cert` and `--router-tls-key`. |
| `--router-domain DOMAIN` | | what the router serves under: `<service>.DOMAIN`, `<service>--<tenant>.DOMAIN`. Required with `--router-listen`. |
| `--router-tls-cert`, `--router-tls-key` | | the router's certificate and key, for `*.DOMAIN`. |
| `--router-public-scheme`, `--router-public-port` | `https` with a router certificate, else `http`; the `--router-listen` port | the URL services are shown with. |

The router is the gateway's ingress for services with `public: true`, on its
own listener, held to the API's rule: an address other machines can reach
needs TLS. It takes no API key — a public service is public — and sends each
request to a healthy replica of the service, round robin, through that
replica's node's tunnel to `port`, as plain HTTP/1.1. WebSocket and other
upgrades pass through. Hop-by-hop headers are removed, `X-Forwarded-For`,
`X-Forwarded-Proto` and `X-Forwarded-Host` are the router's own (one a client
sent is replaced), and the `Host` the client asked for is passed on.

Names live under one wildcard name, so one certificate for `*.DOMAIN` serves
them all:

| Host | Service |
|---|---|
| `<service>.DOMAIN` | `<service>` of the default tenant (users with no tenant) |
| `<service>--<tenant>.DOMAIN` | `<service>` of tenant `<tenant>` |

They cannot collide: a service name never contains `--`, so the first `--`
always ends it and the rest is the tenant, exactly. A tenant that is not
itself a lowercase DNS label (tenants may hold capitals, dots and `@`) has no
name here, and its services cannot be made public. Names are per tenant, not
per user, because that is what a host name can carry.

| The router answers | when |
|---|---|
| `404` | the host names no service, a service that is not public, or nothing under DOMAIN |
| `503` | no replica is healthy (or the owner holds no active key) |
| `502` | the chosen replica did not answer |

Every service is a sibling under DOMAIN, so the router strips `Domain=` from
every cookie a replica sets, keeping cookies with the service that set them.
Use a domain of its own for DOMAIN — not a parent of the gateway's API or of
anything else — so that no service shares a site with something it should not.
The router cannot stop a page's own script from setting a cookie for DOMAIN
(`document.cookie = "…; domain=DOMAIN"`), which the browser then sends to
every tenant's service: for tenants who do not trust each other, make DOMAIN
a registrable domain of its own and add it to the Public Suffix List, as
hosting providers do, so that browsers refuse such cookies.
`--router-public-scheme` and `--router-public-port` set the URL services are
shown with, when the router sits behind a load balancer.


## What services do not do yet


- **No internal names.** A sandbox in the fleet cannot reach a service as
  `review-bot.internal` through its allowlist; services are reached through
  the router or by their owner's tunnel.
- **No autoscaling.** The count is what was asked for; `scale` changes it.
- **Stateless only.** A replica's disk is its own and goes with it; state
  belongs in a database outside the fleet.
- **Health is checked by the one gateway.** Checks run from the gateway
  process, one per replica per interval, through each node's API.
- **No retry in the router.** A request sent to a replica whose node has
  just died answers `502`; the router stops choosing it when the next check
  fails or the gateway marks the node unhealthy (three polls, 15 s by default).
- **Services without a `command` run nothing.** A sandbox has no entrypoint
  of its own, and one with no process running idles out like any other and
  is replaced.

