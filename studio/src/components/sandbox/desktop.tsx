"use client";

import { useEffect, useRef, useState, type RefObject } from "react";
import { toast } from "sonner";
import { Circle, Maximize, MonitorPlay, RefreshCw, RotateCw, Square } from "lucide-react";
import { CodeBlock } from "@/components/common/code-block";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { desktopURL } from "@/lib/api/client";
import { DESKTOP_COMMAND, useDesktops, useStartDesktop } from "@/lib/api/queries";
import type { Process } from "@/lib/types";

/** The desktop image beside the base one a sandbox was made from, for the command that makes one. */
function desktopImage(image: string): string {
  return /sandbox-base(?=:|@|$)/.test(image) ? image.replace(/sandbox-base(?=:|@|$)/, "sandbox-desktop") : "ghcr.io/<owner>/sandbox-desktop:edge";
}

/**
 * Recording formats, best first; the browser uses the first it can make. MP4
 * first, where the browser makes it, since more players open it (QuickTime
 * does not open WebM); otherwise WebM.
 */
const RECORD_TYPES = ["video/mp4;codecs=avc1", "video/mp4", "video/webm;codecs=vp9", "video/webm;codecs=vp8", "video/webm"];

/** Frames a second a recording is taken at: enough for a desktop, and small. */
const RECORD_FPS = 15;

function clock(ms: number): string {
  const s = Math.floor(ms / 1000);
  const hh = Math.floor(s / 3600);
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return hh ? `${hh}:${mm}:${ss}` : `${mm}:${ss}`;
}

/** Hands the browser a file to save, as a download of this page. */
function save(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

/**
 * Records the desktop as Studio shows it: the canvas noVNC draws the screen
 * on, taken by the browser's own MediaRecorder at the desktop's resolution,
 * and saved to this computer as a video when it stops. Nothing runs in the
 * sandbox and nothing is stored anywhere else; it records only what this tab
 * is showing, while it shows it. It stops, and saves what it has, when the
 * desktop disconnects or the tab moves to another desktop or page.
 */
function useRecorder(screen: RefObject<HTMLDivElement | null>, file: string) {
  const rec = useRef<MediaRecorder | null>(null);
  const [since, setSince] = useState<number | null>(null);
  const [now, setNow] = useState(0);

  const stop = () => {
    if (rec.current && rec.current.state !== "inactive") rec.current.stop();
  };
  const start = () => {
    const canvas = screen.current?.querySelector("canvas");
    if (!canvas || typeof MediaRecorder === "undefined" || typeof canvas.captureStream !== "function") {
      toast.error("This browser cannot record the screen.");
      return;
    }
    const mimeType = RECORD_TYPES.find((t) => MediaRecorder.isTypeSupported(t));
    // Frames on a clock, not on change. A canvas stream sends a frame only
    // when its canvas is drawn on, so a desktop sitting still recorded as a
    // single frame with no length, and a recording ended at its last change
    // rather than at Stop; asking for frames (requestFrame) did not help
    // where nothing had been drawn. So the screen is copied onto a canvas of
    // the recording's own on every tick: each copy is a draw, and a frame,
    // whether or not the desktop changed. It follows the desktop's size.
    const copy = document.createElement("canvas");
    copy.width = canvas.width;
    copy.height = canvas.height;
    const pen = copy.getContext("2d");
    if (!pen) {
      toast.error("This browser cannot record the screen.");
      return;
    }
    const draw = () => {
      if (copy.width !== canvas.width || copy.height !== canvas.height) {
        copy.width = canvas.width;
        copy.height = canvas.height;
      }
      pen.drawImage(canvas, 0, 0);
    };
    draw();
    const stream = copy.captureStream(RECORD_FPS);
    const ticker = setInterval(draw, 1000 / RECORD_FPS);
    const r = new MediaRecorder(stream, mimeType ? { mimeType, videoBitsPerSecond: 2_500_000 } : undefined);
    const chunks: Blob[] = [];
    const began = new Date();
    r.ondataavailable = (e) => {
      if (e.data.size) chunks.push(e.data);
    };
    r.onstop = () => {
      clearInterval(ticker);
      stream.getTracks().forEach((t) => t.stop());
      rec.current = null;
      setSince(null);
      if (!chunks.length) return;
      const type = r.mimeType || mimeType || "video/webm";
      const ext = type.startsWith("video/mp4") ? "mp4" : "webm";
      const stamp = began.toISOString().slice(0, 19).replace(/[:T]/g, "-");
      save(new Blob(chunks, { type }), `${file}-${stamp}.${ext}`);
      toast.success("Recording saved", { description: `${clock(Date.now() - began.getTime())} of the desktop, downloaded as .${ext}` });
    };
    // In one-second pieces, so what was recorded survives a stop the
    // recorder did not see coming (the desktop going away).
    r.start(1000);
    rec.current = r;
    setSince(began.getTime());
    setNow(began.getTime());
  };

  useEffect(() => {
    if (since === null) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [since]);
  // Leaving the desktop ends the recording and saves it, rather than losing it.
  useEffect(() => () => stop(), []);

  return { recording: since !== null, elapsed: since === null ? 0 : now - since, start, stop };
}

/**
 * The desktop's screen, drawn by noVNC from the VNC stream Studio's server
 * bridges (/api/ws/desktop). The client is Studio's own code: the guest sends
 * pixels and receives input, and nothing it serves is shown as a page.
 *
 * sandbox-desktop takes a moment to open its VNC port after it starts, so a
 * first connection refused is retried for a few seconds before giving up.
 */
function Viewer({ sandbox, pid, port }: { sandbox: string; pid: number; port?: number }) {
  const screen = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState("connecting…");
  const [attempt, setAttempt] = useState(0);
  const [live, setLive] = useState(false);
  const recorder = useRecorder(screen, `${sandbox}${port ? `-${port}` : ""}`);
  // The disconnect handler is set up once per connection; it reaches the
  // recorder through this, which is always the current one.
  const stopRecording = useRef(recorder.stop);
  stopRecording.current = recorder.stop;

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
      const r = new RFB(el, desktopURL(sandbox, port), { shared: true });
      rfb = r;
      r.scaleViewport = true;
      r.resizeSession = false;
      r.background = "#0b0b0c";
      let connected = false;
      r.addEventListener("connect", () => {
        connected = true;
        setLive(true);
        setStatus("connected — closing this tab leaves the desktop running");
      });
      r.addEventListener("disconnect", () => {
        // The canvas goes with the connection: a recording of it ends here.
        stopRecording.current();
        setLive(false);
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
  }, [sandbox, pid, port, attempt]);

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">{status}</p>
        <div className="flex items-center gap-1">
          {recorder.recording ? (
            <Button variant="destructive" size="sm" className="h-7 gap-1.5 text-xs" onClick={recorder.stop}>
              <Square className="size-3 fill-current" />
              Stop · <span className="font-mono tabular-nums">{clock(recorder.elapsed)}</span>
            </Button>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs"
              disabled={!live}
              title="Record this desktop as it is shown here, and save the video to this computer when you stop"
              onClick={recorder.start}
            >
              <Circle className="size-3 fill-destructive text-destructive" />
              Record
            </Button>
          )}
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
      <div
        ref={screen}
        data-testid="desktop-screen"
        className={cn("h-[32rem] overflow-hidden rounded-md border bg-[#0b0b0c]", recorder.recording && "border-destructive/60 ring-1 ring-destructive/30")}
      />
    </div>
  );
}

/**
 * A sandbox's desktop: a screen with a terminal and a browser, when its image
 * has one (images/desktop). Started here as a background process, so it
 * outlives the tab and is listed with the others; stopping that process stops
 * the desktop.
 *
 * An image may start its desktop itself, as it boots, and an agent may start
 * displays of its own to work in: no process of the sandbox's list is them,
 * but their VNC ports answer. Every display that does, :0 to :9, is offered,
 * to switch between. Offering Start where one already ran started a second
 * desktop on a taken display, which exited at once with 1 ("Server is
 * already active for display 1").
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
  const desktops = useDesktops(sandbox, true);
  const [picked, setPicked] = useState<number | null>(null);
  // The one started here serves display :0, so it counts even before its
  // port is up; the others are whatever answered.
  const ports = [...new Set([...(running ? [5900] : []), ...(desktops.data ?? [])])].sort((a, b) => a - b);
  const port = picked !== null && ports.includes(picked) ? picked : ports[0];

  if (port !== undefined) {
    return (
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-center gap-1.5" role="radiogroup" aria-label="Desktop">
          {ports.map((p) => (
            <button
              key={p}
              type="button"
              role="radio"
              aria-checked={p === port}
              onClick={() => setPicked(p)}
              className={cn(
                "rounded-md border px-2 py-0.5 font-mono text-xs transition-colors",
                p === port ? "border-primary bg-primary/10 text-foreground" : "text-muted-foreground hover:text-foreground",
              )}
              title={`The desktop served over VNC on port ${p}`}
            >
              port {p}
            </button>
          ))}
          <Button variant="ghost" size="sm" className="h-7 text-xs" disabled={desktops.isFetching} onClick={() => void desktops.refetch()}>
            <RefreshCw className={cn("size-3.5", desktops.isFetching && "animate-spin")} />
            Rescan
          </Button>
        </div>
        <Viewer key={port} sandbox={sandbox} pid={running?.pid ?? 0} port={port} />
      </div>
    );
  }
  if (desktops.isPending) return <p className="text-sm text-muted-foreground">Looking for a desktop in this sandbox…</p>;

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
