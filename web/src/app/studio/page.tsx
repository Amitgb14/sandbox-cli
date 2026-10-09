import type { Metadata } from "next";
import Image from "next/image";
import Link from "next/link";
import { ArrowLeft, ShieldCheck } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { type NavEntry } from "@/lib/nav";
import { docPath } from "@/lib/site";

const TITLE = "Sandbox Studio — sandbox-cli";
const DESCRIPTION =
  "The browser view of your sandboxes: launch a command or an agent, watch its output, type into its terminal, browse its files and read its events. One command, served from your machine.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

const NAV: NavEntry[] = [
  { kind: "link", href: "#what", label: "What it is" },
  { kind: "link", href: "#look", label: "A look" },
  { kind: "link", href: "#screens", label: "Screens" },
  { kind: "link", href: "#guards", label: "Who may use it" },
];

const OPEN = `sandbox-cli studio
# Studio: http://127.0.0.1:7080/#token=cb0fbb4f…
# studio: context local · Ctrl-C to stop`;

const SCREENS = [
  {
    name: "Sandboxes",
    what: "The home screen: every sandbox on the sandboxd, started from Studio, the CLI or an SDK, searched and filtered by state, with what each was given. With none yet, a first-run panel with the code to start one. Each sandbox has an overview, a real terminal, its processes' logs from the first byte, its files, and its audit events.",
  },
  {
    name: "Playground",
    what: "A command, an agent run unattended, or an agent's interactive console, in a fresh sandbox that starts in its own home directory — with the same config, profile, network policy, labels, volumes and agent login a sandbox-cli run would get. Beside the form, the same command run as CLI, curl, Python and TypeScript, to repeat it from a script.",
  },
  {
    name: "Snapshots",
    what: "Sandboxes captured whole — memory, processes and disk — to start new ones from, where the backend offers them.",
  },
  {
    name: "Agents, Volumes, Settings",
    what: "The agents Studio runs, each with a verified headless mode, and whose login is saved; named volumes and where each is mounted; the context and what its sandboxd can deliver.",
  },
  {
    name: "Through a gateway",
    what: "When the context is a sandbox-gateway, Studio asks who the API key is and adds what that key may use: Jobs, Services, Secrets (names only), SSH and Account — and for an admin key, Nodes, Lost sandboxes, Users & keys and Audit. Actions the key's scopes do not allow are not offered. A plain sandboxd shows exactly the screens above.",
  },
  {
    name: "Organizations",
    what: "On a gateway, a switcher at the top of the sidebar lists the organizations you belong to. Switching clears everything shown, so nothing of the last one stays on screen; Create organization (with org:create) makes one you own, and Members lets an owner add and remove people. On a plain sandboxd there is no switcher.",
  },
  {
    name: "A hosted dashboard",
    what: "Built with NEXT_PUBLIC_STUDIO_ADMIN=off, Studio leaves the admin screens out of the bundle entirely, for a dashboard served to many tenants. The gateway's refusal stays the control; the build only means they are not shipped.",
  },
];

/**
 * Screenshots of Studio against a real sandboxd: Firecracker on a Linux host,
 * run without root, so every sandbox's network reads "none". Each is taken in
 * both themes (public/studio/<name>-light.png and -dark.png) and the one
 * matching the site's theme is shown, through the `dark:` variant.
 */
const SHOTS = [
  {
    name: "sandboxes",
    title: "Sandboxes",
    caption: "Every sandbox on the server, wherever it was started, with what it was given and what the machine has left.",
  },
  {
    name: "sandbox",
    title: "One sandbox",
    caption: "Its details, editable while it runs, beside a terminal, its desktop, its processes' output from the first byte, its files and its events.",
  },
  {
    name: "playground",
    title: "Playground",
    caption: "An agent unattended, an agent's console, or a command, with the same run as CLI, curl, Python and TypeScript beside it.",
  },
  {
    name: "images",
    title: "Images",
    caption: "What sandboxes start from: installed ahead of the first one, with size and use, and removed when nothing needs it.",
  },
  {
    name: "templates",
    title: "Templates",
    caption: "Sizes by name, built in or saved, measured against what this endpoint allows.",
  },
  {
    name: "snapshots",
    title: "Snapshots",
    caption: "A running sandbox captured whole, memory and disk, to start new ones from.",
  },
];

function Shot({ name, title, priority }: { name: string; title: string; priority?: boolean }) {
  const frame = "overflow-hidden rounded-xl border bg-card shadow-sm transition-shadow hover:shadow-md";
  return (
    <>
      {(["light", "dark"] as const).map((theme) => (
        <a
          key={theme}
          href={`/studio/${name}-${theme}.png`}
          className={`${frame} ${theme === "light" ? "block dark:hidden" : "hidden dark:block"}`}
        >
          <Image
            src={`/studio/${name}-${theme}.png`}
            width={2160}
            height={1350}
            alt={`Sandbox Studio, the ${title} screen`}
            priority={priority}
            className="h-auto w-full"
          />
        </a>
      ))}
    </>
  );
}

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
      <SiteHeader nav={NAV} installHref="/#install" />
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
            lead="Studio is a client of the same API as the CLI and the SDKs, served by sandbox-cli itself on a loopback port. It talks to whichever sandboxd your context points at — your Mac, your Linux box — and does the host-side work the CLI does, such as copying an agent's saved login in and back out."
          />
          <CodeBlock code={OPEN} />
          <p className="mt-4 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Open the address it prints. <code className="font-mono">--context</code> picks another
            sandboxd, <code className="font-mono">--port</code> another port. Closing Studio leaves every
            sandbox it started running; the next Studio finds them again. Every screen, which ones a
            gateway key&apos;s scopes open, organizations and the hosted build are in{" "}
            <Link href={docPath("studio")} className="underline underline-offset-4">
              the Studio docs
            </Link>
            .
          </p>
        </Section>

        <Section id="look" tinted>
          <SectionHead
            eyebrow="a look"
            title="The same sandboxes, on a page"
            lead="Taken from a real sandboxd: Firecracker microVMs on a Linux machine, run without root, which is why each network says none. Select one for full size."
          />
          <figure className="flex flex-col gap-3">
            <Shot name={SHOTS[0].name} title={SHOTS[0].title} priority />
            <figcaption className="text-sm leading-relaxed text-muted-foreground">
              <span className="font-medium text-foreground">{SHOTS[0].title}.</span> {SHOTS[0].caption}
            </figcaption>
          </figure>
          <div className="mt-8 grid grid-cols-1 gap-x-6 gap-y-8 md:grid-cols-2">
            {SHOTS.slice(1).map((s) => (
              <figure key={s.name} className="flex flex-col gap-3">
                <Shot name={s.name} title={s.title} />
                <figcaption className="text-sm leading-relaxed text-muted-foreground">
                  <span className="font-medium text-foreground">{s.title}.</span> {s.caption}
                </figcaption>
              </figure>
            ))}
          </div>
        </Section>

        <Section id="screens">
          <SectionHead
            eyebrow="screens"
            title="Launch, watch, answer, inspect"
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

        <Section id="guards" tinted>
          <SectionHead
            eyebrow="who may use it"
            title="A local tool that can start sandboxes still needs a lock"
            lead="Anything that can reach Studio's API can start sandboxes, and agents with your saved logins. These are the three reasons a web page you happen to have open cannot."
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
