/**
 * Where this sits among the alternatives — including where it loses.
 *
 * The alternatives are described by kind, not by product: what a category of
 * tool does is stable enough to compare honestly, and a row about one vendor's
 * current feature list would be wrong by the time somebody read it. Each cell
 * says what is true of the category as a rule; where a category varies, the
 * cell says so rather than picking its best or worst member.
 */

export type Tone = "strong" | "ok" | "weak" | "none" | "neutral";

export type Cell = { text: string; tone: Tone };

export type Column = {
  id: string;
  name: string;
  sub: string;
  /** The one column that sits inside the boundary this page is about. */
  highlight?: boolean;
};

export const COLUMNS: Column[] = [
  { id: "sandbox", name: "sandbox-cli", sub: "this project", highlight: true },
  { id: "builtin", name: "Agents' own sandboxes", sub: "policy inside the agent" },
  { id: "container", name: "Container sandboxes", sub: "a container per agent" },
  { id: "os", name: "OS sandboxing", sub: "Seatbelt / Landlock" },
  { id: "hosted", name: "Hosted sandbox APIs", sub: "microVMs in a provider's cloud" },
];

export type Row = {
  label: string;
  /** Short plain-language gloss shown under the label. */
  note?: string;
  cells: Record<string, Cell>;
};

const s = (text: string): Cell => ({ text, tone: "strong" });
const o = (text: string): Cell => ({ text, tone: "ok" });
const w = (text: string): Cell => ({ text, tone: "weak" });
const n = (text: string): Cell => ({ text, tone: "none" });
const x = (text: string): Cell => ({ text, tone: "neutral" });

export const ROWS: Row[] = [
  {
    label: "Isolation",
    note: "How hard the wall actually is",
    cells: {
      sandbox: s("A VM per sandbox: its own kernel, behind a hypervisor"),
      builtin: w("Process rules the agent applies to itself"),
      container: o("Namespaces on the host's shared kernel"),
      os: o("Kernel-enforced per process, on your kernel"),
      hosted: s("A VM per sandbox"),
    },
  },
  {
    label: "Your files",
    note: "What is reachable by default",
    cells: {
      sandbox: s("Nothing mounted; the agent clones what it needs"),
      builtin: w("Your filesystem, minus what the rules forbid"),
      container: o("The directories you mount, read-write"),
      os: w("Your filesystem, minus what the rules forbid"),
      hosted: s("Nothing; you upload what it needs"),
    },
  },
  {
    label: "Runs where your code is",
    note: "Without sending it anywhere",
    cells: {
      sandbox: s("Your Mac, or a Linux machine you control"),
      builtin: s("Yes"),
      container: s("Yes"),
      os: s("Yes"),
      hosted: n("No — the provider's cloud"),
    },
  },
  {
    label: "Self-hosted, nothing phoning home",
    note: "For code that cannot leave the building",
    cells: {
      sandbox: s("sandboxd on your machine; no external control plane"),
      builtin: x("Not a server"),
      container: o("Your machine, your daemon"),
      os: x("Not a server"),
      hosted: w("Varies; often their control plane in your cloud at best"),
    },
  },
  {
    label: "One API in every place",
    note: "Laptop, own server, cloud",
    cells: {
      sandbox: s("The same API, checked by one conformance suite"),
      builtin: n("No API"),
      container: w("The engine's API, local only"),
      os: n("No API"),
      hosted: o("Theirs, in their cloud"),
    },
  },
  {
    label: "Egress control",
    note: "Stop exfiltration, keep installs working",
    cells: {
      sandbox: s("Allowlist by name, enforced on the host, deny wins"),
      builtin: o("Settings in the agent"),
      container: w("Varies; often open by default"),
      os: w("Coarse: on or off"),
      hosted: o("Per-sandbox rules, where offered"),
    },
  },
  {
    label: "Start time",
    note: "From request to a running command",
    cells: {
      sandbox: s("~80 ms on Firecracker; under 1 ms from a pool"),
      builtin: s("None — no VM"),
      container: s("Sub-second"),
      os: s("None"),
      hosted: o("Fast, plus the network round-trip"),
    },
  },
  {
    label: "Snapshots, fork, suspend",
    note: "Keep a sandbox without paying for it",
    cells: {
      sandbox: o("On Firecracker; not yet on a Mac"),
      builtin: n("No"),
      container: w("Rare"),
      os: n("No"),
      hosted: s("Usually"),
    },
  },
  {
    label: "Coding agents",
    note: "Logins, fallbacks",
    cells: {
      sandbox: s("Twelve agents; logins kept; fallbacks"),
      builtin: s("Built for one agent"),
      container: o("Some, per tool"),
      os: n("You wire it yourself"),
      hosted: w("An SDK; you build the rest"),
    },
  },
  {
    label: "Audit log",
    note: "What did it run, and how did it end",
    cells: {
      sandbox: s("Every action, on the server; env by name only"),
      builtin: w("The agent's own transcript"),
      container: w("The engine's events"),
      os: n("No"),
      hosted: o("Varies"),
    },
  },
  {
    label: "Where it loses",
    cells: {
      sandbox: w("Needs KVM or macOS 26 on Apple silicon; the cloud mode is not open yet"),
      builtin: w("The wall is the agent's own promise"),
      container: w("One kernel bug from your machine"),
      os: w("Different tools per OS, and your files stay in reach"),
      hosted: w("Your code leaves the building; you pay per second"),
    },
  },
];

/** Where each part runs. The client runs anywhere; the server needs a VM. */
export const PLATFORMS = [
  {
    capability: "Run sandboxes on this machine",
    macos: "yes",
    linux: "yes",
    windows: "no — client only",
    footnote:
      "macOS 26 on Apple silicon, with the native container runtime; Linux with /dev/kvm. Intel Macs and Windows run the client against a sandboxd elsewhere.",
  },
  {
    capability: "Backend",
    macos: "container runtime",
    linux: "Firecracker",
    windows: "—",
  },
  {
    capability: "Egress allowlist by name",
    macos: "not yet",
    linux: "yes, as root",
    windows: "—",
    footnote:
      "On Linux the firewall and proxy run on the host and need root. Without root, and on the macOS backend today, a sandbox gets no network or — where the operator permits — open egress; an allowlist request is refused there rather than served open.",
  },
  {
    capability: "Suspend, snapshot, fork",
    macos: "not yet",
    linux: "yes",
    windows: "—",
  },
  {
    capability: "Volumes",
    macos: "not yet",
    linux: "yes",
    windows: "—",
  },
  {
    capability: "The client: run, agent, shell, exec, list, attach, events",
    macos: "yes",
    linux: "yes",
    windows: "yes",
  },
] as const;
