"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { ChevronDown, MessagesSquare, Search } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Label } from "@/components/ui/label";
import { EmptyState } from "@/components/common/empty-state";
import { SessionViewer } from "@/components/agents/session-viewer";
import { useAgentSessions, useAgents, useProjects } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { formatBytesShort, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SessionSummary } from "@/lib/types";

/**
 * Every conversation an agent has on this machine, readable — with a picker,
 * because there is now more than one agent whose transcripts sandbox-cli can
 * actually read. It offers the agents whose store has been *verified* on this
 * machine; an untracked store is not an empty one, and an agent that could only
 * ever answer "nothing here" is worse in a picker than absent from it.
 *
 * Two stores, and the panel says which each row came from rather than merging
 * them: the **sandbox** store is the agent HOME containers get, so those were
 * written by runs; the **host** store is your own `~/.claude` history, written
 * by Claude Code on your machine. They are different things that look identical
 * in a list, and the one field that separates them at a glance is the working
 * directory — a container's is always `/workspace`.
 *
 * Only the sandbox ones can be resumed here, which is why the resume picker on
 * Launch asks a narrower question. Reading is wider on purpose: a conversation
 * is worth looking at whether or not this daemon could reopen it.
 *
 * **Continue with ▾** is the row action, and it asks the only question a reader
 * of a conversation actually has: *who works on this next?* Picking the agent
 * that held it reopens the conversation; picking another starts that one with a
 * briefing about it. One gesture, two mechanisms, and the menu says which each
 * row will get rather than hiding the difference — a resume carries the
 * conversation, a briefing carries evidence about it.
 *
 * Every row can be handed over, including the **host** store: your own
 * ~/.claude history is exactly what "my claude conversation, run it via codex"
 * means. Only *resume* is narrower, because reopening a host session would mean
 * mounting the host's history into a container that was not asked to have it —
 * and because gemini and droid have no resume argv at all, which is what
 * `canResume` reports.
 */
export function ConversationsPanel({ defaultAgent = "claude" }: { defaultAgent?: string }) {
  // Which agent's conversations. Owned here rather than by the page, because the
  // picker and the list are one control: everything else on this card — the
  // resume offers, the hint about agents that cannot reopen a session, the
  // handoff menu's "reopen" versus "brief and start" — is an answer about the
  // agent selected.
  const [agent, setAgent] = useState(defaultAgent);
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<SessionSummary | null>(null);
  // Off by default: with a repository picked, the question is "what happened
  // here", and a list padded with conversations that may belong to anything is
  // the same noise the repository scope exists to remove.
  const [showUnattributed, setShowUnattributed] = useState(false);
  const repoFilter = useUi((s) => s.repoFilter);
  const { data: projects } = useProjects();
  const { data: agents } = useAgents();
  // Absent while the daemon has not answered, and absent means no offer: an
  // agent whose CLI cannot reopen a session by id has nothing to continue, and
  // guessing yes here would put a button in front of a refusal.
  const canResume = agents?.find((a) => a.name === agent)?.canResume ?? false;
  // Only agents with a verified headless argv: those are the ones POST /runs
  // will start, and the daemon refuses the rest. The conversation's own agent
  // stays in the list even when it cannot resume — a briefing from itself is
  // the only way to carry a gemini conversation on.
  const targets = (agents ?? []).filter((a) => a.headlessVerified);
  // Agents whose store this daemon has actually *found*. `untracked` is not a
  // synonym for empty — it means nothing was ever confirmed on this machine —
  // so offering one would put an agent in the picker that can only ever answer
  // "no conversations", which reads as a missing feature rather than a missing
  // store. The selected agent stays listed even when it is not, so a picker
  // never renders with nothing selected.
  const readable = (agents ?? []).filter(
    (a) => a.contextStore === "verified" || a.name === agent,
  );
  const repoName = projects?.find((p) => p.id === repoFilter)?.name;
  // A high bound rather than the picker's fifty: this list is ordered by
  // recency, so a small cap hides exactly the older conversation somebody came
  // here to find.
  const { data, isPending } = useAgentSessions(agent, { scope: "all", limit: 400 });

  const sessions = useMemo(() => {
    let all = data ?? [];
    // Scoped to the repository the app is on, when it is on one. A conversation
    // the daemon could not attribute is a separate decision — see the toggle:
    // hiding it silently would lose the pooled sandbox sessions entirely, and
    // showing it always would put another project's history under this one's
    // name.
    if (repoFilter) {
      all = all.filter((s) => s.repoId === repoFilter || (showUnattributed && !s.repoId));
    } else if (!showUnattributed) {
      all = all.filter((s) => !!s.repoId);
    }
    if (!query.trim()) return all;
    const q = query.toLowerCase();
    // Id included, because that is what a path or a `--resume` line gives you,
    // and pasting it in is the fastest way to find one conversation.
    return all.filter((s) =>
      `${s.title ?? ""} ${s.project ?? ""} ${s.id}`.toLowerCase().includes(q),
    );
  }, [data, query, repoFilter, showUnattributed]);

  const unattributed = (data ?? []).filter((s) => !s.repoId).length;

  return (
    <Card>
      <CardHeader className="pb-3">
        <CardTitle className="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span className="flex items-center gap-2">
            <MessagesSquare className="size-4" />
            Conversations
            {data && (
              <span className="text-xs font-normal text-muted-foreground">
                {sessions.length} of {data.length}
              </span>
            )}
          </span>
          <span className="flex items-center gap-3">
            {unattributed > 0 && (
              <span className="flex items-center gap-1.5">
                <Switch
                  id="show-unattributed"
                  checked={showUnattributed}
                  onCheckedChange={setShowUnattributed}
                />
                <Label htmlFor="show-unattributed" className="text-[11px] font-normal">
                  {/* Named for what they are rather than "other": a pooled
                      sandbox session records only /workspace, so nothing on disk
                      says which project it was — that is why it is not simply
                      filed elsewhere. */}
                  Show {unattributed} not tied to a repository
                </Label>
              </span>
            )}
          {readable.length > 1 && (
            <Select value={agent} onValueChange={setAgent}>
              <SelectTrigger size="sm" className="h-8 w-36 text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {readable.map((a) => (
                  <SelectItem key={a.name} value={a.name} className="text-xs">
                    {a.label}
                    {a.sessions ? (
                      <span className="ml-1 text-muted-foreground">{a.sessions}</span>
                    ) : null}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <span className="relative">
            <Search className="absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter by title, project or id"
              className="h-8 w-64 pl-7 text-xs"
            />
          </span>
          </span>
        </CardTitle>
      </CardHeader>
      <CardContent className="p-0">
        {isPending && (
          <div className="space-y-2 p-4">
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        )}
        {!isPending && sessions.length === 0 && (
          <EmptyState
            icon={MessagesSquare}
            className="border-0"
            title={
              query
                ? "No conversation matches"
                : repoName
                  ? `No conversations for ${repoName}`
                  : "No conversations yet"
            }
            description={
              query
                ? "Titles, working directories and ids are searched — an id from a --resume line will find its conversation."
                : repoName
                  ? `This agent has no conversation recorded against ${repoName}. A sandbox session pooled in the shared bucket records only /workspace, so it cannot be attributed to a repository — the toggle above shows those.`
                  : "Run this agent once and its transcript appears here, alongside anything Claude Code has written on this machine."
            }
          />
        )}
        {!isPending && sessions.length > 0 && !canResume && (
          // Said once, above the list, rather than as a disabled button on every
          // row: the fact is about the agent, not about any one conversation.
          <p className="border-b px-4 py-2 text-[11px] text-muted-foreground">
            {agent} has no way to reopen a conversation by id, so Continue starts a
            fresh run with a briefing about it rather than resuming — including when
            the agent picked is {agent} itself.
          </p>
        )}
        <ul className="divide-y">
          {sessions.map((s) => (
            <li key={s.id} className="flex items-center">
              <button
                onClick={() => setOpen(s)}
                className="flex min-w-0 flex-1 flex-wrap items-center gap-2 px-4 py-2.5 text-left hover:bg-accent"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm">
                    {s.title || <span className="text-muted-foreground">Untitled</span>}
                  </span>
                  <span className="block truncate font-mono text-[10px] text-muted-foreground">
                    {s.project ?? "—"} · {s.id.slice(0, 8)}
                  </span>
                </span>
                {/* Who wrote it, then where it lives. Constant per panel today —
                    this one is claude's, since that is the only transcript
                    sandbox-cli has a verified reader for — but it stopped being
                    implied the moment a row could hand the conversation to a
                    *different* agent: the menu offers codex and gemini, so which
                    agent held it is a question the row now has to answer itself. */}
                <Badge variant="secondary" className="shrink-0 font-mono text-[10px]">
                  {agent}
                </Badge>
                <Badge
                  variant="outline"
                  className={cn("shrink-0 text-[10px]", s.store === "host" && "opacity-70")}
                >
                  {s.store === "sandbox" ? "sandbox" : "host"}
                </Badge>
                <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">
                  {s.partial ? "?" : s.turns} prompts · {formatBytesShort(s.size ?? 0)} ·{" "}
                  {formatRelative(s.modified)}
                </span>
                <Button asChild variant="ghost" size="sm" className="h-6 shrink-0 px-2 text-[11px]">
                  <span>Open</span>
                </Button>
              </button>
              {/* Outside the row button, because a button inside a button is not
                  a thing the browser will honour — and this is a navigation, not
                  a second way to open the viewer.

                  The link carries the repository as well as the session: a
                  conversation is about files, so reopening it against another
                  tree is a silent wrong answer, and `repoId` is the only thing
                  on the row that says which one it was. A session with no
                  attribution is deliberately still offered — the Launch form
                  asks for the repository in that case rather than picking. */}
              <ContinueMenu agent={agent} session={s} targets={targets} />
            </li>
          ))}
        </ul>
      </CardContent>

      <SessionViewer agent={agent} session={open} onOpenChange={(o) => !o && setOpen(null)} />
    </Card>
  );
}

/**
 * Who works on this conversation next.
 *
 * The two mechanisms are named rather than merged: **reopen** is the agent's own
 * resume, which continues the conversation; **brief and start** is a new
 * conversation carrying `internal/handoff`'s export. A menu that said only
 * "continue" for both would be claiming the second one resumes something, which
 * is exactly what the target must not be told.
 *
 * The repository rides along whenever the conversation could be attributed to
 * one, because a conversation is about files and reopening it against another
 * tree is a silent wrong answer. When it could not, the link omits it and the
 * Launch form asks.
 */
function ContinueMenu({
  agent,
  session,
  targets,
}: {
  agent: string;
  session: SessionSummary;
  targets: { name: string; label: string }[];
}) {
  if (targets.length === 0) return null;
  const repo = session.repoId ? { repo: session.repoId } : {};

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="mr-4 h-6 shrink-0 gap-1 px-2 text-[11px]">
          Continue with
          <ChevronDown className="size-3" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="text-[11px] font-normal text-muted-foreground">
          {session.title || "This conversation"}
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        {targets.map((t) => {
          // Resumable is the daemon's own answer and already accounts for both
          // facts — the sandbox-owned store, and an agent that can reopen a
          // session by id. Anything else is a briefing, including the same agent
          // when it has no resume argv.
          const reopens = t.name === agent && session.resumable;
          return (
            <DropdownMenuItem key={t.name} asChild>
              <Link
                href={{
                  pathname: "/launch",
                  query: reopens
                    ? { agent, resume: session.id, ...repo }
                    : {
                        agent: t.name,
                        handoffAgent: agent,
                        handoffSession: session.id,
                        ...repo,
                      },
                }}
                className="flex items-center justify-between gap-2"
              >
                <span>{t.label}</span>
                <span className="text-[10px] text-muted-foreground">
                  {reopens ? "reopen" : "brief and start"}
                </span>
              </Link>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
