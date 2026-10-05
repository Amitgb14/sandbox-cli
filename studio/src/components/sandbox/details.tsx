"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { ChevronDown, ChevronUp, ExternalLink, Maximize2, Minimize2, SquareTerminal, Trash2, X } from "lucide-react";
import { CopyButton } from "@/components/common/copy-button";
import { SandboxEvents } from "@/components/sandbox/events";
import { SandboxFiles } from "@/components/sandbox/files";
import { ProcessOutput } from "@/components/sandbox/output";
import { SandboxOverview } from "@/components/sandbox/overview";
import { StateDot } from "@/components/sandbox/state-dot";
import { SandboxTerminal } from "@/components/sandbox/terminal";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useMinWidth } from "@/hooks/use-min-width";
import { useCan } from "@/lib/caller";
import { useInfo, useKill, useOpenShell, useProcesses, useSandbox, useSnapshot, useSuspend } from "@/lib/api/queries";
import { cn } from "@/lib/utils";

function IconButton({ label, onClick, disabled, children }: { label: string; onClick?: () => void; disabled?: boolean; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button variant="ghost" size="icon" className="size-8 text-muted-foreground" aria-label={label} onClick={onClick} disabled={disabled}>
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
}

/**
 * One sandbox: who it is and what can be done to it, its overview, and its
 * terminal, logs, files and events. The same component is the slide-in panel
 * on the list and the full page at /sandbox?id=, so the two never drift.
 *
 * Wide, it is two panes, the overview beside the tabs, so the terminal and
 * what the sandbox is are on screen together; narrow, the overview is the
 * first tab. Which is decided by the window's width, not by rendering both and
 * hiding one: a terminal holds a connection, and two would be two shells'
 * worth of traffic.
 */
export function SandboxDetails({
  id,
  variant,
  expanded = false,
  startShell = false,
  onClose,
  onPrev,
  onNext,
  onToggleExpand,
  onGone,
}: {
  id: string;
  variant: "panel" | "page";
  /** A panel opened wide: two panes, like the page. */
  expanded?: boolean;
  /** Open a shell as soon as the sandbox is running, and show it. */
  startShell?: boolean;
  onClose?: () => void;
  onPrev?: () => void;
  onNext?: () => void;
  onToggleExpand?: () => void;
  /** After a terminate from here. */
  onGone?: () => void;
}) {
  const { data: sb, error } = useSandbox(id);
  const live = !!sb && sb.state !== "terminated";
  const { data: procs } = useProcesses(id, live);
  const { data: info } = useInfo();
  const caps = info?.capabilities?.capabilities ?? {};
  const can = useCan();
  const kill = useKill();
  const suspend = useSuspend();
  const snapshot = useSnapshot();
  const openShell = useOpenShell();
  const [shellPid, setShellPid] = useState<number | null>(null);
  const [pid, setPid] = useState<number | null>(null);
  const [tab, setTab] = useState<string | null>(null);
  const wideScreen = useMinWidth(variant === "page" ? 1024 : 900);
  const twoPane = wideScreen && (variant === "page" || expanded);

  // A new sandbox in the same panel starts from its own defaults.
  useEffect(() => {
    setShellPid(null);
    setPid(null);
    setTab(null);
  }, [id]);

  const shell = () =>
    openShell
      .mutateAsync({ id, rows: 30, cols: 120 })
      .then((p) => {
        setShellPid(p.pid);
        setTab("terminal");
      })
      .catch((e: Error) => toast.error(e.message));

  const started = useRef<string | null>(null);
  useEffect(() => {
    if (startShell && sb?.state === "running" && started.current !== id) {
      started.current = id;
      shell();
    }
    // shell is recreated every render; the ref is what makes this run once per sandbox.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [startShell, sb?.state, id]);

  if (error) return <p className="p-5 text-sm text-destructive">{error.message}</p>;
  if (!sb) return <p className="p-5 text-sm text-muted-foreground">Loading…</p>;

  const processes = procs ?? [];
  // The terminal shows the shell opened here, else the first process with one.
  const tty =
    processes.find((p) => p.pid === shellPid && p.state === "running") ??
    processes.find((p) => p.tty && p.state === "running");
  const selected = processes.find((p) => p.pid === pid) ?? processes[processes.length - 1];
  const act = (p: Promise<unknown>, done: string) => p.then(() => toast.success(done)).catch((e: Error) => toast.error(e.message));
  const terminate = () => {
    if (confirm(`Terminate ${sb.name || sb.id}? Everything in it goes with it.`)) {
      act(kill.mutateAsync(id), "Terminated").then(() => onGone?.());
    }
  };

  const fallback = tty ? "terminal" : processes.length ? "output" : "events";
  const current = tab ?? (twoPane ? fallback : tty ? "terminal" : "overview");
  const value = twoPane && current === "overview" ? fallback : current;

  const Title = variant === "page" ? "h1" : "h2";
  const header = (
    <div className="flex flex-col gap-3 border-b px-5 pt-4 pb-4">
      {variant === "panel" ? (
        <div className="-mt-1 -mr-2 flex items-center justify-between gap-2">
          <span className="text-xs font-medium text-muted-foreground">Sandbox details</span>
          <div className="flex items-center">
            {onPrev || onNext ? (
              <>
                <IconButton label="Previous sandbox" onClick={onPrev} disabled={!onPrev}>
                  <ChevronUp className="size-4" />
                </IconButton>
                <IconButton label="Next sandbox" onClick={onNext} disabled={!onNext}>
                  <ChevronDown className="size-4" />
                </IconButton>
              </>
            ) : null}
            {onToggleExpand ? (
              <IconButton label={expanded ? "Narrow" : "Widen"} onClick={onToggleExpand}>
                {expanded ? <Minimize2 className="size-4" /> : <Maximize2 className="size-4" />}
              </IconButton>
            ) : null}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button asChild variant="ghost" size="icon" className="size-8 text-muted-foreground">
                  <Link href={`/sandbox?id=${encodeURIComponent(id)}`} aria-label="Open as a page">
                    <ExternalLink className="size-4" />
                  </Link>
                </Button>
              </TooltipTrigger>
              <TooltipContent>Open as a page</TooltipContent>
            </Tooltip>
            {onClose ? (
              <IconButton label="Close" onClick={onClose}>
                <X className="size-4" />
              </IconButton>
            ) : null}
          </div>
        </div>
      ) : null}

      <div className="flex min-w-0 flex-col gap-0.5">
        <Title className={cn("truncate font-medium tracking-tight", variant === "page" ? "text-[1.75rem] leading-tight" : "text-xl")}>
          {sb.name || <span className="font-mono">{sb.id}</span>}
        </Title>
        {sb.name ? (
          <span className="flex items-center gap-1 font-mono text-xs text-muted-foreground">
            {sb.id}
            <CopyButton value={sb.id} label="Copy id" className="size-6" />
          </span>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <StateDot state={sb.state} />
        {live ? (
          <div className="flex flex-wrap items-center gap-1.5">
            {sb.state === "running" && can("sandbox:create") && (
              <Button size="sm" disabled={openShell.isPending} onClick={shell}>
                <SquareTerminal className="size-4" />
                Terminal
              </Button>
            )}
            {caps.suspend && can("sandbox:create") && (
              <Button
                variant="outline"
                size="sm"
                disabled={suspend.isPending}
                onClick={() => act(suspend.mutateAsync({ id, resume: sb.state === "suspended" }), sb.state === "suspended" ? "Resumed" : "Suspended")}
              >
                {sb.state === "suspended" ? "Resume" : "Suspend"}
              </Button>
            )}
            {caps.memory_snapshot && sb.state === "running" && can("sandbox:create") && (
              <Button variant="outline" size="sm" disabled={snapshot.isPending} onClick={() => act(snapshot.mutateAsync(id), "Snapshot taken")}>
                Snapshot
              </Button>
            )}
            {can("sandbox:delete") && (
              <Button variant="outline" size="sm" className="text-destructive hover:text-destructive" disabled={kill.isPending} onClick={terminate}>
                <Trash2 className="size-3.5" />
                Terminate
              </Button>
            )}
          </div>
        ) : null}
      </div>
    </div>
  );

  const overview = (
    <SandboxOverview
      sb={sb}
      processes={processes}
      onLogs={(p) => {
        setPid(p);
        setTab("output");
      }}
    />
  );

  const tabs = (
    <Tabs value={value} onValueChange={setTab} className="flex min-h-0 flex-1 flex-col gap-0">
      <div className="border-b px-5 pt-3">
        <TabsList className="h-9">
          {!twoPane && <TabsTrigger value="overview">Overview</TabsTrigger>}
          <TabsTrigger value="terminal" disabled={!tty}>
            Terminal
          </TabsTrigger>
          <TabsTrigger value="output" disabled={processes.length === 0}>
            Logs
          </TabsTrigger>
          <TabsTrigger value="files" disabled={sb.state !== "running"}>
            Files
          </TabsTrigger>
          <TabsTrigger value="events">Events</TabsTrigger>
        </TabsList>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {!twoPane && (
          <TabsContent value="overview" className="mt-0">
            {overview}
          </TabsContent>
        )}
        <TabsContent value="terminal" className="mt-0 p-5">
          {tty ? (
            <SandboxTerminal key={tty.pid} sandbox={id} pid={tty.pid} />
          ) : (
            <p className="text-sm text-muted-foreground">No process here has a terminal. Open one with Terminal above.</p>
          )}
        </TabsContent>
        <TabsContent value="output" className="mt-0 flex flex-col gap-3 p-5">
          <div className="flex flex-wrap gap-1.5">
            {processes.map((p) => (
              <Button
                key={p.pid}
                size="sm"
                variant={selected?.pid === p.pid ? "default" : "outline"}
                className="font-mono text-xs"
                onClick={() => setPid(p.pid)}
              >
                {p.pid} · {p.argv[0]} {p.state === "exited" ? `(${p.exit_code})` : ""}
              </Button>
            ))}
          </div>
          {selected ? <ProcessOutput sandbox={id} pid={selected.pid} /> : null}
        </TabsContent>
        <TabsContent value="files" className="mt-0 p-5">
          <SandboxFiles sandbox={id} />
        </TabsContent>
        <TabsContent value="events" className="mt-0 p-5">
          <SandboxEvents sandbox={id} />
        </TabsContent>
      </div>
    </Tabs>
  );

  return (
    <div className="flex h-full min-h-0 flex-col">
      {header}
      {twoPane ? (
        <div className="grid min-h-0 flex-1 grid-cols-[minmax(320px,400px)_1fr]">
          <div className="min-h-0 overflow-auto border-r">{overview}</div>
          <div className="flex min-h-0 flex-col">{tabs}</div>
        </div>
      ) : (
        tabs
      )}
    </div>
  );
}
