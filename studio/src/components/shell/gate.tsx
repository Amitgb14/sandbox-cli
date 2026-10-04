"use client";

import Link from "next/link";
import { Ban } from "lucide-react";
import { Button } from "@/components/ui/button";
import { allowed, type NavItem } from "@/lib/nav";
import { useCaller } from "@/lib/caller";

/**
 * A screen only some callers have: the gateway's tenant screens, or the admin
 * ones. Its children — and so every request they make — are not mounted until
 * whoami has said the caller may see it; a caller who may not gets a plain
 * page saying so, reached by URL or not. The gateway's 403 is still the
 * control; this keeps Studio from asking for what it will be refused.
 */
export function Gate({ need, scope, children }: Pick<NavItem, "need" | "scope"> & { children: React.ReactNode }) {
  const caller = useCaller();
  if (caller.kind === "loading") return <p className="text-sm text-muted-foreground">Loading…</p>;
  if (!allowed({ need, scope }, caller)) return <NotAvailable />;
  return <>{children}</>;
}

export function NotAvailable() {
  return (
    <div className="flex flex-col items-center gap-4 py-24 text-center">
      <div className="flex size-11 items-center justify-center rounded-full bg-muted">
        <Ban className="size-5 text-muted-foreground" aria-hidden />
      </div>
      <div className="space-y-1.5">
        <h1 className="text-lg font-semibold">Not available</h1>
        <p className="max-w-md text-sm text-muted-foreground">
          This screen is not available here: it needs a gateway, or a key with more scopes than this one.
        </p>
      </div>
      <Button asChild size="sm" variant="outline">
        <Link href="/">Back to sandboxes</Link>
      </Button>
    </div>
  );
}
