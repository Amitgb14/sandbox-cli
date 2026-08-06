/**
 * The four session commands, as the terminal actually prints them.
 *
 * A container outlives the process that started it — the daemon owns it, not the
 * `docker run` client — so this is the part of the CLI that answers "what is
 * running right now, and how do I get at it". The strings below are copied from
 * `internal/cli/session.go` rather than paraphrased: the empty-state line, the
 * two notes `attach` prints before handing over the terminal, and the refusal
 * `kill` gives for a container sandbox-cli did not start are each the point of
 * their own tab, and a plausible-looking rewrite would lose it.
 */

export type SessionFrame = {
  /** What you typed, without the `$`. */
  prompt: string;
  /** A table header line, dimmed and never wrapped. */
  header?: string;
  /** Output lines, in order. */
  rows: string[];
  /** Printed after the output, dimmed — hints and refusals. */
  trailing?: string[];
};

export type SessionCommand = {
  id: string;
  label: string;
  /** One line under the tab row: what this command is for. */
  blurb: string;
  frames: SessionFrame[];
  /** The rule worth knowing, in the site's voice. */
  note: string;
};

export const SESSION_COMMANDS: SessionCommand[] = [
  {
    id: "list",
    label: "list",
    blurb: "What is running right now — and, with --all, what has finished.",
    frames: [
      {
        prompt: "sandbox-cli list --all",
        header:
          "ID            NAME                     KIND         AGENT   BRANCH     STATUS      ELAPSED",
        rows: [
          "a1b2c3d4e5f6  sandbox-app-feature-a    fleet        claude  feature-a  running     12m04s",
          "9f2la8hq4vzn  sandbox-app-feature-b    fleet        codex   feature-b  running     11m38s",
          "m4x1pq7bd0cs  sandbox-app-docs         interactive  claude  docs       exited (0)  4m11s",
        ],
        trailing: [
          "watch one with `sandbox-cli logs <id> --follow`, or `sandbox-cli attach <id>`",
        ],
      },
    ],
    note: "KIND is doing real work rather than decorating the row: fleet stop --all does not reach an interactive session, fleet clean does not reap one, and max_parallel does not count one — and the listing was the one place that distinction was invisible, which is exactly where somebody decides what to kill. The ID is the same one stats prints, so a row from either can be pasted into any of the other three.",
  },
  {
    id: "logs",
    label: "logs",
    blurb: "Read what a session has written — finished ones included.",
    frames: [
      {
        prompt: "sandbox-cli logs feature-a --follow",
        rows: [
          "● Implementing the login form in src/auth/",
          "  ⎿ Wrote src/auth/login.tsx (94 lines)",
          "  ⎿ Wrote src/auth/login.test.tsx (61 lines)",
          "● Running the tests",
          "  ⎿ 128 passing",
        ],
      },
    ],
    note: "Detached and fleet containers are deliberately not removed when they exit — their exit code and their logs are the only record the run happened, so --rm would delete exactly what you came back for. sandbox-cli clean reaps them once you have read what you needed.",
  },
  {
    id: "attach",
    label: "attach",
    blurb: "Put this terminal on a session that is already running.",
    frames: [
      {
        prompt: "sandbox-cli attach feature-a",
        rows: [
          "sandbox-cli: attached to sandbox-app-feature-a — Ctrl-C detaches, the agent keeps running",
          "sandbox-cli: this session was started detached, so it has no keyboard: you will see its",
          "             output but cannot type at it",
        ],
      },
    ],
    note: "Ctrl-C detaches and never kills — the signal is not proxied into the container (--sig-proxy=false), because attaching is a way to look and looking must not be able to end someone's run. Both lines above exist so nobody learns them the expensive way: by typing into a container that is not listening, or by pressing Ctrl-C to stop watching and finding they stopped the work.",
  },
  {
    id: "kill",
    label: "kill",
    blurb: "Ask a session to stop — and refuse anything that is not ours.",
    frames: [
      {
        prompt: "sandbox-cli kill feature-b",
        rows: ["stopped sandbox-app-feature-b"],
        trailing: ["logs and exit codes are kept; `sandbox-cli clean` removes them"],
      },
      {
        prompt: "sandbox-cli kill postgres",
        rows: ['no sandbox session matches "postgres"'],
        trailing: [
          "`sandbox-cli list --all` shows every session, finished ones included",
        ],
      },
    ],
    note: "A reference is matched against a listing filtered by our own label and is never handed to the engine to resolve, so kill postgres finds nothing rather than your database. kill is also the one command that will not infer its target when a single sandbox is running: reading the wrong session costs a second, stopping the wrong agent costs its work. SIGTERM and docker's grace period by default; --force is SIGKILL and has to be asked for by name.",
  },
];

/** Shown under the tabs — the reason this whole surface exists. */
export const SESSION_NOTE =
  "A kill -9 on sandbox-cli leaves the agent running and still writing to your project, because the daemon owns the container rather than the client that started it — and --detach means to. These four commands are how you get back to it.";
