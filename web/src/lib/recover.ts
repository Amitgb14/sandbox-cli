/**
 * Snapshots and recovery, as data.
 *
 * Mirrors the repository — `internal/rescue`, `internal/cli/recover.go`,
 * `internal/studioapi/snapshots.go` and `docs/proposals/crash-recovery.md`. If
 * the CLI's behaviour changes, edit this file and the page follows.
 *
 * Written from a support case rather than from the design docs, and that shaped
 * every section. Somebody ran an agent, it wrote a file, they killed it before it
 * committed, they restored — and the file was not there. Nothing had failed. The
 * only snapshot was the one taken *before* the run, because of which front end
 * started it, and no screen said so. So this page leads with what each way of
 * running gets you, not with what a snapshot is.
 */

/** What is captured, and what is deliberately not. */
export const CAPTURED = [
  {
    what: "Uncommitted changes to tracked files",
    included: true,
    body: "The whole point. A snapshot is a commit of the working tree, not a diff against one.",
  },
  {
    what: "Untracked files",
    included: true,
    body: "Also the point: a file the agent created and never added is the one most likely to exist nowhere else.",
  },
  {
    what: "Files matched by .gitignore",
    included: false,
    body: "Honoured, so a gitignored .env an agent wrote is not recoverable this way. That is a deliberate limit rather than an oversight — the alternative is a mechanism that quietly copies your secrets into git objects.",
  },
  {
    what: "Files over 10 MiB",
    included: false,
    body: "Skipped, and reported by name once per session rather than silently. A snapshot every two minutes is not the place for build output.",
  },
  {
    what: "The container, the image, the agent's login",
    included: false,
    body: "None of it. A snapshot holds files, which is what makes it cheap to take and safe to keep — and why restoring one is not a way to resume a stopped machine.",
  },
];

/**
 * The section that would have prevented the support case: how you start the agent
 * decides how much is captured, and the difference is invisible until you need
 * it.
 *
 * A fleet task sits on the *none* row, not with Studio. `baselineFor` is called
 * only from internal/studioapi, and internal/fleet does not import the rescue
 * package at all — so a fleet task records nothing, where the first draft of this
 * page told a fleet user they held a before-image they do not have. On the one
 * page whose job is to say what protection each path gets, that was the worst
 * available mistake.
 */
export type Path = {
  how: string;
  cadence: string;
  /** "full" | "one" | "none" — drives the visual weight. */
  level: "full" | "one" | "none";
  body: string;
};

export const PATHS: Path[] = [
  {
    how: "sandbox-cli claude",
    cadence: "Every 2 minutes",
    level: "full",
    body: "A foreground run gets the real safety net: at the start, every two minutes, and on the way out — including on Ctrl-C. This is the only shape that protects work an agent has not committed.",
  },
  {
    how: "--detach, and every fleet task",
    cadence: "None",
    level: "none",
    body: "A detached run returns before the snapshot loop is reached, and there is no process left to hold a ticker. A fleet task is a detached run, so it records nothing either — internal/fleet does not touch the rescue package at all. The container outlives your terminal; the safety net does not exist.",
  },
  {
    how: "Studio",
    cadence: "One, before the agent starts",
    level: "one",
    body: "The daemon records a baseline — the workspace as the run began — and closes the session immediately. Kill an agent before it commits and the only snapshot you have is the state from before it did anything. This is the only path that records a baseline; nothing else does.",
  },
];

/** What a baseline is, said plainly, because it reads as a snapshot and is not. */
export const BASELINE = {
  title: "A baseline is not a recovery point",
  body: "It is the before-image a daemon run records at launch. `recover list` names it `baseline` and says so; restoring one gives you the state the run started from, which is occasionally what you want and is never what somebody hunting for lost work means. Studio hides them from its Snapshots screen for that reason — it offers a Restore button beside every row, and offering to restore a before-image is the trap.",
};

export type Mode = {
  cmd: string;
  what: string;
  /** The thing that could go wrong, where there is one. */
  careful?: string;
};

export const MODES: Mode[] = [
  {
    cmd: "sandbox-cli recover list",
    what: "Every session recorded for this repository, newest first, with what it left behind and whether the objects are still there.",
  },
  {
    cmd: "sandbox-cli recover show ID",
    what: "What is in a snapshot, before you decide anything. Any unambiguous prefix of the id works.",
  },
  {
    cmd: "sandbox-cli recover restore ID",
    what: "Creates a branch pointing at the snapshot and changes nothing else — not your working tree, not your current branch, not any other branch.",
    careful:
      "The default, and the only mode that cannot destroy anything. --branch NAME picks the name; restoring the same snapshot twice reports that the branch already holds it rather than refusing.",
  },
  {
    cmd: "sandbox-cli recover restore ID --into-worktree",
    what: "Puts the files back where they were.",
    careful:
      "The one mode that overwrites. Refused on a dirty tree rather than offering a force, because after a crash the files on disk may themselves be the newest copy of your work.",
  },
  {
    cmd: "sandbox-cli recover restore ID --patch -o work.patch",
    what: "Writes the changes out as a patch and touches the repository not at all.",
  },
];

/**
 * The half people do not expect to be separate, and the half that actually goes
 * missing after a kill.
 */
export const CONVERSATION = [
  {
    title: "The files are usually already there",
    body: "/workspace is a bind mount, so everything an agent writes lands on your disk the instant it is written. Restore reports when the working tree already matches the snapshot — after a crash that is the common case, and it means nothing was lost to begin with.",
  },
  {
    title: "The conversation is what goes",
    body: "It lives in the container's HOME, not your repository. `recover restore` names it when it can identify one — by agent, project and the run's own time window — and prints the command to reopen it. When it cannot be sure it says how to look instead, because resuming the wrong conversation is worse than offering none.",
  },
  {
    title: "Files and conversation are recovered separately",
    body: "`recover restore` for the work, `--resume <id>` for the thread. Neither knows about the other, which is why you pass both: --worktree decides which files the agent sees, --resume decides which conversation it continues.",
  },
];

/** The off-machine copy, and the one thing people get wrong about its credentials. */
export const MIRROR = {
  yaml: `# ~/.config/sandbox/config.yaml — never a project .sandbox.yaml
snapshot:
  s3:
    bucket: my-sandbox-snapshots
    upload: manual              # manual (default) | all | off
    access_key_env: AWS_ACCESS_KEY_ID       # the variable NAME
    secret_key_env: AWS_SECRET_ACCESS_KEY   # not the value`,
  rules: [
    {
      title: "The credential is named, never held",
      body: "access_key_env is the *name* of an environment variable, read on the machine doing the upload at the moment it uploads. There is nowhere in the config file, the settings file, the API response or the browser for a secret to sit — which is what lets the whole block cross the wire to a screen. Putting a real key in that field does not work and writes your secret to disk.",
    },
    {
      title: "It is a git bundle, not an archive",
      body: "The object in the bucket is a packfile git alone can read on a machine that has never seen the repository: git init && git fetch <bundle>. A `git clone` of it does not work, and that follows from the rule this feature keeps rather than from an oversight — a snapshot ref is not a branch, so the bundle carries no HEAD.",
    },
    {
      title: "A backup, never an offload",
      body: "The local ref stays and is what restore reads. `recover fetch` puts the objects back where they always were, so none of the three restore modes has to learn that a network exists.",
    },
    {
      title: "Retention prunes the local copy only",
      body: "Objects in the bucket are governed by its own lifecycle rules. Deleting an off-machine backup on a timer that runs only while your laptop is open loses the copy that was meant to survive the laptop.",
    },
  ],
};

export const FETCH = [
  {
    cmd: "sandbox-cli recover fetch",
    what: "What the bucket holds for this repository, read from the small manifest stored beside every bundle — so it works on a machine that has never seen these snapshots.",
  },
  {
    cmd: "sandbox-cli recover fetch ID",
    what: "Unpacks it back under refs/sandbox/, after which show, restore and the rest treat it as one that never left.",
  },
  {
    cmd: "sandbox-cli recover fetch --repo-id ID",
    what: "A repository is addressed in the bucket by an id derived from its absolute path, so a clone in a new location looks in a namespace of its own. An empty listing names the other ids that are there.",
  },
];

/** What refuses, and why each refusal is worth having. */
export const GUARANTEES = [
  {
    title: "Your index, HEAD and branches are never written",
    body: "Every capture goes through a private GIT_INDEX_FILE. Nothing appears in git status, nothing is pushed, and no branch moves. The snapshot ref lives under refs/sandbox/ and that is the only namespace this ever creates in.",
  },
  {
    title: "An unchanged tree is refused, not recorded",
    body: "A snapshot id that points at no commit is worse than no snapshot: you find out at the moment you try to roll back.",
  },
  {
    title: "A fetched bundle is checked against what you recorded",
    body: "`git bundle verify` proves a bundle is well formed, not that it is yours. A well-formed bundle of somebody else's commit, served under your key, would otherwise restore as silent success — so the sha is compared and the ref rolled back if it disagrees.",
  },
  {
    title: "Session records live outside the repository",
    body: "In ~/.config/sandbox/rescue/, because the repository is often the broken thing. `recover repair` exists for the case where a killed worktree left git unable to answer at all.",
  },
];
