/**
 * Facts about the project that appear in more than one place. Everything here
 * mirrors the repository — README.md, docs/api/v1.md, docs/local-macos.md,
 * docs/self-hosting.md — so the page has exactly one place to update when the
 * product changes.
 *
 * No version is pinned on the page. The microVM rewrite ships as a new release
 * line, and a number written here before it is tagged would be a claim about a
 * release that does not exist; the install routes take the latest instead, and
 * the releases page is one link away.
 */

/** What the header badge says: which product this page describes. */
export const CHANNEL = "microVM";

export const REPO_URL = "https://github.com/Amitgb14/sandbox-cli";
export const RELEASES_URL = `${REPO_URL}/releases`;
export const RAW_INSTALL_URL =
  "https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh";

/**
 * The multi-agent doc's route. A constant because three files link to it, and
 * with a trailing slash because `trailingSlash: true` in next.config.ts makes
 * the export emit `multi-agent/index.html` — linking without it costs a
 * redirect on the hosts that do one and a 404 on the hosts that do not.
 */
export const MULTI_AGENT_PATH = "/multi-agent/";
export const STUDIO_PATH = "/studio/";

const BLOB = `${REPO_URL}/blob/main`;

export const DOC_URL = {
  readme: `${REPO_URL}#readme`,
  api: `${BLOB}/docs/api/v1.md`,
  localMac: `${BLOB}/docs/local-macos.md`,
  selfHosting: `${BLOB}/docs/self-hosting.md`,
  agents: `${REPO_URL}#coding-agents`,
  security: `${BLOB}/docs/security/README.md`,
  changelog: `${BLOB}/CHANGELOG.md`,
  plan: `${BLOB}/docs/rewrite/PLAN.md`,
  pythonSdk: `${REPO_URL}/tree/main/sdk/python`,
  typescriptSdk: `${REPO_URL}/tree/main/sdk/typescript`,
  license: `${BLOB}/LICENSE`,
} as const;

export type InstallRoute = {
  id: string;
  label: string;
  hint: string;
  lines: string[];
  /** Shown under the block; short, factual. */
  note?: string;
};

export const INSTALL_ROUTES: InstallRoute[] = [
  {
    id: "script",
    label: "Install script",
    hint: "macOS · Linux",
    lines: [`curl -fsSL ${RAW_INSTALL_URL} | sh`],
    note: "On Linux and Apple-silicon Macs: sandbox-cli, sandboxd and the guest agent, each verified against the release checksums.txt, into ~/.local/bin. Elsewhere: the client only. No root, no package manager. It installs sandboxd; starting it is one step in the setup guide below.",
  },
  {
    id: "client",
    label: "Client only",
    hint: "any machine",
    lines: [`curl -fsSL ${RAW_INSTALL_URL} | sh -s -- --client-only`],
    note: "Just sandbox-cli, for a machine that talks to a sandboxd somewhere else: sandbox-cli context add box https://box:7443 --token-file box.token --ca box-ca.pem",
  },
  {
    id: "source",
    label: "From source",
    hint: "Go 1.25+",
    lines: [
      "git clone https://github.com/Amitgb14/sandbox-cli",
      "cd sandbox-cli && make build",
    ],
    note: "Writes bin/sandbox-cli, bin/sandboxd and bin/sandbox-guestd. make test runs the unit tests and the conformance suite against the in-memory backend; no VM required.",
  },
  {
    id: "windows",
    label: "Windows",
    hint: "client only",
    lines: [
      "# download sandbox-cli_<version>_windows_amd64.zip",
      `# from ${RELEASES_URL}`,
      "# then point it at a sandboxd: sandbox-cli context add …",
    ],
    note: "Windows does not run sandboxes. The client talks to a sandboxd on a Mac, a Linux machine or the cloud.",
  },
];

/** First commands after install — the “now what” block in the hero. */
export const FIRST_RUN = [
  { cmd: "sandbox-cli run -- npm test", note: "a fresh VM on a clone of this repo" },
  { cmd: "sandbox-cli agent claude", note: "a coding agent, its login kept" },
  { cmd: "sandbox-cli list", note: "what is running, wherever it runs" },
];

export const HERO_STATS = [
  { value: "~80 ms", label: "to a running VM", sub: "Firecracker, image cached", mono: true },
  { value: "<1 ms", label: "from a pool", sub: "sandboxes booted ahead", mono: true },
  { value: "0", label: "host paths mounted", sub: "your repo goes in as a git bundle" },
  { value: "1", label: "API, three places", sub: "your Mac, your Linux box, the cloud" },
];
