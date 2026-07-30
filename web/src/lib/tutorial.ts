/**
 * The getting-started tutorial: the first session, the whole option surface, and
 * the things that actually go wrong.
 *
 * Three kinds of content, deliberately kept apart from the rest of the page:
 *
 *  - TUTORIAL_STEPS is a *sequence*. Every other section on this page explains
 *    one idea well and can be read in any order; this one is the only place that
 *    answers "what do I type first, and how do I know it worked". Each step
 *    carries an `expect`, because a tutorial whose steps cannot be verified is a
 *    list of hopes.
 *  - OPTION_GROUPS is the complete flag surface, grouped by the question you
 *    arrived with. This does not duplicate the dry-run builder: that one is
 *    interactive and deliberately narrow — fifteen flags that *move the
 *    boundary*, so you can watch the argv change. This is the reference half,
 *    including the many flags that move nothing and are therefore uninteresting
 *    to a boundary toy but are exactly what somebody is looking for.
 *  - CHALLENGES is the failure list. It is the section a docs site usually
 *    omits, and the one people actually search for. Every row is something that
 *    happens on a correctly-installed setup, not a bug — a default that bites, a
 *    refusal that reads like a crash, a cost nobody warned about.
 *
 * The dev/prod comparison is NOT here. It lives in lib/deploy.ts, and this
 * section renders that same PROFILE_MATRIX rather than restating it, so the two
 * cannot drift into disagreeing about what a profile does.
 *
 * Everything mirrors the repository: internal/cli flag definitions,
 * internal/config/profile.go, docs/AGENTS.md and docs/GUIDE.md.
 */

/* -------------------------------------------------------------- the steps */

export type TutorialStep = {
  title: string;
  /** Shell to run. Absent when the step is something to understand, not type. */
  code?: string;
  body: string;
  /** How you know it worked — the line that makes the step checkable. */
  expect?: string;
  /** A sharp edge the step has to lead with rather than mention afterwards. */
  warn?: string;
};

export const TUTORIAL_STEPS: TutorialStep[] = [
  {
    title: "Install the binary, and check Docker is actually running",
    code: [
      "curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh",
      "sandbox-cli version",
      "docker info >/dev/null && echo 'docker ok'",
    ].join("\n"),
    body:
      "The installer detects your OS and CPU, verifies the archive against the release checksums.txt, and drops a single binary in ~/.local/bin. No root, no package manager, nothing to add to your shell profile beyond having that directory on PATH. Docker is the one real prerequisite — Docker Desktop on macOS and Windows.",
    expect:
      "A version line and the base image tag, then `docker ok`. If docker is not reachable, everything below fails at the same place with the same message.",
  },
  {
    title: "Ask this host what it can actually deliver",
    code: "sandbox-cli doctor",
    body:
      "Installing the binary is the easy half; whether this machine can provide the isolation is a property of the machine. doctor checks the daemon is reachable, whether a syscall filter is really applied, whether a container here can program the egress firewall — tried, not queried, because rootless and userns-remapped daemons cannot — and which OCI runtimes are registered. Under dev an unsatisfiable control is a warning and the run still starts, so read it and decide.",
    expect:
      "A checklist. The common finding on Docker Desktop is that no seccomp profile is applied; it names the setting to change.",
  },
  {
    title: "Read the command before you run anything",
    code: "cd ~/your-project\nsandbox-cli run --dry-run -- bash",
    body:
      "Every flag resolves into a plain docker invocation and --dry-run prints it without executing. This is worth doing once, properly: the argv is the entire security story, and reading it yourself beats trusting a paragraph on a landing page. Look for the single --mount that is your project, the fake HOME, --rm, --cap-drop ALL and the user it drops to.",
    expect:
      "One bind mount for your project at /workspace. If you see a second host path you did not ask for, that is the thing to ask about.",
  },
  {
    title: "Open a shell inside and try to escape it",
    code:
      "sandbox-cli run -- bash\n\n# then, inside the container:\nls /workspace     # your project\necho $HOME        # /sandbox/home, not yours\nls ~/.ssh         # No such file or directory\nls /host 2>&1     # nothing of the host is mounted",
    body:
      "The fastest way to trust a boundary is to push on it. Your project is there because you named it; your keys are not there because nothing mounted them. Exit the shell and the container is gone — every run is --rm, so the only things that survive are the files in /workspace, which were on your disk the whole time.",
    expect:
      "$HOME is /sandbox/home and ~/.ssh does not exist. Files you create in /workspace are on your host afterwards; files you create anywhere else are not.",
  },
  {
    title: "Run your agent with the prefix",
    code:
      "sandbox-cli claude\nsandbox-cli claude --dangerously-skip-permissions",
    body:
      "The wrapper consumes a leading run of sandbox flags and forwards everything else to the agent verbatim, so the agent's own flags never collide with sandbox-cli's. That second line is the point of the whole project: 'Allow All' is the mode that makes an agent useful, and it is safe here because the blast radius is a directory that dies on exit. The first run builds the base image, which takes a few minutes once.",
    expect:
      "Claude Code starts, with a status line showing the container's memory and CPU. Fifteen agents have a wrapper; four are baked into the image and the rest install themselves on first use.",
  },
  {
    title: "Log in once — the login outlives the container",
    code:
      "# nothing to do; this is the default\nsandbox-cli claude --no-persist-auth    # opt out for a throwaway session",
    body:
      "The agent's whole HOME is a sandbox-owned host directory (~/.config/sandbox/agents/claude), separate from your real ~/.claude, so the login survives a disposable container. Your host conversation history for this one project is mounted too, so a session started on the host resumes inside the sandbox and the other way round. Know what this costs: that directory holds a long-lived OAuth refresh token the agent can read, which is exactly why the egress allowlist is on by default and why prod does not mount it at all.",
    expect:
      "You authenticate on the first run and never again. --no-persist-auth means an ephemeral HOME and logging in every time.",
  },
  {
    title: "When something cannot reach the network, widen it deliberately",
    code:
      "sandbox-cli claude --allow deb.debian.org --allow proxy.golang.org",
    warn:
      "Egress is default-deny. A build that reaches an unlisted host fails, and the refusal is printed with the hostname.",
    body:
      "Runs start with an allowlist covering the agent APIs, npm, PyPI and GitHub, so most projects need nothing added. When one does, --allow names a domain for that run. Enforcement is by name, not address: a proxy inside the container reads the hostname from the TLS SNI or the HTTP Host header and resolves it fresh per connection, so allowing one domain does not quietly admit every other host sharing its IP.",
    expect:
      "`sandbox-cli: egress DENY <host>:443 (not on the egress allowlist)` is the line to look for. It names exactly what to pass to --allow.",
  },
  {
    title: "Let it commit as you",
    code: "sandbox-cli claude --git",
    body:
      "Forwards your git identity and marks /workspace as a trusted directory, so commits carry your name and email instead of failing on dubious ownership. Without it the agent still edits files perfectly well — it just cannot make a clean commit, which is a confusing way to discover a missing flag an hour in.",
    expect:
      "git commit inside the sandbox succeeds and `git log` on the host shows your name on it.",
  },
  {
    title: "Fan out, then go and do something else",
    code:
      'sandbox-cli claude --worktree feat/api --detach -- -p "add pagination"\nsandbox-cli ps\nsandbox-cli stats\nsandbox-cli worktree commit feat/api -m "wip"',
    body:
      "Isolation stops being a tax the moment it lets you do something you could not do before. --worktree runs the agent in a real git worktree for its own branch, in its own container, so three agents on three branches never collide. --detach backgrounds it and deliberately keeps the container after it exits, because the exit code and the logs become interesting exactly when the run is over.",
    expect:
      "ps lists the container as sandbox-<repo>-<branch>. Starting a second agent on the same branch is refused by the engine's own duplicate-name check — one agent per branch, enforced rather than remembered.",
  },
  {
    title: "Know how to pick up the pieces",
    code:
      "sandbox-cli recover        # what a crashed run left behind\nsandbox-cli context list   # conversations agents have had here\nsandbox-cli clean          # reap exited containers",
    body:
      "A run is snapshotted into refs/sandbox/snapshots while it is in flight, using a private index so your own index, HEAD and working tree are never touched. After a crash the files are usually already on disk — recover says so plainly rather than pretending it saved you — and it correlates the run with its transcript to print the resume command. If it cannot identify the conversation with confidence it tells you how to look instead of guessing, because resuming the wrong one is worse than offering none.",
    expect:
      "On a clean exit there is nothing to recover, and it says so. That is the answer you want to have seen before the day you need it.",
  },
];

/* ------------------------------------------------------------- the options */

/** Which way an option moves the boundary. */
export type Direction = "widen" | "tighten" | "neutral";

export type TutorialOption = {
  flag: string;
  /** The equivalent .sandbox.yaml / config.yaml key, when there is one. */
  key?: string;
  what: string;
  /** What happens when you do not pass it. */
  fallback: string;
  dir: Direction;
  /** Refused from a project .sandbox.yaml as a privileged key. */
  userConfigOnly?: boolean;
};

export type OptionGroup = {
  id: string;
  label: string;
  /** The question somebody arrives with, in their words. */
  question: string;
  options: TutorialOption[];
};

export const OPTION_GROUPS: OptionGroup[] = [
  {
    id: "where",
    label: "Where it runs",
    question: "Which directory does the agent see, and as whom?",
    options: [
      {
        flag: "--project DIR / -p",
        key: "—",
        what: "The one host directory mounted at /workspace.",
        fallback: "The current working directory.",
        dir: "neutral",
      },
      {
        flag: "--worktree BRANCH",
        key: "—",
        what: "Runs in a real git worktree for BRANCH, created if absent, so per-branch agents never collide.",
        fallback: "The project directory itself, on whatever branch is checked out.",
        dir: "neutral",
      },
      {
        flag: "--workdir DIR / -w",
        key: "workdir",
        what: "Working directory inside the container.",
        fallback: "/workspace",
        dir: "neutral",
        userConfigOnly: true,
      },
      {
        flag: "--user USER",
        key: "user",
        what: "Container user: a name, a uid, or uid:gid.",
        fallback: "sandbox — non-root, and agents refuse --dangerously-skip-permissions as root anyway.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--image REF / -i",
        key: "image",
        what: "Use a different base image.",
        fallback: "The embedded base image, built on first use and tagged by content.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--config PATH / -c",
        key: "—",
        what: "Load this config file explicitly. Typing the path is the deliberate act discovery never involves, so a file loaded this way is trusted.",
        fallback: "Built-in defaults, then ~/.config/sandbox/config.yaml, then the nearest .sandbox.yaml.",
        dir: "neutral",
      },
    ],
  },
  {
    id: "reach",
    label: "What it can reach",
    question: "What is on the other side of the boundary?",
    options: [
      {
        flag: "--network MODE",
        key: "network.mode",
        what: "allowlist (default), default (unrestricted), or none.",
        fallback: "allowlist, with the built-in baseline on.",
        dir: "neutral",
      },
      {
        flag: "--allow DOMAIN",
        key: "network.allow",
        what: "Permit a domain, by name, resolved fresh per connection. Repeatable.",
        fallback: "The 9 baseline domains: the agent APIs, npm, PyPI and GitHub.",
        dir: "widen",
      },
      {
        flag: "—",
        key: "network.baseline: false",
        what: "Drop the built-in domains so your allow list is the whole list. The baseline contains github.com, a write endpoint.",
        fallback: "Baseline on.",
        dir: "tighten",
      },
      {
        flag: "--mount SRC:DST[:ro|rw] / -m",
        key: "mounts",
        what: "An extra host path. Repeatable.",
        fallback: "Nothing but the project. /, your home and any ancestor of it are refused outright, by device and inode rather than by string.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--publish SPEC / -P",
        key: "ports",
        what: "Publish a container port. Binds 127.0.0.1 unless you give an address.",
        fallback: "Nothing published; the boundary stays one-way.",
        dir: "widen",
      },
      {
        flag: "--host-gateway",
        key: "—",
        what: "Map host.docker.internal so the agent can reach an MCP server on your host.",
        fallback: "The name is mapped to the container's own loopback, so the documented route to the host resolves to nothing useful.",
        dir: "widen",
      },
      {
        flag: "--paste",
        key: "—",
        what: "Mount ~/Desktop, ~/Downloads and ~/Pictures read-only at their host paths, so an image path pasted into the agent resolves.",
        fallback: "A pasted image is a host path that does not exist in the container.",
        dir: "widen",
      },
      {
        flag: "--share",
        key: "—",
        what: "Mount ~/.config/sandbox/shared at /shared so agents in different projects can exchange files.",
        fallback: "No channel between concurrent sandboxes.",
        dir: "widen",
      },
      {
        flag: "--share-name NAME",
        key: "—",
        what: "With --share, mount the NAME subdirectory at /shared/NAME instead of the root.",
        fallback: "The whole shared directory, which two concurrent runs will write over each other in.",
        dir: "tighten",
      },
      {
        flag: "--add-host HOST:IP",
        key: "—",
        what: "An extra /etc/hosts entry, passed through to the engine. Repeatable.",
        fallback: "The image's own resolution. An explicit mapping wins over sandbox-cli's own entry.",
        dir: "widen",
      },
    ],
  },
  {
    id: "identity",
    label: "Who it is",
    question: "What credentials and identity does it carry?",
    options: [
      {
        flag: "--git",
        key: "—",
        what: "Forward your git identity and trust /workspace, so commits work.",
        fallback: "The agent edits fine but cannot commit cleanly.",
        dir: "neutral",
      },
      {
        flag: "--secret NAME=SRC",
        key: "secrets",
        what: "A brokered credential: file:PATH, cmd:COMMAND or env:VAR, resolved on the host at run time and kept off the argv. Repeatable.",
        fallback: "No secret is forwarded.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--env K=V / -e",
        key: "env",
        what: "Set a variable, or forward the host value with a bare KEY.",
        fallback: "Nothing crosses. Some names are refused entirely — they are instructions to the root phase, not settings.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--env-allow NAME",
        key: "env_allow",
        what: "Forward this host variable if it is set. Repeatable.",
        fallback: "Each agent's own small suggested list, applied only when the value exists.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--no-persist-auth",
        key: "persist_auth: false",
        what: "Do not mount the persisted agent HOME, so no long-lived refresh token is in the container.",
        fallback: "The login persists across runs, in a sandbox-owned directory separate from your real one.",
        dir: "tighten",
      },
      {
        flag: "--no-sync",
        key: "sync: false",
        what: "Do not mount your host conversation history for this project.",
        fallback: "This project's history bucket is mounted, so sessions resolve on both sides.",
        dir: "tighten",
      },
    ],
  },
  {
    id: "limits",
    label: "How much it can use",
    question: "What stops a runaway agent taking the machine with it?",
    options: [
      {
        flag: "--profile dev|prod",
        key: "profile",
        what: "dev warns when a control cannot be satisfied; prod refuses. A project file may raise this, never lower it.",
        fallback: "dev.",
        dir: "neutral",
      },
      {
        flag: "--memory SIZE",
        key: "security.memory",
        what: "Container memory limit, e.g. 4g.",
        fallback: "Unlimited — an OOM-kill mid-task destroys work in a way an observed container does not.",
        dir: "tighten",
        userConfigOnly: true,
      },
      {
        flag: "--cpus N",
        key: "security.cpus",
        what: "CPU limit, e.g. 2.",
        fallback: "Unlimited.",
        dir: "tighten",
        userConfigOnly: true,
      },
      {
        flag: "--runtime NAME",
        key: "runtime",
        what: "A stronger-isolation OCI runtime: runsc (gVisor) or kata-runtime (microVM). Must already be registered with the engine.",
        fallback: "The engine default (runc) — a boundary against a mistaken agent, not a determined one.",
        dir: "tighten",
        userConfigOnly: true,
      },
      {
        flag: "--no-hardening",
        key: "—",
        what: "Debug escape hatch: drops cap-drop, no-new-privileges and the pids limit.",
        fallback: "All three on, plus a 1024 pids cap. This contradicts the allowlist, so the default egress posture yields with a warning.",
        dir: "widen",
      },
    ],
  },
  {
    id: "lifecycle",
    label: "Lifecycle and safety net",
    question: "How do I run it in the background, and what happens if it crashes?",
    options: [
      {
        flag: "--dry-run",
        key: "—",
        what: "Print the docker command and exit. Reads no secrets and runs nothing.",
        fallback: "The run happens.",
        dir: "neutral",
      },
      {
        flag: "--detach",
        key: "—",
        what: "Background it and print the container name. Replaces the TTY and keeps the container after exit, so the exit code and logs survive.",
        fallback: "Interactive, attached, and removed on exit.",
        dir: "neutral",
      },
      {
        flag: "--cache",
        key: "cache.paths",
        what: "Persist npm/pip/cargo/go caches in named volumes across runs.",
        fallback: "Every run downloads again. Note the volumes are shared across projects by design — that is why they are opt-in.",
        dir: "widen",
        userConfigOnly: true,
      },
      {
        flag: "--no-snapshot",
        key: "snapshot",
        what: "Turn off the periodic workspace snapshot into refs/sandbox.",
        fallback: "Snapshots every 2m, via a private git index, so your index, HEAD and working tree are never written.",
        dir: "neutral",
      },
      {
        flag: "--snapshot-interval D",
        key: "snapshot.interval",
        what: "How often to snapshot, e.g. 30s.",
        fallback: "2m.",
        dir: "neutral",
      },
      {
        flag: "--build",
        key: "—",
        what: "Force a rebuild of the base image.",
        fallback: "Built once on first use; the tag is content-addressed, so an image change rebuilds by itself.",
        dir: "neutral",
      },
      {
        flag: "--tty / --no-tty",
        key: "—",
        what: "Force or suppress TTY allocation.",
        fallback: "Interactive when your terminal is one; never with --detach.",
        dir: "neutral",
      },
      {
        flag: "--no-metrics",
        key: "—",
        what: "Turn off the sticky live resource gauge on non-interactive runs.",
        fallback: "The gauge is shown; sandbox-cli measures and never throttles.",
        dir: "neutral",
      },
    ],
  },
];

/* ------------------------------------------------------- what goes wrong */

export type Challenge = {
  symptom: string;
  /** Why it happens — the design decision behind it, not an apology. */
  cause: string;
  /** What to actually do. Shown as a command when it is one. */
  fix: string;
  fixCode?: string;
  /** Which deployment hits this. */
  scope: "dev" | "prod" | "both";
};

export const CHALLENGES: Challenge[] = [
  {
    symptom: "Every command in this directory fails after upgrading",
    cause:
      "A .sandbox.yaml with network.mode: default. It used to match the built-in default and do nothing; now that egress is default-denied it is a weakening, and a project file may only tighten. Older `sandbox-cli init` scaffolds wrote exactly that line, so any project scaffolded then is affected — including read-only commands.",
    fix: "Remove the key, or load the file deliberately with --config, which is trusted because typing the path is a decision.",
    fixCode: "sandbox-cli run --config ./.sandbox.yaml -- bash",
    scope: "both",
  },
  {
    symptom: "Claude Code silently stops updating itself",
    cause:
      "The self-updating install lives in the persisted HOME and fetches from claude.ai and downloads.claude.ai. Neither is in the baseline, so with the allowlist on by default the download is refused, and the run falls back to the copy baked into the image — which is root-owned and can never update.",
    fix: "Allow both hosts, or put them in your own config so every run has them.",
    fixCode: "sandbox-cli claude --allow claude.ai --allow downloads.claude.ai",
    scope: "both",
  },
  {
    symptom: "A new agent's first run fails with exit 127",
    cause:
      "Eleven of the fifteen agents are not baked into the image — they install themselves into the persisted HOME on first use, which keeps hundreds of megabytes of adapters you will never run out of the image. That install needs the vendor's download host reachable at that moment.",
    fix: "Allow the agent's install host. docs/AGENTS.md has the per-agent list; cursor, aider, openhands and continue each need one.",
    fixCode: "sandbox-cli cursor --allow cursor.com --allow downloads.cursor.com",
    scope: "both",
  },
  {
    symptom: "My model provider is refused, but Anthropic works",
    cause:
      "The baseline covers api.anthropic.com and api.openai.com and nothing else. Every other provider is a host you have to name — which is the allowlist working, not failing.",
    fix: "Add the provider's API host for the run, or once in ~/.config/sandbox/config.yaml.",
    fixCode:
      "sandbox-cli gemini --allow generativelanguage.googleapis.com",
    scope: "both",
  },
  {
    symptom: "Runs refuse to start on a CI runner or rootless daemon",
    cause:
      "Enforcing an allowlist means the container starts as root with NET_ADMIN to program iptables and then drops privileges. Rootless Docker, userns-remapped daemons and some CI runners cannot grant that — and the firewall fails closed, so the run aborts rather than proceeding unfiltered. That is the intended direction; before egress was default-denied only people passing --allow ever reached this path.",
    fix: "Decline the allowlist there deliberately, or run doctor first to find out before you schedule anything.",
    fixCode: "sandbox-cli doctor\nsandbox-cli run --network default -- ...",
    scope: "both",
  },
  {
    symptom: '"this docker daemon applies no seccomp profile"',
    cause:
      'Docker Desktop configured with "seccomp-profile": "unconfined". sandbox-cli ships no profile of its own — the daemon default is good and maintaining a custom one is a large ongoing cost — so an absent filter means the container has the full syscall table. It is reported rather than refused under dev, because that is a property of your installation and fixable in its settings.',
    fix: "Settings → Docker Engine, remove that line, apply and restart. Under prod this is a hard failure, not a warning.",
    scope: "both",
  },
  {
    symptom: "--profile prod refuses to start at all",
    cause:
      "Prod turns the baseline off, so a bare --profile prod resolves to an allowlist permitting nothing — which would mean running with no egress firewall wired at all. The strictest-sounding request must not produce the weakest result, so it stops.",
    fix: "Name what the job actually needs, or say network.mode: none to reach nothing on purpose.",
    fixCode:
      'sandbox-cli claude --profile prod \\\n  --allow api.anthropic.com \\\n  -- -p "run the migration"',
    scope: "prod",
  },
  {
    symptom: "A prod run still mounts a host path I did not expect",
    cause:
      "The prod profile asserts network, credentials, seccomp, resource bounds, ports, user and capabilities against the resolved config — but not mounts or caches. A --mount on the command line, or in your own trusted config, is honoured under prod exactly as it is under dev. The non-overridable refusals still apply: never /, never your home, never an ancestor of it.",
    fix: "Read the argv before scheduling. --dry-run is the only thing that shows you every mount that will exist.",
    fixCode:
      "sandbox-cli run --profile prod --allow api.anthropic.com --dry-run -- true",
    scope: "prod",
  },
  {
    symptom: "The agent exhausted host RAM, or the Docker disk image grew",
    cause:
      "Dev leaves memory and CPU unbounded on purpose: agents legitimately spike during builds and test runs, and an OOM-kill mid-task destroys work in a way an unbounded-but-observed container does not. The pids cap is the one default guard, because a fork bomb has no legitimate version. Docker Desktop's disk image is a sparse file that does not shrink when a container is removed.",
    fix: "Ask for limits when the work is untrusted, and watch the disk separately.",
    fixCode: "sandbox-cli run --memory 4g --cpus 2 -- ...\ndocker system df",
    scope: "dev",
  },
  {
    symptom: "The persisted agent home keeps growing",
    cause:
      "The self-updating install keeps every version it has downloaded, and each is a few hundred megabytes. Nothing prunes it, because the directory belongs to the agent — sandbox-cli only provides it.",
    fix: "Remove the old versions by hand; the symlink names the one in use.",
    fixCode: "ls ~/.config/sandbox/agents/claude/.local/share/claude/versions/",
    scope: "dev",
  },
  {
    symptom: "Old base images pile up after every upgrade",
    cause:
      "The image tag is content-addressed over the Dockerfile and the embedded egress proxy, so a change to either produces a new tag and a rebuild. That is what stops a stale proxy silently enforcing your allowlist — but nothing removes the tag it replaced.",
    fix: "Prune periodically. The build cache is usually the larger half.",
    fixCode: "docker image prune -a\ndocker builder prune",
    scope: "both",
  },
  {
    symptom: "A pasted screenshot does not resolve",
    cause:
      "Pasting gives the agent a host path, and that path does not exist inside the container.",
    fix: "--paste mounts ~/Desktop, ~/Downloads and ~/Pictures read-only at their host paths, so the path the agent was handed resolves.",
    fixCode: "sandbox-cli claude --paste",
    scope: "dev",
  },
  {
    symptom: "The agent changed this repository's git config",
    cause:
      "The workspace is the agent's to edit, and agents legitimately run git config. .git/hooks is mounted read-only — planting a hook has no honest use and now fails — but config stays writable, so it is watched instead of sealed.",
    fix: "Read the summary printed at exit before you next run git there. Lines marked ! name a program your own git would run.",
    scope: "both",
  },
  {
    symptom: "An allowlisted domain stops resolving part-way through a run",
    cause:
      "The name-enforcing proxy runs inside the container and nothing restarts it. If it dies, every tcp/80 and tcp/443 connection fails closed — the right direction, but silently, and the symptom looks like a network outage rather than its cause.",
    fix: "Check the proxy is still alive in the container before suspecting the allowlist.",
    fixCode: "docker exec <container> pgrep -a sandbox-egress-proxy",
    scope: "both",
  },
  {
    symptom: "Only Claude Code has a status line",
    cause:
      "Claude Code has a statusLine hook, so the gauge renders inside its own UI through a managed settings file that never touches yours. Neither Gemini CLI nor OpenCode has such a hook. Wrapping their TUIs in tmux to fake one was tried and reverted — it made them render badly, a bad trade for a gauge.",
    fix: "Use a second terminal for every other agent; every run also prints its peak usage on exit.",
    fixCode: "sandbox-cli stats",
    scope: "dev",
  },
];
