/**
 * Per-platform setup, from a cold machine to a verified sandbox.
 *
 * The last step of every path is `sandbox-cli doctor`, deliberately. Installing
 * the binary is the easy half; whether *this host* can actually deliver the
 * isolation — a daemon that applies a syscall filter, a container that can
 * program the egress firewall — is a property of the machine, and the point of
 * doctor is that you find out before an agent does.
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
  /** The container engine this path uses. */
  engine: string;
  /**
   * Set when a path does not work today; the UI leads with this. Nothing sets
   * it now that Podman is supported — kept because the honest thing to do with
   * an engine that does not work is say so on the page rather than omit it.
   */
  unsupported?: string;
  steps: SetupStep[];
};

/** The one-liner, kept here so the setup paths and the install card agree. */
export const INSTALL_STEP: SetupStep = {
  title: "Install sandbox-cli",
  code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh",
  body: "Drops a single static binary on your PATH. `go install github.com/Amitgb14/sandbox-cli/cmd/sandbox-cli@latest` works too if you would rather build it.",
};

const VERIFY_STEP: SetupStep = {
  title: "Check the host can actually deliver it",
  code: "sandbox-cli doctor",
  body: "Reports whether the daemon applies a syscall filter, whether a container here can program the egress firewall — tried, not queried — and which OCI runtimes are registered. Add --profile prod on a machine that will run unattended: it turns every warning into a refusal with a non-zero exit, so a scheduler finds out instead of you.",
};

const FIRST_RUN_STEP: SetupStep = {
  title: "Run an agent",
  code: "cd ~/your-project\nsandbox-cli claude",
  body: "The first run builds the base image, which takes a few minutes once. Only this directory is mounted; HOME inside the container is fake and dies with it. Egress is default-deny, so the agent reaches its own API and the package registries and nothing else.",
};

export const SETUP_PATHS: SetupPath[] = [
  {
    id: "macos",
    label: "macOS",
    engine: "Docker Desktop",
    steps: [
      {
        title: "Install Docker Desktop",
        code: "brew install --cask docker",
        body: "Then launch it once and let it finish starting — the daemon has to be running, not merely installed. Apple silicon and Intel are both fine.",
      },
      {
        title: "Turn seccomp back on",
        body: "Docker Desktop ships some configurations with \"seccomp-profile\": \"unconfined\" in Settings → Docker Engine, which means the container gets the whole syscall table. Remove that line and apply. sandbox-cli warns when it finds this, and refuses under --profile prod.",
      },
      INSTALL_STEP,
      VERIFY_STEP,
      FIRST_RUN_STEP,
    ],
  },
  {
    id: "linux",
    label: "Linux",
    engine: "Docker Engine",
    steps: [
      {
        title: "Install Docker Engine",
        code: "curl -fsSL https://get.docker.com | sh",
        body: "The convenience script covers Debian, Ubuntu, Fedora, CentOS and their relatives. Your distribution's own docker.io package works as well.",
      },
      {
        title: "Let your user talk to the daemon",
        code: "sudo usermod -aG docker $USER   # then log out and back in",
        body: "Without this every command needs sudo. Note that membership of the docker group is root-equivalent on the host — that is Docker's model, not sandbox-cli's, and it is worth knowing before you grant it.",
      },
      INSTALL_STEP,
      VERIFY_STEP,
      FIRST_RUN_STEP,
    ],
  },
  {
    id: "windows",
    label: "Windows",
    engine: "Docker Desktop + WSL2",
    steps: [
      {
        title: "Install WSL2",
        code: "wsl --install",
        body: "In PowerShell as administrator, then reboot. sandbox-cli runs inside the Linux distribution, not on Windows directly.",
      },
      {
        title: "Install Docker Desktop with the WSL2 backend",
        body: "In Settings → Resources → WSL Integration, enable your distribution. Docker Desktop must be running for the CLI inside WSL to reach the daemon.",
      },
      {
        title: "Keep your project inside the Linux filesystem",
        code: "# good:  ~/projects/app\n# slow:  /mnt/c/Users/you/projects/app",
        body: "A project under /mnt/c is reachable but crosses the Windows filesystem boundary on every read, which an agent doing thousands of small file operations will feel. Clone into the WSL home directory instead.",
      },
      INSTALL_STEP,
      VERIFY_STEP,
      FIRST_RUN_STEP,
    ],
  },
  {
    id: "podman",
    label: "Podman",
    engine: "rootless Podman",
    steps: [
      {
        title: "Install Podman and start its VM",
        code: "brew install podman        # or your distribution's package\npodman machine init\npodman machine start",
        body: "The machine step is macOS and Windows only; on Linux Podman talks to the host directly. Rootless is the default and is the interesting case — the container runs as your user, not root.",
      },
      INSTALL_STEP,
      {
        title: "Tell sandbox-cli to use it",
        code: "sandbox-cli run --engine podman -- true\n\n# or permanently, in ~/.config/sandbox/config.yaml:\n#   engine: podman",
        body: "Docker stays the default. The engine is a user-config key rather than a project one: a repository that could choose which binary runs on your machine would be choosing what executes.",
      },
      {
        title: "Expect the image to build again",
        body: "Podman keeps its own image store, so the first run rebuilds the base image even if Docker already has it. That is one wait, not a recurring cost.",
      },
      VERIFY_STEP,
      FIRST_RUN_STEP,
    ],
  },
];
