import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowUpRight, Boxes, FileCode, GitBranch, ShieldCheck } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { LanguageToggle } from "@/components/language-toggle";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { type NavEntry } from "@/lib/nav";
import { REPO_URL, STUDIO_PATH } from "@/lib/site";
import {
  SDK_ERRORS,
  SDK_EXAMPLE,
  SDK_HANDOFF,
  SDK_WORKFLOW,
  SDK_PREREQS,
  SDK_REMOTE_STEPS,
  SDK_RULES,
  SDK_SNIPPETS,
  SDK_STEPS,
} from "@/lib/sdk";
import { cn } from "@/lib/utils";

const TITLE = "TypeScript SDK — sandbox-cli";
const DESCRIPTION =
  "Drive sandbox-cli from a program: connect to the local daemon with no configuration, run commands and agents in isolated containers, and get back the exit code, the output, and which agent actually did the work.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

/**
 * This page's own nav.
 *
 * Grouped rather than flat, unlike the first version: eight destinations in a
 * row crowd the wordmark out of the header at any width somebody actually uses,
 * and "A box that is not this one" is a long label to lose a page title to. Two
 * groups, in the order the questions arrive — get it running, then do more with
 * it — with the one link somebody clicks before either kept flat.
 *
 * Every item carries a hint, which is what makes opening a menu worth the click:
 * a dropdown of bare labels is a worse flat list.
 */
const NAV: NavEntry[] = [
  { kind: "link", href: "#what", label: "What it is" },
  {
    kind: "group",
    label: "Getting started",
    items: [
      {
        href: "#before",
        label: "Before it works",
        hint: "the daemon comes first — this package cannot start one",
      },
      {
        href: "#use",
        label: "Using it",
        hint: "connect, pick a repository, run something, read the outcome",
      },
      {
        href: "#snippets",
        label: "Small examples",
        hint: "six whole scripts to paste on the first day",
      },
    ],
  },
  {
    kind: "group",
    label: "Going further",
    items: [
      {
        href: "#remote",
        label: "A remote Linux box",
        hint: "a URL and a token, no tunnel and no CORS",
      },
      {
        href: "#example",
        label: "A whole script",
        hint: "install, hand the work to an agent, run the tests",
      },
      {
        href: "#workflow",
        label: "A workflow, in parallel",
        hint: "three branches, three containers, one gate",
      },
      {
        href: "#handoff",
        label: "Passing work between agents",
        hint: "artifacts cross through the host, on purpose",
      },
      {
        href: "#rules",
        label: "What it promises",
        hint: "eight claims, most of them enforced by a test",
      },
      {
        href: "#errors",
        label: "When it fails",
        hint: "five failures, five different next steps",
      },
    ],
  },
];

export default function SdkPage() {
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
            <LanguageToggle active="typescript" />
          </div>

          <SectionHead
            eyebrow="typescript sdk"
            title="The same boundary, driven from a program"
            lead={
              <>
                Everything the CLI does to keep an agent inside a container happens on the machine
                running the daemon. This package is the way to ask for it from code — a run is a
                container, the worktree is what persists, and the outcome tells you what actually
                happened rather than what you asked for.
              </>
            }
          />

          <div className="mt-3 grid grid-cols-1 gap-3 md:grid-cols-2">
            <div className="rounded-2xl border bg-card p-5">
              <Badge variant="outline" className="mb-3 w-fit border-border text-muted-foreground">
                three nouns
              </Badge>
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                A <strong className="font-medium text-foreground">Studio</strong> is a daemon, a{" "}
                <strong className="font-medium text-foreground">Project</strong> is a repository it
                has been told about, and a{" "}
                <strong className="font-medium text-foreground">Workspace</strong> is a branch&apos;s
                worktree inside one. They are the daemon&apos;s words, not the package&apos;s:
                borrowing “sandbox” from platforms whose sandbox is a machine you keep would promise
                something no endpoint here delivers.
              </p>
            </div>
            <div className="rounded-2xl border border-contained-line bg-contained-soft/40 p-5">
              <ShieldCheck className="mb-3 size-4 text-contained" />
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                It holds no docker socket and shells out to nothing. Adding a capability here means
                the daemon grows an endpoint, so the check that stands between an agent and your
                home directory is written once, in Go, with a test — rather than once per client
                that hopes to be careful.
              </p>
            </div>
          </div>
        </Section>

        {/* ---------------------------------------------------------- before */}
        <Section id="before" tinted>
          <SectionHead
            eyebrow="before it works"
            title="Three steps, and only the last two are this package"
            lead={
              <>
                The client talks to a daemon; it does not start one, and it cannot. Without the
                first step,{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  Studio.connect()
                </code>{" "}
                fails with “cannot reach the sandbox daemon”, which reads as a broken library rather
                than as a daemon nobody started.
              </>
            }
          />

          <ol className="space-y-3">
            {SDK_PREREQS.map((step, i) => (
              <li key={step.label} className="rounded-2xl border bg-card p-5">
                <div className="mb-2 flex items-baseline gap-2">
                  <span className="font-mono text-[0.7rem] text-muted-foreground">
                    {String(i + 1).padStart(2, "0")}
                  </span>
                  <h3 className="text-[0.95rem] font-medium">{step.label}</h3>
                </div>
                <CodeBlock code={step.code} title={step.where} />
                <p className="mt-3 text-[0.85rem] leading-relaxed text-muted-foreground">
                  {step.note}
                </p>
              </li>
            ))}
          </ol>
        </Section>

        {/* ------------------------------------------------------------- use */}
        <Section id="use">
          <SectionHead
            eyebrow="using it"
            title="Zero configuration, then five lines"
            lead="Every snippet below is from the package's README and its tests rather than written for this page."
          />

          <ol className="space-y-4">
            {SDK_STEPS.map((step, i) => (
              <li key={step.title} className="rounded-2xl border bg-card p-5">
                <div className="mb-2 flex items-baseline gap-2">
                  <span className="font-mono text-[0.7rem] text-muted-foreground">
                    {String(i + 1).padStart(2, "0")}
                  </span>
                  <h3 className="text-[0.95rem] font-medium">{step.title}</h3>
                </div>
                {step.code && <CodeBlock code={step.code} lang="ts" />}
                <p className="mt-3 text-[0.85rem] leading-relaxed text-muted-foreground">
                  {step.body}
                </p>
                {step.expect && (
                  <p className="mt-2 text-[0.8rem] leading-relaxed text-muted-foreground">
                    <span className="font-medium text-foreground">You should see: </span>
                    {step.expect}
                  </p>
                )}
              </li>
            ))}
          </ol>
        </Section>

        {/* -------------------------------------------------------- snippets */}
        <Section id="snippets">
          <SectionHead
            eyebrow="small examples"
            title="Six things worth doing on the first day"
            lead="Each is a whole script rather than a fragment, because the first thing anybody does with a new client is paste one and run it."
          />

          <div className="space-y-3">
            {SDK_SNIPPETS.map((s) => (
              <div key={s.title} className="rounded-2xl border bg-card p-5">
                <h3 className="mb-3 text-[0.9rem] font-medium">{s.title}</h3>
                <CodeBlock code={s.code} lang="ts" title="agent.mts" />
                <p className="mt-3 text-[0.85rem] leading-relaxed text-muted-foreground">{s.note}</p>
              </div>
            ))}
          </div>
        </Section>

        {/* ---------------------------------------------------------- remote */}
        <Section id="remote" tinted>
          <SectionHead
            eyebrow="a box that is not this one"
            title="Point it at a Linux machine, with a URL and a token"
            lead={
              <>
                The containers run where the daemon runs, so a beefy Linux box is the whole point.
                A script needs two values from it and nothing else — no tunnel, and none of the CORS
                or Host flags the browser needs, because those checks fire on an{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">Origin</code>{" "}
                header that a browser sends and a script does not.
              </>
            }
          />

          <ol className="space-y-3">
            {SDK_REMOTE_STEPS.map((step, i) => (
              <li key={step.label} className="rounded-2xl border bg-card p-5">
                <div className="mb-2 flex items-baseline gap-2">
                  <span className="font-mono text-[0.7rem] text-muted-foreground">
                    {String(i + 1).padStart(2, "0")}
                  </span>
                  <h3 className="text-[0.95rem] font-medium">{step.label}</h3>
                </div>
                <CodeBlock code={step.code} lang={step.lang} title={step.where} />
                <p className="mt-3 text-[0.85rem] leading-relaxed text-muted-foreground">
                  {step.note}
                </p>
              </li>
            ))}
          </ol>

          <div className="mt-4 rounded-2xl border border-caution/40 bg-caution/5 p-5">
            <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
              <strong className="font-medium text-foreground">There is no TLS yet.</strong> On a
              bound address the token and everything it protects cross the network in cleartext, so
              this is for a network you already trust. For anything wider, put a reverse proxy in
              front and dial its name — the daemon needs{" "}
              <code className="font-mono text-[0.85em]">-allow-host</code> for that name, and your
              script changes by one string.
            </p>
          </div>
        </Section>

        {/* --------------------------------------------------------- example */}
        <Section id="example">
          <SectionHead
            eyebrow="a whole script"
            title="Install, hand the work to an agent, run the tests"
            lead={
              <>
                Everything above in one file — bounded, and checking each claim the outcome makes.
                It is <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  examples/agent-run.ts
                </code>{" "}
                in the package, compiled by its test run, so an example that stopped matching the
                API fails a build rather than misleading somebody who typed it.
              </>
            }
          />

          <CodeBlock code={SDK_EXAMPLE} lang="ts" title="examples/agent-run.ts" />

          <div className="mt-4 grid grid-cols-1 gap-3 md:grid-cols-3">
            <div className="rounded-2xl border bg-card p-5">
              <FileCode className="mb-3 size-4 text-muted-foreground" />
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                The second command finds <code className="font-mono text-[0.85em]">node_modules</code>{" "}
                because the first wrote it to the <strong className="font-medium text-foreground">worktree</strong>,
                not because a process stayed alive. Each run is its own container.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                <code className="font-mono text-[0.85em]">routedFrom</code> is checked because a
                fallback is invisible otherwise: the work gets done by an agent you did not name,
                under its login and its bill.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                <code className="font-mono text-[0.85em]">stopped</code> is checked separately from
                the exit code, and <code className="font-mono text-[0.85em]">WaitError</code> carries
                the run — a container that outlived its deadline is still holding the branch&apos;s
                name until something stops it.
              </p>
            </div>
          </div>
        </Section>

        {/* -------------------------------------------------------- workflow */}
        <Section id="workflow">
          <SectionHead
            eyebrow="many at once"
            title="A workflow, without writing an agent"
            lead={
              <>
                Three tasks, three branches, three containers, in parallel — then one gate deciding
                which of them is worth a human&apos;s attention. The orchestration is{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">Promise.all</code>{" "}
                and an <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">if</code>:
                the only model involved is the one working inside each container. It is{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  examples/workflow.ts
                </code>
                , compiled by the same test run as the script above.
              </>
            }
          />

          <CodeBlock code={SDK_WORKFLOW} lang="ts" title="examples/workflow.ts" />

          <div className="mt-4 grid grid-cols-1 gap-3 md:grid-cols-3">
            <div className="rounded-2xl border bg-card p-5">
              <GitBranch className="mb-3 size-4 text-muted-foreground" />
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                Parallel because the isolation unit is the{" "}
                <strong className="font-medium text-foreground">branch</strong>: one worktree, one
                container, one agent. Two agents in one tree is a data race with a filesystem in the
                middle; three agents in three trees are simply three runs.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                The gate asks <strong className="font-medium text-foreground">git</strong> whether
                anything changed, rather than the agent. An agent reporting success having written
                nothing is the commonest thing this catches — and the one it cannot be told about.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                The verification runs <strong className="font-medium text-foreground">in the sandbox</strong>.
                On the host it would be host code selected by files the agent had just written.
              </p>
            </div>
          </div>
        </Section>

        {/* -------------------------------------------------------- handoff */}
        <Section id="handoff">
          <SectionHead
            eyebrow="agents that need each other"
            title="Passing work between agents"
            lead={
              <>
                Two specialists research in parallel and a coordinator combines what they produced.
                The interesting part is not the fan-out but the gap in the middle: each agent has its
                own worktree, so the coordinator <strong className="font-medium text-foreground">cannot see</strong>{" "}
                what the others wrote. Artifacts cross through the host, deliberately. It is{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  examples/travel-planner.ts
                </code>
                .
              </>
            }
          />

          <CodeBlock code={SDK_HANDOFF} lang="ts" title="examples/travel-planner.ts" />

          <div className="mt-4 grid grid-cols-1 gap-3 md:grid-cols-3">
            <div className="rounded-2xl border bg-card p-5">
              <ShieldCheck className="mb-3 size-4 text-muted-foreground" />
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                Files cross <strong className="font-medium text-foreground">base64-encoded</strong>, not
                through a heredoc. An artifact written by an agent is attacker-controlled, and an
                interpolated heredoc is one <code className="font-mono text-[0.85em]">EOF</code> line
                away from being the next command.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                Reads are base64 too, because{" "}
                <code className="font-mono text-[0.85em]">stdout</code> is the run&apos;s log{" "}
                <em>lines</em> joined — a file&apos;s trailing newline cannot survive{" "}
                <code className="font-mono text-[0.85em]">cat</code>. Measured: 64 bytes back for 65
                written.
              </p>
            </div>
            <div className="rounded-2xl border bg-card p-5">
              <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                A specialist that produced nothing is <strong className="font-medium text-foreground">named as missing</strong>,
                not quietly skipped. Telling the coordinator to assume the file exists is the natural
                thing to write, and it fails silently — the agent invents plausible inputs and the
                report reads exactly like a real one.
              </p>
            </div>
          </div>
        </Section>

        {/* ----------------------------------------------------------- rules */}
        <Section id="rules" tinted>
          <SectionHead
            eyebrow="what it promises"
            title="Seven claims you can check"
            lead="Each of these is a decision with a reason, and most of them are enforced by a test rather than by intent."
          />

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {SDK_RULES.map((rule) => (
              <div key={rule.title} className="rounded-2xl border bg-card p-5">
                <h3 className="mb-2 text-[0.9rem] font-medium">{rule.title}</h3>
                <p className="text-[0.85rem] leading-relaxed text-muted-foreground">{rule.body}</p>
              </div>
            ))}
          </div>
        </Section>

        {/* ---------------------------------------------------------- errors */}
        <Section id="errors">
          <SectionHead
            eyebrow="when it fails"
            title="Five failures, five different next steps"
            lead="Collapsing these into one “request failed” is what sends a reader to the network when the answer was a timeout on their own side."
          />

          <div className="overflow-x-auto rounded-2xl border bg-card">
            <table className="w-full text-left text-[0.85rem]">
              <thead className="border-b bg-muted/40">
                <tr>
                  <th className="px-5 py-3 font-medium">Error</th>
                  <th className="px-5 py-3 font-medium">What it means</th>
                </tr>
              </thead>
              <tbody>
                {SDK_ERRORS.map((err) => (
                  <tr key={err.name} className="border-b last:border-0">
                    <td className="px-5 py-3 font-mono text-[0.8rem]">{err.name}</td>
                    <td className="px-5 py-3 text-muted-foreground">{err.meaning}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          <div className="mt-6 flex flex-wrap gap-3">
            <a
              href={`${REPO_URL}/tree/main/sdk/typescript`}
              className={cn(buttonVariants({ variant: "default", size: "sm" }), "gap-1.5")}
            >
              <Boxes className="size-3.5" />
              The package
              <ArrowUpRight className="size-3.5" />
            </a>
            <Link
              href={STUDIO_PATH}
              className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
            >
              Studio, which runs the daemon this talks to
            </Link>
          </div>
        </Section>
      </main>

      <SiteFooter />
    </div>
  );
}
