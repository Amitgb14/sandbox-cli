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
  body: "Drops a single static binary on your PATH, and — on a machine that has none yet — writes ~/.config/sandbox/config.yaml with every default spelled out. `go install github.com/Amitgb14/sandbox-cli/cmd/sandbox-cli@latest` works too if you would rather build it; it writes no config, and the built-in defaults are the stricter ones.",
};

/**
 * The config the installer writes is its own step because of one line in it.
 * It sets `network.mode: default`, which relaxes sandbox-cli's built-in dev
 * default (`allowlist`) — a choice worth seeing on the way in rather than
 * discovering later from a denied connection.
 */
const CONFIG_STEP: SetupStep = {
  title: "Know what the default config chose for you",
  code: "sandbox-cli config path      # which files were consulted\nsandbox-cli config show      # the resolved configuration\n\n# ~/.config/sandbox/config.yaml\n#   profile: dev\n#   network:\n#     mode: default            # <- change to allowlist, or none",
  body: "The file is the trusted layer: everything in it is something you could have typed. It ships `network.mode: default`, so a fresh install reaches the whole internet and works with any agent, model provider or private registry without a domain list to maintain. The host boundary does not depend on that — your home, your keys and your other repositories are still unreachable — but an agent can post what it *can* read anywhere, so change the one word to `allowlist` when you want that bounded. An existing file is never overwritten by an upgrade, and `--no-config` skips writing one.",
};

const VERIFY_STEP: SetupStep = {
  title: "Check the host can actually deliver it",
  code: "sandbox-cli doctor",
  body: "Reports whether the daemon applies a syscall filter, whether a container here can program the egress firewall — tried, not queried — and which OCI runtimes are registered. Add --profile prod on a machine that will run unattended: it turns every warning into a refusal with a non-zero exit, so a scheduler finds out instead of you.",
};

const FIRST_RUN_STEP: SetupStep = {
  title: "Run an agent",
  code: "cd ~/your-project\nsandbox-cli claude",
  body: "The first run is slow twice over and mostly silent: it builds the base image (a few minutes), and the claude wrapper then downloads a self-updating copy of Claude Code into the persisted agent home — a large binary, with no progress shown. Interrupting either throws that work away and the next run starts from scratch, so let the first one finish; later runs start immediately. To watch it instead of guessing, run the install with its output visible: `sandbox-cli run -- sh -c 'curl -fsSL https://claude.ai/install.sh | bash'`. Only this directory is mounted; HOME inside the container is fake and dies with it. Egress follows the config from two steps ago — unrestricted as written, default-deny the moment you set `mode: allowlist` or pass --allow.",
};

/**
 * Uninstall, which the page used to describe in a sentence and never show.
 *
 * It is on the page for the same reason the installer is cautious: the one
 * directory people do not expect to lose is ~/.config/sandbox, which holds
 * every agent login. Someone deciding between the two flags should be able to
 * read what each touches rather than find out.
 */
export const UNINSTALL_STEPS: SetupStep[] = [
  {
    title: "Remove the binary",
    code: "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --uninstall",
    body: "Deletes sandbox-cli from ~/.local/bin (it checks /usr/local/bin too), then *reports* what else is on disk without touching it. Your projects and their .sandbox.yaml files are never touched by either flag, and containers are --rm, so nothing lingers between runs.",
  },
  {
    title: "Then decide about your logins",
    code: "sh install.sh --uninstall --purge",
    body: "--purge additionally deletes ~/.config/sandbox — your config.yaml and every agent login — plus the sandbox-base images and the sandbox-cache-* volumes. It is a separate flag because silently signing you out of Claude, Codex and the rest is not something an uninstaller should do on its own.",
  },
  {
    title: "Or clean up by hand",
    code: "rm -rf ~/.config/sandbox                                  # config + agent logins\ndocker rmi $(docker images -q sandbox-base)               # base image(s)\ndocker volume rm $(docker volume ls -q -f name=sandbox-cache-)   # package caches",
    body: "The same three things --purge removes, in case you want one and not the others — reclaiming the image without losing the logins, say. The plain --uninstall prints these exact commands for whatever it found.",
  },
];

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
      CONFIG_STEP,
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
        code: "sudo usermod -aG docker $USER\n\n# groups only apply to a NEW session. Either log out and back in, or:\nnewgrp docker\nid -nG | tr ' ' '\\n' | grep -qx docker && echo ok",
        body: "Without this every command needs sudo. The trap is the second half: usermod changes the account, but your current shell keeps the old group set, so sandbox-cli goes on reporting `permission denied ... /var/run/docker.sock` however many times you restart the daemon — the missing piece is on the client side, not the daemon's. `newgrp docker` gives you a shell that has it now. And note that membership of the docker group is root-equivalent on the host: anyone in it can start a privileged container mounting /. That is Docker's model rather than sandbox-cli's, and rootless Docker avoids it.",
      },
      INSTALL_STEP,
      CONFIG_STEP,
      {
        title: "Know how ids land here — this is the platform where they are real",
        code: "id -u; id -g                 # you, on the host\n# the container runs as uid 1001 with YOUR gid\nls -ld ~/.config/sandbox/agents/claude",
        body: "Docker Desktop virtualizes bind-mount ownership; native Linux does not, so the numbers actually have to line up. Two consequences. Files the agent writes to /workspace come back owned by uid 1001 — pass --user \"$(id -u):$(id -g)\" for a run where that matters. And sandbox-cli's own state dirs, the persisted agent login above all, are yours at mode 0700 and would be unreadable to uid 1001 — so the container takes your primary group and those dirs are shared with it. That is automatic and needs no flag; before it existed, an agent login worked once and was gone by the next run.",
      },
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
      CONFIG_STEP,
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
      CONFIG_STEP,
      {
        title: "Tell sandbox-cli to use it",
        code: "sandbox-cli run --engine podman -- true\n\n# or permanently, in ~/.config/sandbox/config.yaml:\n#   engine: podman",
        body: "Docker stays the default. The engine is a user-config key rather than a project one: a repository that could choose which binary runs on your machine would be choosing what executes.",
      },
      {
        title: "Expect the image to build again",
        body: "Podman keeps its own image store, so the first run rebuilds the base image even if Docker already has it. That is one wait, not a recurring cost.",
      },
      {
        title: "On native Linux, do not pass --user",
        code: "sandbox-cli run --engine podman -- id      # uid 1001, mapped to you\n# NOT this:\n# sandbox-cli run --engine podman --user \"$(id -u):$(id -g)\" -- id",
        body: "Rootless Podman maps your host user to container uid 0, so the usual Linux advice — passing --user \"$(id -u):$(id -g)\" — maps it into the subuid range instead and makes /workspace unreadable. sandbox-cli renders --userns=keep-id:uid=1001,gid=1001 and relabels bind mounts for SELinux, so files the agent writes come back owned by your own uid:gid. Pass nothing.",
      },
      {
        title: "The Docker advice inverts here, including for logins",
        body: "Under Docker on Linux the container joins your primary group so the persisted agent HOME is reachable from both sides. Podman needs none of that and gets none of it: keep-id already makes container uid 1001 you, so the login directory is simply yours. If you switch a machine between the two engines, expect the agent to log in once per engine — each writes as a different id, and that is the mapping working rather than failing.",
      },
      VERIFY_STEP,
      FIRST_RUN_STEP,
    ],
  },
];
