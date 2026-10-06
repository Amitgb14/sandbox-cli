"use client";

import { useState } from "react";
import { Check, ChevronDown, KeyRound, Globe, FileKey2, Info } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/types";

type Status = { label: string; tone: "good" | "warn" | "muted"; hint: string };

/**
 * Whether an agent can run as things stand: from its saved login, or an API
 * key set where Studio runs, which a launch forwards. Only what Studio knows:
 * a saved login file may still have expired, which only the agent can tell.
 */
export function agentStatus(a: Agent): Status {
  const keys = (a.env ?? []).filter((e) => e.set).map((e) => e.name);
  if (a.login === "saved") return { label: "Logged in", tone: "good", hint: "Its saved login is copied in when a run starts." };
  if (keys.length) return { label: "API key set", tone: "good", hint: `${keys.join(", ")} is set where Studio runs, and is forwarded to the run.` };
  if (a.login === "not kept")
    return {
      label: "Needs an API key",
      tone: "warn",
      hint: "This agent logs in with an API key only. Set one of its variables where you start Studio, then restart Studio.",
    };
  return {
    label: "Not logged in",
    tone: "muted",
    hint: "Start it interactive and log in in its terminal; the login is kept for the next run. Or set an API key where you start Studio.",
  };
}

function Row({ k, icon: Icon, children }: { k: string; icon: React.ComponentType<{ className?: string }>; children: React.ReactNode }) {
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
 * The agents Studio runs, one row each: chosen with a click, opened for what
 * it needs to log in — where its login is kept, the API keys it takes and
 * which are set, the API it always reaches.
 */
export function AgentList({ agents, value, onChange }: { agents: Agent[]; value: string; onChange: (name: string) => void }) {
  const [open, setOpen] = useState<string | null>(null);
  return (
    <div role="radiogroup" aria-label="Agent" className="divide-y overflow-hidden rounded-lg border bg-card">
      {agents.map((a) => {
        const on = a.name === value;
        const expanded = open === a.name;
        const st = agentStatus(a);
        return (
          <div key={a.name} className={cn("transition-colors", on && "bg-primary/5")}>
            <div className="flex items-center gap-1 pr-2">
              <button
                type="button"
                role="radio"
                aria-checked={on}
                onClick={() => onChange(a.name)}
                className="flex min-w-0 flex-1 items-center gap-3 px-3.5 py-3 text-left"
              >
                <span
                  aria-hidden
                  className={cn(
                    "flex size-4 shrink-0 items-center justify-center rounded-full border",
                    on ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/40",
                  )}
                >
                  {on ? <Check className="size-3" /> : null}
                </span>
                <span className="w-24 shrink-0 font-mono text-sm font-medium">{a.name}</span>
                <span className="hidden min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground sm:block">{a.provider_host}</span>
                <span
                  title={st.hint}
                  className={cn(
                    "ml-auto shrink-0 rounded-full border px-2 py-0.5 text-[11px] whitespace-nowrap",
                    st.tone === "good" && "border-status-good/30 bg-status-good/10 text-status-good",
                    st.tone === "warn" && "border-caution/30 bg-caution/10 text-caution",
                    st.tone === "muted" && "text-muted-foreground",
                  )}
                >
                  {st.label}
                </span>
              </button>
              <button
                type="button"
                aria-expanded={expanded}
                aria-label={`${expanded ? "Hide" : "Show"} ${a.name}'s login details`}
                onClick={() => setOpen(expanded ? null : a.name)}
                className="rounded-md p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                <ChevronDown className={cn("size-4 transition-transform", expanded && "rotate-180")} />
              </button>
            </div>
            {expanded ? (
              <dl className="flex flex-col gap-2.5 border-t bg-muted/20 px-4 py-3.5 pl-[3.25rem]">
                <Row k="Login" icon={FileKey2}>
                  {a.login_files?.length ? (
                    <span>
                      kept between runs:{" "}
                      {a.login_files.map((f) => (
                        <code key={f} className="mr-1.5 font-mono text-[11px]">
                          ~/{f}
                        </code>
                      ))}
                    </span>
                  ) : (
                    <span className="text-muted-foreground">not kept: it logs in with an API key each run</span>
                  )}
                </Row>
                {a.env?.length ? (
                  <Row k="API keys" icon={KeyRound}>
                    <div className="flex flex-wrap gap-1.5">
                      {a.env.map((e) => (
                        <span
                          key={e.name}
                          className={cn(
                            "inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 font-mono text-[11px]",
                            e.set ? "border-status-good/30 text-status-good" : "text-muted-foreground",
                          )}
                        >
                          {e.set ? <Check className="size-3" /> : null}
                          {e.name}
                        </span>
                      ))}
                    </div>
                  </Row>
                ) : null}
                {a.provider_host ? (
                  <Row k="Network" icon={Globe}>
                    always reaches <code className="font-mono text-[11px]">{a.provider_host}</code>, whatever else the run may reach
                  </Row>
                ) : null}
                <Row k="Status" icon={Info}>
                  <span className="text-muted-foreground">{st.hint}</span>
                </Row>
              </dl>
            ) : null}
          </div>
        );
      })}
    </div>
  );
}
