"use client";

import { useEffect, useRef, useState } from "react";
import { Unplug } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api/endpoints";
import { streamConsole } from "@/lib/api/client";
import type { Run } from "@/lib/types";
import "@xterm/xterm/css/xterm.css";

/**
 * A real terminal, attached to a running agent.
 *
 * This is what `sandbox-cli attach` gives a terminal, in a browser: the agent's
 * own full-screen UI, keystroke for keystroke. The conversation view next to it
 * answers a different question — *what was said* — and is the better place to
 * read a session. This one is for driving it: approving a plan, interrupting
 * with Ctrl-C, arrowing through a menu the agent drew.
 *
 * It *does* tell the container its size, and that is not cosmetic: a
 * full-screen agent renders nothing until it knows one. An earlier version of
 * this skipped the resize on the reasoning that the agent had drawn its layout
 * for the pty it was given — and produced a blank rectangle over a healthy run.
 * The container had written zero bytes in ten minutes and painted its whole
 * interface within a second of the first resize. `docker attach` sends one from
 * the client terminal's dimensions, which is why attaching from a real terminal
 * always worked.
 *
 * Two things it does not do, each for a reason:
 *
 *   - It does not reconnect on its own. An attach is a deliberate act, and a
 *     terminal that silently reattached after a container died would show a
 *     cursor blinking at nothing.
 *   - It does not buffer scrollback across mounts. Detaching and attaching
 *     again gives you what the agent paints next, which is what attaching to a
 *     live pty does everywhere else.
 */
export function AttachedTerminal({
  run,
  onDetach,
}: {
  run: Run;
  onDetach: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;

    const controller = new AbortController();
    let disposed = false;
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    let term: any;

    // Loaded on demand rather than imported at the top: xterm touches `window`
    // at module scope, and this page is server-rendered. It is also the largest
    // dependency in the app, and every other screen can do without it.
    (async () => {
      const { Terminal } = await import("@xterm/xterm");
      const { FitAddon } = await import("@xterm/addon-fit");
      if (disposed) return;

      term = new Terminal({
        convertEol: false,
        cursorBlink: true,
        fontSize: 12,
        fontFamily:
          'ui-monospace, SFMono-Regular, Menlo, Monaco, "Cascadia Mono", monospace',
        theme: { background: "#0b0b0c" },
        // The agent redraws for the size we send it, so this is only for
        // output that has already scrolled past.
        scrollback: 5000,
      });
      const fit = new FitAddon();
      term.loadAddon(fit);
      term.open(host);
      fit.fit();
      term.focus();

      const pushSize = () => {
        fit.fit();
        api.resizeConsole(run.id, term.rows, term.cols).catch(() => {
          // Best-effort: a terminal drawn for the wrong width is worse than
          // right and much better than the blank screen without it.
        });
      };

      /**
       * Provoke a repaint by changing the size and changing it back.
       *
       * A resize only reaches the agent as SIGWINCH when the dimensions
       * actually *differ*, so attaching a second time at the same size sends a
       * resize that changes nothing, the agent has no reason to redraw, and the
       * terminal sits empty over a perfectly healthy run. Observed exactly
       * that: the first attach painted the full UI, the second painted nothing.
       *
       * One column narrower and back is the smallest change that guarantees the
       * signal. The agent redraws twice in quick succession, which nobody sees.
       */
      const provokeRepaint = async () => {
        fit.fit();
        const { rows, cols } = term;
        if (cols > 2) {
          await api.resizeConsole(run.id, rows, cols - 1).catch(() => {});
        }
        await api.resizeConsole(run.id, rows, cols).catch(() => {});
      };

      const onResize = () => pushSize();
      window.addEventListener("resize", onResize);

      /**
       * Keystrokes, serialized and coalesced.
       *
       * Every key was its own POST at first, and they raced: "What is 12 times
       * 12?" reached the agent as "rtWha is21 t ime1 2s?", because concurrent
       * requests arrive at the container's stdin in whatever order they finish.
       * A terminal is a byte stream and order is the whole contract.
       *
       * So keys accumulate in a buffer and one in-flight request drains it. The
       * queue is a promise chain rather than a boolean flag — that is what makes
       * the next flush *wait* for the previous one instead of skipping. Typing
       * fast now sends fewer, larger writes, which is also cheaper.
       */
      let outbox = "";
      let flushing: Promise<void> = Promise.resolve();
      const flush = () => {
        flushing = flushing.then(async () => {
          if (!outbox) return;
          const data = outbox;
          outbox = "";
          try {
            await api.sendConsoleInput(run.id, data, false);
          } catch (e: unknown) {
            setError(e instanceof Error ? e.message : "Could not send that.");
          }
        });
      };
      // xterm already hands us the bytes a terminal sends, carriage return
      // included, so `enter` stays false — adding one would double it.
      const typing = term.onData((data: string) => {
        outbox += data;
        flush();
      });

      // After the stream below is being read, so the repaint this causes is not
      // missed. setTimeout rather than await, because that loop never returns
      // while the run is alive.
      setTimeout(() => void provokeRepaint(), 250);

      try {
        for await (const chunk of streamConsole(
          `/v1/runs/${run.id}/console`,
          controller.signal,
        )) {
          if (disposed) break;
          term.write(chunk);
        }
        if (!disposed) {
          term.write("\r\n\x1b[2m— the stream ended; the run has finished —\x1b[0m\r\n");
        }
      } catch (e) {
        if (!disposed && !controller.signal.aborted) {
          setError(e instanceof Error ? e.message : "The stream failed.");
        }
      } finally {
        window.removeEventListener("resize", onResize);
        typing.dispose();
      }
    })();

    return () => {
      disposed = true;
      controller.abort();
      term?.dispose();
    };
  }, [run.id]);

  return (
    <div className="space-y-2">
      <div className="flex items-center gap-2">
        <p className="text-xs text-muted-foreground">
          Attached to {run.name}. Everything you type goes to the agent —
          including Ctrl-C, which interrupts it rather than closing this.
        </p>
        <Button
          variant="outline"
          size="sm"
          className="ml-auto"
          onClick={onDetach}
        >
          <Unplug className="size-4" />
          Detach
        </Button>
      </div>
      {error && <p className="text-xs text-destructive">{error}</p>}
      <div
        ref={hostRef}
        className="h-[60vh] overflow-hidden rounded-lg border bg-[#0b0b0c] p-2"
      />
    </div>
  );
}
