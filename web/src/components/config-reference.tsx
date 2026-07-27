"use client";

import { useState } from "react";
import { ChevronRight } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyButton } from "@/components/copy-button";
import {
  CONFIG_FILE,
  CONFIG_GROUPS,
  CONFIG_SAMPLES,
  MERGE_RULES,
  PRECEDENCE,
} from "@/lib/config-schema";
import { cn } from "@/lib/utils";

/**
 * One line of YAML, split into the three things worth telling apart: a trailing
 * comment, the key, and the value. Deliberately not a parser — the samples are
 * hand-written and flat enough that a split on ":" and "#" is honest.
 */
function YamlLine({ line }: { line: string }) {
  if (line.trim() === "") return <div className="whitespace-pre"> </div>;

  const hash = line.indexOf("#");
  const code = hash === -1 ? line : line.slice(0, hash);
  const comment = hash === -1 ? "" : line.slice(hash);

  const colon = code.indexOf(":");
  const isKey = colon !== -1 && !code.trimStart().startsWith("-");
  const key = isKey ? code.slice(0, colon + 1) : "";
  const value = isKey ? code.slice(colon + 1) : code;

  return (
    <div className="whitespace-pre">
      {isKey ? <span className="text-[#93c5fd]">{key}</span> : null}
      <span className="text-[#e7e7ea]">{value}</span>
      {comment ? <span className="text-[#7c7c88]">{comment}</span> : null}
    </div>
  );
}

const ALL = "all";

export function ConfigReference({ className }: { className?: string }) {
  const [sample, setSample] = useState(CONFIG_SAMPLES[0].id);
  const [group, setGroup] = useState<string>(ALL);

  const active = CONFIG_SAMPLES.find((s) => s.id === sample) ?? CONFIG_SAMPLES[0];
  const shown = group === ALL ? CONFIG_GROUPS : CONFIG_GROUPS.filter((g) => g.id === group);
  const keyCount = CONFIG_GROUPS.reduce((n, g) => n + g.keys.length, 0);

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      {/* --------------------------------------------------- precedence chain */}
      <div className="rounded-2xl border bg-card p-5">
        <p className="eyebrow mb-4">
          <span className="h-px w-6 bg-current opacity-50" />
          precedence — later wins, key by key
        </p>
        <ol className="flex flex-col gap-2 lg:flex-row lg:items-stretch">
          {PRECEDENCE.map((p, i) => (
            <li key={p.label} className="flex min-w-0 flex-1 items-stretch gap-2">
              <div className="flex min-w-0 flex-1 flex-col gap-1.5 rounded-xl border bg-surface p-3">
                <div className="flex items-baseline gap-2">
                  <span className="tnum text-[0.68rem] text-muted-foreground">{i + 1}</span>
                  <code className="min-w-0 font-mono text-[0.72rem] break-all">{p.label}</code>
                </div>
                <span className="eyebrow">{p.hint}</span>
                <p className="text-[0.75rem] leading-relaxed text-muted-foreground">{p.body}</p>
              </div>
              <ChevronRight
                aria-hidden
                className={cn(
                  "my-auto size-4 shrink-0 rotate-90 text-muted-foreground/50 lg:rotate-0",
                  i === PRECEDENCE.length - 1 && "invisible",
                )}
              />
            </li>
          ))}
        </ol>
        <p className="mt-4 text-[0.8rem] leading-relaxed text-muted-foreground">
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]">
            sandbox-cli config show
          </code>{" "}
          prints the merged result for the directory you are standing in, and{" "}
          <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]">
            sandbox-cli config path
          </code>{" "}
          says which files it read.
        </p>
      </div>

      {/* -------------------------------------------------------- the samples */}
      <div className="overflow-hidden rounded-2xl border bg-card">
        <Tabs value={sample} onValueChange={(v) => setSample(String(v))} className="gap-0">
          <div className="no-scrollbar flex items-center gap-3 overflow-x-auto border-b px-2">
            <TabsList variant="line" className="h-10 gap-0.5">
              {CONFIG_SAMPLES.map((s) => (
                <TabsTrigger
                  key={s.id}
                  value={s.id}
                  className="px-2.5 text-[0.8rem] whitespace-nowrap"
                >
                  {s.label}
                </TabsTrigger>
              ))}
            </TabsList>
            <span className="ml-auto hidden pr-2 text-[0.7rem] whitespace-nowrap text-muted-foreground sm:inline">
              {active.hint}
            </span>
          </div>

          {CONFIG_SAMPLES.map((s) => (
            <TabsContent key={s.id} value={s.id} className="p-0">
              <div className="relative bg-[#0b0b0d]">
                <div className="flex items-center justify-between gap-3 border-b border-white/8 px-4 py-2">
                  <code className="font-mono text-[0.7rem] text-[#8a8a94]">{CONFIG_FILE}</code>
                  <CopyButton
                    value={s.yaml}
                    className="text-[#a1a1aa] hover:bg-white/10 hover:text-white"
                  />
                </div>
                <pre className="no-scrollbar overflow-x-auto px-4 py-4 font-mono text-[0.76rem] leading-[1.65]">
                  {s.yaml.replace(/\n$/, "").split("\n").map((line, i) => (
                    <YamlLine key={i} line={line} />
                  ))}
                </pre>
              </div>
            </TabsContent>
          ))}
        </Tabs>

        <p className="px-4 py-3 text-[0.8rem] leading-relaxed text-muted-foreground">
          {active.note}
        </p>
      </div>

      {/* ------------------------------------------------------ key reference */}
      <div className="rounded-2xl border bg-card p-5">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <h3 className="text-[0.95rem] font-semibold tracking-tight">
            Every key, and what you get without it
          </h3>
          <div className="no-scrollbar -mx-1 flex gap-1.5 overflow-x-auto px-1">
            <Button
              size="sm"
              variant={group === ALL ? "default" : "outline"}
              onClick={() => setGroup(ALL)}
              className="shrink-0"
            >
              Everything
              <span className="ml-1 opacity-60 tnum">{keyCount}</span>
            </Button>
            {CONFIG_GROUPS.map((g) => (
              <Button
                key={g.id}
                size="sm"
                variant={group === g.id ? "default" : "outline"}
                onClick={() => setGroup(g.id)}
                className="shrink-0"
              >
                {g.label}
                <span className="ml-0.5 opacity-60 tnum">{g.keys.length}</span>
              </Button>
            ))}
          </div>
        </div>

        <div className="flex flex-col gap-6">
          {shown.map((g) => (
            <div key={g.id} className="flex flex-col gap-2">
              <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
                <span className="eyebrow">{g.label}</span>
                <span className="text-[0.78rem] text-muted-foreground">{g.blurb}</span>
              </div>
              <ul className="flex flex-col divide-y rounded-xl border">
                {g.keys.map((k) => (
                  <li
                    key={k.key}
                    className="grid grid-cols-1 gap-x-4 gap-y-1.5 px-4 py-3 sm:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]"
                  >
                    <div className="flex flex-col gap-1.5">
                      <code className="font-mono text-[0.76rem] font-medium break-all">
                        {k.key}
                      </code>
                      <span className="font-mono text-[0.66rem] text-muted-foreground">
                        {k.type}
                      </span>
                      <Badge
                        variant="outline"
                        className="w-fit border-border text-[0.62rem] font-normal text-muted-foreground"
                      >
                        unset&nbsp;→&nbsp;{k.fallback}
                      </Badge>
                      {/* Where a key may be set is a security fact, not a
                          preference: a committed .sandbox.yaml is untrusted
                          input, so the keys that reach the host or widen what
                          the container reaches are refused from it outright. */}
                      {k.where ? (
                        <Badge
                          variant="outline"
                          className="w-fit border-amber-500/40 text-[0.62rem] font-normal text-amber-600 dark:text-amber-400"
                        >
                          {k.where === "user"
                            ? "your config only"
                            : "project may tighten only"}
                        </Badge>
                      ) : null}
                    </div>
                    <p className="text-[0.82rem] leading-relaxed text-muted-foreground">
                      {k.body}
                    </p>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </div>
      </div>

      {/* -------------------------------------------------------- merge rules */}
      <div className="grid grid-cols-1 gap-3 md:grid-cols-3">
        {MERGE_RULES.map((r) => (
          <div key={r.title} className="flex flex-col gap-2 rounded-2xl border bg-card p-5">
            <h3 className="text-[0.9rem] font-semibold tracking-tight">{r.title}</h3>
            <p className="text-[0.82rem] leading-relaxed text-muted-foreground">{r.body}</p>
          </div>
        ))}
      </div>
    </div>
  );
}
