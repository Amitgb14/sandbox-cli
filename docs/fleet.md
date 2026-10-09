# A fleet behind a gateway

`sandbox-gateway` is one Sandbox API endpoint in front of any number of
`sandboxd` nodes. It speaks the same API v1 as a node, so the CLI, the SDKs
and the conformance suite work against it with only the address and the
credential changed. What it adds is what one machine never needed:

- **users with API keys**, each with scopes, issued and revoked by the operator;
- **ownership**: every sandbox, volume and snapshot belongs to the user who
  made it, and nobody else can see it;
- **a scheduler** that picks the node for each new sandbox, and **routing** of
  every later call to the node that holds it;
- **quotas** per tenant;
- **organisations**: tenants users create and share, chosen per request;
- **an SSH server** on one port for every sandbox: `ssh SANDBOX@gateway`.

Users talk to the gateway only. Nodes are reached only by the gateway, with
tokens users never hold.

This page covers setting it up and the model it works by. Each part the
gateway adds has a page of its own:

| Page | |
|---|---|
| [SSH](ssh.md) | one SSH port for every sandbox: keys, short-lived tokens, scp, sftp, port forwards, the host key |
| [Organizations](organizations.md) | tenants users create and share, chosen per request with `X-Sandbox-Org` |
| [Jobs and secrets](jobs.md) | commands and agent runs the gateway runs after you have gone, batches, the secret store, notify webhooks |
| [Services and the router](services.md) | a spec and a count kept true: health, rollouts, placement, public HTTP names |
| [Operations](operations.md) | metrics, the audit log, drain, lost nodes, revocation, changing a serving gateway |
| [Studio](studio.md) | the browser view, and which screens a key's scopes open |

The node side of a single machine is [self-hosting.md](self-hosting.md) and
[sandboxd.md](sandboxd.md); the gateway's own endpoints are in
[api/v1.md](api/v1.md#gateway).

## Which shape you need

| Shape | Run | When |
|---|---|---|
| A Mac | `sandboxd` alone ([local-macos.md](local-macos.md)) | one person's machine. There is no gateway: the unix socket only you can open is the access control. |
| One Linux machine | `sandboxd` ([self-hosting.md](self-hosting.md)), and optionally `sandbox-gateway` beside it | `sandboxd` alone has one token: everyone who holds it is the operator and sees every sandbox. Add a gateway on the same box when people should have their own keys, see only their own sandboxes, have quotas, or log in with `ssh`. |
| Many Linux machines | a `sandboxd` on each, one `sandbox-gateway` in front | more sandboxes than one machine runs. Nodes listen only on a private network and accept only the gateway's certificate. |

The release ships `sandbox-gateway` for Linux (amd64, arm64). It needs no KVM
and no root, so it can run on a small machine of its own or beside a node.

### One machine

On one machine the gateway reaches `sandboxd` without TLS. There are two ways,
and which one depends on who runs what:

- **A loopback port with a token** — the one that works with the packaged
  units, where `sandboxd` runs as root and the gateway as a user of its own:

  ```sh
  # sandboxd, as in self-hosting.md but on loopback only, with a node name
  sandboxd --backend firecracker --kernel /var/lib/sandboxd/vmlinux \
    --firecracker /usr/local/bin/firecracker --jailer /usr/local/bin/jailer \
    --policy /etc/sandboxd/policy.yaml \
    --listen 127.0.0.1:7443 --token-file /etc/sandboxd/token --node-id local

  # the gateway's copy of that token, readable by the gateway's user only
  install -o sandbox-gateway -g sandbox-gateway -m 0600 /etc/sandboxd/token \
    /etc/sandbox-gateway/local.token
  sudo -u sandbox-gateway sandbox-gateway --state /var/lib/sandbox-gateway/state.json \
    nodes add local http://127.0.0.1:7443 --token-file /etc/sandbox-gateway/local.token
  ```

  A node may be plain `http://` only on loopback; the gateway refuses it for
  any other address.

- **A unix socket** — `nodes add local unix:///path/to/sandboxd.sock`. `sandboxd`
  makes its socket owner-only, so this works only when the gateway runs as the
  same user as `sandboxd`: an unprivileged `sandboxd` you started yourself, with
  the gateway beside it as you. The packaged gateway unit runs as its own user
  and cannot open root's socket; use the loopback port there.

Then serve the gateway on the address users reach (below, [the
gateway](#the-gateway)), on a port other than the node's.

## Certificates

Between the gateway and its nodes there are two checks: the node's bearer
token on every request, and mutual TLS, where a node accepts a connection only
from a certificate its CA signed. They fail differently — a leaked token is
useless off the private network without the gateway's key, and a leaked key is
useless without the token — so a fleet uses both.

[`packaging/fleet/make-certs.sh`](../packaging/fleet/make-certs.sh) makes a
working set with openssl: a private CA, the gateway's client certificate, a
server certificate for each node, and optionally one for the gateway's own API.

```sh
curl -fsSLO https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/fleet/make-certs.sh
sh make-certs.sh -o fleet-certs \
  -g gateway.example.internal 10.0.0.17 10.0.0.18
```

The script is all it needs, so it is fetched on its own; from a checkout,
`sh packaging/fleet/make-certs.sh` is the same.

Each node argument is the host part of that node's endpoint **exactly as the
gateway dials it**: an IP address becomes an IP SAN, a name a DNS SAN. Run it
again with the same `-o` to add a node: the CA is reused and existing
certificates are kept. Keys are P-256, written 0600; leaves last 825 days
(`-d DAYS`), the CA ten years.

| File | Goes to | Used as |
|---|---|---|
| `ca.pem` | every node; the gateway | node: `--client-ca`; gateway: `ca_file` / `--ca-file` |
| `node-HOST.pem`, `node-HOST-key.pem` | that node | `--tls-cert`, `--tls-key` |
| `gateway-client.pem`, `gateway-client-key.pem` | the gateway | `cert_file`, `key_file` / `--cert-file`, `--key-file` |
| `gateway.pem`, `gateway-key.pem` (with `-g`) | the gateway | `serve --tls-cert`, `--tls-key` |
| `ca-key.pem` | **nowhere** | it can mint a certificate every node accepts: keep it offline |

For the gateway's own API, a certificate from a CA your users' machines already
trust is better than `-g`: then `sandbox-cli context add` needs no `--ca`. Use
`-g` for a gateway reached on a private network, and give users `ca.pem`.

## Nodes

Each node is a `sandboxd` set up as in [self-hosting.md](self-hosting.md),
including a disk of its own for its state directory ([An extra disk for
sandboxes](self-hosting.md#an-extra-disk-for-sandboxes)), with these
differences:

```sh
sandboxd --backend firecracker --kernel /var/lib/sandboxd/vmlinux \
  --firecracker /usr/local/bin/firecracker --jailer /usr/local/bin/jailer \
  --policy /etc/sandboxd/policy.yaml \
  --listen 10.0.0.17:7443 \
  --token-file /etc/sandboxd/token \
  --tls-cert /etc/sandboxd/tls/node-10.0.0.17.pem \
  --tls-key /etc/sandboxd/tls/node-10.0.0.17-key.pem \
  --client-ca /etc/sandboxd/tls/ca.pem \
  --node-id n17 --node-label region=west \
  --allowed-host 10.0.0.17
```

In `packaging/systemd/sandboxd.service` that means changing `--listen`, the TLS
paths and `--allowed-host`, and adding `--client-ca` and `--node-id`.

- **`--listen` on the private address only**, never `0.0.0.0` on a machine
  with an address users can reach, and a host firewall that lets only the
  gateway reach the port.
- **`--client-ca`** is mutual TLS: no certificate from that CA, no handshake.
  It needs `--tls-cert` and `--tls-key`.
- **`--allowed-host`** must name the host part of the endpoint the gateway is
  given. The gateway sends that as the `Host` header, and `sandboxd` answers
  only loopback names and the ones listed there.
- **`--node-id`** must equal the name the node is given at the gateway. Every
  sandbox id the node makes carries it (`sbx_n17_0123456789abcdef`); a node
  that reports a different name is marked unhealthy at once, because its ids
  would route somewhere else. Lowercase letters, digits and `-`, at most 31.
- **`--node-label key=value`** (repeatable) describes the node at `GET /v1/node`.
- **`--capacity-cpus`, `--capacity-memory-mb`, `--capacity-disk-mb`** are what
  the scheduler may place there. The defaults are the whole machine; set them
  lower to keep room for the host.

Make a token per node (`head -c 32 /dev/urandom | base64`, 0600) and copy it to
the gateway. Nodes should run the same release and, ideally, the same policy:
the gateway offers what **every** answering node can do (see
[scheduling](#scheduling-and-failure)).

## The gateway

### Install

**The binary: built from a checkout, for now.** No published release has
`sandbox-gateway` yet: 0.0.1 is the last release of the container design.
Until the rewrite's first release, build it as the nodes' binaries are built
([self-hosting.md](self-hosting.md#install)); once a release has it,
`install.sh --with-gateway` (with `--client-only` on a machine that runs no
sandboxes) or the release's `sandbox-gateway_<version>_linux_<arch>.tar.gz`
replaces the first two lines below. The gateway needs no KVM and no root.

```sh
git clone https://github.com/Amitgb14/sandbox-cli && cd sandbox-cli && make build
install -m 0755 bin/sandbox-gateway bin/sandbox-cli /usr/local/bin/

useradd --system --home-dir /var/lib/sandbox-gateway --shell /usr/sbin/nologin sandbox-gateway
install -d -o sandbox-gateway -g sandbox-gateway -m 0700 \
  /etc/sandbox-gateway /etc/sandbox-gateway/tls /var/lib/sandbox-gateway

install -o sandbox-gateway -g sandbox-gateway -m 0600 \
  fleet-certs/ca.pem fleet-certs/gateway-client.pem fleet-certs/gateway-client-key.pem \
  fleet-certs/gateway.pem fleet-certs/gateway-key.pem /etc/sandbox-gateway/tls/
install -o sandbox-gateway -g sandbox-gateway -m 0600 n17.token n18.token /etc/sandbox-gateway/
```

Every file the gateway reads must belong to its user: it refuses a token, a
client key or its state file that other users can read.

### Nodes

Nodes come from two places, and a name may be in only one of them.

**A node file**, read at start, for nodes managed as configuration.
[`packaging/fleet/nodes.yaml`](../packaging/fleet/nodes.yaml) is an example:

```yaml
nodes:
  - name: n17
    endpoint: https://10.0.0.17:7443
    token_file: /etc/sandbox-gateway/n17.token
    ca_file: /etc/sandbox-gateway/tls/ca.pem
    cert_file: /etc/sandbox-gateway/tls/gateway-client.pem
    key_file: /etc/sandbox-gateway/tls/gateway-client-key.pem
```

Unknown keys are refused, so a misspelt `ca_file` cannot quietly mean "trust
the system's CAs". Pass it with `serve --node-config`. A node defined here is
changed by editing the file and restarting, never through the admin API.

**The state file**, through the CLI while the gateway is stopped, or the admin
API while it serves:

```sh
sudo -u sandbox-gateway sandbox-gateway --state /var/lib/sandbox-gateway/state.json \
  nodes add n17 https://10.0.0.17:7443 \
    --token-file /etc/sandbox-gateway/n17.token \
    --ca-file /etc/sandbox-gateway/tls/ca.pem \
    --cert-file /etc/sandbox-gateway/tls/gateway-client.pem \
    --key-file /etc/sandbox-gateway/tls/gateway-client-key.pem
sandbox-gateway --state … nodes list
sandbox-gateway --state … nodes remove n17
```

`nodes add` reads every file it is given and refuses a node it could not reach
as configured. An endpoint is `https://host:port`, `unix:///path`, or plain
`http://` on loopback only.

### The first admin key

```sh
sudo -u sandbox-gateway sandbox-gateway --state /var/lib/sandbox-gateway/state.json \
  keys create --user ops --scope admin
```

The secret (`sgk_…`) is printed once and stored only as a hash. Run `keys` and
`nodes` as the gateway's user: run as root, they would create a state file the
gateway cannot open.

While the gateway serves it holds the state file, and `keys` and `nodes` refuse
with *another process holds …state.json.lock*: a change made beside a running
gateway would be overwritten by it, and a revoked key would come back. Change
a serving gateway through the [admin API](operations.md#changing-a-serving-gateway).

### Serve

```sh
sandbox-gateway serve --state /var/lib/sandbox-gateway/state.json \
  --listen 0.0.0.0:8443 \
  --tls-cert /etc/sandbox-gateway/tls/gateway.pem \
  --tls-key /etc/sandbox-gateway/tls/gateway-key.pem \
  --node-config /etc/sandbox-gateway/nodes.yaml \
  --ssh-listen 0.0.0.0:2222 --ssh-public-host gateway.example.internal
```

[`packaging/systemd/sandbox-gateway.service`](../packaging/systemd/sandbox-gateway.service)
runs this as the `sandbox-gateway` user with no capabilities, a read-only
system and only its state directory writable.

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd/sandbox-gateway.service \
  -o /etc/systemd/system/sandbox-gateway.service      # or cp it from a checkout
# set --ssh-public-host to the name users ssh to, as above
sed -i 's/gateway.example.internal/gw.example.internal/' /etc/systemd/system/sandbox-gateway.service
systemctl daemon-reload && systemctl enable --now sandbox-gateway
```

| Flag | Default | |
|---|---|---|
| `--state FILE` | required | keys, owners, nodes added at run time. Created 0600 when missing. |
| `--listen HOST:PORT` | `127.0.0.1:7443` | an address other machines can reach is refused without `--tls-cert` and `--tls-key`. TCP only. |
| `--tls-cert`, `--tls-key` | | the API's certificate and key (PEM). |
| `--node-config FILE` | | the node file above. |
| `--node-files-dir DIR` | `nodes/` beside the state file | the only directory whose files a node added **through the admin API** may name. |
| `--ssh-listen HOST:PORT` | off | the SSH server. |
| `--ssh-host-key FILE` | `ssh_host_ed25519_key` beside the state file | created 0600 when missing; one key for the whole fleet. |
| `--ssh-public-host`, `--ssh-public-port` | the `--ssh-listen` host and port | what clients are told to connect to. Required when listening on `0.0.0.0` or behind a load balancer. |
| `--router-listen HOST:PORT` | off | the HTTP router for public [services](services.md#the-router). Off loopback, refused without `--router-tls-cert` and `--router-tls-key`. |
| `--router-domain DOMAIN` | | what the router serves under: `<service>.DOMAIN`, `<service>--<tenant>.DOMAIN`. Required with `--router-listen`. |
| `--router-tls-cert`, `--router-tls-key` | | the router's certificate and key, for `*.DOMAIN`. |
| `--router-public-scheme`, `--router-public-port` | `https` with a router certificate, else `http`; the `--router-listen` port | the URL services are shown with. |
| `--quota-sandboxes`, `--quota-cpus`, `--quota-memory-mb` | 0 (unlimited) | most one tenant (or organisation) may hold at once. |
| `--max-orgs-per-user N` | `10` | most [organisations](organizations.md) one user may create or own; each has a quota of its own, so this bounds how far one user multiplies theirs. 0 is unlimited. |
| `--poll-interval` | `5s` | how often each node is asked for its status. |
| `--cors-origin ORIGIN` | | a browser origin allowed to call the API (repeatable). Others are refused. |
| `--secrets-key-file FILE` | | 32 random bytes, mode 0600, sealing the tenants' secrets and jobs' environments. Without it there are no secrets. |
| `--notify-allow-private` | off | lets a job's `notify` URL reach loopback, private and link-local addresses (and http to loopback). Off, the gateway posts only to public addresses, checked as it connects. |
| `--metrics-listen HOST:PORT` | off | Prometheus metrics, on loopback only ([operations.md](operations.md#metrics)). |
| `--audit-log FILE` | `audit/gateway.jsonl` beside the state file | who did what, as JSONL; `none` keeps no log ([operations.md](operations.md#the-audit-log)). |
| `--node-lost-after` | `5m` | how long a node may not answer before its sandboxes are reported lost and stop counting against quotas ([operations.md](operations.md#node-loss-and-lost-sandboxes)). |
| `--jobs-dir DIR` | `jobs/` beside the state file | where jobs' runs keep output and files ([jobs.md](jobs.md)). |
| `--job-retention` | `24h` | how long a finished job, and what it kept, is kept. |

To serve SSH on port 22 or the API on 443, give the unit
`AmbientCapabilities=CAP_NET_BIND_SERVICE` (it is in the unit, commented out),
and move the host's own sshd elsewhere first.

The first log line says how many nodes answer; `GET /v1/health` needs no key,
for a load balancer's health check.

### Changing a serving gateway

While it serves, the gateway holds its state file, so keys and nodes change
through the admin API — plain HTTP with an admin key — or, for nodes, through
`sandbox-cli gateway`. The calls, and why a node added at run time may name
files only under `--node-files-dir`, are in
[operations.md](operations.md#changing-a-serving-gateway).

## Giving users keys

```sh
sudo -u sandbox-gateway sandbox-gateway --state … keys create --user alice --tenant team-a \
  --scope sandbox:read --scope sandbox:create --scope sandbox:delete --scope sandbox:ssh
```

| Scope | Lets the key |
|---|---|
| `sandbox:read` | get, list, process output, files read, directory listings, events, volumes and snapshots listed |
| `sandbox:create` | create sandboxes and volumes, and act on its user's: run, start processes, stdin, signals, attach, tunnels, file writes, suspend, resume, snapshots, network policy |
| `sandbox:delete` | terminate sandboxes, delete volumes and snapshots |
| `sandbox:ssh` | register SSH keys, issue SSH access tokens, log in over SSH — which runs commands in the sandbox. A login, by SSH key or token, needs its user to hold an active key with this scope at the time, and an open connection ends when they no longer do |
| `secrets:write` | set and remove the tenant's secrets. Any key of the tenant may name them in a job or a service, and so read them from inside its sandboxes: a tenant is the unit that shares secrets, and users with no tenant all share the default one |
| `org:create` | create [organisations](organizations.md), up to `--max-orgs-per-user`. Joining one needs no scope: an owner adds you |
| `admin` | every scope, on every user's sandboxes, plus keys, nodes and cordon; may act in any organisation |

A user is letters, digits and `. _ @ + -`, at most 64; so is a tenant, which
is optional and is what quotas count. A key acts as its user: two keys for one
user see the same sandboxes. Hand the secret over as you would a password, in
a file. `keys list` shows ids, never secrets; `keys revoke ID` ends a key.

### Revoking

Revoking a key (`DELETE /v1/admin/keys/{id}`, or `keys revoke` with the
gateway stopped) ends what is already running, not only what starts next:
open API streams, SSH connections and running jobs, before the call returns,
each recorded in the audit log. What exactly ends, and how a gateway catches
a state file changed while it was stopped, is in
[operations.md](operations.md#revoking).

## Users' side

```sh
# the key in a file only you can read
umask 077; printf '%s\n' 'sgk_…' > ~/.config/sandbox/fleet.key

sandbox-cli context add fleet https://gateway.example.internal:8443 \
  --token-file ~/.config/sandbox/fleet.key --ca ca.pem     # --ca only for a private CA
sandbox-cli context use fleet
sandbox-cli whoami            # user, tenant, key id, scopes
sandbox-cli run --keep --name demo -- uname -a
sandbox-cli ls                # your sandboxes only
```

Everything that works against a `sandboxd` works here, with the scopes the key
holds. Two exceptions: a sandbox may not set labels starting with `gateway.`
(the gateway stamps `gateway.owner` and `gateway.tenant` itself), and through a
gateway at most 30 labels.

**SSH**, when the gateway serves it: `sandbox-cli ssh demo` registers your
public key, pins the gateway's host key and runs `ssh`; afterwards plain
`ssh -p 2222 demo@gateway.example.internal`, `scp`, `sftp` and `ssh -L` work
too, and `sandbox-cli ssh-access demo` prints a short-lived login that needs
no key. Logins need an active key with `sandbox:ssh`. All of it is in
[ssh.md](ssh.md).

The Python and TypeScript SDKs take the gateway's URL and the key as their
token, and have the same SSH calls ([sdk/README.md](../sdk/README.md)).

## Organisations

An organisation is a tenant that users create and share, rather than one the
operator writes on their keys, with all of a tenant's guarantees: its own
sandboxes, secrets, jobs, services and quota, invisible from any other.
`sandbox-cli org create`, `org use` and `org members` manage them, and every
request picks one with the `X-Sandbox-Org` header, checked against the key's
user's memberships. The model, the header, members, what is guaranteed and
the per-user cap are in [organizations.md](organizations.md).

## Studio

`sandbox-cli studio --context fleet` opens Studio on a gateway context. It is
the same Studio as for a plain `sandboxd`, with screens added for what the
key's scopes allow — jobs, services, secrets, SSH, organisations, and for an
admin key nodes, lost sandboxes, keys and the audit log — and a build without
the admin screens for a dashboard hosted for many tenants. See
[studio.md](studio.md).

To give users Studio without installing anything, serve it with
`sandbox-cli studio host` and send each one an invite link from
`keys create --invite-url` ([studio.md](studio.md#hosted-studio)).

## The security model

- **Users never hold node tokens.** The gateway strips the caller's
  `Authorization`, cookies and `Origin` from every forwarded request and puts
  the node's token on. Node tokens and client keys are files the gateway reads,
  refused when other users can read them.
- **Ownership comes from the gateway's state, never from the request.** A
  sandbox id names its node only so the call can be routed; who may act on it
  is the owner the gateway recorded when it made the sandbox. A request that
  sets a `gateway.*` label is refused, and a name is looked up among the
  caller's own sandboxes only, so two users may each have a `web`.
- **Someone else's sandbox does not exist.** Every call on a sandbox the caller
  does not own answers `404 not_found`, the same as one that never existed, so
  ids cannot be probed. A key without the scope a call needs is refused before
  anything is looked up.
- **Scopes are fixed at issue.** A key holds the scopes it was given; `admin`
  holds all of them.
- **An organisation is chosen, never assumed.** `X-Sandbox-Org` selects a
  tenant only for a key whose user is a member, checked once per request
  before any handler runs; memberships come only from creating one or being
  added by an owner, and an organisation's name cannot be a tenant already
  in use ([organizations.md](organizations.md)).
- **SSH is a scope of its own, and access ends with it.** An SSH key or token
  logs in only while its user holds an active key with `sandbox:ssh`, so a user
  left with read-only keys cannot open a shell. Revoking ends open connections,
  open API streams and running jobs, not only the next login
  ([operations.md](operations.md#revoking)).
- **Secrets travel by reference.** API keys and SSH tokens are random 256-bit
  strings stored only as their SHA-256; logs name a key by its id. The state
  file holds no secret, and is still 0600 and refused if others can read it,
  because it says who owns what. The SSH host key is refused if others can
  read it or if it is a symlink.
- **A request may tighten, never loosen.** Each node still applies its own
  policy to everything the gateway forwards: the gateway adds checks and
  removes none.
- **A job's notify URL reaches public addresses only**, checked when the
  gateway connects, after the name is resolved, so a user cannot make the
  gateway call its own loopback, the nodes' network or a metadata address.
  `--notify-allow-private` lifts this for hook receivers on a private network.
- **The guest never reaches the gateway's network.** SSH remote forwarding is
  refused, and a local forward goes only to the sandbox's own loopback. A
  session accepts only `TERM`, `LANG` and `LC_*` from the client's environment.

## Images

A node's images — `GET`, `POST` and `DELETE /v1/images`, `sandbox-cli image`,
Studio's Images screen — are its operator's, managed on the node itself
([self-hosting.md](self-hosting.md#images)). The gateway answers them `501
unsupported` and does not report the `images` capability: an install fills a
node's disk for every tenant on it. A gateway catalog of images run by admin
keys, which keeps nodes filled, is not done yet. Sandboxes still prefer a node
that has their image (below).

## Scheduling and failure

- **Placement.** Among nodes that are answering, not cordoned, able to run the
  request and with room for it, the gateway prefers one with a booted pool for
  the image, then one that has already built the image, then the most free
  memory. A sandbox started from a snapshot, or mounting volumes, goes to the
  node that holds them.
- **Capabilities** at the gateway are what every answering node can do: a
  capability only if all have it, the smallest limits, the strictest network
  ceiling.
- **A node that fails** three polls in a row (15 s at the default interval)
  takes no new sandboxes; calls on its sandboxes answer `503`, and they are
  missing from listings until it answers again.
- **Ownership is reconciled** every minute: the record of a sandbox its node
  no longer runs (an idle timeout, a node restart) is forgotten, and stops
  counting against its tenant's quota.
- **Cordon** a node (`POST /v1/admin/nodes/{name}/cordon`) to stop new
  sandboxes landing there while those running carry on. The node holds the
  cordon in memory, so a restart of the node clears it.
- **Removing a node** keeps its sandboxes' owner records, for if it comes back.

## Services

A service is a sandbox spec and a count the gateway keeps true: a stateless
microservice, or a long-lived agent, with health checks, rolling updates,
spread across nodes, and an HTTP router that serves public ones as
`<service>.DOMAIN`. See [services.md](services.md).

## Jobs and secrets

A job is a command, or an agent and a prompt, that the gateway runs in a
fresh sandbox per run after you have gone, keeping its output and the files
you name; a batch runs one per prompt. Secrets are kept sealed on the
gateway and set in a run's environment by name. See [jobs.md](jobs.md).

## Operations

Metrics, the gateway's audit log, cordon and drain, what happens when a node
is lost, revocation and changing a serving gateway are in
[operations.md](operations.md).

## Back up

`/var/lib/sandbox-gateway/` holds the state file and the SSH host key.
Losing the state file loses every key and every ownership record: running
sandboxes then belong to nobody and can only be reached on their node. Losing
the host key makes every user's `ssh` warn that the host changed. Copy both
while the gateway is stopped, or copy the state file whole (it is replaced
atomically on every change).

## Not done yet

- **One gateway process per state file.** The state is a JSON file one process
  holds; there is no second gateway for failover, and no database yet.
- **No single sign-on.** Users authenticate with gateway-issued API keys.
- **No API-key commands in `sandbox-cli`.** `sandbox-cli gateway` covers
  nodes, drain, lost sandboxes and the audit log; issue and revoke keys with
  `sandbox-gateway keys` while the gateway is stopped, the admin API with
  `curl` ([operations.md](operations.md#changing-a-serving-gateway)), or
  Studio. The Go client (`internal/api`) has every admin call.
- **SSH access follows a user's API keys.** An SSH key or token logs in, and
  stays connected, only while its user holds an active API key with
  `sandbox:ssh` ([operations.md](operations.md#revoking)). An admin lists and removes any user's
  SSH keys with `GET /v1/admin/ssh-keys?user=U` and
  `DELETE /v1/admin/ssh-keys/{id}`.
- **Quotas are per tenant and the same for every tenant**, set by flags; an
  organisation is a tenant.
- **Organisations cannot be deleted or renamed** yet, and memberships are
  managed through the API, the CLI and Studio only (`sandbox-gateway` has no
  offline command for them).
- **No usage metering** beyond the per-node audit logs; each node keeps its own.
- **The gateway does not proxy `GET /v1/node`**; node status is
  `GET /v1/admin/nodes`.
- **One SSH host key** for the whole fleet, ed25519 only.

## Checking it works

The conformance suite runs against a gateway as against any endpoint. Give it
a user's key, not an admin's (an admin sees past the ownership rules the suite
must work within), holding `sandbox:read`, `sandbox:create`, `sandbox:delete`
and `sandbox:ssh`, as the gateway's own tests do:

```sh
SANDBOX_CONFORMANCE_ENDPOINT=https://gateway.example.internal:8443 \
SANDBOX_CONFORMANCE_TOKEN=$(cat alice.key) \
  go test ./internal/api/conformance -run TestEndpoint -v
```

[testing/fleet-walkthrough.md](testing/fleet-walkthrough.md) runs the whole
gateway by hand on one KVM machine and a Mac: nodes, keys, SSH, isolation,
secrets, jobs, services and the router, Studio, revocation and drain.

`sandbox-cli ssh` against a real gateway and OpenSSH is row 37 of
[testing/end-to-end.md](testing/end-to-end.md), and a node's mutual TLS and
capacity are row 36.
