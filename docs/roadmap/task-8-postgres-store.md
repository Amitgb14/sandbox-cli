# Task 8 — the gateway's state in PostgreSQL, so several gateways can serve one fleet

**Status:** proposed 2026-10-04, not scheduled. Nothing here is built.
**Builds on:** [task 7](task-7-fleet-gateway.md) (the gateway) and
organisations (#204).
**Needs a decision first:** a new Go dependency, the PostgreSQL driver
(decision 1). `AGENTS.md` allows none without one recorded in
[`../rewrite/PLAN.md`](../rewrite/PLAN.md).

## Why

Everything `sandbox-gateway` must keep across a restart is in one JSON file
(`--state`, `FileStore` in `internal/gateway/store.go`): API keys (hashes),
SSH keys and tokens (hashes), organisations and members, the owner and node
of every sandbox, volume and snapshot, nodes added at run time, sealed
secrets, jobs, and services. It is written atomically, mode 0600, under an
exclusive lock.

That is right for one gateway, and stays the default. It stops being enough
when:

1. **More than one gateway serves the fleet**, for availability or behind a
   load balancer. The lock is one machine's; two gateways on one file would
   overwrite each other's changes, and a revoked key could come back.
2. **The change rate is high.** Every change rewrites the whole file: at a
   thousand sandboxes created a minute, that is the bottleneck.
3. **Operators want backups, queries and reporting** without stopping the
   gateway.

**What a database does not change:** isolation. Tenants and organisations are
isolated today by the gateway's own checks — every record carries its tenant,
and `Router.Resolve` and the request guard check ownership before anything
reaches a node — and by each sandbox being its own microVM. Those checks stay
exactly where they are. The database adds a second wall (row-level security,
below), not the first one.

## Shape

```
              load balancer (TCP for SSH, HTTPS for the API)
                │                     │
        sandbox-gateway #1    sandbox-gateway #2   … (stateless, any number)
                │   \               /   │
                │    PostgreSQL (one primary + replicas, operator-run)
                │                       │
             nodes (sandboxd) ── the same mutual TLS + token as today
```

- `sandbox-gateway serve --store postgres --store-dsn-file FILE`. The DSN is
  read from a file, never an argument: a password in an argv is visible to
  every user on the machine (rule 6).
- Without `--store`, the file store, as today. Nothing changes for one machine
  or a laptop.
- TLS to the database is required unless it is on loopback or a unix socket
  (`sslmode=verify-full`); a DSN that asks for less is refused (rule 7, fail
  closed).

## The schema

One table per kind of record in `fileState`. Every record that belongs to a
tenant has a `tenant text NOT NULL` column (`''` is the default tenant), and
every uniqueness rule that is per tenant today is a constraint that includes
it.

| Table | Key | Notes |
|---|---|---|
| `api_keys` | `id` | `hash` unique, `user`, `tenant`, `scopes text[]`, `revoked_at` |
| `ssh_keys` | `id` | `fingerprint` indexed, `user`, `tenant`, `sandbox` |
| `ssh_tokens` | `hash` | `expires` indexed; expired rows deleted by a sweep |
| `orgs` | `name` | case-insensitive unique (`lower(name)`) |
| `org_members` | `(org, user, home_tenant)` | `role`; at least one owner, enforced in the transaction |
| `sandboxes` | `id` | `tenant`, `user`, `node`, `name`, `cpus`, `memory_mb`, `disk_mb`, `recorded`; unique `(tenant, user, name)` where live — this replaces the in-memory name claim |
| `volumes`, `snapshots` | `id` | owner and node |
| `nodes` | `name` | endpoint and file paths for nodes added at run time; `want_cordon` (drain.go) |
| `secrets` | `(tenant, name)` | `sealed bytea`, sealed by the gateway before it leaves the process |
| `jobs`, `job_runs` | `id`, `(job, n)` | spec, state, attempts, error |
| `services`, `service_replicas`, `retire_queue` | | the controller's state |
| `schema_version` | | migrations |

**Secrets stay sealed by the gateway** with `--secrets-key-file`, AES-256-GCM
as today. The database holds ciphertext, so a dump, a replica or a backup does
not reveal a value. Every gateway gets the same key file.

**Migrations** are SQL files embedded in the binary, applied in order inside a
transaction, under an advisory lock so two gateways starting together do not
race. A gateway refuses a database whose schema is newer than it knows,
rather than writing to it.

## Row-level security: the second wall

Two database roles:

- `gateway_tenant`, **with row-level security enforced**. Every request a user
  makes runs its queries in a transaction that first does
  `SET LOCAL app.tenant = $principal_tenant`, and every tenant-scoped table has
  the policy `USING (tenant = current_setting('app.tenant'))`. A query that
  forgets its tenant filter returns nothing, rather than another tenant's
  rows.
- `gateway_system`, with `BYPASSRLS`, for what is not one tenant's: looking a
  key up by its hash (before the tenant is known), the scheduler and node
  pool, reconciling against nodes' listings, the job runner and service
  controller loops, and admin endpoints.

Which role a code path uses is decided in one place, the store's constructor
for a request (`store.ForTenant(p)`) versus the system handle. A test runs
every request-path store method under `gateway_tenant` with another tenant
set and checks it sees nothing.

## What has to move out of one gateway's memory

The file store is not the only state. Some lives only in the process, and
with two gateways each would have its own copy:

| Today, in memory | With several gateways |
|---|---|
| name claims (`claimed`) for sandboxes and volumes being created | the unique constraint on `sandboxes(tenant, user, name)`; a clash is a 409 from the insert |
| quota in flight (`inflight`, `quotaMu`) | a row per tenant locked `FOR UPDATE` in the create's transaction, or a `SERIALIZABLE` check-and-insert |
| node capacity reservations and credits (`placed`, `schedMu`) | stays per gateway: each places against the node's own status; a node refuses what it has no room for, and the gateway retries the next node (`neverSent`). Two gateways can overcommit a node by one interval at most — the node is the authority, as it is today |
| the open-request registry (`live`), SSH connections | stays per gateway — they are that gateway's connections. A revocation must reach all of them: `NOTIFY access_changed` on revoke, SSH key removal and member removal; every gateway `LISTEN`s and runs `accessChanged()`. The 30-second recheck stays as the backstop if a notification is missed |
| tombstones of terminated sandboxes | a table with a TTL, or accepted per gateway (they only shape a 404's wording) |
| the job runner and service controller | **one leader at a time**, chosen by a PostgreSQL advisory lock held on a dedicated connection; the others stand by and take over when the lock frees (the leader died). Work they do is already resumable from stored state |
| `wantCordon` (the cordons the gateway asked for) | the `nodes` table |

**Files beside the state file** also have to be shared:

- **What jobs kept** (`--jobs-dir`: output and files, kept for a day) — decision 3.
- **The SSH host key** — every gateway must present the same one, or clients
  see a changed host key behind the load balancer. Same file on each, as with
  the secrets key.
- **The audit log** (JSONL per gateway) — decision 4.

## Phases

1. **One store interface.** `g.store` is the concrete `*FileStore` today, with
   about 70 methods across `store.go`, `orgs.go`, `jobs.go`, `secrets.go`,
   `services_store.go` and `lost.go`. Extract a `Store` interface covering all
   of them and make the gateway use only it. No behaviour change; a pure
   refactor with the existing tests.
2. **A store conformance suite.** One set of tests that every store must
   pass, run against the file store first: ownership, uniqueness, the last
   owner, quotas under concurrent creates, revocation, secrets sealed at
   rest, an old state file loading. This is what "the same with either store"
   means, as the API's conformance suite is for endpoints.
3. **The PostgreSQL store**, behind the interface, passing the suite
   (`SANDBOX_TEST_POSTGRES_DSN=… go test -tags postgres ./internal/gateway/...`;
   skipped without it). Migrations, the two roles and row-level security,
   with the "a forgotten filter returns nothing" test.
4. **Several gateways.** `LISTEN/NOTIFY` for access changes, the leader lock
   for the job runner and service controller, the shared files. An e2e run
   with two gateways on one database and a client switching between them:
   create on one, revoke on the other, the session on the first ends.
5. **Moving over.** `sandbox-gateway store import --from state.json` copies a
   file store into an empty database in one transaction, refusing a database
   that already holds data; `store export` writes a file store back, for
   leaving. Documented in `docs/fleet.md` with backups (`pg_dump`, point-in-time
   recovery) and what each gateway needs.

Phases 1 and 2 are useful on their own and need no decision. Phase 3 needs
decision 1.

## Decisions for the maintainer

1. **The driver.** Recommended: `github.com/jackc/pgx/v5`, the actively
   maintained, pure-Go driver most Go services use, through its own pool
   (`pgxpool`) for `LISTEN/NOTIFY` and the advisory-lock connection. The
   alternative, `lib/pq`, is in maintenance mode. Pin the newest release that
   builds with Go 1.25, as with x/crypto. Recorded as an exception in
   `AGENTS.md` beside x/crypto/ssh: `internal/gateway` (its store) only.
2. **Row-level security: on, as described,** or the gateway's checks alone.
   Recommended on: it costs one `SET LOCAL` per request transaction, and it is
   what stops a future query bug from becoming a cross-tenant leak.
3. **Where jobs' kept output goes with several gateways.** (a) A shared
   filesystem mounted at `--jobs-dir` on every gateway — simplest, nothing new
   in the code. (b) An S3-compatible bucket — `_old/` has S3 snapshot storage
   to port from. (c) In PostgreSQL as `bytea`, capped — no new service, but it
   grows the database. Recommended: (a) to start, (b) for a cloud.
4. **The audit log.** (a) Stays JSONL, one file per gateway, shipped by the
   operator's log pipeline. (b) A table, queried by `GET /v1/admin/audit`
   across every gateway. Recommended (b): the admin endpoint and Studio's Audit
   screen would otherwise show only the gateway that answered.
5. **The default stays the file store.** Recommended yes: one machine, or a
   few nodes with one gateway, needs no database.

## Not in this task

- Sharding one fleet across several databases.
- Running PostgreSQL for the operator: they bring it (a managed service, or
  their own with a replica).
- A multi-region gateway.

## Effort

Phases 1–2: about a day, no new dependency. Phase 3: two to three days.
Phase 4: two to three days, most of it the e2e run with two gateways.
Phase 5: a day.
