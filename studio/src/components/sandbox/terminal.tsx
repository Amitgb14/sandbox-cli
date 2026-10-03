"use client";

import { useEffect, useRef, useState } from "react";
import { attachURL } from "@/lib/api/client";
import "@xterm/xterm/css/xterm.css";

/**
 * A real terminal on a process inside a sandbox, through Studio's WebSocket
 * bridge to the API's attach stream.
 *
 * Three things here were learned by measurement in beta.15 and carry over:
 *
 *   - A full-screen agent renders nothing until it is told its terminal's
 *     size, and SIGWINCH only fires on a *change* — so after attaching, the
 *     size is sent one column narrower and then right, which makes it repaint.
 *   - Mouse-reporting modes are swallowed: an agent that turns them on would
 *     otherwise receive a report for every mouse move over the page.
 *   - Ctrl+Shift+C copies the selection rather than sending ^C.
 *
 * Over a WebSocket, keystrokes arrive in order by construction — the reason
 * beta.15 had to serialize its per-keystroke POSTs is gone with them.
 */
function isMouseReport(data: string): boolean {
  const esc = "\u001b[";
  if (data.startsWith(`${esc}<`) && /[Mm]$/.test(data)) return true;
  return data.startsWith(`${esc}M`) && data.length === 6;
}

export function SandboxTerminal({ sandbox, pid }: { sandbox: string; pid: number }) {
  const host = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<string>("connecting…");

  useEffect(() => {
    const el = host.current;
    if (!el) return;
    let disposed = false;
    let cleanup = () => {};

    (async () => {
      const { Terminal } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");
      if (disposed) return;
      const term = new Terminal({
        cursorBlink: true,
        fontSize: 12,
        fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, "Cascadia Mono", monospace',
        theme: { background: "#0b0b0c" },
        scrollback: 5000,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      const MOUSE_MODES = new Set([9, 1000, 1001, 1002, 1003, 1005, 1006, 1015, 1016]);
      const swallow = (params: (number | number[])[]) =>
        params.map((p) => (Array.isArray(p) ? p[0] : p)).some((p) => MOUSE_MODES.has(p));
      term.parser.registerCsiHandler({ prefix: "?", final: "h" }, swallow);
      term.parser.registerCsiHandler({ prefix: "?", final: "l" }, swallow);
      term.attachCustomKeyEventHandler((e: KeyboardEvent) => {
        if (e.type === "keydown" && e.ctrlKey && e.shiftKey && e.code === "KeyC") {
          const sel = term.getSelection();
          if (sel) {
            void navigator.clipboard.writeText(sel);
            return false;
          }
        }
        return true;
      });
      term.open(el);
      fit.fit();
      term.focus();

      const ws = new WebSocket(attachURL(sandbox, pid));
      const send = (m: object) => ws.readyState === WebSocket.OPEN && ws.send(JSON.stringify(m));
      const resize = (rows: number, cols: number) => send({ type: "resize", rows, cols });
      ws.onopen = () => {
        setStatus("attached — closing this tab leaves the process running");
        fit.fit();
        const { rows, cols } = term;
        if (cols > 2) resize(rows, cols - 1);
        setTimeout(() => resize(term.rows, term.cols), 120);
      };
      ws.onmessage = (ev) => {
        const m = JSON.parse(ev.data as string);
        if (m.type === "output") term.write(m.data);
        if (m.type === "exit") {
          term.write(`\r\n\x1b[2m— the process exited ${m.code} —\x1b[0m\r\n`);
          setStatus(`exited ${m.code}`);
        }
      };
      ws.onclose = () => setStatus((s) => (s.startsWith("exited") ? s : "detached"));
      ws.onerror = () => setStatus("could not attach — is the process still running?");
      const typing = term.onData((data: string) => {
        if (!isMouseReport(data)) send({ type: "input", data });
      });
      const observer = new ResizeObserver(() => {
        if (el.clientWidth === 0 || el.clientHeight === 0) return;
        fit.fit();
        resize(term.rows, term.cols);
      });
      observer.observe(el);
      cleanup = () => {
        observer.disconnect();
        typing.dispose();
        ws.close();
        term.dispose();
      };
    })();

    return () => {
      disposed = true;
      cleanup();
    };
  }, [sandbox, pid]);

  return (
    <div className="flex flex-col gap-2">
      <div ref={host} className="h-[28rem] overflow-hidden rounded-md border bg-[#0b0b0c] p-2" />
      <p className="text-xs text-muted-foreground">{status}</p>
    </div>
  );
}
