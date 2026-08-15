"use client";

import { useMemo, useState } from "react";
import { MessagesSquare, Search } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/common/empty-state";
import { SessionViewer } from "@/components/agents/session-viewer";
import { useAgentSessions } from "@/lib/api/queries";
import { formatBytesShort, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SessionSummary } from "@/lib/types";

/**
 * Every conversation this agent has on this machine, readable.
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
 */
export function ConversationsPanel({ agent }: { agent: string }) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState<SessionSummary | null>(null);
  // A high bound rather than the picker's fifty: this list is ordered by
  // recency, so a small cap hides exactly the older conversation somebody came
  // here to find.
  const { data, isPending } = useAgentSessions(agent, { scope: "all", limit: 400 });

  const sessions = useMemo(() => {
    const all = data ?? [];
    if (!query.trim()) return all;
    const q = query.toLowerCase();
    // Id included, because that is what a path or a `--resume` line gives you,
    // and pasting it in is the fastest way to find one conversation.
    return all.filter((s) =>
      `${s.title ?? ""} ${s.project ?? ""} ${s.id}`.toLowerCase().includes(q),
    );
  }, [data, query]);

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
          <span className="relative">
            <Search className="absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder="Filter by title, project or id"
              className="h-8 w-64 pl-7 text-xs"
            />
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
            title={query ? "No conversation matches" : "No conversations yet"}
            description={
              query
                ? "Titles, working directories and ids are searched — an id from a --resume line will find its conversation."
                : "Run this agent once and its transcript appears here, alongside anything Claude Code has written on this machine."
            }
          />
        )}
        <ul className="divide-y">
          {sessions.map((s) => (
            <li key={s.id}>
              <button
                onClick={() => setOpen(s)}
                className="flex w-full flex-wrap items-center gap-2 px-4 py-2.5 text-left hover:bg-accent"
              >
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm">
                    {s.title || <span className="text-muted-foreground">Untitled</span>}
                  </span>
                  <span className="block truncate font-mono text-[10px] text-muted-foreground">
                    {s.project ?? "—"} · {s.id.slice(0, 8)}
                  </span>
                </span>
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
            </li>
          ))}
        </ul>
      </CardContent>

      <SessionViewer agent={agent} session={open} onOpenChange={(o) => !o && setOpen(null)} />
    </Card>
  );
}
