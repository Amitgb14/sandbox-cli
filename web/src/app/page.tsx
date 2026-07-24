import { Check, X } from "lucide-react";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Hero, INSTALL_CMD } from "@/components/hero";
import { BlastRadius } from "@/components/blast-radius";
import { FeaturesBento } from "@/components/features-bento";
import { UsageTabs } from "@/components/usage-tabs";
import { DryRunBuilder } from "@/components/dry-run-builder";
import { ComparisonTable } from "@/components/comparison-table";
import { AttackSurfaceChart } from "@/components/attack-surface-chart";
import { AgentExplorer } from "@/components/agent-explorer";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const EXPOSED_POINTS = [
  "Reads ~/.ssh, ~/.aws, tokens, browser cookies",
  "One wrong path and the blast radius is your whole disk",
  "A poisoned README becomes arbitrary local execution",
  "Trust is enforced by asking the model nicely",
];

const CONTAINED_POINTS = [
  "Those paths were never mounted — nothing to read",
  "Blast radius is /workspace: the repo it was editing anyway",
  "Injection lands in a container that dies on exit",
  "Trust is enforced by the kernel, not by the prompt",
];

function SectionHead({
  eyebrow,
  title,
  lead,
  center,
}: {
  eyebrow: string;
  title: React.ReactNode;
  lead?: React.ReactNode;
  center?: boolean;
}) {
  return (
    <div
      className={cn(
        "mb-12 flex max-w-3xl flex-col gap-3",
        center && "mx-auto items-center text-center",
      )}
    >
      <span className="eyebrow">
        <span className="h-px w-6 bg-current opacity-60" />
        {eyebrow}
      </span>
      <h2 className="font-heading text-3xl font-bold sm:text-4xl lg:text-[2.7rem]">{title}</h2>
      {lead && <p className="text-lg text-muted-foreground">{lead}</p>}
    </div>
  );
}

export default function Home() {
  return (
    <>
      <SiteHeader />

      <main className="flex-1">
        <Hero />

        {/* ---------------------------------------------------------- threat */}
        <section id="threat" className="mx-auto w-full max-w-6xl px-6 py-24">
          <SectionHead
            center
            eyebrow="The trade nobody should have to make"
            title="“Allow All” is the useful mode. It's also the dangerous one."
            lead="Agents earn their keep when they stop asking permission for every edit. But the same flag that unblocks the work hands a non-deterministic process your entire home directory — and prompt injection turns someone else's text into your shell."
          />

          <div className="grid gap-4 md:grid-cols-2">
            <div className="flex flex-col gap-4 rounded-xl border border-exposed/40 bg-card p-6">
              <Badge variant="outline" className="w-fit border-exposed/40 text-exposed">
                Agent on the bare host
              </Badge>
              <ul className="flex flex-col gap-2.5 text-sm text-muted-foreground">
                {EXPOSED_POINTS.map((p) => (
                  <li key={p} className="flex gap-2.5">
                    <X className="mt-0.5 size-4 shrink-0 text-exposed" strokeWidth={2.4} />
                    {p}
                  </li>
                ))}
              </ul>
            </div>

            <div className="flex flex-col gap-4 rounded-xl border border-contained/45 bg-card p-6">
              <Badge variant="outline" className="w-fit border-contained/45 text-contained">
                Agent inside sandbox-cli
              </Badge>
              <ul className="flex flex-col gap-2.5 text-sm text-muted-foreground">
                {CONTAINED_POINTS.map((p) => (
                  <li key={p} className="flex gap-2.5">
                    <Check className="mt-0.5 size-4 shrink-0 text-contained" strokeWidth={2.4} />
                    {p}
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <BlastRadius />
        </section>

        {/* -------------------------------------------------------- features */}
        <section id="features" className="mx-auto w-full max-w-6xl px-6 py-24">
          <SectionHead
            eyebrow="What holds the line"
            title="Guarantees you can read the source of"
            lead={
              <>
                Every isolation decision funnels through one pure function that turns config into a{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">docker</code>{" "}
                argv. It is unit-tested against a golden output, so the boundary cannot drift silently.
              </>
            }
          />
          <FeaturesBento />
        </section>

        {/* ----------------------------------------------------------- usage */}
        <section id="usage" className="mx-auto w-full max-w-6xl px-6 py-24">
          <SectionHead
            eyebrow="Get started"
            title="Install once. Prefix your agent. Done."
            lead="Needs Docker (Docker Desktop on macOS). Go 1.25+ only if you build from source."
          />
          <UsageTabs />
          <p className="mt-5 text-center text-sm text-muted-foreground">
            Per-agent prerequisites, browserless login, and required{" "}
            <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">--allow</code>{" "}
            domains live in the{" "}
            <a
              href="https://github.com/Aegmis/sandbox-cli/blob/main/docs/AGENTS.md"
              target="_blank"
              rel="noopener noreferrer"
              className="font-medium text-contained hover:underline"
            >
              agent reference
            </a>
            .
          </p>
        </section>

        {/* --------------------------------------------------------- builder */}
        <section id="builder" className="mx-auto w-full max-w-6xl px-6 py-24">
          <SectionHead
            eyebrow="Nothing up our sleeve"
            title="Build the command. Read the argv."
            lead={
              <>
                Every flag resolves into a plain{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">docker</code>{" "}
                invocation, and{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">--dry-run</code>{" "}
                prints it before anything executes. Toggle the options and watch the boundary widen or
                tighten in real time.
              </>
            }
          />
          <DryRunBuilder />
        </section>

        {/* --------------------------------------------------------- compare */}
        <section id="compare" className="mx-auto w-full max-w-6xl px-6 py-24">
          <SectionHead
            eyebrow="Spec sheet"
            title="Container-cheap, VM-shaped isolation"
            lead="The highlighted column is what sits inside the boundary."
          />
          <div className="flex flex-col gap-6">
            <ComparisonTable />
            <AttackSurfaceChart />
          </div>
          <p className="mt-4 text-xs text-muted-foreground">
            Dev Container behaviour varies by configuration; entries describe common defaults.
          </p>
        </section>

        {/* ---------------------------------------------------------- agents */}
        <section id="agents" className="mx-auto w-full max-w-6xl px-6 py-24">
          <SectionHead
            eyebrow="Adapters"
            title="Fifteen agents, one prefix"
            lead="Each wrapper forwards your flags verbatim and keeps its login in its own sandbox-owned home. Pick one to see how it is installed and what it is handed."
          />
          <AgentExplorer />
        </section>

        {/* ------------------------------------------------------------- cta */}
        <section className="mx-auto w-full max-w-6xl px-6 pb-12">
          <div className="relative flex flex-col items-center gap-5 overflow-hidden rounded-2xl border bg-card px-8 py-14 text-center">
            <div
              aria-hidden="true"
              className="pointer-events-none absolute inset-0"
              style={{
                background:
                  "radial-gradient(70% 130% at 50% -10%, color-mix(in oklch, var(--contained) 15%, transparent), transparent 68%)",
              }}
            />
            <Badge variant="secondary" className="relative font-mono text-[0.68rem]">
              Open source · Go · MIT
            </Badge>
            <h2 className="relative font-heading text-3xl font-bold sm:text-4xl">
              Stop choosing between a useful agent
              <br className="hidden sm:block" /> and a safe machine.
            </h2>
            <p className="relative max-w-2xl text-muted-foreground">
              Install once, prefix your agent, and let it run flat out. The wall does the worrying.
            </p>

            <div className="relative flex w-fit max-w-full items-center gap-2 rounded-lg border bg-background p-1.5 pl-3">
              <span className="font-mono font-semibold text-contained" aria-hidden="true">
                $
              </span>
              <code className="no-scrollbar overflow-x-auto whitespace-nowrap font-mono text-[0.8rem]">
                {INSTALL_CMD}
              </code>
              <CopyButton value={INSTALL_CMD} label="Copy" />
            </div>

            <div className="relative flex flex-wrap justify-center gap-2.5">
              <a
                href="https://github.com/Aegmis/sandbox-cli"
                target="_blank"
                rel="noopener noreferrer"
                className={cn(
                  buttonVariants({ size: "lg" }),
                  "bg-contained text-background hover:bg-contained/90",
                )}
              >
                Star on GitHub
              </a>
              <a
                href="https://github.com/Aegmis/sandbox-cli/blob/main/docs/GUIDE.md"
                target="_blank"
                rel="noopener noreferrer"
                className={buttonVariants({ variant: "outline", size: "lg" })}
              >
                Read the guide
              </a>
            </div>
          </div>
        </section>
      </main>

      <SiteFooter />
    </>
  );
}
