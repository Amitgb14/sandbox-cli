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
 * Whether a published release has sandboxd. The latest, 0.0.1, is the
 * container design's and ships the client alone: install.sh refuses it for a
 * server, and with --client-only installs that old client, which cannot talk
 * to a sandboxd. Until the rewrite's first release every install step on the
 * site builds from a checkout instead. Set this to true when it is tagged, and
 * the install.sh routes come back.
 */
export const RELEASED = false;

/** The build from a checkout every install step uses until RELEASED. */
export const SOURCE_BUILD = `git clone https://github.com/Amitgb14/sandbox-cli && cd sandbox-cli
make studio build   # Studio's UI (Node 20+), then bin/sandbox-cli, bin/sandboxd, bin/sandbox-guestd`;

/** Said wherever a step builds from source because of RELEASED. */
export const NOT_RELEASED_YET =
  "No published release has sandboxd yet (0.0.1 is the container design's, client only), so this builds from a checkout: Go 1.25+, and Node 20+ for Studio's UI.";

/**
 * The sub-routes. Constants because several files link to them, and with a
 * trailing slash because `trailingSlash: true` in next.config.ts makes the
 * export emit `studio/index.html` — linking without it costs a redirect on the
 * hosts that do one and a 404 on the hosts that do not.
 */
export const STUDIO_PATH = "/studio/";
export const SETUP_PATH = "/setup/";

/**
 * The documentation, rendered from the repository's docs/ at build time
 * (src/lib/docs.ts). `docPath` is the one way to name a page of it, so a page
 * that moves is renamed in the manifest and nowhere else.
 */
export const DOCS_PATH = "/docs/";
export function docPath(slug: string, anchor?: string) {
  const base = slug ? `${DOCS_PATH}${slug}/` : DOCS_PATH;
  return anchor ? `${base}#${anchor}` : base;
}

/** A file in the repository on GitHub, and a directory. */
export const BLOB = `${REPO_URL}/blob/main`;
export const TREE = `${REPO_URL}/tree/main`;
export const EDIT = `${REPO_URL}/edit/main`;

export const DOC_URL = {
  readme: `${REPO_URL}#readme`,
  api: `${BLOB}/docs/api/v1.md`,
  localMac: `${BLOB}/docs/local-macos.md`,
  selfHosting: `${BLOB}/docs/self-hosting.md`,
  fleet: `${BLOB}/docs/fleet.md`,
  fleetWalkthrough: `${BLOB}/docs/testing/fleet-walkthrough.md`,
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

const RELEASE_ROUTES: InstallRoute[] = [
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

/**
 * Before the first release: building from a checkout first and chosen by
 * default, the script routes left out (they would install the old client),
 * and Windows built rather than downloaded.
 */
const PRE_RELEASE_ROUTES: InstallRoute[] = [
  {
    id: "source",
    label: "From source",
    hint: "Go 1.25+",
    lines: [
      "git clone https://github.com/Amitgb14/sandbox-cli",
      "cd sandbox-cli && make studio build",
      "install -d ~/.local/bin",
      "install -m 0755 bin/sandbox-cli bin/sandboxd bin/sandbox-guestd ~/.local/bin/",
    ],
    note: "The first microVM release is not out yet: 0.0.1 is the container design's, so the install script would refuse it. This builds sandbox-cli (with Studio's UI; Node 20+), sandboxd and the guest agent, and installs them side by side; starting sandboxd is one step in the setup guide below.",
  },
  {
    id: "client",
    label: "Client only",
    hint: "any machine",
    lines: [
      "git clone https://github.com/Amitgb14/sandbox-cli",
      "cd sandbox-cli && make studio build",
      "install -d ~/.local/bin && install -m 0755 bin/sandbox-cli ~/.local/bin/",
    ],
    note: "Just sandbox-cli, for a machine that talks to a sandboxd somewhere else: sandbox-cli context add box https://box:7443 --token-file box.token --ca box-ca.pem",
  },
  {
    id: "windows",
    label: "Windows",
    hint: "client only",
    lines: [
      "git clone https://github.com/Amitgb14/sandbox-cli",
      "cd sandbox-cli",
      "go build -o sandbox-cli.exe ./cmd/sandbox-cli",
    ],
    note: "Windows does not run sandboxes. The client talks to a sandboxd on a Mac, a Linux machine or the cloud.",
  },
];

export const INSTALL_ROUTES: InstallRoute[] = RELEASED ? RELEASE_ROUTES : PRE_RELEASE_ROUTES;

/** First commands after install — the “now what” block in the hero. */
export const FIRST_RUN = [
  { cmd: "sandbox-cli run -- uname -a", note: "a fresh VM, its own kernel" },
  { cmd: "sandbox-cli agent claude", note: "a coding agent, its login kept" },
  { cmd: "sandbox-cli list", note: "what is running, wherever it runs" },
];

export const HERO_STATS = [
  { value: "~80 ms", label: "to a running VM", sub: "Firecracker, image cached", mono: true },
  { value: "<1 ms", label: "from a pool", sub: "sandboxes booted ahead", mono: true },
  { value: "0", label: "host paths mounted", sub: "nothing on your machine is mounted in" },
  { value: "1", label: "API, three places", sub: "your Mac, your Linux box, the cloud" },
];
