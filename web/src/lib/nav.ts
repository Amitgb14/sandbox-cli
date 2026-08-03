/**
 * The top navigation, grouped.
 *
 * This was a flat row of ten links, which stopped working for two reasons at
 * once. It no longer fit — adding the tutorial pushed the row past the width
 * available beside the wordmark and the buttons — and, worse, ten equally
 * weighted labels an inch apart give a reader no way to tell which three are
 * about *learning the tool* and which three are about *what it does*. Length was
 * the symptom; the missing hierarchy was the problem, so widening the container
 * or shrinking the type would have fixed the wrong one.
 *
 * Four entries now: one plain link for the argument the page opens with, and
 * three groups named after the question somebody is holding — how does it work,
 * how do I run it for real, how do I start. Every anchor on the page appears in
 * exactly one of them, including the ones that were never in the nav at all
 * (#network, #parallel, #install were reachable only by scrolling).
 *
 * One entry is a route rather than an anchor (the fleet doc). The type does not
 * distinguish them, because nothing about *navigation* differs — what differs is
 * how the header renders it, and that is a question about `basePath` and
 * client-side routing rather than about this list.
 *
 * Each item carries a `hint`. A dropdown of bare labels is a worse flat list —
 * it costs a click and returns nothing for it. The hint is what makes opening
 * the menu tell you something you did not already know from the label.
 */

import { MULTI_AGENT_PATH } from "@/lib/site";

export type NavLink = {
  href: string;
  label: string;
  /** One line, lower-case, no full stop: what you find there. */
  hint: string;
};

export type NavEntry =
  | { kind: "link"; href: string; label: string }
  | { kind: "group"; label: string; items: NavLink[] };

export const NAV: NavEntry[] = [
  { kind: "link", href: "#threat", label: "Why" },
  {
    kind: "group",
    label: "How it works",
    items: [
      {
        href: "#features",
        label: "Features",
        hint: "thirty-one capabilities, filtered by the question you came with",
      },
      {
        href: "#command",
        label: "The command",
        hint: "build the flags, read the docker argv they produce",
      },
      {
        href: "#config",
        label: "Config file",
        hint: "every flag as a .sandbox.yaml key, and which ones a project may not set",
      },
      {
        href: "#network",
        label: "Egress allowlist",
        hint: "flip default-deny and watch which requests still leave",
      },
      {
        href: "#parallel",
        label: "Parallel agents",
        hint: "three branches, three containers, no collisions",
      },
      {
        href: MULTI_AGENT_PATH,
        label: "Running a fleet",
        hint: "several agents from one file, mixed across tasks, with the work checked before it lands",
      },
      {
        href: "#agents",
        label: "Agents",
        hint: "fifteen adapters, one prefix, your flags forwarded verbatim",
      },
      {
        href: "#studio",
        label: "Sandbox Studio",
        hint: "the browser control plane: a daemon, a UI, and the three refusals between them",
      },
    ],
  },
  {
    kind: "group",
    label: "Deploy",
    items: [
      {
        href: "#deploy",
        label: "Dev & prod",
        hint: "both deployments, walked through step by step",
      },
      {
        href: "#profiles",
        label: "What actually differs",
        hint: "the two profiles side by side, in one table",
      },
      {
        href: "#compare",
        label: "Compare",
        hint: "prior art, including where this one loses",
      },
    ],
  },
  {
    kind: "group",
    label: "Get started",
    items: [
      {
        href: "#setup",
        label: "Setup",
        hint: "from a cold machine to a verified sandbox, per platform",
      },
      {
        href: "#tutorial",
        label: "Tutorial",
        hint: "your first ten minutes, with what you should see at each step",
      },
      {
        href: "#options",
        label: "All options",
        hint: "the whole flag surface, and what happens without each one",
      },
      {
        href: "#troubleshooting",
        label: "Troubleshooting",
        hint: "what goes wrong, and why it is usually working",
      },
      {
        href: "#install",
        label: "Install",
        hint: "one binary, five routes, no package manager",
      },
    ],
  },
];
