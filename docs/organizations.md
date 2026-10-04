# Organizations

An organisation is a tenant that users create and share, rather than one the
operator writes on their keys. It is the same tenant everything else is keyed
on, so it has all of a tenant's guarantees: its sandboxes, volumes,
snapshots, secrets, jobs and services are its own, it has its own quota, and
its services are routed as `<service>--<org>.DOMAIN`. Nothing in one is
visible or reachable from another, or from its members' own tenants.

Organisations exist only on a gateway ([fleet.md](fleet.md)); a plain
`sandboxd` ignores everything on this page. The endpoints are in
[api/v1.md](api/v1.md#organisations).

## The model

```sh
sandbox-cli org create acme              # needs org:create; you become its owner
sandbox-cli org members add bob          # bob, of your own tenant (--tenant T for another; --role owner)
sandbox-cli org use acme                 # this context now acts in acme
sandbox-cli run --keep --name web -- …   # made in acme, counted against acme's quota
sandbox-cli --org default ls             # one command in your key's own tenant
sandbox-cli org ls                       # yours, * on the current one
sandbox-cli org members                  # anyone in it may list them
sandbox-cli org members rm bob           # bob's open streams and SSH sessions in acme end now
```

**Inside one,** sandboxes are still their user's, as within any tenant: two
members do not see each other's sandboxes, while secrets and services are
the organisation's, as they are a tenant's.

## Choosing one: the X-Sandbox-Org header

Every API request may carry `X-Sandbox-Org: NAME`. Without
it a request acts in its key's own tenant, as before organisations existed.
With it, the gateway checks once, as the request is authenticated, that the
key's user is a member of NAME, and the whole request then acts in NAME: the
router, every listing and lookup, the quota, the audit record. The key's own
tenant is always allowed; the default tenant (keys issued with no tenant) is
called `default`. Not a member and no such organisation are the same answer,
`404 not_found` "no such organization", so names cannot be probed. The CLI
sends the header from `--org`, else `SANDBOX_ORG`, else the context's
(`org use`, or `context add --org`); the SDKs take `org`.

The header given twice is `400 invalid_request`. `GET /v1/whoami` answers
with `org`, the organisation the request acts in after the header, beside
`tenant`, the key's own.

## Creating, switching and members

**Who is in one.** Memberships come from two places only: creating an
organisation, which makes you its owner, and being added by one of its
owners. A key issued before organisations has none, and can select nothing it
could not reach already. A member is a user and the tenant of their own keys
(a user name is unique only within a tenant), so `org members add` takes
`--tenant` for someone from another tenant; a second user of the same name
in one organisation is refused, because ownership inside it is keyed on the
name. A key an operator issues with an organisation's name as its tenant is
in that organisation from the start, as its own tenant, and can be made an
owner like anyone else. Owners add and remove members and change roles (`owner` or `member`);
members may list them. The last owner cannot be removed or demoted. There is
no deleting an organisation yet.

**Switching.** `sandbox-cli org use NAME` makes a context act in NAME from
then on; `--org NAME` (or `SANDBOX_ORG`) does it for one command, and
`--org default` reaches the key's own tenant when that is not named.
`sandbox-cli org ls` lists the key's own tenant first and every organisation
its user belongs to, with `*` on the current one.

**Names.** An organisation name is a DNS label: 1 to 30 lowercase letters,
digits and dashes, starting with a letter, with no `--` (the router splits
`<service>--<org>` on it) and not `default` or `admin`. It may not be a
tenant already in use — by a key, a sandbox, a volume, a secret, a job or a
service, compared without case — or creating it would make its creator a
member of someone else's tenant.

## What is guaranteed

**Leaving ends access at once.** Removing a member, before the call returns,
ends what they had open in that organisation, as revoking a key does
([operations.md](operations.md#revoking)): open forwarded API requests (`api.revoked`), SSH
connections to its sandboxes (`ssh.revoked`), and their running jobs there
(`job.revoked`); their services there stop being routed and get no new
replicas. What they hold in their own tenant is untouched.

**The header cannot loosen anything else.** Scopes are the key's whatever it
selects: a member's key cannot reach the admin endpoints in an organisation
any more than outside one. An admin key may act in any organisation, or the
default tenant, as it may already act on every sandbox.

**An organisation is chosen, never assumed.** `X-Sandbox-Org` selects a
tenant only for a key whose user is a member, checked once per request
before any handler runs; memberships come only from creating one or being
added by an owner, and an organisation's name cannot be a tenant already
in use.

**The audit record** names each change by key id, user and organisation:
`org.created`, `org.member_added` and `org.member_role` (result: the role),
`org.member_removed`. Never a secret.

## The cap

Each organisation has its own quota, so one user may make or own
at most `--max-orgs-per-user` (10). An organisation counts against its
creator for as long as it exists, even after they hand it to another owner.
Quotas themselves (`--quota-sandboxes`, `--quota-cpus`, `--quota-memory-mb`)
are the same for every tenant, and an organisation is a tenant.

## SSH

An SSH key is its user's, kept under their own tenant whatever
organisation registered it. Logging in by key, a sandbox *name* is looked up
in the key's own tenant only; a sandbox *id* also reaches the user's own
sandboxes in an organisation they are a member of. `sandbox-cli ssh --org
acme web` resolves `web` in acme and logs in by its id. `sandbox-cli
ssh-access --org acme web` issues a token for acme's `web`. See
[ssh.md](ssh.md).

## In Studio

On a gateway, the top of Studio's sidebar is the organisation switcher: the
key's own tenant and every organisation its user belongs to, **Create
organization** with `org:create`, and **Members**. The switcher keeps its
choice per browser and sends `X-Sandbox-Org` on every call Studio makes, a
terminal's included; switching clears what was loaded, so nothing of the
previous organisation stays on screen. If the remembered one is no longer
allowed — you were removed — Studio goes back to your key's own tenant and
says so. On a plain `sandboxd` there is no switcher and Studio never asks for
`/v1/orgs`. See [studio.md](studio.md#organizations).

## From an SDK

A Python client made with `org="acme"`, or a TypeScript one with
`{ org: "acme" }`, sends the header on every request; `with_org` / `withOrg`
derives one for another organisation. `orgs`, `create_org`, `org_members`,
`set_org_member` and `remove_org_member` (camelCase in TypeScript) manage
them ([sdk/README.md](../sdk/README.md)).

## Not done yet

- **Organisations cannot be deleted or renamed** yet, and memberships are
  managed through the API, the CLI and Studio only (`sandbox-gateway` has no
  offline command for them).
- **Quotas are the same for every tenant**, set by flags.
