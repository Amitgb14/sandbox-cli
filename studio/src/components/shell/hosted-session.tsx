"use client";

import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, LogOut } from "lucide-react";
import { toast } from "sonner";
import { SidebarMenu, SidebarMenuButton, SidebarMenuItem } from "@/components/ui/sidebar";
import { signIn, signOut, takeInviteKey } from "@/lib/api/client";

/**
 * A hosted Studio's sign-in: an invite link carries the user's gateway key
 * in the fragment, and it is exchanged for a session cookie before anything
 * else asks the server for data — so no screen fetches without a session
 * and then flashes "session ended" while the sign-in is still on its way.
 *
 * In a local build this renders its children and nothing else, and the rest
 * is removed by the minifier (the comparison is with the literal).
 */
export function HostedSignIn({ children }: { children: React.ReactNode }) {
  // "checking" until the effect has looked at the address: the static
  // export is rendered without one, and must match on first paint.
  const [state, setState] = useState<"checking" | "ready" | { error: string }>("checking");
  useEffect(() => {
    if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return;
    const check = (first: boolean) => {
      const key = takeInviteKey();
      if (key === null) {
        if (first) setState("ready");
        return;
      }
      if (key === "") {
        setState({ error: "This link does not carry a key. Copy the whole invite link, including everything after #." });
        return;
      }
      setState("checking");
      signIn(key)
        .then(() => {
          // A new session, perhaps another user's: nothing of the last stays.
          window.location.replace("/");
        })
        .catch((e: Error) => setState({ error: e.message }));
    };
    check(true);
    // An invite link opened in a tab already on Studio changes only the
    // fragment, which does not load the page again.
    const onHash = () => check(false);
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return <>{children}</>;
  if (state === "checking") return null;
  if (state !== "ready") {
    return (
      <div className="flex min-h-svh items-center justify-center p-6">
        <SessionNotice title="This invite link did not sign you in" detail={state.error} />
      </div>
    );
  }
  return <>{children}</>;
}

/** What a hosted Studio shows when the server has no session for this browser. */
export function SessionEnded() {
  return (
    <div className="mx-4 mt-4 md:mx-8">
      <SessionNotice
        title="Your session has ended"
        detail="Open your invite link again to continue. If you no longer have it, or it stopped working, ask whoever invited you for a new one."
      />
    </div>
  );
}

function SessionNotice({ title, detail }: { title: string; detail: string }) {
  return (
    <div className="flex max-w-xl flex-col gap-2 rounded-lg border border-caution/40 bg-caution/10 p-4">
      <div className="flex items-center gap-2 text-sm font-medium">
        <KeyRound className="size-4" aria-hidden /> {title}
      </div>
      <p className="text-sm text-muted-foreground">{detail}</p>
    </div>
  );
}

/** Ends this browser's session; the page then says so. Hosted builds only. */
export function SignOutButton() {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  if (process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on") return null;
  return (
    <SidebarMenu>
      <SidebarMenuItem>
    <SidebarMenuButton
      disabled={busy}
      onClick={() => {
        setBusy(true);
        signOut()
          .catch((e: Error) => toast.error(e.message))
          .finally(() => {
            setBusy(false);
            // Every query again, info included, which now answers 401 and
            // puts the session-ended notice up.
            qc.invalidateQueries();
          });
      }}
    >
      <LogOut aria-hidden />
      <span>Sign out</span>
    </SidebarMenuButton>
      </SidebarMenuItem>
    </SidebarMenu>
  );
}
