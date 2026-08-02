"use client";

import { useEffect, useRef, useState } from "react";
import { Unplug } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api } from "@/lib/api/endpoints";
import { streamConsole } from "@/lib/api/client";
import { useConversation } from "@/lib/api/queries";
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
/**
 * Whether a chunk from the terminal is a mouse report rather than typing.
 *
 * Claude Code turns on any-event tracking (`?1003h`), so with mouse reporting
 * forwarded, *moving the pointer across the terminal* sends an escape sequence
 * per motion — one HTTP POST each — and a click lands inside the agent's UI as
 * a click. That is faithful to what a terminal does, and it was worse than
 * useless here: a single click to focus the panel left the agent unable to
 * accept the next Enter, so a typed question sat in its input box forever.
 * Measured — the same question submitted fine when nothing was clicked.
 *
 * So mouse goes no further than the browser. What is lost is clicking inside
 * the agent's own interface; what is kept is that typing works and idle mouse
 * movement does not talk to the daemon at all. Both SGR (`ESC[<...M/m`) and the
 * legacy X10 form (`ESC[M` plus three bytes) are recognised.
 */
function isMouseReport(data: string): boolean {
  const esc = "\u001b[";
  if (data.startsWith(`${esc}<`) && /[Mm]$/.test(data)) return true;
  return data.startsWith(`${esc}M`) && data.length === 6;
}

export function AttachedTerminal({
  run,
  onDetach,
}: {
  run: Run;
  onDetach: () => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [error, setError] = useState<string | null>(null);

  // Read once and held in a ref: the effect below runs for the life of the
  // attachment and must not be torn down and rebuilt because a query resolved.
  const { data: conversation } = useConversation(run.id, false);
  const resumeRef = useRef<string | undefined>(undefined);
  resumeRef.current = conversation?.resume;

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

      /**
       * Resize failures are reported, not swallowed.
       *
       * They were swallowed at first, on "best-effort" reasoning that a
       * terminal drawn for the wrong width beats none. That was wrong in the
       * one case that matters: resize is what makes the agent paint at all, so
       * a daemon started without -token answered 403 here and the result was a
       * blank rectangle with no explanation anywhere on screen. An error that
       * decides whether anything is visible is not best-effort.
       */
      const pushSize = () => {
        fit.fit();
        api.resizeConsole(run.id, term.rows, term.cols).catch((e: unknown) => {
          setError(e instanceof Error ? e.message : "Could not size the terminal.");
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
        try {
          if (cols > 2) await api.resizeConsole(run.id, rows, cols - 1);
          await api.resizeConsole(run.id, rows, cols);
        } catch (e: unknown) {
          // Same reasoning as pushSize: if this fails the screen stays empty,
          // so saying why is the difference between a bug report and a fix.
          setError(e instanceof Error ? e.message : "Could not size the terminal.");
        }
      };

      /**
       * Refit when the panel's own box changes, not when the window does.
       *
       * The tab this lives in stays mounted and is hidden when another tab is
       * showing, so a window resize can fire while this has *no size at all* —
       * and fitting a zero-height element yields a 1x1 terminal, which would
       * then be sent to the container as its new dimensions and wreck the
       * agent's layout. A ResizeObserver answers the right question: it fires
       * when this element is laid out again, and the guard drops the readings
       * taken while it is hidden.
       */
      let wasVisible = true;
      const observer = new ResizeObserver(() => {
        const visible = host.clientWidth > 0 && host.clientHeight > 0;
        if (!visible) {
          wasVisible = false;
          return;
        }
        pushSize();
        if (!wasVisible) {
          // Coming back from another tab. The keyboard has to follow, or the
          // terminal is on screen and quietly ignoring everything typed at it —
          // which reads exactly like the detach this mounting was meant to stop.
          term.focus();
        }
        wasVisible = true;
      });
      observer.observe(host);

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
        if (isMouseReport(data)) return;
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
          // The run is over and the container will be reaped, but the
          // conversation is not lost — so this ends with the way back into it
          // rather than just an obituary. Printed into the terminal itself
          // because that is where somebody is looking when it stops.
          term.write("\r\n\x1b[2m— the stream ended; the run has finished —\x1b[0m\r\n");
          if (resumeRef.current) {
            term.write(`\x1b[2m  carry it on:\x1b[0m ${resumeRef.current}\r\n`);
          }
        }
      } catch (e) {
        if (!disposed && !controller.signal.aborted) {
          setError(e instanceof Error ? e.message : "The stream failed.");
        }
      } finally {
        observer.disconnect();
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
