/**
 * Setup, from a cold machine to a verified sandbox, for each place a sandbox can
 * run — and for a machine that only talks to one.
 *
 * The last step of every path is `sandbox-cli doctor`, deliberately. Installing
 * the binaries is the easy half; what *this* sandboxd can deliver — an egress
 * allowlist, snapshots, volumes — is a property of the machine and how it was
 * started, and the point of doctor is that you find out before an agent does.
 *
 * Mirrors docs/local-macos.md and docs/self-hosting.md.
 */

export type SetupStep = {
  title: string;
  /** Shell to run, when the step is a command. */
  code?: string;
  body: string;
};

export type SetupPath = {
  id: string;
  label: string;
  /** What runs the sandboxes on this path. */
  engine: string;
  /**
   * Something a reader must know before following the path, shown first. The
   * macOS path carries one because its backend has not yet run on a real Mac,
   * and a setup guide is the wrong place to discover that.
   */
  caveat?: string;
  steps: SetupStep[];
};

/** The one-liner, kept here so the setup paths and the install card agree. */
export const INSTALL_STEP: SetupStep = {
  title: "Install",
  code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh",
  body: "Installs sandbox-cli, sandboxd and the guest agent beside it into ~/.local/bin, each archive verified against the release checksums, and — on a machine that has none — writes ~/.config/sandbox/config.yaml with the client settings spelled out. It installs sandboxd; it does not start it.",
};

/**
 * The launch agent, fetched and pointed at the installed sandboxd. The plist in
 * packaging/launchd runs /usr/local/bin/sandboxd, but the install script puts
 * it in ~/.local/bin; copying the plist as-is left a launch agent with nothing
 * to run.
 */
export const LAUNCH_AGENT_CODE = `curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/packaging/launchd/dev.sandbox.sandboxd.plist \\
  | sed "s#/usr/local/bin/sandboxd#$HOME/.local/bin/sandboxd#" \\
  > ~/Library/LaunchAgents/dev.sandbox.sandboxd.plist
launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/dev.sandbox.sandboxd.plist`;

const DOCTOR_STEP: SetupStep = {
  title: "Ask what this sandboxd can deliver",
  code: "sandbox-cli doctor",
  body: "Prints the backend, the API version, the network policy's default and ceiling, the capabilities (egress allowlist, suspend, snapshots, volumes, audit) and the limits. A request for something missing from that list is refused, never served weaker.",
};

const FIRST_RUN_STEP: SetupStep = {
  title: "Run something",
  code: "sandbox-cli run -- uname -a\nsandbox-cli agent claude",
  body: "The first run builds the image's root disk, which takes a while; later ones start in about 80 ms on Firecracker. A sandbox starts in /sandbox/home, its own home directory, with nothing of your machine mounted in: ask the agent to clone what it needs.",
};

export const UNINSTALL_STEPS: SetupStep[] = [
  {
    title: "Remove the binaries",
    code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --uninstall",
    body: "Deletes sandbox-cli, sandboxd and the guest agent from ~/.local/bin (it checks /usr/local/bin too, and reports a file it may not remove rather than stopping), then reports what else is on disk without touching it. A running sandboxd keeps running until you stop its launch agent or unit.",
  },
  {
    title: "Then decide about logins and volumes",
    code: "sh install.sh --uninstall --purge",
    body: "--purge also deletes ~/.config/sandbox — your config and every saved agent login — and sandboxd's state directory, which holds image disks, your volumes and the audit log. It is a separate flag because signing you out of every agent and deleting your volumes is not something an uninstaller should do on its own.",
  },
];

export const SETUP_PATHS: SetupPath[] = [
  {
    id: "macos",
    label: "macOS",
    engine: "the native container runtime",
    caveat:
      "The macOS backend was written and tested on Linux against a fake runtime that runs the real guest agent. Its first runs on a real Mac (macOS 26.1) boot a sandbox in under a second, but the full check has not run yet. Expect rough edges; docs/local-macos.md lists the open points, and the Linux paths are the verified ones.",
    steps: [
      {
        title: "Have the runtime",
        code: "container system start",
        body: "macOS 26 or later, on Apple silicon. Each sandbox is a VM of the runtime with a kernel of its own.",
      },
      INSTALL_STEP,
      {
        title: "Start sandboxd as a launch agent",
        code: LAUNCH_AGENT_CODE,
        body: "The launch agent in the repository runs /usr/local/bin/sandboxd; the sed points it at the copy the script installed in ~/.local/bin, so nothing needs root. It listens on a unix socket only you can open, which is the CLI's default context. Egress here is none, or open if your policy allows it: the macOS backend does not enforce an allowlist yet, so it does not claim one.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
    ],
  },
  {
    id: "linux",
    label: "Linux server",
    engine: "Firecracker",
    steps: [
      {
        title: "Have KVM, Firecracker and a guest kernel",
        code: "ls -l /dev/kvm\n# firecracker and jailer: github.com/firecracker-microvm/firecracker/releases\n# a vmlinux with IP_PNP, VIRTIO_VSOCKETS and overlayfs",
        body: "x86_64 or arm64, plus mkfs.ext4, ip and nft. The machine needs KVM: a cloud VM without nested virtualisation will not do.",
      },
      {
        title: "Install, as root, where the unit expects it",
        code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh \\\n  | sudo sh -s -- --dest /usr/local/bin --no-config",
        body: "sandboxd and the guest agent land side by side in /usr/local/bin, where packaging/systemd/sandboxd.service runs them from. --no-config: the server reads its policy file, not a client config.",
      },
      {
        title: "Run it as a service",
        code: "install -m 0600 token /etc/sandboxd/token\ninstall -m 0600 cert.pem key.pem /etc/sandboxd/tls/\ncp packaging/systemd/policy.example.yaml /etc/sandboxd/policy.yaml\ncp packaging/systemd/sandboxd.service /etc/systemd/system/\nsystemctl enable --now sandboxd",
        body: "As root it enforces the egress allowlist on the host and runs every VM under the jailer with a uid of its own. It refuses to listen on a network address without a token and TLS. The policy file is where the ceiling, the image list, pools and limits are set; a request can only ask for less.",
      },
      {
        title: "Point your client at it",
        code: "sandbox-cli context add box https://box.example.internal:7443 \\\n  --token-file box.token --ca box-ca.pem\nsandbox-cli context use box",
        body: "From your laptop, or on the server itself. The token never appears in an argv; it is read from the file you name.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
    ],
  },
  {
    id: "linux-dev",
    label: "Linux, quick try",
    engine: "Firecracker, unprivileged",
    caveat:
      "No root means no host networking: sandboxes get no network at all, and a request for an allowlist is refused. Everything else — boot, run, files, snapshots, volumes — works.",
    steps: [
      INSTALL_STEP,
      {
        title: "Start sandboxd in a terminal",
        code: "sandboxd --backend firecracker \\\n  --kernel ~/vmlinux --firecracker ~/bin/firecracker",
        body: "It listens on a unix socket under $XDG_RUNTIME_DIR, the CLI's default local context. Your user needs read-write access to /dev/kvm.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
    ],
  },
  {
    id: "client",
    label: "Client only",
    engine: "a sandboxd elsewhere",
    steps: [
      {
        title: "Install the client",
        code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --client-only",
        body: "For Windows (download the .zip from the releases page), an Intel Mac, or any machine that should not run VMs itself.",
      },
      {
        title: "Add the endpoint",
        code: "sandbox-cli context add box https://box.example.internal:7443 \\\n  --token-file box.token --ca box-ca.pem\nsandbox-cli context use box",
        body: "Contexts are how one client talks to your Mac, your server and the cloud: the commands are the same, only the context differs.",
      },
      DOCTOR_STEP,
      FIRST_RUN_STEP,
    ],
  },
];
