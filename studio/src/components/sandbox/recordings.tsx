"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Square } from "lucide-react";
import { Button } from "@/components/ui/button";
import { desktopURL } from "@/lib/api/client";

/**
 * Recording formats, best first; the browser uses the first it can make. MP4
 * first, where the browser makes it, since more players open it (QuickTime
 * does not open WebM); otherwise WebM.
 */
const RECORD_TYPES = ["video/mp4;codecs=avc1", "video/mp4", "video/webm;codecs=vp9", "video/webm;codecs=vp8", "video/webm"];

/** Frames a second a recording is taken at: enough for a desktop, and small. */
const RECORD_FPS = 15;

export function clock(ms: number): string {
  const s = Math.floor(ms / 1000);
  const hh = Math.floor(s / 3600);
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return hh ? `${hh}:${mm}:${ss}` : `${mm}:${ss}`;
}

/**
 * Calls tick every ms, from a worker's clock: a page in a background browser
 * tab has its own timers slowed to once a second, and after a few minutes to
 * once a minute, which made a recording left running there a slideshow. A
 * worker's timer is not slowed that way. Where a worker cannot be made, the
 * page's own timer. Returns what stops it.
 */
function steadyClock(tick: () => void, ms: number): () => void {
  try {
    const src = URL.createObjectURL(
      new Blob([`setInterval(() => postMessage(0), ${ms});`], {
        type: "text/javascript",
      }),
    );
    const w = new Worker(src);
    URL.revokeObjectURL(src);
    w.onmessage = tick;
    return () => w.terminate();
  } catch {
    const t = setInterval(tick, ms);
    return () => clearInterval(t);
  }
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

/** A recording: of one desktop, a sandbox's VNC port. since is null while it connects. */
export type Recording = {
  key: string;
  sandbox: string;
  port: number;
  since: number | null;
};

type Recordings = {
  list: Recording[];
  now: number;
  start(sandbox: string, port: number): void;
  stop(key: string): void;
};

const Ctx = createContext<Recordings | null>(null);

export function useRecordings(): Recordings {
  const c = useContext(Ctx);
  if (!c) throw new Error("useRecordings outside RecordingsProvider");
  return c;
}

export const recordingKey = (sandbox: string, port: number) => `${sandbox}:${port}`;

/**
 * Records sandboxes' desktops, from the browser: the screen noVNC draws, taken
 * by the browser's own MediaRecorder at the desktop's resolution and saved to
 * this computer as a video when it stops. Nothing runs in the sandbox and
 * nothing is stored anywhere else.
 *
 * It lives in the layout, above every page, and records over a connection of
 * its own, view-only and drawn off screen. Recording the Desktop tab's own
 * screen ended the recording whenever that screen went: another tab of the
 * panel, another page of the sidebar. Now a recording runs until Stop, here
 * or on the Desktop tab, until the desktop goes away, or until Studio's page
 * is closed, which the browser is asked to confirm. What was recorded is saved
 * in each of those cases but the last.
 */
export function RecordingsProvider({ children }: { children: React.ReactNode }) {
  const [list, setList] = useState<Recording[]>([]);
  const [now, setNow] = useState(0);
  const stops = useRef(new Map<string, () => void>());

  const drop = useCallback((key: string) => {
    stops.current.delete(key);
    setList((l) => l.filter((r) => r.key !== key));
  }, []);

  const start = useCallback(
    (sandbox: string, port: number) => {
      const key = recordingKey(sandbox, port);
      if (stops.current.has(key)) return;
      if (typeof MediaRecorder === "undefined" || typeof HTMLCanvasElement.prototype.captureStream !== "function") {
        toast.error("This browser cannot record the screen.");
        return;
      }
      // noVNC draws into this, out of sight. Unscaled, its canvas is the
      // desktop's own size, whatever the size of this box.
      const host = document.createElement("div");
      host.setAttribute("aria-hidden", "true");
      host.style.cssText = "position:fixed;left:-100000px;top:0;width:1px;height:1px;overflow:hidden;pointer-events:none";
      document.body.appendChild(host);

      let rfb: { disconnect(): void } | null = null;
      let rec: MediaRecorder | null = null;
      let ended = false;
      const end = () => {
        if (ended) return;
        ended = true;
        rfb?.disconnect();
        host.remove();
        drop(key);
      };
      // Stop saves: the recorder's onstop ends the connection after the file.
      const stop = () => {
        if (rec && rec.state !== "inactive") rec.stop();
        else end();
      };
      stops.current.set(key, stop);
      setList((l) => [...l, { key, sandbox, port, since: null }]);

      const record = (canvas: HTMLCanvasElement) => {
        const mimeType = RECORD_TYPES.find((t) => MediaRecorder.isTypeSupported(t));
        // Frames on a clock, not on change. A canvas stream sends a frame only
        // when its canvas is drawn on, so a desktop sitting still recorded as
        // a single frame with no length, and a recording ended at its last
        // change rather than at Stop; asking for frames (requestFrame) did
        // not help where nothing had been drawn. So the screen is copied onto
        // a canvas of the recording's own on every tick: each copy is a draw,
        // and a frame, whether or not the desktop changed. It follows the
        // desktop's size.
        const copy = document.createElement("canvas");
        copy.width = canvas.width;
        copy.height = canvas.height;
        const pen = copy.getContext("2d");
        if (!pen) {
          toast.error("This browser cannot record the screen.");
          end();
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
        const stopClock = steadyClock(draw, Math.round(1000 / RECORD_FPS));
        const r = new MediaRecorder(stream, mimeType ? { mimeType, videoBitsPerSecond: 2_500_000 } : undefined);
        const chunks: Blob[] = [];
        const began = new Date();
        r.ondataavailable = (e) => {
          if (e.data.size) chunks.push(e.data);
        };
        r.onstop = () => {
          stopClock();
          stream.getTracks().forEach((t) => t.stop());
          end();
          if (!chunks.length) return;
          const type = r.mimeType || mimeType || "video/webm";
          const ext = type.startsWith("video/mp4") ? "mp4" : "webm";
          const stamp = began.toISOString().slice(0, 19).replace(/[:T]/g, "-");
          save(new Blob(chunks, { type }), `${sandbox}-${port}-${stamp}.${ext}`);
          toast.success("Recording saved", {
            description: `${clock(Date.now() - began.getTime())} of the desktop, downloaded as .${ext}`,
          });
        };
        // In one-second pieces, so what was recorded survives a stop the
        // recorder did not see coming (the desktop going away).
        r.start(1000);
        rec = r;
        setList((l) => l.map((x) => (x.key === key ? { ...x, since: began.getTime() } : x)));
        setNow(began.getTime());
      };

      void import("@novnc/novnc").then(({ default: RFB }) => {
        if (ended) return;
        const r = new RFB(host, desktopURL(sandbox, port), { shared: true });
        rfb = r;
        // It only watches: input goes through the screen the user sees.
        r.viewOnly = true;
        r.scaleViewport = false;
        r.resizeSession = false;
        r.addEventListener("connect", () => {
          const canvas = host.querySelector("canvas");
          if (ended) return;
          if (!canvas) {
            toast.error("Could not record the desktop.");
            end();
            return;
          }
          record(canvas);
        });
        r.addEventListener("disconnect", () => {
          if (ended) return;
          // The desktop went away: what was recorded is saved.
          if (rec && rec.state !== "inactive") {
            rec.stop();
            return;
          }
          toast.error("Could not reach the desktop to record it.");
          end();
        });
      });
    },
    [drop],
  );

  const stop = useCallback((key: string) => stops.current.get(key)?.(), []);

  const recording = list.length > 0;
  useEffect(() => {
    if (!recording) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    // Closing or reloading Studio's page loses a recording in progress: the
    // browser is asked to confirm it.
    const warn = (e: BeforeUnloadEvent) => e.preventDefault();
    window.addEventListener("beforeunload", warn);
    return () => {
      clearInterval(t);
      window.removeEventListener("beforeunload", warn);
    };
  }, [recording]);
  // Signing out ends them, and saves what they have.
  useEffect(() => {
    const all = stops.current;
    return () => [...all.values()].forEach((s) => s());
  }, []);

  return (
    <Ctx.Provider value={{ list, now, start, stop }}>
      {children}
      <RecordingBar list={list} now={now} stop={stop} />
    </Ctx.Provider>
  );
}

/** What is being recorded, on every page, with its Stop. */
function RecordingBar({ list, now, stop }: { list: Recording[]; now: number; stop: (key: string) => void }) {
  if (!list.length) return null;
  return (
    <div className="fixed right-4 bottom-4 z-50 flex flex-col items-end gap-2" role="status" aria-label="Recordings">
      {list.map((r) => (
        <div
          key={r.key}
          className="flex items-center gap-2 rounded-full border border-destructive/40 bg-background/95 py-1 pr-1 pl-3 text-xs shadow-lg backdrop-blur"
        >
          <span className="size-2 animate-pulse rounded-full bg-destructive" />
          <span className="max-w-48 truncate" title={`${r.sandbox}, port ${r.port}`}>
            Recording <span className="font-mono">{r.sandbox}</span>
          </span>
          {r.since === null ? (
            <span className="text-muted-foreground">starting…</span>
          ) : (
            <span className="font-mono tabular-nums">{clock(Math.max(0, now - r.since))}</span>
          )}
          <Button variant="destructive" size="sm" className="h-6 gap-1 rounded-full px-2 text-xs" onClick={() => stop(r.key)}>
            <Square className="size-2.5 fill-current" />
            Stop
          </Button>
        </div>
      ))}
    </div>
  );
}
