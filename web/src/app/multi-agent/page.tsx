import type { Metadata } from "next";
import Link from "next/link";
import { ArrowLeft, ArrowUpRight, Check, GitMerge, ShieldCheck, X } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Section, SectionHead } from "@/components/section-head";
import { CodeBlock } from "@/components/code-block";
import { ParallelAgents } from "@/components/parallel-agents";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  FLEET_AGENTS,
  FLEET_YAML,
  GUARDRAILS,
  LAND_REFUSALS,
  LOOP,
  MIXED_YAML,
  RECOVERY,
  RUNGS,
  SHARE_RULES,
  SHARE_YAML,
  UNSUPPORTED_AGENT_COUNT,
} from "@/lib/fleet";
import { DOC_URL, REPO_URL } from "@/lib/site";
import { cn } from "@/lib/utils";

const TITLE = "Running agents in parallel — sandbox-cli";
const DESCRIPTION =
  "One agent per branch, each in its own git worktree and its own container. Mix Claude, Codex, Gemini, OpenCode and Droid across tasks in one fleet file, check the work with verify, and land what passed.";

export const metadata: Metadata = {
  title: TITLE,
  description: DESCRIPTION,
  openGraph: { title: TITLE, description: DESCRIPTION, type: "article" },
  twitter: { card: "summary_large_image", title: TITLE, description: DESCRIPTION },
};

/** This page's own sections, for the shared header and footer. */
const NAV = [
  { href: "#ladder", label: "The ladder" },
  { href: "#quickstart", label: "Quick start" },
  { href: "#mixing", label: "Mixing agents" },
  { href: "#verify", label: "verify" },
  { href: "#land", label: "Landing" },
  { href: "#share", label: "Handing files" },
  { href: "#guardrails", label: "Guardrails" },
];

export default function MultiAgentPage() {
  return (
    <>
      <SiteHeader nav={NAV} homeHref="/" installHref="/#install" />

      <main id="top" className="flex flex-col">
        {/* ---------------------------------------------------------------- */}
        <Section className="pt-10 lg:pt-14">
          <Link
            href="/"
            className="mb-8 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <ArrowLeft className="size-3.5" />
            sandbox-cli
          </Link>

          <div className="flex max-w-3xl flex-col gap-4">
            <span className="eyebrow">
              <span className="h-px w-6 bg-current opacity-50" />
              multi-agent
            </span>
            <h1 className="text-[2.1rem] leading-[1.1] font-semibold tracking-[-0.03em] text-balance sm:text-[2.7rem]">
              Run several agents at once, and know which of them worked
            </h1>
            <p className="text-[1.05rem] leading-relaxed text-pretty text-muted-foreground">
              The unit never changes: <strong className="font-medium text-foreground">one agent,
              one branch, one worktree, one container</strong>. Everything on this page adds
              something to that unit — a background container, a file describing several of them, a
              check that decides whether the work is done. None of it changes the boundary, because
              a fleet task becomes exactly the options a single <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">--worktree</code> run
              produces.
            </p>
            <div className="mt-2 flex flex-wrap items-center gap-2.5">
              <a href="#quickstart" className={cn(buttonVariants({ size: "sm" }))}>
                Start here
              </a>
              <a
                href={DOC_URL.guide}
                target="_blank"
                rel="noopener noreferrer"
                className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
              >
                Full guide
                <ArrowUpRight className="size-3.5" />
              </a>
            </div>
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="ladder" tinted>
          <SectionHead
            eyebrow="four rungs, one ladder"
            title="You can stop at any rung"
            lead="These are not four features to choose between. Each one is the previous one plus a single addition, and most days the first is enough."
          />
          <ol className="grid grid-cols-1 gap-4 md:grid-cols-2">
            {RUNGS.map((r, i) => (
              <li
                key={r.id}
                className="flex flex-col gap-2.5 rounded-xl border bg-card p-5"
              >
                <div className="flex items-center gap-2.5">
                  <span className="flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-[0.7rem] text-muted-foreground">
                    {i + 1}
                  </span>
                  <h3 className="text-[0.98rem] font-medium">{r.label}</h3>
                </div>
                <code className="w-fit rounded-md bg-muted px-2 py-1 font-mono text-[0.72rem]">
                  {r.flag}
                </code>
                <p className="text-sm leading-relaxed text-muted-foreground">{r.adds}</p>
                <p className="mt-auto pt-1 text-xs leading-relaxed text-muted-foreground">
                  <span className="font-medium text-foreground">Enough when:</span> {r.enough}
                </p>
              </li>
            ))}
          </ol>

          <div className="mt-10">
            <ParallelAgents />
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="quickstart">
          <SectionHead
            eyebrow="quick start"
            title="A fleet is one file and one command"
            lead={
              <>
                Write a <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">fleet.yaml</code>{" "}
                next to your project. Every task gets its own branch, its own worktree and its own
                detached container; your checkout is never touched and never changes branch.
              </>
            }
          />

          <CodeBlock code={FLEET_YAML} lang="yaml" />

          <p className="mt-4 text-sm leading-relaxed text-muted-foreground">
            Then the whole cycle, all of it from your normal checkout.{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">fleet run</code>{" "}
            looks for <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">fleet.yaml</code>{" "}
            in the current directory; <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">-f path</code>{" "}
            names another.
          </p>

          <ol className="mt-8 flex flex-col gap-0 divide-y rounded-xl border bg-card">
            {LOOP.map((s, i) => (
              <li key={s.cmd} className="flex flex-col gap-2.5 p-5 sm:flex-row sm:gap-5">
                <span className="flex size-6 shrink-0 items-center justify-center rounded-full border font-mono text-[0.7rem] text-muted-foreground">
                  {i + 1}
                </span>
                <div className="flex min-w-0 flex-1 flex-col gap-2">
                  <CodeBlock code={s.cmd} />
                  <p className="text-sm leading-relaxed text-muted-foreground">{s.what}</p>
                  {s.also ? (
                    <p className="font-mono text-xs text-muted-foreground">{s.also}</p>
                  ) : null}
                </div>
              </li>
            ))}
          </ol>

          <div className="mt-10">
            <h3 className="mb-3 text-[1.05rem] font-medium">When part of it goes wrong</h3>
            <p className="mb-5 max-w-2xl text-sm leading-relaxed text-muted-foreground">
              You do not re-run the file. Commenting the other tasks out is the thing people reach
              for, and a fleet file with half its tasks commented out is one that will be run that
              way again by mistake.
            </p>
            <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
              {RECOVERY.map((r) => (
                <div key={r.cmd} className="flex flex-col gap-2.5 rounded-xl border bg-card p-5">
                  <CodeBlock code={r.cmd} />
                  <p className="text-sm leading-relaxed text-muted-foreground">{r.what}</p>
                </div>
              ))}
            </div>
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="mixing" tinted>
          <SectionHead
            eyebrow="mixing agents"
            title="Claude on one branch, Codex on another"
            lead={
              <>
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">agent:</code>{" "}
                at the top of the file is the default. A task that names its own overrides it — so
                you can put two agents on the same problem and compare what comes back, or use
                whichever is better at each job.
              </>
            }
          />

          <CodeBlock code={MIXED_YAML} lang="yaml" />

          <p className="mt-4 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            The fleet-wide{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">agent:</code>{" "}
            becomes optional once every task names one. Mixing costs nothing at the boundary — each
            agent gets exactly the container it would get on its own. What it does cost is setup:{" "}
            <strong className="font-medium text-foreground">
              every agent you name needs its own login before the run
            </strong>
            , because none of them can answer a login prompt from a detached container.{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
              sandbox-cli fleet run --dry-run
            </code>{" "}
            prints a reminder when it sees a mixed file.
          </p>

          <h3 className="mt-10 mb-2 text-[1.05rem] font-medium">Which agents are eligible</h3>
          <p className="mb-5 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            A fleet starts every agent detached, so an agent may only appear in a file if it has a{" "}
            <strong className="font-medium text-foreground">verified headless mode</strong> — a way
            to run a prompt to completion without ever asking a human anything. An agent that stops
            for approval in a fleet does not fail; it hangs until you notice, holding a slot.
          </p>

          <div className="overflow-hidden rounded-xl border bg-card">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[7.5rem]">agent:</TableHead>
                  <TableHead>What the fleet runs</TableHead>
                  <TableHead className="w-[9rem]">Delivery</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {FLEET_AGENTS.map((a) => (
                  <TableRow key={a.name}>
                    <TableCell className="align-top font-mono text-[0.8rem] font-medium">
                      {a.name}
                    </TableCell>
                    <TableCell className="align-top">
                      <code className="font-mono text-[0.75rem] break-all">{a.argv}</code>
                      {a.note ? (
                        <p className="mt-1.5 text-xs leading-relaxed text-muted-foreground">
                          {a.note}
                        </p>
                      ) : null}
                    </TableCell>
                    <TableCell className="align-top">
                      <Badge variant="outline" className="font-mono text-[0.65rem] whitespace-nowrap">
                        {a.delivery === "baked" ? "in the image" : "on first use"}
                      </Badge>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <p className="mt-4 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Anything else is rejected when the file is parsed, before a single container starts. The
            other {UNSUPPORTED_AGENT_COUNT} adapters are perfectly usable interactively — they are
            simply not ones we have confirmed will never stop and wait. Adding one to this list
            means running it and recording the argv, which a test pins, so it cannot grow by
            guesswork.
          </p>

          <h3 className="mt-10 mb-2 text-[1.05rem] font-medium">Per-task limits</h3>
          <p className="max-w-3xl text-sm leading-relaxed text-muted-foreground">
            A task may also raise its own{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">memory</code>,{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">cpus</code> and{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">allow</code>, for
            the one branch that needs a bigger build or one more domain. The two rules are not
            symmetric, on purpose: <strong className="font-medium text-foreground">memory and cpus
            replace</strong> the fleet-wide value, and <strong className="font-medium text-foreground">
            allow adds to it</strong>. A task that could subtract from the allowlist would be asking
            for less egress than the file&apos;s author wrote a line above it; the way to want that
            is to move the domain onto the tasks that need it.
          </p>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="verify">
          <SectionHead
            eyebrow="the point of the whole thing"
            title={
              <>
                <code className="font-mono text-[0.86em]">verify:</code> is what makes a run
                autonomous rather than merely unattended
              </>
            }
            lead="Without it, a fleet is a fan-out with a nicer status table: every agent succeeds the moment it stops talking — including the one that confidently did nothing, and the one that deleted the failing test."
          />

          <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
            <div className="flex flex-col gap-3 rounded-xl border bg-card p-5">
              <h3 className="text-[0.98rem] font-medium">How it runs</h3>
              <p className="text-sm leading-relaxed text-muted-foreground">
                The command runs <em>inside the container, after the agent</em>, and its exit code
                becomes the container&apos;s. Inside because a check running on your host would be
                host code selected by a file the agent can write. After the agent, whatever the
                agent&apos;s own exit code was — an agent that exits non-zero having left a tree
                that builds and tests clean has done the job, and one that exits 0 having deleted
                the test file has not. The task&apos;s definition of done gets the last word.
              </p>
            </div>
            <div className="flex flex-col gap-3 rounded-xl border bg-card p-5">
              <h3 className="text-[0.98rem] font-medium">What it cannot do</h3>
              <p className="text-sm leading-relaxed text-muted-foreground">
                The command is fixed, but its meaning is not:{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.8em]">
                  go test ./...
                </code>{" "}
                runs agent-written tests over an agent-written tree, so an agent that deletes the
                failing test passes. This makes forging a pass require editing the tests rather than
                merely claiming success — a real improvement over an exit code alone, and not the
                same thing as being unforgeable.
              </p>
            </div>
          </div>

          <p className="mt-6 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Exit <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">90</code>{" "}
            means the agent finished and its verify said no. A task with{" "}
            <em>no</em> verify still runs — this is a fleet of agents, not a CI system — but it
            lands reported as <strong className="font-medium text-foreground">unverified</strong>{" "}
            rather than passed, because nothing checked it.
          </p>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="land" tinted>
          <SectionHead
            eyebrow="landing the work"
            title="The only command that writes to your base branch"
            lead={
              <>
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  fleet land
                </code>{" "}
                commits whatever the agent left in its worktree, then merges the branch (
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">--no-ff</code>
                ) into the one you have checked out. It refuses rather than guessing, and{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">--all</code>{" "}
                sorts those refusals into two kinds.
              </>
            }
          />

          <div className="overflow-hidden rounded-xl border bg-card">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>It refuses when…</TableHead>
                  <TableHead className="w-[11rem]">Under --all</TableHead>
                  <TableHead>Why</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {LAND_REFUSALS.map((r) => {
                  const skips = r.scope === "skips this branch";
                  return (
                    <TableRow key={r.when}>
                      <TableCell className="align-top text-[0.85rem] font-medium">
                        {r.when}
                      </TableCell>
                      <TableCell className="align-top">
                        <span
                          className={cn(
                            "inline-flex items-center gap-1.5 text-xs whitespace-nowrap",
                            skips ? "text-caution" : "text-exposed",
                          )}
                        >
                          {skips ? (
                            <ArrowUpRight className="size-3.5 shrink-0" />
                          ) : (
                            <X className="size-3.5 shrink-0" />
                          )}
                          {r.scope}
                        </span>
                      </TableCell>
                      <TableCell className="align-top text-[0.85rem] leading-relaxed text-muted-foreground">
                        {r.why}
                      </TableCell>
                    </TableRow>
                  );
                })}
              </TableBody>
            </Table>
          </div>

          <div className="mt-6 flex max-w-3xl items-start gap-3 rounded-xl border bg-card p-5">
            <GitMerge className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
            <p className="text-sm leading-relaxed text-muted-foreground">
              <strong className="font-medium text-foreground">
                The split is the design, not a convenience.
              </strong>{" "}
              A problem with <em>the branch</em> skips it and the rest carry on. A problem with{" "}
              <em>the branch being merged into</em> stops there, because it will be just as wrong
              for the next branch and landing more on top of it makes it harder to undo. What
              already landed is printed either way — those merges are commits in your branch
              whatever happens next.
            </p>
          </div>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="share">
          <SectionHead
            eyebrow="handing files between agents"
            title="A convention, deliberately not a protocol"
            lead="Two sandboxes are blind to each other by design. When one agent produces something another needs — an API contract, a schema, a generated client — --share gives them one directory in common. Files in a shared directory, or nothing: there is no messaging protocol here and none is planned."
          />

          <CodeBlock code={SHARE_YAML} lang="yaml" />

          <div className="mt-6 grid grid-cols-1 gap-4 md:grid-cols-3">
            {SHARE_RULES.map((r) => (
              <div key={r.title} className="flex flex-col gap-2 rounded-xl border bg-card p-5">
                <div className="flex items-start gap-2">
                  <Check className="mt-0.5 size-4 shrink-0 text-contained" />
                  <h3 className="text-[0.95rem] font-medium">{r.title}</h3>
                </div>
                <p className="text-sm leading-relaxed text-muted-foreground">{r.body}</p>
              </div>
            ))}
          </div>

          <p className="mt-6 max-w-3xl text-sm leading-relaxed text-muted-foreground">
            Two agents that need to coordinate step by step are one task, not two.
          </p>
        </Section>

        {/* ---------------------------------------------------------------- */}
        <Section id="guardrails" tinted>
          <SectionHead
            eyebrow="guardrails"
            title="The parts that refuse before you find out the hard way"
            lead="Each of these exists because the failure it prevents is silent, expensive, or both."
          />
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2">
            {GUARDRAILS.map((g) => (
              <div key={g.title} className="flex flex-col gap-2 rounded-xl border bg-card p-5">
                <div className="flex items-start gap-2">
                  <ShieldCheck className="mt-0.5 size-4 shrink-0 text-contained" />
                  <h3 className="text-[0.95rem] font-medium">{g.title}</h3>
                </div>
                <p className="text-sm leading-relaxed text-muted-foreground">{g.body}</p>
              </div>
            ))}
          </div>

          <div className="mt-10 flex flex-wrap items-center gap-3">
            <a
              href={DOC_URL.guide}
              target="_blank"
              rel="noopener noreferrer"
              className={cn(buttonVariants({ size: "sm" }), "gap-1.5")}
            >
              Read the full guide
              <ArrowUpRight className="size-3.5" />
            </a>
            <a
              href={`${REPO_URL}/blob/main/docs/examples/fleet.yaml`}
              target="_blank"
              rel="noopener noreferrer"
              className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
            >
              Commented fleet.yaml
              <ArrowUpRight className="size-3.5" />
            </a>
            <a
              href={DOC_URL.agents}
              target="_blank"
              rel="noopener noreferrer"
              className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
            >
              Agent reference
              <ArrowUpRight className="size-3.5" />
            </a>
          </div>
        </Section>
      </main>

      <SiteFooter onThisPage={NAV.map((n) => ({ label: n.label, href: n.href }))} />
    </>
  );
}
