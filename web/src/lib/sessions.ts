/**
 * The session commands, as the terminal actually prints them.
 *
 * A sandbox outlives the process that started it — sandboxd owns it, not the
 * client — so this is the part of the CLI that answers "what is running right
 * now, and how do I get at it". The strings below were captured from the CLI
 * against a sandboxd rather than paraphrased; the refusal `kill` gives for a
 * name the server does not know is the point of its own tab.
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
    blurb: "What exists right now, on the sandboxd this context points at.",
    frames: [
      {
        prompt: "sandbox-cli list",
        header:
          "ID                    NAME   STATE    IMAGE         NETWORK    CREATED              LABELS",
        rows: [
          "sbx_6b6b467a88ae4dae  tests  running  sandbox-base  allowlist  2026-10-02 03:38:32  -",
          "sbx_31c4aa440aa0b3ae  build  running  sandbox-base  allowlist  2026-10-02 03:38:32  team=infra",
        ],
        trailing: ["--label team=infra keeps only the sandboxes carrying that label"],
      },
    ],
    note: "The same listing whichever machine it is: your Mac, a Linux box, the cloud — sandbox-cli context use picks which. Labels are your own metadata; the agent layer adds its own (agent, route.id, route.from), so an agent's runs, and a run that fell back to another agent, are findable by what they were for.",
  },
  {
    id: "logs",
    label: "logs",
    blurb: "A process's output from the start, followed until it exits.",
    frames: [
      {
        prompt: "sandbox-cli logs tests",
        rows: ["ok"],
      },
    ],
    note: "Output is kept by the server per process, from the first byte, so a client that connects late — or reconnects after a closed laptop — still reads it from the beginning. --pid picks a process when a sandbox runs several.",
  },
  {
    id: "attach",
    label: "attach",
    blurb: "Put this terminal on a process that is already running.",
    frames: [
      {
        prompt: "sandbox-cli attach build",
        rows: ["(the process's terminal, at this window's size)"],
      },
    ],
    note: "A process started with a terminal gets yours back, resized as you resize the window. Closing the terminal detaches and leaves the process running: attaching is a way to look, and looking must not be able to end someone's run.",
  },
  {
    id: "kill",
    label: "kill",
    blurb: "Terminate a sandbox — and refuse anything that is not one of ours.",
    frames: [
      {
        prompt: "sandbox-cli kill build",
        rows: [],
      },
      {
        prompt: "sandbox-cli kill postgres",
        rows: ["sandbox-cli: postgres: not_found (404): no such sandbox"],
      },
    ],
    note: "A reference is matched against the server's own sandboxes and is never handed to a backend to resolve, so kill postgres finds nothing rather than your database. Killing discards the VM and its disk and everything on it; what should outlive the sandbox belongs in a volume, or pushed from inside it before it ends.",
  },
];

/** Shown under the tabs — the reason this whole surface exists. */
export const SESSION_NOTE =
  "A kill -9 on sandbox-cli leaves the sandbox running — sandboxd owns it, not the client that started it, and --detach means to. These four commands are how you get back to it; events shows what it did.";
