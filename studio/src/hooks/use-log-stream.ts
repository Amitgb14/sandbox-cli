"use client";

import { useEffect, useRef, useState } from "react";
import type { LogLine } from "@/lib/types";

/**
 * Reveals a live run's output as it arrives.
 *
 * Against the daemon this will be an NDJSON stream (`streamNdjson`). Against
 * fixtures it replays the transcript on a timer, which is what makes the live
 * terminal reviewable without a backend — and the reveal is *append-only*, the
 * same shape a real stream has, so nothing in the view can come to depend on
 * having the whole transcript up front.
 *
 * A finished run is not streamed: its output is complete, and animating it would
 * imply something is still happening.
 */
export function useLogStream(lines: LogLine[] | undefined, live: boolean) {
  const [revealed, setRevealed] = useState<LogLine[]>([]);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    if (!lines) {
      setRevealed([]);
      return;
    }
    if (!live) {
      setRevealed(lines);
      return;
    }

    // Start partway in: a live run has already been going for a while, and
    // beginning from line zero would misrepresent when it started.
    let i = Math.max(1, Math.floor(lines.length * 0.55));
    setRevealed(lines.slice(0, i));

    function tick() {
      if (i >= lines!.length) return;
      const line = lines![i];
      i += 1;
      setRevealed((prev) => [...prev, line]);
      // Uneven cadence, because an agent's output is uneven: a burst of tool
      // calls, then a pause while it thinks.
      const gap = line.text.trim() === "" ? 120 : 420 + (i % 5) * 260;
      timer.current = setTimeout(tick, gap);
    }

    timer.current = setTimeout(tick, 700);
    return () => {
      if (timer.current) clearTimeout(timer.current);
    };
  }, [lines, live]);

  return {
    lines: revealed,
    /** True while more output is still expected. */
    streaming: live && !!lines && revealed.length < lines.length,
  };
}
