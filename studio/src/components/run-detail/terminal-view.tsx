"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { ArrowDownToLine, MessageSquare, Plug, Terminal, WrapText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Toggle } from "@/components/ui/toggle";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { CopyButton } from "@/components/common/copy-button";
import { EmptyState } from "@/components/common/empty-state";
import { AttachedTerminal } from "@/components/run-detail/attached-terminal";
import { useDaemon, useRunLogs } from "@/lib/api/queries";
import { useLogStream } from "@/hooks/use-log-stream";
import { useUi } from "@/lib/store";
import { parseAnsi, stripAnsi } from "@/lib/ansi";
import { formatClock } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { LogLine, Run } from "@/lib/types";

/**
 * The terminal.
 *
 * Read-only by default, and it says so: a run launched without a console has no
 * stdin — `-d` replaces `-i`/`-it` — so offering an input that silently went
 * nowhere would be worse than offering none.
 *
 * A run launched *with* a console does have stdin, and then this offers to
 * attach: the same live pty `sandbox-cli attach` connects to, rendered by a
 * real terminal emulator. That is a deliberate click rather than the default
 * view, for two reasons. It loads the largest dependency in the app, and it
 * takes the keyboard — every keystroke after it goes to the agent, Ctrl-C
 * included.
 */
export function TerminalView({ run }: { run: Run }) {
  const live = run.state === "running";
  const { data, isPending } = useRunLogs(run.id, run.state === "running");
  const { lines, streaming } = useLogStream(data, live);

  const follow = useUi((s) => s.terminalFollow);
  const setFollow = useUi((s) => s.setTerminalFollow);
  const wrap = useUi((s) => s.terminalWrap);
  const setWrap = useUi((s) => s.setTerminalWrap);
  const timestamps = useUi((s) => s.terminalTimestamps);
  const setTimestamps = useUi((s) => s.setTerminalTimestamps);

  const scrollRef = useRef<HTMLDivElement>(null);
  const [attached, setAttached] = useState(false);
  const { data: daemon } = useDaemon();

  /**
   * Whether this daemon will let anything drive a console.
   *
   * Typing at a running agent — and sizing its terminal, which is what makes it
   * paint — requires the daemon to have been started with `-token`, whatever
   * the rest of the server allows. `authRequired` is how /v1/health reports
   * that it has one.
   *
   * Read here so the Attach button is simply not offered when it cannot work.
   * Without this it opened a terminal that 403'd on resize and sat blank, which
   * is the worst of both: an action that looks available and an empty screen
   * with the reason three network requests away.
   */
  const consoleEnabled = daemon?.authRequired === true;
  const bottomRef = useRef<HTMLDivElement>(null);

  // The timestamps toggle is labelled with the current clock, which cannot be
  // rendered during the first pass: "use client" does not mean "not rendered on
  // the server", so the server wrote one minute into the HTML and the browser
  // hydrated with another. Filled in after mount; the placeholder keeps the
  // button the same width so nothing shifts when it arrives.
  const [clockLabel, setClockLabel] = useState("--:--");
  useEffect(() => {
    setClockLabel(formatClock(new Date().toISOString()).slice(0, 5));
  }, []);

  useEffect(() => {
    if (!follow) return;
    bottomRef.current?.scrollIntoView({ block: "end" });
  }, [lines.length, follow]);

  // Scrolling up is how you say "stop following"; a view that yanked you back to
  // the bottom mid-read would make the transcript unreadable.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    function onScroll() {
      const atBottom = el!.scrollHeight - el!.scrollTop - el!.clientHeight < 48;
      if (!atBottom && follow) setFollow(false);
    }
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => el.removeEventListener("scroll", onScroll);
  }, [follow, setFollow]);

  const plainText = useMemo(
    () => lines.map((l) => stripAnsi(l.text)).join("\n"),
    [lines],
  );

  // Both halves have to be true: the container was created with stdin, and it
  // is still running. A finished console run has a terminal to read and nothing
  // listening, which is what the log view above already shows.
  const canAttach = live && run.openStdin && consoleEnabled;

  // A run that *could* be attached to, on a daemon that will not allow it.
  // Worth telling apart from "this run has no console": the fix is a daemon
  // flag rather than relaunching the run.
  const consoleLockedOut = live && run.openStdin && !consoleEnabled;

  // An agent run with no stdin was started headless — `claude -p` and its
  // equivalents — so there is no Attach button *and* nothing will appear here
  // until it exits. Both are correct and both look like a broken screen, which
  // is why this says so rather than showing an empty box.
  const headless = Boolean(run.agent) && !run.openStdin;

  if (attached) {
    return <AttachedTerminal run={run} onDetach={() => setAttached(false)} />;
  }

  return (
    // `relative` anchors "Jump to latest" below. Without a positioned ancestor
    // an absolute child walks up to the sticky header and lands off-screen.
    <div className="surface-sheen relative overflow-hidden rounded-lg border bg-[#0c0c0f]">
      <div className="flex flex-wrap items-center gap-2 border-b border-white/5 px-3 py-1.5">
        <Terminal className="size-3.5 text-white/40" />
        <span className="font-mono text-[11px] tracking-wide text-white/40 uppercase">
          {run.agent ?? "guest"} · {run.workdir}
        </span>
        {streaming && (
          <span className="flex items-center gap-1.5 text-[11px] text-status-running">
            <span className="live-dot size-1.5 rounded-full bg-status-running" />
            streaming
          </span>
        )}

        <div className="ml-auto flex items-center gap-1">
          {consoleLockedOut && (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="cursor-default px-2 text-[11px] text-white/40">
                  console disabled
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                This run has a console, but the daemon was started without a
                token. Typing at a running agent — and sizing its terminal, which
                is what makes it draw — needs one. Restart with
                <code className="mx-1">-token</code> (or
                <code className="mx-1">$SANDBOX_STUDIO_TOKEN</code>) and attach.
              </TooltipContent>
            </Tooltip>
          )}
          {canAttach && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => setAttached(true)}
                  className="h-7 gap-1.5 px-2 text-[11px] text-white/70 hover:bg-white/10 hover:text-white"
                >
                  <Plug className="size-3.5" />
                  Attach
                </Button>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                Open the agent&apos;s live terminal. Everything you type goes to
                it — this is the same session `sandbox-cli attach` connects to.
              </TooltipContent>
            </Tooltip>
          )}
          <Tooltip>
            <TooltipTrigger asChild>
              <Toggle
                size="sm"
                pressed={timestamps}
                onPressedChange={setTimestamps}
                aria-label="Show timestamps"
                className="h-7 px-2 text-[11px] text-white/50 hover:bg-white/10 hover:text-white data-[state=on]:bg-white/10 data-[state=on]:text-white"
              >
                {clockLabel}
              </Toggle>
            </TooltipTrigger>
            <TooltipContent>Timestamps</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Toggle
                size="sm"
                pressed={wrap}
                onPressedChange={setWrap}
                aria-label="Wrap long lines"
                className="h-7 px-2 text-white/50 hover:bg-white/10 hover:text-white data-[state=on]:bg-white/10 data-[state=on]:text-white"
              >
                <WrapText className="size-3.5" />
              </Toggle>
            </TooltipTrigger>
            <TooltipContent>Wrap long lines</TooltipContent>
          </Tooltip>
          {live && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Toggle
                  size="sm"
                  pressed={follow}
                  onPressedChange={setFollow}
                  aria-label="Follow output"
                  className="h-7 px-2 text-white/50 hover:bg-white/10 hover:text-white data-[state=on]:bg-white/10 data-[state=on]:text-white"
                >
                  <ArrowDownToLine className="size-3.5" />
                </Toggle>
              </TooltipTrigger>
              <TooltipContent>Follow new output</TooltipContent>
            </Tooltip>
          )}
          <CopyButton
            value={plainText}
            label="Copy transcript"
            className="text-white/50 hover:bg-white/10 hover:text-white"
          />
        </div>
      </div>

      <div
        ref={scrollRef}
        className="scrollbar-thin h-[30rem] overflow-auto px-3 py-2.5 font-mono text-[13px] leading-[1.55]"
      >
        {isPending ? (
          <div className="space-y-2 py-2">
            {Array.from({ length: 10 }).map((_, i) => (
              <Skeleton key={i} className="h-3.5 bg-white/5" style={{ width: `${40 + i * 5}%` }} />
            ))}
          </div>
        ) : lines.length === 0 ? (
          <EmptyState
            icon={Terminal}
            title={headless ? "A headless run writes nothing until it ends" : "No output"}
            description={
              headless
                ? `${run.agent} was started in its headless mode, which produces one final answer and prints it on exit — so this stays empty while it works, however long that is. What it is saying right now is in the Console tab, read from its transcript.`
                : "The container has not written anything yet, or its output was never captured."
            }
            action={
              headless ? (
                <Button variant="outline" size="sm" asChild>
                  <Link href={`/runs/${run.id}?tab=console`}>
                    <MessageSquare className="size-4" />
                    Read the conversation
                  </Link>
                </Button>
              ) : undefined
            }
            className="border-0 text-white/60"
          />
        ) : (
          <>
            {lines.map((line) => (
              <Line key={line.seq} line={line} wrap={wrap} showTime={timestamps} />
            ))}
            {streaming && <span className="inline-block h-4 w-2 animate-pulse bg-white/60" />}
            <div ref={bottomRef} />
          </>
        )}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2 border-t border-white/5 px-3 py-1.5 text-[11px] text-white/35">
        <span>
          {lines.length} {lines.length === 1 ? "line" : "lines"}
          {run.state !== "running" && run.exitCode !== null && (
            <> · exited {run.exitCode}</>
          )}
        </span>
        {/* A detached run has neither -i nor -t, so there is nothing to type at. */}
        <span>
          {run.openStdin
            ? "Interactive session — attach a real terminal with `sandbox-cli attach` to type at it."
            : "Read-only: this run was started detached, so it is not listening on stdin."}
        </span>
      </div>

      {!follow && live && (
        <Button
          size="sm"
          className="absolute right-4 bottom-12 shadow-lg"
          onClick={() => setFollow(true)}
        >
          <ArrowDownToLine className="size-3.5" />
          Jump to latest
        </Button>
      )}
    </div>
  );
}

function Line({
  line,
  wrap,
  showTime,
}: {
  line: LogLine;
  wrap: boolean;
  showTime: boolean;
}) {
  const spans = useMemo(() => parseAnsi(line.text), [line.text]);
  return (
    <div className={cn("flex gap-2", wrap ? "whitespace-pre-wrap" : "whitespace-pre")}>
      {showTime && (
        <span className="shrink-0 tabular-nums text-white/25 select-none">
          {formatClock(line.ts)}
        </span>
      )}
      <span
        className={cn(
          "min-w-0",
          wrap ? "break-words" : "",
          // stderr is dimmed toward the warning hue rather than recoloured
          // outright: an agent's own colours are inside the line and must survive.
          line.stream === "stderr" ? "text-status-warning/90" : "text-zinc-300",
        )}
      >
        {spans.map((s, i) => (
          <span
            key={i}
            style={s.color ? { color: s.color } : undefined}
            className={cn(
              s.bold && "font-semibold",
              s.dim && "opacity-55",
              s.italic && "italic",
              s.underline && "underline",
            )}
          >
            {s.text}
          </span>
        ))}
      </span>
    </div>
  );
}
