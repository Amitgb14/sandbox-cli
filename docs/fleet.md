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
  sudo -u sandbox-gateway sandbox-gateway --state-dir /var/lib/sandbox-gateway \
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
sudo sandboxd --backend firecracker --kernel /var/lib/sandboxd/vmlinux \
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

A command that needs root says `sudo`. One that reads the gateway's own files,
its state, keys and certificates, runs as its user, `sudo -u sandbox-gateway`,
as its unit does; the gateway refuses files other users can read.

**The binary: built from a checkout, for now.** No published release has
`sandbox-gateway` yet: 0.0.1 is the last release of the container design.
Until the rewrite's first release, build it as the nodes' binaries are built
([self-hosting.md](self-hosting.md#install)); once a release has it,
`install.sh --with-gateway` (with `--client-only` on a machine that runs no
sandboxes) or the release's `sandbox-gateway_<version>_linux_<arch>.tar.gz`
replaces the first two lines below. The gateway needs no KVM and no root.

```sh
git clone https://github.com/Amitgb14/sandbox-cli && cd sandbox-cli && make build
sudo install -m 0755 bin/sandbox-gateway bin/sandbox-cli /usr/local/bin/

sudo useradd --system --home-dir /var/lib/sandbox-gateway --shell /usr/sbin/nologin sandbox-gateway
sudo install -d -o sandbox-gateway -g sandbox-gateway -m 0700 \
  /etc/sandbox-gateway /etc/sandbox-gateway/tls /var/lib/sandbox-gateway

sudo install -o sandbox-gateway -g sandbox-gateway -m 0600 \
  fleet-certs/ca.pem fleet-certs/gateway-client.pem fleet-certs/gateway-client-key.pem \
  fleet-certs/gateway.pem fleet-certs/gateway-key.pem /etc/sandbox-gateway/tls/
sudo install -o sandbox-gateway -g sandbox-gateway -m 0600 n17.token n18.token /etc/sandbox-gateway/
```

Every file the gateway reads must belong to its user: it refuses a token, a
client key, its database file or its database's address file that other
users can read.

### The gateway's database

Everything the gateway keeps across a restart — keys and accounts,
organisations and their members, policies and invites, who owns every
sandbox, volume and snapshot, nodes added at run time, sealed secrets, jobs
and services — is in a database:

| `--store` | For | Where |
|---|---|---|
| `sqlite` (the default) | one gateway on one machine | `gateway.db` in `--state-dir`, mode 0600, made at first start |
| `postgres` | one or more gateways, a managed database, backups without a pause | the database `--store-dsn-file` names |

The schema is the gateway's own: it is brought up to date as the gateway
starts (`store migrate` does it alone), and a database whose schema is newer
than the gateway is refused rather than written to. `store status` shows
the schema version and what the database holds.

Each sandbox's row holds, besides its owner, node and size, its details as
its node last answered: name, image, state, labels, network, lifetime, end
and idle timeout, kept in step on every change made through the gateway and
by a refresh from each node every minute (the node stays the authority).
A terminated sandbox's row is kept as history. Processes, events, metrics
and files stay on the node.

**Who owns what.** Organisations and users are tables of their own, and
every record points at them by foreign key, so the database itself holds
whose each row is:

- `orgs`: every tenant is one. The default tenant is the organisation of
  kind `default` (its `key` is empty: `default` is a tenant name like any
  other). A tenant named by a key or an invite is a `personal` one, made
  with it; one made with `org create` is a `team` one, with members.
- `users`: a user is a name in a home organisation, the tenant of their
  keys (`org_id`). An account's address and password are on its user.
- `api_keys` belong to a user (`user_id`), and go with it; a sign-in's key
  also points at the account key it was made for (`parent`).
- `org_members`: a user in a team organisation, with a role.
- `sandboxes`, `volumes`, `snapshots`, `jobs` and `services` carry the
  organisation and the user they belong to (`org_id`, `user_id`); `secrets`
  and `job_runs` the organisation; `ssh_keys`, `user_policies` the user.

```sql
SELECT o.key AS org, u.name AS user, count(*) FROM sandboxes s
  JOIN orgs o ON o.id = s.org_id JOIN users u ON u.id = s.user_id GROUP BY 1, 2;
```

The API, the CLI and the SDKs still say *tenant*: a tenant is an
organisation's `key`.

**PostgreSQL, on this machine, in Docker:**

```sh
scripts/postgres-local.sh up      # postgres:17 on 127.0.0.1:5432; writes ~/.config/sandbox/gateway-db.dsn (0600)
sandbox-gateway --state-dir /var/lib/sandbox-gateway --store postgres \
  --store-dsn-file ~/.config/sandbox/gateway-db.dsn serve …
```

`deploy/postgres/compose.yaml` keeps the data in a volume, publishes on
loopback only, and makes two roles: an admin, and `sandbox_gateway`, the
gateway's own login, which owns its database and is neither a superuser nor
able to bypass row-level security. Their passwords are made on first use in
`deploy/postgres/secrets/`, readable by you only, and never committed.

**PostgreSQL elsewhere** (a managed database, or your own with a replica):
create the gateway's login role and its database, and the role its requests
take, once, as an administrator:

```sql
CREATE ROLE sandbox_gateway LOGIN PASSWORD '…';
CREATE ROLE sandbox_gateway_tenant NOLOGIN NOBYPASSRLS;
GRANT sandbox_gateway_tenant TO sandbox_gateway WITH ADMIN OPTION;
CREATE DATABASE sandbox_gateway OWNER sandbox_gateway;
```

Put its address in a file only the gateway's user can read:

```sh
sudo -u sandbox-gateway sh -c 'umask 077; printf "%s\n" \
  "postgres://sandbox_gateway:…@db.example.internal:5432/sandbox_gateway?sslmode=verify-full&sslrootcert=/etc/sandbox-gateway/db-ca.pem" \
  > /etc/sandbox-gateway/db.dsn'
```

The address is read from the file, never a flag: a password in a command
line is every local user's. A database not on this machine must be reached
with `sslmode=verify-full`; the gateway refuses anything less.

**Isolation, twice.** Every tenant's records carry their organisation, and
the gateway's own checks decide whose a request may touch, on either store.
On PostgreSQL the database holds the same line: a request's statements run
as `sandbox_gateway_tenant` with row-level security for the organisation it
acts in (`org_id = current_setting('app.org')`),
so a query that forgot its tenant returns nothing of another's. The
gateway's work across tenants — the scheduler, the job runner, admin calls
— runs as the owner of the tables, which row-level security does not
filter.

**One serving gateway, and standbys.** One gateway serves a database at a
time: it also holds things in its own memory (names being created, quota in
flight, open streams a revocation must end, the job runner and the service
controller), which two serving at once would each hold apart.

- **PostgreSQL:** start as many gateways on one database as you like, with
  the same flags and the same key files (`--secrets-key-file`, the SSH host
  key, TLS). One serves; the others log *standby: another gateway serves this
  database* and listen on nothing, so a load balancer's health check
  (`GET /v1/health`) finds them down and sends nothing their way. When the
  serving gateway stops, crashes or loses its connection to the database, a
  standby takes over within seconds with the whole state. A gateway that
  loses its hold stops serving at once and exits, for its service manager to
  restart it as a standby. Jobs' kept output (`--jobs-dir`) should be on a
  filesystem every gateway mounts.
- **SQLite:** a second gateway on the same database is refused.

The offline commands (`keys`, `policies`, `nodes`, `store`) hold nothing and
work beside the serving gateway.

**Upgrading to schema 3 (organisations and users as tables).** A database a
gateway made before this release has tenants and user names as text
columns. The upgrade moves every row into the new tables, in one
transaction: if any row cannot be placed, nothing changes and the gateway
says which table. Keys keep their ids and hashes, so every secret and
password handed out keeps working.

- **SQLite** copies the database aside first, as `gateway.db.before-0003`
  (mode 0600), and upgrades as the gateway starts.
- **PostgreSQL** refuses to move rows until you say you have a backup:

  ```sh
  pg_dump -Fc -f gateway-before-0003.dump "$(cat /etc/sandbox-gateway/db.dsn)"
  sudo systemctl stop sandbox-gateway     # every gateway on the database
  sudo -u sandbox-gateway sandbox-gateway --state-dir /var/lib/sandbox-gateway \
    --store postgres --store-dsn-file /etc/sandbox-gateway/db.dsn --store-migrate store migrate
  sudo systemctl start sandbox-gateway
  ```

  To go back, restore the dump with `pg_restore --clean` and start the
  previous release.

**From a state file.** A gateway from before databases kept a JSON state
file. Every command refuses to start on an empty database beside one, and
says how to import it:

```sh
sudo systemctl stop sandbox-gateway
sudo -u sandbox-gateway sandbox-gateway --state-dir /var/lib/sandbox-gateway [--store postgres --store-dsn-file …] \
  store import --from /var/lib/sandbox-gateway/state.json
sudo systemctl start sandbox-gateway
```

Keys keep their ids and hashes, so every secret handed out keeps working;
everything else is copied as it was. The import is one transaction, into a
database that holds nothing yet. Move the file aside once the gateway
serves.

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

**The database**, through the CLI or the admin API:

```sh
sudo -u sandbox-gateway sandbox-gateway --state-dir /var/lib/sandbox-gateway \
  nodes add n17 https://10.0.0.17:7443 \
    --token-file /etc/sandbox-gateway/n17.token \
    --ca-file /etc/sandbox-gateway/tls/ca.pem \
    --cert-file /etc/sandbox-gateway/tls/gateway-client.pem \
    --key-file /etc/sandbox-gateway/tls/gateway-client-key.pem
sudo -u sandbox-gateway sandbox-gateway --state-dir … nodes list
sudo -u sandbox-gateway sandbox-gateway --state-dir … nodes remove n17
```

`nodes add` reads every file it is given and refuses a node it could not reach
as configured. An endpoint is `https://host:port`, `unix:///path`, or plain
`http://` on loopback only.

### The first admin key

```sh
sudo -u sandbox-gateway sandbox-gateway --state-dir /var/lib/sandbox-gateway \
  keys create --user ops --scope admin
```

The secret (`sgk_…`) is printed once and stored only as a hash. Run `keys` and
`nodes` as the gateway's user: run as root, they would create a database file
the gateway cannot open. They work while the gateway serves, and what they
change holds at once: the gateway reads keys and policies on every request.

### Serve

```sh
sudo -u sandbox-gateway sandbox-gateway serve --state-dir /var/lib/sandbox-gateway \
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
sudo curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/systemd/sandbox-gateway.service \
  -o /etc/systemd/system/sandbox-gateway.service      # or cp it from a checkout
# set --ssh-public-host to the name users ssh to, as above
sudo sed -i 's/gateway.example.internal/gw.example.internal/' /etc/systemd/system/sandbox-gateway.service
sudo systemctl daemon-reload && sudo systemctl enable --now sandbox-gateway
```

| Flag | Default | |
|---|---|---|
| `--state-dir DIR` | required | the gateway's directory: its SQLite database, SSH host key, audit log, jobs and node files. (`--state FILE`, as gateways before databases took, names a file in it.) |
| `--store sqlite\|postgres` | `sqlite` | where its state is kept ([above](#the-gateways-database)). |
| `--store-path FILE` | `gateway.db` in `--state-dir` | the SQLite database. |
| `--store-dsn-file FILE` | | with `--store postgres`: a file, mode 0600, holding the database's address. |
| `--listen HOST:PORT` | `127.0.0.1:7443` | an address other machines can reach is refused without `--tls-cert` and `--tls-key`. TCP only. |
| `--tls-cert`, `--tls-key` | | the API's certificate and key (PEM). |
| `--node-config FILE` | | the node file above. |
| `--node-files-dir DIR` | `nodes/` in `--state-dir` | the only directory whose files a node added **through the admin API** may name. |
| `--ssh-listen HOST:PORT` | off | the SSH server. |
| `--ssh-host-key FILE` | `ssh_host_ed25519_key` in `--state-dir` | created 0600 when missing; one key for the whole fleet. |
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
| `--audit-log FILE` | `audit/gateway.jsonl` in `--state-dir` | who did what, as JSONL; `none` keeps no log ([operations.md](operations.md#the-audit-log)). |
| `--node-lost-after` | `5m` | how long a node may not answer before its sandboxes are reported lost and stop counting against quotas ([operations.md](operations.md#node-loss-and-lost-sandboxes)). |
| `--jobs-dir DIR` | `jobs/` in `--state-dir` | where jobs' runs keep output and files ([jobs.md](jobs.md)). |
| `--job-retention` | `24h` | how long a finished job, and what it kept, is kept. |
| `--sandbox-history` | `720h` (30 days) | how long a terminated sandbox's details stay in the database, as history; `0` keeps them. |

To serve SSH on port 22 or the API on 443, give the unit
`AmbientCapabilities=CAP_NET_BIND_SERVICE` (it is in the unit, commented out),
and move the host's own sshd elsewhere first.

The first log line says how many nodes answer; `GET /v1/health` needs no key,
for a load balancer's health check.

### Changing a serving gateway

Keys, policies and nodes change through the offline commands above, the
admin API — plain HTTP with an admin key — or `sandbox-cli gateway`. A node
added with the offline command is picked up by a serving gateway at its next
restart; through the admin API, at once. The calls, and why a node added at run time may name
files only under `--node-files-dir`, are in
[operations.md](operations.md#changing-a-serving-gateway).

## Giving users keys

```sh
sudo -u sandbox-gateway sandbox-gateway --state-dir … keys create --user alice --tenant team-a \
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

`sandbox-gateway keys` edits the state file, which the serving gateway holds
locked, so it works only with the gateway stopped. While it serves, the same
is `sandbox-cli gateway keys create|list|revoke` with an admin key, or Studio's
Users & keys screen:

```sh
# a demo user: a policy, and a key that works for a week
sandbox-cli gateway keys create --user bob --tenant bob \
  --scope sandbox:read --scope sandbox:create --scope sandbox:delete \
  --policy demo --expires 7d --invite-url https://studio.example.com
# a long-time user: neither
sandbox-cli gateway keys create --user carol --tenant carol \
  --scope sandbox:read --scope sandbox:create --scope sandbox:delete --scope sandbox:ssh \
  --invite-url https://studio.example.com
```

`--invite-url` also prints the link that signs the user in to a
[hosted Studio](studio.md#hosted-studio); an admin's signs in only where it
runs with `--allow-admin`, where the admin can do all of this from the Users &
keys screen instead.

### Key expiry

`--expires 7d` (or `12h`, or `never`, the default) makes a key stop working
then, exactly as if it were revoked then: its calls get `401`, a hosted
Studio session with it ends, its open streams and SSH connections close within
the recheck interval (30s), and its user's jobs are cancelled once they hold no
working key. Unlike a revoked key, an expired one can be renewed:

```sh
sandbox-cli gateway keys renew KEY_ID --expires 30d     # 30 days from now
sandbox-cli gateway keys renew KEY_ID --expires never
sandbox-cli gateway keys renew KEY_ID --expires now     # ends its access at once
```

`keys list` shows each key's EXPIRES and whether it is active, expired or
revoked. With the gateway stopped, `sandbox-gateway keys create --expires`
does the same on the state file.

### User policies

A node's `policy.yaml` is the same for everyone who reaches it. A user policy
is a set of limits the gateway holds a user's sandboxes to on top of it, so
one fleet can give a trial user 30 minutes and the admin no limit. For now a
policy holds a lifetime: how long each of the user's sandboxes may exist, busy
or not, from its creation.

```sh
sandbox-cli gateway policies set demo --max-lifetime 30m
sandbox-cli gateway policies assign bob demo --tenant bob     # or --policy demo at keys create
sandbox-cli gateway policies list
sandbox-cli update SANDBOX --lifetime +15m                    # one sandbox: +15m, -10m, or 45m from its creation
```

- A sandbox asking for no lifetime gets the policy's; one asking for more is
  refused. A policy is assigned to a user named with the tenant of their keys,
  and follows them into every organisation they act in, and into their jobs and
  services.
- A change to a policy holds for sandboxes created after it. A running one
  keeps its end, so an edit cannot end, or prolong, sandboxes in use; move those
  one at a time.
- Only an admin key may push a sandbox's end out. Its owner may bring it
  nearer, and is refused a later one: the policy is the operator's limit on the
  user.
- Admin keys hold no policy. A policy in use cannot be removed; assign its
  users another, or `--none`, first.
- The node's own `limits.max_lifetime_secs` still applies, and the strictest
  wins. To let an admin extend sandboxes past a policy, set the nodes' limit
  above it (or leave it unset) and let the policy carry the users' limit.

With the gateway stopped, `sandbox-gateway policies set|list|assign|remove` and
`keys create --policy` do the same on the state file.

### Allowlist groups

An allowlist group is a named set of hosts — Go modules, npm, an internal
registry — that an admin keeps and any user adds to a sandbox's allowlist by
name, instead of listing the hosts every time.

```sh
sandbox-cli gateway egress-groups set go --host proxy.golang.org --host sum.golang.org --description "Go modules"
sandbox-cli gateway egress-groups list                     # any key
sandbox-cli run --network allowlist --allow-group go -- go build ./...
sandbox-cli update SANDBOX --network allowlist --allow-group go --no-baseline
sandbox-cli gateway egress-groups remove go
```

- Only an admin key sets or removes a group; every key lists them, and hosted
  Studio's Network step offers them. A group widens what every user naming it
  reaches, so it is the operator's to say, as a policy is.
- The gateway turns names into hosts before a request reaches a node. Each
  node's `policy.yaml` still decides: a group host outside its `may_allow` is
  refused at launch, as the same host in `--allow` would be.
- An unknown group is refused, never dropped. Named with no other host and
  `--no-baseline`, the groups' hosts are the whole list.
- A change holds for sandboxes created after it. A running sandbox keeps the
  hosts it was given; a job's or a service's next sandbox gets the group as it
  is then. A group a running job or a service names cannot be removed.
- There is no default group: a launch reaches a group's hosts only by naming
  it, so editing a group never widens a launch that did not ask for it.

Groups are kept in the gateway's database. A state file has none, so
`sandbox-gateway store import` brings none.

### Accounts: invite by email, sign in with a password

A key is a good credential for a program and a poor one for a person: it has
to be sent to them, and wherever it was sent it keeps working until it is
revoked. An account lets a person sign in to hosted Studio
([studio.md](studio.md#hosted-studio)) with an email address and a password
instead.

```sh
# a demo user: 30-minute sandboxes, an account that lasts a week
sandbox-cli gateway invites create --email bob@example.com --user bob \
  --scope sandbox:read --scope sandbox:create --scope sandbox:delete \
  --policy demo --expires 7d --invite-url https://studio.example.com
sandbox-cli gateway invites list                 # sent and not yet used
sandbox-cli gateway invites revoke INVITE_ID
sandbox-cli gateway keys reset KEY_ID --invite-url https://studio.example.com   # a forgotten password
```

Or in Studio, under Users & keys: type the address in **Add a user** and
press **Create invite**; **Email invite** opens your own mail program with the
message and link written.

- **Its organization.** An invite naming no tenant is a sign-up: the
  account gets an organization of its own, named after its user (`bob`;
  `john.smith` gets `john-smith`), with its own sandboxes, secrets, jobs,
  services and quota. It is refused when that name is a tenant someone is
  in already, a team organization, or one another pending invite names,
  rather than putting a stranger beside them. `--tenant NAME` puts the
  account in an existing tenant, `--tenant default` in the default one.
- **The invite** is a link, `https://studio…/#invite=sgi_…`, shown once and
  stored only as its hash. It works once, for two days (`valid_secs` in the
  API, at most seven), so a leaked email is worth nothing once it was used or
  has lapsed. A second invite for the same address replaces the first.
- **Opening it**, the person chooses a password (10 to 256 characters) and is
  signed in. The account is a key with the user, tenant, scopes, policy and
  lifetime the invite named, plus the email and the password's PBKDF2-SHA256
  hash; its own secret is never shown. It is listed by `keys list` (EMAIL) and
  renewed, expired and revoked like any key, and a job of the user's keeps
  running while they are signed out, as it would for a key.
- **Each sign-in** gets a key of its own, for at most as long as the session
  (12 hours unless Studio's `--session-max-age` says otherwise) and never past
  the account's end. Signing out ends it; revoking or expiring the account
  ends them all within the recheck interval. Sign-ins' keys are not listed.
- **A forgotten password** is reset with a new link (`keys reset`, or Reset
  password… in Studio): the old password works until it is used, and every
  sign-in ends when it is. A user changes their own password on the Account
  page, which ends their other sign-ins.
- **Guessing is held back.** Five failures in a row lock an address for 15
  minutes, and failures across all addresses are rate-limited; an unknown
  address takes as long to refuse as a wrong password, and gets the same
  answer. Each check is a PBKDF2 hash, at most two at a time.
- **There is no sign-up without an invite.**

**Sending the email from the gateway.** Without a mail relay the admin sends
the link (Studio's **Email invite** opens their own mail program). With one,
the gateway sends it when asked (`--send`, or **email the invite from the
gateway** in Studio, on by default when a relay is set), and resets too.
Any SMTP relay works: your own mail server, or an email-sending service's
SMTP relay, whose user name and API key go in the flags below.

```sh
umask 077; printf '%s\n' '<the relay password or API key>' > /var/lib/sandbox-gateway/smtp.key
sandbox-gateway --state-dir … serve … \
  --smtp-host smtp.example.com --smtp-port 465 --smtp-user <user> \
  --smtp-password-file /var/lib/sandbox-gateway/smtp.key \
  --mail-from 'Sandbox Studio <invites@your-domain.example>' \
  --studio-url https://studio.example.com
```

- `--studio-url` is hosted Studio's public address: every emailed link is it
  followed by `/#invite=…`, so it must be what the recipient can open.
- Port 465 is TLS from the start; any other port must offer STARTTLS, and
  a relay that offers neither is refused unless it is on loopback.
- The password file is refused if others can read it; the password and the
  links appear in no log. The sender must be an address the relay lets you
  send from (with a sending service, one on a domain you have verified).
- A send that fails leaves the invite usable: the answer says why
  (`email_error`), and the link is shown to the admin to send by hand.

### Personal API keys

A user makes keys of their own for their programs — a CI job, a script, the
CLI on another machine — under **API keys** in Studio's sidebar, or with
`POST /v1/keys` ([api/v1.md](api/v1.md#personal-api-keys)). Signed in with
a password, that is how a person gets a key at all without asking an admin,
and without handing their password to a program.

- **Less than its maker.** Its scopes are ones its maker holds, never
  `admin` (a super admin's keys are issued with `keys create`). Its user's
  policy applies to it, as to everything they do: a policy is the user's,
  not a key's.
- **It always ends**: in an hour to 366 days, and never after the account
  or key it was made under. An admin shortening that account shortens it
  too, and renewing it alone cannot take it past the account.
- **It ends with its account.** Revoking, suspending or expiring the account
  (or the plain key it was made with) ends it. A suspension ends it for
  good: **Restore** brings back the account, not its personal keys. An
  admin's password reset revokes them, since a password taken may have made
  some; signing out and a change the user makes, knowing the password, do
  not.
- **It manages nothing.** A personal key makes, lists and revokes no keys,
  and is not its account: it cannot change the password. A leaked one is
  worth what its scopes are, until it expires or is revoked.
- **At most 20** live at once per account. The secret is shown once; the
  user lists, and revokes, their own, and the super admin sees every one
  on the Users screen (`personal key · NAME`) and can revoke it there.

### Revoking

**Studio's Revoke suspends.** It asks first, then calls
`POST /v1/admin/keys/{id}/suspend` (`sandbox-cli gateway keys suspend ID`):
the user is refused from that moment — signing in, their keys, SSH — their
sign-ins end and their running jobs are cancelled, exactly as a revocation
does. For an hour the super admin can take it back (**Restore**, `gateway keys
restore ID`); after that it is a revocation for good. Until then the user's
address and name stay theirs, so an invite cannot take them. `keys revoke`
and `DELETE` revoke for good at once.

**One kind of account.** Studio's Users screen gives every account the same
scopes — everything in its own organization (`sandbox:read`, `create`,
`delete`, `ssh`, `secrets:write`, `org:create`) — so each user is the admin of
their own organization and of nothing else. The `admin` scope is the super
admin's, gateway-wide, and is given only from the CLI (`gateway keys create
--scope admin`); only a super admin sees the Users screen.

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
- **Passwords are only for people, and only through an invite.** The sign-in
  routes are the only ones without a key; they are rate-limited and lock an
  address after repeated failures, and a password is stored as a salted
  PBKDF2 hash. An account's sign-ins end with it.
- **Secrets travel by reference.** API keys, invites and SSH tokens are random
  256-bit strings stored only as their SHA-256; logs name a key by its id. The state
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

A node's images are its disk, shared by every tenant on it, so through a
gateway only an admin manages them, once for the fleet:

```sh
sandbox-cli gateway images pull ghcr.io/amitgb14/sandbox-desktop:edge   # every node taking new sandboxes
sandbox-cli gateway images ls                                           # each image, and which nodes have it
sandbox-cli gateway images rm python:3.13-slim                          # from every node that has it
```

- **A pull** goes to every healthy node that is not cordoned, and each node's
  own policy decides whether it may install the image. A node that refuses is
  named in the answer; the others go ahead.
- **A removal** is refused, removing nothing, while any node still needs the
  image: a sandbox there starts from it, it is the node's default or pooled
  image, or it is being installed. Then it goes to every node that has it.
- **Studio's Images screen** is the same for an admin key, with where each
  image is. Any other key sees what the nodes have built, read-only
  (`GET /v1/fleet/images`). The plain `/v1/images` routes and `sandbox-cli
  image` stay `501 unsupported` through a gateway, which does not report the
  `images` capability.
- A node added later has none of them until a sandbox asks or the admin
  pulls again. Sandboxes still prefer a node that has their image (below).

The routes are in [api/v1.md](api/v1.md).

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

The database holds every key and every ownership record: losing it leaves
running sandboxes belonging to nobody, reachable only on their node.
`/var/lib/sandbox-gateway/` also holds the SSH host key, whose loss makes
every user's `ssh` warn that the host changed, and the secrets key file must
be kept with the database: without it the sealed secrets do not open.

- **SQLite:** `sqlite3 /var/lib/sandbox-gateway/gateway.db ".backup /backup/gateway.db"`
  while the gateway serves (a consistent copy), or copy `gateway.db` while it
  is stopped.
- **PostgreSQL:** `pg_dump -Fc sandbox_gateway > gateway.dump`, or a managed
  database's point-in-time recovery. Restore with `pg_restore` into an empty
  database owned by the gateway's role.

Restore one to a scratch directory now and then, and start a gateway on it
with no nodes: `store status` and `keys list` show what came back.

## Not done yet

- **One serving gateway per database.** Others on the same PostgreSQL are
  standbys that take over, not gateways serving beside it: requests are not
  spread across gateways yet.
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
