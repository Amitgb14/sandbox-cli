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
- **an SSH server** on one port for every sandbox: `ssh SANDBOX@gateway`.

Users talk to the gateway only. Nodes are reached only by the gateway, with
tokens users never hold.

This guide covers running it. The node side of a single machine is
[self-hosting.md](self-hosting.md); the gateway's own endpoints are in
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
sh packaging/fleet/make-certs.sh -o fleet-certs \
  -g gateway.example.internal 10.0.0.17 10.0.0.18
```

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

Each node is a `sandboxd` set up as in [self-hosting.md](self-hosting.md), with
these differences:

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

```sh
# the binary: the release's sandbox-gateway_<version>_linux_<arch>.tar.gz,
# or install.sh --with-gateway, or `make build` from a checkout
install -m 0755 sandbox-gateway /usr/local/bin/

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
a serving gateway through the [admin API](#changing-a-serving-gateway).

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
| `--router-listen HOST:PORT` | off | the HTTP router for public [services](#the-router). Off loopback, refused without `--router-tls-cert` and `--router-tls-key`. |
| `--router-domain DOMAIN` | | what the router serves under: `<service>.DOMAIN`, `<service>--<tenant>.DOMAIN`. Required with `--router-listen`. |
| `--router-tls-cert`, `--router-tls-key` | | the router's certificate and key, for `*.DOMAIN`. |
| `--router-public-scheme`, `--router-public-port` | `https` with a router certificate, else `http`; the `--router-listen` port | the URL services are shown with. |
| `--quota-sandboxes`, `--quota-cpus`, `--quota-memory-mb` | 0 (unlimited) | most one tenant may hold at once. |
| `--poll-interval` | `5s` | how often each node is asked for its status. |
| `--cors-origin ORIGIN` | | a browser origin allowed to call the API (repeatable). Others are refused. |
| `--secrets-key-file FILE` | | 32 random bytes, mode 0600, sealing the tenants' secrets and jobs' environments. Without it there are no secrets. |
| `--notify-allow-private` | off | lets a job's `notify` URL reach loopback, private and link-local addresses (and http to loopback). Off, the gateway posts only to public addresses, checked as it connects. |

To serve SSH on port 22 or the API on 443, give the unit
`AmbientCapabilities=CAP_NET_BIND_SERVICE` (it is in the unit, commented out),
and move the host's own sshd elsewhere first.

The first log line says how many nodes answer; `GET /v1/health` needs no key,
for a load balancer's health check.

### Changing a serving gateway

`sandbox-cli` has no admin commands; the admin API is plain HTTP with an admin
key ([api/v1.md](api/v1.md#gateway)):

```sh
GW=https://gateway.example.internal:8443
AUTH="Authorization: Bearer $(cat ops.key)"

curl -sS --cacert ca.pem -H "$AUTH" $GW/v1/admin/nodes          # health, capacity, last error
curl -sS --cacert ca.pem -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"user": "alice", "tenant": "team-a", "scopes": ["sandbox:read", "sandbox:create", "sandbox:delete", "sandbox:ssh"]}' \
  $GW/v1/admin/keys                                              # prints the secret once
curl -sS --cacert ca.pem -H "$AUTH" -X DELETE $GW/v1/admin/keys/key_…
curl -sS --cacert ca.pem -H "$AUTH" -H 'Content-Type: application/json' \
  -d '{"cordoned": true}' $GW/v1/admin/nodes/n17/cordon           # drain before maintenance
```

A node added through the API (`POST /v1/admin/nodes`) may name files only
under `--node-files-dir`: the gateway sends what a token file holds to the
endpoint beside it, so an admin key that could name any path could read any
file the gateway can.

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
| `admin` | every scope, on every user's sandboxes, plus keys, nodes and cordon |

A user is letters, digits and `. _ @ + -`, at most 64; so is a tenant, which
is optional and is what quotas count. A key acts as its user: two keys for one
user see the same sandboxes. Hand the secret over as you would a password, in
a file. `keys list` shows ids, never secrets; `keys revoke ID` ends a key.

### Revoking

Revoking a key (`DELETE /v1/admin/keys/{id}`) acts on what is already running,
not only on what starts next, before the call returns:

- **SSH.** Every open SSH connection whose user no longer holds an active key
  with `sandbox:ssh` (or `admin`) in that tenant is closed, its sessions and
  forwards with it, and the processes they ran are hung up. A connection made
  with an `ssh-access` token is held to the same check: it stays while its
  user still may use SSH. Removing an SSH key (`sandbox-cli ssh-key remove`, or
  `DELETE /v1/admin/ssh-keys/{id}`) closes the connections that logged in with
  it.
- **Jobs and agent runs.** A running job whose owner holds no active key at all
  in that tenant is cancelled as `DELETE /v1/jobs/{id}` would: its running
  sandboxes are terminated, its queued runs never start, and the job and each
  run say `cancelled: the owner's access was revoked`. Every run also checks its
  owner before it makes a sandbox.
- **Services** are routed to and given new replicas only while their owner
  holds an active key, which is checked on every request and every step, so
  revoking the owner's last key stops a service's traffic at once. Its
  replicas stay until an admin deletes the service.
- **The audit record** says what revocation ended: `ssh.revoked` (result
  `closed`) per connection, with the credential's key id and fingerprint, and
  `job.revoked` (result `cancelled`) per job, with its id. Never a secret.

Every API request checks its key as it arrives, so a revoked key's next call
is refused. The gateway also rechecks open SSH connections and running jobs
every 30 seconds, and at start, for a state file changed while it was stopped
(`keys revoke` with the gateway down).

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

**SSH**, when the gateway serves it:

```sh
sandbox-cli ssh demo                 # registers ~/.ssh/id_ed25519.pub if needed, pins the host key, runs ssh
sandbox-cli ssh demo -- uname -a

sandbox-cli ssh-key add              # or register a key yourself (--sandbox NAME limits it to one)
sandbox-cli ssh-key list
ssh -p 2222 -o UserKnownHostsFile=~/.config/sandbox/known_hosts demo@gateway.example.internal
scp -P 2222 -o UserKnownHostsFile=~/.config/sandbox/known_hosts file demo@gateway.example.internal:

sandbox-cli ssh-access demo --ttl 10m   # prints a one-off `ssh -p 2222 sgt_…@gateway` line
```

The SSH user name is the sandbox (its id, or a name among your own). Logins are
by a registered public key, or by an `ssh-access` token as the user name; there
are no passwords. A session is a shell, a command, sftp (so `scp` works) or a
local forward (`ssh -L`) to a port on the sandbox's own loopback. Agent
forwarding, X11 and remote forwards (`-R`) are refused. An `ssh-access` line is
the whole credential until it expires (15 minutes by default, at most 24
hours): anyone holding it can log in.

The Python and TypeScript SDKs take the gateway's URL and the key as their
token, and have the same SSH calls ([sdk/README.md](../sdk/README.md)).

## Studio

`sandbox-cli studio --context fleet` opens Studio on a gateway context. It is
the same Studio as for a plain `sandboxd`: `sandbox-cli studio` holds the API
key and adds it to each call it proxies, and the browser never sees the key.
On load Studio asks `GET /v1/whoami`; a plain `sandboxd` answers 404 and gets
exactly the screens it always had. A gateway answers with the key's scopes,
and those decide the screens:

| The key holds | Studio adds |
|---|---|
| any scope | **Jobs** (list, detail with each run's kept output and files), **Services** (list, detail with replicas, health and rollout), **Secrets** (names only), **SSH** (where to connect, the host key to pin), **Account** (user, tenant, key id, scopes) |
| `sandbox:create` | the Playground, submitting and cancelling jobs, deploying (a JSON spec) and scaling services, creating volumes, a sandbox's Terminal, Suspend and Snapshot |
| `sandbox:delete` | terminating sandboxes, deleting volumes and snapshots; with `sandbox:create`, removing a service |
| `sandbox:ssh` | adding and removing your SSH keys, issuing a short-lived access token for a sandbox (shown once) |
| `secrets:write` | setting and removing secrets. A value goes in a password field and is never shown: no call returns it |
| `admin` | everything above, plus **Nodes** (health, allocated capacity, cordon, uncordon, drain, add, remove), **Lost sandboxes**, **Users & keys** (issue and revoke API keys, any user's SSH keys) and **Audit** |

An action the key's scopes do not allow is not offered, rather than offered
and refused. A screen the key may not have — an admin screen for a tenant's
key, or a gateway screen on a plain `sandboxd` — is missing from the sidebar
and the palette, and a typed URL shows a plain *Not available* page that makes
no request for it. That is a convenience, not the control: the gateway refuses
a call without its scope (`403`) whoever sends it.

Two things Studio never shows: a node's endpoint, which is dropped as the node
list is read (and taken out of a node's error text), and a secret value. A new
API key's secret is in one answer only; Studio shows it once, with a copy
button and a warning, and drops it when you click Done. An SSH access token is
shown the same way.

**A hosted dashboard without the admin screens.** Building Studio with
`NEXT_PUBLIC_STUDIO_ADMIN=off` leaves the admin screens out of the bundle
altogether — their pages are not routes in that build and nothing they import
is included — so a Studio served to many tenants does not carry the
operator's UI at all:

```sh
NEXT_PUBLIC_STUDIO_ADMIN=off make studio build   # a sandbox-cli whose Studio has no admin screens
```

The default build keeps them, for an operator running their own gateway.
`npm run check:admin-off` in `studio/` (part of `npm run check`) makes such a
build and fails if any admin route, admin API path or admin screen title is
in it.

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
- **SSH is a scope of its own, and access ends with it.** An SSH key or token
  logs in only while its user holds an active key with `sandbox:ssh`, so a user
  left with read-only keys cannot open a shell. Revoking ends open connections
  and running jobs, not only the next login ([Revoking](#revoking)).
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
microservice, or a long-lived agent. Its replicas are ordinary sandboxes,
owned by the user who deployed it and made by the same create path as theirs
— placed by the scheduler, counted against the tenant's quota, labelled
`gateway.owner` — with two more labels the gateway sets and a request may
not: `gateway.service=<name>` and `gateway.service.rev=<revision>`.

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

**Health.** `health.http` is a `GET` on `port` through the node's tunnel —
the guest needs no network — and a 2xx or 3xx within `timeout_secs` is
healthy; `health.command` runs in the replica and exit 0 is healthy. Checks
run every `every_secs` (10), and `failures` (3) in a row replace the replica.
Without a health check a replica is healthy while its sandbox lives and its
command runs. A replica whose command exits, or whose sandbox is gone (an
idle timeout, someone deleting it), is replaced at once. Replacements after
repeated failures back off, up to a minute apart; each replica shows how many
replacements came before it (`restarts`).

**Placement.** `spread: node` puts each new replica on a node holding the
fewest of the service's replicas among those with room, so losing a machine
costs as few as it can; two replicas share a node only when no other fits.

**A lost node.** When the gateway marks a node unhealthy (three failed polls
by default), its replicas are replaced elsewhere at once. They are queued for
termination, and terminated when the node answers again, so a node that comes
back does not run a second copy.

**Rolling update.** A `PUT` that changes what a replica is — image, command,
resources, port, health, env, network — is a new revision. The gateway starts
one replica of it, waits until it is healthy, retires one old replica, and
repeats; while it runs `rollout.state` is `in_progress`, and at the end
`done`. A change to only `replicas`, `public` or `placement` applies to the
replicas there are, with no new revision. If a new replica fails its health
check `failures` times in a row, or a node refuses to create one (a bad
image, a reserved variable, over quota), the rollout stops: `rollout.state`
is `failed` with the reason, the old revision's replicas keep serving, and
any new replicas are replaced by old ones. Deploy a fixed spec to try again.

**Restarts.** The controller's state — specs, revisions, rollouts, which
sandbox is which replica, replicas waiting to be terminated — is in the state
file, so a restarted gateway resumes each service where it was, with the same
replicas, after checking each once.

**Owners.** A service runs in its owner's name. Once the owner holds no
active API key, no new replica is made and the router stops serving it; what
runs stays until an admin deletes it (`DELETE /v1/services/{name}?tenant=T`).

### The router

```sh
sandbox-gateway serve … \
  --router-listen 0.0.0.0:443 --router-domain apps.example.com \
  --router-tls-cert /etc/sandbox-gateway/tls/apps.pem \
  --router-tls-key /etc/sandbox-gateway/tls/apps-key.pem
```

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

### What services do not do yet

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
- **No admin commands in `sandbox-cli`.** Use `sandbox-gateway keys|nodes` with
  the gateway stopped, or the admin API with `curl` (above); the Go client
  (`internal/api`) has the admin calls.
- **SSH access follows a user's API keys.** An SSH key or token logs in, and
  stays connected, only while its user holds an active API key with
  `sandbox:ssh` ([Revoking](#revoking)). An admin lists and removes any user's
  SSH keys with `GET /v1/admin/ssh-keys?user=U` and
  `DELETE /v1/admin/ssh-keys/{id}`.
- **An open API stream outlives its key's revocation.** A request checks its
  key when it arrives; an attached terminal or a followed output stream opened
  before the revocation runs until it ends. SSH connections and jobs do not
  (above).
- **Quotas are per tenant and the same for every tenant**, set by flags.
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

`sandbox-cli ssh` against a real gateway and OpenSSH is row 37 of
[testing/end-to-end.md](testing/end-to-end.md), and a node's mutual TLS and
capacity are row 36.
