import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowUpRight, Boxes, FileCode, ShieldCheck } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { type NavEntry } from "@/lib/nav";
import { REPO_URL, STUDIO_PATH } from "@/lib/site";
import { SDK_ERRORS, SDK_EXAMPLE, SDK_PREREQS, SDK_RULES, SDK_STEPS } from "@/lib/sdk";
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

const NAV: NavEntry[] = [
  { kind: "link", href: "#what", label: "What it is" },
  { kind: "link", href: "#before", label: "Before it works" },
  { kind: "link", href: "#use", label: "Using it" },
  { kind: "link", href: "#example", label: "A whole script" },
  { kind: "link", href: "#rules", label: "What it promises" },
  { kind: "link", href: "#errors", label: "When it fails" },
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
            title="Two steps, and only the second is this package"
            lead={
              <>
                The client talks to a daemon; it does not start one, and it cannot. Without the
                first,{" "}
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
                <CodeBlock code={step.code} />
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
                {step.code && <CodeBlock code={step.code} />}
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

        {/* --------------------------------------------------------- example */}
        <Section id="example" tinted>
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

          <CodeBlock code={SDK_EXAMPLE} lang="sh" />

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

        {/* ----------------------------------------------------------- rules */}
        <Section id="rules">
          <SectionHead
            eyebrow="what it promises"
            title="Six claims you can check"
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
        <Section id="errors" tinted>
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
