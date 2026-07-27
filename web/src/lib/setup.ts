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
  /** Set when the path does not work today; the UI leads with this. */
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
    engine: "not supported yet",
    unsupported:
      "sandbox-cli shells out to a binary named docker and cannot yet be pointed at anything else, so Podman does not work today — including through podman-docker, which only supplies the name.",
    steps: [
      {
        title: "What is actually missing",
        body: "Three things, and only the first is trivial. The container engine is hardcoded rather than configurable, so there is no flag or config key to select Podman. The shared network is created with com.docker.network.bridge.enable_icc=false, a Docker bridge option Podman's netavark backend does not accept — and that option is the thing that stops one sandbox reading another. And the seccomp and runtime checks parse `docker info` JSON, whose shape differs under Podman.",
      },
      {
        title: "Why it is worth doing properly",
        body: "Rootless Podman is a genuinely stronger default than rootful Docker, so this is not a box to tick. But an egress allowlist needs to program iptables from inside the container, and a rootless engine may not permit that — in which case sandbox-cli fails closed and refuses the run rather than proceeding unfiltered. Getting that path right is the work, not the flag.",
      },
      {
        title: "In the meantime",
        code: "sandbox-cli doctor",
        body: "If you try Podman behind a docker-named shim anyway, run doctor first. It will tell you whether the daemon can be queried and whether a container can program the firewall, which is exactly where this is expected to fall over.",
      },
    ],
  },
];
