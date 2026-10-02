import { ArrowRight, Cpu, Terminal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { GithubMark } from "@/components/logo";
import { buttonVariants } from "@/components/ui/button";
import { InstallCard } from "@/components/install-card";
import { ContainmentSimulator } from "@/components/containment-simulator";
import { FIRST_RUN, HERO_STATS, REPO_URL } from "@/lib/site";
import { cn } from "@/lib/utils";

export function Hero() {
  return (
    <section className="relative overflow-hidden">
      {/* ambient: a faint engineering grid that fades before it distracts */}
      <div
        aria-hidden="true"
        className="bg-blueprint pointer-events-none absolute inset-0 opacity-70 [mask-image:radial-gradient(80%_60%_at_50%_0%,black,transparent)]"
      />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 top-0 h-[520px]"
        style={{
          background:
            "radial-gradient(60% 70% at 50% -10%, color-mix(in srgb, var(--contained) 10%, transparent), transparent 70%)",
        }}
      />

      <div className="relative mx-auto w-full max-w-6xl px-5 pt-14 pb-6 sm:px-6 lg:pt-20">
        <div className="grid grid-cols-1 items-start gap-10 lg:grid-cols-[1.05fr_0.95fr] lg:gap-14">
          <div className="flex flex-col items-start">
            <Badge
              variant="outline"
              className="h-6 gap-2 border-contained/30 bg-contained-soft px-2.5 font-mono text-[0.7rem] text-contained"
            >
              <span className="relative flex size-1.5">
                <span
                  className="absolute inline-flex size-full rounded-full bg-contained"
                  style={{ animation: "pulse-ring 2.4s ease-out infinite" }}
                />
                <span className="relative inline-flex size-1.5 rounded-full bg-contained" />
              </span>
              every sandbox a VM with its own kernel
            </Badge>

            <h1 className="mt-5 text-[2.4rem] leading-[1.06] font-semibold tracking-[-0.032em] text-balance sm:text-[3rem] lg:text-[3.25rem]">
              A whole machine for the agent.
              <br className="hidden sm:block" />{" "}
              <span className="text-muted-foreground">None of it is yours.</span>
            </h1>

            <p className="mt-5 max-w-xl text-[1.05rem] leading-relaxed text-muted-foreground">
              <span className="font-mono text-[0.95em] text-foreground">sandbox-cli</span> gives
              any command — a test suite, a build, Claude Code or Codex at full autonomy — a
              disposable microVM, on your Mac, a Linux machine you control, or the cloud, behind
              one API. Your repository goes in as a git bundle and comes back as verified commits;
              nothing on your machine is mounted, and egress is an allowlist of names enforced
              outside the guest.
            </p>

            <div className="mt-7 flex flex-wrap items-center gap-2.5">
              <a href="#install" className={cn(buttonVariants({ size: "lg" }), "gap-1.5 px-4")}>
                Install
                <ArrowRight className="size-4" />
              </a>
              <a
                href={REPO_URL}
                target="_blank"
                rel="noopener noreferrer"
                className={cn(buttonVariants({ variant: "outline", size: "lg" }), "gap-1.5 px-4")}
              >
                <GithubMark className="size-4" />
                Source on GitHub
              </a>
            </div>

            <p className="mt-5 flex flex-wrap items-center gap-x-4 gap-y-1.5 text-xs text-muted-foreground">
              <span className="inline-flex items-center gap-1.5">
                <Cpu className="size-3.5" /> Apple silicon (macOS 26) or Linux with KVM
              </span>
              <span className="inline-flex items-center gap-1.5">
                <Terminal className="size-3.5" /> client on macOS · Linux · Windows
              </span>
              <span>MIT licensed · written in Go</span>
            </p>
          </div>

          <div className="flex w-full flex-col gap-4">
            <InstallCard />

            <div className="rounded-2xl border bg-surface px-4 py-3.5">
              <p className="eyebrow mb-2.5">then</p>
              <ul className="flex flex-col gap-2">
                {FIRST_RUN.map((f) => (
                  <li key={f.cmd} className="flex flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
                    <code className="font-mono text-[0.8rem] font-medium">
                      <span className="pr-1.5 text-contained select-none">$</span>
                      {f.cmd}
                    </code>
                    <span className="text-xs text-muted-foreground">{f.note}</span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </div>
      </div>

      {/* The argument, animated. */}
      <div className="relative mx-auto w-full max-w-6xl px-5 pt-8 sm:px-6">
        <ContainmentSimulator />
      </div>

      {/* Four numbers that are the whole product. */}
      <div className="relative mx-auto mt-10 w-full max-w-6xl px-5 pb-4 sm:px-6">
        <dl className="grid grid-cols-2 gap-px overflow-hidden rounded-2xl border bg-border md:grid-cols-4">
          {HERO_STATS.map((s) => (
            <div key={s.label} className="flex flex-col gap-0.5 bg-card px-4 py-5">
              <dt
                className={cn(
                  "text-2xl leading-none font-semibold tracking-tight tnum",
                  s.mono && "font-mono text-xl",
                )}
              >
                {s.value}
              </dt>
              <dd className="mt-1.5 text-sm font-medium">{s.label}</dd>
              <dd className="text-xs text-muted-foreground">{s.sub}</dd>
            </div>
          ))}
        </dl>
      </div>
    </section>
  );
}
