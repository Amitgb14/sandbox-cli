"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { CornerDownLeft, ShieldBan, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { ContainmentCanvas, type ShotKind } from "@/components/containment-canvas";
import { PRESETS, classify, type Target } from "@/lib/commands";
import { cn } from "@/lib/utils";

interface LogEntry {
  id: number;
  allowed: boolean;
  label: string;
  cmd: string;
  reason: string;
}

const HOST_PATHS: { key: Target; path: string }[] = [
  { key: "ssh", path: "~/.ssh/id_rsa" },
  { key: "aws", path: "~/.aws/credentials" },
  { key: "home", path: "~/ (200 other repos)" },
  { key: "net", path: "the open internet" },
];

const BOX_PATHS = [
  { path: "/workspace", note: "your project" },
  { path: "/sandbox/home", note: "ephemeral" },
  { path: "user: sandbox", note: "non-root" },
];

export function ContainmentSimulator() {
  const canvasHost = useRef<HTMLDivElement | null>(null);
  const [entries, setEntries] = useState<LogEntry[]>([]);
  const [blocked, setBlocked] = useState(0);
  const [hit, setHit] = useState<Target | null>(null);
  const [value, setValue] = useState("");
  const nextId = useRef(0);
  const engaged = useRef(false);

  const fire = useCallback((cmd: string, allowed: boolean, target: Target, reason: string) => {
    const el = canvasHost.current?.querySelector("canvas") as
      | (HTMLCanvasElement & { fire?: (l: string, k: ShotKind) => void })
      | null;
    el?.fire?.(cmd, allowed ? "allowed" : "blocked");

    setHit(target);
    if (!allowed) setBlocked((n) => n + 1);
    setEntries((prev) => {
      const entry: LogEntry = {
        id: nextId.current++,
        allowed,
        label: allowed ? "ALLOWED" : "BLOCKED",
        cmd,
        reason,
      };
      return [...prev, entry].slice(-4);
    });
  }, []);

  const submit = useCallback(() => {
    const cmd = value.trim();
    if (!cmd) return;
    engaged.current = true;
    const v = classify(cmd);
    if (v) fire(cmd, v.allowed, v.target, v.reason);
    setValue("");
  }, [value, fire]);

  // Autoplay the presets once, until the visitor takes over.
  useEffect(() => {
    const node = canvasHost.current;
    if (!node) return;
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    let timers: ReturnType<typeof setTimeout>[] = [];
    const io = new IntersectionObserver(
      (obs) => {
        for (const e of obs) {
          if (!e.isIntersecting) continue;
          io.disconnect();
          PRESETS.forEach((p, i) => {
            timers.push(
              setTimeout(() => {
                if (engaged.current) return;
                fire(p.cmd, p.allowed, p.target, p.reason);
              }, 600 + i * 1900),
            );
          });
        }
      },
      { threshold: 0.4 },
    );
    io.observe(node);
    return () => {
      io.disconnect();
      timers.forEach(clearTimeout);
      timers = [];
    };
  }, [fire]);

  return (
    <div className="overflow-hidden rounded-xl border bg-card shadow-2xl shadow-black/10 dark:shadow-black/40">
      {/* title bar */}
      <div className="flex flex-wrap items-center gap-3 border-b bg-muted/50 px-4 py-2.5 font-mono text-xs text-muted-foreground">
        <span className="flex gap-1.5" aria-hidden="true">
          <i className="size-2.5 rounded-full bg-exposed/85" />
          <i className="size-2.5 rounded-full bg-signal/85" />
          <i className="size-2.5 rounded-full bg-contained/85" />
        </span>
        <span>containment&nbsp;monitor</span>
        <span className="flex-1" />
        <span className="tabular-nums">{blocked} blocked</span>
      </div>

      {/* preset launcher */}
      <div className="flex flex-wrap items-center gap-2 border-b px-4 py-3">
        <span className="mr-1 font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted-foreground">
          Fire a command
        </span>
        {PRESETS.map((p) => (
          <button
            key={p.cmd}
            onClick={() => {
              engaged.current = true;
              fire(p.cmd, p.allowed, p.target, p.reason);
            }}
            className={cn(
              "rounded-md border bg-background px-2.5 py-1.5 font-mono text-xs transition-colors",
              p.allowed
                ? "hover:border-contained/55 hover:text-contained"
                : "hover:border-exposed/55 hover:text-exposed",
            )}
          >
            {p.cmd}
          </button>
        ))}
      </div>

      {/* stage: paths + canvas overlay */}
      <div ref={canvasHost} className="relative">
        <ContainmentCanvas className="absolute inset-0 size-full" />

        <div className="relative grid min-h-[280px] grid-cols-1 md:grid-cols-2">
          <div className="flex flex-col gap-2 p-5 pt-11">
            {HOST_PATHS.map((p) => (
              <PathChip key={p.key} label={p.path} state={hit === p.key ? "hit" : "idle"} />
            ))}
          </div>
          <div className="flex flex-col gap-2 p-5 pt-11">
            {BOX_PATHS.map((p, i) => (
              <PathChip
                key={p.path}
                label={p.path}
                note={p.note}
                state={i === 0 && hit === "workspace" ? "reached" : "idle"}
              />
            ))}
          </div>
        </div>
      </div>

      {/* free-text prompt */}
      <div className="flex items-center gap-2 border-y bg-muted/30 px-4 py-2.5">
        <span className="font-mono font-semibold text-contained" aria-hidden="true">
          $
        </span>
        <Input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              submit();
            }
          }}
          placeholder="…or type your own: ssh-add -L, cat /etc/passwd, go build ./..."
          aria-label="Type a command to test against the boundary"
          className="h-8 border-0 bg-transparent px-0 font-mono text-xs shadow-none focus-visible:ring-0 dark:bg-transparent"
        />
        <Button size="sm" variant="secondary" onClick={submit} className="h-8 gap-1.5 font-mono text-xs">
          run <CornerDownLeft className="size-3" />
        </Button>
      </div>

      {/* verdict log */}
      <div className="min-h-[5.5rem] space-y-1 bg-muted/30 px-4 py-3 font-mono text-xs" aria-live="polite">
        <AnimatePresence initial={false}>
          {entries.length === 0 ? (
            <p className="text-muted-foreground/75">
              Pick a command above — or type your own — and watch where it lands.
            </p>
          ) : (
            entries.map((e) => (
              <motion.div
                key={e.id}
                initial={{ opacity: 0, y: -4 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0 }}
                transition={{ duration: 0.25 }}
                className="flex items-baseline gap-2"
              >
                <span
                  className={cn(
                    "flex shrink-0 items-center gap-1 font-semibold",
                    e.allowed ? "text-contained" : "text-exposed",
                  )}
                >
                  {e.allowed ? <ShieldCheck className="size-3" /> : <ShieldBan className="size-3" />}
                  {e.label}
                </span>
                <span className="text-muted-foreground">
                  $ {e.cmd} → {e.reason}
                </span>
              </motion.div>
            ))
          )}
        </AnimatePresence>
      </div>
    </div>
  );
}

function PathChip({
  label,
  note,
  state,
}: {
  label: string;
  note?: string;
  state: "idle" | "hit" | "reached";
}) {
  return (
    <motion.div
      animate={
        state === "hit"
          ? { scale: [1, 1.03, 1] }
          : state === "reached"
            ? { scale: [1, 1.03, 1] }
            : { scale: 1 }
      }
      transition={{ duration: 0.35 }}
      className={cn(
        "flex items-center gap-2 rounded-md border px-2.5 py-1.5 font-mono text-xs transition-colors duration-300",
        state === "hit" && "border-exposed/40 bg-exposed-soft text-exposed",
        state === "reached" && "border-contained/40 bg-contained-soft text-contained",
        state === "idle" && "border-transparent text-muted-foreground",
      )}
    >
      <span>{label}</span>
      {note && <span className="opacity-60">— {note}</span>}
    </motion.div>
  );
}

export function StatBadge({ children }: { children: React.ReactNode }) {
  return <Badge variant="secondary">{children}</Badge>;
}
