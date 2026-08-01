"use client";

import { useEffect, useRef, useState } from "react";
import { KeyRound, MessageSquare, SendHorizontal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Textarea } from "@/components/ui/textarea";
import { EmptyState } from "@/components/common/empty-state";
import { setApiToken } from "@/lib/api/client";
import { ApiError } from "@/lib/api/client";
import { useConversation, useSendConsoleInput } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";
import type { Run } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * The conversation with a running agent, and the box that answers it.
 *
 * Why a conversation and not a terminal: a console run starts the agent's own
 * full-screen UI, so its output is a stream of repaints. Rendering that needs a
 * terminal emulator, and reading text out of it *without* one produces
 * plausible nonsense. The transcript is the same exchange as data — which is
 * also the half worth reading, since what the agent *did* is already a diff and
 * a set of commits, and what it *said* is the part that can contain a question.
 *
 * What this screen deliberately does not do is pretend a run can be typed at
 * when it cannot. `writable` comes from the daemon, which knows both halves
 * (the container is running, and it was created with stdin); a reply box over a
 * headless run would be a button whose only outcome is a 409.
 */
export function ConsoleView({ run }: { run: Run }) {
  const live = run.state === "running";
  const { data, isPending, error } = useConversation(run.id, live);
  const send = useSendConsoleInput(run.id);
  const [draft, setDraft] = useState("");
  const endRef = useRef<HTMLDivElement>(null);

  const messages = data?.messages ?? [];

  // Follow the conversation as it grows, the way a terminal does. Keyed on the
  // count rather than the array so a poll that returned the same turns does not
  // yank the view away from someone reading further up.
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages.length]);

  // 403 means the daemon has a token and this browser has not got it. That is a
  // fixable state rather than an error, so it gets a field instead of a message.
  if (error instanceof ApiError && error.status === 403) {
    return <TokenPrompt />;
  }

  if (isPending) {
    return <Skeleton className="h-80 w-full rounded-lg" />;
  }

  return (
    <div className="space-y-3">
      <Card>
        <CardContent className="max-h-[60vh] space-y-4 overflow-y-auto pt-5">
          {messages.length === 0 ? (
            <EmptyState
              icon={MessageSquare}
              title="Nothing said yet"
              description={
                live
                  ? "The agent has not written its first turn. A transcript appears once it answers, which for a fresh session is a few seconds."
                  : "No transcript was found for this run. It is correlated by agent and time window, and reporting none beats showing you another run's conversation."
              }
            />
          ) : (
            messages.map((m, i) => (
              <div
                key={`${m.at ?? i}-${i}`}
                className={cn(
                  "space-y-1",
                  m.role === "user" && "rounded-md bg-muted/50 p-3",
                )}
              >
                <div className="flex items-center gap-2 text-xs text-muted-foreground">
                  <span className="font-medium">
                    {m.role === "user" ? "you" : (run.agent ?? "agent")}
                  </span>
                  {m.at && <span>{formatRelative(m.at)}</span>}
                </div>
                {/* Rendered as text, never as markup: this is written by an
                    agent working in a repository whose contents it does not
                    control either. whitespace-pre-wrap keeps the line breaks
                    that make a numbered question readable. */}
                <p className="whitespace-pre-wrap text-sm leading-relaxed">
                  {m.text}
                </p>
              </div>
            ))
          )}
          <div ref={endRef} />
        </CardContent>
      </Card>

      {data?.writable ? (
        <Card>
          <CardContent className="space-y-2 pt-5">
            <Textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                // Enter sends, Shift-Enter breaks the line — the convention of
                // every chat box, and the opposite would make a two-line answer
                // impossible to type.
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  submit();
                }
              }}
              placeholder="Answer the agent…"
              rows={3}
              disabled={send.isPending}
            />
            <div className="flex items-center gap-2">
              <p className="text-xs text-muted-foreground">
                Goes to the container&apos;s stdin. The agent has to read it and
                think, so its reply appears on the next poll rather than at once.
              </p>
              <Button
                size="sm"
                className="ml-auto"
                disabled={!draft.trim() || send.isPending}
                onClick={submit}
              >
                <SendHorizontal className="size-4" />
                Send
              </Button>
            </div>
            {send.error && (
              <p className="text-xs text-destructive">
                {send.error instanceof Error
                  ? send.error.message
                  : "Could not deliver that."}
              </p>
            )}
          </CardContent>
        </Card>
      ) : (
        <p className="px-1 text-xs text-muted-foreground">
          {live
            ? "This run has no console — it was launched without one, so the container was created with no stdin and cannot be typed at. Tick “Keep a console I can attach to” when launching."
            : "The run has finished. Its conversation is kept; there is nothing listening to answer."}
        </p>
      )}
    </div>
  );

  function submit() {
    const text = draft.trim();
    if (!text) return;
    // Cleared optimistically: the daemon returns 204 with nothing to render, and
    // leaving the text in the box makes it look like it was not sent.
    setDraft("");
    send.mutate(
      { data: text },
      { onError: () => setDraft(text) },
    );
  }
}

/**
 * The console is the one endpoint that requires a token even when the rest of
 * the daemon does not, so this is where somebody first meets that rule. It is
 * shown as a field rather than an error because the fix is one paste.
 */
function TokenPrompt() {
  const [value, setValue] = useState("");
  return (
    <Card>
      <CardContent className="space-y-3 pt-5">
        <div className="flex items-center gap-2">
          <KeyRound className="size-4 text-muted-foreground" />
          <h3 className="text-sm font-medium">This console needs a token</h3>
        </div>
        <p className="text-sm text-muted-foreground">
          Typing at a <em>running</em> agent requires the daemon&apos;s bearer
          token, whatever the rest of the server is doing. Everything else here
          is read-only or launches a container you could have launched anyway; a
          keyboard on a live session holding your workspace is not something to
          leave open because a flag was forgotten.
        </p>
        <p className="text-sm text-muted-foreground">
          Start the daemon with <code>-token</code> (or{" "}
          <code>$SANDBOX_STUDIO_TOKEN</code>) and paste the same value here. It
          is kept in this browser only.
        </p>
        <div className="flex gap-2">
          <Input
            type="password"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="bearer token"
            className="font-mono"
          />
          <Button
            size="sm"
            disabled={!value.trim()}
            onClick={() => {
              setApiToken(value.trim());
              // A full reload rather than a refetch: the token is read at
              // request time by every hook, and the simplest way to be sure
              // nothing is holding a 403 is to start again.
              window.location.reload();
            }}
          >
            Save
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
