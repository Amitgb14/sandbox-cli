import { ArrowRight, Package, Terminal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { GithubMark } from "@/components/logo";
import { buttonVariants } from "@/components/ui/button";
import { InstallCard } from "@/components/install-card";
import { ContainmentSimulator } from "@/components/containment-simulator";
import { FIRST_RUN, HERO_STATS, REPO_URL, VERSION } from "@/lib/site";
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
              v{VERSION} · out now
            </Badge>

            <h1 className="mt-5 text-[2.4rem] leading-[1.06] font-semibold tracking-[-0.032em] text-balance sm:text-[3rem] lg:text-[3.25rem]">
              Give the agent full autonomy.
              <br className="hidden sm:block" />{" "}
              <span className="text-muted-foreground">Give it nothing else.</span>
            </h1>

            <p className="mt-5 max-w-xl text-[1.05rem] leading-relaxed text-muted-foreground">
              <span className="font-mono text-[0.95em] text-foreground">sandbox-cli</span> runs
              Claude Code, Codex, Gemini and twelve more coding agents inside a disposable Docker
              container. Only the project you point it at is mounted;{" "}
              <span className="text-foreground">HOME</span> is a fake ephemeral path and your SSH
              keys, cloud credentials and browser cookies are not there to be read.
            </p>

            <div className="mt-7 flex flex-wrap items-center gap-2.5">
              <a href="#install" className={cn(buttonVariants({ size: "lg" }), "gap-1.5 px-4")}>
                Install v{VERSION}
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
                <Package className="size-3.5" /> Needs Docker. Nothing else.
              </span>
              <span className="inline-flex items-center gap-1.5">
                {/* Windows is reachable through WSL2 and the setup guide says how,
                    but a one-line summary listing it beside macOS and Linux
                    claims a parity that is not there: sandbox-cli runs inside
                    the Linux distribution, not on Windows itself. */}
                <Terminal className="size-3.5" /> macOS · Linux
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
