"use client";

import { useEffect, useState } from "react";
import { KeyRound } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError, SESSION_ENDED, setToken } from "@/lib/api/client";
import { useQueryClient } from "@tanstack/react-query";
import { useInfo } from "@/lib/api/queries";
import { SessionEnded } from "@/components/shell/hosted-session";

/**
 * Shown when Studio refuses this page's token — usually because the page was
 * opened without the address `sandbox-cli studio` printed, or Studio was
 * restarted, which makes a new token. Without it every request fails, and a
 * screen of empty tables would not say why.
 */
export function TokenGate() {
  const { error } = useInfo();
  const ended = useSessionEnded();
  // Hosted, there is no token to paste: the session is the invite link's.
  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" && ended) return <SessionEnded />;
  if (!(error instanceof ApiError) || error.status !== 401) return null;
  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on") return <SessionEnded />;
  return <PasteToken />;
}

/** Whether any request has been told this hosted session is over. */
function useSessionEnded(): boolean {
  const [ended, setEnded] = useState(false);
  useEffect(() => {
    if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return;
    const on = () => setEnded(true);
    window.addEventListener(SESSION_ENDED, on);
    return () => window.removeEventListener(SESSION_ENDED, on);
  }, []);
  return ended;
}

function PasteToken() {
  const qc = useQueryClient();
  const [value, setValue] = useState("");
  return (
    <div className="mx-4 mt-4 flex flex-col gap-3 rounded-lg border border-caution/40 bg-caution/10 p-4 md:mx-8">
      <div className="flex items-center gap-2 text-sm font-medium">
        <KeyRound className="size-4" /> Studio needs its token
      </div>
      <p className="text-sm text-muted-foreground">
        Open the address <code className="font-mono">sandbox-cli studio</code> printed — it carries
        a token made for that launch — or paste the token or the whole address here.
      </p>
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          const m = value.match(/token=([0-9a-f]+)/) ?? value.match(/^([0-9a-f]+)$/);
          if (m) {
            setToken(m[1]);
            setValue("");
            // Every query, whoami included: what failed without the token is retried with it.
            qc.invalidateQueries();
          }
        }}
      >
        <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="http://127.0.0.1:7080/#token=…" className="font-mono" />
        <Button type="submit">Use</Button>
      </form>
    </div>
  );
}
