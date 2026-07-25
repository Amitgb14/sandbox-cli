import { ArrowUpRight, Check, FileCode2, Lock, ShieldCheck, X } from "lucide-react";
import { GithubMark } from "@/components/logo";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Hero } from "@/components/hero";
import { Section, SectionHead } from "@/components/section-head";
import { BlastRadius } from "@/components/blast-radius";
import { FeaturesGrid } from "@/components/features-grid";
import { DryRunBuilder } from "@/components/dry-run-builder";
import { EgressVisualizer } from "@/components/egress-visualizer";
import { ParallelAgents } from "@/components/parallel-agents";
import { LiveGauge } from "@/components/live-gauge";
import { AgentExplorer } from "@/components/agent-explorer";
import { ComparisonTable } from "@/components/comparison-table";
import { CapabilityChart } from "@/components/capability-chart";
import { PlatformTable } from "@/components/platform-table";
import { InstallCard } from "@/components/install-card";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { AGENTS, BAKED_COUNT } from "@/lib/agents";
import { HISTORY_NOTE } from "@/lib/features";
import { DOC_URL, REPO_URL, VERSION } from "@/lib/site";
import { cn } from "@/lib/utils";

const EXPOSED = [
  "Reads ~/.ssh, ~/.aws, cloud tokens, browser cookies",
  "One hallucinated path and the blast radius is your whole disk",
  "A poisoned README turns into local execution",
  "The only thing enforcing trust is the prompt",
];

const CONTAINED = [
  "Those paths were never mounted — there is nothing to read",
  "The blast radius is /workspace: the repo it was editing anyway",
  "Injection lands in a container that dies on exit",
  "The kernel enforces trust, not a paragraph of instructions",
];

const INVARIANTS = [
  {
    icon: FileCode2,
    title: "One function decides everything",
    body: "runtime.BuildArgs is pure and deterministic: config in, docker argv out. It is the single choke point for what the container can reach, and it is exhaustively unit-tested against a golden output so the boundary cannot drift silently.",
  },
  {
    icon: Lock,
    title: "Three refusals no flag can override",
    body: "Never mount /, never mount your home directory, never mount an ancestor of it. ResolveWorkspace enforces those before anything else runs, and there is no configuration key that turns them off.",
  },
  {
    icon: ShieldCheck,
    title: "Nothing crosses that you did not name",
    body: "Host environment variables are default-deny. Each agent wrapper ships a small suggested allowlist applied only if the value is set, and everything else needs an explicit --env or --mount.",
  },
];

export default function Home() {
  return (
    <div id="top">
      <SiteHeader />

      <main className="flex-1">
        <Hero />

        {/* ------------------------------------------------------------ why */}
        <Section id="threat">
          <SectionHead
            center
            eyebrow="the trade nobody should have to make"
            title={
              <>
                &ldquo;Allow All&rdquo; is the mode that makes agents useful.
                <br className="hidden sm:block" /> It is also the one that scares you.
              </>
            }
            lead="Agents earn their keep the moment they stop asking permission for every edit. But the same flag that unblocks the work hands a non-deterministic process your entire home directory — and prompt injection turns text somebody else wrote into commands your shell runs."
          />

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div className="flex flex-col gap-4 rounded-2xl border border-exposed-line bg-exposed-soft/40 p-5">
              <Badge variant="outline" className="w-fit border-exposed/30 bg-card text-exposed">
                Agent on the bare host
              </Badge>
              <ul className="flex flex-col gap-2.5 text-sm text-muted-foreground">
                {EXPOSED.map((p) => (
                  <li key={p} className="flex gap-2.5">
                    <X className="mt-0.5 size-4 shrink-0 text-exposed" strokeWidth={2.4} />
                    {p}
                  </li>
                ))}
              </ul>
            </div>

            <div className="flex flex-col gap-4 rounded-2xl border border-contained-line bg-contained-soft/50 p-5">
              <Badge variant="outline" className="w-fit border-contained/30 bg-card text-contained">
                Agent inside sandbox-cli
              </Badge>
              <ul className="flex flex-col gap-2.5 text-sm text-muted-foreground">
                {CONTAINED.map((p) => (
                  <li key={p} className="flex gap-2.5">
                    <Check className="mt-0.5 size-4 shrink-0 text-contained" strokeWidth={2.4} />
                    {p}
                  </li>
                ))}
              </ul>
            </div>
          </div>

          <div className="mt-3">
            <BlastRadius />
          </div>

          <div className="mt-3 grid grid-cols-1 gap-3 md:grid-cols-3">
            {INVARIANTS.map((i) => (
              <div key={i.title} className="flex flex-col gap-2.5 rounded-2xl border bg-card p-5">
                <i.icon className="size-4 text-muted-foreground" />
                <h3 className="text-[0.95rem] font-semibold tracking-tight">{i.title}</h3>
                <p className="text-[0.82rem] leading-relaxed text-muted-foreground">{i.body}</p>
              </div>
            ))}
          </div>
        </Section>

        {/* ------------------------------------------------------- features */}
        <Section id="features" tinted>
          <SectionHead
            eyebrow="what you actually get"
            title="Twenty-three capabilities, one prefix"
            lead={
              <>
                Everything below ships today and is reachable from a flag or a{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  .sandbox.yaml
                </code>{" "}
                key. Filter by the question you came with.
              </>
            }
          />
          <FeaturesGrid />
        </Section>

        {/* -------------------------------------------------- the command */}
        <Section id="command">
          <SectionHead
            eyebrow="nothing up our sleeve"
            title="Build the command. Read the argv. Then decide."
            lead={
              <>
                Every flag resolves into a plain{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">docker</code>{" "}
                invocation, and{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  --dry-run
                </code>{" "}
                prints it before anything executes. Toggle real options and watch the boundary widen
                or tighten — the arrow beside each flag tells you which way it moves.
              </>
            }
          />
          <DryRunBuilder />
        </Section>

        {/* --------------------------------------------------------- egress */}
        <Section id="network" tinted>
          <SectionHead
            eyebrow="the network half of the problem"
            title="A firewall that stops exfiltration without stopping npm install"
            lead="Filesystem isolation does nothing about an agent that has been talked into POSTing your .env somewhere. Flip the allowlist and watch which requests still leave."
          />
          <EgressVisualizer />
        </Section>

        {/* -------------------------------------------------------- workflow */}
        <Section id="parallel">
          <SectionHead
            eyebrow="what containment buys you"
            title="Three agents, three branches, one repo, zero collisions"
            lead={
              <>
                Isolation stops being a tax the moment it lets you do something you could not do
                before.{" "}
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  --worktree
                </code>{" "}
                runs each agent in a real git worktree for its own branch, in its own container, so
                you can start three and go and do something else.
              </>
            }
          />
          <ParallelAgents />

          <div className="mt-14">
            <SectionHead
              eyebrow="observability"
              title="You can see what it is doing"
              lead="A sandbox you cannot watch is a sandbox you will not trust. sandbox-cli measures — it never throttles — and reports in three places."
            />
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]">
              <div className="flex flex-col gap-3 rounded-2xl border bg-card p-5">
                <h3 className="text-[0.95rem] font-semibold tracking-tight">
                  Why only Claude gets a status line
                </h3>
                <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                  Claude Code has a{" "}
                  <code className="font-mono text-[0.9em]">statusLine</code> hook, so the gauge lives
                  in its own UI, injected through a managed-settings file that never touches your
                  Claude settings. Neither Gemini CLI nor OpenCode has such a hook. Running them
                  inside tmux to fake one was tried and reverted — it made their TUIs render badly,
                  which is a bad trade for a gauge.
                </p>
                <p className="text-[0.85rem] leading-relaxed text-muted-foreground">
                  For every other agent the answer is{" "}
                  <code className="font-mono text-[0.9em]">sandbox-cli stats</code> in a second
                  terminal, plus the peak-usage line every run prints when it exits.
                </p>
                <div className="mt-auto rounded-lg border bg-surface px-3 py-2.5">
                  <p className="eyebrow mb-1.5">shared history</p>
                  <code className="block text-[0.68rem] break-all text-muted-foreground">
                    {HISTORY_NOTE.mount}
                  </code>
                  <p className="mt-2 text-[0.8rem] leading-relaxed text-muted-foreground">
                    {HISTORY_NOTE.body}
                  </p>
                </div>
              </div>
              <LiveGauge />
            </div>
          </div>
        </Section>

        {/* --------------------------------------------------------- agents */}
        <Section id="agents" tinted>
          <SectionHead
            eyebrow="adapters"
            title={`${AGENTS.length} agents, one prefix, your flags forwarded verbatim`}
            lead={
              <>
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  sandbox-cli claude --dangerously-skip-permissions
                </code>{" "}
                just works: a leading run of sandbox flags is consumed by sandbox, and the first
                token that is not one ends it — everything after goes to the agent untouched.{" "}
                {BAKED_COUNT} agents are baked into the base image; the rest install themselves into
                their own persisted home on first use, so you only download what you actually run.
              </>
            }
          />
          <AgentExplorer />
        </Section>

        {/* -------------------------------------------------------- compare */}
        <Section id="compare">
          <SectionHead
            eyebrow="prior art, honestly"
            title="Where this sits, including where it loses"
            lead="Running an agent in a disposable container is a crowded space. sandbox-cli's edge is code quality, ergonomics and a focused feature set — not a hard security boundary. If you need one of those, the table says so."
          />
          <div className="flex flex-col gap-3">
            <ComparisonTable />
            <CapabilityChart />
          </div>

          <div className="mt-14">
            <SectionHead
              eyebrow="platform support"
              title="Everywhere Docker runs"
              lead="Almost everything works identically across platforms; the differences are all about the boundary the host can provide."
            />
            <PlatformTable />
          </div>
        </Section>

        {/* -------------------------------------------------------- install */}
        <Section id="install" tinted>
          <div className="grid grid-cols-1 items-start gap-10 lg:grid-cols-[1fr_minmax(0,30rem)]">
            <div>
              <SectionHead
                className="mb-6"
                eyebrow="get started"
                title="Install once. Prefix your agent. Done."
                lead="Needs Docker — Docker Desktop on macOS and Windows. Go 1.25+ only if you build from source. The first run builds the base image; every run after that starts immediately."
              />
              <div className="flex flex-wrap items-center gap-2.5">
                <a
                  href={REPO_URL}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={cn(buttonVariants({ size: "lg" }), "gap-1.5 px-4")}
                >
                  <GithubMark className="size-4" />
                  Star on GitHub
                </a>
                <a
                  href={DOC_URL.guide}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={cn(
                    buttonVariants({ variant: "outline", size: "lg" }),
                    "gap-1.5 px-4",
                  )}
                >
                  Read the guide
                  <ArrowUpRight className="size-4" />
                </a>
              </div>
              <p className="mt-6 max-w-xl text-sm leading-relaxed text-muted-foreground">
                Uninstalling is symmetrical and cautious:{" "}
                <code className="font-mono text-[0.9em]">--uninstall</code> removes the binary and
                then <em>reports</em> what else is on disk without deleting it — because{" "}
                <code className="font-mono text-[0.9em]">~/.config/sandbox</code> holds your agent
                logins, and silently deleting it would sign you out of everything with no warning.
                Add <code className="font-mono text-[0.9em]">--purge</code> when you mean it.
              </p>
            </div>

            <div className="flex flex-col gap-3">
              <InstallCard />
              <p className="text-xs text-muted-foreground">
                v{VERSION} · verified against the release{" "}
                <code className="font-mono">checksums.txt</code> · installs to{" "}
                <code className="font-mono">~/.local/bin</code> · no root, no package manager.
              </p>
            </div>
          </div>
        </Section>
      </main>

      <SiteFooter />
    </div>
  );
}
