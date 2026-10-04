# Studio changelog

What changed in Studio's screens and dashboards, newest first. A change that
also touches the CLI, the API or a command Studio runs is in the main
[CHANGELOG.md](../CHANGELOG.md) instead, and this file links to it.

Studio ships inside `sandbox-cli` (`sandbox-cli studio`), so it has no version
of its own: entries land under `Unreleased` and move under the version of
`sandbox-cli` they ship in.

## Unreleased

- **Studio's text is set in Inter,** bundled with the UI so it needs no font
  service and works offline. Column headings are small monospace capitals,
  page titles a medium weight; code, ids and the terminal stay in Geist Mono.

- **Studio is simpler.** It opens on the sandbox list instead of an overview of
  counts, in a near-monochrome theme with one quiet sidebar: Sandboxes,
  Snapshots and Volumes; Playground and Agents; Settings, with search, which
  sandboxd this is, a Light/Dark switch and the version at its foot. The list
  is a search, a state filter and a refresh over one table, each row with a menu
  to open, copy or terminate the sandbox; with none, a first-run panel offers
  the Playground and the same start in Python, TypeScript, curl or the CLI.

- **Studio has screens for a gateway.** On a gateway context Studio asks who
  the API key is (`GET /v1/whoami`) and adds Jobs (submit, cancel, each run's
  kept output and files), Services (deploy from a JSON spec, replicas and
  health, scale, remove), Secrets (names only; set and remove with
  `secrets:write`), SSH (how to connect, your SSH keys, a short-lived access
  token shown once) and Account. An admin key also gets Nodes (capacity,
  cordon, drain, add, remove — never a node's endpoint), Lost sandboxes, Users
  & keys (a new key's secret shown once) and Audit. Actions the key's scopes do
  not allow are not offered, and a screen it may not have is *Not available*
  by URL too, without a request; the gateway's 403 stays the control. A plain
  `sandboxd` shows the same screens as before. `NEXT_PUBLIC_STUDIO_ADMIN=off`
  at build time leaves the admin screens out of the bundle, for a dashboard
  hosted for many tenants ([fleet.md](../docs/fleet.md#studio)).

- **Studio is reorganised around sandboxes, the way hosted sandbox dashboards
  are.** The Overview shows what is running, agents waiting for you, snapshots
  and volumes, and a quick start in the CLI, curl, Python or TypeScript when
  there is nothing yet. Sandboxes filters by state, start time and a search,
  and shows what each sandbox was given (vCPU, memory, disk: allocations, since
  sandboxd reports no live usage). A sandbox opens on an Overview tab —
  resources, network, lifecycle, volumes, environment names and its processes —
  beside Terminal, Logs (formerly Output), Files and Events. Launch is now the
  Playground: the form, with the same command run written as CLI, curl, Python
  and TypeScript beside it, so a run set up by hand can be repeated from a
  script. And a new Snapshots screen lists snapshots and deletes them.

- **Studio lists only the agents it can run unattended.** The Agents page and the launch form show only the
  agents with a verified headless mode (claude, codex, gemini, opencode, cline);
  Studio refuses to start the interactive-only ones, console runs included, and
  they stay available from the CLI with `sandbox-cli agent <name>`. Studio also
  has a refreshed look: a readable page width, tables as cards, a step-by-step
  launch form with a summary of the run, and an agent card per agent.
