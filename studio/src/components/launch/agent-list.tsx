"use client";

import { useState } from "react";
import { Check, ChevronDown, FileKey2, Globe, Info, KeyRound } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/types";

type Status = { label: string; tone: "good" | "warn" | "muted"; hint: string };

/**
 * Whether an agent can run as things stand: from its saved login, or an API
 * key set where Studio runs, which a launch forwards. Only what Studio knows:
 * a saved login file may still have expired, which only the agent can tell.
 */
export function agentStatus(a: Agent): Status {
  // Hosted: nothing is copied in or forwarded from where Studio runs.
  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" || a.login === "in sandbox")
    return { label: "Log in inside", tone: "muted", hint: "Start it interactive and log in in its terminal. For jobs, store its API key under Secrets." };
  const keys = (a.env ?? []).filter((e) => e.set).map((e) => e.name);
  const saved = (a.env ?? []).filter((e) => e.saved && !e.set).map((e) => e.name);
  if (a.login === "saved") return { label: "Logged in", tone: "good", hint: "Its saved login is copied in when a run starts." };
  if (keys.length) return { label: "API key set", tone: "good", hint: `${keys.join(", ")} is set where Studio runs, and is forwarded to the run.` };
  if (saved.length) return { label: "API key saved", tone: "good", hint: `${saved.join(", ")} is saved on the Agents screen, and is forwarded to the run.` };
  if (a.login === "not kept")
    return {
      label: "Needs an API key",
      tone: "warn",
      hint: "This agent logs in with an API key only. Add one on the Agents screen, or set one of its variables where you start Studio.",
    };
  return {
    label: "Not logged in",
    tone: "muted",
    hint: "Start it interactive and log in in its terminal; the login is kept for the next run. Or add an API key on the Agents screen.",
  };
}

const GRID = "grid grid-cols-[1.5rem_6.5rem_minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.2fr)_1.75rem] items-center gap-3";

function Detail({ k, icon: Icon, children }: { k: string; icon: React.ComponentType<{ className?: string }>; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[7.5rem_1fr] items-start gap-3 text-xs">
      <dt className="flex items-center gap-1.5 text-muted-foreground">
        <Icon className="size-3.5" />
        {k}
      </dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}

/**
 * The agents Studio runs, as a table: one row each, with what a launch hands
 * the sandbox for it — its login (copied in), the API keys set where Studio
 * runs (forwarded), and the API it always reaches (allowed through) — on the
 * row itself, and the particulars a click away. A row is chosen with a click.
 */
export function AgentList({ agents, value, onChange }: { agents: Agent[]; value: string; onChange: (name: string) => void }) {
  const [open, setOpen] = useState<string | null>(null);
  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <div className={cn(GRID, "border-b bg-muted/30 px-3.5 py-2 font-mono text-[10px] tracking-[0.06em] text-muted-foreground uppercase")}>
        <span />
        <span>Agent</span>
        <span>Login</span>
        <span>API keys</span>
        <span>Reaches</span>
        <span />
      </div>
      <p className="border-b px-3.5 py-1.5 text-[11px] text-muted-foreground">
        {process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on"
          ? "Handed to the sandbox at launch: the API is let through. You log in inside the sandbox."
          : "Handed to the sandbox at launch: the login is copied in, set keys are forwarded, the API is let through."}
      </p>
      <div role="radiogroup" aria-label="Agent" className="divide-y">
        {agents.map((a) => {
          const on = a.name === value;
          const expanded = open === a.name;
          const st = agentStatus(a);
          const set = (a.env ?? []).filter((e) => e.set || e.saved);
          return (
            <div key={a.name} className={cn("transition-colors", on && "bg-primary/5")}>
              <div className={cn(GRID, "px-3.5 py-2.5")}>
                <button
                  type="button"
                  role="radio"
                  aria-checked={on}
                  aria-label={a.name}
                  onClick={() => onChange(a.name)}
                  className={cn(
                    "flex size-4 items-center justify-center rounded-full border",
                    on ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/40 hover:border-foreground/60",
                  )}
                >
                  {on ? <Check className="size-3" /> : null}
                </button>
                <button type="button" onClick={() => onChange(a.name)} className="truncate text-left font-mono text-sm font-medium">
                  {a.name}
                </button>
                <span title={st.hint}>
                  <span
                    className={cn(
                      "inline-flex rounded-full border px-2 py-0.5 text-[11px] whitespace-nowrap",
                      st.tone === "good" && "border-status-good/30 bg-status-good/10 text-status-good",
                      st.tone === "warn" && "border-caution/30 bg-caution/10 text-caution",
                      st.tone === "muted" && "text-muted-foreground",
                    )}
                  >
                    {st.label}
                  </span>
                </span>
                <span className="truncate text-xs" title={(a.env ?? []).map((e) => `${e.name}${e.set ? " (set)" : e.saved ? " (saved)" : ""}`).join("\n")}>
                  {set.length ? (
                    <span className="font-mono text-status-good">{set.map((e) => e.name).join(", ")}</span>
                  ) : (
                    <span className="text-muted-foreground">{a.env?.length ? `none set (${a.env.length})` : "—"}</span>
                  )}
                </span>
                <span className="truncate font-mono text-xs text-muted-foreground">{a.provider_host ?? "—"}</span>
                <button
                  type="button"
                  aria-expanded={expanded}
                  aria-label={`${expanded ? "Hide" : "Show"} ${a.name}'s login details`}
                  onClick={() => setOpen(expanded ? null : a.name)}
                  className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
                >
                  <ChevronDown className={cn("size-4 transition-transform", expanded && "rotate-180")} />
                </button>
              </div>
              {expanded ? (
                <dl className="flex flex-col gap-2.5 border-t bg-muted/20 px-4 py-3.5 pl-[2.75rem]">
                  <Detail k="Login" icon={FileKey2}>
                    {a.login_files?.length ? (
                      <span>
                        kept between runs, copied in at launch:{" "}
                        {a.login_files.map((f) => (
                          <code key={f} className="mr-1.5 font-mono text-[11px]">
                            ~/{f}
                          </code>
                        ))}
                      </span>
                    ) : (
                      <span className="text-muted-foreground">not kept: it logs in with an API key each run</span>
                    )}
                  </Detail>
                  {a.env?.length ? (
                    <Detail k="API keys" icon={KeyRound}>
                      <div className="flex flex-wrap gap-1.5">
                        {a.env.map((e) => (
                          <span
                            key={e.name}
                            className={cn(
                              "inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 font-mono text-[11px]",
                              e.set || e.saved ? "border-status-good/30 text-status-good" : "text-muted-foreground",
                            )}
                          >
                            {e.set || e.saved ? <Check className="size-3" /> : null}
                            {e.name}
                          </span>
                        ))}
                      </div>
                      <p className="mt-1 text-[11px] text-muted-foreground">Those set where Studio runs, or saved on the Agents screen, are forwarded to the run; values are never shown.</p>
                    </Detail>
                  ) : null}
                  {a.provider_host ? (
                    <Detail k="Network" icon={Globe}>
                      always reaches <code className="font-mono text-[11px]">{a.provider_host}</code>, whatever else the run may reach
                    </Detail>
                  ) : null}
                  <Detail k="Status" icon={Info}>
                    <span className="text-muted-foreground">{st.hint}</span>
                  </Detail>
                </dl>
              ) : null}
            </div>
          );
        })}
      </div>
    </div>
  );
}
