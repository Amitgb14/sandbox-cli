# SDKs

Clients for [Sandbox API v1](../docs/api/v1.md). They speak to any endpoint —
local, self-hosted or cloud — and learn what it offers from `capabilities()`.

| SDK | Status | Tested |
|---|---|---|
| [`python/`](python/) (`sandboxapi`) | standard library only; unix socket, http(s), private CA | yes: `make test-sdk` runs it against a real `sandboxd` |
| [`typescript/`](typescript/) (`sandboxapi`) | `fetch`-based: Node 18+, Deno, Bun, browsers; http(s) endpoints | **not yet run**: written to the same contract, no Node in the development environment that wrote it |

Both cover sandboxes, processes (run, background, output streaming, stdin,
signals), files, suspend/resume and snapshots. Attach and
tunnels (connection upgrades) are in the Go client and the CLI only, for now.

Against a gateway in front of many sandboxd nodes, both also have its
user-facing calls (a plain sandboxd answers each with `not_found`):

| Python | TypeScript | |
|---|---|---|
| `whoami()` | `whoami()` | the user, tenant, scopes and key id behind the API key |
| `ssh_info()` | `sshInfo()` | the SSH server's host and port, and the host keys to pin |
| `add_ssh_key(key, sandbox="")` | `addSSHKey(key, sandbox?)` | register an authorized_keys line; `sandbox` limits it to one |
| `ssh_keys()` | `sshKeys()` | your registered keys |
| `remove_ssh_key(id)` | `removeSSHKey(id)` | |
| `ssh_access(ref, ttl_secs=0)` | `sshAccess(ref, ttlSecs?)` | a short-lived login; its `user` is the token, and the whole credential until `expires_at` |

And its jobs, agent runs and secrets — work the gateway runs after the
request that started it has gone (README, "Agents and jobs on a fleet"):

| Python | TypeScript | |
|---|---|---|
| `create_job(spec)` | `createJob(spec)` | start a job: `command` or `agent` + `prompt`/`prompts`, `parallelism`, `completions`, `retries`, `timeout_secs`, `env`, `secrets`, `keep`, `notify`, … |
| `agent_run(agent, prompt, **opts)` | `agentRun(agent, prompt, opts?)` | a job of one agent run |
| `jobs()` | `jobs()` | your jobs, newest first, without their runs |
| `job(id)` | `job(id)` | one job, with each run's state, exit code, attempts and sandbox |
| `cancel_job(id)` | `cancelJob(id)` | runs not started never are; running ones' sandboxes are terminated |
| `job_output(id, run)` | `jobOutput(id, run)` | what a run kept of its stdout and stderr |
| `job_file(id, run, path)` | `jobFile(id, run, path)` | a file a run kept (`keep.files`) |
| `set_secret(name, value)` | `setSecret(name, value)` | set one of the tenant's secrets; never returned |
| `secrets()` | `secrets()` | the tenant's secrets by name |
| `delete_secret(name)` | `deleteSecret(name)` | |

And organizations (docs/fleet.md, "Organisations"). A client made with
`org="acme"` (Python) or `{ org: "acme" }` (TypeScript), or derived with
`with_org("acme")` / `withOrg("acme")`, sends `X-Sandbox-Org: acme` on every
request, so everything it does acts in that organization; the gateway answers
`not_found` for one the key's user is not a member of.

| Python | TypeScript | |
|---|---|---|
| `orgs()` | `orgs()` | the organizations the key may act in, its own tenant first (`default` when unnamed) |
| `create_org(name)` | `createOrg(name)` | needs `org:create`; you become its owner |
| `org_members(org)` | `orgMembers(org)` | any member may list them |
| `set_org_member(org, user, role="", tenant="")` | `setOrgMember(org, user, {role?, tenant?})` | owners add a member or change a role |
| `remove_org_member(org, user, tenant="")` | `removeOrgMember(org, user, tenant?)` | owners; what the member had open there ends at once |

The gateway's admin calls (API keys, nodes, cordon) are in the Go client only;
`admin_orgs()` / `adminOrgs()` lists every organization for an admin key.

Byte fields are bytes (`bytes` / `Uint8Array`); the wire format's base64 is
handled inside. A sandbox has no repository: put code in with the files calls,
or clone it inside the sandbox.
