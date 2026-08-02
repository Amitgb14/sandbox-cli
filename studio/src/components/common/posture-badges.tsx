import { Globe, Layers, Lock, ShieldCheck, Unplug, Users } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type { NetworkPosture, Profile, RunKind } from "@/lib/types";

/**
 * The badges that describe a run's *posture* rather than its outcome.
 *
 * Each one has a tooltip that says what it costs or protects, because the whole
 * argument of the tool is in these three facts and a coloured pill on its own
 * teaches nobody anything.
 */

export function ProfileBadge({ profile, className }: { profile: Profile; className?: string }) {
  const prod = profile === "prod";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant="outline"
          className={cn(
            "gap-1 font-mono text-[11px]",
            prod
              ? "border-contained/40 bg-contained/10 text-contained"
              : "border-border bg-muted/40 text-muted-foreground",
            className,
          )}
        >
          {prod ? <ShieldCheck className="size-3" /> : <Layers className="size-3" />}
          {profile}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        {prod
          ? "prod refuses when a control cannot be satisfied, and does not mount the persisted agent HOME — so there is no refresh token to steal."
          : "dev warns when a control cannot be satisfied. Both profiles are secure; neither relaxes the host boundary."}
      </TooltipContent>
    </Tooltip>
  );
}

export function NetworkBadge({
  network,
  className,
}: {
  network: NetworkPosture;
  className?: string;
}) {
  if (network.mode === "none") {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge
            variant="outline"
            className={cn(
              "gap-1 border-contained/40 bg-contained/10 text-contained font-mono text-[11px]",
              className,
            )}
          >
            <Unplug className="size-3" />
            none
          </Badge>
        </TooltipTrigger>
        <TooltipContent>Asked to reach nothing. `--network none`.</TooltipContent>
      </Tooltip>
    );
  }

  if (network.mode === "default") {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge
            variant="outline"
            className={cn(
              "gap-1 border-exposed/40 bg-exposed/10 text-exposed font-mono text-[11px]",
              className,
            )}
          >
            <Globe className="size-3" />
            open
          </Badge>
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">
          Unrestricted egress. Any token this agent holds can leave.
        </TooltipContent>
      </Tooltip>
    );
  }

  const byName = network.enforcement === "name";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant="outline"
          className={cn(
            "gap-1 font-mono text-[11px]",
            byName
              ? "border-contained/40 bg-contained/10 text-contained"
              : "border-caution/40 bg-caution/10 text-caution",
            className,
          )}
        >
          <Lock className="size-3" />
          {network.allow.length}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-sm">
        <p className="font-medium">
          Allowlist · {network.allow.length} domains ·{" "}
          {byName ? "decided by hostname" : "matched on resolved address"}
        </p>
        <p className="mt-1 text-xs opacity-80">
          {byName
            ? "The in-container proxy resolves fresh per connection and decides on the name, so a host sharing an allowlisted address does not ride in on it."
            : "Address matching was requested: a host sharing an allowlisted IP is reachable, and names resolved once at start can rotate away mid-session."}
        </p>
        {network.baseline && (
          <p className="mt-1 text-xs opacity-80">
            The built-in baseline is on, which includes github.com — a write endpoint.
          </p>
        )}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * `sandbox.fleet`. This distinction is load-bearing everywhere else in the CLI,
 * and the listing was the one place it used to be invisible — which is exactly
 * where somebody decides what to kill.
 */
export function KindBadge({ kind, className }: { kind: RunKind; className?: string }) {
  const fleet = kind === "fleet";
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant="outline"
          className={cn(
            "gap-1 font-mono text-[11px] text-muted-foreground",
            fleet && "border-chart-7/40 bg-chart-7/10 text-chart-7",
            className,
          )}
        >
          {fleet ? <Users className="size-3" /> : <Layers className="size-3" />}
          {fleet ? "fleet" : "session"}
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        {fleet
          ? "Launched by `fleet run`. `fleet stop --all` reaches it and max_parallel counts it."
          : "An interactive detached session. Fleet commands do not reach it — one open session would otherwise block a max_parallel: 1 fleet forever."}
      </TooltipContent>
    </Tooltip>
  );
}
