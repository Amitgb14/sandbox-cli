import { AGENT_SEEDS, BASELINE_EGRESS } from "@/lib/constants";
import type {
  Agent,
  AgentName,
  AuditRecord,
  DaemonInfo,
  DiffFile,
  BrowseEntry,
  BrowseListing,
  DoctorCheck,
  FileContent,
  FileEntry,
  FileListing,
  LogLine,
  MetricSample,
  MetricSeries,
  MountSpec,
  NetworkPosture,
  Profile,
  Project,
  ResolvedConfig,
  Run,
  RunKind,
  RunState,
  SecurityPosture,
  UsageSnapshot,
  Worktree,
  ConversationMessage,} from "@/lib/types";
import { VERIFY_FAILED_EXIT } from "@/lib/types";
import { DAY, HOUR, MINUTE, NOW, ago, ahead, rngFor } from "@/lib/mock/rng";

const HOME = "/Users/amitghadge";
const WORKTREE_ROOT = `${HOME}/.config/sandbox/worktrees`;

/**
 * The repositories the fixtures are about.
 *
 * Deliberately **not exported**, and that is the whole of a bug rather than a
 * style preference. Every screen used to import this list and render it as the
 * repository picker, so a running daemon managing one real repository showed
 * three invented ones — with ids and paths that look exactly like the real
 * thing — and no way to reach the repository actually being managed. Repositories
 * now come from `GET /v1/projects`, which is what makes the list true; this
 * stays because the runs and worktrees below have to be *about* something, and
 * a fixture is only reached when the header is already saying "Fixture data".
 *
 * If you need a repository list in a component, use `useProjects()`. If you need
 * one here, it is because you are building a fixture.
 */
const REPOS = [
  { id: "sandbox-cli-82799c04", name: "sandbox-cli", root: `${HOME}/code/sandbox-cli` },
  { id: "intrupt-web-1f3ab902", name: "intrupt_web", root: `${HOME}/code/intrupt_web` },
  { id: "intrupt-api-9c02de11", name: "intrupt_api", root: `${HOME}/code/intrupt_api` },
] as const;

/**
 * What `GET /v1/projects` answers with no daemon: the fixtures' own repositories,
 * the first standing in as the one Studio was started in.
 */
export const MOCK_PROJECTS: Project[] = REPOS.map((r, i) => ({
  id: r.id,
  name: r.name,
  root: r.root,
  default: i === 0,
}));

const IMAGE = "sandbox-cli/base:0.9.2-4f1c8ad";

/** Branch names an agent fleet would actually produce. */
const BRANCH_SEEDS = [
  { branch: "feat/studio-ui", base: "main", repo: 0 },
  { branch: "feat/egress-proxy-sni", base: "main", repo: 0 },
  { branch: "fix/worktree-symlink-drift", base: "main", repo: 0 },
  { branch: "chore/audit-env-names", base: "main", repo: 0 },
  { branch: "feat/podman-userns-keepid", base: "main", repo: 0 },
  { branch: "docs/roadmap-task-3", base: "main", repo: 0 },
  { branch: "feat/contract-handover", base: "develop", repo: 1 },
  { branch: "fix/hydration-mismatch", base: "develop", repo: 1 },
  { branch: "feat/pricing-table", base: "develop", repo: 1 },
  { branch: "perf/route-prefetch", base: "develop", repo: 1 },
  { branch: "feat/webhook-retries", base: "main", repo: 2 },
  { branch: "fix/session-token-rotation", base: "main", repo: 2 },
  { branch: "test/contract-fixtures", base: "main", repo: 2 },
] as const;

const PROMPTS = [
  "Wire the run-detail metrics tab to the daemon's sample stream and keep the peak summary honest when a container was never sampled.",
  "The egress proxy resolves per connection but the audit line still records the *request*. Add a test that pins the difference.",
  "worktree.Path falls back to a name-derived directory; make Resolve ask git which worktree has the branch and pin it.",
  "Record env var names only in the audit line — no values, and a test that fails if a value can reach the record struct.",
  "Rootless podman maps the host user to container uid 0. Render --userns=keep-id and prove the workspace is writable.",
  "Update docs/roadmap for task 3, saying what shipped and what is still open.",
  "Publish the OpenAPI contract into /shared and regenerate the client types.",
  "Timestamps render differently on server and client. Move relative times behind a mount effect.",
  "Build the pricing table from the plan fixtures; three tiers, annual toggle, no dual-axis anything.",
  "Prefetch the top-level routes on hover, but only when the connection is not metered.",
  "Webhook deliveries need bounded exponential retries with a dead-letter after five attempts.",
  "Session tokens must rotate on privilege change. Add the rotation and the regression test.",
  "Generate contract fixtures from the published schema and assert both services agree.",
];

const VERIFY_CMDS = [
  "make test",
  "go test ./... && make fmt-check",
  "npm run typecheck && npm test",
  "make test-integration",
];

function baselineNetwork(mode: NetworkPosture["mode"], extra: string[] = []): NetworkPosture {
  if (mode === "none") {
    return { mode, baseline: false, allow: [], enforcement: null };
  }
  if (mode === "default") {
    return { mode, baseline: false, allow: [], enforcement: null };
  }
  return {
    mode,
    baseline: true,
    allow: [...BASELINE_EGRESS, ...extra],
    networkName: "sandbox-net",
    enforcement: "name",
  };
}

function security(profile: Profile, memory: string, cpus: string): SecurityPosture {
  return {
    noNewPrivileges: true,
    capDrop: ["ALL"],
    capAdd: profile === "dev" ? ["NET_ADMIN", "NET_RAW"] : ["NET_ADMIN", "NET_RAW"],
    pidsLimit: 1024,
    memory,
    cpus,
    seccomp: "",
    user: "sandbox",
    hardening: true,
  };
}

function mountsFor(
  workspace: string,
  agent: AgentName | null,
  profile: Profile,
  isWorktree: boolean,
  repoRoot: string,
): MountSpec[] {
  const m: MountSpec[] = [
    { host: workspace, container: "/workspace", mode: "rw", origin: "workspace" },
  ];
  if (isWorktree) {
    m.push({ host: `${repoRoot}/.git`, container: `${repoRoot}/.git`, mode: "rw", origin: "worktree-git" });
  }
  // prod does not mount the persisted HOME: there is no refresh token to steal.
  if (agent && profile === "dev") {
    m.push({
      host: `${HOME}/.config/sandbox/agents/${agent}`,
      container: "/sandbox/home",
      mode: "rw",
      origin: "persisted-home",
    });
    if (agent === "claude") {
      m.push({
        host: `${HOME}/.claude/projects/-workspace`,
        container: "/sandbox/home/.claude/projects/-workspace",
        mode: "rw",
        origin: "history",
      });
      m.push({
        host: `${HOME}/.config/sandbox/statusline/managed-settings.json`,
        container: "/etc/claude-code/managed-settings.json",
        mode: "ro",
        origin: "statusline",
      });
    }
  }
  return m;
}

function metricsAt(
  rng: ReturnType<typeof rngFor>,
  memLimitBytes: number,
  t: string,
  load: number,
): MetricSample {
  return {
    t,
    cpuPct: Math.max(0.4, Math.min(load * 100 * rng.float(0.7, 1.25), 780)),
    memBytes: Math.round(memLimitBytes * Math.min(0.94, load * rng.float(0.55, 0.95))),
    memLimitBytes,
    netRxBytes: Math.round(rng.float(2e6, 9e7)),
    netTxBytes: Math.round(rng.float(3e5, 1.2e7)),
    blockReadBytes: Math.round(rng.float(1e6, 4e8)),
    blockWriteBytes: Math.round(rng.float(1e6, 2e8)),
    pids: rng.int(14, 190),
  };
}

/** The run list. 34 runs across three repos, six of them still live. */
function buildRuns(): Run[] {
  const rng = rngFor(0xf1eeded);
  const runs: Run[] = [];

  // Live runs first — a fleet of four on sandbox-cli, one interactive, one
  // detached plain `run`.
  const live: Array<{
    seed: (typeof BRANCH_SEEDS)[number];
    kind: RunKind;
    agent: AgentName | null;
    startedMs: number;
    verify: string | null;
    load: number;
  }> = [
    { seed: BRANCH_SEEDS[0], kind: "interactive", agent: "claude", startedMs: 41 * MINUTE, verify: null, load: 0.62 },
    { seed: BRANCH_SEEDS[1], kind: "fleet", agent: "claude", startedMs: 18 * MINUTE, verify: VERIFY_CMDS[1], load: 0.88 },
    { seed: BRANCH_SEEDS[2], kind: "fleet", agent: "codex", startedMs: 12 * MINUTE, verify: VERIFY_CMDS[0], load: 0.44 },
    { seed: BRANCH_SEEDS[4], kind: "fleet", agent: "droid", startedMs: 7 * MINUTE, verify: VERIFY_CMDS[0], load: 1.31 },
    { seed: BRANCH_SEEDS[6], kind: "fleet", agent: "gemini", startedMs: 24 * MINUTE, verify: VERIFY_CMDS[2], load: 0.35 },
    { seed: BRANCH_SEEDS[10], kind: "interactive", agent: null, startedMs: 3 * MINUTE, verify: null, load: 0.19 },
  ];

  live.forEach((l, i) => {
    const repo = REPOS[l.seed.repo];
    const memLimit = l.kind === "fleet" ? 4 * 1024 ** 3 : 8 * 1024 ** 3;
    const workspace = `${WORKTREE_ROOT}/${repo.id}/${l.seed.branch.replace(/\//g, "-")}`;
    runs.push({
      id: `c${(0x8ab3f100 + i * 0x1d33).toString(16)}${rng.int(100000, 999999)}`,
      name: `sandbox-${repo.name}-${l.seed.branch.replace(/\//g, "-")}`,
      kind: l.kind,
      state: "running",
      exitCode: null,
      createdAt: ago(l.startedMs + 4_000),
      startedAt: ago(l.startedMs),
      finishedAt: null,
      durationMs: l.startedMs,
      agent: l.agent,
      command: l.agent
        ? [l.agent, ...(l.kind === "fleet" ? ["-p", "<prompt>", "--dangerously-skip-permissions"] : [])]
        : ["npm", "run", "test:watch"],
      prompt: l.kind === "fleet" ? PROMPTS[l.seed.repo === 0 ? BRANCH_SEEDS.indexOf(l.seed) : 6] : undefined,
      image: IMAGE,
      engine: "docker",
      workspace,
      workdir: "/workspace",
      repoId: repo.id,
      repoName: repo.name,
      branch: l.seed.branch,
      base: l.seed.base,
      verify: l.verify,
      profile: l.kind === "fleet" ? "prod" : "dev",
      network: baselineNetwork("allowlist", l.seed.repo === 1 ? ["fonts.googleapis.com"] : []),
      security: security(l.kind === "fleet" ? "prod" : "dev", `${memLimit / 1024 ** 3}g`, l.kind === "fleet" ? "2" : "4"),
      mounts: mountsFor(workspace, l.agent, l.kind === "fleet" ? "prod" : "dev", true, repo.root),
      envNames: l.agent === "claude" ? ["ANTHROPIC_API_KEY"] : l.agent === "droid" ? ["FACTORY_API_KEY"] : [],
      detached: l.kind === "fleet",
      tty: l.kind === "interactive",
      openStdin: l.kind === "interactive",
      latestMetrics: metricsAt(rng, memLimit, ago(2_000), l.load),
      diffStat: {
        files: rng.int(1, 14),
        insertions: rng.int(8, 620),
        deletions: rng.int(0, 210),
      },
    });
  });

  // Finished runs, walking back over eight days.
  const outcomes: Array<{ state: RunState; exit: number }> = [
    { state: "exited", exit: 0 },
    { state: "exited", exit: 0 },
    { state: "exited", exit: 0 },
    { state: "exited", exit: 0 },
    { state: "exited", exit: 1 },
    { state: "exited", exit: VERIFY_FAILED_EXIT },
    { state: "exited", exit: 137 },
    { state: "exited", exit: 2 },
  ];

  for (let i = 0; i < 28; i++) {
    const seed = BRANCH_SEEDS[i % BRANCH_SEEDS.length];
    const repo = REPOS[seed.repo];
    const o = outcomes[rng.int(0, outcomes.length - 1)];
    const kind: RunKind = rng.chance(0.62) ? "fleet" : "interactive";
    // A plain `run` carries no agent label, so the interactive pool includes
    // null on purpose — `sandbox.agent` is empty for those and the listing has
    // to render that case.
    const agentPool: Array<AgentName | null> =
      kind === "fleet"
        ? ["claude", "cline", "codex", "gemini", "droid", "opencode"]
        : ["claude", "claude", "codex", "qwen", "cursor", null];
    const agent = rng.pick(agentPool);
    const profile: Profile = kind === "fleet" ? "prod" : "dev";
    const duration = rng.int(45, 5400) * 1000;
    const finishedMs = rng.int(20 * MINUTE, 8 * DAY);
    const memLimit = kind === "fleet" ? 4 * 1024 ** 3 : 8 * 1024 ** 3;
    const isWorktree = kind === "fleet" || rng.chance(0.5);
    const workspace = isWorktree
      ? `${WORKTREE_ROOT}/${repo.id}/${seed.branch.replace(/\//g, "-")}`
      : repo.root;
    const verify = kind === "fleet" && rng.chance(0.8) ? rng.pick(VERIFY_CMDS) : null;
    const mode = rng.chance(0.12) ? "none" : "allowlist";

    runs.push({
      id: `c${(0x1a2b0000 + i * 0x9e37).toString(16)}${rng.int(100000, 999999)}`,
      name: isWorktree
        ? `sandbox-${repo.name}-${seed.branch.replace(/\//g, "-")}`
        : `sandbox-${repo.name}-${seed.base}`,
      kind,
      state: o.state,
      exitCode: o.exit,
      createdAt: ago(finishedMs + duration + 3_000),
      startedAt: ago(finishedMs + duration),
      finishedAt: ago(finishedMs),
      durationMs: duration,
      agent: agent ?? null,
      command: agent
        ? [agent, ...(kind === "fleet" ? ["-p", "<prompt>"] : [])]
        : rng.pick([
            ["npm", "test"],
            ["make", "test"],
            ["go", "test", "./..."],
            ["bash"],
          ]),
      prompt: kind === "fleet" ? PROMPTS[i % PROMPTS.length] : undefined,
      image: IMAGE,
      engine: rng.chance(0.1) ? "podman" : "docker",
      workspace,
      workdir: "/workspace",
      repoId: repo.id,
      repoName: repo.name,
      branch: seed.branch,
      base: seed.base,
      verify,
      profile,
      network: baselineNetwork(mode as NetworkPosture["mode"]),
      security: security(profile, `${memLimit / 1024 ** 3}g`, kind === "fleet" ? "2" : ""),
      mounts: mountsFor(workspace, agent ?? null, profile, isWorktree, repo.root),
      envNames: agent === "claude" ? ["ANTHROPIC_API_KEY"] : [],
      detached: kind === "fleet",
      tty: kind === "interactive",
      openStdin: kind === "interactive",
      latestMetrics: null,
      diffStat:
        o.exit === 0
          ? { files: rng.int(1, 22), insertions: rng.int(10, 940), deletions: rng.int(0, 380) }
          : { files: rng.int(0, 5), insertions: rng.int(0, 90), deletions: rng.int(0, 40) },
    });
  }

  return runs.sort((a, b) => {
    const aLive = a.state === "running" ? 1 : 0;
    const bLive = b.state === "running" ? 1 : 0;
    if (aLive !== bLive) return bLive - aLive;
    return new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime();
  });
}

export const MOCK_RUNS: Run[] = buildRuns();

/**
 * A metric series for one run. Live runs get samples up to now; finished runs
 * get the window they actually ran for, which is why the x-axis is relative.
 */
export function mockMetrics(runId: string): MetricSeries {
  const run = MOCK_RUNS.find((r) => r.id === runId);
  const rng = rngFor(hash(runId));
  const memLimit = run?.security.memory
    ? parseInt(run.security.memory, 10) * 1024 ** 3
    : 4 * 1024 ** 3;
  const spanMs = Math.min(run?.durationMs ?? 20 * MINUTE, 60 * MINUTE);
  const count = 60;
  const step = spanMs / count;
  const samples: MetricSample[] = [];
  // A believable agent workload: a build spike, a plateau while it thinks, a
  // second spike when the verify runs.
  for (let i = 0; i < count; i++) {
    const p = i / (count - 1);
    const base = 0.3 + 0.25 * Math.sin(p * Math.PI * 1.4);
    const spike = p > 0.72 && p < 0.86 ? 0.8 : p < 0.12 ? 0.65 : 0;
    samples.push(
      metricsAt(rng, memLimit, new Date(NOW - spanMs + i * step).toISOString(), base + spike),
    );
  }
  return {
    runId,
    samples,
    peak: {
      cpuPct: Math.max(...samples.map((s) => s.cpuPct)),
      memBytes: Math.max(...samples.map((s) => s.memBytes)),
    },
  };
}

function hash(s: string): number {
  let h = 2166136261;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 16777619);
  }
  return h >>> 0;
}

// ---------------------------------------------------------------------------
// Worktrees
// ---------------------------------------------------------------------------

export const MOCK_WORKTREES: Worktree[] = (() => {
  const rng = rngFor(0x77aa11);
  const list: Worktree[] = REPOS.map((repo, i) => ({
    branch: i === 1 ? "develop" : "main",
    path: repo.root,
    head: rng.int(0x100000, 0xffffff).toString(16).padStart(7, "0"),
    repoId: repo.id,
    dirty: [],
    ahead: 0,
    behind: 0,
    base: null,
    runId: null,
    createdAt: ago(90 * DAY),
    verified: null,
    primary: true,
  }));

  BRANCH_SEEDS.forEach((seed, i) => {
    const repo = REPOS[seed.repo];
    const run = MOCK_RUNS.find((r) => r.branch === seed.branch && r.state === "running");
    const dirtyCount = rng.int(0, 6);
    list.push({
      branch: seed.branch,
      path: `${WORKTREE_ROOT}/${repo.id}/${seed.branch.replace(/\//g, "-")}`,
      head: rng.int(0x100000, 0xffffff).toString(16).padStart(7, "0"),
      repoId: repo.id,
      dirty: Array.from({ length: dirtyCount }, () =>
        rng.pick([
          "internal/cli/session.go",
          "internal/runtime/args.go",
          "studio/src/app/page.tsx",
          "docs/roadmap/task-3.md",
          "internal/fleet/spec.go",
          "web/src/lib/agents.ts",
        ]),
      ),
      ahead: rng.int(0, 9),
      behind: rng.int(0, 4),
      base: seed.base,
      runId: run?.id ?? null,
      createdAt: ago(rng.int(1, 20) * DAY),
      verified: run ? null : rng.chance(0.55) ? true : rng.chance(0.5) ? false : null,
      primary: false,
    });
    void i;
  });

  return list;
})();

// ---------------------------------------------------------------------------
// Agents
// ---------------------------------------------------------------------------

export const MOCK_AGENTS: Agent[] = AGENT_SEEDS.map((seed, i) => {
  const rng = rngFor(0x5150 + i);
  const persisted = ["claude", "cline", "codex", "gemini", "droid", "opencode"].includes(seed.name);
  const sessionCount = persisted ? rng.int(3, 148) : 0;
  return {
    name: seed.name,
    label: seed.label,
    persistDir: seed.name,
    envAllow: seed.envAllow,
    env: seed.env,
    delivery: seed.delivery,
    headlessVerified: seed.headlessVerified,
    // Offline, the Launch form's skip-permissions control was permanently
    // disabled without these: it keys off canSkipPermissions, which only the
    // daemon was answering.
    canSkipPermissions: (seed.skipPermissionArgs ?? []).length > 0,
    // The same gap the comment above describes, one field over: without this the
    // conversations panel reads canResume as false for every agent offline, says
    // "claude has no way to reopen a conversation by id", and offers only
    // "brief and start" on rows that would resume perfectly against a daemon.
    // The three that can are the three whose stores declare a resume argv
    // (agentctx/stores.go): claude --resume, codex resume, opencode --session.
    canResume: ["claude", "codex", "opencode"].includes(seed.name),
    skipPermissionArgs: seed.skipPermissionArgs,
    autonomousInvocation: seed.headlessVerified
      ? autonomousArgv(seed.name)
      : undefined,
    auth: {
      persisted,
      path: `${HOME}/.config/sandbox/agents/${seed.name}`,
      lastSeen: persisted ? ago(rng.int(1, 200) * HOUR) : null,
    },
    statusLine: Boolean(seed.statusLine),
    historySync: Boolean(seed.historySync),
    sessions: sessionCount,
    // Only the claude-jsonl reader is written against a confirmed format; every
    // other store lists Partial, and an agent with no verified descriptor is
    // reported untracked rather than guessed at.
    contextStore: seed.name === "claude" ? "verified" : persisted ? "empty" : "untracked",
    docs: `https://github.com/Amitgb14/sandbox-cli/blob/main/docs/AGENTS.md#${seed.name}`,
  };
});

function autonomousArgv(name: AgentName): string[] {
  switch (name) {
    case "claude":
      return ["claude", "-p", "<prompt>", "--dangerously-skip-permissions"];
    case "codex":
      return ["codex", "exec", "--full-auto", "<prompt>"];
    case "gemini":
      return ["gemini", "--yolo", "-p", "<prompt>"];
    case "opencode":
      return ["opencode", "run", "<prompt>"];
    case "droid":
      return ["droid", "exec", "--auto", "high", "<prompt>"];
    default:
      return [name, "<prompt>"];
  }
}

// ---------------------------------------------------------------------------
// Usage
// ---------------------------------------------------------------------------

/**
 * Two windows plus a model-scoped one. Note what is *absent*: the seven-day
 * Opus allowance is past its reset, so it carries no percentage — the cached
 * figure would measure the period before the reset rather than a stale amount of
 * the current one.
 */
export const MOCK_USAGE: UsageSnapshot = {
  agent: "claude",
  canRefresh: true,
  windows: [
    // active mirrors the real payload: the agent marks exactly one window as the
    // one in force, and it is the short one — which is also the first to expire.
    { kind: "five_hour", label: "5-hour", utilization: 23, resetsAt: ahead(2 * HOUR + 14 * MINUTE), active: true },
    { kind: "seven_day", label: "Weekly", utilization: 49, resetsAt: ahead(3 * DAY + 6 * HOUR), active: false },
    {
      kind: "seven_day",
      label: "Weekly",
      active: false,
      utilization: null,
      resetsAt: ago(40 * MINUTE),
      scope: "opus",
    },
  ],
  fetchedAt: ago(3 * HOUR + 12 * MINUTE),
  path: `${HOME}/.config/sandbox/agents/claude/.claude.json`,
};

// ---------------------------------------------------------------------------
// Doctor
// ---------------------------------------------------------------------------

export const MOCK_DOCTOR: DoctorCheck[] = [
  {
    id: "engine",
    title: "Container engine reachable",
    result: "pass",
    detail: "docker 27.4.0 · Docker Desktop 4.38.0 · 8 CPUs · 12.0 GiB to the VM",
    underDev: "fail",
    underProd: "fail",
  },
  {
    id: "seccomp",
    title: "Seccomp profile actually applied",
    result: "pass",
    detail: "Probed by running a syscall the default profile blocks; it was blocked.",
    underDev: "warn",
    underProd: "fail",
  },
  {
    id: "iptables",
    title: "Container can program iptables",
    result: "pass",
    detail:
      "Tried, not queried — nat, owner, conntrack and REDIRECT all succeeded, so the allowlist needs no weaker mode.",
    underDev: "warn",
    underProd: "fail",
  },
  {
    id: "egress-proxy",
    title: "Egress proxy present in image",
    result: "pass",
    detail:
      "sandbox-egress-proxy on PATH and the sandbox-proxy user resolves, so allowlist decisions are made on the hostname.",
    underDev: "warn",
    underProd: "fail",
  },
  {
    id: "userns",
    title: "Workspace writable by the sandbox user",
    result: "pass",
    detail: "Docker Desktop virtualises bind-mount ownership; no keep-id mapping needed.",
    underDev: "fail",
    underProd: "fail",
  },
  {
    id: "runtime",
    title: "Stronger runtimes registered",
    result: "warn",
    detail:
      "Neither gVisor (runsc) nor Kata is registered. Reported, not refused — no profile selects one yet, so failing here would be theatre.",
    underDev: "warn",
    underProd: "warn",
  },
  {
    id: "rescue",
    title: "Crash-recovery store writable",
    result: "pass",
    detail: "~/.config/sandbox/rescue — outside every repo, because the repo is often the broken thing.",
    underDev: "warn",
    underProd: "warn",
  },
  {
    id: "tz",
    title: "Host timezone resolvable",
    result: "pass",
    detail: "Asia/Kolkata forwarded as TZ — a name, never a mount of /etc/localtime.",
    underDev: "warn",
    underProd: "warn",
  },
];

// ---------------------------------------------------------------------------
// Daemon
// ---------------------------------------------------------------------------

export const MOCK_DAEMON: DaemonInfo = {
  // Fixture mode has a posture too. Without one the Launch screen's egress badge
  // renders "…" forever and the preview falls back to whatever the form was
  // initialised with, which is the sort of quiet half-answer the live/fixture
  // badge exists to prevent.
  egress: { mode: "allowlist" as const, baseline: true, domains: BASELINE_EGRESS.length },
  version: "0.9.2",
  engine: "docker",
  engineVersion: "27.4.0",
  host: { os: "darwin", arch: "arm64", cpus: 8, memBytes: 12 * 1024 ** 3 },
  profile: "dev",
  mock: true,
};

// ---------------------------------------------------------------------------
// Logs and terminal
// ---------------------------------------------------------------------------

const CLAUDE_TRANSCRIPT: Array<[LogLine["stream"], string]> = [
  ["stdout", "\u001b[2msandbox-cli 0.9.2 · profile dev · engine docker\u001b[0m"],
  ["stdout", "\u001b[2mfirewall: default-deny programmed, 9 domains permitted (166ms)\u001b[0m"],
  ["stdout", "\u001b[2megress: decisions made on hostname (sandbox-egress-proxy)\u001b[0m"],
  ["stdout", "\u001b[2mprivileges dropped to sandbox (uid 1001)\u001b[0m"],
  ["stdout", ""],
  ["stdout", "\u001b[38;5;213m✻\u001b[0m Welcome to \u001b[1mClaude Code\u001b[0m"],
  ["stdout", "  \u001b[2mcwd: /workspace\u001b[0m"],
  ["stdout", ""],
  ["stdout", "\u001b[1m>\u001b[0m Wire the run-detail metrics tab to the daemon's sample stream."],
  ["stdout", ""],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Read \u001b[1minternal/runtime/inspect.go\u001b[0m \u001b[2m(148 lines)\u001b[0m"],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Grep \u001b[2m\"docker stats\"\u001b[0m \u001b[2m→ 3 files\u001b[0m"],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Read \u001b[1minternal/metrics/metrics.go\u001b[0m \u001b[2m(431 lines)\u001b[0m"],
  ["stdout", ""],
  ["stdout", "  The meter already samples on an interval and keeps a peak. What the"],
  ["stdout", "  detail tab needs is the *series*, not the footer string — so I will"],
  ["stdout", "  expose the readings rather than re-parse the formatted line."],
  ["stdout", ""],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Update \u001b[1minternal/metrics/metrics.go\u001b[0m"],
  ["stdout", "  \u001b[38;5;114m+ 34\u001b[0m  \u001b[38;5;203m- 6\u001b[0m"],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Update \u001b[1minternal/cli/session.go\u001b[0m"],
  ["stdout", "  \u001b[38;5;114m+ 61\u001b[0m  \u001b[38;5;203m- 12\u001b[0m"],
  ["stdout", ""],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Bash \u001b[2mgo test ./internal/metrics\u001b[0m"],
  ["stdout", "  \u001b[2mok  \tgithub.com/Amitgb14/sandbox-cli/internal/metrics\t0.412s\u001b[0m"],
  ["stderr", "\u001b[38;5;214mwarn\u001b[0m  peak summary read 0 samples for a container that exited in 1.2s"],
  ["stdout", ""],
  ["stdout", "  A run that was never sampled must report absent, not zero. Adding the"],
  ["stdout", "  case and a test that pins it."],
  ["stdout", ""],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Update \u001b[1minternal/metrics/metrics_test.go\u001b[0m"],
  ["stdout", "  \u001b[38;5;114m+ 22\u001b[0m"],
  ["stdout", "\u001b[38;5;114m●\u001b[0m Bash \u001b[2mgo test ./internal/metrics\u001b[0m"],
  ["stdout", "  \u001b[2mok  \tgithub.com/Amitgb14/sandbox-cli/internal/metrics\t0.388s\u001b[0m"],
  ["stdout", ""],
  ["stdout", "\u001b[2m· opus 5 · mem 1.4G/4.0G · 5h 23% (2h14m) · wk 49%\u001b[0m"],
];

export function mockLogs(runId: string, count = CLAUDE_TRANSCRIPT.length): LogLine[] {
  const run = MOCK_RUNS.find((r) => r.id === runId);
  const spanMs = Math.min(run?.durationMs ?? 15 * MINUTE, 45 * MINUTE);
  const lines = CLAUDE_TRANSCRIPT.slice(0, count);
  return lines.map(([stream, text], i) => ({
    seq: i,
    ts: new Date(NOW - spanMs + (i * spanMs) / Math.max(lines.length, 1)).toISOString(),
    stream,
    text,
  }));
}

/** The full transcript length, so the streaming view knows where it ends. */
export const MOCK_LOG_LENGTH = CLAUDE_TRANSCRIPT.length;

// ---------------------------------------------------------------------------
// Diff
// ---------------------------------------------------------------------------

export function mockDiff(runId: string): DiffFile[] {
  void runId;
  return [
    {
      path: "internal/metrics/metrics.go",
      status: "modified",
      insertions: 34,
      deletions: 6,
      hunks: [
        {
          header: "@@ -134,7 +134,14 @@ type reading struct {",
          lines: [
            { kind: "ctx", oldNo: 134, newNo: 134, content: "type reading struct {" },
            { kind: "ctx", oldNo: 135, newNo: 135, content: "\tcpuPct  float64" },
            { kind: "ctx", oldNo: 136, newNo: 136, content: "\tmemUsed float64" },
            { kind: "del", oldNo: 137, newNo: null, content: "\tat      time.Time" },
            { kind: "add", oldNo: null, newNo: 137, content: "\tat      time.Time" },
            { kind: "add", oldNo: null, newNo: 138, content: "" },
            { kind: "add", oldNo: null, newNo: 139, content: "\t// ok is false for a container docker never returned a line for." },
            { kind: "add", oldNo: null, newNo: 140, content: "\t// Absent is not zero: a run that exited in a second was not idle," },
            { kind: "add", oldNo: null, newNo: 141, content: "\t// it was never measured, and a gauge reading 0% says the wrong thing." },
            { kind: "add", oldNo: null, newNo: 142, content: "\tok bool" },
            { kind: "ctx", oldNo: 138, newNo: 143, content: "}" },
          ],
        },
        {
          header: "@@ -232,10 +239,24 @@ func (m *Meter) Summary() string {",
          lines: [
            { kind: "ctx", oldNo: 232, newNo: 239, content: "func (m *Meter) Summary() string {" },
            { kind: "ctx", oldNo: 233, newNo: 240, content: "\tm.mu.Lock()" },
            { kind: "ctx", oldNo: 234, newNo: 241, content: "\tdefer m.mu.Unlock()" },
            { kind: "add", oldNo: null, newNo: 242, content: "\tif m.samples == 0 {" },
            { kind: "add", oldNo: null, newNo: 243, content: "\t\treturn \"\"" },
            { kind: "add", oldNo: null, newNo: 244, content: "\t}" },
            { kind: "del", oldNo: 235, newNo: null, content: "\treturn fmt.Sprintf(\"peak %s / %.0f%%\", humanBytes(m.peakMem), m.peakCPU)" },
            { kind: "add", oldNo: null, newNo: 245, content: "\treturn fmt.Sprintf(\"peak %s / %.0f%% over %d samples\"," },
            { kind: "add", oldNo: null, newNo: 246, content: "\t\thumanBytes(m.peakMem), m.peakCPU, m.samples)" },
            { kind: "ctx", oldNo: 236, newNo: 247, content: "}" },
          ],
        },
      ],
    },
    {
      path: "internal/metrics/series.go",
      status: "added",
      insertions: 78,
      deletions: 0,
      hunks: [
        {
          header: "@@ -0,0 +1,20 @@",
          lines: [
            { kind: "add", oldNo: null, newNo: 1, content: "package metrics" },
            { kind: "add", oldNo: null, newNo: 2, content: "" },
            { kind: "add", oldNo: null, newNo: 3, content: "// Series is the readings a Meter has taken, in order." },
            { kind: "add", oldNo: null, newNo: 4, content: "//" },
            { kind: "add", oldNo: null, newNo: 5, content: "// The footer needs one formatted line; anything drawing a chart needs the" },
            { kind: "add", oldNo: null, newNo: 6, content: "// readings themselves. Exposing them is strictly cheaper than re-parsing the" },
            { kind: "add", oldNo: null, newNo: 7, content: "// line the footer already threw away." },
            { kind: "add", oldNo: null, newNo: 8, content: "type Series struct {" },
            { kind: "add", oldNo: null, newNo: 9, content: "\tSamples []Sample" },
            { kind: "add", oldNo: null, newNo: 10, content: "}" },
          ],
        },
      ],
    },
    {
      path: "internal/cli/session.go",
      status: "modified",
      insertions: 61,
      deletions: 12,
      hunks: [
        {
          header: "@@ -88,6 +88,18 @@ func resolveSession(ctx context.Context, in Inspector, ref string) (…) {",
          lines: [
            { kind: "ctx", oldNo: 88, newNo: 88, content: "\t// A reference is matched against a listing filtered by sandbox.cli and is" },
            { kind: "ctx", oldNo: 89, newNo: 89, content: "\t// never handed to the engine to resolve." },
            { kind: "ctx", oldNo: 90, newNo: 90, content: "\tall, err := in.Containers(ctx, map[string]string{sandbox.LabelCLI: \"1\"})" },
            { kind: "add", oldNo: null, newNo: 91, content: "\tif err != nil {" },
            { kind: "add", oldNo: null, newNo: 92, content: "\t\treturn ContainerInfo{}, fmt.Errorf(\"listing sandboxes: %w\", err)" },
            { kind: "add", oldNo: null, newNo: 93, content: "\t}" },
            { kind: "ctx", oldNo: 91, newNo: 94, content: "" },
          ],
        },
      ],
    },
    {
      path: "internal/metrics/metrics_test.go",
      status: "modified",
      insertions: 22,
      deletions: 0,
      hunks: [
        {
          header: "@@ -71,3 +71,25 @@ func TestParseMemUsage(t *testing.T) {",
          lines: [
            { kind: "ctx", oldNo: 71, newNo: 71, content: "}" },
            { kind: "add", oldNo: null, newNo: 72, content: "" },
            { kind: "add", oldNo: null, newNo: 73, content: "// A container that exited before the first sample must report absent." },
            { kind: "add", oldNo: null, newNo: 74, content: "func TestSummaryWithNoSamplesIsEmpty(t *testing.T) {" },
            { kind: "add", oldNo: null, newNo: 75, content: "\tm := &Meter{}" },
            { kind: "add", oldNo: null, newNo: 76, content: "\tif got := m.Summary(); got != \"\" {" },
            { kind: "add", oldNo: null, newNo: 77, content: "\t\tt.Errorf(\"Summary() = %q, want empty — 0%% is a measurement, absent is not\", got)" },
            { kind: "add", oldNo: null, newNo: 78, content: "\t}" },
            { kind: "add", oldNo: null, newNo: 79, content: "}" },
          ],
        },
      ],
    },
    {
      path: "docs/proposals/usage-stats.md",
      status: "modified",
      insertions: 9,
      deletions: 2,
      hunks: [
        {
          header: "@@ -204,8 +204,15 @@ ## Rejected",
          lines: [
            { kind: "ctx", oldNo: 204, newNo: 204, content: "## Rejected" },
            { kind: "del", oldNo: 205, newNo: null, content: "Reading the internal file directly." },
            { kind: "add", oldNo: null, newNo: 205, content: "Reading the internal transcript file directly for the same two numbers:" },
            { kind: "add", oldNo: null, newNo: 206, content: "reaching past a supported contract is a bad trade when the status line is" },
            { kind: "add", oldNo: null, newNo: 207, content: "already handed a documented rate_limits object." },
            { kind: "ctx", oldNo: 206, newNo: 208, content: "" },
          ],
        },
      ],
    },
  ];
}

// ---------------------------------------------------------------------------
// Resolved config
// ---------------------------------------------------------------------------

export function mockConfig(runId: string): ResolvedConfig {
  const run = MOCK_RUNS.find((r) => r.id === runId) ?? MOCK_RUNS[0];
  return {
    profile: run.profile,
    image: run.image,
    workdir: run.workdir,
    user: run.security.user,
    home: "/sandbox/home",
    engine: run.engine,
    network: run.network,
    security: run.security,
    mounts: run.mounts,
    envAllow: run.envNames,
    persistAuth: run.profile === "dev",
    sync: run.agent === "claude" && run.profile === "dev",
    fields: [
      { key: "profile", value: run.profile, layer: run.profile === "prod" ? "flag" : "default" },
      { key: "image", value: run.image, layer: "default" },
      { key: "workdir", value: run.workdir, layer: "default" },
      { key: "user", value: run.security.user, layer: "profile" },
      { key: "network.mode", value: run.network.mode, layer: "profile" },
      { key: "network.baseline", value: String(run.network.baseline), layer: run.profile === "prod" ? "profile" : "default" },
      { key: "network.allow", value: `${run.network.allow.length} domains`, layer: "user" },
      { key: "security.no_new_privileges", value: "true", layer: "default" },
      { key: "security.cap_drop", value: "ALL", layer: "default" },
      { key: "security.pids_limit", value: String(run.security.pidsLimit), layer: "default" },
      { key: "security.memory", value: run.security.memory || "unlimited", layer: "flag" },
      { key: "security.cpus", value: run.security.cpus || "unlimited", layer: run.security.cpus ? "flag" : "default" },
      { key: "persist_auth", value: String(run.profile === "dev"), layer: "profile" },
      // A project .sandbox.yaml is untrusted input: the privilege-relevant keys
      // are refused from it, and the UI says so rather than hiding the attempt.
      { key: "mounts", value: `${run.mounts.length} declared`, layer: "flag", refusedFrom: "project" },
      { key: "security.cap_add", value: run.security.capAdd.join(", "), layer: "default", refusedFrom: "project" },
    ],
    argv: buildArgv(run),
  };
}

/**
 * A display-only model of what `runtime.BuildArgs` emits, in its order. This is
 * a *preview*, never something handed to an engine — the real argv is built by
 * the pure Go function and nothing here should tempt anyone to reimplement it.
 */
export function buildArgv(run: Run): string[] {
  const argv = [run.engine, "run", "--rm"];
  if (run.detached) {
    argv.splice(2, 1, "-d");
  } else {
    argv.push("-it");
  }
  argv.push("--name", run.name);
  argv.push("--label", "sandbox.cli=1");
  argv.push("--label", `sandbox.repo=${run.repoId}`);
  if (run.branch) argv.push("--label", `sandbox.branch=${run.branch}`);
  if (run.agent) argv.push("--label", `sandbox.agent=${run.agent}`);
  if (run.base) argv.push("--label", `sandbox.base=${run.base}`);
  if (run.kind === "fleet") argv.push("--label", "sandbox.fleet=1");
  if (run.verify) argv.push("--label", `sandbox.verify=${run.verify}`);
  for (const m of run.mounts) {
    argv.push("-v", `${m.host}:${m.container}${m.mode === "ro" ? ":ro" : ""}`);
  }
  argv.push("-w", run.workdir);
  argv.push("-e", "HOME=/sandbox/home");
  argv.push("-e", "TZ=Asia/Kolkata");
  if (run.network.mode === "none") {
    argv.push("--network", "none");
  } else if (run.network.mode === "allowlist") {
    argv.push("--network", run.network.networkName ?? "sandbox-net");
    argv.push("--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW");
    argv.push("-e", `SANDBOX_EGRESS_ALLOW=${run.network.allow.join(",")}`);
    argv.push("-e", `SANDBOX_RUN_AS=${run.security.user}`);
  }
  // Published ports, and — under an allowlist only — the carve-out that makes
  // them answer. Both, because the preview exists to show what you are about to
  // get, and the one launch option that opens a way in is the last thing that
  // should be missing from it. The pairing is not decoration: without the
  // carve-out the port is open on the host and refused inside the container.
  for (const p of run.network.ingressPorts ?? []) {
    argv.push("-p", `127.0.0.1:${p}:${p}`);
  }
  if (run.network.mode === "allowlist" && (run.network.ingressPorts ?? []).length > 0) {
    argv.push("-e", `SANDBOX_INGRESS_PORTS=${(run.network.ingressPorts ?? []).join(",")}`);
  }
  argv.push("--security-opt", "no-new-privileges");
  argv.push("--cap-drop", "ALL");
  argv.push("--pids-limit", String(run.security.pidsLimit));
  if (run.security.memory) argv.push("--memory", run.security.memory);
  if (run.security.cpus) argv.push("--cpus", run.security.cpus);
  if (run.network.mode !== "allowlist") argv.push("--user", run.security.user);
  argv.push(run.image);
  argv.push(...run.command);
  return argv;
}

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

export const MOCK_AUDIT: AuditRecord[] = MOCK_RUNS.filter((r) => r.state === "exited").map(
  (r) => ({
    time: r.finishedAt ?? r.createdAt,
    image: r.image,
    workspace: r.workspace,
    workdir: r.workdir,
    agent: r.agent,
    branch: r.branch,
    command: r.command,
    engine: r.engine,
    network: r.network.mode,
    networkName: r.network.networkName ?? "",
    egressEnforcementRequested: r.network.enforcement,
    egressAllow: r.network.allow,
    envNames: r.envNames,
    exitCode: r.exitCode ?? 0,
    durationMs: r.durationMs ?? 0,
    detached: r.detached,
  }),
);

export { HOME, WORKTREE_ROOT };

/**
 * A short conversation for the fixture path, so the console screen can be seen
 * without a daemon. Written as the thing the feature exists for: an agent that
 * stopped to ask something rather than one that guessed.
 */
export const MOCK_CONVERSATION: ConversationMessage[] = [
  {
    role: "user",
    text: "Wire the metrics tab to the sample stream, and keep the peak summary honest when a container was never sampled.",
    at: "2026-07-31T14:02:11Z",
  },
  {
    role: "assistant",
    text: "Read the stream and the tab. One thing to decide before I write it: a run that exited before its first sample has no peak, and the summary currently renders 0 there — which reads as 'it used nothing' rather than 'nobody looked'.\n\nShow a dash for that case, or omit the peak row entirely?",
    at: "2026-07-31T14:03:40Z",
  },
  {
    role: "user",
    text: "A dash. Omitting the row makes the table shift between runs.",
    at: "2026-07-31T14:05:02Z",
  },
  {
    role: "assistant",
    text: "Done — a dash, and the tooltip says the run was never sampled so the two are not confused.",
    at: "2026-07-31T14:07:18Z",
  },
];

// ---------------------------------------------------------------------------
// Files
// ---------------------------------------------------------------------------

/**
 * A small stand-in tree, so the Files screen can be built and reviewed without a
 * daemon. Deliberately tiny and obviously a sample: the real listing comes from
 * the host, and a fixture that imitated a real repository in detail is the same
 * mistake the repository picker used to make.
 */
const MOCK_TREE: Record<string, FileEntry[]> = {
  "": [
    { name: "cmd", path: "cmd", dir: true },
    { name: "internal", path: "internal", dir: true },
    { name: "CHANGELOG.md", path: "CHANGELOG.md", size: 48_120 },
    { name: "README.md", path: "README.md", size: 12_400 },
  ],
  cmd: [{ name: "sandbox-cli", path: "cmd/sandbox-cli", dir: true }],
  "cmd/sandbox-cli": [{ name: "main.go", path: "cmd/sandbox-cli/main.go", size: 780 }],
  internal: [
    { name: "sandbox", path: "internal/sandbox", dir: true },
    { name: "runtime", path: "internal/runtime", dir: true },
  ],
  "internal/sandbox": [{ name: "mounts.go", path: "internal/sandbox/mounts.go", size: 9_310 }],
  "internal/runtime": [{ name: "args.go", path: "internal/runtime/args.go", size: 14_002 }],
};

export function mockFiles(path: string): FileListing {
  return { path, entries: MOCK_TREE[path] ?? [] };
}

export function mockFileContent(path: string): FileContent {
  const body = `// ${path}\n//\n// Fixture content — no daemon answered, so this is a stand-in\n// rather than the file on your disk.\n`;
  return { path, size: body.length, content: body };
}

/**
 * A stand-in directory tree for the folder picker, so the Add-repository dialog
 * can be built without a daemon. Two levels and obviously a sample — the real
 * listing is the host's, and a fixture that imitated one in detail is the
 * mistake the repository picker used to make.
 */
const MOCK_BROWSE: Record<string, BrowseEntry[]> = {
  [HOME]: [
    { name: "code", path: `${HOME}/code` },
    { name: "Documents", path: `${HOME}/Documents` },
  ],
  [`${HOME}/code`]: [
    { name: "sandbox-cli", path: `${HOME}/code/sandbox-cli`, repo: true, registered: true },
    { name: "intrupt_web", path: `${HOME}/code/intrupt_web`, repo: true },
    { name: "scratch", path: `${HOME}/code/scratch` },
  ],
};

export function mockBrowse(path?: string): BrowseListing {
  const at = path ?? HOME;
  const parent = at === "/" ? undefined : at.slice(0, at.lastIndexOf("/")) || "/";
  return { path: at, parent, home: HOME, entries: MOCK_BROWSE[at] ?? [] };
}
