# Studio changelog

What changed in Studio's screens and dashboards, newest first. A change that
also touches the CLI, the API or a command Studio runs is in the main
[CHANGELOG.md](../CHANGELOG.md) instead, and this file links to it.

Studio ships inside `sandbox-cli` (`sandbox-cli studio`), so it has no version
of its own: entries land under `Unreleased` and move under the version of
`sandbox-cli` they ship in.

## Unreleased

- **Agents as rows, with their API keys.** The Agents screen is a table —
  login, keys, the API reached — and a row opens to add, edit or remove a
  saved key for each variable the agent reads (see
  [CHANGELOG.md](../CHANGELOG.md), "Saved agent API keys, VM templates and
  egress rules").
- **Templates.** A new screen under Build: sizes to launch at, micro to
  xlarge built in and your own, with a Size step in the Playground and a
  Launch link from each.
- **Egress rules in Settings.** Hosts every launch allows or denies, each
  with a switch; the Playground's summary counts them and its code includes
  them.
- **Metrics.** A sandbox's resources, in the list and its overview, open the
  last hour of its CPU and memory as charts, with its current network and disk
  rates, where the endpoint measures them (see
  [CHANGELOG.md](../CHANGELOG.md), "A sandbox's CPU and memory over the last
  hour").
- **A snapshot's progress.** Taking a snapshot shows its three steps —
  preparing, reading files (with a percentage where there is an estimate),
  storing — and the time so far, in the sandbox's panel and as a short status
  in the list's Snapshots column, in place of a button that said "Taking
  snapshot…" for a minute and a half. It is read from the sandbox, so it is
  still there after the panel is closed or the page reloaded (see
  [CHANGELOG.md](../CHANGELOG.md), "A snapshot's progress").
- **A Snapshots column, and a Snapshot button that stands out.** Where the
  endpoint takes snapshots, the sandbox list has a column with each sandbox's
  snapshot count, marked scheduled or manual, and "Inactive" for one with no
  schedule and none taken. A running sandbox's Snapshot button is coloured,
  with an icon.
- **The Playground's agents as a table.** One row per agent, with what a
  launch hands the sandbox for it: its login (copied in), the API keys set
  where Studio runs (forwarded) and the API it reaches (let through); a row
  expands to the login files, every key it reads and its status.

- **A snapshot schedule from the Playground.** *Start from* has "Snapshot it
  on a schedule": every so often, keeping the newest few, offered only within
  the server's limits and only where the endpoint takes snapshots. The
  summary shows it, and the code beside the form carries it
  (`--snapshot-every 30m --snapshot-keep 3`, `snapshot_every_secs` and
  `snapshot_keep`). Studio's launch request carries the two fields to the same
  code the CLI's flags reach.

- **Agents, one row each, with what each needs to log in.** The Playground's
  agents are a list: each row says whether it is logged in, has an API key set
  where Studio runs (which a launch forwards), or needs one, and opens for
  where its login is kept between runs, the variables it reads and which are
  set (names only, never values), and the API it always reaches.
- **The Playground starts from the image or snapshot you choose.** A new
  *Start from* step takes an image, from a searchable picker grouped into the
  desktop image, those the machine has already built and those its sandboxes
  run, or typed; or a snapshot, where the endpoint takes them.
  Empty is the server's default, as before. The code beside the form follows:
  `--image` or `--from-snapshot` for the CLI, `image` or `snapshot_id` for the
  API and the SDKs. Studio's launch request carries `image` and `snapshot`,
  one or neither; sandboxd's policy still decides whether they run.
- **A Desktop tab.** A running sandbox's panel has a Desktop tab: in a sandbox
  made from the desktop image it starts the desktop and shows its screen — a
  terminal and a browser to use — scaled to fit, with Reconnect and Full
  screen; closing it leaves the desktop running. In any other sandbox it says
  the image has no desktop and gives the command that makes one. The screen is
  drawn by noVNC, bundled in Studio. See the main
  [CHANGELOG.md](../CHANGELOG.md) and [docs/desktop.md](../docs/desktop.md).
- **Files never waits for good.** Listing a directory or opening a file
  gives up after 20 seconds with "The sandbox did not answer" and a Retry,
  where a read the sandbox never answered left "reading…" up for good; a file
  the server refused to read says why instead of showing the error as its
  contents.

- **Snapshots where the backend keeps files only.** The panel's Snapshot
  button appears where the endpoint takes either kind of snapshot, says
  "Taking snapshot…" while a disk snapshot is made (about a minute on macOS),
  and the Snapshots screen shows each one's kind: whole machine, or files
  only. See the main [CHANGELOG.md](../CHANGELOG.md).
- **Scheduled snapshots in the panel.** A sandbox's overview has a Snapshots
  section: its schedule (every so often, the newest few kept, chosen within the
  server's limits), set, changed or stopped there, and that sandbox's
  snapshots, scheduled or manual, with their kind, size and age.

- **The sandbox list shows live sandboxes by default**: starting, running and
  suspended. Terminated ones are one choice away in the State filter, and a
  list with none live says so, with a link to show them all.

- **A running sandbox's dot pulses**, in the list and its panel, as Studio's
  other live indicators do, so what is alive reads at a glance; every other
  state's dot holds still, and so does this one under reduced motion.

- **A sandbox opens beside the list.** A click on a row slides its details in
  from the right: who it is and its actions at the top, then its overview in
  sections (id, image, network, resources, lifecycle, labels, environment
  names, volumes, processes) and its Terminal, Logs, Files and Events. Its up
  and down buttons step through the list as it is filtered and sorted; *Widen* puts the
  overview beside the tabs, and *Open as a page* is the same view at
  `/sandbox?id=`, two panes on a wide screen. The row's terminal button opens
  the panel with a shell already started.
- **Terminals are kept, not multiplied.** *Terminal* goes back to the shell
  already open and starts one only when there is none; it used to start a new
  shell on every click. The Terminal tab lists every process with a terminal —
  an agent's console and each shell — to attach to any of them, and *New
  shell* starts another. An agent's sandbox opens on its console. A click on
  such a process in the overview attaches to it instead of showing its output
  as lines, and its Logs say why that output reads poorly. Logs drop the
  cursor moves, erases and mode switches a terminal program writes, which
  showed as boxes and brackets, and keep its colours.
- **What an agent is doing, under its sandbox's state.** A running sandbox
  with an agent says whether it is working, waiting for you, idle, done or
  failed (the same check as `sandbox-cli agent state`; the reason on hover),
  in the list and the panel; that replaces the Agent column. The panel shows
  *Interactive* when a process there has a terminal, with how many, and a
  click on it opens the Terminal tab; such a process is *Interactive* in the
  process list rather than a spinner.
- **The sandbox list.** Above it, how many sandboxes are running and the vCPU,
  memory and disk given to them, against the machine's capacity where the
  endpoint reports it (`GET /v1/node`); a gateway that does not gives totals.
  These are allocations, not live usage, which sandboxd does not report. Each
  row shows its state as a dot and its resources as chips; the State filter
  takes several states at once with a count for each; columns sort and hide;
  the list pages; and selected sandboxes are terminated together.

- **Organizations on a gateway.** The top of the sidebar is an organization
  switcher: the key's own tenant and every organization you belong to, with a
  check on the current one, *Create organization* (with `org:create`; the name
  is checked as you type) and *Members*. Choosing one shows only that
  organization's sandboxes, jobs, services and secrets: the selection is sent on
  every call, terminal and streams included, and everything of the previous one
  is dropped. It is remembered per browser; one you no longer belong to falls
  back to your key's own tenant, with a notice. **Members** lists who belongs;
  owners add, remove and change roles. **Account** shows the current
  organization, and an admin key gets **All organizations**, which an
  admin-off build leaves out. A plain sandboxd shows none of this and is never
  asked about organizations. See the main [CHANGELOG.md](../CHANGELOG.md).

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
