"use client";

import { useState } from "react";
import { KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { apiToken, setApiToken } from "@/lib/api/client";
import { useDaemon } from "@/lib/api/queries";

/**
 * Shown when the daemon requires a bearer token and this browser has not got
 * one.
 *
 * Without it, a daemon started with `-token` looks like a broken Studio: the
 * health probe answers (it is the one unauthenticated endpoint, deliberately)
 * so the app decides it is live, and then every panel fails with a 401 it has
 * no way to explain. Reporting *that* a token is needed is the only thing a
 * client without one can do, which is why /v1/health carries the flag.
 *
 * A bar rather than a modal: the parts of Studio that do not need the daemon
 * still work, and blocking the whole screen to demand a credential is a worse
 * trade than saying what is missing while you decide.
 */
export function TokenBar() {
  const { data: daemon } = useDaemon();
  const [value, setValue] = useState("");

  // apiToken() reads localStorage, so this only renders after hydration —
  // which is correct here: the server has no idea what this browser holds, and
  // rendering the bar on the server would flash it for everyone.
  if (!daemon?.authRequired || apiToken()) return null;

  return (
    <div className="flex flex-wrap items-center gap-3 border-b border-caution/40 bg-caution/10 px-4 py-2 text-sm">
      <KeyRound className="size-4 shrink-0 text-caution" />
      <span className="text-caution">
        This daemon requires a token. Paste the value it was started with
        (&lsquo;-token&rsquo;, or <code>$SANDBOX_STUDIO_TOKEN</code>).
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
