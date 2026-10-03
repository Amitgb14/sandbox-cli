import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ShieldCheck } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { type NavEntry } from "@/lib/nav";

const TITLE = "Sandbox Studio — sandbox-cli";
const DESCRIPTION =
  "The browser view of your sandboxes: launch a run on your repository, watch its output, type into its terminal, bring its work back and review the diff. One command, served from your machine.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

const NAV: NavEntry[] = [
  { kind: "link", href: "#what", label: "What it is" },
  { kind: "link", href: "#screens", label: "Screens" },
  { kind: "link", href: "#guards", label: "Who may use it" },
];

const OPEN = `cd ~/src/app            # the repository to work on
sandbox-cli studio
# Studio: http://127.0.0.1:7080/#token=cb0fbb4f…
# studio: repository /home/you/src/app
# studio: context local · Ctrl-C to stop`;

const SCREENS = [
  {
    name: "Dashboard",
    what: "What is running and suspended, runs whose work has not come back, and what this sandboxd can do — its backend, capabilities, network ceiling and limits.",
  },
  {
    name: "Sandboxes",
    what: "Every sandbox on the sandboxd, started from Studio, the CLI or an SDK, with its labels. Each one has a real terminal, its processes' output from the first byte, its files, and its audit events.",
  },
  {
    name: "Launch",
    what: "A command, an agent run unattended, or an agent's interactive console, on a clone of your repository — with the same config, profile, network policy, labels, volumes and git identity a sandbox-cli run would get.",
  },
  {
    name: "Runs",
    what: "Runs on your repository and where each one's work is: home, still in a live sandbox (bring it back), in a checkpoint after a crash, or lost.",
  },
  {
    name: "Review",
    what: "Every ref under refs/sandbox/ — bring-backs, checkpoints, fleet tasks — and its diff against what you have checked out. Merging stays a git command you run.",
  },
  {
    name: "Fleet",
    what: "A fleet run's tasks and their verdicts, and landing what verified, through the same refusals sandbox-cli agent fleet land makes.",
  },
  {
    name: "Agents, Volumes, Settings",
    what: "Which agents can run unattended and whose login is saved; named volumes and where each is mounted; the context, its capabilities, and the repositories Studio may act on.",
  },
];

const GUARDS = [
  {
    name: "Loopback only, and a loopback Host",
    why: "Studio listens on 127.0.0.1 and answers only Host headers naming loopback, so a page whose own name resolves to 127.0.0.1 — DNS rebinding — is refused: the name it dialled gives it away.",
  },
  {
    name: "A token per launch",
    why: "A loopback port is reachable by every user on the machine. Every request to Studio's API needs the token in the address sandbox-cli studio prints; it travels in the URL's fragment, which is never sent to a server, and is kept for the tab only.",
  },
  {
    name: "The browser never holds sandboxd's token",
    why: "Studio proxies sandbox calls to the context's sandboxd and adds that sandboxd's token itself. A cross-origin request is refused outright, and a body that is not JSON — the shape of a request that skips a browser's preflight — is refused too.",
  },
];

export default function StudioPage() {
  return (
    <div id="top">
      <SiteHeader nav={NAV} />
      <main className="flex-1">
        <Section id="what">
          <Link
            href="/"
            className="mb-8 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" />
            sandbox-cli
          </Link>
          <SectionHead
            eyebrow="sandbox studio"
            title="Your sandboxes, in a browser, from one command"
            lead="Studio is a client of the same API as the CLI and the SDKs, served by sandbox-cli itself on a loopback port. It talks to whichever sandboxd your context points at — your Mac, your Linux box — and does the host-side work the CLI does: launching on your repository, bringing work back, recovering it, landing a fleet."
          />
          <CodeBlock code={OPEN} />
          <p className="mt-4 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Open the address it prints. <code className="font-mono">--context</code> picks another
            sandboxd, <code className="font-mono">--port</code> another port. Closing Studio leaves every
            sandbox it started running; the next Studio finds them again.
          </p>
        </Section>

        <Section id="screens" tinted>
          <SectionHead
            eyebrow="screens"
            title="Launch, watch, answer, bring back, review"
            lead="Nothing here is a second implementation of the CLI: every screen is an API call or the same host-side code, so a rule the CLI keeps, Studio keeps."
          />
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {SCREENS.map((s) => (
              <div key={s.name} className="flex flex-col gap-2 rounded-xl border bg-card p-5">
                <h3 className="text-[0.95rem] font-semibold tracking-tight">{s.name}</h3>
                <p className="text-sm leading-relaxed text-muted-foreground">{s.what}</p>
              </div>
            ))}
          </div>
        </Section>

        <Section id="guards">
          <SectionHead
            eyebrow="who may use it"
            title="A local tool that can start sandboxes still needs a lock"
            lead="Anything that can reach Studio's API can launch on your repository. These are the three reasons a web page you happen to have open cannot."
          />
          <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
            {GUARDS.map((g) => (
              <div key={g.name} className="flex flex-col gap-2 rounded-xl border bg-card p-5">
                <div className="flex items-start gap-2">
                  <ShieldCheck className="mt-0.5 size-4 shrink-0 text-contained" />
                  <h3 className="text-[0.95rem] font-medium">{g.name}</h3>
                </div>
                <p className="text-sm leading-relaxed text-muted-foreground">{g.why}</p>
              </div>
            ))}
          </div>
        </Section>
      </main>
      <SiteFooter />
    </div>
  );
}
