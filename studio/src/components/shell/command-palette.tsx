"use client";

import { useEffect, useMemo } from "react";
import { useRouter } from "next/navigation";
import {
  Bot,
  GitBranch,
  Moon,
  Play,
  RefreshCw,
  Skull,
  Square,
  Sun,
  Terminal,
} from "lucide-react";
import { useTheme } from "next-themes";
import { toast } from "sonner";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "@/components/ui/command";
import { ALL_NAV_ITEMS } from "@/lib/nav";
import { useUi } from "@/lib/store";
import {
  useAgents,
  useKillRun,
  useRuns,
  useStopRun,
  useTransportMode,
  useWorktrees,
} from "@/lib/api/queries";
import { runOutcome } from "@/lib/types";
import { formatDurationTight, shortId } from "@/lib/format";
import { StatusBadge } from "@/components/common/status-badge";

/**
 * ⌘K.
 *
 * The palette reaches the same actions the screens do, with one deliberate
 * asymmetry carried over from the CLI: **stop and kill are separate items.**
 * Stop asks the guest to exit; kill does not wait. The difference is whether the
 * agent closed the file it was editing, so it has to be chosen by name — a
 * single "end run" would make the destructive one reachable by accident, which
 * in a fuzzy-matching palette is exactly the wrong place for it.
 */
export function CommandPalette() {
  const router = useRouter();
  const open = useUi((s) => s.paletteOpen);
  const setOpen = useUi((s) => s.setPaletteOpen);
  const recentRuns = useUi((s) => s.recentRuns);
  const { setTheme, theme } = useTheme();
  const { retry } = useTransportMode();

  const { data: runs } = useRuns({ live: false });
  const { data: worktrees } = useWorktrees();
  const { data: agents } = useAgents();
  const stop = useStopRun();
  const kill = useKillRun();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen(!open);
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, setOpen]);

  const live = useMemo(() => runs?.filter((r) => r.state === "running") ?? [], [runs]);
  const recent = useMemo(() => {
    if (!runs) return [];
    const byId = new Map(runs.map((r) => [r.id, r]));
    return recentRuns.map((id) => byId.get(id)).filter((r): r is NonNullable<typeof r> => !!r);
  }, [runs, recentRuns]);

  function go(href: string) {
    setOpen(false);
    router.push(href);
  }

  return (
    <CommandDialog
      open={open}
      onOpenChange={setOpen}
      title="Command palette"
      description="Jump to a screen, a run, a branch or an agent — or act on a live sandbox."
    >
      <CommandInput placeholder="Search runs, branches, agents, or type a command…" />
      <CommandList className="max-h-[26rem]">
        <CommandEmpty>Nothing matches that.</CommandEmpty>

        <CommandGroup heading="Go to">
          {ALL_NAV_ITEMS.map((item) => (
            <CommandItem
              key={item.href}
              value={`${item.title} ${item.hint}`}
              onSelect={() => go(item.href)}
            >
              <item.icon className="size-4" />
              <span>{item.title}</span>
              <span className="ml-2 truncate text-xs text-muted-foreground">{item.hint}</span>
              {item.shortcut && <CommandShortcut>⌘{item.shortcut}</CommandShortcut>}
            </CommandItem>
          ))}
        </CommandGroup>

        {live.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading={`Live sandboxes (${live.length})`}>
              {live.map((run) => (
                <CommandItem
                  key={run.id}
                  value={`live ${run.name} ${run.branch ?? ""} ${run.agent ?? ""} ${run.id}`}
                  onSelect={() => go(`/runs/${run.id}`)}
                >
                  <Terminal className="size-4" />
                  <span className="truncate font-mono text-xs">{run.branch ?? run.name}</span>
                  <span className="ml-auto flex items-center gap-2">
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {formatDurationTight(run.durationMs)}
                    </span>
                    <StatusBadge outcome="running" size="sm" />
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>

            <CommandSeparator />
            <CommandGroup heading="Act on a live sandbox">
              {live.map((run) => (
                <CommandItem
                  key={`stop-${run.id}`}
                  value={`stop ${run.branch ?? run.name} graceful`}
                  onSelect={() => {
                    setOpen(false);
                    stop.mutate(run.id, {
                      onSuccess: () =>
                        toast.success(`Asked ${run.branch ?? run.name} to exit`, {
                          description: "The guest gets a grace period to finish writing.",
                        }),
                    });
                  }}
                >
                  <Square className="size-4" />
                  <span>
                    Stop <span className="font-mono text-xs">{run.branch ?? run.name}</span>
                  </span>
                  <span className="ml-auto text-xs text-muted-foreground">graceful</span>
                </CommandItem>
              ))}
              {live.map((run) => (
                <CommandItem
                  key={`kill-${run.id}`}
                  value={`kill ${run.branch ?? run.name} force immediate`}
                  onSelect={() => {
                    setOpen(false);
                    kill.mutate(run.id, {
                      onSuccess: () =>
                        toast.warning(`Killed ${run.branch ?? run.name}`, {
                          description:
                            "Terminated immediately — it had no chance to finish what it was writing.",
                        }),
                    });
                  }}
                  className="text-destructive data-[selected=true]:text-destructive"
                >
                  <Skull className="size-4" />
                  <span>
                    Kill <span className="font-mono text-xs">{run.branch ?? run.name}</span>
                  </span>
                  <span className="ml-auto text-xs opacity-70">immediate</span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {recent.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Recently viewed">
              {recent.map((run) => (
                <CommandItem
                  key={`recent-${run.id}`}
                  value={`recent ${run.name} ${run.id}`}
                  onSelect={() => go(`/runs/${run.id}`)}
                >
                  <Terminal className="size-4" />
                  <span className="truncate font-mono text-xs">{run.branch ?? run.name}</span>
                  <span className="ml-auto flex items-center gap-2">
                    {/* Abbreviated for display; the route carries the whole id. */}
                    <span className="font-mono text-[10px] text-muted-foreground">
                      {shortId(run.id, 8)}
                    </span>
                    <StatusBadge outcome={runOutcome(run)} exitCode={run.exitCode} size="sm" />
                  </span>
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}

        {worktrees && worktrees.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Branches">
              {worktrees
                .filter((w) => !w.primary)
                .slice(0, 12)
                .map((w) => (
                  <CommandItem
                    key={w.branch}
                    value={`branch ${w.branch} worktree`}
                    onSelect={() => go(`/worktrees?branch=${encodeURIComponent(w.branch)}`)}
                  >
                    <GitBranch className="size-4" />
                    <span className="truncate font-mono text-xs">{w.branch}</span>
                    {w.dirty.length > 0 && (
                      <span className="ml-auto text-xs text-caution tabular-nums">
                        {w.dirty.length} dirty
                      </span>
                    )}
                  </CommandItem>
                ))}
            </CommandGroup>
          </>
        )}

        {agents && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Launch an agent">
              {agents
                .filter((a) => a.headlessVerified)
                .map((a) => (
                  <CommandItem
                    key={a.name}
                    value={`launch run start ${a.name} ${a.label}`}
                    onSelect={() => go(`/launch?agent=${a.name}`)}
                  >
                    <Play className="size-4" />
                    <span>
                      New run with <span className="font-mono text-xs">{a.name}</span>
                    </span>
                    <span className="ml-auto text-xs text-muted-foreground">{a.label}</span>
                  </CommandItem>
                ))}
              <CommandItem value="agents all fifteen adapters" onSelect={() => go("/agents")}>
                <Bot className="size-4" />
                <span>See all fifteen adapters</span>
              </CommandItem>
            </CommandGroup>
          </>
        )}

        <CommandSeparator />
        <CommandGroup heading="Studio">
          <CommandItem
            value="theme toggle dark light appearance"
            onSelect={() => {
              setTheme(theme === "dark" ? "light" : "dark");
              setOpen(false);
            }}
          >
            {theme === "dark" ? <Sun className="size-4" /> : <Moon className="size-4" />}
            <span>Switch to {theme === "dark" ? "light" : "dark"} theme</span>
          </CommandItem>
          <CommandItem
            value="reconnect daemon retry connection refresh"
            onSelect={() => {
              retry();
              setOpen(false);
              toast.info("Retrying the daemon on localhost:8787…");
            }}
          >
            <RefreshCw className="size-4" />
            <span>Reconnect to the daemon</span>
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
