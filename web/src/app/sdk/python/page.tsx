import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowUpRight, GitBranch, Layers, ShieldCheck } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { LanguageToggle } from "@/components/language-toggle";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { type NavEntry } from "@/lib/nav";
import { REPO_URL, SDK_PATH } from "@/lib/site";
import {
  PY_ASYNC,
  PY_CLONE,
  PY_FIRST,
  PY_INSTALL,
  PY_RULES,
  PY_STEPS,
  PY_STOCK,
  PY_TRAVEL,
} from "@/lib/sdk-python";
import { cn } from "@/lib/utils";

const TITLE = "Python SDK — sandbox-cli";
const DESCRIPTION =
  "Drive sandbox-cli from Python: run commands and agents in isolated containers, sync or async, with no dependencies.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

const NAV: NavEntry[] = [
  { kind: "link", href: "#what", label: "What it is" },
  {
    kind: "group",
    label: "Getting started",
    items: [
      { href: "#install", label: "Install", hint: "two names, and why they differ" },
      { href: "#async", label: "Sync and async", hint: "one implementation, both faces" },
      { href: "#repos", label: "Repositories and steps", hint: "clone, env, a sequence" },
    ],
  },
  {
    kind: "group",
    label: "Examples",
    items: [
      { href: "#stock", label: "Untrusted code, one host", hint: "what allow= actually does" },
      { href: "#multi", label: "Agents that need each other", hint: "handover, then a decision" },
    ],
  },
  { kind: "link", href: "#rules", label: "What it promises" },
];

export default function PythonSdkPage() {
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

          <div className="mb-6">
            <LanguageToggle active="python" />
          </div>

          <SectionHead
            eyebrow="python sdk"
            title="The same boundary, from a Python agent"
            lead={
              <>
                A client for the Studio daemon, so a LangGraph node, a FastAPI handler or a plain{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">while</code>{" "}
                loop can put work in a container. Every gate that makes a sandbox a sandbox is
                applied where the container is built — this package holds no docker socket, shells
                out to nothing, and assembles no argv.
              </>
            }
          />

          <div className="mt-6 flex flex-wrap gap-2">
            <Badge variant="outline">no dependencies</Badge>
            <Badge variant="outline">sync + async</Badge>
            <Badge variant="outline">python 3.9+</Badge>
          </div>
        </Section>

        {/* --------------------------------------------------------- install */}
        <Section id="install">
          <SectionHead
            eyebrow="install"
            title="Two names, and they are not the same"
            lead={
              <>
                The distribution is{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  sandbox-cli-sdk
                </code>{" "}
                because the plain name on PyPI belongs to an unrelated project. The import keeps the
                name you would guess.
              </>
            }
          />

          <div className="grid gap-4 md:grid-cols-2">
            <CodeBlock code={PY_INSTALL} lang="sh" title="install" />
            <CodeBlock code={PY_FIRST} lang="text" title="first script" />
          </div>

          <p className="mt-4 text-[0.85rem] leading-relaxed text-muted-foreground">
            A Studio daemon has to be running —{" "}
            <code className="font-mono text-[0.85em]">sh studio.sh up</code> in a checkout. The port
            and token are read from{" "}
            <code className="font-mono text-[0.85em]">~/.config/sandbox/studio</code>, the same files
            the daemon writes, so there is nothing to paste.
          </p>
        </Section>

        {/* ----------------------------------------------------------- async */}
        <Section id="async">
          <SectionHead
            eyebrow="both faces"
            title="Sync and async, from one implementation"
            lead={
              <>
                Two hand-written clients of one protocol drift. The standard library has no async
                HTTP client, so a native async face would mean a dependency while the sync one needs
                none — and this package is imported into somebody&apos;s agent process. The async
                face runs the same calls in a thread, and a test fails when the two surfaces stop
                matching.
              </>
            }
          />

          <CodeBlock code={PY_ASYNC} lang="text" title="two containers at once" />
        </Section>

        {/* ----------------------------------------------------------- repos */}
        <Section id="repos">
          <SectionHead
            eyebrow="repositories"
            title="Clone it, configure it, run a sequence"
            lead="A repository is named rather than located, and a workspace is a branch's worktree — one tree, one container, one agent."
          />

          <div className="grid gap-4 md:grid-cols-2">
            <CodeBlock code={PY_CLONE} lang="text" title="from GitHub" />
            <CodeBlock code={PY_STEPS} lang="text" title="steps with shared env" />
          </div>

          <div className="mt-4 rounded-2xl border bg-card p-5">
            <GitBranch className="mb-3 size-4 text-muted-foreground" />
            <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
              <code className="font-mono text-[0.85em]">steps</code> stops at the first failure and
              returns what actually ran. That rule is why it exists rather than a{" "}
              <code className="font-mono text-[0.85em]">for</code> loop: a loop that runs everything
              reports the <em>last</em> exit code, so a failed install followed by a passing lint
              looks like success.
            </p>
          </div>
        </Section>

        {/* ----------------------------------------------------------- stock */}
        <Section id="stock">
          <SectionHead
            eyebrow="untrusted code"
            title="One host, and nothing else"
            lead={
              <>
                The smallest program that has to say what code may reach. Worth reading for{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">allow=</code>{" "}
                rather than for the price.
              </>
            }
          />

          <CodeBlock code={PY_STOCK} lang="text" title="examples/stock_price.py" />

          <div className="mt-4 rounded-2xl border border-caution/40 bg-caution/5 p-5">
            <ShieldCheck className="mb-3 size-4 text-caution" />
            <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
              Naming a host turns the allowlist <strong className="font-medium text-foreground">on</strong>{" "}
              for that run. Measured against a daemon with unrestricted egress,{" "}
              <code className="font-mono text-[0.85em]">example.com</code> answers 200 without{" "}
              <code className="font-mono text-[0.85em]">allow</code> and is refused with it — so this
              is not asking for more reach, it is giving up the rest of the internet in exchange for
              one host.
            </p>
          </div>
        </Section>

        {/* ----------------------------------------------------------- multi */}
        <Section id="multi">
          <SectionHead
            eyebrow="agents that need each other"
            title="Handover, then a decision"
            lead={
              <>
                Two specialists in parallel and a coordinator that combines what they produced. Each
                has its own worktree, so the coordinator{" "}
                <strong className="font-medium text-foreground">cannot see</strong> their files:
                artifacts cross through the calling process, base64 in both directions.
              </>
            }
          />

          <CodeBlock code={PY_TRAVEL} lang="text" title="examples/travel_planner.py" />

          <div className="mt-4 grid grid-cols-1 gap-3 md:grid-cols-2">
            <div className="rounded-2xl border bg-card p-5">
              <Layers className="mb-3 size-4 text-muted-foreground" />
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                The gate asks <strong className="font-medium text-foreground">git and the
                filesystem</strong>, not the agent. An agent that reports success having written
                nothing is the commonest failure here, and the one it cannot be asked about.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                A specialist that produced nothing is{" "}
                <strong className="font-medium text-foreground">named as missing</strong>. Telling
                the coordinator to assume the files exist is the natural thing to write, and it fails
                silently: the agent invents plausible inputs and the report reads exactly like a real
                one.
              </p>
            </div>
          </div>
        </Section>

        {/* ----------------------------------------------------------- rules */}
        <Section id="rules">
          <SectionHead
            eyebrow="what it promises"
            title="Six claims, most of them enforced by a test"
            lead="Everything here is checkable. Where a claim is a trade rather than a guarantee, it says which."
          />

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {PY_RULES.map((rule) => (
              <div key={rule.title} className="rounded-2xl border bg-card p-5">
                <h3 className="mb-2 text-[0.9rem] font-medium">{rule.title}</h3>
                <p className="text-[0.85rem] leading-relaxed text-muted-foreground">{rule.body}</p>
              </div>
            ))}
          </div>

          <div className="mt-8 flex flex-wrap gap-3">
            <a
              href={`${REPO_URL}/tree/main/sdk/python`}
              className={cn(buttonVariants({ variant: "outline", size: "sm" }))}
            >
              Source and tests
              <ArrowUpRight className="size-3.5" />
            </a>
            <Link href={SDK_PATH} className={cn(buttonVariants({ variant: "ghost", size: "sm" }))}>
              The TypeScript client
            </Link>
          </div>
        </Section>
      </main>

      <SiteFooter />
    </div>
  );
}
