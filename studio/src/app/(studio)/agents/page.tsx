"use client";

import { useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { ArrowRight, Bot, ChevronDown, FileKey2, Globe, KeyRound, Pencil, Plus, Trash2 } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { agentStatus } from "@/components/launch/agent-list";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useAgents, useDeleteAgentKey, useSaveAgentKey } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/types";

/**
 * The agents Studio runs: only those with a verified headless mode, because an
 * agent that stops to ask with nobody there does not fail, it hangs. The
 * interactive-only wrappers stay in the CLI (sandbox-cli agent ls).
 *
 * One row each, opened for its particulars: where its login is kept, the API
 * it reaches, and its API keys, which can be saved here. A saved key is
 * write-only — Studio's server keeps it in a file only this user can read and
 * never serves it back — and a run uses it when the environment Studio was
 * started in does not set that variable.
 */
export default function AgentsPage() {
  const { data, isLoading } = useAgents();
  const [open, setOpen] = useState<string | null>(null);
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Agents"
        description={
          process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on"
            ? "The agents with a verified headless mode. Start one interactive from the Playground and log in inside its sandbox; to run one unattended as a job, store its API key under Secrets."
            : "The agents with a verified headless mode. A login is copied into each sandbox an agent runs in, and back out when it ends: log in once with sandbox-cli agent <name>, start a console from the Playground, or save an API key here."
        }
      />
      {isLoading ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-14 rounded-lg" />
          ))}
        </div>
      ) : (data ?? []).length === 0 ? (
        <EmptyState icon={Bot} title="No agents" description="This sandbox-cli knows no agent with a verified headless mode." />
      ) : (
        <div className="overflow-hidden rounded-lg border bg-card">
          <div className={cn(ROW, "hidden border-b bg-muted/30 py-2 font-mono text-[10px] tracking-[0.06em] text-muted-foreground uppercase md:grid")}>
            <span>Agent</span>
            <span>Login</span>
            <span>API keys</span>
            <span>Reaches</span>
            <span />
          </div>
          <ul className="divide-y">
            {data!.map((a) => (
              <AgentRow key={a.name} agent={a} open={open === a.name} onToggle={() => setOpen(open === a.name ? null : a.name)} />
            ))}
          </ul>
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Other agents can be run interactively from the CLI; Studio lists only these. Saved keys are also used by sandbox-cli agent runs.
      </p>
    </div>
  );
}

const ROW = "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 md:grid-cols-[minmax(10rem,1.2fr)_9rem_minmax(0,1.3fr)_minmax(0,1fr)_7.5rem]";

const TONE = {
  good: "border-status-good/30 bg-status-good/10 text-status-good",
  warn: "border-caution/30 bg-caution/10 text-caution",
  muted: "text-muted-foreground",
} as const;

function AgentRow({ agent: a, open, onToggle }: { agent: Agent; open: boolean; onToggle: () => void }) {
  const can = useCan();
  const st = agentStatus(a);
  const present = (a.env ?? []).filter((e) => e.set || e.saved);
  return (
    <li className={cn("transition-colors", open ? "bg-muted/20" : "hover:bg-muted/20")}>
      <div className={cn(ROW, "py-3")}>
        <button type="button" onClick={onToggle} aria-expanded={open} className="flex min-w-0 items-center gap-3 text-left">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
            <Bot className="size-4" aria-hidden />
          </span>
          <span className="min-w-0">
            <span className="block truncate font-mono text-sm font-medium">{a.name}</span>
            <span className="block text-[11px] text-muted-foreground">verified headless</span>
          </span>
        </button>
        <span className="hidden md:block" title={st.hint}>
          <span className={cn("inline-flex rounded-full border px-2 py-0.5 text-[11px] whitespace-nowrap", TONE[st.tone])}>{st.label}</span>
        </span>
        <span className="hidden min-w-0 truncate text-xs md:block">
          {present.length ? (
            <span className="font-mono text-status-good">{present.map((e) => e.name).join(", ")}</span>
          ) : (
            <span className="text-muted-foreground">{a.env?.length ? `none of ${a.env.length}` : "—"}</span>
          )}
        </span>
        <span className="hidden truncate font-mono text-xs text-muted-foreground md:block">{a.provider_host ?? "—"}</span>
        <span className="flex items-center justify-end gap-1">
          {can("sandbox:create") && (
            <Button asChild size="sm" variant="ghost" className="h-7 gap-1 px-2 text-xs">
              <Link href="/launch">
                Launch <ArrowRight className="size-3.5" aria-hidden />
              </Link>
            </Button>
          )}
          <button
            type="button"
            aria-expanded={open}
            aria-label={`${open ? "Hide" : "Show"} ${a.name}'s details`}
            onClick={onToggle}
            className="rounded-md p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
          >
            <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
          </button>
        </span>
      </div>
      {open ? (
        <div className="grid gap-5 border-t bg-muted/10 px-4 py-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1.6fr)] md:pl-15">
          <dl className="flex flex-col gap-3 text-xs">
            <div className="flex flex-col gap-1">
              <dt className="flex items-center gap-1.5 text-muted-foreground">
                <FileKey2 className="size-3.5" aria-hidden /> Login
              </dt>
              <dd>
                {a.login_files?.length ? (
                  <>
                    <span className={a.login === "saved" ? "text-status-good" : "text-muted-foreground"}>
                      {a.login === "saved" ? "saved, copied in at launch: " : "kept between runs once you log in: "}
                    </span>
                    {a.login_files.map((f) => (
                      <code key={f} className="mr-1.5 font-mono text-[11px]">
                        ~/{f}
                      </code>
                    ))}
                  </>
                ) : (
                  <span className="text-muted-foreground">not kept: it logs in with an API key each run</span>
                )}
              </dd>
            </div>
            {a.provider_host ? (
              <div className="flex flex-col gap-1">
                <dt className="flex items-center gap-1.5 text-muted-foreground">
                  <Globe className="size-3.5" aria-hidden /> Network
                </dt>
                <dd>
                  always reaches <code className="font-mono text-[11px]">{a.provider_host}</code>, whatever else the run may reach
                </dd>
              </div>
            ) : null}
            <div className="flex flex-col gap-1">
              <dt className="text-muted-foreground">Status</dt>
              <dd className="text-muted-foreground">{st.hint}</dd>
            </div>
          </dl>
          {/* Saved keys live in this machine's user's config: hosted users
              keep theirs under Secrets, and the hosted build has no editor. */}
          {process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on" && <AgentKeys agent={a} />}
        </div>
      ) : null}
    </li>
  );
}

/** Each variable the agent reads, where its value comes from, and a way to save, change or remove a key for it. */
function AgentKeys({ agent: a }: { agent: Agent }) {
  const [editing, setEditing] = useState<string | null>(null);
  const [value, setValue] = useState("");
  const save = useSaveAgentKey();
  const remove = useDeleteAgentKey();
  if (!a.env?.length) {
    return <p className="text-xs text-muted-foreground">This agent reads no API key from the environment.</p>;
  }
  function submit(e: React.FormEvent, name: string) {
    e.preventDefault();
    save.mutate(
      { agent: a.name, name, value },
      {
        onSuccess: () => {
          toast.success(`Saved ${name} for ${a.name}`);
          setEditing(null);
          setValue("");
        },
        onError: (err) => toast.error(err.message),
      },
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <h3 className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <KeyRound className="size-3.5" aria-hidden /> API keys
      </h3>
      <ul className="divide-y rounded-md border bg-background">
        {a.env.map((e) => (
          <li key={e.name} className="flex flex-col gap-2 px-3 py-2">
            <div className="flex flex-wrap items-center gap-2">
              <code className="font-mono text-xs font-medium">{e.name}</code>
              <KeySource set={e.set} saved={!!e.saved} />
              <span className="ml-auto flex items-center gap-1">
                {editing !== e.name && (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 gap-1 px-2 text-xs"
                    onClick={() => {
                      setEditing(e.name);
                      setValue("");
                    }}
                    aria-label={`${e.saved ? "Edit" : "Add"} ${e.name}`}
                  >
                    {e.saved ? <Pencil className="size-3.5" /> : <Plus className="size-3.5" />}
                    {e.saved ? "Edit" : "Add"}
                  </Button>
                )}
                {e.saved && editing !== e.name && (
                  <Button
                    size="sm"
                    variant="ghost"
                    className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-destructive"
                    disabled={remove.isPending}
                    aria-label={`Remove ${e.name}`}
                    onClick={() =>
                      confirm(`Remove the saved ${e.name} for ${a.name}?`) &&
                      remove.mutate(
                        { agent: a.name, name: e.name },
                        { onSuccess: () => toast.success(`Removed ${e.name}`), onError: (err) => toast.error(err.message) },
                      )
                    }
                  >
                    <Trash2 className="size-3.5" />
                    Remove
                  </Button>
                )}
              </span>
            </div>
            {editing === e.name ? (
              <form className="flex flex-wrap items-center gap-2" onSubmit={(ev) => submit(ev, e.name)}>
                <Input
                  autoFocus
                  type="password"
                  autoComplete="off"
                  spellCheck={false}
                  aria-label={`${e.name} value`}
                  placeholder={e.saved ? "the new key" : "paste the key"}
                  value={value}
                  onChange={(ev) => setValue(ev.target.value)}
                  className="h-8 min-w-0 flex-1 font-mono text-xs"
                  required
                />
                <Button type="submit" size="sm" className="h-8" disabled={save.isPending || !value.trim()}>
                  Save
                </Button>
                <Button type="button" size="sm" variant="ghost" className="h-8" onClick={() => setEditing(null)}>
                  Cancel
                </Button>
                {e.set ? (
                  <p className="w-full text-[11px] text-caution">
                    The environment Studio runs in sets {e.name}; its value is used while it does, and this one after.
                  </p>
                ) : null}
              </form>
            ) : null}
          </li>
        ))}
      </ul>
      <p className="text-[11px] text-muted-foreground">
        Saved keys stay on this machine (~/.config/sandbox/agent-keys.json, readable by you only) and are never shown again.
      </p>
    </div>
  );
}

function KeySource({ set, saved }: { set: boolean; saved: boolean }) {
  const label = set && saved ? "environment (overrides saved)" : set ? "from environment" : saved ? "saved" : "not set";
  return (
    <span
      className={cn(
        "inline-flex rounded-full border px-1.5 py-px text-[10px] whitespace-nowrap",
        set || saved ? TONE.good : "text-muted-foreground",
      )}
    >
      {label}
    </span>
  );
}
