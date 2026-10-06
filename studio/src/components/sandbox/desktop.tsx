"use client";

import { useEffect, useRef, useState } from "react";
import { Maximize, MonitorPlay, RotateCw } from "lucide-react";
import { CodeBlock } from "@/components/common/code-block";
import { Button } from "@/components/ui/button";
import { desktopURL } from "@/lib/api/client";
import { DESKTOP_COMMAND, useStartDesktop } from "@/lib/api/queries";
import type { Process } from "@/lib/types";

/** The desktop image beside the base one a sandbox was made from, for the command that makes one. */
function desktopImage(image: string): string {
  return /sandbox-base(?=:|@|$)/.test(image) ? image.replace(/sandbox-base(?=:|@|$)/, "sandbox-desktop") : "ghcr.io/<owner>/sandbox-desktop:edge";
}

/**
 * The desktop's screen, drawn by noVNC from the VNC stream Studio's server
 * bridges (/api/ws/desktop). The client is Studio's own code: the guest sends
 * pixels and receives input, and nothing it serves is shown as a page.
 *
 * sandbox-desktop takes a moment to open its VNC port after it starts, so a
 * first connection refused is retried for a few seconds before giving up.
 */
function Viewer({ sandbox, pid }: { sandbox: string; pid: number }) {
  const screen = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting…");
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const el = screen.current;
    if (!el) return;
    let disposed = false;
    let rfb: { disconnect(): void } | null = null;
    let retry: ReturnType<typeof setTimeout> | undefined;
    let tries = 0;

    const connect = async () => {
      const { default: RFB } = await import("@novnc/novnc");
      if (disposed) return;
      const r = new RFB(el, desktopURL(sandbox), { shared: true });
      rfb = r;
      r.scaleViewport = true;
      r.resizeSession = false;
      r.background = "#0b0b0c";
      let connected = false;
      r.addEventListener("connect", () => {
        connected = true;
        setStatus("connected — closing this tab leaves the desktop running");
      });
      r.addEventListener("disconnect", () => {
        if (disposed) return;
        if (!connected && tries < 15) {
          tries++;
          setStatus("waiting for the desktop to start…");
          retry = setTimeout(connect, 700);
          return;
        }
        setStatus(connected ? "disconnected" : "could not reach the desktop — is sandbox-desktop still running?");
      });
    };
    void connect();
    return () => {
      disposed = true;
      clearTimeout(retry);
      rfb?.disconnect();
    };
  }, [sandbox, pid, attempt]);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">{status}</p>
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => setAttempt((a) => a + 1)}>
            <RotateCw className="size-3.5" />
            Reconnect
          </Button>
          <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => void screen.current?.requestFullscreen()}>
            <Maximize className="size-3.5" />
            Full screen
          </Button>
        </div>
      </div>
      <div ref={screen} data-testid="desktop-screen" className="h-[32rem] overflow-hidden rounded-md border bg-[#0b0b0c]" />
    </div>
  );
}

/**
 * A sandbox's desktop: a screen with a terminal and a browser, when its image
 * has one (images/desktop). Started here as a background process, so it
 * outlives the tab and is listed with the others; stopping that process stops
 * the desktop.
 */
export function SandboxDesktop({
  sandbox,
  image,
  processes,
  canStart,
}: {
  sandbox: string;
  image: string;
  processes: Process[];
  canStart: boolean;
}) {
  const start = useStartDesktop();
  const [missing, setMissing] = useState(false);
  const mine = processes.filter((p) => p.argv[0] === DESKTOP_COMMAND);
  const running = mine.find((p) => p.state === "running");
  const last = mine[mine.length - 1];

  if (running) return <Viewer sandbox={sandbox} pid={running.pid} />;

  if (missing) {
    return (
      <div className="flex flex-col gap-3 text-sm">
        <p className="text-muted-foreground">
          This sandbox&apos;s image has no desktop: <span className="font-mono">{DESKTOP_COMMAND}</span> is not in it. A sandbox made from the desktop
          image has one, with a terminal and a browser:
        </p>
        {(() => {
          const cmd = `sandbox-cli run --keep --name desktop --image ${desktopImage(image)} --memory 2048 -- true`;
          return (
            <CodeBlock title="CLI" value={cmd} className="w-full">
              {cmd}
            </CodeBlock>
          );
        })()}
      </div>
    );
  }

  return (
    <div className="flex flex-col items-start gap-3 text-sm">
      <p className="text-muted-foreground">
        {last && last.state === "exited"
          ? `The desktop exited with ${last.exit_code}; its Logs say why.`
          : "No desktop is running in this sandbox. Start one to see its screen here, with a terminal and a browser."}
      </p>
      {canStart ? (
        <Button
          size="sm"
          disabled={start.isPending}
          onClick={() =>
            start.mutateAsync({ id: sandbox }).catch((e: Error) => {
              if (/no such command/i.test(e.message)) setMissing(true);
              else throw e;
            })
          }
        >
          <MonitorPlay className="size-4" />
          {start.isPending ? "Starting…" : "Start desktop"}
        </Button>
      ) : null}
      {start.error && !missing ? <p className="text-xs text-destructive">{start.error.message}</p> : null}
    </div>
  );
}
