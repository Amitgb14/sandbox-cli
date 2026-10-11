"use client";

import { useEffect, useState } from "react";
import { formatDateTime, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Sandbox } from "@/lib/types";

/**
 * Time left before a sandbox's lifetime ends it, busy or not. It ticks: the
 * last minutes are what someone is watching for.
 */
export function Countdown({ at, className }: { at: string; className?: string }) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const left = new Date(at).getTime() - now;
  return (
    <span title={formatDateTime(at)} className={cn("tabular-nums", left < 5 * 60_000 && "text-caution", className)}>
      {left > 0 ? `in ${formatDuration(left)}` : "now"}
    </span>
  );
}

/**
 * Who a gateway's sandbox belongs to, from the labels the gateway stamps on
 * every sandbox it makes (a request cannot set them): "user", or
 * "user@tenant" when the tenant is not the user's own name. Null for a
 * sandbox not made through a gateway.
 */
export function ownerOf(sb: Sandbox): string | null {
  const user = sb.labels?.["gateway.owner"];
  if (!user) return null;
  const tenant = sb.labels?.["gateway.tenant"];
  return tenant && tenant !== user ? `${user}@${tenant}` : user;
}
