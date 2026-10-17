"use client";

import { useEffect, useRef, useState } from "react";
import { Bot, ChevronDown, ChevronRight, CircleCheck, CircleDashed, CircleX, Loader2, Play, Send, Square, Wrench } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { AgentMarkdown } from "@/components/sandbox/agent-markdown";
import { toast } from "sonner";
import { answerPermission, cancelTurn, clearSession, endSession, sendPrompt, setMode, setOption, startSession, useAcp } from "@/lib/acp";
import type { AcpEntry, AcpSession, AcpToolCall } from "@/lib/acp";
import type { AgentOS } from "@/lib/types";
import { cn } from "@/lib/utils";

function ToolCall({ call }: { call: AcpToolCall }) {
  const [open, setOpen] = useState(false);
  const icon =
    call.status === "completed" ? (
      <CircleCheck className="size-3.5 text-emerald-500" />
    ) : call.status === "failed" ? (
      <CircleX className="size-3.5 text-destructive" />
    ) : call.status === "in_progress" ? (
      <Loader2 className="size-3.5 animate-spin text-muted-foreground" />
    ) : (
      <CircleDashed className="size-3.5 text-muted-foreground" />
    );
  const more = call.text.length > 0 || call.images.length > 0;
  return (
    <div className="rounded-md border bg-muted/30 text-xs">
      <button
        type="button"
        className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left disabled:cursor-default"
        disabled={!more}
        onClick={() => setOpen((o) => !o)}
      >
        {more ? open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" /> : <Wrench className="size-3.5 text-muted-foreground" />}
        <span className="min-w-0 flex-1 truncate font-mono">{call.title}</span>
        {icon}
      </button>
      {open && (
        <div className="flex flex-col gap-2 border-t px-2.5 py-2">
          {call.text.map((t, i) => (
            <pre key={i} className="max-h-60 overflow-auto whitespace-pre-wrap break-words font-mono text-[11px] text-muted-foreground">
              {t}
            </pre>
          ))}
          {call.images.map((src, i) => (
            // A data URL of a PNG, JPEG or WebP the agent took (lib/acp.ts checks which).
            // eslint-disable-next-line @next/next/no-img-element
            <img key={i} src={src} alt="what the agent saw" className="max-h-80 w-auto rounded border" />
          ))}
        </div>
      )}
    </div>
  );
}

function Entry({ e }: { e: AcpEntry }) {
  switch (e.kind) {
    case "user":
      return <div className="ml-auto max-w-[85%] whitespace-pre-wrap rounded-lg bg-primary px-3 py-2 text-sm text-primary-foreground">{e.text}</div>;
    case "agent":
      return <AgentMarkdown text={e.text} />;
    case "thought":
      return <div className="whitespace-pre-wrap break-words border-l-2 pl-3 text-xs italic text-muted-foreground">{e.text}</div>;
    case "tool":
      return <ToolCall call={e.call} />;
    case "note":
      return <p className="text-xs text-muted-foreground">{e.text}</p>;
  }
}

function Plan({ s }: { s: AcpSession }) {
  if (s.plan.length === 0) return null;
  return (
    <div className="rounded-md border px-3 py-2">
      <p className="mb-1 text-xs font-medium text-muted-foreground">Plan</p>
      <ul className="flex flex-col gap-0.5 text-xs">
        {s.plan.map((p, i) => (
          <li key={i} className={cn("flex items-start gap-2", p.status === "completed" && "text-muted-foreground line-through")}>
            {p.status === "completed" ? (
              <CircleCheck className="mt-0.5 size-3 shrink-0 text-emerald-500" />
            ) : p.status === "in_progress" ? (
              <Loader2 className="mt-0.5 size-3 shrink-0 animate-spin" />
            ) : (
              <CircleDashed className="mt-0.5 size-3 shrink-0 text-muted-foreground" />
            )}
            {p.content}
          </li>
        ))}
      </ul>
    </div>
  );
}

/**
 * An agentos sandbox's agent, over the Agent Client Protocol: a prompt, what
 * the agent plans and does, and its questions when its mode asks first. The
 * agent works on the sandbox's desktop, which the Desktop tab shows.
 */
export function SandboxAgent({ sandbox, image, manifest, canStart }: { sandbox: string; image: string; manifest: AgentOS; canStart: boolean }) {
  const s = useAcp((st) => st.sessions[sandbox]);
  const [agent, setAgent] = useState(manifest.agents[0]?.id ?? "");
  const [text, setText] = useState("");
  const [showLog, setShowLog] = useState(false);
  const bottom = useRef<HTMLDivElement>(null);
  const live = s?.status === "connecting" || s?.status === "ready";

  useEffect(() => {
    bottom.current?.scrollIntoView({ block: "nearest" });
  }, [s?.entries.length, s?.permissions.length, s?.busy]);

  const submit = () => {
    const t = text.trim();
    if (!t || !s || s.status !== "ready" || s.busy) return;
    setText("");
    void sendPrompt(sandbox, t);
  };

  const agentName = (id: string) => manifest.agents.find((a) => a.id === id)?.name ?? id;

  if (!s) {
    return (
      <div className="flex max-w-xl flex-col gap-3">
        <div className="flex items-center gap-2">
          <Bot className="size-4 text-muted-foreground" />
          <p className="text-sm">
            This sandbox has an agent that works its desktop. Give it a task here, and watch it on the <span className="font-medium">Desktop</span> tab.
          </p>
        </div>
        <div className="flex items-center gap-2">
          {manifest.agents.length > 1 && (
            <Select value={agent} onValueChange={setAgent}>
              <SelectTrigger aria-label="Agent" className="h-8 w-44 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {manifest.agents.map((a) => (
                  <SelectItem key={a.id} value={a.id} className="text-xs">
                    {a.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Button size="sm" disabled={!canStart || !agent} onClick={() => startSession(sandbox, agent)}>
            <Play className="size-3.5" />
            Start {agentName(agent)}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          The agent uses its login in the sandbox. Where the gateway keeps yours, Start copies it in, as a console does; a
          sandbox started with{" "}
          <code className="break-all font-mono">
            sandbox-cli agent {manifest.agents.find((a) => a.id === agent)?.login || agent} --image {image} --keep -- --version
          </code>{" "}
          has it too; otherwise log in from the Terminal tab. Files the agent makes for you go to{" "}
          <code className="font-mono">{manifest.outputDir || "its home"}</code>.
        </p>
      </div>
    );
  }

  return (
    <div className="flex h-[32rem] flex-col gap-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm font-medium">{s.agentName ?? agentName(s.agent)}</span>
        <span className="text-xs text-muted-foreground">
          {s.status === "connecting" ? "starting…" : s.status === "ready" ? (s.busy ? "working" : "ready") : s.status === "failed" ? "failed" : "ended"}
        </span>
        <div className="ml-auto flex items-center gap-1.5">
          {s.status === "ready" &&
            s.options.map((o) => (
              <Select
                key={o.id}
                value={o.value}
                onValueChange={(v) => void setOption(sandbox, o.id, v).catch((e: Error) => toast.error(`${o.name}: ${e.message}`))}
              >
                <SelectTrigger aria-label={o.name} className="h-7 w-44 text-xs" title={o.description ?? o.name}>
                  <SelectValue placeholder={o.name} />
                </SelectTrigger>
                <SelectContent>
                  {o.choices.map((c) => (
                    <SelectItem key={c.id} value={c.id} className="text-xs" title={c.description}>
                      {c.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ))}
          {/* An agent that sends its mode as a config option (OpenCode) has it above. */}
          {s.modes.length > 0 && s.status === "ready" && !s.options.some((o) => o.category === "mode") && (
            <Select value={s.mode} onValueChange={(m) => void setMode(sandbox, m).catch((e: Error) => toast.error(`Mode: ${e.message}`))}>
              <SelectTrigger aria-label="Mode" className="h-7 w-44 text-xs" title="Whether the agent asks before it acts">
                <SelectValue placeholder="Mode" />
              </SelectTrigger>
              <SelectContent>
                {s.modes.map((m) => (
                  <SelectItem key={m.id} value={m.id} className="text-xs">
                    {m.name}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          {live ? (
            <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => endSession(sandbox)}>
              End session
            </Button>
          ) : (
            <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => clearSession(sandbox)}>
              New session
            </Button>
          )}
        </div>
      </div>

      {s.error && <p className="text-xs text-destructive">{s.error}</p>}
      <Plan s={s} />

      <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-auto rounded-md border p-3" aria-live="polite">
        {s.entries.length === 0 && s.status === "ready" && <p className="text-xs text-muted-foreground">Say what you want done: “open example.com and save a screenshot”.</p>}
        {s.entries.map((e, i) => (
          <Entry key={i} e={e} />
        ))}
        {s.permissions.map((p) => (
          <div key={String(p.rpcId)} className="flex flex-col gap-2 rounded-md border border-amber-500/50 bg-amber-500/5 p-2.5">
            <p className="text-xs">
              The agent asks to run <span className="font-mono">{p.title}</span>
            </p>
            <div className="flex flex-wrap gap-1.5">
              {p.options.map((o) => (
                <Button
                  key={o.id}
                  size="sm"
                  variant={o.kind.startsWith("allow") ? "default" : "outline"}
                  className="h-7 text-xs"
                  onClick={() => answerPermission(sandbox, p.rpcId, o.id)}
                >
                  {o.name}
                </Button>
              ))}
            </div>
          </div>
        ))}
        {s.busy && s.permissions.length === 0 && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
        <div ref={bottom} />
      </div>

      <div className="flex items-end gap-2">
        <Textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              submit();
            }
          }}
          placeholder={s.status === "ready" ? "A task for the agent" : ""}
          disabled={s.status !== "ready"}
          aria-label="Prompt"
          className="min-h-[2.5rem] flex-1 resize-none text-sm"
          rows={2}
        />
        {s.busy ? (
          <Button size="sm" variant="outline" onClick={() => cancelTurn(sandbox)}>
            <Square className="size-3.5" />
            Stop
          </Button>
        ) : (
          <Button size="sm" disabled={s.status !== "ready" || !text.trim()} onClick={submit}>
            <Send className="size-3.5" />
            Send
          </Button>
        )}
      </div>

      {s.log.length > 0 && (
        <div className="text-xs">
          <button type="button" className="flex items-center gap-1 text-muted-foreground" onClick={() => setShowLog((v) => !v)}>
            {showLog ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            Agent log ({s.log.length})
          </button>
          {showLog && <pre className="mt-1 max-h-40 overflow-auto whitespace-pre-wrap rounded border bg-muted/30 p-2 font-mono text-[11px]">{s.log.join("\n")}</pre>}
        </div>
      )}
    </div>
  );
}
