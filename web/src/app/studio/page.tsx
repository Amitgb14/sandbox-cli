import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowUpRight, ShieldCheck } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { StudioSetup } from "@/components/studio-setup";
import { StudioPreview } from "@/components/studio-preview";
import { CodeBlock } from "@/components/code-block";
import { type NavEntry } from "@/lib/nav";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { REPO_URL } from "@/lib/site";
import { cn } from "@/lib/utils";

const TITLE = "Sandbox Studio — sandbox-cli";
const DESCRIPTION =
  "A local control plane for sandbox-cli: every agent run, the boundary it ran inside, and what it changed. Bring it up with docker compose or by hand, and understand the three refusals that stand between a web page and your containers.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

/**
 * This page's own nav. Flat rather than grouped, unlike the landing page and the
 * fleet page: there are four destinations and they are already in the order you
 * need them, so grouping would be ceremony imposed on a list short enough to
 * read at a glance.
 */
const NAV: NavEntry[] = [
  { kind: "link", href: "#what", label: "What it is" },
  { kind: "link", href: "#setup", label: "Setting it up" },
  { kind: "link", href: "#guards", label: "The three refusals" },
  { kind: "link", href: "#look", label: "What it looks like" },
];

/**
 * The refusals, stated once here and referenced by the steps rather than
 * repeated in them. Each one presents as "Studio is broken" the first time it
 * fires, so a reader who has met them here recognises a 403 as a check doing its
 * job instead of a bug to work around.
 */
const GUARDS = [
  {
    name: "The Host header must name a loopback address",
    why: "Catches DNS rebinding. A page on attacker.example whose DNS answer is 127.0.0.1 reaches this server with the browser's same-origin policy satisfied — as far as the browser is concerned the origin is attacker.example. What gives it away is the name it dialled.",
    escape: "-allow-host adds to loopback rather than replacing it.",
  },
  {
    name: "An unlisted Origin is refused outright",
    why: "Not merely denied a CORS header. Refusing to reflect an origin only stops a page reading the reply; the request still arrives and still starts a container. A cross-origin POST can skip preflight entirely, so CORS alone never sees it.",
    escape: "-cors-origin http://localhost:3100 for the UI's own origin.",
  },
  {
    name: "Everything but /v1/health needs the bearer token",
    why: "Health is exempt so a client can discover whether the server is up before it has a credential. Non-browser clients send no Origin and are governed by this alone — which is most of what can reach a loopback port.",
    escape: "-token, or $SANDBOX_STUDIO_TOKEN so it stays out of your shell history.",
  },
];

export default function StudioPage() {
  return (
    <div id="top">
      <SiteHeader nav={NAV} />

      <main className="flex-1">
        {/* ------------------------------------------------------------ what */}
        <Section id="what">
          <Link
            href="/"
            className="mb-6 inline-flex items-center gap-1.5 text-[0.8rem] text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" />
            sandbox-cli
          </Link>

          <SectionHead
            eyebrow="sandbox studio"
            title="The same boundary, driven from a browser"
            lead={
              <>
                Studio is a local control plane: every agent run, the boundary it ran inside, and
                what it changed. Nothing about the sandbox changes — a run launched here goes through
                the same resolution as one launched from your shell, and shows up in{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  sandbox-cli list
                </code>{" "}
                alongside it.
              </>
            }
          />

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div className="rounded-2xl border bg-card p-5">
              <Badge variant="outline" className="mb-3 w-fit border-border text-muted-foreground">
                two processes
              </Badge>
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                A <strong className="font-medium text-foreground">daemon</strong> holding the docker
                socket and answering HTTP on loopback, and a{" "}
                <strong className="font-medium text-foreground">Next app</strong> in your browser.
                Almost everything that goes wrong the first time is the two disagreeing about a port,
                an origin, or a token — which is why the walkthrough below tags every step with the
                side it belongs to.
              </p>
            </div>
            <div className="rounded-2xl border border-contained-line bg-contained-soft/40 p-5">
              <ShieldCheck className="mb-3 size-4 text-contained" />
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                The daemon launches containers, so it answers{" "}
                <em className="text-foreground">who may ask it to</em> before it answers anything
                else. A control plane on 127.0.0.1 is reachable by any page you happen to have open,
                and three checks stand in the way — worth reading before you meet them as a 403.
              </p>
            </div>
          </div>
        </Section>

        {/* ----------------------------------------------------------- setup */}
        <Section id="setup" tinted>
          <SectionHead
            eyebrow="bringing it up"
            title="Two ways in, and they do not mix"
            lead="Pick one and follow it through. Running half of each is how you end up with a UI that cannot reach a daemon, which is the single most common way this setup fails."
          />
          <StudioSetup />
        </Section>

        {/* ---------------------------------------------------------- guards */}
        <Section id="guards">
          <SectionHead
            eyebrow="the three refusals"
            title="Why a local tool has authentication at all"
            lead="Loopback is not a boundary. Any page you visit can reach 127.0.0.1, and this process starts containers — so it establishes who is asking before it does anything. Each of these presents as a failure the first time; knowing which one fired is the difference between a fix and a workaround."
          />
          <div className="flex flex-col gap-3">
            {GUARDS.map((g, i) => (
              <div key={g.name} className="rounded-xl border bg-card p-5">
                <div className="flex items-baseline gap-2.5">
                  <Badge
                    variant="outline"
                    className="shrink-0 border-border font-mono text-[0.62rem] font-normal text-muted-foreground"
                  >
                    {i + 1}
                  </Badge>
                  <h3 className="text-[0.9rem] font-medium">{g.name}</h3>
                </div>
                <p className="mt-2.5 text-[0.82rem] leading-relaxed text-muted-foreground">{g.why}</p>
                <p className="mt-2 font-mono text-[0.72rem] text-muted-foreground">{g.escape}</p>
              </div>
            ))}
          </div>

          <div className="mt-6">
            <CodeBlock
              code={[
                "# the shape all three are happy with",
                "export SANDBOX_STUDIO_TOKEN=$(openssl rand -hex 16)",
                'sandbox-studio-api -project "$PWD" -cors-origin http://localhost:3100',
              ].join("\n")}
            />
          </div>
        </Section>

        {/* ------------------------------------------------------------ look */}
        <Section id="look" tinted>
          <SectionHead
            eyebrow="what it looks like"
            title="Runs, the boundary each one got, and a keyboard"
            lead="Three screens from a real session. The captions point at the numbers that are easy to read past — the denominator behind a pass rate, the run kind that explains a missing verify, the dirty count that decides whether a branch can land."
          />
          <StudioPreview />

          <div className="mt-8 flex flex-wrap items-center gap-2.5">
            <a
              href={`${REPO_URL}/blob/main/docker-compose.yml`}
              target="_blank"
              rel="noopener noreferrer"
              className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
            >
              docker-compose.yml
              <ArrowUpRight className="size-3.5" />
            </a>
            <a
              href={`${REPO_URL}/tree/main/docs/studio-api`}
              target="_blank"
              rel="noopener noreferrer"
              className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
            >
              The API contract
              <ArrowUpRight className="size-3.5" />
            </a>
          </div>
        </Section>
      </main>

      <SiteFooter />
    </div>
  );
}
