/**
 * Facts about the project that appear in more than one place. Everything here
 * mirrors the repository — README.md, docs/AGENTS.md, docs/GUIDE.md — so the
 * page has exactly one place to update when the CLI changes.
 */

export const VERSION = "0.0.1beta.14";

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

export const DOC_URL = {
  docs: `${REPO_URL}/blob/main/docs/README.md`,
  guide: `${REPO_URL}/blob/main/docs/GUIDE.md`,
  agents: `${REPO_URL}/blob/main/docs/AGENTS.md`,
  development: `${REPO_URL}/blob/main/docs/DEVELOPMENT.md`,
  changelog: `${REPO_URL}/blob/main/CHANGELOG.md`,
  readme: `${REPO_URL}#readme`,
  // A page of its own since the README was slimmed; it used to be a README anchor.
  security: `${REPO_URL}/blob/main/docs/security/README.md`,
  license: `${REPO_URL}/blob/main/LICENSE`,
} as const;

/** The one-liner the hero leads with — pinned to the current release. */
export const INSTALL_ONELINER = `curl -fsSL ${RAW_INSTALL_URL} | sh -s -- --version ${VERSION}`;

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
    lines: [
      `curl -fsSL ${RAW_INSTALL_URL} \\`,
      `  | sh -s -- --version ${VERSION}`,
    ],
    note: "Detects your OS and CPU, verifies the archive against the release checksums.txt, and installs to ~/.local/bin/sandbox-cli. No root, no package manager.",
  },
  {
    id: "latest",
    label: "Latest release",
    hint: "always current",
    lines: [`curl -fsSL ${RAW_INSTALL_URL} | sh`],
    note: "Same installer with no --version pin: takes whatever the newest published release is.",
  },
  {
    id: "go",
    label: "Go",
    hint: "Go 1.25+",
    lines: ["go install github.com/Amitgb14/sandbox-cli/cmd/sandbox-cli@latest"],
    // Deliberately @latest rather than @v<VERSION>: the release tags are not
    // semver (0.0.1beta.8, no `v`, no `-` before the pre-release), so the module
    // proxy cannot resolve one — `@v0.0.1beta.8` fails with "invalid version"
    // and @latest lands on a pseudo-version of the default branch. Pinning a
    // release is what the install script is for.
    note: `Builds from source into $GOBIN, at whatever the default branch is. The release tags are not valid semver, so go install cannot pin one — use the install script with --version ${VERSION} when you need this exact release.`,
  },
  {
    id: "source",
    label: "From source",
    hint: "make",
    lines: [
      "git clone https://github.com/Amitgb14/sandbox-cli",
      "cd sandbox-cli && make install",
    ],
    note: "make build writes bin/sandbox-cli instead; make test runs the unit tests with no Docker required.",
  },
  {
    id: "windows",
    label: "Windows",
    hint: "Docker Desktop / WSL2",
    lines: [
      `# download sandbox-cli_${VERSION}_windows_amd64.zip`,
      `# from ${RELEASES_URL}`,
      "# then put sandbox-cli.exe somewhere on your PATH",
    ],
    note: "The shell installer covers Linux and macOS only; Windows binaries ship as .zip on the releases page.",
  },
];

/** First commands after install — the “now what” block in the hero. */
export const FIRST_RUN = [
  { cmd: "sandbox-cli claude", note: "Claude Code, contained" },
  { cmd: "sandbox-cli run -- bash", note: "a shell in the sandbox" },
  { cmd: "sandbox-cli run --dry-run -- npm test", note: "print the docker argv, run nothing" },
];

export const HERO_STATS = [
  { value: "1", label: "host path mounted", sub: "your project, at /workspace" },
  { value: "15", label: "agents wrapped", sub: "one prefix, flags forwarded verbatim" },
  { value: "0", label: "host creds forwarded", sub: "default-deny env allowlist" },
  { value: "--rm", label: "every container", sub: "nothing survives the run", mono: true },
];
