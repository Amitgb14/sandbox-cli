"use client";

import { useEffect, useState } from "react";
import { KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  apiToken,
  isAuthRejected,
  onAuthChange,
  setApiToken,
} from "@/lib/api/client";
import { useDaemon } from "@/lib/api/queries";

/**
 * Asks for the daemon's bearer token when Studio needs one and has not got a
 * working one.
 *
 * Two states reach here and they used to be one. **Missing** is the easy case:
 * a daemon started with `-token` rejects everything but /v1/health, so without
 * this bar the app decides it is live (health answers, deliberately) and then
 * every panel fails with a 401 it cannot explain. **Wrong** is the case that
 * was broken — the bar only appeared when *nothing* was stored, so a stale or
 * mistyped value hid it, every request 401'd, and the interface offered no way
 * left to correct the value. That is a dead end you can only leave through
 * devtools.
 *
 * So the condition is "the daemon wants a token and ours is not working",
 * which covers both.
 *
 * A bar rather than a modal: the parts of Studio that do not need the daemon
 * still work, and blocking the screen to demand a credential is a worse trade
 * than saying what is missing while you decide.
 */
export function TokenBar() {
  const { data: daemon } = useDaemon();
  const [value, setValue] = useState("");

  // Subscribed *and* read on mount. Subscribing alone was not enough: the
  // rejection latches when the page's first queries come back, which can land
  // before this has anything registered — and with nothing else forcing a
  // re-render the component kept its initial "fine" snapshot and stayed hidden
  // over a screen full of 401s.
  const [rejected, setRejected] = useState(false);
  useEffect(() => {
    setRejected(isAuthRejected());
    return onAuthChange(() => setRejected(isAuthRejected()));
  }, []);

  // localStorage is not readable during a server render, so this is resolved
  // after mount. Without the guard the first client render disagrees with the
  // server's and React reports a hydration mismatch.
  const [held, setHeld] = useState<string | null>(null);
  useEffect(() => setHeld(apiToken()), []);

  if (!daemon?.authRequired) return null;
  if (held === null) return null; // not resolved yet
  if (held && !rejected) return null; // working fine

  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-caution/40 bg-caution/10 px-4 py-2 text-sm">
      <KeyRound className="size-4 shrink-0 text-caution" />
      <span className="text-caution">
        {rejected
          ? "The daemon refused this token. Paste the value it was started with."
          : "This daemon requires a token. Paste the value it was started with."}
      </span>
      <span className="text-xs text-muted-foreground">
        SANDBOX_STUDIO_TOKEN in your .env
      </span>
      <div className="ml-auto flex items-center gap-2">
        <Input
          type="password"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="bearer token"
          className="h-8 w-56 font-mono"
          onKeyDown={(e) => {
            if (e.key === "Enter") save();
          }}
        />
        <Button size="sm" disabled={!value.trim()} onClick={save}>
          Save
        </Button>
        {rejected && held && (
          <Button
            variant="ghost"
            size="sm"
            onClick={() => {
              setApiToken("");
              window.location.reload();
            }}
          >
            Clear
          </Button>
        )}
      </div>
    </div>
  );

  function save() {
    setApiToken(value.trim());
    // Reload rather than refetch: the token is read per request by every hook,
    // and the queries that already failed are cached as errors. Starting again
    // is the one move that cannot leave half the screen stale.
    window.location.reload();
  }
}
