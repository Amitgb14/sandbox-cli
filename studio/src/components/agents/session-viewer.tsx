"use client";

import { useState } from "react";
import { Bot, FileJson, MessagesSquare, User } from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyButton } from "@/components/common/copy-button";
import { EmptyState } from "@/components/common/empty-state";
import { useSessionRaw, useSessionTranscript } from "@/lib/api/queries";
import { formatBytesShort, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SessionSummary } from "@/lib/types";

/**
 * One conversation, read two ways.
 *
 * **Formatted** is sandbox-cli's own reading of the transcript, and it is a
 * reading rather than the file: the claude jsonl carries a dozen line kinds and
 * only some of them are turns. The 4.5 MB session this was built for holds 474
 * user lines, 775 assistant lines, 550 attachments, 112 mode records and 120
 * queue operations — the parser keeps the conversation and drops the machinery.
 *
 * **Raw** is the file. It exists because a parsed view is an interpretation, and
 * the only way to check an interpretation is to see what it was made from —
 * which is also the only way to see the line kinds the parser drops.
 *
 * A *user* turn here means a prompt somebody typed, never a tool result coming
 * back as a user message: they outnumber real prompts about thirty to one, and
 * counting them would make every conversation look like a monologue.
 */
export function SessionViewer({
  agent,
  session,
  onOpenChange,
}: {
  agent: string;
  session: SessionSummary | null;
  onOpenChange: (open: boolean) => void;
}) {
  const [tab, setTab] = useState("formatted");
  const id = session?.id ?? null;
  const { data, isPending } = useSessionTranscript(agent, id);
  // Only when asked for: the raw read is the whole file, and fetching half a
  // megabyte to render a tab nobody opened is the kind of thing that makes a
  // dialog feel slow.
  const raw = useSessionRaw(agent, id, tab === "raw");

  const meta = data?.session ?? session;

  return (
    <Dialog open={!!session} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[88vh] sm:max-w-4xl">
        <DialogHeader>
          <DialogTitle className="flex flex-wrap items-center gap-2 text-base">
            <MessagesSquare className="size-4" />
            <span className="truncate">{meta?.title || "Untitled conversation"}</span>
            {meta?.store && (
              // Which store, because it decides what this conversation *is*: a
              // container wrote the sandbox one, you wrote the host one.
              <Badge variant="outline" className="text-[10px]">
                {meta.store === "sandbox" ? "sandbox agent" : "your own history"}
              </Badge>
            )}
            {meta?.partial && (
              <Badge variant="outline" className="text-[10px]">
                partial — no verified reader
              </Badge>
            )}
          </DialogTitle>
          <DialogDescription asChild>
            <div className="space-y-1 text-xs">
              <p className="font-mono break-all">{meta?.path}</p>
              <p>
                {meta?.project && (
                  <>
                    cwd <code className="font-mono">{meta.project}</code> ·{" "}
                  </>
                )}
                {meta?.turns ?? 0} prompts · {formatBytesShort(meta?.size ?? 0)} ·{" "}
                {meta?.modified ? formatRelative(meta.modified) : "—"}
              </p>
            </div>
          </DialogDescription>
        </DialogHeader>

        <Tabs value={tab} onValueChange={setTab}>
          <TabsList>
            <TabsTrigger value="formatted" className="gap-1.5">
              <MessagesSquare className="size-3.5" />
              Conversation
            </TabsTrigger>
            <TabsTrigger value="raw" className="gap-1.5">
              <FileJson className="size-3.5" />
              Original file
            </TabsTrigger>
          </TabsList>

          <TabsContent value="formatted">
            {isPending ? (
              <div className="space-y-3">
                {Array.from({ length: 4 }, (_, i) => (
                  <Skeleton key={i} className="h-16 w-full" />
                ))}
              </div>
            ) : !data || data.messages.length === 0 ? (
              <EmptyState
                icon={MessagesSquare}
                title="Nothing to show"
                description="This transcript holds no turns sandbox-cli recognises. The original file is still readable beside this tab."
              />
            ) : (
              <div className="max-h-[56vh] space-y-3 overflow-y-auto pr-1">
                {data.messages.map((m, i) => (
                  <div
                    key={i}
                    className={cn(
                      "rounded-lg border p-3",
                      m.role === "user" ? "bg-muted/40" : "bg-background",
                    )}
                  >
                    <div className="mb-1.5 flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      {m.role === "user" ? (
                        <User className="size-3" />
                      ) : (
                        <Bot className="size-3" />
                      )}
                      <span className="font-medium capitalize">{m.role}</span>
                      {m.at && <span>· {formatRelative(m.at)}</span>}
                    </div>
                    <p className="whitespace-pre-wrap break-words text-sm">{m.text}</p>
                  </div>
                ))}
              </div>
            )}
          </TabsContent>

          <TabsContent value="raw">
            {raw.isPending ? (
              <Skeleton className="h-72 w-full" />
            ) : raw.isError ? (
              <p className="text-sm text-destructive">
                {raw.error instanceof Error ? raw.error.message : String(raw.error)}
              </p>
            ) : (
              <div className="space-y-2">
                <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                  <span>{formatBytesShort(raw.data?.size ?? 0)} on disk</span>
                  {raw.data?.truncated && (
                    // Said out loud: showing the last part of a file as though it
                    // were the file is a claim nobody checked.
                    <Badge variant="outline" className="text-[10px]">
                      showing the last {formatBytesShort(raw.data.content.length)}
                    </Badge>
                  )}
                  <CopyButton value={raw.data?.session.path ?? ""} label="Copy path" />
                </div>
                <pre className="max-h-[52vh] overflow-auto rounded-md bg-muted/40 p-3 font-mono text-[11px] leading-relaxed">
                  {raw.data?.content}
                </pre>
              </div>
            )}
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
