import Link from "next/link";
import { ArrowUpRight, Check, Cpu, Lock, ShieldCheck, X } from "lucide-react";
import { GithubMark } from "@/components/logo";
import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { Hero } from "@/components/hero";
import { Section, SectionHead } from "@/components/section-head";
import { BlastRadius } from "@/components/blast-radius";
import { ModesTable } from "@/components/modes-table";
import { ApiExamples } from "@/components/api-examples";
import { FeaturesGrid } from "@/components/features-grid";
import { EgressVisualizer } from "@/components/egress-visualizer";
import { SessionCommands } from "@/components/session-commands";
import { AgentExplorer } from "@/components/agent-explorer";
import { ComparisonTable } from "@/components/comparison-table";
import { PlatformTable } from "@/components/platform-table";
import { SetupGuide } from "@/components/setup-guide";
import { InstallCard } from "@/components/install-card";
import { UninstallSteps } from "@/components/uninstall-steps";
import { CodeBlock } from "@/components/code-block";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { AGENTS } from "@/lib/agents";
import { DOC_URL, MULTI_AGENT_PATH, REPO_URL, SETUP_PATH } from "@/lib/site";
import { cn } from "@/lib/utils";

const EXPOSED = [
  "Reads ~/.ssh, ~/.aws, cloud tokens, browser cookies",
  "One hallucinated path and the blast radius is your whole disk",
  "A poisoned README turns into local execution",
  "A container shares your kernel: one bug from the host",
];

const CONTAINED = [
  "Nothing of yours is mounted — there is nothing to read",
  "The repository is a clone; your checkout is never written",
  "Injection lands in a VM that is discarded with the sandbox",
  "Its own kernel, behind a hypervisor, not a namespace",
];

const INVARIANTS = [
  {
    icon: Lock,
    title: "Tighten, never loosen",
    body: "The server's policy is a ceiling every request is resolved against: a request may ask for less network, fewer resources, a narrower allowlist — never more. A repository's own .sandbox.yaml is untrusted and may only tighten your config.",
  },
  {
    icon: ShieldCheck,
    title: "The guest is hostile",
    body: "The host talks to one agent in the VM over a bounded protocol and never acts on what the guest volunteers. A bundle coming back is verified against your repository and must carry exactly one ref; host-side git runs with every hook and filter neutralised.",
  },
  {
    icon: Cpu,
    title: "Fail closed",
    body: "A control that was asked for and cannot be delivered refuses the run. A backend that cannot enforce an allowlist says so in its capabilities, and the request is refused — never served open, never quietly offline.",
  },
];

const RECOVER = `sandbox-cli recover
# SANDBOX               STARTED  SANDBOX STATE  CHECKPOINT  REPOSITORY
# sbx_579194ea61058897  4m ago   gone           1m ago      /home/you/src/app
#
# sbx_579194ea61058897: the sandbox is gone; its last checkpoint is refs/sandbox/checkpoints/sbx_579194ea61058897
#   review: git log -p HEAD..refs/sandbox/checkpoints/sbx_579194ea61058897 · merge: git merge …`;

const BRING_BACK = `sandbox-cli run -- sh -c 'make fix && git commit -qam fix; echo note > TODO'
# sandbox-cli: work brought back to refs/sandbox/sbx_cf20dd8c24c71989
#   review: git log -p HEAD..refs/sandbox/sbx_cf20dd8c24c71989 · merge: git merge refs/sandbox/sbx_cf20dd8c24c71989`;

const EVENTS = `sandbox-cli events sbx_cf20dd8c24c71989
# 2026-10-02 03:17:02  sandbox.created     image sandbox-base · network none · env SECRET_TOKEN · labels team=infra
# 2026-10-02 03:17:02  workspace.in        branch sandbox
# 2026-10-02 03:17:02  process.started     pid 1 · sh -c echo hi > note.txt; exit 4
# 2026-10-02 03:17:02  process.exited      pid 1 exit 4 after 0s
# 2026-10-02 03:17:02  workspace.out       ab74a8aab9aed487e0714e71d9dd3f8fe306985e..sandbox
# 2026-10-02 03:17:02  sandbox.terminated  request`;

const FLEET = `sandbox-cli agent claude --fallback codex -p "fix the flaky test"
sandbox-cli agent fleet run -f fleet.yaml   # one agent per branch, in parallel sandboxes
sandbox-cli agent fleet land --all          # merge only what its verify accepted`;

export default function Home() {
  return (
    <div id="top">
      <SiteHeader />

      <main className="flex-1">
        <Hero />

        {/* ------------------------------------------------------------ why */}
        <Section id="why">
          <SectionHead
            center
            eyebrow="the trade nobody should have to make"
            title={
              <>
                Autonomy is what makes agents useful.
                <br className="hidden sm:block" /> Your machine is what it puts at risk.
              </>
            }
            lead="An agent earns its keep the moment it stops asking permission for every edit — and the same flag hands a non-deterministic process your home directory, while prompt injection turns text somebody else wrote into commands your shell runs. The answer is not a better prompt. It is a machine of its own."
          />

          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <div className="flex flex-col gap-4 rounded-2xl border border-exposed-line bg-exposed-soft/40 p-5">
              <Badge variant="outline" className="w-fit border-exposed/30 bg-card text-exposed">
                Agent on your machine
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
                Agent in a sandbox
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

        {/* ---------------------------------------------------------- modes */}
        <Section id="modes" tinted>
          <SectionHead
            eyebrow="three ways to run it"
            title="Your Mac, your own Linux box, or the cloud"
            lead="sandboxd serves the API on each machine. The same request means the same thing on all three; what differs is what each machine can deliver, and the API says so rather than letting you find out."
          />
          <ModesTable />
        </Section>

        {/* ------------------------------------------------------------ api */}
        <Section id="api">
          <SectionHead
            eyebrow="one API"
            title="The CLI is one client. Your code can be another."
            lead={
              <>
                Make a sandbox, run something, read what it printed, throw it away. The CLI, curl, and
                the Python and TypeScript SDKs do it with the same calls — documented in{" "}
                <a
                  href={DOC_URL.api}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="underline underline-offset-4"
                >
                  docs/api/v1.md
                </a>
                , with files, background processes, a real terminal over attach, tunnels, snapshots,
                volumes and an event log on top.
              </>
            }
          />
          <ApiExamples />
        </Section>

        {/* ------------------------------------------------------- features */}
        <Section id="features" tinted>
          <SectionHead
            eyebrow="what you actually get"
            title="Everything it does, by the question you came with"
            lead="Each card names the flag or setting behind it and whether it is on by default. Nothing here is a plan: what is not built yet is said so in the modes and the comparison."
          />
          <FeaturesGrid />
        </Section>

        {/* -------------------------------------------------------- network */}
        <Section id="network">
          <SectionHead
            eyebrow="the network half of the problem"
            title="An allowlist of names, enforced where the agent cannot reach"
            lead="A sandbox can still read your repository, so the question is where that can go. The default policy permits the agent APIs and package registries and nothing else, checked by name on the host — so npm install works and a POST to somebody's webhook does not."
          />
          <EgressVisualizer />
        </Section>

        {/* ------------------------------------------------------ workspace */}
        <Section id="workspace" tinted>
          <SectionHead
            eyebrow="your repository"
            title="A git bundle in, verified commits out"
            lead="The sandbox gets a clone, never your checkout. When the run ends, everything it left — committed or not — comes back into refs/sandbox/<id>, verified against your repository, and your branches do not move until you merge. While a run is attached, its working tree is checkpointed every five minutes, so a crash costs minutes rather than the run."
          />
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <div className="flex flex-col gap-2">
              <p className="eyebrow">when the run ends</p>
              <CodeBlock code={BRING_BACK} />
            </div>
            <div className="flex flex-col gap-2">
              <p className="eyebrow">when something ended it first</p>
              <CodeBlock code={RECOVER} />
            </div>
          </div>
        </Section>

        {/* ------------------------------------------------------- sessions */}
        <Section id="sessions">
          <SectionHead
            eyebrow="supervision"
            title="A sandbox outlives the terminal that started it"
            lead="sandboxd owns every sandbox, not the client that asked for it. Detach, close the laptop, come back from another machine: the same four commands find it, wherever it runs."
          />
          <SessionCommands />
          <div className="mt-10 flex flex-col gap-2">
            <SectionHead
              className="mb-2"
              eyebrow="observability"
              title="And afterwards, what it did"
              lead="The server records every sandbox's events — whichever client asked: its policy and environment variable names, every process with its argv and exit code, files read and written, network changes, and how it ended. Values are never written down."
            />
            <CodeBlock code={EVENTS} />
          </div>
        </Section>

        {/* --------------------------------------------------------- agents */}
        <Section id="agents" tinted>
          <SectionHead
            eyebrow="coding agents"
            title={`${AGENTS.length} agents under one prefix, logins kept between runs`}
            lead={
              <>
                <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]">
                  sandbox-cli agent claude --dangerously-skip-permissions
                </code>{" "}
                is <code className="font-mono text-[0.85em]">run</code> with an agent&apos;s
                conveniences on top: its login copied in and back out, its own environment
                variables forwarded when set, and everything after the sandbox flags handed to the
                agent untouched. None of it is required to use a sandbox.
              </>
            }
          />
          <AgentExplorer />
          <div className="mt-6 flex flex-col gap-3 rounded-2xl border bg-card p-5">
            <div className="flex flex-wrap items-baseline justify-between gap-2">
              <h3 className="text-[0.95rem] font-semibold tracking-tight">
                Fallbacks when a provider is down, and fleets of agents
              </h3>
              <Link
                href={MULTI_AGENT_PATH}
                className="inline-flex items-center gap-1 text-sm text-muted-foreground transition-colors hover:text-foreground"
              >
                running a fleet <ArrowUpRight className="size-3.5" />
              </Link>
            </div>
            <CodeBlock code={FLEET} />
          </div>
        </Section>

        {/* -------------------------------------------------------- compare */}
        <Section id="compare">
          <SectionHead
            eyebrow="the alternatives, honestly"
            title="Where this sits, including where it loses"
            lead="Running untrusted code somewhere safer is a crowded space. This compares kinds of tool rather than products, so it stays true; where a kind varies, the cell says so."
          />
          <ComparisonTable />

          <div className="mt-14">
            <SectionHead
              eyebrow="platform support"
              title="The client runs anywhere. Sandboxes need a VM."
              lead="Where a feature is missing on a platform, the server says so in its capabilities and refuses the request rather than running a weaker one."
            />
            <PlatformTable />
          </div>
        </Section>

        {/* ---------------------------------------------------------- setup */}
        <Section id="setup" tinted>
          <SectionHead
            className="mb-6"
            eyebrow="setup"
            title="From a cold machine to a verified sandbox"
            lead="Pick where sandboxes will run. Every path ends with doctor, because installing is the easy half and what this sandboxd can actually deliver is a property of the machine."
          />
          <SetupGuide />
          <Link
            href={SETUP_PATH}
            className="mt-6 inline-flex items-center gap-1.5 text-sm font-medium underline-offset-4 hover:underline"
          >
            The full setup guide, with troubleshooting →
          </Link>
        </Section>

        {/* -------------------------------------------------------- install */}
        <Section id="install">
          <div className="grid grid-cols-1 items-start gap-10 lg:grid-cols-[1fr_minmax(0,30rem)]">
            <div>
              <SectionHead
                className="mb-6"
                eyebrow="get started"
                title="One script: client, server, guest agent."
                lead="On Linux and Apple-silicon Macs it installs all three; elsewhere, the client, which talks to a sandboxd somewhere else. Go 1.25+ only if you build from source."
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
                  href={DOC_URL.readme}
                  target="_blank"
                  rel="noopener noreferrer"
                  className={cn(
                    buttonVariants({ variant: "outline", size: "lg" }),
                    "gap-1.5 px-4",
                  )}
                >
                  Read the README
                  <ArrowUpRight className="size-4" />
                </a>
              </div>
              <p className="mt-6 max-w-xl text-sm leading-relaxed text-muted-foreground">
                Uninstalling is cautious: <code className="font-mono text-[0.9em]">--uninstall</code>{" "}
                removes the binaries and <em>reports</em> what else is on disk —{" "}
                <code className="font-mono text-[0.9em]">~/.config/sandbox</code> holds your agent
                logins, and sandboxd&apos;s state directory your volumes. Add{" "}
                <code className="font-mono text-[0.9em]">--purge</code> when you mean it.
              </p>
              <UninstallSteps className="mt-5 max-w-xl" />
            </div>

            <div className="flex flex-col gap-3">
              <InstallCard />
              <p className="text-xs text-muted-foreground">
                verified against the release <code className="font-mono">checksums.txt</code> ·
                installs to <code className="font-mono">~/.local/bin</code> · no root, no package
                manager.
              </p>
            </div>
          </div>
        </Section>
      </main>

      <SiteFooter />
    </div>
  );
}
