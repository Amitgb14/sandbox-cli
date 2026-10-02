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

import { MULTI_AGENT_PATH, STUDIO_PATH } from "@/lib/site";

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
  { kind: "link", href: "#why", label: "Why" },
  {
    kind: "group",
    label: "How it works",
    items: [
      {
        href: "#modes",
        label: "Three ways to run it",
        hint: "your Mac, a Linux machine you control, or the cloud — one API",
      },
      {
        href: "#api",
        label: "The API",
        hint: "the CLI, curl, Python and TypeScript, doing the same thing",
      },
      {
        href: "#features",
        label: "Features",
        hint: "every capability, filtered by the question you came with",
      },
      {
        href: "#network",
        label: "Egress by name",
        hint: "an allowlist enforced outside the guest, where the agent cannot reach",
      },
      {
        href: "#workspace",
        label: "Your repository",
        hint: "a git bundle in, verified commits out, checkpoints in between",
      },
      {
        href: "#sessions",
        label: "Sessions",
        hint: "list, follow, attach, stop — and what each one did",
      },
    ],
  },
  {
    kind: "group",
    label: "Agents",
    items: [
      {
        href: "#agents",
        label: "Coding agents",
        hint: "twelve of them, under one prefix, logins kept between runs",
      },
      {
        href: MULTI_AGENT_PATH,
        label: "Running a fleet",
        hint: "one agent per branch, checked before it lands, with fallbacks",
      },
      {
        href: STUDIO_PATH,
        label: "Studio",
        hint: "the browser view of the same sandboxes",
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
        hint: "a Mac, a Linux server, or a client pointed at either",
      },
      {
        href: "#compare",
        label: "Compare",
        hint: "where this sits among the alternatives, including where it loses",
      },
      {
        href: "#install",
        label: "Install",
        hint: "one script; client, server and guest agent",
      },
    ],
  },
];
