# Operating a fleet

Running a gateway after it is set up: watching it, reading what happened,
taking nodes out for maintenance, what happens when one is lost, ending a
user's access, and changing the gateway while it serves. Setting it up is
[fleet.md](fleet.md); a node's own side is
[self-hosting.md](self-hosting.md).

Most of this needs an admin key. `sandbox-cli gateway` is the admin's
command for nodes, lost sandboxes and the audit log; keys and nodes added at
run time go through the [admin API](#changing-a-serving-gateway).

## Metrics

Both the gateway and each node serve Prometheus metrics, in the text format,
on a loopback address:

```sh
sandbox-gateway serve … --metrics-listen 127.0.0.1:9101
sandboxd … --metrics-listen 127.0.0.1:9100
curl -s 127.0.0.1:9101/metrics
```

The endpoint has no credential, so each refuses any address but loopback;
put a proxy with its own authentication in front if the scraper is elsewhere.
No label carries a user, a tenant or a sandbox's name.

| On the gateway | |
|---|---|
| `sandbox_gateway_http_requests_total` | requests by route and status |
| `sandbox_gateway_sandboxes_created_total`, `sandbox_gateway_create_seconds` | creates, and their latency |
| `sandbox_gateway_creates_refused_total` | creates refused for quota or capacity |
| `sandbox_gateway_scheduled_total` | the scheduler's decisions |
| `sandbox_gateway_sandboxes_recorded`, `sandbox_gateway_sandboxes_lost`, `sandbox_gateway_sandboxes_terminated_total` | sandboxes the gateway holds owners for, those on lost nodes, and terminations |
| `sandbox_gateway_node_up`, `sandbox_gateway_node_cordoned`, `sandbox_gateway_node_running` | each node's health, cordon and sandboxes running |
| `sandbox_gateway_node_capacity_*`, `sandbox_gateway_node_free_*` | each node's CPUs, memory and disk, offered and free |
| `sandbox_gateway_ssh_connections_open`, `sandbox_gateway_ssh_sessions_open`, `sandbox_gateway_ssh_auth_failures_total` | SSH connections, sessions and failed logins |

| On a node | |
|---|---|
| `sandboxd_sandboxes` | sandboxes by state |
| `sandboxd_processes_running` | processes running |
| `sandboxd_pool_target`, `sandboxd_pool_ready`, `sandboxd_pool_booting` | pool sizes by image |
| `sandboxd_capacity_*`, `sandboxd_free_*`, `sandboxd_cordoned` | capacity and free, and the cordon |
| `sandboxd_creates_total`, `sandboxd_create_seconds` | creates by status, with a latency histogram |

## The audit log

A node's audit log says what happened to each sandbox
([self-hosting.md](self-hosting.md#the-audit-log)); it cannot say who asked,
because every request reaches it with the gateway's token. The gateway keeps
its own: every authenticated API request and every SSH login and session,
one JSON line each.

```sh
sandbox-cli gateway audit --since 1h        # an admin key; oldest first
curl -sS --cacert ca.pem -H "$AUTH" "$GW/v1/admin/audit?since=2026-10-04T09:00:00Z&limit=500"
```

- **Where.** `--audit-log FILE`, by default `audit/gateway.jsonl` beside the
  state file; `--audit-log none` keeps no log, and `GET /v1/admin/audit`
  then answers `501`. The file is 0600 and rotates as a node's does: at
  8 MiB, keeping five old generations.
- **What it names.** A credential by its key id, an SSH key by its
  fingerprint, a token login as `token`. Never a secret, a request body, a
  query string, a header or an SSH user name, any of which may carry one (a
  token login's user name is the token).
- **What it records** beyond requests: `ssh.login`; `api.revoked`,
  `ssh.revoked` and `job.revoked` when revocation ends something
  ([below](#revoking)); `drain.terminate`; and the organisation changes
  `org.created`, `org.member_added`, `org.member_role` and
  `org.member_removed`.
- **Best-effort**, as a node's: a request is not refused because its record
  could not be written.

Studio's **Audit** screen, for an admin key, reads the same log.

## Cordon and drain

```sh
sandbox-cli gateway nodes                   # health, cordon, sandboxes running, sandboxd version
sandbox-cli gateway cordon n17              # no new sandboxes there; what runs carries on
sandbox-cli gateway drain n17               # cordon; prints how many sandboxes still run there
sandbox-cli gateway drain n17 --terminate   # ...or end them now
sandbox-cli gateway uncordon n17
```

**Cordon** a node to stop new sandboxes landing there while those running
carry on. **Drain** cordons it and reports what still runs; run it again
until it says 0, or pass `--terminate` to end them (each is
`drain.terminate` in the audit log). The node holds its own cordon in
memory, so a restart of the node clears it — but the gateway remembers that
it cordoned the node, puts the cordon back on its next poll, and places
nothing there in between, so a drained node takes new sandboxes only once
you uncordon it.

Without the CLI, the same calls are `POST /v1/admin/nodes/{name}/drain` with
`{"terminate": false|true}` and `POST /v1/admin/nodes/{name}/cordon` with
`{"cordoned": true|false}`. Upgrading a node one at a time — cordon, drain,
upgrade, uncordon — is in
[self-hosting.md](self-hosting.md#upgrading-nodes-behind-a-gateway).

## Node loss and lost sandboxes

A node that stops answering is not drained:

- **After three failed polls** (15 s at the default `--poll-interval`) it
  takes no new sandboxes; calls on its sandboxes answer `503 unavailable`,
  and they are missing from listings until it answers again. A service's
  replicas on it are replaced elsewhere at once
  ([services.md](services.md#placement-and-lost-nodes)).
- **After `--node-lost-after`** (5 minutes by default) its sandboxes are
  listed by `sandbox-cli gateway lost` (`GET /v1/admin/lost`), with their
  user, tenant and since when the node has been down, and they stop counting
  against their tenants' quotas.
- **They are not reported terminated,** because the node may come back with
  them running; when it answers again they are reconciled from its own
  listing.

Ownership is also reconciled every minute: the record of a sandbox its node
no longer runs (an idle timeout, a node restart) is forgotten, and stops
counting against its tenant's quota. Removing a node keeps its sandboxes'
owner records, for if it comes back. Studio's **Lost sandboxes** screen is
the same list.

## Revoking

Revoking a key (`DELETE /v1/admin/keys/{id}`) acts on what is already running,
not only on what starts next, before the call returns:

- **Open API requests.** Every request on a sandbox that is still open — an
  attached terminal (`sandbox-cli attach`, `shell` against the API), a followed
  output stream (`sandbox-cli logs`), a tunnel, a `run` waiting on its command,
  a file transfer — is held to its key again, and ended if the key is revoked,
  gone from the state, or no longer holds the scope the request needed. The
  request to the node is cancelled and, for an attach or a tunnel, both the
  client's and the node's connections are closed. The client sees its stream
  cut off; one ended before the node answered gets `401`. Another key of the
  same user is not affected.
- **SSH.** Every open SSH connection whose user no longer holds an active key
  with `sandbox:ssh` (or `admin`) in that tenant is closed, its sessions and
  forwards with it, and the processes they ran are hung up. A connection made
  with an `ssh-access` token is held to the same check: it stays while its
  user still may use SSH. Removing an SSH key (`sandbox-cli ssh-key rm`, or
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
- **The audit record** says what revocation ended: `api.revoked` (result
  `closed`) per open API request, with its key id, sandbox, node and route
  (`target`, e.g. `GET /v1/sandboxes/{ref}/processes/{pid}/output`);
  `ssh.revoked` (result `closed`) per connection, with the credential's key id
  and fingerprint; and `job.revoked` (result `cancelled`) per job, with its id.
  Never a secret.

Every API request checks its key as it arrives, so a revoked key's next call
is refused. The gateway also rechecks open API requests, open SSH connections
and running jobs every 30 seconds, and running jobs at start, for a state file
changed while it was stopped (`keys revoke` with the gateway down).

Removing a member from an [organisation](organizations.md#what-is-guaranteed)
ends what they had open in it the same way.

## Changing a serving gateway

While the gateway serves it holds the state file, and `sandbox-gateway keys`
and `nodes` refuse with *another process holds …state.json.lock*: a change
made beside a running gateway would be overwritten by it, and a revoked key
would come back. The admin API is plain HTTP with an admin key
([api/v1.md](api/v1.md#gateway-only-endpoints)):

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
file the gateway can. A node defined in the node file is changed by editing
the file and restarting, never through the admin API.

`sandbox-cli gateway` covers nodes, cordon, drain, lost sandboxes and the
audit log; API keys have no `sandbox-cli` command, so issue and revoke them
with `curl` as above, with `sandbox-gateway keys` while the gateway is
stopped, or in Studio's **Users & keys** screen. The Go client
(`internal/api`) has every admin call.
