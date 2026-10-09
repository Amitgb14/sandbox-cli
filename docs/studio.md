# Studio

Studio is the browser view of your sandboxes: launch a command or an agent,
watch its output, type into its terminal, read its files and its audit
events. It is a client of the same API as the CLI and the SDKs, served by
`sandbox-cli` itself on a loopback port, and it talks to whichever endpoint
your context points at — a `sandboxd` on your Mac, on your Linux machine, or
a gateway in front of many.

```sh
sandbox-cli studio
# Studio: http://127.0.0.1:7080/#token=cb0fbb4f…
# studio: context local · Ctrl-C to stop
```

Open the address it prints. `--context` picks another endpoint, `--port`
another port. Closing Studio leaves every sandbox it started running; the
next Studio finds them again.

This page is the user's guide. Working on Studio itself — building it, its
tests, how its routes are laid out — is
[studio/README.md](../studio/README.md).

## Screens

Nothing in Studio is a second implementation of the CLI: every screen is an
API call or the same host-side code the CLI runs, so a rule the CLI keeps,
Studio keeps.

| Screen | What it does |
|---|---|
| **Sandboxes** (home) | Every sandbox on the endpoint — started from Studio, the CLI or an SDK — each with what its agent is doing (working, waiting for you, idle) where it runs one, under a strip of how many are running and the vCPU, memory and disk given to them, against the machine's capacity where the endpoint reports it (a plain sandboxd does; a gateway gives totals). Searched, filtered by state, sorted, paged; each row shows what the sandbox was given — a click there opens its CPU and memory over the last hour, where the endpoint measures them —, a terminal button and a menu (open, open as a page, copy the id, suspend, terminate); select several to terminate them at once. With none yet, a first-run panel with the code to start one. |
| **A sandbox** | A click on a row opens it in a panel beside the list: its up and down buttons step through the list, and it widens to two panes or opens as a page of its own (`/sandbox?id=`). Its overview — id, image, network, resources, lifecycle, labels, environment names, volumes, processes —; a real terminal (Terminal goes back to the shell open there, or starts one as `sandbox-cli shell` does; the tab lists every process with a terminal, an agent's console included, to attach to, and New shell starts another); a Desktop tab, where the sandbox's image has one ([desktop.md](desktop.md)): its screen, with a terminal and a browser, to see and use; its processes' logs from the first byte; its files; and its audit events. Wide, the overview stays beside the other tabs. |
| **Playground** | A command, an agent run unattended, or an agent's interactive console, in a fresh sandbox that starts in its own home directory, from the server's image, one you name (suggested: those the machine has built, those its sandboxes run, the desktop image) or a snapshot where the backend takes them, with a snapshot schedule if asked for, at a size from Templates — with the same config, profile, network policy, labels, volumes and agent login a `sandbox-cli run` would get. Beside the form, the same run written as CLI, curl, Python and TypeScript, to repeat it from a script. |
| **Snapshots** | Sandboxes captured to start new ones from — whole (memory, processes and disk) where the backend can, their files only where it cannot capture more (macOS) — with their kind and size; delete one. A running sandbox's panel takes one with Snapshot, and its overview's Snapshots section sets a schedule — every so often, the newest few kept, within the server's limits — and lists that sandbox's snapshots, scheduled or manual. |
| **Agents** | The agents Studio runs, each with a verified headless mode, one row each: whose login is saved, which API keys are set or saved, and the API it reaches. Open a row to add, change or remove an API key for any variable the agent reads. A key is write-only: it is kept in `~/.config/sandbox/agent-keys.json` (yours only, 0600), never shown again, and used by an agent run — Studio's or the CLI's — when the environment does not set that variable; the environment's value wins. |
| **Templates** | Sizes to launch at — vCPUs, memory and disk — by name: micro, small, medium, large and xlarge built in, and any of your own, made, edited and deleted here (a built-in one is copied, not changed). The Playground's Size step offers them, and a launch sends the size as `--cpus`, `--memory` and `--disk` would; the endpoint's limits still apply, and a template above them is marked and refused. Kept in `~/.config/sandbox/studio.json`, and read by the CLI too: `sandbox-cli run --template large -- make` (or `agent claude --template medium`) launches at the same size, with `--cpus`, `--memory` or `--disk` beside it overriding that field. |
| **Volumes** | Named volumes and where each is mounted. |
| **Settings** | The context, what its endpoint can deliver, and egress rules: hosts every Studio launch allows or denies, each with a switch to turn it off. A deny rule applies to every run with a network; an allow rule widens a run that is an allowlist (one that asks for it, or the endpoint's default when that is one) and leaves an open run open. The endpoint's policy still decides — a host it does not let a request add is refused at launch — and the Playground's code shows the rules as `--allow` and `--deny`. Kept in `~/.config/sandbox/studio.json`; for rules every `sandbox-cli` run gets, use `network.allow` in `~/.config/sandbox/config.yaml`. |

## A plain sandboxd, or a gateway

On load Studio asks `GET /v1/whoami`. A plain `sandboxd` answers 404 and
gets exactly the screens above. A gateway ([fleet.md](fleet.md)) answers with
the API key's user, tenant and scopes, and those decide the rest:

```sh
sandbox-cli studio --context fleet
```

`sandbox-cli studio` holds the API key and adds it to each call it proxies;
the browser never sees the key. It is the same Studio either way.

## Scopes and screens

| The key holds | Studio adds |
|---|---|
| any scope | **Jobs** (list, detail with each run's kept output and files), **Services** (list, detail with replicas, health and rollout), **Secrets** (names only), **SSH** (where to connect, the host key to pin), **Account** (user, tenant, current organisation, key id, scopes), the **organisation switcher** at the top of the sidebar and **Members** (list; add, remove and change roles as an owner) |
| `org:create` | **Create organization** in the switcher |
| `sandbox:create` | the Playground, submitting and cancelling jobs, deploying (a JSON spec) and scaling services, creating volumes, a sandbox's Terminal, Suspend and Snapshot |
| `sandbox:delete` | terminating sandboxes, deleting volumes and snapshots; with `sandbox:create`, removing a service |
| `sandbox:ssh` | adding and removing your SSH keys, issuing a short-lived access token for a sandbox (shown once) |
| `secrets:write` | setting and removing secrets. A value goes in a password field and is never shown: no call returns it |
| `admin` | everything above, plus **Nodes** (health, allocated capacity, cordon, uncordon, drain, add, remove), **Lost sandboxes**, **Users & keys** (issue and revoke API keys, any user's SSH keys), **Organizations** (every organisation, with its members and owners) and **Audit** |

An action the key's scopes do not allow is not offered, rather than offered
and refused. A screen the key may not have — an admin screen for a tenant's
key, or a gateway screen on a plain `sandboxd` — is missing from the sidebar
and the palette, and a typed URL shows a plain *Not available* page that makes
no request for it. That is a convenience, not the control: the gateway refuses
a call without its scope (`403`) whoever sends it.

What each gateway screen is about is on its own page: [jobs and
secrets](jobs.md), [services](services.md), [SSH](ssh.md), and for an admin,
[operations](operations.md) (nodes, drain, lost sandboxes, keys, the audit
log).

## Organizations

On a gateway the top of the sidebar is the organisation switcher: the key's
own tenant and every [organisation](organizations.md) its user belongs to,
**Create organization** with `org:create`, and **Members**. The switcher
keeps its choice per browser and sends `X-Sandbox-Org` on every call Studio
makes, a terminal's included; switching clears what was loaded, so nothing of
the previous organisation stays on screen. If the remembered one is no longer
allowed — you were removed — Studio goes back to your key's own tenant and
says so. With nothing chosen in this browser, Studio starts in the context's
organisation (`sandbox-cli org use`). On a plain `sandboxd` there is no
switcher and Studio never asks for `/v1/orgs`.

## What Studio never shows

- **A node's endpoint,** which is dropped as the node list is read (and taken
  out of a node's error text).
- **A secret value.** No call returns one, and neither does any call return
  an agent's saved API key.
- **A credential twice.** A new API key's secret is in one answer only;
  Studio shows it once, with a copy button and a warning, and drops it when
  you click Done. An SSH access token is shown the same way.

## A hosted dashboard without the admin screens

Building Studio with `NEXT_PUBLIC_STUDIO_ADMIN=off` leaves the admin screens
out of the bundle altogether — their pages are not routes in that build and
nothing they import is included — so a Studio served to many tenants does not
carry the operator's UI at all:

```sh
NEXT_PUBLIC_STUDIO_ADMIN=off make studio build   # a sandbox-cli whose Studio has no admin screens
```

The default build keeps them, for an operator running their own gateway.
`npm run check:admin-off` in `studio/` (part of `npm run check`) makes such a
build and fails if any admin route, admin API path or admin screen title is
in it. The gateway's refusal stays the control; the build only means the
screens are not shipped.

## How it is served, and who may use it

Studio is a static export embedded in `sandbox-cli`. `sandbox-cli studio`
serves it on a loopback port together with a small API, from the same
origin: the endpoint's API proxied to the current context, a WebSocket bridge
to a process's terminal, and launching a run through the same code the CLI
uses.

Anything that can reach Studio's API can start sandboxes, and agents with
your saved logins. These are the three reasons a web page you happen to have
open cannot:

- **Loopback only, and a loopback Host.** Studio listens on 127.0.0.1 and
  answers only `Host` headers naming loopback, so a page whose own name
  resolves to 127.0.0.1 — DNS rebinding — is refused: the name it dialled
  gives it away.
- **A token per launch.** A loopback port is reachable by every user on the
  machine. Every request to Studio's API needs the token in the address
  `sandbox-cli studio` prints; it travels in the URL's fragment, which is
  never sent to a server, is kept in the tab's `sessionStorage` only, and is
  wiped from the address bar.
- **The browser never holds the endpoint's credential.** Studio proxies
  sandbox calls to the context's `sandboxd` or gateway and adds that token or
  API key itself. A cross-origin request is refused outright, and a body that
  is not JSON — the shape of a request that skips a browser's preflight — is
  refused too.

## Checking it works

Studio's end-to-end suite runs the real `sandbox-cli studio` in front of a
`sandboxd` and a real gateway on the in-memory backend. What it cannot prove
— a job's kept file from a real guest, an SSH token logging in, a real
drain — is row 44 of [testing/end-to-end.md](testing/end-to-end.md).
