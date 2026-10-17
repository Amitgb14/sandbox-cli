"use client";

import { useState } from "react";
import { ChevronDown, ChevronRight, CircleCheck, CircleDashed, CircleSlash, CircleX, MessageSquare, ShieldQuestion, Wrench } from "lucide-react";
import { useAgentLog } from "@/lib/api/queries";
import type { AgentRecord } from "@/lib/types";
import { formatDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Session {
  id: string;
  agent: string;
  user?: string;
  start?: AgentRecord;
  end?: AgentRecord;
  records: AgentRecord[];
}

function sessionsOf(records: AgentRecord[]): Session[] {
  const out: Session[] = [];
  const byId = new Map<string, Session>();
  for (const r of records) {
    let s = byId.get(r.session);
    if (!s) {
      s = { id: r.session, agent: r.agent, user: r.user, records: [] };
      byId.set(r.session, s);
      out.push(s);
    }
    if (r.type === "session.start") s.start = r;
    else if (r.type === "session.end") s.end = r;
    else s.records.push(r);
  }
  return out.reverse(); // newest session first
}

function time(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
}

function ending(s: Session): string {
  if (!s.end) return "running, or Studio stopped before it ended";
  const dropped = s.end.dropped ? ` · ${s.end.dropped} records not kept` : "";
  if (s.end.code !== undefined) return `ended, exit ${s.end.code}${dropped}`;
  return `ended when the page closed${dropped}`;
}

function Cut({ r }: { r: AgentRecord }) {
  return r.truncated ? <span className="text-muted-foreground"> (cut short)</span> : null;
}

function ToolRow({ r }: { r: AgentRecord }) {
  const [open, setOpen] = useState(false);
  const icon =
    r.status === "completed" ? (
      <CircleCheck className="size-3.5 shrink-0 text-emerald-500" />
    ) : r.status === "failed" ? (
      <CircleX className="size-3.5 shrink-0 text-destructive" />
    ) : (
      <CircleDashed className="size-3.5 shrink-0 text-muted-foreground" />
    );
  const more = !!(r.input || r.output || r.images);
  return (
    <div className="rounded-md border bg-muted/30 text-xs">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left disabled:cursor-default"
        disabled={!more}
        onClick={() => setOpen((o) => !o)}
      >
        {more ? open ? <ChevronDown className="size-3.5 shrink-0" /> : <ChevronRight className="size-3.5 shrink-0" /> : <Wrench className="size-3.5 shrink-0 text-muted-foreground" />}
        <span className="min-w-0 flex-1 truncate font-mono">{r.title || "a tool"}</span>
        <span className="text-muted-foreground">{r.status}</span>
        {icon}
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-t px-2.5 py-2">
          {r.input ? (
            <div>
              <p className="mb-0.5 text-[11px] text-muted-foreground">Arguments</p>
              <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px]">{r.input}</pre>
            </div>
          ) : null}
          {r.output ? (
            <div>
              <p className="mb-0.5 text-[11px] text-muted-foreground">Result</p>
              <pre className="max-h-60 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px]">{r.output}</pre>
            </div>
          ) : null}
          {r.images ? <p className="text-[11px] text-muted-foreground">{r.images === 1 ? "an image" : `${r.images} images`} (not kept)</p> : null}
          {r.truncated ? <p className="text-[11px] text-muted-foreground">Cut short: the log keeps 4 KiB of each.</p> : null}
        </div>
      )}
    </div>
  );
}

function Row({ r }: { r: AgentRecord }) {
  let body: React.ReactNode;
  switch (r.type) {
    case "prompt":
      body = (
        <div className="ml-auto max-w-[85%] whitespace-pre-wrap break-words rounded-lg bg-primary px-3 py-2 text-sm text-primary-foreground">
          {r.text}
          {r.images ? ` [${r.images === 1 ? "an image" : `${r.images} images`}]` : null}
          <Cut r={r} />
        </div>
      );
      break;
    case "tool":
      body = <ToolRow r={r} />;
      break;
    case "permission":
      body = (
        <p className="flex items-start gap-1.5 text-xs">
          <ShieldQuestion className="mt-px size-3.5 shrink-0 text-amber-500" />
          <span className="min-w-0 break-words">
            Asked to run <span className="font-mono">{r.title}</span>:{" "}
            <span className={cn("font-medium", r.option_kind?.startsWith("reject") && "text-destructive")}>
              {r.status === "selected" ? r.option : r.status === "cancelled" ? "cancelled" : "not answered"}
            </span>
          </span>
        </p>
      );
      break;
    case "turn.end":
      body = (
        <div className="flex flex-col gap-0.5">
          {r.text ? (
            <div className="whitespace-pre-wrap break-words text-sm">
              {r.text}
              <Cut r={r} />
            </div>
          ) : null}
          <p className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
            <MessageSquare className="size-3" />
            turn ended{r.stop_reason ? `: ${r.stop_reason.replace(/_/g, " ")}` : ""}
          </p>
        </div>
      );
      break;
    case "limit":
      body = <p className="text-xs text-amber-600 dark:text-amber-500">{r.text}</p>;
      break;
    case "mode":
      body = <p className="text-xs text-muted-foreground">mode: {r.mode}</p>;
      break;
    case "cancel":
      body = (
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          <CircleSlash className="size-3.5" />
          stopped from the page
        </p>
      );
      break;
    default:
      return null;
  }
  return (
    <div className="flex items-start gap-3">
      <span className="w-14 shrink-0 pt-0.5 font-mono text-[11px] text-muted-foreground" title={formatDateTime(r.time)}>
        {time(r.time)}
      </span>
      <div className="min-w-0 flex-1">{body}</div>
    </div>
  );
}

/**
 * What the sandbox's agentos sessions did, as Studio's server recorded them
 * from the messages it relayed: prompts, tool calls with their arguments and
 * results, questions and answers. It is kept on the machine Studio runs on,
 * so it is here after the sandbox is gone.
 */
export function SandboxHistory({ sandbox }: { sandbox: string }) {
  const { data, error, isLoading } = useAgentLog(sandbox, true);
  if (error) return <p className="text-sm text-muted-foreground">No history: {error.message}</p>;
  if (isLoading || !data) return <p className="text-sm text-muted-foreground">reading…</p>;
  if (!data.enabled) return <p className="text-sm text-muted-foreground">This Studio keeps no agent log (it was started with --agent-log none).</p>;
  const sessions = sessionsOf(data.records);
  if (sessions.length === 0) return <p className="text-sm text-muted-foreground">No agent sessions recorded for this sandbox yet.</p>;
  return (
    <div className="flex flex-col gap-4">
      {data.truncated ? <p className="text-xs text-muted-foreground">(older records omitted)</p> : null}
      {sessions.map((s) => (
        <section key={s.id} className="flex flex-col gap-2 rounded-md border p-3">
          <header className="flex flex-wrap items-baseline gap-x-2 text-xs">
            <span className="text-sm font-medium">{s.agent}</span>
            <span className="text-muted-foreground">{formatDateTime((s.start ?? s.records[0] ?? s.end)?.time ?? "")}</span>
            {s.user ? <span className="text-muted-foreground">by {s.user}</span> : null}
            <span className="ml-auto text-muted-foreground">{ending(s)}</span>
          </header>
          {s.records.length === 0 ? <p className="text-xs text-muted-foreground">Nothing was asked.</p> : null}
          {s.records.map((r, i) => (
            <Row key={i} r={r} />
          ))}
        </section>
      ))}
    </div>
  );
}
